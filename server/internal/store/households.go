package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// CreatePasswordlessAccount crea una cuenta principal sin contraseña y autoriza su primer dispositivo (el del código)
// en una transacción: un código o una biblioteca no válidos no dejan cuentas huérfanas ni autorizan al administrador.
// Ya no crea un hogar: lo crea, si tiene permiso, el propio titular desde «Mi cuenta» (replica/PLAN_HOGARES_Y_PIN.md).
func (s *Store) CreatePasswordlessAccount(ctx context.Context, actorSession uuid.UUID, code []byte, username, name string, libraries []uuid.UUID, canCreateHousehold bool) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var guard uuid.UUID
	if err = tx.QueryRow(ctx, `select s.id from sessions s join users u on u.id=s.user_id where s.id=$1 and s.revoked_at is null and s.expires_at>now() and u.is_admin and not u.is_disabled for share of s`, actorSession).Scan(&guard); err != nil {
		return uuid.Nil, ErrNotFound
	}
	if err = tx.QueryRow(ctx, `select id from device_authorizations where code_hash=$1 and expires_at>now() and consumed_at is null and approved_session is null for update`, code).Scan(&guard); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	var known int
	if err = tx.QueryRow(ctx, `select count(*) from libraries where id=any($1) and retired_at is null`, libraries).Scan(&known); err != nil {
		return uuid.Nil, err
	}
	if known != len(uniqueIDs(libraries)) {
		return uuid.Nil, ErrNotFound
	}
	var uid uuid.UUID
	if err = tx.QueryRow(ctx, `insert into users(username,display_name,passwordless,is_hidden,can_create_household) values($1,$2,true,true,$3) returning id`,
		username, name, canCreateHousehold).Scan(&uid); err != nil {
		if isUnique(err) {
			return uuid.Nil, ErrDuplicate
		}
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `insert into library_access(user_id,library_id) select $1,unnest($2::uuid[])`, uid, libraries); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `update device_authorizations set approved_session=$2,approved_user_id=$3,approved_pin_hash='' where code_hash=$1`, code, actorSession, uid); err != nil {
		return uuid.Nil, err
	}
	return uid, tx.Commit(ctx)
}

func (s *Store) HouseholdProfiles(ctx context.Context, hid uuid.UUID) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `select `+userCols+` from users where household_id=$1 and deleted_at is null order by household_owner desc,display_name`, hid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) CreateFamilyProfile(ctx context.Context, owner uuid.UUID, name string) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var hid uuid.UUID
	var allowed bool
	if err = tx.QueryRow(ctx, `select household_id, can_create_household from users where id=$1 and household_owner and not is_disabled for update`, owner).Scan(&hid, &allowed); err != nil {
		return uuid.Nil, ErrNotFound
	}
	if !allowed { // el administrador retiró el permiso: el hogar sigue, pero no crece
		return uuid.Nil, ErrHouseholdNotAllowed
	}
	// Miembros sin contar al responsable ni a los de la papelera, frente a su límite (9 + el responsable).
	var count, limit int
	if err = tx.QueryRow(ctx, `select (select count(*) from users where household_id=$1 and not household_owner and deleted_at is null),
		(select max_household_members from users where id=$2)`, hid, owner).Scan(&count, &limit); err != nil {
		return uuid.Nil, err
	}
	if count >= limit {
		return uuid.Nil, ErrRateLimited
	}
	id := uuid.New()
	_, err = tx.Exec(ctx, `insert into users(id,username,display_name,household_id,passwordless,is_hidden) values($1,$2,$3,$4,true,true)`, id, "perfil_"+id.String(), name, hid)
	if err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}
func (s *Store) ChangeProfilePIN(ctx context.Context, id, session uuid.UUID, expected, hash string, reset bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var current string
	if err = tx.QueryRow(ctx, `select coalesce(pin_hash,'') from users where id=$1 and not is_disabled for update`, id).Scan(&current); err != nil {
		return ErrNotFound
	}
	if current != expected {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `update users set pin_hash=nullif($2,'') where id=$1`, id, hash); err != nil {
		return err
	}
	// A reset must not leave another browser already logged in with the old PIN.
	_, err = tx.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1 and revoked_at is null and ($3 or id<>$2)`, id, session, reset)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) SetFamilyDisabled(ctx context.Context, owner, id uuid.UUID, disabled bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `update users u set is_disabled=$3 where u.id=$2 and not u.household_owner and not u.is_admin and u.deleted_at is null and exists(select 1 from users o where o.id=$1 and o.household_owner and not o.is_disabled and o.household_id=u.household_id)`, owner, id, disabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if disabled {
		if _, err = tx.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) NewHouseholdDevice(ctx context.Context, u User, session uuid.UUID, hash []byte, name string, ttl time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err = tx.QueryRow(ctx, `insert into household_devices(household_id,token_hash,name,expires_at) select $1,$2,$3,now()+make_interval(secs=>$4) where exists(select 1 from sessions where id=$5 and user_id=$6 and revoked_at is null and expires_at>now()) returning id`, u.HouseholdID, hash, name, ttl.Seconds(), session, u.ID).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `update sessions set household_device_id=$2 where id=$1`, session, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) HouseholdDevice(ctx context.Context, hash []byte) (uuid.UUID, uuid.UUID, error) {
	var id, hid uuid.UUID
	err := s.Pool.QueryRow(ctx, `select d.id,d.household_id from household_devices d join households h on h.id=d.household_id join users o on o.id=h.owner_id where d.token_hash=$1 and d.revoked_at is null and d.expires_at>now() and not o.is_disabled`, hash).Scan(&id, &hid)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return id, hid, err
}

// A device grant cannot authenticate as a profile. The caller must verify that
// profile's PIN; this transaction rechecks its hash and household membership.
func (s *Store) CreateHouseholdSession(ctx context.Context, grant, target uuid.UUID, expectedPIN string, token []byte, ttl time.Duration) (uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var guard uuid.UUID
	if err = tx.QueryRow(ctx, `select u.id from users u join households h on h.id=u.household_id join users o on o.id=h.owner_id join household_devices d on d.household_id=h.id where u.id=$1 and not u.is_disabled and u.deleted_at is null and not o.is_disabled and coalesce(u.pin_hash,'')=$2 and d.id=$3 and d.revoked_at is null and d.expires_at>now() for share of u,o,d`, target, expectedPIN, grant).Scan(&guard); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	var id uuid.UUID
	if err = tx.QueryRow(ctx, `insert into sessions(user_id,token_hash,household_device_id,expires_at) values($1,$2,$3,now()+make_interval(secs=>$4)) returning id`, target, token, grant, ttl.Seconds()).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `update household_devices set expires_at=now()+make_interval(secs=>$2) where id=$1`, grant, ttl.Seconds()); err != nil {
		return uuid.Nil, err
	}
	return id, tx.Commit(ctx)
}
func (s *Store) RevokeHouseholdDevice(ctx context.Context, session uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update household_devices set revoked_at=now() where id=(select household_device_id from sessions where id=$1)`, session)
	return err
}

type HouseholdDeviceSummary struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Expires time.Time `json:"expires_at"`
}

func (s *Store) HouseholdDevices(ctx context.Context, hid uuid.UUID) ([]HouseholdDeviceSummary, error) {
	rows, err := s.Pool.Query(ctx, `select id,name,expires_at from household_devices where household_id=$1 and revoked_at is null and expires_at>now() order by name`, hid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (HouseholdDeviceSummary, error) {
		var d HouseholdDeviceSummary
		err := r.Scan(&d.ID, &d.Name, &d.Expires)
		return d, err
	})
}
func (s *Store) RemoveHouseholdDevice(ctx context.Context, hid, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `update household_devices set revoked_at=now() where id=$1 and household_id=$2`, id, hid)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
