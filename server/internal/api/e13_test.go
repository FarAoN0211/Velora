package api

// E1.3 T0: el escáner separa tráileres y extras de los archivos de la película, reclasifica lo que escaneos anteriores
// registraron como versión (sin contarlo como desaparecido) y deja «pendiente» (oculta, con su ficha) la película de
// una carpeta que solo tiene su tráiler, hasta que aparece la película.

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"velora/internal/library"

	"github.com/google/uuid"

	"velora/internal/store"
)

func TestScannerSeparatesExtras(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	src, err := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel string, data []byte) {
		t.Helper()
		p := filepath.Join(e.mediaRoot, "Peliculas", rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	movie := "Prueba sintética (2020)/"
	write(movie+"trailer.mkv", src)
	write(movie+"Trailers/teaser.mkv", src)
	write(movie+"prueba-featurette.mkv", src)
	write(movie+"prueba-sample.mkv", src)                 // muestra: se ignora
	write(movie+"trailer.f137.mp4.part", []byte("medio")) // descarga a medias: se ignora
	write("Solo tráiler (2025)/trailer.mkv", src)         // carpeta sin película, nueva: se ignora
	write("Huérfana (2024)/trailer.mkv", src)             // carpeta sin película ya registrada: su ficha queda pendiente

	// Estado anterior a E1.3: el tráiler figuraba como segunda versión de la película (ya analizado) y «Huérfana» era
	// una «película» cuyo único archivo es su tráiler.
	info, _ := os.Stat(filepath.Join(e.mediaRoot, "Peliculas", movie+"trailer.mkv"))
	var legacyID uuid.UUID
	if err := e.pool.QueryRow(ctx, `insert into media_files (item_id, library_path_id, rel_path, size_bytes, mtime, duration_ms, probed_at)
		select id, library_path_id, $2, $3, $4, 6000, now() from media_items where id = $1 returning id`,
		e.pelisItem, movie+"trailer.mkv", info.Size(), info.ModTime()).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	var orphanItem uuid.UUID
	info2, _ := os.Stat(filepath.Join(e.mediaRoot, "Peliculas", "Huérfana (2024)/trailer.mkv"))
	if err := e.pool.QueryRow(ctx, `with i as (insert into media_items (library_id, library_path_id, kind, title, sort_title, content_path)
		  select library_id, library_path_id, 'movie', 'Huérfana', 'huérfana', 'Huérfana (2024)' from media_items where id = $1 returning id, library_path_id)
		insert into media_files (item_id, library_path_id, rel_path, size_bytes, mtime) select id, library_path_id, $2, $3, $4 from i returning item_id`,
		e.pelisItem, "Huérfana (2024)/trailer.mkv", info2.Size(), info2.ModTime()).Scan(&orphanItem); err != nil {
		t.Fatal(err)
	}

	res := e.scan(t, e.pelis.ID)
	if res.Extras != 4 || res.Pending != 1 || res.Missing != 0 {
		t.Fatalf("resultado del escaneo: %+v", res)
	}
	type extra struct {
		Rel, Kind string
		Origin    *uuid.UUID
		Probed    *time.Time
		Item      uuid.UUID
	}
	extras := func() map[string]extra {
		rows, err := e.pool.Query(ctx, `select rel_path, kind, origin_file_id, probed_at, item_id from media_extras where missing_since is null`)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]extra{}
		for rows.Next() {
			var x extra
			_ = rows.Scan(&x.Rel, &x.Kind, &x.Origin, &x.Probed, &x.Item)
			m[x.Rel] = x
		}
		return m
	}
	got := extras()
	want := map[string]string{movie + "trailer.mkv": "trailer", movie + "Trailers/teaser.mkv": "trailer", movie + "prueba-featurette.mkv": "featurette"}
	for rel, kind := range want {
		x, ok := got[rel]
		if !ok || x.Kind != kind || x.Item != e.pelisItem || x.Probed == nil {
			t.Fatalf("extra %s: %+v (todos: %v)", rel, x, got)
		}
	}
	if len(got) != len(want)+1 || got["Huérfana (2024)/trailer.mkv"].Item != orphanItem {
		t.Fatalf("extras: %v", got)
	}
	// Reclasificado con rastro y sin volver a analizarlo.
	if x := got[movie+"trailer.mkv"]; x.Origin == nil || *x.Origin != legacyID {
		t.Fatalf("el tráiler reclasificado debe guardar su id anterior: %+v", x)
	}
	// La película se queda con su único archivo; la ficha no ofrece el tráiler como versión.
	var detail struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	if len(detail.Item.Files) != 1 {
		t.Fatalf("la película debería tener un solo archivo: %d", len(detail.Item.Files))
	}
	// Ningún extra es película, y la carpeta nueva con solo tráiler no crea ninguna.
	var titles []string
	rows, _ := e.pool.Query(ctx, `select title from media_items where library_id = $1 order by title`, e.pelis.ID)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		titles = append(titles, s)
	}
	if len(titles) != 2 { // «Prueba sintética» y la «Huérfana» heredada
		t.Fatalf("películas: %v", titles)
	}
	// La ficha de la carpeta con solo tráiler queda pendiente: sin archivos, oculta a los usuarios (lista, búsqueda,
	// ficha y reproducción, aunque se conozcan sus ids) y visible para el administrador en el panel.
	var orphanFiles int
	_ = e.pool.QueryRow(ctx, `select count(*) from media_files where item_id = $1`, orphanItem).Scan(&orphanFiles)
	if orphanFiles != 0 {
		t.Fatalf("la ficha pendiente no debería tener archivos: %d", orphanFiles)
	}
	e.grantB(t, e.pelis.ID)
	if e.itemIDs(t, e.tokenB, "")[orphanItem] || e.itemIDs(t, e.tokenB, "Huérfana")[orphanItem] {
		t.Fatal("una ficha pendiente no debe aparecer en la lista ni en la búsqueda")
	}
	if c := e.call(t, "GET", "/api/v1/items/"+orphanItem.String(), e.tokenB, nil, nil); c != 404 {
		t.Fatalf("la ficha pendiente debe dar 404 a un usuario: %d", c)
	}
	if c := e.call(t, "POST", "/api/v1/me/items/"+orphanItem.String()+"/played", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("marcar como vista una ficha pendiente debe dar 404: %d", c)
	}
	var orphanOldFile uuid.UUID
	_ = e.pool.QueryRow(ctx, `select origin_file_id from media_extras where item_id = $1`, orphanItem).Scan(&orphanOldFile)
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": orphanOldFile, "caps": chromiumCaps}, nil); c != 404 {
		t.Fatalf("el antiguo archivo (el tráiler) no debe poder reproducirse como película: %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/items/"+orphanItem.String(), e.token, nil, nil); c != 200 {
		t.Fatalf("el administrador sí ve la ficha pendiente: %d", c)
	}
	var pending struct {
		Items []store.PendingItem `json:"items"`
	}
	e.call(t, "GET", "/api/v1/admin/pending-items", e.token, nil, &pending)
	if len(pending.Items) != 1 || pending.Items[0].ID != orphanItem || pending.Items[0].ContentPath != "Huérfana (2024)" || pending.Items[0].Extras != 1 {
		t.Fatalf("pendientes: %+v", pending.Items)
	}
	if c := e.call(t, "GET", "/api/v1/admin/pending-items", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("la lista de pendientes es solo para administradores: %d", c)
	}

	// Volver a escanear no duplica ni cambia nada.
	if res := e.scan(t, e.pelis.ID); res.Extras != 4 || res.Missing != 0 {
		t.Fatalf("segundo escaneo: %+v", res)
	}
	if n := len(extras()); n != 4 {
		t.Fatalf("extras tras el segundo escaneo: %d", n)
	}
	var total int
	_ = e.pool.QueryRow(ctx, `select count(*) from media_extras`).Scan(&total)
	if total != 4 {
		t.Fatalf("filas de extras duplicadas: %d", total)
	}

	// Si un extra desaparece del disco se marca como ausente, sin tocar la película.
	_ = os.Remove(filepath.Join(e.mediaRoot, "Peliculas", movie+"Trailers/teaser.mkv"))
	e.scan(t, e.pelis.ID)
	if _, ok := extras()[movie+"Trailers/teaser.mkv"]; ok {
		t.Fatal("el teaser borrado debería figurar como ausente")
	}
	var mainMissing bool
	_ = e.pool.QueryRow(ctx, `select missing_since is not null from media_files where item_id = $1`, e.pelisItem).Scan(&mainMissing)
	if mainMissing {
		t.Fatal("la película no debe verse afectada")
	}

	// Llega la película a la carpeta pendiente: aparece una sola vez, en la misma ficha (con su identificación y título
	// corregidos a mano), con su archivo principal y su tráiler.
	if _, err := e.pool.Exec(ctx, `update media_items set title = 'Huérfana (corregida)', provider_ids = '{"tmdb":"777"}', locked_fields = '{title}' where id = $1`, orphanItem); err != nil {
		t.Fatal(err)
	}
	write("Huérfana (2024)/Huérfana (2024).mkv", src)
	e.scan(t, e.pelis.ID)
	var count int
	var title, tmdb string
	_ = e.pool.QueryRow(ctx, `select count(*) from media_items where content_path = 'Huérfana (2024)'`).Scan(&count)
	_ = e.pool.QueryRow(ctx, `select title, provider_ids->>'tmdb' from media_items where id = $1`, orphanItem).Scan(&title, &tmdb)
	if count != 1 || title != "Huérfana (corregida)" || tmdb != "777" {
		t.Fatalf("la película debe volver a su ficha: %d fichas, %q, tmdb %q", count, title, tmdb)
	}
	if !e.itemIDs(t, e.tokenB, "")[orphanItem] {
		t.Fatal("con la película presente, la ficha vuelve a verse")
	}
	e.call(t, "GET", "/api/v1/items/"+orphanItem.String(), e.tokenB, nil, &detail)
	if len(detail.Item.Files) != 1 || got["Huérfana (2024)/trailer.mkv"].Item != orphanItem {
		t.Fatalf("archivos de la película: %d", len(detail.Item.Files))
	}
	if x := extras()["Huérfana (2024)/trailer.mkv"]; x.Item != orphanItem {
		t.Fatalf("el tráiler sigue asociado a su película: %+v", x)
	}
	e.call(t, "GET", "/api/v1/admin/pending-items", e.token, nil, &pending)
	if len(pending.Items) != 0 {
		t.Fatalf("ya no queda nada pendiente: %+v", pending.Items)
	}
}

// Una ficha que versiones anteriores dejaron con el título vacío («Spanish Affair 2 (2015)») se repara al escanear y
// vuelve a pedirse a TMDB; una ficha con título (aunque venga de TMDB) no se toca.
func TestScanRepairsEmptyTitle(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	dir := filepath.Join(e.mediaRoot, "Peliculas", "Spanish Affair 2 (2015)")
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "Spanish Affair 2 (2015).mkv"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	var broken uuid.UUID
	if err := e.pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, year, content_path, metadata_refreshed_at)
		select library_id, library_path_id, 'movie', '', '', 2015, 'Spanish Affair 2 (2015)', now() from media_items where id = $1 returning id`,
		e.pelisItem).Scan(&broken); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set title = 'Título de TMDB', metadata_refreshed_at = now() where id = $1`, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	e.scan(t, e.pelis.ID)
	var title string
	var pending bool
	_ = e.pool.QueryRow(ctx, `select title, metadata_refreshed_at is null from media_items where id = $1`, broken).Scan(&title, &pending)
	if title != "Spanish Affair 2" || !pending {
		t.Fatalf("ficha reparada: %q, pendiente de TMDB %v", title, pending)
	}
	_ = e.pool.QueryRow(ctx, `select title from media_items where id = $1`, e.pelisItem).Scan(&title)
	if title != "Título de TMDB" {
		t.Fatalf("un título con contenido no se toca: %q", title)
	}
}

// E1.3 T1: los extras aparecen en la ficha y se reproducen con el reproductor de siempre (Directo/Optimizado, mismos
// permisos y revocación), pero nunca guardan progreso ni marcan la película como vista.
func TestExtrasPlaybackWithoutProgress(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	for _, rel := range []string{"trailer.mkv", "Featurettes/Rodaje en el desierto.mkv"} {
		p := filepath.Join(e.movieDir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.scan(t, e.pelis.ID)
	var detail struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	x := detail.Item.Extras
	if len(x) != 2 || x[0].Kind != "trailer" || x[0].Title != "" || x[1].Kind != "featurette" || x[1].Title != "Rodaje en el desierto" {
		t.Fatalf("extras de la ficha: %+v", x)
	}
	if len(x[0].Streams) == 0 || x[0].DurationMS == nil {
		t.Fatalf("el tráiler debe traer sus pistas y duración: %+v", x[0])
	}
	trailer := x[0].ID

	// Se reproduce como cualquier archivo: plan y flujo.
	var plan struct {
		Method string `json:"method"`
		URL    string `json:"url"`
	}
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": trailer, "caps": chromiumCaps}, &plan); c != 200 || plan.URL == "" {
		t.Fatalf("decidir el tráiler: %d %+v", c, plan)
	}
	res := e.raw(t, "GET", plan.URL+map[bool]string{true: "&t=0", false: ""}[plan.Method == "direct_stream"], nil)
	head := make([]byte, 32)
	n, _ := res.Body.Read(head)
	res.Body.Close()
	if res.StatusCode != 200 || n == 0 {
		t.Fatalf("flujo del tráiler: %d (%d bytes)", res.StatusCode, n)
	}

	// Nunca guarda progreso: el servidor lo rechaza si se indica el extra, y la película no cambia.
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.pelisItem.String()+"/progress", e.token,
		map[string]any{"file_id": trailer, "position_ms": 5000, "duration_ms": 6000}, nil); c != 400 {
		t.Fatalf("el progreso de un tráiler debe rechazarse: %d", c)
	}
	var rows int
	_ = e.pool.QueryRow(ctx, `select count(*) from user_item_data where item_id = $1`, e.pelisItem).Scan(&rows)
	if rows != 0 {
		t.Fatal("ver el tráiler no debe crear progreso ni marcar la película")
	}
	// El progreso de la película sí se guarda (con su propio archivo).
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.pelisItem.String()+"/progress", e.token,
		map[string]any{"file_id": e.pelisFile, "position_ms": 2000, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("el progreso de la película: %d", c)
	}

	// Permisos: sin acceso a la biblioteca, ni ficha ni tráiler; con acceso, sí; al revocarlo, otra vez no.
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": trailer, "caps": chromiumCaps}, nil); c != 404 {
		t.Fatalf("sin acceso, el tráiler debe dar 404: %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+trailer.String()+"/subtitles", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("sin acceso, tampoco sus subtítulos: %d", c)
	}
	e.grantB(t, e.pelis.ID)
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": trailer, "caps": chromiumCaps}, nil); c != 200 {
		t.Fatalf("con acceso, el tráiler se reproduce: %d", c)
	}
	e.grantB(t)
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": trailer, "caps": chromiumCaps}, nil); c != 404 {
		t.Fatalf("al revocar el acceso, el tráiler deja de reproducirse: %d", c)
	}

	// Sin tráiler: la ficha no trae extras (la web no muestra el botón).
	if _, err := e.pool.Exec(ctx, `delete from media_extras`); err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	if detail.Item.Extras == nil || len(detail.Item.Extras) != 0 {
		t.Fatalf("sin extras, la lista debe venir vacía (no nula): %+v", detail.Item.Extras)
	}
}

// Menú ⋮: la tarjeta trae el tráiler a reproducir con un criterio fijo (principal, luego numerados, luego subcarpeta) y
// nada si no hay tráiler.
func TestCardTrailerChoice(t *testing.T) {
	e := newE11Env(t)
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	card := func() *uuid.UUID {
		t.Helper()
		var out struct {
			Items []store.ItemCard `json:"items"`
		}
		e.call(t, "GET", "/api/v1/items?q=prueba", e.token, nil, &out)
		if len(out.Items) != 1 {
			t.Fatalf("tarjetas: %d", len(out.Items))
		}
		return out.Items[0].TrailerID
	}
	if id := card(); id != nil {
		t.Fatalf("sin tráiler no debe venir trailer_id: %v", id)
	}
	for _, rel := range []string{"Trailers/teaser.mkv", "trailer2.mkv", "trailer.mkv"} {
		p := filepath.Join(e.movieDir, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, src, 0o644)
	}
	e.scan(t, e.pelis.ID)
	var main uuid.UUID
	_ = e.pool.QueryRow(context.Background(), `select id from media_extras where rel_path like '%/trailer.mkv'`).Scan(&main)
	for i := 0; i < 3; i++ { // estable entre consultas
		if id := card(); id == nil || *id != main {
			t.Fatalf("debe elegirse trailer.mkv: %v (esperado %s)", id, main)
		}
	}
}

// Escaneo con progreso: el escáner informa primero de la búsqueda, luego del análisis y de los extras, con el total
// correcto; «Escanear todas» encola una por biblioteca sin duplicar, y /admin/scans da el estado de cada una.
func TestScanProgressAndScanAll(t *testing.T) {
	e := newE11Env(t)
	src, _ := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	_ = os.WriteFile(filepath.Join(e.movieDir, "trailer.mkv"), src, 0o644)
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.pelis.ID)
	sc := library.NewScanner(st, nil)
	var got []library.ScanProgress
	sc.OnProgress = func(p library.ScanProgress) { got = append(got, p) }
	if _, err := sc.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	phases := []string{}
	for _, p := range got {
		if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
			phases = append(phases, p.Phase)
		}
		if p.Phase != "listing" && (p.Total != 2 || p.Done >= p.Total || p.Paths != 1 || p.Path != 1) {
			t.Fatalf("progreso incoherente: %+v", p)
		}
	}
	if strings.Join(phases, ",") != "analyzing,extras" {
		t.Fatalf("fases: %v", phases)
	}

	// Escanear todas: una por biblioteca; repetir no duplica. (Al crear las bibliotecas ya se encoló un escaneo de cada
	// una y aquí no hay proceso de cola: se dan por terminados.)
	if _, err := e.pool.Exec(context.Background(), `update jobs set state = 'done', finished_at = now() where kind = 'library_scan'`); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Started int `json:"started"`
		Already int `json:"already"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/libraries/scan", e.token, nil, &out); c != 202 || out.Started != 2 || out.Already != 0 {
		t.Fatalf("escanear todas: %d %+v", c, out)
	}
	if c := e.call(t, "POST", "/api/v1/admin/libraries/scan", e.token, nil, &out); c != 202 || out.Started != 0 || out.Already != 2 {
		t.Fatalf("repetir no debe duplicar: %d %+v", c, out)
	}
	var scans struct {
		Scans map[string]struct {
			State string `json:"state"`
		} `json:"scans"`
	}
	e.call(t, "GET", "/api/v1/admin/scans", e.token, nil, &scans)
	if scans.Scans[e.pelis.ID.String()].State != "queued" || scans.Scans[e.anim.ID.String()].State != "queued" {
		t.Fatalf("estado de los escaneos: %+v", scans.Scans)
	}
	if c := e.call(t, "GET", "/api/v1/admin/scans", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("solo para administradores: %d", c)
	}
	if c := e.call(t, "POST", "/api/v1/admin/libraries/scan", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("solo para administradores: %d", c)
	}
}

// Explorador de carpetas del panel: entra en subcarpetas dentro de las raíces de medios y nunca fuera (ni con «..» ni
// con enlaces simbólicos); solo administradores.
func TestMediaFolderBrowser(t *testing.T) {
	e := newE11Env(t)
	sub := filepath.Join(e.mediaRoot, "nube", "Peliculas")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.Symlink("/etc", filepath.Join(e.mediaRoot, "nube", "fuera"))
	type resp struct {
		Folders []struct{ Name, Path, Kind string } `json:"folders"`
		Parent  string                              `json:"parent"`
	}
	var top resp
	if c := e.call(t, "GET", "/api/v1/admin/media-folders", e.token, nil, &top); c != 200 {
		t.Fatalf("primer nivel: %d", c)
	}
	var in resp
	if c := e.call(t, "GET", "/api/v1/admin/media-folders?path="+url.QueryEscape(filepath.Join(e.mediaRoot, "nube")), e.token, nil, &in); c != 200 {
		t.Fatalf("dentro de nube: %d", c)
	}
	names := []string{}
	for _, f := range in.Folders {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "Peliculas" || in.Folders[0].Kind != "local" {
		t.Fatalf("subcarpetas: %v (el enlace a /etc no debe salir como carpeta navegable)", in.Folders)
	}
	for _, bad := range []string{"/etc", filepath.Join(e.mediaRoot, "nube", "fuera"), filepath.Join(e.mediaRoot, "..")} {
		if c := e.call(t, "GET", "/api/v1/admin/media-folders?path="+url.QueryEscape(bad), e.token, nil, nil); c != 400 {
			t.Fatalf("%s debería rechazarse: %d", bad, c)
		}
	}
	if c := e.call(t, "GET", "/api/v1/admin/media-folders?path="+url.QueryEscape(sub), e.tokenB, nil, nil); c != 404 {
		t.Fatalf("solo administradores: %d", c)
	}
}
