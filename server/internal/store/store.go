// Package store agrupa las consultas de E0 (usuarios, sesiones, dispositivos y ajustes).
// Todas las consultas de catálogo de etapas posteriores pasarán por el predicado de visibilidad de authz (§6).
package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound se devuelve cuando no existe la fila buscada.
var ErrNotFound = errors.New("no encontrado")

// ErrAlreadySetup indica que el asistente inicial ya se completó.
var ErrAlreadySetup = errors.New("el servidor ya está configurado")

// ErrBadSetupCode indica que el código de instalación falta, no coincide o se invalidó por intentos fallidos.
var ErrBadSetupCode = errors.New("código de instalación no válido")

// Store envuelve el pool de PostgreSQL.
type Store struct{ Pool *pgxpool.Pool }

// User es la vista de un usuario que necesitan la API y la autenticación.
type User struct {
	ID             uuid.UUID  `json:"id"`
	Username       string     `json:"username"`
	DisplayName    string     `json:"display_name"`
	IsAdmin        bool       `json:"is_admin"`
	IsHidden       bool       `json:"-"`
	IsDisabled     bool       `json:"-"`
	HasPIN         bool       `json:"has_pin"`
	PasswordHash   string     `json:"-"`
	CanPlay        bool       `json:"can_play"`
	CanDownload    bool       `json:"can_download"`
	CanDelete      bool       `json:"can_delete"`
	CanRemote      bool       `json:"can_remote"`
	LockProfile    bool       `json:"lock_profile"`
	HouseholdID    *uuid.UUID `json:"household_id"`
	HouseholdOwner bool       `json:"household_owner"`
	Passwordless   bool       `json:"passwordless"`
	PINHash        string     `json:"-"`
	// Hogares del propietario (fase 1): permiso concedido por el administrador y cuándo se vuelve a pedir el PIN
	// (0 = siempre al abrir o seleccionar el perfil; 15, 60 o 240 minutos de inactividad).
	CanCreateHousehold bool       `json:"can_create_household"`
	PinLockMinutes     int        `json:"pin_lock_minutes"`
	DeletedAt          *time.Time `json:"-"` // papelera de miembros
	PINRecoveryHash    []byte     `json:"-"` // hash del código de recuperación del PIN
	// Locked: la sesión de un perfil con PIN lleva demasiado tiempo sin uso (fase 4): el servidor solo deja
	// desbloquearla, cambiar de perfil o salir. Solo lo calcula SessionUser.
	Locked bool `json:"locked"`
}

const userCols = `id, username::text, display_name, is_admin, is_hidden, is_disabled, pin_hash is not null,
	coalesce(password_hash, ''), can_play, can_download, can_delete, can_remote, lock_profile, household_id, household_owner, passwordless, coalesce(pin_hash, ''),
	can_create_household, pin_lock_minutes, deleted_at, pin_recovery_hash`

// userColsU es userCols con el alias u. (para consultas con join).
const userColsU = `u.id, u.username::text, u.display_name, u.is_admin, u.is_hidden, u.is_disabled, u.pin_hash is not null,
	coalesce(u.password_hash, ''), u.can_play, u.can_download, u.can_delete, u.can_remote, u.lock_profile, u.household_id, u.household_owner, u.passwordless, coalesce(u.pin_hash, ''),
	u.can_create_household, u.pin_lock_minutes, u.deleted_at, u.pin_recovery_hash`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.IsHidden, &u.IsDisabled, &u.HasPIN,
		&u.PasswordHash, &u.CanPlay, &u.CanDownload, &u.CanDelete, &u.CanRemote, &u.LockProfile, &u.HouseholdID, &u.HouseholdOwner, &u.Passwordless, &u.PINHash,
		&u.CanCreateHousehold, &u.PinLockMinutes, &u.DeletedAt, &u.PINRecoveryHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// SetupRequired indica si todavía no existe ningún administrador.
func (s *Store) SetupRequired(ctx context.Context) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from users where is_admin)`).Scan(&exists)
	return !exists, err
}

// CreateFirstAdmin crea el administrador inicial y guarda los ajustes del asistente, de forma atómica.
// El cerrojo consultivo evita que dos peticiones simultáneas creen dos administradores, y el código de instalación
// (codeHash) se comprueba y se consume en la misma transacción (E0.1, punto 1).
func (s *Store) CreateFirstAdmin(ctx context.Context, codeHash []byte, maxFailures int, username, displayName, passwordHash, serverName, language string) (User, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('velora:setup'))`); err != nil {
		return User{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from users where is_admin)`).Scan(&exists); err != nil {
		return User{}, err
	}
	if exists {
		return User{}, ErrAlreadySetup
	}
	var stored []byte
	err = tx.QueryRow(ctx, `select decode(value #>> '{}', 'hex') from settings where key = 'setup.code_hash'`).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && subtle.ConstantTimeCompare(stored, codeHash) != 1) {
		// El fallo se cuenta en ESTA transacción (con el cerrojo del asistente aún tomado) y se confirma aunque el
		// resultado sea un error: sin una segunda conexión que pueda quedarse esperando al pool, y sin carreras entre
		// intentos simultáneos. Al llegar al máximo se invalida el código en el mismo paso.
		var failures int
		if err := tx.QueryRow(ctx, `insert into settings (key, value) values ('setup.code_failures', '1'::jsonb)
			on conflict (key) do update set value = to_jsonb(coalesce((settings.value #>> '{}')::int, 0) + 1), updated_at = now()
			returning (value #>> '{}')::int`).Scan(&failures); err != nil {
			return User{}, err
		}
		if failures >= maxFailures {
			if _, err := tx.Exec(ctx, `delete from settings where key = 'setup.code_hash'`); err != nil {
				return User{}, err
			}
			if err := upsertSetting(ctx, tx, "setup.locked", true); err != nil {
				return User{}, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return User{}, err
		}
		return User{}, ErrBadSetupCode
	}
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `delete from settings where key in ('setup.code_hash', 'setup.code_failures')`); err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRow(ctx,
		`insert into users (username, display_name, password_hash, is_admin, can_download, can_delete, can_convert, can_manage_collections)
		 values ($1, $2, $3, true, true, false, true, true) returning `+userCols,
		username, displayName, passwordHash))
	if err != nil {
		return User{}, err
	}
	for key, val := range map[string]any{
		"server.name":        serverName,
		"server.language":    language,
		"setup.completed_at": time.Now().UTC(),
	} {
		if err := upsertSetting(ctx, tx, key, val); err != nil {
			return User{}, err
		}
	}
	return u, tx.Commit(ctx)
}

// UserByUsername busca por nombre (sin distinguir mayúsculas).
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `select `+userCols+` from users where username = $1`, username))
}

// UserByID busca por identificador.
func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `select `+userCols+` from users where id = $1`, id))
}

// PublicProfiles devuelve los perfiles visibles en el selector (S02): habilitados y no ocultos.
func (s *Store) PublicProfiles(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `select `+userCols+` from users where not is_hidden and not is_disabled and not passwordless and household_id is null order by display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeviceInfo es lo que declara el cliente al iniciar sesión.
type DeviceInfo struct {
	ClientID   string
	Name       string
	Kind       string
	AppVersion string
	IP         string
}

// CreateSession registra (o actualiza) el dispositivo y crea una sesión con el hash del token.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, ttl time.Duration, dev DeviceInfo) (uuid.UUID, error) {
	return s.CreateSessionAuthorized(ctx, userID, tokenHash, ttl, dev, nil)
}

// Approval is rechecked while holding the user/session locks: logout or password
// recovery cannot race device polling into creating a session after revocation.
func (s *Store) CreateSessionAuthorized(ctx context.Context, userID uuid.UUID, tokenHash []byte, ttl time.Duration, dev DeviceInfo, approval *uuid.UUID, expectedPIN ...string) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var guard uuid.UUID
	var currentPIN string
	err = tx.QueryRow(ctx, `select id,coalesce(pin_hash,'') from users where id=$1 and not is_disabled and (household_id is null or exists(select 1 from households h join users o on o.id=h.owner_id where h.id=users.household_id and not o.is_disabled)) for update`, userID).Scan(&guard, &currentPIN)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, err
	}
	if len(expectedPIN) > 0 && expectedPIN[0] != currentPIN {
		return uuid.Nil, ErrNotFound
	}
	if approval != nil {
		err = tx.QueryRow(ctx, `select a.id from sessions a join users au on au.id=a.user_id join users target on target.id=$2
 where a.id=$1 and a.revoked_at is null and a.expires_at>now() and not au.is_disabled
 and (au.id=target.id or au.is_admin or (au.household_owner and au.household_id=target.household_id))
 and (a.household_device_id is null or exists(select 1 from household_devices hd where hd.id=a.household_device_id and hd.revoked_at is null and hd.expires_at>now())) for share of a`, approval, userID).Scan(&guard)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		if err != nil {
			return uuid.Nil, err
		}
	}

	ip := parseIP(dev.IP)
	var deviceID *uuid.UUID
	if dev.ClientID != "" {
		var id uuid.UUID
		err := tx.QueryRow(ctx,
			`insert into devices (user_id, client_id, name, client_kind, app_version, last_ip, last_seen_at)
			 values ($1, $2, $3, $4, nullif($5, ''), $6, now())
			 on conflict (client_id) do update set user_id = excluded.user_id, name = excluded.name,
			   client_kind = excluded.client_kind, app_version = excluded.app_version,
			   last_ip = excluded.last_ip, last_seen_at = now()
			 returning id`,
			userID, dev.ClientID, dev.Name, dev.Kind, dev.AppVersion, ip).Scan(&id)
		if err != nil {
			return uuid.Nil, err
		}
		deviceID = &id
	}
	var sessionID uuid.UUID
	err = tx.QueryRow(ctx,
		`insert into sessions (user_id, device_id, token_hash, expires_at) values ($1, $2, $3, now() + make_interval(secs => $4)) returning id`,
		userID, deviceID, tokenHash, ttl.Seconds()).Scan(&sessionID)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `update users set last_login_at = now() where id = $1`, userID); err != nil {
		return uuid.Nil, err
	}
	return sessionID, tx.Commit(ctx)
}

// SessionUser resuelve un token: sesión vigente, no revocada, de un usuario habilitado.
// Renueva la caducidad deslizante como mucho una vez cada 5 minutos para no escribir en cada petición.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte, ttl time.Duration) (uuid.UUID, User, error) {
	var sessionID uuid.UUID
	var lastUsed time.Time
	row := s.Pool.QueryRow(ctx,
		`select s.id, s.last_used_at, `+userColsU+`, `+lockedExpr+`
		 from sessions s join users u on u.id = s.user_id
		 where s.token_hash = $1 and s.revoked_at is null and s.expires_at > now() and not u.is_disabled and u.deleted_at is null
 and (u.household_id is null or exists(select 1 from households h join users o on o.id=h.owner_id where h.id=u.household_id and not o.is_disabled))
 and (s.household_device_id is null or exists(select 1 from household_devices hd where hd.id=s.household_device_id and hd.revoked_at is null and hd.expires_at>now()))`, tokenHash)
	var u User
	err := row.Scan(&sessionID, &lastUsed, &u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.IsHidden, &u.IsDisabled,
		&u.HasPIN, &u.PasswordHash, &u.CanPlay, &u.CanDownload, &u.CanDelete, &u.CanRemote, &u.LockProfile, &u.HouseholdID, &u.HouseholdOwner, &u.Passwordless, &u.PINHash,
		&u.CanCreateHousehold, &u.PinLockMinutes, &u.DeletedAt, &u.PINRecoveryHash, &u.Locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, u, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, u, err
	}
	if u.HouseholdID != nil && !u.HouseholdOwner {
		var play, download, del, remote bool
		e := s.Pool.QueryRow(ctx, `select o.can_play,o.can_download,o.can_delete,o.can_remote from households h join users o on o.id=h.owner_id where h.id=$1 and not o.is_disabled`, u.HouseholdID).Scan(&play, &download, &del, &remote)
		if errors.Is(e, pgx.ErrNoRows) {
			return uuid.Nil, u, ErrNotFound
		}
		if e != nil {
			return uuid.Nil, u, e
		}
		u.CanPlay = u.CanPlay && play
		u.CanDownload = u.CanDownload && download
		u.CanDelete = u.CanDelete && del
		u.CanRemote = u.CanRemote && remote
	}
	if time.Since(lastUsed) > 5*time.Minute {
		if _, err := s.RefreshSession(ctx, sessionID, ttl); err != nil {
			return uuid.Nil, u, err
		}
	}
	return sessionID, u, nil
}

// RefreshSession renueva la caducidad deslizante solo si la sesión sigue vigente y sin revocar (E0.1, punto 6):
// una revocación que llegue entre la lectura y la renovación no se ve alterada. Devuelve si renovó algo.
func (s *Store) RefreshSession(ctx context.Context, sessionID uuid.UUID, ttl time.Duration) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update sessions set last_used_at = now(), expires_at = now() + make_interval(secs => $2)
		where id = $1 and revoked_at is null and expires_at > now()`, sessionID, ttl.Seconds())
	if err == nil && tag.RowsAffected() == 1 {
		_, err = s.Pool.Exec(ctx, `update household_devices set expires_at=now()+make_interval(secs=>$2) where id=(select household_device_id from sessions where id=$1) and revoked_at is null and expires_at>now()`, sessionID, ttl.Seconds())
	}
	return tag.RowsAffected() == 1, err
}

// RevokeSession revoca una sesión concreta.
func (s *Store) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update sessions set revoked_at = now() where id = $1 and revoked_at is null`, sessionID)
	return err
}

// Setting lee un ajuste; devuelve ErrNotFound si no existe.
func (s *Store) Setting(ctx context.Context, key string, dst any) error {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `select value from settings where key = $1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// SetSetting guarda un ajuste.
func (s *Store) SetSetting(ctx context.Context, key string, val any) error {
	return upsertSetting(ctx, s.Pool, key, val)
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func upsertSetting(ctx context.Context, q execer, key string, val any) error {
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `insert into settings (key, value) values ($1, $2)
		on conflict (key) do update set value = excluded.value, updated_at = now()`, key, raw)
	return err
}

func parseIP(s string) *netip.Addr {
	if a, err := netip.ParseAddr(s); err == nil {
		return &a
	}
	return nil
}

// Bloqueo del perfil (fase 4 de replica/PLAN_HOGARES_Y_PIN.md), aplicado por el servidor en cada petición.
//
// Cada sesión tiene un plazo de desbloqueo (profile_unlocked_until). Una sesión recién creada ya ha pasado por la
// contraseña, el PIN o un dispositivo aprobado, así que cuenta como desbloqueada desde su creación. El plazo se alarga
// con el uso real: interacciones (mando, teclas, toques, clics) y latidos del reproductor, nunca con las consultas
// automáticas. La duración la elige cada miembro: 15, 60 o 240 minutos; «siempre» (0) son 2 minutos que también alarga
// tener la app a la vista, de modo que se pide al volver tras más de 2 minutos en segundo plano.
// Un perfil sin PIN nunca se bloquea. El ajuste profiles.lock_enabled = false lo desactiva todo (medida de recuperación).
const lockWindow = `make_interval(mins => case when u.pin_lock_minutes = 0 then 2 else u.pin_lock_minutes end)`

const lockedExpr = `(u.pin_hash is not null
	and coalesce((select value from settings where key = 'profiles.lock_enabled'), 'true'::jsonb) <> 'false'::jsonb
	and coalesce(s.profile_unlocked_until, s.created_at + ` + lockWindow + `) < now())`

// UnlockSession desbloquea el perfil en esta sesión (tras teclear su PIN, o al volver a un perfil recordado).
func (s *Store) UnlockSession(ctx context.Context, session uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update sessions s set profile_unlocked_until = now() + `+lockWindow+`, last_activity_at = now()
		from users u where s.id = $1 and u.id = s.user_id`, session)
	return err
}

// LockSession bloquea el perfil en esta sesión (al cambiar a otro perfil en el mismo dispositivo).
func (s *Store) LockSession(ctx context.Context, session uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update sessions set profile_unlocked_until = now() - interval '1 second' where id = $1`, session)
	return err
}

// TouchSession alarga el desbloqueo por uso real. interaction: el usuario ha hecho algo (o el reproductor sigue abierto,
// reproduciendo o en pausa); si no, solo es la app a la vista, que únicamente cuenta para la opción «siempre». Nunca
// desbloquea una sesión ya bloqueada: para eso hay que teclear el PIN.
func (s *Store) TouchSession(ctx context.Context, session uuid.UUID, interaction bool) error {
	_, err := s.Pool.Exec(ctx, `update sessions s set last_activity_at = now(),
		  profile_unlocked_until = greatest(coalesce(s.profile_unlocked_until, s.created_at + `+lockWindow+`), now() + `+lockWindow+`)
		from users u where s.id = $1 and u.id = s.user_id and ($2 or u.pin_lock_minutes = 0)
		  and coalesce(s.profile_unlocked_until, s.created_at + `+lockWindow+`) >= now()`, session, interaction)
	return err
}

// SetPinLockMinutes guarda cuándo se vuelve a pedir el PIN del perfil (0, 15, 60 o 240).
func (s *Store) SetPinLockMinutes(ctx context.Context, uid uuid.UUID, minutes int) error {
	_, err := s.Pool.Exec(ctx, `update users set pin_lock_minutes = $2 where id = $1`, uid, minutes)
	return err
}
