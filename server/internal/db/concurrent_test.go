package db

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Regresión: server y worker arrancan a la vez y migran sobre una base vacía (fallo encontrado en la prueba de humo).
func TestConcurrentMigrate(t *testing.T) {
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
	name := fmt.Sprintf("velora_conc_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
	u, _ := url.Parse(base)
	u.Path = "/" + name

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = Migrate(ctx, u.String())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migración concurrente %d falló: %v", i, err)
		}
	}
}
