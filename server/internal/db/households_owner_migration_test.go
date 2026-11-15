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

// Fase 1 de replica/PLAN_HOGARES_Y_PIN.md: la migración 00024 se aplica sobre datos reales de hogares (creados con el
// flujo anterior, por el administrador) sin perder nada, el responsable conserva el permiso de hogar, las reglas nuevas
// se cumplen y bajar la migración deja los datos como estaban.
func TestHouseholdOwnerMigration(t *testing.T) {
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
	name := fmt.Sprintf("velora_hogares_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
	u, _ := url.Parse(base)
	u.Path = "/" + name
	dsn := u.String()
	version := func() int64 {
		t.Helper()
		cur, _, err := SchemaVersion(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		return cur
	}
	downTo := func(v int64) {
		t.Helper()
		for version() > v {
			if err := MigrateDown(ctx, dsn); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	downTo(23)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// Estado con el esquema 23: un hogar (responsable + un familiar), una cuenta normal, un administrador, progreso.
	if _, err := conn.Exec(ctx, `
		insert into users (id, username, display_name, is_admin) values
		  ('00000000-0000-0000-0000-0000000000a1', 'admin', 'Admin', true),
		  ('00000000-0000-0000-0000-0000000000b1', 'normal', 'Normal', false);
		insert into users (id, username, display_name, passwordless, is_hidden) values
		  ('00000000-0000-0000-0000-0000000000c1', 'responsable', 'Responsable', true, true),
		  ('00000000-0000-0000-0000-0000000000c2', 'familiar', 'Familiar', true, true);
		insert into households (id, owner_id, name) values ('00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000c1', 'Hogar');
		update users set household_id = '00000000-0000-0000-0000-0000000000d1', household_owner = (username = 'responsable')
		  where username in ('responsable', 'familiar');
		insert into libraries (id, name, kind) values ('00000000-0000-0000-0000-0000000000e1', 'Pelis', 'movies');
		insert into library_paths (id, library_id, path) values ('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000e1', '/media/P');
		insert into media_items (id, library_id, library_path_id, kind, title, sort_title, content_path) values
		  ('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000e2', 'movie', 'A', 'a', 'A');
		insert into user_item_data (user_id, item_id, position_ms, played) values
		  ('00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-0000000000f1', 12345, false);`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	type row struct {
		username string
		can      bool
		lock     int
	}
	rows, err := conn.Query(ctx, `select username, can_create_household, pin_lock_minutes from users order by username`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		err := r.Scan(&x.username, &x.can, &x.lock)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"admin": false, "normal": false, "responsable": true, "familiar": false}
	for _, r := range got {
		if r.can != want[r.username] || r.lock != 60 {
			t.Fatalf("%s: permiso %v, bloqueo %d; se esperaba %v y 60", r.username, r.can, r.lock, want[r.username])
		}
	}
	var pos int64
	if err := conn.QueryRow(ctx, `select position_ms from user_item_data where user_id = '00000000-0000-0000-0000-0000000000c2'`).Scan(&pos); err != nil || pos != 12345 {
		t.Fatalf("el progreso del familiar debe conservarse: %d %v", pos, err)
	}

	// Reglas nuevas: la papelera es solo para familiares; el bloqueo solo admite 0, 15, 60 o 240 minutos.
	if _, err := conn.Exec(ctx, `update users set deleted_at = now() where username = 'responsable'`); err == nil {
		t.Fatal("el responsable no puede ir a la papelera")
	}
	if _, err := conn.Exec(ctx, `update users set deleted_at = now() where username = 'normal'`); err == nil {
		t.Fatal("una cuenta sin hogar no puede ir a la papelera")
	}
	if _, err := conn.Exec(ctx, `update users set deleted_at = now() where username = 'familiar'`); err != nil {
		t.Fatalf("un familiar sí puede ir a la papelera: %v", err)
	}
	if _, err := conn.Exec(ctx, `update users set pin_lock_minutes = 30 where username = 'familiar'`); err == nil {
		t.Fatal("30 minutos no es una opción de bloqueo")
	}
	// Sellado: el historial sellado vive aparte y desaparece con su sello.
	var seal string
	if err := conn.QueryRow(ctx, `insert into profile_seals (user_id, reason, expires_at)
		values ('00000000-0000-0000-0000-0000000000c2', 'owner_reset', now() + interval '30 days') returning id`).Scan(&seal); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into sealed_item_data (seal_id, item_id, position_ms) values ($1, '00000000-0000-0000-0000-0000000000f1', 5)`, seal); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into profile_seals (user_id, reason, expires_at) values ('00000000-0000-0000-0000-0000000000c2', 'cualquiera', now() + interval '1 day')`); err == nil {
		t.Fatal("solo se admiten los motivos de sellado previstos")
	}
	if _, err := conn.Exec(ctx, `delete from profile_seals where id = $1`, seal); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = conn.QueryRow(ctx, `select count(*) from sealed_item_data`).Scan(&left)
	if left != 0 {
		t.Fatal("al borrar el sello debe borrarse su historial sellado")
	}

	// Volver atrás: los datos anteriores siguen intactos.
	if _, err := conn.Exec(ctx, `update users set deleted_at = null`); err != nil {
		t.Fatal(err)
	}
	downTo(23)
	var owner bool
	if err := conn.QueryRow(ctx, `select household_owner from users where username = 'responsable'`).Scan(&owner); err != nil || !owner {
		t.Fatalf("el hogar debe seguir igual tras bajar la migración: %v %v", owner, err)
	}
	if err := conn.QueryRow(ctx, `select position_ms from user_item_data where user_id = '00000000-0000-0000-0000-0000000000c2'`).Scan(&pos); err != nil || pos != 12345 {
		t.Fatalf("el progreso debe seguir igual tras bajar la migración: %d %v", pos, err)
	}
	var cols int
	_ = conn.QueryRow(ctx, `select count(*) from information_schema.columns where table_name = 'users'
		and column_name in ('can_create_household', 'deleted_at', 'pin_lock_minutes', 'pin_recovery_hash')`).Scan(&cols)
	if cols != 0 {
		t.Fatalf("bajar la migración debe quitar las columnas nuevas: quedan %d", cols)
	}
}
