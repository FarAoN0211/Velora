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

// E1.3 T0: la migración 00005 reclasifica los «trailer.*» de la raíz de la carpeta de su película (si no hay otro
// archivo, la ficha queda pendiente); no toca subcarpetas ni títulos que empiezan por «Trailer»; conserva el rastro
// (origin_file_id y las pistas); y al revertirla todo vuelve con sus ids y sus pistas.
func TestExtrasMigration(t *testing.T) {
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
	name := fmt.Sprintf("velora_extras_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
	u, _ := url.Parse(base)
	u.Path = "/" + name
	dsn := u.String()

	// Estado anterior a E1.3: se aplica todo y se revierte hasta la 00004 (hay migraciones posteriores a la 00005).
	downTo := func(v int64) {
		t.Helper()
		for {
			cur, _, err := SchemaVersion(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			if cur <= v {
				return
			}
			if err := MigrateDown(ctx, dsn); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	downTo(4)
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// A: película + trailer.mkv (con pistas analizadas) → se reclasifica.
	// B: carpeta con solo trailer.mkv → se reclasifica y la ficha B queda pendiente (sin archivos).
	// C: «Trailer Park Boys.mkv» → es la película, se queda.
	// D: película + trailer2.mp4 → se reclasifica.
	// E: película + Extras/trailer.mkv (subcarpeta) → no lo toca la migración (lo hará el escáner).
	if _, err := conn.Exec(ctx, `
		insert into libraries (id, name, kind) values ('00000000-0000-0000-0000-00000000000a', 'Pelis', 'movies');
		insert into library_paths (id, library_id, path) values ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-00000000000a', '/media/P');
		insert into media_items (id, library_id, library_path_id, kind, title, sort_title, content_path) values
		  ('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-0000000000a1', 'movie', 'A', 'a', 'A (2000)'),
		  ('00000000-0000-0000-0000-0000000000f2', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-0000000000a1', 'movie', 'B', 'b', 'B (2001)'),
		  ('00000000-0000-0000-0000-0000000000f3', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-0000000000a1', 'movie', 'C', 'c', 'Trailer Park Boys (2006)'),
		  ('00000000-0000-0000-0000-0000000000f4', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-0000000000a1', 'movie', 'D', 'd', 'D (2003)'),
		  ('00000000-0000-0000-0000-0000000000f5', '00000000-0000-0000-0000-00000000000a', '00000000-0000-0000-0000-0000000000a1', 'movie', 'E', 'e', 'E (2004)');
		insert into media_files (id, item_id, library_path_id, rel_path, size_bytes, mtime, duration_ms, probed_at) values
		  ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000a1', 'A (2000)/A (2000).mkv', 1000, now(), 7200000, now()),
		  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000a1', 'A (2000)/trailer.mkv', 10, now(), 120000, now()),
		  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-0000000000f2', '00000000-0000-0000-0000-0000000000a1', 'B (2001)/trailer.mkv', 10, now(), 120000, now()),
		  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-0000000000f3', '00000000-0000-0000-0000-0000000000a1', 'Trailer Park Boys (2006)/Trailer Park Boys.mkv', 1000, now(), 6000000, now()),
		  ('00000000-0000-0000-0000-000000000401', '00000000-0000-0000-0000-0000000000f4', '00000000-0000-0000-0000-0000000000a1', 'D (2003)/D (2003).mkv', 1000, now(), 6000000, now()),
		  ('00000000-0000-0000-0000-000000000402', '00000000-0000-0000-0000-0000000000f4', '00000000-0000-0000-0000-0000000000a1', 'D (2003)/trailer2.mp4', 10, now(), 90000, now()),
		  ('00000000-0000-0000-0000-000000000501', '00000000-0000-0000-0000-0000000000f5', '00000000-0000-0000-0000-0000000000a1', 'E (2004)/E (2004).mkv', 1000, now(), 6000000, now()),
		  ('00000000-0000-0000-0000-000000000502', '00000000-0000-0000-0000-0000000000f5', '00000000-0000-0000-0000-0000000000a1', 'E (2004)/Extras/trailer.mkv', 10, now(), 90000, now());
		insert into media_streams (file_id, stream_index, kind, codec, height, language, is_default) values
		  ('00000000-0000-0000-0000-000000000101', 0, 'video', 'hevc', 2160, null, true),
		  ('00000000-0000-0000-0000-000000000102', 0, 'video', 'h264', 1080, null, true),
		  ('00000000-0000-0000-0000-000000000102', 1, 'audio', 'aac', null, 'spa', true)`); err != nil {
		t.Fatal(err)
	}
	files := func() map[string]bool {
		rows, _ := conn.Query(ctx, `select id::text from media_files`)
		ids, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		m := map[string]bool{}
		for _, id := range ids {
			m[id] = true
		}
		return m
	}
	before := files()
	streams := func() string {
		var s string
		_ = conn.QueryRow(ctx, `select coalesce(string_agg(file_id::text || ':' || stream_index || kind || codec || coalesce(height::text, '') ||
			coalesce(language, '') || is_default::text, ',' order by file_id, stream_index), '') from media_streams`).Scan(&s)
		return s
	}
	streamsBefore := streams()

	check := func(step string) {
		t.Helper()
		var extras, traced int
		if err := conn.QueryRow(ctx, `select count(*), count(origin_file_id) from media_extras`).Scan(&extras, &traced); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		f := files()
		if extras != 3 || traced != 3 || f["00000000-0000-0000-0000-000000000102"] || f["00000000-0000-0000-0000-000000000201"] ||
			f["00000000-0000-0000-0000-000000000402"] {
			t.Fatalf("%s: extras %d (con rastro %d), archivos %v", step, extras, traced, f)
		}
		for _, keep := range []string{"101", "301", "401", "501", "502"} {
			if !f["00000000-0000-0000-0000-000000000"+keep] {
				t.Fatalf("%s: el archivo …%s no debía moverse", step, keep)
			}
		}
		var item, kind string
		var dur int64
		_ = conn.QueryRow(ctx, `select item_id::text, kind, duration_ms from media_extras where origin_file_id = '00000000-0000-0000-0000-000000000102'`).Scan(&item, &kind, &dur)
		if item != "00000000-0000-0000-0000-0000000000f1" || kind != "trailer" || dur != 120000 {
			t.Fatalf("%s: el tráiler de A quedó mal: %s %s %d", step, item, kind, dur)
		}
	}

	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	check("primera subida")

	// Revertir: todo vuelve a media_files con sus ids originales.
	downTo(4)
	after := files()
	if len(after) != len(before) {
		t.Fatalf("tras revertir hay %d archivos; antes %d", len(after), len(before))
	}
	for id := range before {
		if !after[id] {
			t.Fatalf("tras revertir falta el archivo %s", id)
		}
	}
	if got := streams(); got != streamsBefore {
		t.Fatalf("tras revertir las pistas no son las de antes:\n%s\n%s", got, streamsBefore)
	}

	// Volver a aplicar da el mismo resultado (sin duplicados).
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	check("segunda subida")
}
