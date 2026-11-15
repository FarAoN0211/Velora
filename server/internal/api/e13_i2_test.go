package api

// E1.3 I2 · Identificar y Editar imágenes, contra un TMDB simulado y PostgreSQL de verdad: nada se aplica sin elección,
// un fallo del proveedor no cambia nada, las elecciones manuales sobreviven a escaneos y actualizaciones, y ni los
// archivos, ni las rutas, ni el progreso cambian. Los medios remotos y las series son sintéticos.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/audit"
	"velora/internal/backup"
	"velora/internal/config"
	"velora/internal/db"
	"velora/internal/jobs"
	"velora/internal/metadata"
	"velora/internal/secrets"
	"velora/internal/store"
)

func jpegBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// fakeTMDB simula el proveedor: dos películas homónimas (años distintos), una serie y sus galerías. failDetails y
// failImages provocan caídas para comprobar que no se aplica nada a medias.
type fakeTMDB struct {
	*httptest.Server
	failDetails, failImages atomic.Bool
	failOne                 atomic.Value // nombre de una imagen concreta que falla
	imageGen                atomic.Int32 // cambia el contenido de todas las imágenes (TMDB las actualiza)
	repaired                atomic.Bool  // la ficha 503 pasa de incompleta a correcta
}

func newFakeTMDB(t *testing.T) *fakeTMDB {
	t.Helper()
	f := &fakeTMDB{}
	movie := func(id int, title, date string) string {
		return fmt.Sprintf(`{"id":%d,"title":%q,"original_title":%q,"overview":"Sinopsis de %s.","tagline":"Lema de %s","release_date":%q,"runtime":100,
			"credits":{"cast":[{"id":100,"name":"Actriz de prueba","character":"Protagonista","profile_path":"/actriz.jpg"}],"crew":[{"id":101,"name":"Director de prueba","job":"Director"}]},"production_companies":[{"name":"Estudio de prueba"}],"production_countries":[{"name":"España"}],"original_language":"es","status":"Released","vote_average":7.5,"poster_path":"/p%d.jpg","backdrop_path":"/b%d.jpg","genres":[{"name":"Drama"}],"imdb_id":"tt%07d"}`, id, title, title, title, title, date, id, id, id)
	}
	logo := jpegBytes(t, 400, 100, color.RGBA{B: 200, A: 255})
	// Cada nombre y versión da una imagen distinta: las pruebas comparan el contenido, no solo las filas.
	tint := func(name string) color.Color {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", name, f.imageGen.Load())))
		return color.RGBA{R: h[0], G: h[1], B: h[2], A: 255}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/3/person/100/combined_credits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cast":[{"id":20,"media_type":"movie"},{"id":77,"media_type":"tv"},{"id":999999,"media_type":"movie"}],"crew":[]}`))
	})
	mux.HandleFunc("/3/person/101/combined_credits", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"cast":[],"crew":[{"id":20,"media_type":"movie"}]}`))
	})
	// Respuestas 200 que no son la ficha pedida: nunca deben aplicarse.
	mux.HandleFunc("/3/movie/500", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	mux.HandleFunc("/3/movie/501", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":999,"title":"Otra película"}`)) })
	mux.HandleFunc("/3/movie/502", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":502,"title":"  "}`)) })
	mux.HandleFunc("/3/tv/500", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":500}`)) })
	// El título en el campo del otro tipo: una película con «name» y una serie con «title» se quedarían sin título.
	mux.HandleFunc("/3/movie/504", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":504,"name":"Otra ficha"}`)) })
	mux.HandleFunc("/3/tv/501", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"id":501,"title":"Otra ficha"}`)) })
	mux.HandleFunc("/3/movie/503", func(w http.ResponseWriter, r *http.Request) {
		if f.repaired.Load() {
			w.Write([]byte(movie(503, "Reparada", "2003-01-01")))
			return
		}
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/3/configuration", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	mux.HandleFunc("/3/search/movie", func(w http.ResponseWriter, r *http.Request) {
		if f.failDetails.Load() {
			w.WriteHeader(500)
			return
		}
		switch q := r.URL.Query(); {
		case q.Get("query") == "nadaquever":
			w.Write([]byte(`{"results":[]}`))
		case q.Get("year") == "1990":
			w.Write([]byte(`{"results":[{"id":90,"title":"Homónima","original_title":"Same","release_date":"1990-01-01","overview":"La antigua.","poster_path":"/p90.jpg"}]}`))
		default:
			w.Write([]byte(`{"results":[{"id":20,"title":"Homónima","original_title":"Same","release_date":"2020-01-01","overview":"La nueva.","poster_path":"/p20.jpg"},
				{"id":90,"title":"Homónima","original_title":"Same","release_date":"1990-01-01","overview":"La antigua.","poster_path":"/p90.jpg"}]}`))
		}
	})
	for id, spec := range map[int][2]string{20: {"Homónima", "2020-01-01"}, 90: {"Homónima", "1990-01-01"}, 1: {"Equivocada", "2001-01-01"}} {
		mux.HandleFunc(fmt.Sprintf("/3/movie/%d", id), func(w http.ResponseWriter, r *http.Request) {
			if f.failDetails.Load() {
				w.WriteHeader(500)
				return
			}
			w.Write([]byte(movie(id, spec[0], spec[1])))
		})
	}
	mux.HandleFunc("/3/movie/20/images", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posters":[{"file_path":"/gp1.jpg","width":200,"height":300,"iso_639_1":"es"},{"file_path":"/rara.jpg","width":900,"height":300}],
			"backdrops":[{"file_path":"/gb1.jpg","width":640,"height":360}],
			"logos":[{"file_path":"/gl1.png","width":400,"height":100},{"file_path":"/gl2.svg","width":400,"height":100}]}`))
	})
	mux.HandleFunc("/3/search/tv", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"id":77,"name":"La Serie","original_name":"The Show","first_air_date":"2015-05-05","overview":"De una serie.","poster_path":"/s77.jpg"}]}`))
	})
	mux.HandleFunc("/3/tv/77", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":77,"name":"La Serie","original_name":"The Show","first_air_date":"2015-05-05","overview":"De una serie.","episode_run_time":[45],
			"poster_path":"/s77.jpg","backdrop_path":"/sb77.jpg","genres":[{"name":"Drama"}],"external_ids":{"imdb_id":"tt0000077"},
			"content_ratings":{"results":[{"iso_3166_1":"ES","rating":"16"}]}}`))
	})
	mux.HandleFunc("/3/tv/77/images", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posters":[{"file_path":"/gsp.jpg","width":200,"height":300}],"backdrops":[{"file_path":"/gsb.jpg","width":640,"height":360}],"logos":[]}`))
	})
	mux.HandleFunc("/3/tv/77/season/2/images", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"posters":[{"file_path":"/gs2.jpg","width":200,"height":300}]}`))
	})
	mux.HandleFunc("/3/tv/77/season/2/episode/3/images", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"stills":[{"file_path":"/ge3.jpg","width":640,"height":360}]}`))
	})
	mux.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) {
		if f.failImages.Load() {
			w.WriteHeader(500)
			return
		}
		name := filepath.Base(r.URL.Path)
		if one, _ := f.failOne.Load().(string); one == name {
			w.WriteHeader(500)
			return
		}
		switch {
		case strings.HasPrefix(name, "gl"):
			w.Write(logo)
		case strings.HasPrefix(name, "gb"), strings.HasPrefix(name, "gsb"), strings.HasPrefix(name, "ge"), strings.HasPrefix(name, "b"), strings.HasPrefix(name, "sb"):
			w.Write(jpegBytes(t, 640, 360, tint(name)))
		default:
			w.Write(jpegBytes(t, 200, 300, tint(name)))
		}
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

type i2Env struct {
	*e1Env
	fake     *fakeTMDB
	st       *store.Store
	lib      store.Library
	item     uuid.UUID
	base     string
	tm       *metadata.TMDB
	userTok  string
	mediaSum string
}

// mediaState resume todos los archivos de medios (ruta, tamaño, fecha y contenido): nada de esto debe cambiar nunca.
func mediaState(t *testing.T, root string) string {
	t.Helper()
	var out strings.Builder
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(&out, "%s %d %d", p, fi.Size(), fi.ModTime().UnixNano())
		if !fi.IsDir() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(&out, " %x", sha256.Sum256(raw))
		}
		out.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func newI2Env(t *testing.T) *i2Env {
	t.Helper()
	fake := newFakeTMDB(t)
	e := &i2Env{e1Env: newE1Env(t, fake.URL), fake: fake}
	e.st = &store.Store{Pool: e.pool}
	e.tm = metadata.New(fake.URL, fake.URL+"/img", e.configDir, e.st)
	if c := e.call(t, "PUT", "/api/v1/admin/metadata/tmdb", e.token, map[string]string{"api_key": "clave"}, nil); c != 200 {
		t.Fatalf("clave: %d", c)
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Pelis", "path": filepath.Join(e.mediaRoot, "Peliculas")}, &created); c != 201 {
		t.Fatalf("biblioteca: %d", c)
	}
	e.lib = created.Library
	e.scan(t, e.lib.ID)
	if err := e.pool.QueryRow(context.Background(), `select id from media_items limit 1`).Scan(&e.item); err != nil {
		t.Fatal(err)
	}
	e.base = "/api/v1/admin/items/" + e.item.String()
	var userID uuid.UUID
	if c := e.call(t, "POST", "/api/v1/admin/users", e.token, map[string]string{"username": "lucia", "display_name": "Lucía", "password": "otra-contraseña-larga"}, &struct {
		ID *uuid.UUID `json:"id"`
	}{&userID}); c != 201 {
		t.Fatalf("usuario: %d", c)
	}
	if c := e.call(t, "PUT", "/api/v1/admin/users/"+userID.String()+"/libraries", e.token, map[string]any{"library_ids": []uuid.UUID{e.lib.ID}}, nil); c != 204 {
		t.Fatalf("permisos: %d", c)
	}
	var sess struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "lucia", "password": "otra-contraseña-larga"}, &sess); c != 200 {
		t.Fatalf("login: %d", c)
	}
	e.userTok = sess.Token
	e.mediaSum = mediaState(t, e.mediaRoot)
	return e
}

func (e *i2Env) meta(t *testing.T, base string) store.EditableMetadata {
	t.Helper()
	var r struct {
		Metadata store.EditableMetadata `json:"metadata"`
	}
	if c := e.call(t, "GET", base+"/metadata", e.token, nil, &r); c != 200 {
		t.Fatalf("metadatos: %d", c)
	}
	return r.Metadata
}

func (e *i2Env) identify(t *testing.T, base string, tmdbID int, images bool) int {
	t.Helper()
	return e.call(t, "POST", base+"/identify", e.token, map[string]any{"tmdb_id": tmdbID, "replace_images": images}, nil)
}

// upload envía un archivo como lo hace el navegador y devuelve el estado y la versión de las imágenes.
func (e *i2Env) upload(t *testing.T, base, token, kind string, raw []byte) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("kind", kind)
	w, _ := mw.CreateFormFile("file", "imagen.jpg")
	w.Write(raw)
	mw.Close()
	req, _ := http.NewRequest("POST", e.server.URL+base+"/images/upload", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		ImageRev string `json:"image_rev"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out.ImageRev
}

func (e *i2Env) image(t *testing.T, token, id, kind, query string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.server.URL+"/api/v1/items/"+id+"/images/"+kind+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return res, b.Bytes()
}

// Película identificada mal: búsqueda con homónimos sin aplicar nada, comparación, confirmación, deshacer y restablecer.
func TestIdentifyCorrectsWrongMatch(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	// Identificación automática equivocada (la película 1), con su IMDb.
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"1"}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	it, _, err := e.st.ItemForRefresh(ctx, e.item)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.tm.Refresh(ctx, it, false); err != nil || !ok {
		t.Fatalf("refresh inicial: %v %v", ok, err)
	}
	before := e.meta(t, e.base)
	if before.ProviderIDs["tmdb"] != "1" || before.Title != "Equivocada" {
		t.Fatalf("preparación: %+v", before)
	}

	type cand struct {
		TMDBID    int    `json:"tmdb_id"`
		Title     string `json:"title"`
		Year      int    `json:"year"`
		Overview  string `json:"overview"`
		Poster    string `json:"poster_path"`
		InLibrary bool   `json:"in_library"`
	}
	var res struct {
		Candidates []cand `json:"candidates"`
	}
	// Títulos repetidos: dos resultados que se distinguen por año, sinopsis y cartel. Buscar no cambia nada.
	if c := e.call(t, "GET", e.base+"/identify/search?q=Hom%C3%B3nima", e.token, nil, &res); c != 200 || len(res.Candidates) != 2 {
		t.Fatalf("búsqueda: %d %+v", c, res)
	}
	a, b := res.Candidates[0], res.Candidates[1]
	if a.Title != b.Title || a.Year == b.Year || a.Overview == b.Overview || a.Poster == b.Poster || a.Poster == "" {
		t.Fatalf("los homónimos no se distinguen: %+v", res.Candidates)
	}
	if c := e.call(t, "GET", e.base+"/identify/search?q=Hom%C3%B3nima&year=1990", e.token, nil, &res); c != 200 || len(res.Candidates) != 1 || res.Candidates[0].TMDBID != 90 {
		t.Fatalf("búsqueda por año: %d %+v", c, res)
	}
	if c := e.call(t, "GET", e.base+"/identify/search?tmdb_id=20", e.token, nil, &res); c != 200 || len(res.Candidates) != 1 || res.Candidates[0].Year != 2020 {
		t.Fatalf("búsqueda por ID: %d %+v", c, res)
	}
	if c := e.call(t, "GET", e.base+"/identify/search?q=tmdb-90", e.token, nil, &res); c != 200 || res.Candidates[0].TMDBID != 90 {
		t.Fatalf("atajo tmdb-: %d %+v", c, res)
	}
	if c := e.call(t, "GET", e.base+"/identify/search?tmdb_id=abc", e.token, nil, nil); c != 400 {
		t.Fatalf("ID no numérico: %d", c)
	}
	var prev struct {
		Current   map[string]any `json:"current"`
		Candidate map[string]any `json:"candidate"`
	}
	if c := e.call(t, "GET", e.base+"/identify/preview?tmdb_id=20", e.token, nil, &prev); c != 200 || prev.Candidate["title"] != "Homónima" || prev.Current["title"] != before.Title {
		t.Fatalf("comparación: %d %+v", c, prev)
	}
	if got := e.meta(t, e.base); got.Title != before.Title || got.MatchSource != "auto" {
		t.Fatalf("buscar o comparar ha cambiado la ficha: %+v", got)
	}

	// Un campo editado a mano se conserva al identificar, salvo que se pida lo contrario.
	edit := e.meta(t, e.base)
	edit.Tagline = "Mi lema"
	if c := e.call(t, "PATCH", e.base+"/metadata", e.token, edit, nil); c != 200 {
		t.Fatalf("editar: %d", c)
	}
	var wrongPoster string
	if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, e.item).Scan(&wrongPoster); err != nil {
		t.Fatal(err)
	}
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	got := e.meta(t, e.base)
	if got.Title != "Homónima" || *got.Year != 2020 || got.Overview != "Sinopsis de Homónima." || got.MatchSource != "manual" || got.Tagline != "Mi lema" {
		t.Fatalf("identificación: %+v", got)
	}
	if got.ProviderIDs["tmdb"] != "20" || got.ProviderIDs["imdb"] != "tt0000020" {
		t.Fatalf("los IDs de la película anterior no deben quedar: %+v", got.ProviderIDs)
	}
	var tmdbPoster string
	if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, e.item).Scan(&tmdbPoster); err != nil || tmdbPoster == wrongPoster {
		t.Fatalf("carátula nueva: %q %v", tmdbPoster, err)
	}
	if c := e.call(t, "POST", e.base+"/identify", e.token, map[string]any{"tmdb_id": 90, "replace_locked": true, "replace_images": true}, nil); c != 200 {
		t.Fatalf("identificar sustituyendo candados: %d", c)
	}
	if got = e.meta(t, e.base); got.Tagline != "Lema de Homónima" {
		t.Fatalf("replace_locked: %+v", got)
	}

	// Deshacer vuelve exactamente a lo anterior, dos veces.
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 {
		t.Fatalf("deshacer: %d", c)
	}
	if got = e.meta(t, e.base); got.ProviderIDs["tmdb"] != "20" || got.Tagline != "Mi lema" {
		t.Fatalf("deshacer 1: %+v", got)
	}
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 {
		t.Fatalf("deshacer: %d", c)
	}
	if got = e.meta(t, e.base); got.ProviderIDs["tmdb"] != "1" || got.ProviderIDs["imdb"] != "tt0000001" || got.Title != "Equivocada" || got.Tagline != "Mi lema" || got.MatchSource != "auto" {
		t.Fatalf("deshacer 2: %+v", got)
	}
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 409 {
		t.Fatalf("sin nada que deshacer: %d", c)
	}

	// Restablecer: vuelve al nombre de la carpeta, sin elección manual ni candados.
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	if c := e.call(t, "POST", e.base+"/identify/reset", e.token, map[string]bool{"restore_images": false}, nil); c != 200 {
		t.Fatalf("restablecer: %d", c)
	}
	got = e.meta(t, e.base)
	if got.Title != "Prueba sintética" || *got.Year != 2020 || got.MatchSource != "auto" || got.ProviderIDs["tmdb"] != "" || len(got.LockedFields) != 0 {
		t.Fatalf("restablecer: %+v", got)
	}
	var job int
	if err := e.pool.QueryRow(ctx, `select count(*) from jobs where kind = 'item_refresh'`).Scan(&job); err != nil || job == 0 {
		t.Fatalf("restablecer debe pedir de nuevo la ficha a TMDB: %d %v", job, err)
	}
	if s := mediaState(t, e.mediaRoot); s != e.mediaSum {
		t.Fatalf("los medios han cambiado:\n%s\n%s", e.mediaSum, s)
	}
}

// Película sin identificar: cero resultados y fallos del proveedor dejan la ficha intacta; se puede editar a mano.
func TestIdentifyFailuresLeaveItemIntact(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	before := e.meta(t, e.base)
	if before.ProviderIDs["tmdb"] != "" {
		t.Fatalf("debería estar sin identificar: %+v", before.ProviderIDs)
	}
	same := func(step string) {
		t.Helper()
		got := e.meta(t, e.base)
		if got.Title != before.Title || got.ProviderIDs["tmdb"] != "" || got.MatchSource != "auto" || got.CanUndo {
			t.Fatalf("%s: la ficha ha cambiado: %+v", step, got)
		}
		var n int
		_ = e.pool.QueryRow(ctx, `select count(*) from item_images where item_id = $1 and source = 'tmdb'`, e.item).Scan(&n)
		if n != 0 {
			t.Fatalf("%s: %d imágenes de TMDB registradas", step, n)
		}
	}
	var res struct {
		Candidates []any `json:"candidates"`
	}
	if c := e.call(t, "GET", e.base+"/identify/search?q=nadaquever", e.token, nil, &res); c != 200 || len(res.Candidates) != 0 {
		t.Fatalf("cero resultados: %d %+v", c, res)
	}
	same("cero resultados")
	if c := e.identify(t, e.base, 999, true); c != 404 {
		t.Fatalf("ID inexistente: %d", c)
	}
	same("ID inexistente")
	e.fake.failDetails.Store(true)
	if c := e.call(t, "GET", e.base+"/identify/search?q=caido", e.token, nil, nil); c != 502 {
		t.Fatalf("TMDB caído al buscar: %d", c)
	}
	if c := e.identify(t, e.base, 20, true); c != 502 {
		t.Fatalf("TMDB caído al aplicar: %d", c)
	}
	same("TMDB caído")
	e.fake.failDetails.Store(false)
	e.fake.failImages.Store(true) // la ficha llega, pero una imagen falla: no se aplica nada
	if c := e.identify(t, e.base, 20, true); c != 502 {
		t.Fatalf("imagen caída al aplicar: %d", c)
	}
	same("imagen caída")
	e.fake.failImages.Store(false)
	// Ficha bloqueada entera: no se identifica.
	edit := before
	edit.Locked = true
	if c := e.call(t, "PATCH", e.base+"/metadata", e.token, edit, nil); c != 200 {
		t.Fatalf("bloquear: %d", c)
	}
	if c := e.identify(t, e.base, 20, true); c != 409 {
		t.Fatalf("ficha bloqueada: %d", c)
	}
	same("ficha bloqueada")
	// Sin coincidencia: edición manual, que sobrevive.
	edit.Locked, edit.Title = false, "Mi película casera"
	if c := e.call(t, "PATCH", e.base+"/metadata", e.token, edit, nil); c != 200 {
		t.Fatalf("editar: %d", c)
	}
	e.scan(t, e.lib.ID)
	if got := e.meta(t, e.base); got.Title != "Mi película casera" {
		t.Fatalf("edición manual tras escanear: %+v", got)
	}
	// Sin clave de TMDB: aviso claro; la edición manual sigue disponible.
	if c := e.call(t, "DELETE", "/api/v1/admin/metadata/tmdb", e.token, nil, nil); c != 204 && c != 200 {
		t.Fatalf("quitar clave: %d", c)
	}
	if c := e.call(t, "GET", e.base+"/identify/search?q=x", e.token, nil, nil); c != 409 {
		t.Fatalf("sin clave: %d", c)
	}
}

// Galería: categorías separadas y solo lo que TMDB ofrece para ese tipo.
func TestImageGalleryAndSelection(t *testing.T) {
	e := newI2Env(t)
	var gal struct {
		Gallery    metadata.Gallery `json:"gallery"`
		Identified bool             `json:"identified"`
	}
	if c := e.call(t, "GET", e.base+"/images/gallery", e.token, nil, &gal); c != 200 || gal.Identified {
		t.Fatalf("sin identificar no hay galería: %d %+v", c, gal)
	}
	if c := e.identify(t, e.base, 20, false); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	if c := e.call(t, "GET", e.base+"/images/gallery", e.token, nil, &gal); c != 200 || !gal.Identified {
		t.Fatalf("galería: %d", c)
	}
	if len(gal.Gallery.Posters) != 1 || len(gal.Gallery.Backdrops) != 1 || len(gal.Gallery.Logos) != 1 || gal.Gallery.Posters[0].FilePath != "/gp1.jpg" || gal.Gallery.Logos[0].FilePath != "/gl1.png" {
		t.Fatalf("categorías: %+v", gal.Gallery)
	}
	sel := func(kind, path string) int {
		return e.call(t, "POST", e.base+"/images/select", e.token, map[string]string{"kind": kind, "file_path": path}, nil)
	}
	for _, c := range []struct{ kind, path string }{
		{"poster", "/gb1.jpg"},          // un fondo no es un cartel
		{"backdrop", "/gp1.jpg"},        // ni un cartel, un fondo
		{"poster", "/rara.jpg"},         // la lista marca horizontal lo que dice ser cartel
		{"logo", "/gl2.svg"},            // SVG: fuera
		{"poster", "/inventada.jpg"},    // ruta que TMDB no ofrece
		{"poster", "/../../etc/passwd"}, // manipulada
		{"disc", "/gb1.jpg"},            // tipo no personalizable
	} {
		if got := sel(c.kind, c.path); got != 422 {
			t.Errorf("%s %s: %d, esperaba 422", c.kind, c.path, got)
		}
	}
	for _, c := range []struct{ kind, path string }{{"poster", "/gp1.jpg"}, {"backdrop", "/gb1.jpg"}, {"banner", "/gb1.jpg"}, {"logo", "/gl1.png"}, {"thumb", "/gb1.jpg"}} {
		if got := sel(c.kind, c.path); got != 200 {
			t.Fatalf("%s: %d", c.kind, got)
		}
	}
	var srcs struct {
		Slots []store.ImageSlot `json:"slots"`
	}
	e.call(t, "GET", e.base+"/images", e.token, nil, &srcs)
	for _, s := range srcs.Slots {
		want := ""
		if slices.Contains([]string{"poster", "backdrop", "banner", "logo", "thumb"}, s.Kind) {
			want = "user"
		}
		if s.Source != want {
			t.Fatalf("%s: origen %q, esperaba %q", s.Kind, s.Source, want)
		}
	}
	// Se sirven todos los tipos, y la ficha y las tarjetas lo saben.
	for _, k := range []string{"poster", "backdrop", "banner", "logo"} {
		if res, _ := e.image(t, e.userTok, e.item.String(), k, "", nil); res.StatusCode != 200 {
			t.Fatalf("servir %s: %d", k, res.StatusCode)
		}
	}
	var item struct {
		Item store.ItemDetail `json:"item"`
	}
	if c := e.call(t, "GET", "/api/v1/items/"+e.item.String(), e.token, nil, &item); c != 200 || !item.Item.HasLogo || !item.Item.HasBanner || item.Item.ImageRev == "" {
		t.Fatalf("ficha: %d %+v", c, item.Item.ItemCard)
	}
	// Vista previa: solo rutas y tamaños válidos.
	req := func(q string) int { return e.call(t, "GET", "/api/v1/admin/metadata/thumb?"+q, e.token, nil, nil) }
	if c := req("path=/etc/passwd"); c != 400 {
		t.Errorf("ruta manipulada: %d", c)
	}
	if c := req("path=/gp1.jpg&size=original"); c != 400 {
		t.Errorf("tamaño no admitido: %d", c)
	}
}

// Subida: valida de verdad, guarda solo en metadatos y se ve al momento, en cualquier cliente y sin esperar a la caché.
func TestImageUploadValidationAndCache(t *testing.T) {
	e := newI2Env(t)
	id := e.item.String()
	// Antes: la carátula de la carpeta. La vuelve a pedir otro dispositivo con lo que ya tenía guardado.
	res0, old := e.image(t, e.userTok, id, "poster", "", nil)
	if res0.StatusCode != 200 || !strings.Contains(res0.Header.Get("Cache-Control"), "no-cache") || res0.Header.Get("ETag") == "" {
		t.Fatalf("imagen sin versión debe revalidarse: %d %v", res0.StatusCode, res0.Header)
	}
	res304, _ := e.image(t, e.userTok, id, "poster", "", map[string]string{"If-None-Match": res0.Header.Get("ETag")})
	if res304.StatusCode != 304 {
		t.Fatalf("sin cambios debe responder 304: %d", res304.StatusCode)
	}

	valid := jpegBytes(t, 400, 600, color.RGBA{R: 10, G: 200, B: 10, A: 255})
	for name, c := range map[string]struct {
		kind string
		raw  []byte
		want int
	}{
		"texto disfrazado":       {"poster", []byte("#!/bin/sh\necho hola\n"), 422},
		"ejecutable":             {"poster", append([]byte("\x7fELF"), make([]byte, 500)...), 422},
		"HTML":                   {"poster", []byte("<html><script>alert(1)</script></html>"), 422},
		"horizontal como cartel": {"poster", jpegBytes(t, 640, 360, color.Black), 422},
		"vertical como fondo":    {"backdrop", valid, 422},
		"demasiado grande":       {"poster", make([]byte, 11<<20), 413},
		"tipo manipulado":        {"../../poster", valid, 422},
		"tipo no admitido":       {"disc", valid, 422},
		"miniatura en película":  {"thumb", jpegBytes(t, 640, 360, color.Black), 200},
	} {
		if got, _ := e.upload(t, e.base, e.token, c.kind, c.raw); got != c.want {
			t.Errorf("%s: %d, esperaba %d", name, got, c.want)
		}
	}
	if _, after := e.image(t, e.userTok, id, "poster", "", nil); !bytes.Equal(after, old) {
		t.Fatal("una subida rechazada ha cambiado la imagen")
	}

	code, rev := e.upload(t, e.base, e.token, "poster", valid)
	if code != 200 || rev == "" {
		t.Fatalf("subir: %d %q", code, rev)
	}
	// Otro cliente, con la versión antigua guardada: recibe la nueva.
	res1, now := e.image(t, e.userTok, id, "poster", "", map[string]string{"If-None-Match": res0.Header.Get("ETag")})
	if res1.StatusCode != 200 || bytes.Equal(now, old) {
		t.Fatalf("el otro cliente sigue con la imagen anterior: %d", res1.StatusCode)
	}
	if bytes.Equal(now, valid) {
		t.Fatal("se ha guardado el archivo tal cual, sin procesar")
	}
	if res2, _ := e.image(t, e.userTok, id, "poster", "?v="+rev, nil); !strings.Contains(res2.Header.Get("Cache-Control"), "max-age") {
		t.Fatalf("con versión se puede guardar: %v", res2.Header)
	}
	var item struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+id, e.userTok, nil, &item)
	if item.Item.ImageRev != rev {
		t.Fatalf("la tarjeta no lleva la versión nueva: %q %q", item.Item.ImageRev, rev)
	}
	// Solo en /config/metadata, con el contenido procesado; los medios siguen igual.
	var path string
	if err := e.pool.QueryRow(context.Background(), `select path from item_images where item_id = $1 and source = 'user' and kind = 'poster'`, e.item).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.configDir, "metadata", path)); err != nil {
		t.Fatalf("archivo guardado: %v", err)
	}
	if s := mediaState(t, e.mediaRoot); s != e.mediaSum {
		t.Fatal("la subida ha tocado los medios")
	}
	// Sustituirla borra la anterior; restaurar vuelve a la automática y borra la propia.
	code, rev2 := e.upload(t, e.base, e.token, "poster", jpegBytes(t, 400, 600, color.RGBA{R: 200, A: 255}))
	if code != 200 || rev2 == rev {
		t.Fatalf("sustituir: %d %q", code, rev2)
	}
	if _, err := os.Stat(filepath.Join(e.configDir, "metadata", path)); !os.IsNotExist(err) {
		t.Fatalf("la imagen sustituida sigue en disco: %v", err)
	}
	if c := e.call(t, "DELETE", e.base+"/images/poster", e.token, nil, nil); c != 200 {
		t.Fatalf("restaurar: %d", c)
	}
	if _, after := e.image(t, e.userTok, id, "poster", "", nil); !bytes.Equal(after, old) {
		t.Fatal("restaurar no vuelve a la imagen automática")
	}
	if c := e.call(t, "DELETE", e.base+"/images/poster", e.token, nil, nil); c != 404 {
		t.Fatalf("restaurar sin imagen propia: %d", c)
	}
}

// Persistencia: lo elegido a mano sobrevive a escaneo, «Actualizar» en sus dos modos, un trabajo en curso y un reinicio.
func TestManualChoicesSurviveRefreshScanAndRestart(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	stale, _, err := e.st.ItemForRefresh(ctx, e.item) // un trabajo leyó la ficha antes de la identificación manual
	if err != nil {
		t.Fatal(err)
	}
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	if c, _ := e.upload(t, e.base, e.token, "poster", jpegBytes(t, 400, 600, color.White)); c != 200 {
		t.Fatalf("subir: %d", c)
	}
	if c := e.call(t, "POST", e.base+"/images/select", e.token, map[string]string{"kind": "backdrop", "file_path": "/gb1.jpg"}, nil); c != 200 {
		t.Fatalf("elegir: %d", c)
	}
	edit := e.meta(t, e.base)
	edit.Overview = "Sinopsis mía."
	if c := e.call(t, "PATCH", e.base+"/metadata", e.token, edit, nil); c != 200 {
		t.Fatalf("editar: %d", c)
	}
	_, poster := e.image(t, e.token, e.item.String(), "poster", "", nil)
	_, backdrop := e.image(t, e.token, e.item.String(), "backdrop", "", nil)
	check := func(step string) {
		t.Helper()
		m := e.meta(t, e.base)
		if m.ProviderIDs["tmdb"] != "20" || m.MatchSource != "manual" || m.Title != "Homónima" || m.Overview != "Sinopsis mía." {
			t.Fatalf("%s: %+v", step, m)
		}
		_, p := e.image(t, e.token, e.item.String(), "poster", "", nil)
		_, b := e.image(t, e.token, e.item.String(), "backdrop", "", nil)
		if !bytes.Equal(p, poster) || !bytes.Equal(b, backdrop) {
			t.Fatalf("%s: han cambiado las imágenes elegidas", step)
		}
	}
	tmdbRow := func() string {
		var p string
		_ = e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, e.item).Scan(&p)
		return p
	}
	tmdbPoster := tmdbRow()
	// El trabajo que empezó antes llega tarde: no puede volver a la identificación anterior ni tocar imágenes.
	if ok, err := e.tm.Refresh(ctx, stale, false); err != nil || !ok {
		t.Fatalf("trabajo tardío: %v %v", ok, err)
	}
	check("trabajo tardío")
	if p := tmdbRow(); p != tmdbPoster || p == "" {
		t.Fatalf("el trabajo tardío ha cambiado la imagen de TMDB: %q, era %q", p, tmdbPoster)
	}
	for _, mode := range []bool{true, false} {
		it, _, err := e.st.ItemForRefresh(ctx, e.item)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := e.tm.Refresh(ctx, it, mode); err != nil || !ok {
			t.Fatalf("refresh %v: %v %v", mode, ok, err)
		}
		check(fmt.Sprintf("actualizar (solo lo que falta: %v)", mode))
	}
	e.scan(t, e.lib.ID)
	check("escaneo")

	// Reinicio simulado: otro servidor con otra conexión, los mismos datos y un cliente nuevo.
	cfg := config.Config{DatabaseURL: e.dbURL, SessionTTL: time.Hour, BackupDir: t.TempDir(), ConfigDir: e.configDir, Location: time.UTC, MediaRoots: []string{e.mediaRoot},
		MaxRemux: 2, TMDBAPIURL: e.fake.URL, TMDBImageURL: e.fake.URL + "/img"}
	key, _ := secrets.EnsureMasterKey(cfg.ConfigDir)
	srv := New(cfg, key, e.st, audit.New(e.pool), jobs.NewQueue(e.pool), nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	old := e.server
	e.server = ts
	defer func() { e.server = old }()
	check("reinicio")
	// Restaurar lo automático solo cuando el administrador lo pide.
	if c := e.call(t, "POST", e.base+"/identify/reset", e.token, map[string]bool{"restore_images": true}, nil); c != 200 {
		t.Fatalf("restablecer: %d", c)
	}
	if n, _ := e.st.CustomImageKinds(ctx, e.item); len(n) != 0 {
		t.Fatalf("restablecer con imágenes: quedan %v", n)
	}
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 {
		t.Fatalf("deshacer: %d", c)
	}
	check("deshacer el restablecimiento")
}

// Series sintéticas con temporadas y episodios: identificar la serie no toca la jerarquía; temporadas y episodios van por ella.
func TestSeriesIdentificationKeepsHierarchy(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	var pathID uuid.UUID
	if err := e.pool.QueryRow(ctx, `select library_path_id from media_items where id = $1`, e.item).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	insert := func(parent *uuid.UUID, kind, title, content, ids string) uuid.UUID {
		var id uuid.UUID
		if err := e.pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, parent_id, kind, title, sort_title, content_path, provider_ids)
			values ($1, $2, $3, $4, $5, $5, $6, $7) returning id`, e.lib.ID, pathID, parent, kind, title, content, ids).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	series := insert(nil, "series", "Serie mal", "La Serie (2015)", `{"tmdb":"1"}`)
	var seasons, episodes []uuid.UUID
	for s := 1; s <= 2; s++ {
		sid := insert(&series, "season", fmt.Sprintf("Temporada %d", s), fmt.Sprintf("La Serie (2015)/S%d", s), fmt.Sprintf(`{"tmdb_season":"%d"}`, s))
		seasons = append(seasons, sid)
		for n := 1; n <= 3; n++ {
			episodes = append(episodes, insert(&sid, "episode", fmt.Sprintf("Episodio %d", n), fmt.Sprintf("La Serie (2015)/S%d/E%d", s, n),
				fmt.Sprintf(`{"tmdb_season":"%d","tmdb_episode":"%d"}`, s, n)))
		}
	}
	tree := func() string {
		var out string
		if err := e.pool.QueryRow(ctx, `select string_agg(id::text || ':' || coalesce(parent_id::text, '') || ':' || content_path || ':' || title, ',' order by id)
			from media_items where id <> $1 and id <> $2`, e.item, series).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	base := "/api/v1/admin/items/" + series.String()
	before := tree()
	var res struct {
		Candidates []struct {
			TMDBID int    `json:"tmdb_id"`
			Title  string `json:"title"`
			Year   int    `json:"year"`
		} `json:"candidates"`
	}
	if c := e.call(t, "GET", base+"/identify/search?q=serie", e.token, nil, &res); c != 200 || len(res.Candidates) != 1 || res.Candidates[0].Title != "La Serie" || res.Candidates[0].Year != 2015 {
		t.Fatalf("buscar serie: %d %+v", c, res)
	}
	if c := e.identify(t, base, 77, true); c != 200 {
		t.Fatalf("identificar serie: %d", c)
	}
	m := e.meta(t, base)
	if m.Title != "La Serie" || *m.Year != 2015 || m.ProviderIDs["imdb"] != "tt0000077" || m.OfficialRating != "16" || m.MatchSource != "manual" {
		t.Fatalf("serie: %+v", m)
	}
	after := tree()
	if after != before {
		t.Fatalf("identificar la serie ha cambiado temporadas o episodios:\n%s\n%s", before, after)
	}
	for _, id := range []uuid.UUID{seasons[0], episodes[0]} {
		if c := e.call(t, "GET", "/api/v1/admin/items/"+id.String()+"/identify/search?q=x", e.token, nil, nil); c != 422 {
			t.Fatalf("temporada o episodio por su cuenta: %d", c)
		}
		if c := e.identify(t, "/api/v1/admin/items/"+id.String(), 77, false); c != 422 {
			t.Fatalf("identificar temporada o episodio: %d", c)
		}
	}
	// Datos e imágenes propios de temporadas y episodios, con su galería y con subida.
	season2 := "/api/v1/admin/items/" + seasons[1].String()
	episode := "/api/v1/admin/items/" + episodes[5].String() // T2E3
	var gal struct {
		Gallery    metadata.Gallery `json:"gallery"`
		Identified bool             `json:"identified"`
	}
	if c := e.call(t, "GET", season2+"/images/gallery", e.token, nil, &gal); c != 200 || !gal.Identified || gal.Gallery.Posters[0].FilePath != "/gs2.jpg" {
		t.Fatalf("galería temporada: %d %+v", c, gal)
	}
	if c := e.call(t, "POST", season2+"/images/select", e.token, map[string]string{"kind": "poster", "file_path": "/gs2.jpg"}, nil); c != 200 {
		t.Fatalf("cartel de temporada: %d", c)
	}
	if c := e.call(t, "GET", episode+"/images/gallery", e.token, nil, &gal); c != 200 || gal.Gallery.Stills[0].FilePath != "/ge3.jpg" {
		t.Fatalf("galería episodio: %d %+v", c, gal)
	}
	if c := e.call(t, "POST", episode+"/images/select", e.token, map[string]string{"kind": "thumb", "file_path": "/ge3.jpg"}, nil); c != 200 {
		t.Fatalf("miniatura de episodio: %d", c)
	}
	if c, _ := e.upload(t, season2, e.token, "thumb", jpegBytes(t, 640, 360, color.White)); c != 200 {
		t.Fatalf("subir miniatura de temporada: %d", c)
	}
	if c, _ := e.upload(t, episode, e.token, "poster", jpegBytes(t, 400, 600, color.White)); c != 422 {
		t.Fatalf("un episodio no tiene cartel: %d", c)
	}
	if res, _ := e.image(t, e.token, episodes[5].String(), "thumb", "", nil); res.StatusCode != 200 {
		t.Fatalf("servir miniatura: %d", res.StatusCode)
	}
	ep := e.meta(t, episode)
	ep.Title = "El de la boda"
	if c := e.call(t, "PATCH", episode+"/metadata", e.token, ep, nil); c != 200 {
		t.Fatalf("editar episodio: %d", c)
	}
	// Actualizar la serie no cambia sus episodios ni sus temporadas.
	it, _, err := e.st.ItemForRefresh(ctx, series)
	if err != nil || it.Kind != "series" {
		t.Fatalf("serie para actualizar: %+v %v", it, err)
	}
	if ok, err := e.tm.Refresh(ctx, it, false); err != nil || !ok {
		t.Fatalf("actualizar serie: %v %v", ok, err)
	}
	if got := e.meta(t, episode); got.Title != "El de la boda" {
		t.Fatalf("episodio: %+v", got)
	}
}

// Conservación: progreso, archivos, rutas y reproducción no cambian; también con una biblioteca remota de solo lectura.
func TestIdentifyKeepsProgressFilesAndReadOnlyMedia(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	// Simulación de un montaje de Telegram/Teldrive: ruta marcada como remota y carpetas sin permiso de escritura.
	if _, err := e.pool.Exec(ctx, `update library_paths set kind = 'remote_fuse' where library_id = $1`, e.lib.ID); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(e.mediaRoot, "Peliculas")
	movie := e.movieDir
	for _, d := range []string{movie, folder} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(folder, 0o755)
		_ = os.Chmod(movie, 0o755)
	})
	files := func() string {
		var out string
		if err := e.pool.QueryRow(ctx, `select string_agg(id::text || ':' || rel_path || ':' || size_bytes || ':' || library_path_id::text, ',' order by id) from media_files where item_id = $1`, e.item).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	filesBefore := files()
	var fileID uuid.UUID
	if err := e.pool.QueryRow(ctx, `select id from media_files where item_id = $1 limit 1`, e.item).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "PUT", "/api/v1/me/items/"+e.item.String()+"/progress", e.userTok, map[string]any{"file_id": fileID, "position_ms": 2000, "duration_ms": 6000, "client_time": time.Now()}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	progress := func() int64 {
		var pos int64
		if err := e.pool.QueryRow(ctx, `select position_ms from user_item_data where item_id = $1`, e.item).Scan(&pos); err != nil {
			t.Fatal(err)
		}
		return pos
	}
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	if c, _ := e.upload(t, e.base, e.token, "poster", jpegBytes(t, 400, 600, color.White)); c != 200 {
		t.Fatalf("subir: %d", c)
	}
	if c := e.call(t, "POST", e.base+"/identify/reset", e.token, map[string]bool{"restore_images": true}, nil); c != 200 {
		t.Fatalf("restablecer: %d", c)
	}
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 {
		t.Fatalf("deshacer: %d", c)
	}
	if progress() != 2000 {
		t.Fatalf("progreso perdido: %d", progress())
	}
	if files() != filesBefore {
		t.Fatalf("archivos o rutas cambiados:\n%s\n%s", filesBefore, files())
	}
	if s := mediaState(t, e.mediaRoot); s != e.mediaSum {
		t.Fatal("los medios han cambiado")
	}
	// La misma ficha, con la misma ruta, sigue reproduciéndose.
	if res := e.raw(t, "GET", "/api/v1/stream/files/"+fileID.String(), map[string]string{"Range": "bytes=0-99"}); res.StatusCode != 206 {
		t.Fatalf("reproducción directa: %d", res.StatusCode)
	}
	var plan struct {
		Method string `json:"method"`
	}
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.userTok, map[string]any{"file_id": fileID, "caps": map[string]any{"containers": []string{"mkv"}, "video": []string{"h264"}, "audio": []string{"ac3"}}, "dry_run": true}, &plan); c != 200 || plan.Method == "" || plan.Method == "unsupported" {
		t.Fatalf("reproducción: %d %+v", c, plan)
	}
}

// Permisos: un usuario normal no ve estas rutas (404), sin sesión 401, y con cookie hace falta el token anti-CSRF.
func TestImageAndIdentifyPermissions(t *testing.T) {
	e := newI2Env(t)
	valid := jpegBytes(t, 400, 600, color.White)
	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", e.base + "/identify/search?q=x", nil},
		{"GET", e.base + "/identify/preview?tmdb_id=20", nil},
		{"POST", e.base + "/identify", map[string]any{"tmdb_id": 20}},
		{"POST", e.base + "/identify/undo", nil},
		{"POST", e.base + "/identify/reset", map[string]bool{}},
		{"GET", e.base + "/images", nil},
		{"GET", e.base + "/images/gallery", nil},
		{"POST", e.base + "/images/select", map[string]string{"kind": "poster", "file_path": "/gp1.jpg"}},
		{"DELETE", e.base + "/images/poster", nil},
		{"GET", "/api/v1/admin/metadata/thumb?path=/gp1.jpg", nil},
	}
	for _, r := range routes {
		if c := e.call(t, r.method, r.path, e.userTok, r.body, nil); c != 404 {
			t.Errorf("usuario normal en %s %s: %d", r.method, r.path, c)
		}
		if c := e.call(t, r.method, r.path, "", r.body, nil); c != 401 {
			t.Errorf("sin sesión en %s %s: %d", r.method, r.path, c)
		}
	}
	if c, _ := e.upload(t, e.base, e.userTok, "poster", valid); c != 404 {
		t.Errorf("usuario normal al subir: %d", c)
	}
	if m := e.meta(t, e.base); m.MatchSource != "auto" || m.ProviderIDs["tmdb"] != "" {
		t.Fatalf("un usuario ha cambiado la ficha: %+v", m)
	}

	// Sesión de navegador (cookie): sin token anti-CSRF no se puede cambiar nada; con él, sí.
	raw, _ := json.Marshal(map[string]string{"username": "marta", "password": "contraseña-larga"})
	login, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/login", bytes.NewReader(raw))
	login.Header.Set("X-Velora-Client", "web")
	res, err := http.DefaultClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	var session, csrf *http.Cookie
	for _, c := range res.Cookies() {
		switch c.Name {
		case sessionCookie:
			session = c
		case csrfCookie:
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatalf("login con cookie: %d %v", res.StatusCode, res.Cookies())
	}
	send := func(method, path string, withCSRF bool, body []byte, ct string) int {
		req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader(body))
		req.AddCookie(session)
		req.AddCookie(csrf)
		req.Header.Set("Content-Type", ct)
		if withCSRF {
			req.Header.Set(csrfHeader, csrf.Value)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("kind", "poster")
	w, _ := mw.CreateFormFile("file", "x.jpg")
	w.Write(valid)
	mw.Close()
	id20 := []byte(`{"tmdb_id":20}`)
	for _, c := range []struct {
		method, path string
		body         []byte
		ct           string
	}{
		{"POST", e.base + "/identify", id20, "application/json"},
		{"POST", e.base + "/identify/undo", nil, "application/json"},
		{"POST", e.base + "/identify/reset", []byte(`{}`), "application/json"},
		{"POST", e.base + "/images/select", []byte(`{"kind":"poster","file_path":"/gp1.jpg"}`), "application/json"},
		{"POST", e.base + "/images/upload", form.Bytes(), mw.FormDataContentType()},
		{"DELETE", e.base + "/images/poster", nil, "application/json"},
	} {
		if got := send(c.method, c.path, false, c.body, c.ct); got != 403 {
			t.Errorf("sin CSRF en %s %s: %d", c.method, c.path, got)
		}
	}
	if got := send("POST", e.base+"/images/upload", true, form.Bytes(), mw.FormDataContentType()); got != 200 {
		t.Errorf("subir con CSRF: %d", got)
	}
}

// Las imágenes son inmutables: «Deshacer» recupera exactamente el contenido de antes, aunque después se haya actualizado la
// ficha, y volver a identificar con una descarga a medias no cambia lo que se ve ni deja archivos sueltos.
func TestUndoRestoresImageContentAfterRefresh(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	// La carátula de la carpeta tiene prioridad: se comparan los archivos de TMDB y, a través de la API, el fondo.
	file := func(kind string) []byte {
		t.Helper()
		var rel string
		if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = $2 and source = 'tmdb'`, e.item, kind).Scan(&rel); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(e.configDir, "metadata", rel))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		return raw
	}
	refresh := func() {
		t.Helper()
		it, _, err := e.st.ItemForRefresh(ctx, e.item)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := e.tm.Refresh(ctx, it, false); err != nil || !ok {
			t.Fatalf("refresh: %v %v", ok, err)
		}
	}
	files := func() int {
		t.Helper()
		list, err := os.ReadDir(filepath.Join(e.configDir, "metadata", e.item.String()))
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"1"}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	refresh() // identificación automática A
	posterA, backdropA := file("poster"), file("backdrop")
	_, shownA := e.image(t, e.token, e.item.String(), "backdrop", "", nil)
	if !bytes.Equal(shownA, backdropA) {
		t.Fatal("el fondo mostrado no es el de TMDB")
	}

	if c := e.identify(t, e.base, 20, true); c != 200 { // identificar B
		t.Fatalf("identificar: %d", c)
	}
	if bytes.Equal(file("poster"), posterA) {
		t.Fatal("el simulador debería dar otra imagen para B")
	}
	e.fake.imageGen.Add(1)
	refresh() // actualizar B: antes sobrescribía el archivo que guardaba la instantánea de A
	posterB2 := file("poster")
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 {
		t.Fatalf("deshacer: %d", c)
	}
	if !bytes.Equal(file("poster"), posterA) || !bytes.Equal(file("backdrop"), backdropA) {
		t.Fatal("deshacer no recupera el contenido de las imágenes de A")
	}
	if _, shown := e.image(t, e.token, e.item.String(), "backdrop", "", nil); !bytes.Equal(shown, backdropA) {
		t.Fatal("tras deshacer se muestra otro fondo")
	}
	if bytes.Equal(posterB2, posterA) {
		t.Fatal("preparación: la actualización debía traer otra imagen")
	}
	// Tras deshacer, las imágenes de B ya no se usan: no se acumulan.
	if n := files(); n != 2 {
		t.Fatalf("archivos tras deshacer: %d, esperaba solo los 2 de A", n)
	}

	// Volver a identificar el mismo ID con otra imagen en TMDB, y la segunda descarga falla: no cambia nada ni sobra nada.
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	poster20, backdrop20, count := file("poster"), file("backdrop"), files()
	e.fake.imageGen.Add(1)
	e.fake.failOne.Store("b20.jpg")
	for i := 0; i < 6; i++ { // el orden de las dos descargas no es fijo: se prueban varias veces
		if c := e.identify(t, e.base, 20, true); c != 502 {
			t.Fatalf("una imagen caída debe impedir aplicar: %d", c)
		}
		if !bytes.Equal(file("poster"), poster20) || !bytes.Equal(file("backdrop"), backdrop20) {
			t.Fatal("un intento fallido ha cambiado las imágenes visibles")
		}
		if n := files(); n != count {
			t.Fatalf("un intento fallido deja archivos sueltos: %d, antes %d", n, count)
		}
	}
	// Actualizar sustituye el archivo por otro nuevo y borra el anterior, salvo si «Deshacer» lo necesita.
	e.fake.failOne.Store("")
	refresh()
	if bytes.Equal(file("poster"), poster20) {
		t.Fatal("actualizar debería traer la imagen nueva")
	}
	if n := files(); n != count+1 {
		t.Fatalf("actualizar debe añadir solo el logo nuevo: %d, antes %d", n, count)
	}
	refresh()
	if n := files(); n != count+1 {
		t.Fatalf("actualizar otra vez acumula archivos: %d", n)
	}
}

// Un trabajo que comprobó la identidad A no puede publicar su imagen cuando otra transacción está identificando B.
func TestProviderImageSerializedWithIdentification(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"1"}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	row := func() string {
		var p string
		if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, e.item).Scan(&p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if ok, old, err := e.st.SetProviderImage(ctx, e.item, "poster", "x/a0.jpg", "1"); err != nil || !ok || old != "" {
		t.Fatalf("publicar con la identidad correcta: %v %q %v", ok, old, err)
	}
	if ok, old, err := e.st.SetProviderImage(ctx, e.item, "poster", "x/a1.jpg", "1"); err != nil || !ok || old != "x/a0.jpg" {
		t.Fatalf("sustituir: %v %q %v", ok, old, err)
	}

	// Solapamiento controlado: la otra transacción tiene la ficha bloqueada a medio cambiar. El trabajo debe esperar...
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"20"}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		ok, _, err := e.st.SetProviderImage(ctx, e.item, "poster", "x/tarde.jpg", "1")
		done <- result{ok, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("el trabajo no ha esperado a la identificación: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := tx.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, 'poster', 'tmdb', 'x/b.jpg')
		on conflict (item_id, kind, source) do update set path = excluded.path`, e.item); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// ... y, al continuar, ver la identidad nueva y no publicar nada.
	select {
	case r := <-done:
		if r.err != nil || r.ok {
			t.Fatalf("el trabajo tardío ha publicado: %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("el trabajo sigue bloqueado")
	}
	if p := row(); p != "x/b.jpg" {
		t.Fatalf("la imagen de la identificación se ha sustituido: %q", p)
	}

	// Con las funciones reales y en cualquier orden, la imagen final siempre es la de la identificación manual.
	for i := 0; i < 25; i++ {
		if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"1"}', locked = false where id = $1`, e.item); err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("x/manual-%d.jpg", i)
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, _, err := e.st.SetProviderImage(ctx, e.item, "poster", fmt.Sprintf("x/trabajo-%d.jpg", i), "1")
			errs <- err
		}()
		go func() {
			<-start
			errs <- e.st.ApplyIdentification(ctx, e.item, store.IdentifyInput{Fields: store.MetadataFields{Title: "B", ProviderIDs: map[string]string{"tmdb": "20"}},
				SortTitle: "b", ReplaceImages: true, Images: map[string]string{"poster": want}, By: e.adminID(t)})
		}()
		close(start)
		for j := 0; j < 2; j++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if p := row(); p != want {
			t.Fatalf("vuelta %d: imagen final %q, esperaba %q", i, p, want)
		}
	}
}

func (e *i2Env) adminID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `select id from users where username = 'marta'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Una respuesta 200 que no es la ficha pedida no se aplica, no se guarda en caché y no borra nada.
func TestInvalidProviderResponsesAreNotApplied(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	before := e.meta(t, e.base)
	images := func() string {
		var out string
		_ = e.pool.QueryRow(ctx, `select coalesce(string_agg(kind || source || path, ','  order by kind, source), '') from item_images where item_id = $1`, e.item).Scan(&out)
		return out
	}
	imgs := images()
	for _, id := range []string{"500", "501", "502", "503", "504"} {
		var n int
		fmt.Sscan(id, &n)
		if c := e.identify(t, e.base, n, true); c != 502 {
			t.Errorf("identificar con la ficha %s: %d", id, c)
		}
		if c := e.call(t, "GET", e.base+"/identify/preview?tmdb_id="+id, e.token, nil, nil); c != 502 {
			t.Errorf("comparar con la ficha %s: %d", id, c)
		}
		if c := e.call(t, "GET", e.base+"/identify/search?tmdb_id="+id, e.token, nil, nil); c != 502 {
			t.Errorf("buscar la ficha %s: %d", id, c)
		}
		got := e.meta(t, e.base)
		if got.Title != before.Title || got.ProviderIDs["tmdb"] != "" || got.MatchSource != "auto" || got.CanUndo || images() != imgs {
			t.Fatalf("ficha %s: ha cambiado: %+v %s", id, got, images())
		}
	}
	// Actualizar con esa identidad tampoco escribe nada.
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{"tmdb":"500"}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	it, _, err := e.st.ItemForRefresh(ctx, e.item)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.tm.Refresh(ctx, it, false); err == nil || ok {
		t.Fatalf("actualizar con una ficha vacía: %v %v", ok, err)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids = '{}' where id = $1`, e.item); err != nil {
		t.Fatal(err)
	}
	if got := e.meta(t, e.base); got.Title != before.Title || images() != imgs {
		t.Fatalf("actualizar ha escrito datos: %+v", got)
	}
	// El fallo puntual no queda guardado: cuando TMDB se recupera, funciona.
	e.fake.repaired.Store(true)
	if c := e.identify(t, e.base, 503, true); c != 200 {
		t.Fatalf("tras recuperarse TMDB: %d", c)
	}
	if got := e.meta(t, e.base); got.Title != "Reparada" || got.ProviderIDs["tmdb"] != "503" {
		t.Fatalf("identificación tras recuperarse: %+v", got)
	}
	// Series: lo mismo.
	var pathID uuid.UUID
	if err := e.pool.QueryRow(ctx, `select library_path_id from media_items where id = $1`, e.item).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	var series uuid.UUID
	if err := e.pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, content_path)
		values ($1, $2, 'series', 'Serie', 'serie', 'Serie') returning id`, e.lib.ID, pathID).Scan(&series); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{500, 501} {
		if c := e.identify(t, "/api/v1/admin/items/"+series.String(), id, false); c != 502 {
			t.Fatalf("serie con la ficha %d: %d", id, c)
		}
		var cached int
		if err := e.pool.QueryRow(ctx, `select count(*) from metadata_cache where provider = 'tmdb' and cache_key like $1`, fmt.Sprintf("tv:%d:%%", id)).Scan(&cached); err != nil || cached != 0 {
			t.Fatalf("la ficha %d de serie no válida ha entrado en la caché: %d %v", id, cached, err)
		}
	}
	var cachedMovie int
	if err := e.pool.QueryRow(ctx, `select count(*) from metadata_cache where provider = 'tmdb' and cache_key like 'movie:504:%'`).Scan(&cachedMovie); err != nil || cachedMovie != 0 {
		t.Fatalf("la película no válida ha entrado en la caché: %d %v", cachedMovie, err)
	}
	// Una entrada mala ya guardada en caché no se usa: se vuelve a pedir.
	if err := e.st.CachePut(ctx, "tmdb", "movie:90:es-ES", []byte(`{"id":90,"name":"Otra ficha"}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	if c := e.identify(t, e.base, 90, false); c != 200 {
		t.Fatalf("con una entrada mala en caché: %d", c)
	}
	if got := e.meta(t, e.base); got.Title != "Homónima" || got.ProviderIDs["tmdb"] != "90" {
		t.Fatalf("identificación con una entrada mala en caché: %+v", got)
	}
	if got := e.meta(t, "/api/v1/admin/items/"+series.String()); got.Title != "Serie" || got.MatchSource != "auto" {
		t.Fatalf("serie: %+v", got)
	}
}

// Copias de seguridad: las imágenes personalizadas sobreviven a perder la base y /config/metadata, siguen asociadas a sus
// fichas (película y temporada), también las que solo guarda una instantánea, y no las sustituye una actualización posterior.
func TestBackupRestoresCustomImages(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	if c := e.identify(t, e.base, 20, true); c != 200 {
		t.Fatalf("identificar: %d", c)
	}
	if c, _ := e.upload(t, e.base, e.token, "poster", jpegBytes(t, 400, 600, color.RGBA{R: 40, G: 90, B: 200, A: 255})); c != 200 {
		t.Fatalf("subir cartel: %d", c)
	}
	if c := e.call(t, "POST", e.base+"/images/select", e.token, map[string]string{"kind": "logo", "file_path": "/gl1.png"}, nil); c != 200 {
		t.Fatalf("elegir logotipo: %d", c)
	}
	if c, _ := e.upload(t, e.base, e.token, "backdrop", jpegBytes(t, 640, 360, color.RGBA{R: 200, G: 200, A: 255})); c != 200 {
		t.Fatalf("subir fondo: %d", c)
	}
	// Una temporada sintética: su asociación también debe conservarse.
	var pathID uuid.UUID
	if err := e.pool.QueryRow(ctx, `select library_path_id from media_items where id = $1`, e.item).Scan(&pathID); err != nil {
		t.Fatal(err)
	}
	var series, season uuid.UUID
	if err := e.pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, content_path) values ($1, $2, 'series', 'S', 's', 'S') returning id`, e.lib.ID, pathID).Scan(&series); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, parent_id, kind, title, sort_title, content_path) values ($1, $2, $3, 'season', 'T1', 't1', 'S/T1') returning id`, e.lib.ID, pathID, series).Scan(&season); err != nil {
		t.Fatal(err)
	}
	seasonBase := "/api/v1/admin/items/" + season.String()
	if c, _ := e.upload(t, seasonBase, e.token, "thumb", jpegBytes(t, 640, 360, color.RGBA{G: 220, B: 90, A: 255})); c != 200 {
		t.Fatalf("subir miniatura: %d", c)
	}
	shown := func(id, kind string) []byte {
		t.Helper()
		res, raw := e.image(t, e.token, id, kind, "", nil)
		if res.StatusCode != 200 {
			t.Fatalf("%s de %s: %d", kind, id, res.StatusCode)
		}
		return raw
	}
	id := e.item.String()
	poster, logo, backdrop, thumb := shown(id, "poster"), shown(id, "logo"), shown(id, "backdrop"), shown(season.String(), "thumb")
	// Este fondo pasa a estar solo en una instantánea, y la copia también debe llevarlo.
	if c := e.call(t, "POST", e.base+"/identify/reset", e.token, map[string]bool{"restore_images": true}, nil); c != 200 {
		t.Fatalf("restablecer: %d", c)
	}
	if n, _ := e.st.CustomImageKinds(ctx, e.item); len(n) != 0 {
		t.Fatalf("restablecer debe quitar las imágenes: %v", n)
	}
	metaDir := filepath.Join(e.configDir, "metadata")
	backups := t.TempDir()
	bk := &backup.Service{Pool: e.pool, DatabaseURL: e.dbURL, Dir: backups, MetadataDir: metaDir, Keep: 7, KeepWeeks: 4}
	meta, err := bk.Run(ctx)
	if err != nil || !meta.Verified {
		t.Fatalf("copia: %+v %v", meta, err)
	}
	if !meta.ImagesIncluded || meta.ImagesCount != 4 || meta.ImagesFile == "" || len(meta.ImagesMissing) != 0 {
		t.Fatalf("imágenes de la copia: %+v", meta)
	}
	st, err := os.Stat(filepath.Join(backups, meta.ImagesFile))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("archivo de imágenes: %v %v", st, err)
	}
	list, err := bk.List()
	if err != nil || len(list) != 1 || list[0].ImagesCount != 4 {
		t.Fatalf("listado: %+v %v", list, err)
	}

	// Desastre: se pierden los metadatos en disco y la base se restaura desde la copia.
	if err := os.RemoveAll(metaDir); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `delete from item_images where source = 'user'`); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(e.dbURL)
	restoredDB := strings.TrimPrefix(u.Path, "/") + "_rest"
	if _, err := e.pool.Exec(ctx, `create database `+restoredDB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `drop database if exists `+restoredDB+` with (force)`)
	})
	pass, _ := u.User.Password()
	cmd := exec.Command("pg_restore", "--no-owner", "--exit-on-error", "--dbname="+restoredDB, filepath.Join(backups, meta.File))
	cmd.Env = append(os.Environ(), "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(), "PGPASSWORD="+pass, "PGDATABASE="+restoredDB, "PGSSLMODE=disable")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pg_restore: %v %s", err, out)
	}
	u.Path = "/" + restoredDB
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	restored := &backup.Service{Pool: pool, DatabaseURL: u.String(), Dir: backups, MetadataDir: metaDir}
	dumpBefore, _ := os.ReadFile(filepath.Join(backups, meta.ImagesFile))
	res, err := restored.RestoreImages(ctx, meta.File)
	if err != nil || res.Legacy || res.Restored != 4 || len(res.Missing) != 0 {
		t.Fatalf("restaurar imágenes: %+v %v", res, err)
	}
	if res, err = restored.RestoreImages(ctx, meta.File); err != nil || res.Restored != 0 || res.Present != 4 {
		t.Fatalf("repetir la restauración: %+v %v", res, err)
	}
	if after, _ := os.ReadFile(filepath.Join(backups, meta.ImagesFile)); !bytes.Equal(after, dumpBefore) {
		t.Fatal("restaurar ha modificado la copia")
	}

	// Servidor nuevo sobre lo restaurado: cada imagen vuelve a su ficha.
	cfg := config.Config{DatabaseURL: u.String(), SessionTTL: time.Hour, BackupDir: backups, ConfigDir: e.configDir, Location: time.UTC, MediaRoots: []string{e.mediaRoot},
		MaxRemux: 2, TMDBAPIURL: e.fake.URL, TMDBImageURL: e.fake.URL + "/img"}
	key, _ := secrets.EnsureMasterKey(cfg.ConfigDir)
	st2 := &store.Store{Pool: pool}
	ts := httptest.NewServer(New(cfg, key, st2, audit.New(pool), jobs.NewQueue(pool), restored).Handler())
	defer ts.Close()
	old := e.server
	e.server = ts
	defer func() { e.server = old }()
	if c := e.call(t, "POST", e.base+"/identify/undo", e.token, nil, nil); c != 200 { // recupera lo que solo estaba en la instantánea
		t.Fatalf("deshacer tras restaurar: %d", c)
	}
	check := func(step string) {
		t.Helper()
		if !bytes.Equal(shown(id, "poster"), poster) || !bytes.Equal(shown(id, "logo"), logo) || !bytes.Equal(shown(id, "backdrop"), backdrop) || !bytes.Equal(shown(season.String(), "thumb"), thumb) {
			t.Fatalf("%s: las imágenes no son las de antes", step)
		}
	}
	check("tras restaurar")
	var parent uuid.UUID
	if err := pool.QueryRow(ctx, `select parent_id from media_items where id = $1`, season).Scan(&parent); err != nil || parent != series {
		t.Fatalf("jerarquía restaurada: %v %v", parent, err)
	}
	// Persistencia: actualizar la ficha restaurada no las sustituye.
	tm := metadata.New(e.fake.URL, e.fake.URL+"/img", e.configDir, st2)
	it, _, err := st2.ItemForRefresh(ctx, e.item)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := tm.Refresh(ctx, it, false); err != nil || !ok {
		t.Fatalf("actualizar tras restaurar: %v %v", ok, err)
	}
	check("tras actualizar lo restaurado")
	// Una copia posterior de lo restaurado vuelve a llevarlas.
	again := &backup.Service{Pool: pool, DatabaseURL: u.String(), Dir: t.TempDir(), MetadataDir: metaDir, Keep: 7, KeepWeeks: 4}
	if m, err := again.Run(ctx); err != nil || !m.Verified || m.ImagesCount != 4 {
		t.Fatalf("copia de lo restaurado: %+v %v", m, err)
	}
}
