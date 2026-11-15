package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UserSummary es un usuario en la administración, con las bibliotecas a las que tiene acceso.
type UserSummary struct {
	HouseholdID    *uuid.UUID  `json:"household_id"`
	HouseholdOwner bool        `json:"household_owner"`
	Passwordless   bool        `json:"passwordless"`
	HasPIN         bool        `json:"has_pin"`
	ID             uuid.UUID   `json:"id"`
	Username       string      `json:"username"`
	DisplayName    string      `json:"display_name"`
	IsAdmin        bool        `json:"is_admin"`
	IsDisabled     bool        `json:"is_disabled"`
	LastLoginAt    *time.Time  `json:"last_login_at"`
	Libraries      []uuid.UUID `json:"libraries"` // vacío para un administrador: ve todas
	// Límites para todo su hogar: reproducciones simultáneas (nil = el general) y miembros que puede crear.
	MaxStreams          *int `json:"max_streams"`
	MaxHouseholdMembers int  `json:"max_household_members"`
	HouseholdMembers    int  `json:"household_members"` // perfiles familiares creados, sin contar al responsable
	// Permiso concedido por el administrador para crear (y ampliar) su propio hogar.
	CanCreateHousehold bool `json:"can_create_household"`
	// Perfil familiar en la papelera (se elimina definitivamente a los 30 días).
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	// Intentos fallidos de PIN o código en 30 días: solo el número y la fecha, nunca el dispositivo ni el contenido.
	PINFailures    int        `json:"pin_failures"`
	PINFailureLast *time.Time `json:"pin_failure_last,omitempty"`
}

// Users lista los usuarios con sus permisos de biblioteca.
func (s *Store) Users(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.Pool.Query(ctx, `select u.id, u.username::text, u.display_name, u.is_admin, u.is_disabled, u.last_login_at,
		coalesce(array_agg(a.library_id order by l.sort_order) filter (where a.library_id is not null), '{}'),u.household_id,u.household_owner,u.passwordless,u.pin_hash is not null,
		u.max_streams, u.max_household_members, u.can_create_household,
		case when u.household_owner then (select count(*) from users m where m.household_id = u.household_id and not m.household_owner and m.deleted_at is null) else 0 end,
		u.deleted_at
		from users u left join library_access a on a.user_id = u.id and a.path_id is null
		left join libraries l on l.id = a.library_id
		group by u.id order by u.is_admin desc, u.display_name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (UserSummary, error) {
		var u UserSummary
		err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.IsDisabled, &u.LastLoginAt, &u.Libraries, &u.HouseholdID, &u.HouseholdOwner, &u.Passwordless, &u.HasPIN,
			&u.MaxStreams, &u.MaxHouseholdMembers, &u.CanCreateHousehold, &u.HouseholdMembers, &u.DeletedAt)
		return u, err
	})
}

// CreateUser crea un usuario normal SIN acceso a ninguna biblioteca: el administrador decide las concesiones.
func (s *Store) CreateUser(ctx context.Context, username, displayName, passwordHash string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `insert into users (username, display_name, password_hash) values ($1, $2, $3) returning id`,
		username, displayName, passwordHash).Scan(&id)
	if isUnique(err) {
		return id, ErrDuplicate
	}
	return id, err
}

// SetUserDisabled desactiva o reactiva una cuenta y, al desactivarla, revoca sus sesiones.
func (s *Store) SetUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `update users set is_disabled = $2 where id = $1 and not is_admin`, id, disabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if disabled {
		if _, err := tx.Exec(ctx, `update sessions set revoked_at = now() where user_id = $1 and revoked_at is null`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SetUserLibraries sustituye las bibliotecas permitidas de un usuario. Es efectivo en la siguiente petición: los permisos
// se consultan siempre en la base de datos, nunca se guardan en la sesión. Si dos administradores editan a la vez, gana
// la última escritura (B4 de la revisión de código: control de versión pendiente).
func (s *Store) SetUserLibraries(ctx context.Context, userID uuid.UUID, libraryIDs []uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from users where id = $1)`, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	var member bool
	if err := tx.QueryRow(ctx, `select household_id is not null and not household_owner from users where id=$1`, userID).Scan(&member); err != nil {
		return err
	}
	if member {
		return ErrNotFound
	}
	var known int
	if err := tx.QueryRow(ctx, `select count(*) from libraries where id = any($1)`, libraryIDs).Scan(&known); err != nil {
		return err
	}
	if known != len(uniqueIDs(libraryIDs)) {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `delete from library_access where user_id = $1 and path_id is null and not (library_id = any($2))`,
		userID, libraryIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into library_access (user_id, library_id) select $1, unnest($2::uuid[])
		on conflict do nothing`, userID, libraryIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func uniqueIDs(ids []uuid.UUID) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// MenuPrefs devuelve qué bibliotecas ha decidido mostrar u ocultar el usuario en SU barra lateral (sin entrada = lo que
// diga el administrador con «Mostrar en el menú»). Es solo presentación: no cambia el acceso ni el inicio.
func (s *Store) MenuPrefs(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]bool, error) {
	var raw map[string]bool
	err := s.Pool.QueryRow(ctx, `select coalesce(preferences->'menu', '{}'::jsonb) from users where id = $1`, userID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]bool, len(raw))
	for k, v := range raw {
		if id, err := uuid.Parse(k); err == nil {
			out[id] = v
		}
	}
	return out, nil
}

// LibraryView es el orden con el que un usuario ve una biblioteca; se recuerda entre visitas y aparatos.
type LibraryView struct {
	Sort    string `json:"sort"`
	Reverse bool   `json:"reverse"`
}

// LibraryViews devuelve el orden guardado de cada biblioteca del usuario (preferences.library_view).
func (s *Store) LibraryViews(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]LibraryView, error) {
	var raw map[string]LibraryView
	err := s.Pool.QueryRow(ctx, `select coalesce(preferences->'library_view', '{}'::jsonb) from users where id = $1`, userID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]LibraryView, len(raw))
	for k, v := range raw {
		if id, err := uuid.Parse(k); err == nil {
			out[id] = v
		}
	}
	return out, nil
}

// SetLibraryView guarda el orden con el que el usuario quiere ver una biblioteca.
func (s *Store) SetLibraryView(ctx context.Context, userID, libraryID uuid.UUID, v LibraryView) error {
	_, err := s.Pool.Exec(ctx, `update users set preferences = jsonb_set(coalesce(preferences, '{}'::jsonb), '{library_view}',
		coalesce(preferences->'library_view', '{}'::jsonb) || jsonb_build_object($2::text, jsonb_build_object('sort', $3::text, 'reverse', $4::boolean))) where id = $1`,
		userID, libraryID.String(), v.Sort, v.Reverse)
	return err
}

// SetMenuPref guarda si el usuario quiere ver una biblioteca en su barra lateral.
func (s *Store) SetMenuPref(ctx context.Context, userID, libraryID uuid.UUID, visible bool) error {
	_, err := s.Pool.Exec(ctx, `update users set preferences = jsonb_set(coalesce(preferences, '{}'::jsonb), '{menu}',
		coalesce(preferences->'menu', '{}'::jsonb) || jsonb_build_object($2::text, $3::boolean)) where id = $1`,
		userID, libraryID.String(), visible)
	return err
}

// SetUserLimits fija los límites de un usuario (y de su hogar). setStreams: cambiar max_streams (nil = volver al valor
// general). members: nil = no cambiar. Los perfiles familiares no tienen límites propios: se rechazan (ErrNotFound).
func (s *Store) SetUserLimits(ctx context.Context, id uuid.UUID, setStreams bool, streams *int, members *int) error {
	tag, err := s.Pool.Exec(ctx, `update users set
		  max_streams = case when $2 then $3::int else max_streams end,
		  max_household_members = coalesce($4::int, max_household_members)
		where id = $1 and not is_admin and (household_id is null or household_owner)`, id, setStreams, streams, members)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// HouseholdMemberLimit devuelve cuántos miembros puede crear el responsable del hogar y cuántos tiene.
func (s *Store) HouseholdMemberLimit(ctx context.Context, hid uuid.UUID) (max, used int, err error) {
	err = s.Pool.QueryRow(ctx, `select coalesce((select max_household_members from users where household_id = $1 and household_owner), 9),
		(select count(*) from users where household_id = $1 and not household_owner and deleted_at is null)`, hid).Scan(&max, &used)
	return
}

// SetHouseholdPermission concede o retira a una cuenta el permiso de crear y ampliar su hogar. Retirarlo no elimina
// a nadie: el hogar sigue funcionando, pero no se pueden añadir ni recuperar miembros. Los perfiles familiares no
// pueden recibirlo (nunca hay hogares dentro de hogares): ErrNotFound.
func (s *Store) SetHouseholdPermission(ctx context.Context, id uuid.UUID, allowed bool) error {
	tag, err := s.Pool.Exec(ctx, `update users set can_create_household = $2
		where id = $1 and deleted_at is null and (household_id is null or household_owner)`, id, allowed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
