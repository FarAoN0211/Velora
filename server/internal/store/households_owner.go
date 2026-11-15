package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Fase 2 de replica/PLAN_HOGARES_Y_PIN.md: el hogar lo crea el titular de la cuenta (si el administrador le ha dado
// permiso) y gestiona sus miembros, con una papelera de 30 días.

// TrashDays es lo que un miembro eliminado puede recuperarse antes de borrarse definitivamente.
const TrashDays = 30

var (
	// ErrHouseholdNotAllowed: la cuenta no tiene (o ya no tiene) el permiso de hogar del administrador.
	ErrHouseholdNotAllowed = errors.New("sin permiso para crear o ampliar el hogar")
	// ErrAlreadyInHousehold: la cuenta ya tiene hogar o es miembro de uno.
	ErrAlreadyInHousehold = errors.New("la cuenta ya pertenece a un hogar")
	// ErrTrashExpired: el miembro lleva más de 30 días en la papelera (o ya se ha borrado).
	ErrTrashExpired = errors.New("ya no se puede recuperar")
)

// CreateOwnHousehold crea el hogar del titular: «Familia de <nombre>», con él como propietario. Sin configuraciones.
func (s *Store) CreateOwnHousehold(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var name string
	var allowed, disabled bool
	var current *uuid.UUID
	var deleted *time.Time
	err = tx.QueryRow(ctx, `select display_name, can_create_household, is_disabled, household_id, deleted_at from users where id = $1 for update`,
		userID).Scan(&name, &allowed, &disabled, &current, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || disabled || deleted != nil {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if current != nil {
		return uuid.Nil, ErrAlreadyInHousehold
	}
	if !allowed {
		return uuid.Nil, ErrHouseholdNotAllowed
	}
	var hid uuid.UUID
	if err = tx.QueryRow(ctx, `insert into households(owner_id, name) values ($1, $2) returning id`, userID, "Familia de "+name).Scan(&hid); err != nil {
		if isUnique(err) {
			return uuid.Nil, ErrAlreadyInHousehold
		}
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `update users set household_id = $2, household_owner = true where id = $1`, userID, hid); err != nil {
		return uuid.Nil, err
	}
	return hid, tx.Commit(ctx)
}

// HouseholdName devuelve el nombre del hogar.
func (s *Store) HouseholdName(ctx context.Context, hid uuid.UUID) (string, error) {
	var name string
	err := s.Pool.QueryRow(ctx, `select name from households where id = $1`, hid).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// RenameHousehold cambia el nombre del hogar (solo su propietario).
func (s *Store) RenameHousehold(ctx context.Context, owner uuid.UUID, name string) error {
	tag, err := s.Pool.Exec(ctx, `update households set name = $2 where owner_id = $1`, owner, name)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ownerMember comprueba, dentro de tx, que id es un miembro (no el propietario) del hogar de owner, y lo bloquea.
func ownerMember(ctx context.Context, tx pgx.Tx, owner, id uuid.UUID) (deleted *time.Time, err error) {
	err = tx.QueryRow(ctx, `select m.deleted_at from users m join users o on o.household_id = m.household_id
		where m.id = $2 and o.id = $1 and o.household_owner and not o.is_disabled and not m.household_owner and not m.is_admin
		for update of m`, owner, id).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return deleted, err
}

// RenameFamilyProfile cambia el nombre visible de un miembro (no en la papelera).
func (s *Store) RenameFamilyProfile(ctx context.Context, owner, id uuid.UUID, name string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	deleted, err := ownerMember(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if deleted != nil {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `update users set display_name = $2 where id = $1`, id, name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TrashFamilyProfile lleva un miembro a la papelera: deja de poder usarse al instante (sesiones revocadas, sin
// reproducciones, fuera de «Cambiar usuario») y no cuenta en el límite. Su historial queda intacto e inaccesible.
func (s *Store) TrashFamilyProfile(ctx context.Context, owner, id uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	deleted, err := ownerMember(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if deleted != nil {
		return nil // ya estaba en la papelera
	}
	if _, err = tx.Exec(ctx, `update users set deleted_at = now() where id = $1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `delete from playback_leases where session_id in (select id from sessions where user_id = $1)`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `update sessions set revoked_at = now() where user_id = $1 and revoked_at is null`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RestoreFamilyProfile saca un miembro de la papelera, con su historial, si no han pasado 30 días, el propietario
// conserva el permiso de hogar y hay un hueco libre (recuperar nunca sirve para superar el límite).
func (s *Store) RestoreFamilyProfile(ctx context.Context, owner, id uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var allowed bool
	var hid uuid.UUID
	var limit int
	if err = tx.QueryRow(ctx, `select household_id, can_create_household, max_household_members from users
		where id = $1 and household_owner and not is_disabled for update`, owner).Scan(&hid, &allowed, &limit); err != nil {
		return ErrNotFound
	}
	deleted, err := ownerMember(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if deleted == nil {
		return nil // no estaba en la papelera
	}
	if time.Since(*deleted) > TrashDays*24*time.Hour {
		return ErrTrashExpired
	}
	if !allowed {
		return ErrHouseholdNotAllowed
	}
	var count int
	if err = tx.QueryRow(ctx, `select count(*) from users where household_id = $1 and not household_owner and deleted_at is null`, hid).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrRateLimited
	}
	if _, err = tx.Exec(ctx, `update users set deleted_at = null where id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PurgeFamilyProfile borra definitivamente un miembro que ya está en la papelera, con todo su historial.
func (s *Store) PurgeFamilyProfile(ctx context.Context, owner, id uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	deleted, err := ownerMember(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if deleted == nil {
		return ErrNotFound // primero a la papelera: nunca se borra de un golpe por error
	}
	if err = deleteUser(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PurgeExpiredMembers borra definitivamente los miembros con más de 30 días en la papelera (tarea nocturna).
func (s *Store) PurgeExpiredMembers(ctx context.Context) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select id from users where deleted_at < now() - make_interval(days => $1) for update`, TrashDays)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err = deleteUser(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), tx.Commit(ctx)
}

// deleteUser borra la fila del usuario. Historial, progreso, sesiones, códigos y sellos se van en cascada; las
// referencias que no la tienen se sueltan antes.
func deleteUser(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `update device_authorizations set approved_user_id = null where approved_user_id = $1`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from users where id = $1 and deleted_at is not null`, id)
	return err
}

// TrashedMember es un miembro en la papelera, para «Eliminados recientemente».
type TrashedMember struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
	DeletedAt   time.Time `json:"deleted_at"`
	PurgeAt     time.Time `json:"purge_at"`
}

// TrashedMembers lista los miembros de la papelera de un hogar, del más reciente al más antiguo.
func (s *Store) TrashedMembers(ctx context.Context, hid uuid.UUID) ([]TrashedMember, error) {
	rows, err := s.Pool.Query(ctx, `select id, display_name, deleted_at, deleted_at + make_interval(days => $2)
		from users where household_id = $1 and deleted_at is not null order by deleted_at desc`, hid, TrashDays)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TrashedMember, error) {
		var m TrashedMember
		err := r.Scan(&m.ID, &m.DisplayName, &m.DeletedAt, &m.PurgeAt)
		return m, err
	})
}

// HouseholdStreamLimit devuelve el límite propio de reproducciones del hogar (nil = el valor general).
func (s *Store) HouseholdStreamLimit(ctx context.Context, hid uuid.UUID) (*int, error) {
	var n *int
	err := s.Pool.QueryRow(ctx, `select max_streams from users where household_id = $1 and household_owner`, hid).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}
