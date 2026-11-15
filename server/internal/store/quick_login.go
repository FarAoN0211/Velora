package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

var ErrRateLimited = errors.New("demasiadas solicitudes")

type DeviceAuthorization struct {
	Name    string    `json:"device_name"`
	IP      string    `json:"ip"`
	Expires time.Time `json:"expires_at"`
}

func (s *Store) CreateDeviceAuthorization(ctx context.Context, code, device []byte, name, ip string) (time.Time, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize issuance to bound both six-digit code guessing and table growth.
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(753109)`); err != nil {
		return time.Time{}, err
	}
	if _, err = tx.Exec(ctx, `delete from device_authorizations where expires_at<now()-interval '1 hour'`); err != nil {
		return time.Time{}, err
	}
	var allowed bool
	if err = tx.QueryRow(ctx, `select (select count(*) from device_authorizations where ip=$1 and created_at>now()-interval '1 hour')<20 and (select count(*) from device_authorizations where expires_at>now())<500`, ip).Scan(&allowed); err != nil {
		return time.Time{}, err
	}
	if !allowed {
		return time.Time{}, ErrRateLimited
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `insert into device_authorizations(code_hash,device_hash,device_name,ip,expires_at) values($1,$2,$3,$4,now()+interval '5 minutes') returning expires_at`, code, device, name, ip).Scan(&expires)
	if isUnique(err) {
		return time.Time{}, ErrDuplicate
	}
	if err != nil {
		return expires, err
	}
	return expires, tx.Commit(ctx)
}

func (s *Store) DeviceAuthorization(ctx context.Context, code []byte) (DeviceAuthorization, error) {
	var a DeviceAuthorization
	err := s.Pool.QueryRow(ctx, `select device_name,ip,expires_at from device_authorizations where code_hash=$1 and expires_at>now() and consumed_at is null and approved_session is null`, code).Scan(&a.Name, &a.IP, &a.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return a, err
}
func (s *Store) ApproveDeviceAuthorization(ctx context.Context, code []byte, session uuid.UUID) error {
	var user uuid.UUID
	if err := s.Pool.QueryRow(ctx, `select user_id from sessions where id=$1`, session).Scan(&user); err != nil {
		return err
	}
	return s.ApproveDeviceForUser(ctx, code, session, user, "")
}
func (s *Store) ApproveDeviceForUser(ctx context.Context, code []byte, session, target uuid.UUID, pinHash string) error {
	tag, err := s.Pool.Exec(ctx, `update device_authorizations a set approved_session=$2,approved_user_id=$3,approved_pin_hash=$4
 where code_hash=$1 and expires_at>now() and consumed_at is null and approved_session is null
 and exists(select 1 from sessions s join users actor on actor.id=s.user_id join users u on u.id=$3
 where s.id=$2 and s.revoked_at is null and s.expires_at>now() and not actor.is_disabled and not u.is_disabled
 and coalesce(u.pin_hash,'')=$4 and (actor.household_id is null or actor.household_owner)
 and (actor.id=u.id or actor.is_admin or (actor.household_owner and actor.household_id=u.household_id)))`, code, session, target, pinHash)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Claim is atomic: a known user code cannot poll, and only one request can consume approval.
func (s *Store) ClaimDeviceAuthorization(ctx context.Context, device []byte) (*User, *uuid.UUID, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var session *uuid.UUID
	var last *time.Time
	var target *uuid.UUID
	var pinHash *string
	err = tx.QueryRow(ctx, `select id,approved_session,last_poll_at,approved_user_id,approved_pin_hash from device_authorizations where device_hash=$1 and expires_at>now() and consumed_at is null for update`, device).Scan(&id, &session, &last, &target, &pinHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if last != nil && time.Since(*last) < time.Second {
		return nil, nil, ErrRateLimited
	}
	if _, err = tx.Exec(ctx, `update device_authorizations set last_poll_at=now() where id=$1`, id); err != nil {
		return nil, nil, err
	}
	if session == nil {
		return nil, nil, tx.Commit(ctx)
	}
	var uid uuid.UUID
	err = tx.QueryRow(ctx, `select u.id from sessions s join users actor on actor.id=s.user_id join users u on u.id=coalesce($2,s.user_id)
 where s.id=$1 and s.revoked_at is null and s.expires_at>now() and not actor.is_disabled and not u.is_disabled
 and (actor.id=u.id or actor.is_admin or (actor.household_owner and actor.household_id=u.household_id))
 and ($3::text is null or coalesce(u.pin_hash,'')=$3)
 and (u.household_id is null or exists(select 1 from households h join users o on o.id=h.owner_id where h.id=u.household_id and not o.is_disabled))`, session, target, pinHash).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	u, err := scanUser(tx.QueryRow(ctx, `select `+userCols+` from users where id=$1 for share`, uid))
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `update device_authorizations set consumed_at=now() where id=$1`, id); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &u, session, nil
}

func (s *Store) SetRecoveryCodes(ctx context.Context, user uuid.UUID, hashes [][]byte, ttl time.Duration, expectedHash ...string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentHash string
	if err = tx.QueryRow(ctx, `select password_hash from users where id=$1 and not is_disabled for update`, user).Scan(&currentHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if len(expectedHash) > 0 && currentHash != expectedHash[0] {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `delete from recovery_codes where user_id=$1`, user); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err = tx.Exec(ctx, `insert into recovery_codes(user_id,code_hash,expires_at) values($1,$2,now()+make_interval(secs=>$3))`, user, hash, ttl.Seconds()); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ResetPasswordWithRecovery(ctx context.Context, username string, code []byte, passwordHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var uid uuid.UUID
	err = tx.QueryRow(ctx, `select id from users where username=$1 and not is_disabled for update`, username).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `delete from recovery_codes where user_id=$1 and code_hash=$2 and expires_at>now()`, uid, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err = resetPasswordTx(ctx, tx, uid, passwordHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ResetUserPassword(ctx context.Context, uid uuid.UUID, passwordHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `select id from users where id=$1 for update`, uid).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = resetPasswordTx(ctx, tx, uid, passwordHash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func resetPasswordTx(ctx context.Context, tx pgx.Tx, uid uuid.UUID, hash string) error {
	if _, err := tx.Exec(ctx, `update users set password_hash=$2 where id=$1`, uid, hash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update sessions set revoked_at=now() where user_id=$1 and revoked_at is null`, uid); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from recovery_codes where user_id=$1`, uid)
	return err
}
