package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
)

var ErrPlaybackUserLimit = errors.New("límite de reproducciones por usuario")
var ErrPlaybackIPLimit = errors.New("límite de reproducciones por IP")

// Admission and renewal are serialized across processes. Ranges, seeks and audio
// changes renew the same player; disconnected players expire after 45 seconds.
func (s *Store) ReservePlayback(ctx context.Context, session, user, file uuid.UUID, player, ip string, userMax, ipMax int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `select pg_advisory_xact_lock(753110)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `delete from playback_leases where expires_at<=now()`); err != nil {
		return err
	}
	var existing bool
	if err = tx.QueryRow(ctx, `select exists(select 1 from playback_leases where session_id=$1 and player_id=$2 and ip=$3::inet)`, session, player, ip).Scan(&existing); err != nil {
		return err
	}
	// El límite propio del usuario (el del responsable, para todo su hogar) sustituye al general si lo tiene.
	var own *int
	if err = tx.QueryRow(ctx, `select (select case when u.household_id is null then u.max_streams
		  else (select o.max_streams from users o where o.household_id = u.household_id and o.household_owner limit 1) end
		from users u where u.id = $1)`, user).Scan(&own); err != nil {
		return err
	}
	if own != nil {
		userMax = *own
	}
	if !existing && (userMax > 0 || ipMax > 0) {
		var users, ips int
		err = tx.QueryRow(ctx, `select count(*) filter(where coalesce(u.household_id,u.id)=coalesce((select coalesce(household_id,id) from users where id=$3),$3)),count(*) filter(where l.ip=$4::inet)
   from playback_leases l join sessions se on se.id=l.session_id join users u on u.id=se.user_id
   where se.revoked_at is null and se.expires_at>now() and not u.is_disabled and u.can_play
   and not (l.session_id=$1 and l.player_id=$2)`, session, player, user, ip).Scan(&users, &ips)
		if err != nil {
			return err
		}
		if userMax > 0 && users >= userMax {
			return ErrPlaybackUserLimit
		}
		if ipMax > 0 && ips >= ipMax {
			return ErrPlaybackIPLimit
		}
	}
	_, err = tx.Exec(ctx, `insert into playback_leases(session_id,player_id,file_id,ip,expires_at) values($1,$2,$3,$4::inet,now()+interval '45 seconds')
 on conflict(session_id,player_id) do update set file_id=excluded.file_id,ip=excluded.ip,expires_at=excluded.expires_at`, session, player, file, ip)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReleasePlayback(ctx context.Context, session, file uuid.UUID, player string) error {
	_, err := s.Pool.Exec(ctx, `delete from playback_leases where session_id=$1 and player_id=$2 and file_id=$3`, session, player, file)
	return err
}

// Older APKs send presence IDs but do not include them in /decide. Keep their
// heartbeat attached to the legacy reservation rather than counting it twice.
func (s *Store) PlaybackHeartbeatID(ctx context.Context, session, file uuid.UUID, player, legacy string) (string, error) {
	var useLegacy bool
	err := s.Pool.QueryRow(ctx, `select not exists(select 1 from playback_leases where session_id=$1 and player_id=$2)
 and exists(select 1 from playback_leases where session_id=$1 and player_id=$3 and file_id=$4)`, session, player, legacy, file).Scan(&useLegacy)
	if useLegacy {
		return legacy, err
	}
	return player, err
}
