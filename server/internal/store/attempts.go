package store

import (
	"context"
	"math"
	"net/netip"
	"time"
)

// Bloqueo por intentos (VF-075, adelantado de E3). Se cuenta por pareja (usuario, IP) para que un atacante desde otra
// IP no pueda dejar fuera a un usuario legítimo; un tope global por usuario frena los ataques repartidos entre IPs y uno
// por IP frena el reparto entre usuarios.
const (
	attemptWindow    = 15 * time.Minute
	pairFreeFailures = 5  // fallos de un usuario desde una IP antes de esperar
	userFreeFailures = 50 // fallos de un usuario desde cualquier IP (ataque repartido)
	ipFreeFailures   = 20 // fallos desde una IP contra cualquier usuario
	maxLockout       = time.Hour
	maxUserLockout   = 15 * time.Minute // el tope global castiga menos: también afecta al usuario legítimo
)

// RecordAttempt registra un intento de acceso.
func (s *Store) RecordAttempt(ctx context.Context, username, ip, kind string, success bool) error {
	var addr *netip.Addr
	if a, err := netip.ParseAddr(ip); err == nil {
		addr = &a
	}
	if addr == nil {
		a := netip.IPv4Unspecified()
		addr = &a
	}
	var user *string
	if username != "" {
		user = &username
	}
	_, err := s.Pool.Exec(ctx, `insert into login_attempts (username, ip, kind, success) values ($1, $2, $3, $4)`, user, addr, kind, success)
	return err
}

// LockedFor devuelve cuánto debe esperar este usuario desde esta IP antes de otro intento (0 = puede intentarlo).
// Cuenta los fallos de los últimos 15 minutos posteriores al último acierto; la espera crece 1, 2, 4… min.
func (s *Store) LockedFor(ctx context.Context, username, ip string) (time.Duration, error) {
	var pairFails, userFails, ipFails int
	var pairLast, userLast, ipLast *time.Time
	err := s.Pool.QueryRow(ctx, `
		with recent as (
		  select * from login_attempts where created_at > now() - make_interval(secs => $3)
		), ok as (
		  select coalesce(max(created_at), '-infinity') as at from recent where username = $1 and success
		)
		select
		  count(*) filter (where username = $1 and ip = $2::inet and not success and created_at > (select at from ok)),
		  max(created_at) filter (where username = $1 and ip = $2::inet and not success and created_at > (select at from ok)),
		  count(*) filter (where username = $1 and not success and created_at > (select at from ok)),
		  max(created_at) filter (where username = $1 and not success and created_at > (select at from ok)),
		  count(*) filter (where ip = $2::inet and not success),
		  max(created_at) filter (where ip = $2::inet and not success)
		from recent`,
		username, ip, attemptWindow.Seconds()).Scan(&pairFails, &pairLast, &userFails, &userLast, &ipFails, &ipLast)
	if err != nil {
		return 0, err
	}
	wait := func(fails, free int, last *time.Time, max time.Duration) time.Duration {
		if fails < free || last == nil {
			return 0
		}
		d := time.Duration(math.Min(float64(time.Minute)*math.Pow(2, float64(fails-free)), float64(max)))
		if r := time.Until(last.Add(d)); r > 0 {
			return r
		}
		return 0
	}
	longest := wait(pairFails, pairFreeFailures, pairLast, maxLockout)
	for _, d := range []time.Duration{
		wait(userFails, userFreeFailures, userLast, maxUserLockout),
		wait(ipFails, ipFreeFailures, ipLast, maxLockout),
	} {
		if d > longest {
			longest = d
		}
	}
	return longest, nil
}

// PurgeAttempts borra intentos de más de 30 días.
func (s *Store) PurgeAttempts(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `delete from login_attempts where created_at < now() - interval '30 days'`)
	return err
}
