package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/audit"
	"velora/internal/backup"
	"velora/internal/config"
	"velora/internal/jobs"
	"velora/internal/library"
	"velora/internal/metadata"
	"velora/internal/secrets"
	"velora/internal/setup"
	"velora/internal/store"
)

// e1Env es un servidor con una raíz de medios temporal y una película sintética (H.264 + AC3 en MKV + póster local).
type e1Env struct {
	*testEnv
	mediaRoot string
	movieDir  string
	token     string
	srv       *Server
}

func ffmpegAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no disponible: se omite la prueba de E1")
	}
}

func newE1Env(t *testing.T, tmdbURL string) *e1Env {
	t.Helper()
	ffmpegAvailable(t)
	base := newEnv(t) // base de datos efímera ya migrada
	mediaRoot := t.TempDir()
	movieDir := filepath.Join(mediaRoot, "Peliculas", "Prueba sintética (2020)")
	if err := os.MkdirAll(movieDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25",
		"-c:a", "ac3", "-metadata:s:a:0", "language=spa", filepath.Join(movieDir, "Prueba sintética (2020).mkv"))
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo generar el vídeo: %v %s", err, out)
	}
	poster := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=200x300",
		"-frames:v", "1", filepath.Join(movieDir, "poster.jpg"))
	if out, err := poster.CombinedOutput(); err != nil {
		t.Fatalf("no se pudo generar el póster: %v %s", err, out)
	}

	cfg := config.Config{DatabaseURL: base.dbURL, SessionTTL: time.Hour, BackupDir: t.TempDir(), ConfigDir: t.TempDir(),
		Location: time.UTC, MediaRoots: []string{mediaRoot}, MaxRemux: 2, TMDBAPIURL: tmdbURL, TMDBImageURL: tmdbURL + "/img"}
	key, _ := secrets.EnsureMasterKey(cfg.ConfigDir)
	st := &store.Store{Pool: base.pool}
	srv := New(cfg, key, st, audit.New(base.pool), jobs.NewQueue(base.pool), &backup.Service{Pool: base.pool, DatabaseURL: base.dbURL, Dir: cfg.BackupDir})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	base.server = ts
	base.srv = srv
	code, err := setup.Ensure(context.Background(), base.pool, cfg.ConfigDir, false)
	if err != nil {
		t.Fatal(err)
	}
	base.code = code
	base.configDir = cfg.ConfigDir
	e := &e1Env{testEnv: base, mediaRoot: mediaRoot, movieDir: movieDir, srv: srv}
	e.token = adminSetup(t, base)
	return e
}

func (e *e1Env) raw(t *testing.T, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, e.server.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (e *e1Env) scan(t *testing.T, libID uuid.UUID) library.Result {
	t.Helper()
	st := &store.Store{Pool: e.pool}
	lib, err := st.LibraryByID(context.Background(), libID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := library.NewScanner(st, nil).ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatalf("escaneo: %v", err)
	}
	return res
}

var chromiumCaps = map[string]any{"containers": []string{"mp4", "webm"}, "video": []string{"h264", "vp9", "av1"},
	"audio": []string{"aac", "mp3", "opus", "flac"}}

func TestE1LibraryScanAndPlayback(t *testing.T) {
	e := newE1Env(t, "")
	ctx := context.Background()

	// Una carpeta fuera de las raíces de medios se rechaza (también con ..).
	for _, bad := range []string{"/etc", filepath.Join(e.mediaRoot, "..")} {
		if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Mala", "path": bad}, nil); c != 422 {
			t.Fatalf("la ruta %s debería rechazarse (422), dio %d", bad, c)
		}
	}
	var folders struct {
		Folders []struct{ Name, Path string } `json:"folders"`
	}
	e.call(t, "GET", "/api/v1/admin/media-folders", e.token, nil, &folders)
	if len(folders.Folders) != 1 || folders.Folders[0].Name != "Peliculas" {
		t.Fatalf("carpetas de medios: %+v", folders)
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Películas", "path": folders.Folders[0].Path}, &created); c != 201 {
		t.Fatalf("crear biblioteca: %d", c)
	}
	res := e.scan(t, created.Library.ID)
	if res.Items != 1 || res.Files != 1 || res.Probed != 1 {
		t.Fatalf("escaneo inesperado: %+v", res)
	}
	// Un segundo escaneo no vuelve a analizar lo que no ha cambiado.
	if res2 := e.scan(t, created.Library.ID); res2.Probed != 0 {
		t.Fatalf("no debería reanalizar ficheros sin cambios: %+v", res2)
	}

	var items struct {
		Items []store.ItemCard `json:"items"`
	}
	e.call(t, "GET", "/api/v1/items?q=sintetica", e.token, nil, &items) // búsqueda sin tildes
	if len(items.Items) != 1 || items.Items[0].Title != "Prueba sintética" || items.Items[0].Year == nil || *items.Items[0].Year != 2020 || !items.Items[0].HasPoster {
		t.Fatalf("catálogo: %+v", items.Items)
	}
	itemID := items.Items[0].ID
	var detail struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+itemID.String(), e.token, nil, &detail)
	if len(detail.Item.Files) != 1 || len(detail.Item.Files[0].Streams) < 2 {
		t.Fatalf("ficha sin ficheros o pistas: %+v", detail.Item)
	}
	fileID := detail.Item.Files[0].ID
	codecs := map[string]string{}
	for _, s := range detail.Item.Files[0].Streams {
		codecs[s.Kind] = s.Codec
	}
	if codecs["video"] != "h264" || codecs["audio"] != "ac3" {
		t.Fatalf("pistas inesperadas: %v", codecs)
	}
	if res := e.raw(t, "GET", "/api/v1/items/"+itemID.String()+"/images/poster", nil); res.StatusCode != 200 {
		t.Fatalf("póster local: %d", res.StatusCode)
	} else {
		res.Body.Close()
	}

	// Navegador sin AC3 ni MKV → Direct Stream con el audio convertido.
	var plan decideResponse
	e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": fileID, "caps": chromiumCaps}, &plan)
	if plan.Method != "direct_stream" || !plan.ConvertAudio || plan.URL == "" || plan.DurationMS < 5000 || len(plan.Reasons) == 0 {
		t.Fatalf("plan para Chromium: %+v", plan)
	}
	res2 := e.raw(t, "GET", plan.URL+"&t=1", nil)
	head := make([]byte, 64*1024)
	n, _ := io.ReadAtLeast(res2.Body, head, 16)
	res2.Body.Close()
	if res2.StatusCode != 200 || res2.Header.Get("Content-Type") != "video/mp4" || !bytes.Contains(head[:n], []byte("ftyp")) {
		t.Fatalf("Direct Stream: estado %d, tipo %q, %d bytes", res2.StatusCode, res2.Header.Get("Content-Type"), n)
	}

	// Cliente que admite MKV + H.264 + AC3 → Direct Play con Range.
	tv := map[string]any{"containers": []string{"mkv", "mp4"}, "video": []string{"h264", "hevc"}, "audio": []string{"aac", "ac3", "eac3"}}
	e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": fileID, "caps": tv}, &plan)
	if plan.Method != "direct_play" {
		t.Fatalf("plan para TV: %+v", plan)
	}
	res3 := e.raw(t, "GET", plan.URL, map[string]string{"Range": "bytes=0-99"})
	body, _ := io.ReadAll(res3.Body)
	res3.Body.Close()
	if res3.StatusCode != 206 || len(body) != 100 {
		t.Fatalf("Direct Play con Range: estado %d, %d bytes", res3.StatusCode, len(body))
	}

	// Progreso → Continuar viendo; ocultar sin marcar como vista; al 90 % queda vista.
	if c := e.call(t, "PUT", "/api/v1/me/items/"+itemID.String()+"/progress", e.token, map[string]any{"position_ms": 2000, "duration_ms": 6000}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	var home struct {
		Featured []store.Featured `json:"featured"`
		Continue []store.ItemCard `json:"continue"`
		Rows     []struct {
			Library struct{ Name string } `json:"library"`
			Items   []store.ItemCard      `json:"items"`
		} `json:"rows"`
	}
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 1 || home.Continue[0].PositionMS != 2000 || len(home.Rows) != 1 || len(home.Rows[0].Items) != 1 {
		t.Fatalf("inicio: %+v", home)
	}
	// Con póster local (sin fondo) la película puede ir en la cabecera destacada; la duración sale del archivo sin TMDB.
	c := home.Continue[0]
	if len(home.Featured) != 1 || c.RuntimeMS == nil || *c.RuntimeMS < 5000 || c.Quality != "SD" {
		t.Fatalf("cabecera, duración o calidad: %+v / %+v", home.Featured, c)
	}
	e.call(t, "POST", "/api/v1/me/items/"+itemID.String()+"/continue-hidden", e.token, nil, nil)
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 0 {
		t.Fatal("tras ocultarla no debería aparecer en Continuar viendo")
	}
	e.call(t, "PUT", "/api/v1/me/items/"+itemID.String()+"/progress", e.token, map[string]any{"position_ms": 5800, "duration_ms": 6000}, nil)
	var again struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+itemID.String(), e.token, nil, &again)
	if !again.Item.Played {
		t.Fatal("al pasar del 90 % debería quedar vista")
	}

	// Sin permiso de reproducción no hay stream (ni siquiera conociendo la URL).
	if _, err := e.pool.Exec(ctx, `update users set can_play = false`); err != nil {
		t.Fatal(err)
	}
	if res := e.raw(t, "GET", "/api/v1/stream/files/"+fileID.String(), nil); res.StatusCode != 403 {
		t.Fatalf("sin can_play debería dar 403, dio %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
	if _, err := e.pool.Exec(ctx, `update users set can_play = true`); err != nil {
		t.Fatal(err)
	}

	// El fichero desaparece → no se borra el título; queda marcado y el stream lo dice.
	if err := os.Rename(e.movieDir, e.movieDir+".movida"); err != nil {
		t.Fatal(err)
	}
	if res := e.scan(t, created.Library.ID); res.Missing != 1 {
		t.Fatalf("debería marcar 1 fichero desaparecido: %+v", res)
	}
	if res := e.raw(t, "GET", "/api/v1/stream/files/"+fileID.String(), nil); res.StatusCode != 404 {
		t.Fatalf("fichero desaparecido debería dar 404, dio %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
}

// VF-075: 5 fallos seguidos bloquean el usuario, aunque después llegue la contraseña correcta.
func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	for i := 0; i < 5; i++ {
		if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "mala"}, nil); c != 401 {
			t.Fatalf("intento %d: %d", i, c)
		}
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "contraseña-larga"}, nil); c != 429 {
		t.Fatalf("tras 5 fallos debería dar 429, dio %d", c)
	}
	// Pasado el bloqueo (se simula envejeciendo los intentos), vuelve a poder entrar y el contador se reinicia.
	if _, err := e.pool.Exec(context.Background(), `update login_attempts set created_at = created_at - interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "contraseña-larga"}, nil); c != 200 {
		t.Fatalf("pasado el bloqueo debería entrar, dio %d", c)
	}
}

// TMDB contra un servidor falso: datos, póster descargado a /config/metadata y campos bloqueados respetados.
func TestTMDBEnrich(t *testing.T) {
	fake := http.NewServeMux()
	fake.HandleFunc("/3/configuration", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "clave-buena" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{}`))
	})
	fake.HandleFunc("/3/search/movie", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"id":603}]}`))
	})
	fake.HandleFunc("/3/movie/603", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":603,"title":"Matrix","original_title":"The Matrix","overview":"Un hacker descubre la verdad.",
			"release_date":"1999-03-30","runtime":136,"vote_average":8.2,"imdb_id":"tt0133093","poster_path":"/p.jpg",
			"genres":[{"id":28,"name":"Acción"},{"id":878,"name":"Ciencia ficción"}],
			"release_dates":{"results":[{"iso_3166_1":"ES","release_dates":[{"certification":"13"}]}]}}`))
	})
	var images atomic.Int32
	fake.HandleFunc("/img/w500/p.jpg", func(w http.ResponseWriter, r *http.Request) { images.Add(1); w.Write([]byte("JPEGDATA")) })
	ts := httptest.NewServer(fake)
	defer ts.Close()

	e := newE1Env(t, ts.URL)
	ctx := context.Background()
	if c := e.call(t, "PUT", "/api/v1/admin/metadata/tmdb", e.token, map[string]string{"api_key": "mala"}, nil); c != 422 {
		t.Fatalf("una clave rechazada debería dar 422, dio %d", c)
	}
	if c := e.call(t, "PUT", "/api/v1/admin/metadata/tmdb", e.token, map[string]string{"api_key": "clave-buena"}, nil); c != 200 {
		t.Fatalf("guardar la clave: %d", c)
	}
	if st, err := os.Stat(filepath.Join(e.configDir, "secrets", "tmdb.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("la clave debe guardarse con 0600: %v", err)
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Pelis", "path": filepath.Join(e.mediaRoot, "Peliculas")}, &created)
	e.scan(t, created.Library.ID)
	st := &store.Store{Pool: e.pool}
	// El usuario bloqueó el título: TMDB no debe cambiarlo.
	if _, err := e.pool.Exec(ctx, `update media_items set locked_fields = '{title}'`); err != nil {
		t.Fatal(err)
	}
	pending, _ := st.ItemsNeedingMetadata(ctx, created.Library.ID, 10)
	if len(pending) != 1 {
		t.Fatalf("pendientes: %d", len(pending))
	}
	tm := metadata.New(ts.URL, ts.URL+"/img", e.configDir, st)
	if ok, err := tm.Enrich(ctx, pending[0]); err != nil || !ok {
		t.Fatalf("Enrich: %v %v", ok, err)
	}
	var title, overview string
	var year, ratingAge int
	_ = e.pool.QueryRow(ctx, `select title, overview, year, rating_age from media_items`).Scan(&title, &overview, &year, &ratingAge)
	if title != "Prueba sintética" || !strings.HasPrefix(overview, "Un hacker") || year != 1999 || ratingAge != 13 {
		t.Fatalf("metadatos aplicados mal: %q %q %d %d", title, overview, year, ratingAge)
	}
	img, err := st.ImageFor(ctx, store.Viewer{Admin: true}, pending[0].ID, "poster")
	if err != nil {
		t.Fatal(err)
	}
	// Hay póster local y de TMDB: se prefiere el local.
	if img.Source != "local" {
		t.Fatalf("debería preferirse el póster local, es %s", img.Source)
	}
	var tmdbPoster string
	if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, pending[0].ID).Scan(&tmdbPoster); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(e.configDir, "metadata", tmdbPoster)); err != nil || string(raw) != "JPEGDATA" {
		t.Fatalf("póster de TMDB no guardado: %v", err)
	}
	var cached int
	_ = e.pool.QueryRow(ctx, `select count(*) from metadata_cache where provider = 'tmdb' and expires_at <= fetched_at + interval '180 days'`).Scan(&cached)
	if cached < 2 {
		t.Fatalf("la caché de TMDB debería guardar búsqueda y ficha: %d", cached)
	}

	// Géneros: se guardan con la ficha y, en instalaciones anteriores a la migración 00004 (genres null), se rellenan
	// solos sin volver a descargar imágenes.
	var genres []string
	_ = e.pool.QueryRow(ctx, `select genres from media_items`).Scan(&genres)
	if strings.Join(genres, ",") != "Acción,Ciencia ficción" {
		t.Fatalf("géneros: %v", genres)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set genres = null`); err != nil {
		t.Fatal(err)
	}
	before := images.Load()
	need, _ := st.ItemsNeedingGenres(ctx, created.Library.ID, 10)
	if len(need) != 1 {
		t.Fatalf("pendientes de géneros: %d", len(need))
	}
	if err := tm.FillGenres(ctx, need[0]); err != nil {
		t.Fatal(err)
	}
	_ = e.pool.QueryRow(ctx, `select genres from media_items`).Scan(&genres)
	if len(genres) != 2 || images.Load() != before {
		t.Fatalf("relleno de géneros: %v, imágenes descargadas de nuevo: %d", genres, images.Load()-before)
	}
	if need, _ = st.ItemsNeedingGenres(ctx, created.Library.ID, 10); len(need) != 0 {
		t.Fatal("tras rellenar no debe quedar nada pendiente")
	}
}
