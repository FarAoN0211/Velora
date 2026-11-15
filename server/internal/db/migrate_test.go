package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Las migraciones de E0 deben poder aplicarse, revertirse y volver a aplicarse (D-8).
func TestMigrationsAreReversible(t *testing.T) {
	base := os.Getenv("VELORA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("VELORA_TEST_DATABASE_URL no definida")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("velora_mig_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
	u, _ := url.Parse(base)
	u.Path = "/" + name

	for step, f := range []func(context.Context, string) error{Migrate, MigrateDown, Migrate} {
		if err := f(ctx, u.String()); err != nil {
			t.Fatalf("paso %d: %v", step, err)
		}
	}
	cur, latest, err := SchemaVersion(ctx, u.String())
	if err != nil || cur != latest || cur < 1 {
		t.Fatalf("versión %d/%d, error %v", cur, latest, err)
	}
}
