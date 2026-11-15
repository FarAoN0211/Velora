package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Fase 3 de replica/PLAN_HOGARES_Y_PIN.md: historial sellado.
//
// Regla: cualquier restablecimiento hecho por alguien que no es el titular de un perfil con PIN sella su historial
// privado (progreso, vistos, favoritos, «Continuar viendo» y preferencias). Se traslada a sealed_item_data, que ninguna
// consulta de Velora lee, junto con el hash del código de recuperación ANTERIOR: la única llave para devolverlo. Ni el
// propietario ni el administrador pueden recuperarlo. Pasados 30 días se elimina.

// SealDays es lo que se conserva un historial sellado.
const SealDays = 30

// ErrNoPIN: el perfil no tiene PIN; no hay nada que restablecer.
var ErrNoPIN = errors.New("el perfil no tiene PIN")

// sealProfileTx sella el historial de uid y deja el perfil sin PIN, sin código y sin sesiones. Devuelve el sello.
func sealProfileTx(ctx context.Context, tx pgx.Tx, uid uuid.UUID, reason string, by *uuid.UUID) (uuid.UUID, error) {
	var seal uuid.UUID
	if err := tx.QueryRow(ctx, `insert into profile_seals (user_id, recovery_hash, preferences, reason, sealed_by, expires_at)
		select id, pin_recovery_hash, preferences, $2, $3, now() + make_interval(days => $4) from users where id = $1
		returning id`, uid, reason, by, SealDays).Scan(&seal); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `insert into sealed_item_data (seal_id, item_id, position_ms, played, play_count, favorite, last_played_at,
		  client_updated_at, continue_hidden_at, audio_language, subtitle_language, subtitle_mode)
		select $2, item_id, position_ms, played, play_count, favorite, last_played_at, client_updated_at, continue_hidden_at,
		  audio_language, subtitle_language, subtitle_mode from user_item_data where user_id = $1`, uid, seal); err != nil {
		return uuid.Nil, err
	}
	for _, q := range []string{
		`delete from user_item_data where user_id = $1`,
		`update users set preferences = '{}', pin_hash = null, pin_recovery_hash = null where id = $1`,
		`delete from playback_leases where session_id in (select id from sessions where user_id = $1)`,
		`update sessions set revoked_at = now() where user_id = $1 and revoked_at is null`,
	} {
		if _, err := tx.Exec(ctx, q, uid); err != nil {
			return uuid.Nil, err
		}
	}
	return seal, nil
}

// ResetMemberPIN: el propietario restablece el PIN de un miembro de su hogar. El perfil queda utilizable, sin PIN y
// vacío; su historial queda sellado (el propietario no puede verlo). ErrNoPIN si el miembro no tenía PIN.
func (s *Store) ResetMemberPIN(ctx context.Context, owner, member uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	deleted, err := ownerMember(ctx, tx, owner, member)
	if err != nil {
		return err
	}
	if deleted != nil {
		return ErrNotFound
	}
	var hasPIN bool
	if err = tx.QueryRow(ctx, `select pin_hash is not null from users where id = $1`, member).Scan(&hasPIN); err != nil {
		return err
	}
	if !hasPIN {
		return ErrNoPIN
	}
	if _, err = sealProfileTx(ctx, tx, member, "owner_reset", &owner); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AdminResetPIN: el administrador restablece el PIN de una cuenta principal sin contraseña (nunca de un miembro de un
// hogar), siempre con sellado. ErrNoPIN si no tenía PIN.
func (s *Store) AdminResetPIN(ctx context.Context, admin, uid uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hasPIN bool
	if err = tx.QueryRow(ctx, `select pin_hash is not null from users where id = $1 for update`, uid).Scan(&hasPIN); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !hasPIN {
		return ErrNoPIN
	}
	if _, err = sealProfileTx(ctx, tx, uid, "admin_pin_reset", &admin); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResetUserPasswordByAdmin cambia la contraseña de una cuenta (lo hace el administrador). Si la cuenta tiene PIN, su
// historial se sella y el PIN se quita: la contraseña nueva la conoce el administrador, y con ella no debe poder ver
// el historial protegido de esa persona. Devuelve si se ha sellado.
func (s *Store) ResetUserPasswordByAdmin(ctx context.Context, admin, uid uuid.UUID, passwordHash string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var hasPIN bool
	if err = tx.QueryRow(ctx, `select pin_hash is not null from users where id = $1 for update`, uid).Scan(&hasPIN); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if hasPIN {
		if _, err = sealProfileTx(ctx, tx, uid, "admin_password_reset", &admin); err != nil {
			return false, err
		}
	}
	if err = resetPasswordTx(ctx, tx, uid, passwordHash); err != nil {
		return false, err
	}
	return hasPIN, tx.Commit(ctx)
}

// Seal es un historial sellado de un perfil, tal como lo ve su titular.
type Seal struct {
	ID        uuid.UUID `json:"id"`
	SealedAt  time.Time `json:"sealed_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Items     int       `json:"items"`
	Lock      []byte    `json:"-"` // hash del código de recuperación anterior (nil: no hay llave, no se puede devolver)
}

// Seals lista los historiales sellados vigentes de un perfil.
func (s *Store) Seals(ctx context.Context, uid uuid.UUID) ([]Seal, error) {
	rows, err := s.Pool.Query(ctx, `select p.id, p.sealed_at, p.expires_at, (select count(*) from sealed_item_data d where d.seal_id = p.id),
		p.recovery_hash from profile_seals p where p.user_id = $1 and p.expires_at > now() order by p.sealed_at desc`, uid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Seal, error) {
		var x Seal
		err := r.Scan(&x.ID, &x.SealedAt, &x.ExpiresAt, &x.Items, &x.Lock)
		return x, err
	})
}

// Unseal devuelve un historial sellado a su titular (después de comprobar su código antiguo). Si desde el sellado ha
// visto algo, por cada título gana el progreso más reciente; las preferencias se recuperan si no ha puesto otras.
func (s *Store) Unseal(ctx context.Context, uid, seal uuid.UUID) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var prefs []byte
	if err = tx.QueryRow(ctx, `select preferences from profile_seals where id = $1 and user_id = $2 and expires_at > now() for update`,
		seal, uid).Scan(&prefs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	tag, err := tx.Exec(ctx, `insert into user_item_data (user_id, item_id, position_ms, played, play_count, favorite, last_played_at,
		  client_updated_at, continue_hidden_at, audio_language, subtitle_language, subtitle_mode)
		select $2, item_id, position_ms, played, play_count, favorite, last_played_at, client_updated_at, continue_hidden_at,
		  audio_language, subtitle_language, subtitle_mode from sealed_item_data where seal_id = $1
		on conflict (user_id, item_id) do update set
		  position_ms = case when coalesce(excluded.client_updated_at, excluded.last_played_at, '-infinity') >
		    coalesce(user_item_data.client_updated_at, user_item_data.last_played_at, '-infinity') then excluded.position_ms else user_item_data.position_ms end,
		  last_played_at = greatest(user_item_data.last_played_at, excluded.last_played_at),
		  client_updated_at = greatest(user_item_data.client_updated_at, excluded.client_updated_at),
		  played = user_item_data.played or excluded.played,
		  play_count = user_item_data.play_count + excluded.play_count,
		  favorite = user_item_data.favorite or excluded.favorite`, seal, uid)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `update users set preferences = $2 where id = $1 and preferences = '{}'`, uid, prefs); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `delete from profile_seals where id = $1`, seal); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// PurgeExpiredSeals elimina los historiales sellados con más de 30 días (tarea nocturna).
func (s *Store) PurgeExpiredSeals(ctx context.Context) (int, error) {
	tag, err := s.Pool.Exec(ctx, `delete from profile_seals where expires_at <= now()`)
	return int(tag.RowsAffected()), err
}

// SetPINWithRecovery guarda el PIN y el código de recuperación de un perfil (hashes; "" quita ambos) y cierra las
// demás sesiones del perfil. expected es el hash del PIN que había al empezar (para no pisar un cambio simultáneo).
func (s *Store) SetPINWithRecovery(ctx context.Context, uid, keep uuid.UUID, expected, pinHash string, recovery []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `select coalesce(pin_hash, '') from users where id = $1 and not is_disabled and deleted_at is null for update`, uid).Scan(&current); err != nil {
		return ErrNotFound
	}
	if current != expected {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `update users set pin_hash = nullif($2, ''), pin_recovery_hash = $3 where id = $1`, uid, pinHash, recovery); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `update sessions set revoked_at = now() where user_id = $1 and revoked_at is null and id <> $2`, uid, keep); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PINFailures cuenta los intentos fallidos de PIN o código de un perfil desde su anterior visita (el aviso que ve al
// entrar) o, con since, desde esa fecha (lo que ve el administrador: número y fecha, sin dispositivo).
func (s *Store) PINFailures(ctx context.Context, uid uuid.UUID, since *time.Time) (int, *time.Time, error) {
	var n int
	var last *time.Time
	err := s.Pool.QueryRow(ctx, `with prev as (
		  select coalesce($2::timestamptz, (select max(created_at) from login_attempts
		    where username = $1 and success and created_at < now() - interval '5 seconds'), '-infinity') as at)
		select count(*), max(created_at) from login_attempts
		where username = $1 and not success and created_at > (select at from prev)`, "pin:"+uid.String(), since).Scan(&n, &last)
	return n, last, err
}
