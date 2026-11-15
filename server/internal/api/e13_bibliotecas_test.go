package api

// E1.3 · Gestión de bibliotecas (antes de montar toda la carpeta de medios, con Teldrive, en Pruebas): un fallo de
// lectura o un montaje que deja de responder nunca da películas por desaparecidas; carpetas con el mismo nombre en dos
// bibliotecas no se mezclan; «Escanear todas» a la vez no duplica; el explorador avisa si la lista está incompleta.

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"

	"velora/internal/library"
	"velora/internal/store"
)

func TestScanNeverDropsMoviesOnReadFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("como root los permisos 000 no impiden leer")
	}
	e := newE11Env(t)
	ctx := context.Background()
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	other := filepath.Join(e.mediaRoot, "Animacion", "Otra (2001)")
	_ = os.MkdirAll(other, 0o755)
	_ = os.WriteFile(filepath.Join(other, "Otra (2001).mkv"), src, 0o644)
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(ctx, e.anim.ID)
	e.scanLib(t, lib)
	present := func() int {
		var n int
		_ = e.pool.QueryRow(ctx, `select count(*) from media_files f join media_items i on i.id = f.item_id
			where i.library_id = $1 and f.missing_since is null`, e.anim.ID).Scan(&n)
		return n
	}
	before := present()
	if before < 3 {
		t.Fatalf("archivos antes: %d", before)
	}

	// Una carpeta deja de poder leerse a mitad del recorrido (como un FUSE que falla): nada se marca como desaparecido.
	if err := os.Chmod(other, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(other, 0o755)
	res, err := e.scanErr(lib)
	if err != nil {
		t.Fatalf("un fallo parcial no debe abortar el escaneo: %v", err)
	}
	if r, ok := res.(library.Result); !ok || r.Errors == 0 {
		t.Fatalf("el escaneo debe informar del error de lectura: %+v", res)
	}
	if got := present(); got != before {
		t.Fatalf("con un fallo de lectura no se debe marcar nada como desaparecido: %d → %d", before, got)
	}

	// El montaje entero deja de responder (se ve vacío): la ruta queda inaccesible y las películas siguen.
	_ = os.Chmod(other, 0o755)
	dir := filepath.Join(e.mediaRoot, "Animacion")
	_ = os.Rename(dir, dir+".caido")
	_ = os.MkdirAll(dir, 0o755)
	if _, err := e.scanErr(lib); err == nil {
		t.Fatal("un montaje vacío debe informar del problema")
	}
	if got := present(); got != before {
		t.Fatalf("un montaje caído no debe dar películas por desaparecidas: %d → %d", before, got)
	}
	var status string
	_ = e.pool.QueryRow(ctx, `select status from library_paths where library_id = $1`, e.anim.ID).Scan(&status)
	if status != "unavailable" {
		t.Fatalf("la ruta debería figurar como inaccesible: %s", status)
	}
}

// La misma carpeta en dos bibliotecas (p. ej. local y Teldrive) da dos fichas independientes: cada una con su biblioteca y
// su ruta, y lo que se corrija en una no toca la otra.
func TestSameFolderNameInTwoLibraries(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	for _, base := range []string{"Peliculas", "Animacion"} {
		d := filepath.Join(e.mediaRoot, base, "Duplicada (2020)")
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "Duplicada (2020).mkv"), src, 0o644)
	}
	e.scan(t, e.pelis.ID)
	e.scan(t, e.anim.ID)
	rows, err := e.pool.Query(ctx, `select id, library_id from media_items where content_path = 'Duplicada (2020)' order by library_id`)
	if err != nil {
		t.Fatal(err)
	}
	type it struct{ id, lib uuid.UUID }
	var items []it
	for rows.Next() {
		var x it
		_ = rows.Scan(&x.id, &x.lib)
		items = append(items, x)
	}
	if len(items) != 2 || items[0].lib == items[1].lib {
		t.Fatalf("deben ser dos fichas, una por biblioteca: %+v", items)
	}
	// Se identifica a mano una; tras volver a escanear las dos, la otra sigue sin tocar.
	if _, err := e.pool.Exec(ctx, `update media_items set title = 'Corregida', provider_ids = '{"tmdb":"1"}', locked_fields = '{title}' where id = $1`, items[0].id); err != nil {
		t.Fatal(err)
	}
	e.scan(t, e.pelis.ID)
	e.scan(t, e.anim.ID)
	var t0, t1 string
	_ = e.pool.QueryRow(ctx, `select title from media_items where id = $1`, items[0].id).Scan(&t0)
	_ = e.pool.QueryRow(ctx, `select title from media_items where id = $1`, items[1].id).Scan(&t1)
	if t0 != "Corregida" || t1 != "Duplicada" {
		t.Fatalf("las fichas se han mezclado: %q / %q", t0, t1)
	}
}

// Dos «Escanear todas» a la vez: entre las dos se encola exactamente uno por biblioteca.
func TestScanAllTwiceAtOnce(t *testing.T) {
	e := newE11Env(t)
	if _, err := e.pool.Exec(context.Background(), `update jobs set state = 'done', finished_at = now() where kind = 'library_scan'`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]struct {
		Started int `json:"started"`
		Already int `json:"already"`
	}, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.call(t, "POST", "/api/v1/admin/libraries/scan", e.token, nil, &results[i])
		}(i)
	}
	wg.Wait()
	if results[0].Started+results[1].Started != 2 || results[0].Already+results[1].Already != 2 {
		t.Fatalf("resultados: %+v", results)
	}
	var queued int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from jobs where kind = 'library_scan' and state = 'queued'`).Scan(&queued)
	if queued != 2 {
		t.Fatalf("escaneos en cola: %d (debería haber uno por biblioteca)", queued)
	}
}

// Más de 2000 subcarpetas: el explorador devuelve 2000 y lo dice (truncated), para que la web avise.
func TestFolderBrowserTruncates(t *testing.T) {
	e := newE11Env(t)
	big := filepath.Join(e.mediaRoot, "Grande")
	for i := 0; i < 2005; i++ {
		if err := os.MkdirAll(filepath.Join(big, "Carpeta "+strconv.Itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Folders   []struct{ Name string } `json:"folders"`
		Truncated bool                    `json:"truncated"`
	}
	if c := e.call(t, "GET", "/api/v1/admin/media-folders?path="+url.QueryEscape(big), e.token, nil, &out); c != 200 {
		t.Fatalf("listado: %d", c)
	}
	if len(out.Folders) != 2000 || !out.Truncated {
		t.Fatalf("debería devolver 2000 y avisar: %d %v", len(out.Folders), out.Truncated)
	}
}
