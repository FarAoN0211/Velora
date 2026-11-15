// Package audit escribe el registro de auditoría. Nunca recibe secretos ni contraseñas.
package audit

import (
	"context"
	"log/slog"
	"net/netip"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Logger escribe en audit_log.
type Logger struct{ pool *pgxpool.Pool }

// New crea el registrador.
func New(pool *pgxpool.Pool) *Logger { return &Logger{pool: pool} }

// Record guarda un evento. Si falla, se deja constancia en el log y no se interrumpe la petición.
func (l *Logger) Record(ctx context.Context, actor *uuid.UUID, action, target string, details map[string]any, ip string) {
	if details == nil {
		details = map[string]any{}
	}
	var addr *netip.Addr
	if a, err := netip.ParseAddr(ip); err == nil {
		addr = &a
	}
	_, err := l.pool.Exec(ctx,
		`insert into audit_log (actor_user_id, action, target, details, ip) values ($1, $2, nullif($3, ''), $4, $5)`,
		actor, action, target, details, addr)
	if err != nil {
		slog.Error("no se pudo escribir en la auditoría", "accion", action, "error", err)
	}
}
