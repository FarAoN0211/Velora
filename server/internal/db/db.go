// Package db abre el pool de PostgreSQL y aplica las migraciones embebidas.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // driver database/sql para goose
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open crea el pool de conexiones y comprueba que la base de datos responde.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("URL de base de datos no válida: %w", err)
	}
	cfg.MaxConns = 10 // max_connections=30 en velora-db, compartido entre server y worker
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("la base de datos no responde: %w", err)
	}
	return pool, nil
}

func provider(url string) (*goose.Provider, *sql.DB, error) {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return nil, nil, err
	}
	dir, err := fs.Sub(migrations, "migrations")
	if err != nil {
		sqlDB.Close()
		return nil, nil, err
	}
	// Cerrojo de sesión: server y worker arrancan a la vez y no deben migrar en paralelo
	// (lo detectó la prueba de humo: dos "create extension" simultáneos chocan).
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		sqlDB.Close()
		return nil, nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, dir, goose.WithSessionLocker(locker))
	if err != nil {
		sqlDB.Close()
		return nil, nil, err
	}
	return p, sqlDB, nil
}

// Migrate aplica todas las migraciones pendientes.
func Migrate(ctx context.Context, url string) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	_, err = p.Up(ctx)
	return err
}

// MigrateDown revierte la última migración (todas las de E0 son reversibles).
func MigrateDown(ctx context.Context, url string) error {
	p, sqlDB, err := provider(url)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	_, err = p.Down(ctx)
	return err
}

// SchemaVersion devuelve la versión aplicada y la última disponible en el binario.
func SchemaVersion(ctx context.Context, url string) (current, latest int64, err error) {
	p, sqlDB, err := provider(url)
	if err != nil {
		return 0, 0, err
	}
	defer sqlDB.Close()
	if current, err = p.GetDBVersion(ctx); err != nil {
		return 0, 0, err
	}
	sources := p.ListSources()
	if len(sources) > 0 {
		latest = sources[len(sources)-1].Version
	}
	return current, latest, nil
}
