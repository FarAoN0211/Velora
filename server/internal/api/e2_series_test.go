package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/markers"
	"velora/internal/media"
	"velora/internal/metadata"
	"velora/internal/store"
)

// E2 S1: escaneo de series con temporadas y episodios, fichas, progreso, «siguiente episodio» e imágenes de la serie.
// Los vídeos son copias del sintético de E1 (ffmpeg), con nombres de formato habitual.

type seriesEnv struct {
	*e1Env
	lib uuid.UUID
}

func newSeriesEnv(t *testing.T, tmdbURL string) *seriesEnv {
	t.Helper()
	e := newE1Env(t, tmdbURL)
	src, err := os.ReadFile(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"Serie A/Season 1/Serie A - 1x01 - Piloto.mkv",
		"Serie A/Season 1/Serie A - 1x02 - Segundo.mkv",
		"Serie A/Season 2/Serie.A.S02E01.1080p.WEB-DL.mkv",
		"Serie A/Specials/Serie A S00E01 - Especial.mkv",
		"Serie A/trailer.mkv",            // tráiler de la serie
		"Serie A/Season 1/sample.mkv",    // muestra: se ignora
		"Serie A/Season 1/Extras/x.mkv",  // dentro de una temporada: se ignora
		"Narcos/Narcos S01E01 1080p.mkv", // episodios sueltos en la carpeta de la serie
		"Narcos/Narcos S01E02 1080p.mkv",
	} {
		p := filepath.Join(e.mediaRoot, "Series", rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Póster local de la serie: lo heredan temporadas y episodios.
	poster, _ := os.ReadFile(filepath.Join(e.movieDir, "poster.jpg"))
	_ = os.WriteFile(filepath.Join(e.mediaRoot, "Series", "Serie A", "poster.jpg"), poster, 0o644)

	var created struct {
		Library store.Library `json:"library"`
	}
	if c := e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Series", "kind": "tv",
		"path": filepath.Join(e.mediaRoot, "Series")}, &created); c != 201 && c != 200 {
		t.Fatalf("crear biblioteca de series: %d", c)
	}
	return &seriesEnv{e1Env: e, lib: created.Library.ID}
}

type seriesItem struct {
	store.ItemCard
	Overview *string `json:"overview"`
	Seasons  []struct {
		store.ItemCard
		EpisodeCount  int `json:"episode_count"`
		UnplayedCount int `json:"unplayed_count"`
	} `json:"seasons"`
	NextUp   *store.ItemCard `json:"next_up"`
	Episodes []struct {
		store.ItemCard
		FileID *uuid.UUID        `json:"file_id"`
		Tech   []media.TechBadge `json:"tech"`
	} `json:"episodes"`
	Next   *store.ItemCard   `json:"next_episode"`
	Files  []store.FileInfo  `json:"files"`
	Extras []store.ExtraInfo `json:"extras"`
}

func (e *seriesEnv) item(t *testing.T, id uuid.UUID) seriesItem {
	t.Helper()
	var out struct {
		Item seriesItem `json:"item"`
	}
	if c := e.call(t, "GET", "/api/v1/items/"+id.String(), e.token, nil, &out); c != 200 {
		t.Fatalf("ficha %s: %d", id, c)
	}
	return out.Item
}

func (e *seriesEnv) list(t *testing.T, query string) []store.ItemCard {
	t.Helper()
	var out struct {
		Items []store.ItemCard `json:"items"`
	}
	if c := e.call(t, "GET", "/api/v1/items?library_id="+e.lib.String()+query, e.token, nil, &out); c != 200 {
		t.Fatalf("listado: %d", c)
	}
	return out.Items
}

func epLabel(c *store.ItemCard) string {
	if c == nil {
		return "ninguno"
	}
	s, n := -1, -1
	if c.SeasonNumber != nil {
		s = *c.SeasonNumber
	}
	if c.EpisodeNumber != nil {
		n = *c.EpisodeNumber
	}
	return c.Title + " T" + itoa(s) + "E" + itoa(n)
}

func itoa(n int) string {
	if n < 0 {
		return "?"
	}
	return strconv.Itoa(n)
}

func TestSeriesScanBrowseAndNextUp(t *testing.T) {
	e := newSeriesEnv(t, "")
	ctx := context.Background()
	res := e.scan(t, e.lib)
	if res.Series != 2 || res.Episodes != 6 || res.Extras != 1 || res.Errors != 0 {
		t.Fatalf("resultado del escaneo: %+v", res)
	}
	// Un segundo escaneo no duplica nada y conserva las fichas.
	var before int
	_ = e.pool.QueryRow(ctx, `select count(*) from media_items`).Scan(&before)
	if res2 := e.scan(t, e.lib); res2.Series != 2 || res2.Episodes != 6 {
		t.Fatalf("segundo escaneo: %+v", res2)
	}
	var after int
	_ = e.pool.QueryRow(ctx, `select count(*) from media_items`).Scan(&after)
	if before != after {
		t.Fatalf("el segundo escaneo cambia el número de fichas: %d → %d", before, after)
	}

	items := e.list(t, "")
	if len(items) != 2 || items[0].Title != "Narcos" || items[1].Title != "Serie A" || items[1].Kind != "series" {
		t.Fatalf("la biblioteca debería listar las 2 series: %+v", items)
	}
	serieA := items[1].ID
	if !items[1].HasPoster || items[1].TrailerID == nil {
		t.Fatalf("Serie A debería tener póster local y tráiler: %+v", items[1])
	}

	s := e.item(t, serieA)
	if len(s.Seasons) != 3 || s.Seasons[0].Title != "Temporada 1" || s.Seasons[0].EpisodeCount != 2 ||
		s.Seasons[1].Title != "Temporada 2" || s.Seasons[2].Title != "Especiales" {
		t.Fatalf("temporadas (especiales al final): %+v", s.Seasons)
	}
	if s.NextUp == nil || s.NextUp.Title != "Piloto" {
		t.Fatalf("sin nada visto, toca el primer episodio: %s", epLabel(s.NextUp))
	}
	t1 := e.item(t, s.Seasons[0].ID)
	if len(t1.Episodes) != 2 || t1.Episodes[0].Title != "Piloto" || t1.Episodes[1].Title != "Segundo" || t1.Episodes[0].FileID == nil ||
		*t1.Episodes[0].SeriesTitle != "Serie A" || *t1.Episodes[1].EpisodeNumber != 2 || *t1.Episodes[1].SeasonNumber != 1 {
		t.Fatalf("episodios de la temporada 1: %+v", t1.Episodes)
	}
	t2 := e.item(t, s.Seasons[1].ID)
	if len(t2.Episodes) != 1 || t2.Episodes[0].Title != "Episodio 1" {
		t.Fatalf("un episodio sin título en el nombre se llama «Episodio N»: %+v", t2.Episodes)
	}
	ep1, ep2, s2e1 := t1.Episodes[0].ID, t1.Episodes[1].ID, t2.Episodes[0].ID

	// Ficha del episodio: archivos para reproducir y el siguiente en orden, sin saltar a los especiales.
	d := e.item(t, ep1)
	if len(d.Files) != 1 || d.Next == nil || d.Next.ID != ep2 {
		t.Fatalf("episodio 1: archivos %d, siguiente %s", len(d.Files), epLabel(d.Next))
	}
	// Insignias técnicas del sintético (H.264 320×240 a 25 fps, AC-3 mono, MKV), en la ficha y en la lista de episodios.
	techIDs := func(b []media.TechBadge) string {
		var out []string
		for _, x := range b {
			out = append(out, x.Category+":"+x.ID)
		}
		return strings.Join(out, " ")
	}
	const wantTech = "resolution:sd hdr:sdr video:h264 audio:ac3 channels:mono fps:25 container:mkv"
	if got := techIDs(d.Files[0].Tech); got != wantTech {
		t.Fatalf("insignias de la ficha del episodio: %s", got)
	}
	if got := techIDs(t1.Episodes[0].Tech); got != wantTech {
		t.Fatalf("insignias en la lista de episodios: %s", got)
	}
	// Un archivo sin subtítulos (y sin OpenSubtitles) responde una lista vacía, nunca null: un null tumbaba la ficha web.
	subRes := e.raw(t, "GET", "/api/v1/stream/files/"+d.Files[0].ID.String()+"/subtitles", map[string]string{"Authorization": "Bearer " + e.token})
	subBody, _ := io.ReadAll(subRes.Body)
	subRes.Body.Close()
	if subRes.StatusCode != 200 || !strings.Contains(string(subBody), `"subtitles":[]`) {
		t.Fatalf("lista de subtítulos vacía: %d %s", subRes.StatusCode, subBody)
	}
	if n := e.item(t, ep2).Next; n == nil || n.ID != s2e1 {
		t.Fatalf("tras el último de la T1 va el primero de la T2: %s", epLabel(n))
	}
	if n := e.item(t, s2e1).Next; n != nil {
		t.Fatalf("tras el último episodio normal no se pasa a los especiales: %s", epLabel(n))
	}

	// El episodio hereda el póster de la serie.
	resImg := e.raw(t, "GET", "/api/v1/items/"+ep1.String()+"/images/poster", nil)
	resImg.Body.Close()
	if resImg.StatusCode != 200 {
		t.Fatalf("el episodio debería servir el póster de su serie: %d", resImg.StatusCode)
	}

	// A medias: la serie propone continuarlo y aparece en «Continuar viendo» con el nombre de la serie.
	dur := int64(6000)
	if c := e.call(t, "PUT", "/api/v1/me/items/"+ep1.String()+"/progress", e.token,
		map[string]any{"position_ms": 2000, "duration_ms": dur, "file_id": d.Files[0].ID}, nil); c != 204 {
		t.Fatalf("progreso: %d", c)
	}
	if n := e.item(t, serieA).NextUp; n == nil || n.ID != ep1 || n.PositionMS != 2000 {
		t.Fatalf("a medias, toca continuar el episodio 1: %s", epLabel(n))
	}
	var home struct {
		Continue []store.ItemCard `json:"continue"`
		NextUp   []store.ItemCard `json:"next_up"`
	}
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 1 || home.Continue[0].ID != ep1 || home.Continue[0].SeriesTitle == nil || len(home.NextUp) != 0 {
		t.Fatalf("inicio con un episodio a medias: %+v / %+v", home.Continue, home.NextUp)
	}

	// Terminado: toca el 2, que sale en «Siguiente episodio».
	e.call(t, "PUT", "/api/v1/me/items/"+ep1.String()+"/progress", e.token, map[string]any{"position_ms": 5900, "duration_ms": dur}, nil)
	if n := e.item(t, serieA).NextUp; n == nil || n.ID != ep2 {
		t.Fatalf("visto el 1, toca el 2: %s", epLabel(n))
	}
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 0 || len(home.NextUp) != 1 || home.NextUp[0].ID != ep2 {
		t.Fatalf("inicio tras terminar el episodio 1: %+v / %+v", home.Continue, home.NextUp)
	}
	if got := e.list(t, "&played=played"); len(got) != 0 {
		t.Fatalf("Serie A no está vista hasta verla entera: %+v", got)
	}

	// Marcar la serie como vista marca todos sus episodios; «siguiente» vuelve al principio.
	if c := e.call(t, "POST", "/api/v1/me/items/"+serieA.String()+"/played", e.token, nil, nil); c != 204 {
		t.Fatalf("marcar la serie: %d", c)
	}
	if got := e.list(t, "&played=played"); len(got) != 1 || got[0].ID != serieA || !got[0].Played {
		t.Fatalf("la serie debería figurar como vista: %+v", got)
	}
	if s := e.item(t, serieA); s.Seasons[0].UnplayedCount != 0 {
		t.Fatalf("temporada 1 sin pendientes: %+v", s.Seasons[0])
	}
	if c := e.call(t, "DELETE", "/api/v1/me/items/"+s.Seasons[1].ID.String()+"/played", e.token, nil, nil); c != 204 {
		t.Fatalf("desmarcar la temporada 2: %d", c)
	}
	if n := e.item(t, serieA).NextUp; n == nil || n.ID != s2e1 {
		t.Fatalf("con la T2 sin ver, toca su primer episodio: %s", epLabel(n))
	}

	// Un episodio que desaparece deja de listarse; su temporada sigue con el resto.
	if err := os.Remove(filepath.Join(e.mediaRoot, "Series", "Serie A/Season 1/Serie A - 1x02 - Segundo.mkv")); err != nil {
		t.Fatal(err)
	}
	if r := e.scan(t, e.lib); r.Missing != 1 {
		t.Fatalf("debería marcarse 1 ausente: %+v", r)
	}
	if t1 := e.item(t, s.Seasons[0].ID); len(t1.Episodes) != 1 {
		t.Fatalf("la temporada 1 debería quedarse con 1 episodio: %d", len(t1.Episodes))
	}
	if n := e.item(t, ep1).Next; n == nil || n.ID != s2e1 {
		t.Fatalf("sin el episodio 2, tras el 1 va el de la T2: %s", epLabel(n))
	}

	// Un usuario sin acceso a la biblioteca no ve ni la serie ni sus episodios.
	if c := e.call(t, "POST", "/api/v1/admin/users", e.token, map[string]string{"username": "lucia", "display_name": "Lucía",
		"password": "otra-contraseña-larga"}, nil); c != 201 {
		t.Fatalf("crear usuario: %d", c)
	}
	var sess struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "lucia", "password": "otra-contraseña-larga"}, &sess); c != 200 {
		t.Fatalf("login del usuario sin acceso: %d", c)
	}
	for _, id := range []uuid.UUID{serieA, s.Seasons[0].ID, ep1} {
		if c := e.call(t, "GET", "/api/v1/items/"+id.String(), sess.Token, nil, nil); c != 404 {
			t.Fatalf("sin acceso, %s debería dar 404: %d", id, c)
		}
	}
}

func TestSeriesSeasonMetadataFromTMDB(t *testing.T) {
	fake := http.NewServeMux()
	fake.HandleFunc("/3/search/tv", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "Serie A") {
			w.Write([]byte(`{"results":[{"id":77}]}`))
			return
		}
		w.Write([]byte(`{"results":[]}`))
	})
	fake.HandleFunc("/3/tv/77", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":77,"name":"Serie A oficial","overview":"Una serie.","first_air_date":"2011-04-17","poster_path":"/sp.jpg"}`))
	})
	fake.HandleFunc("/3/tv/77/season/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":701,"season_number":1,"name":"Temporada 1","overview":"La primera.","air_date":"2011-04-17","poster_path":"/t1.jpg",
			"episodes":[{"id":9001,"episode_number":1,"name":"Se acerca el invierno","overview":"Empieza todo.","runtime":62,"still_path":"/e1.jpg"},
			            {"id":9002,"episode_number":2,"name":"Episodio 2","overview":"Sigue.","runtime":56}]}`))
	})
	fake.HandleFunc("/3/tv/77/season/2", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	fake.HandleFunc("/3/tv/78", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":78,"name":"Otra serie","overview":"Otra.","first_air_date":"2020-01-01"}`))
	})
	fake.HandleFunc("/3/tv/78/season/1", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":801,"season_number":1,"name":"Temporada 1","overview":"Otra serie.","episodes":[{"id":9101,"episode_number":1,"name":"Piloto nuevo"}]}`))
	})
	fake.HandleFunc("/3/tv/78/season/2", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	fake.HandleFunc("/3/tv/78/season/0", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	fake.HandleFunc("/3/tv/77/season/0", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	var images atomic.Int32
	fake.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) { images.Add(1); w.Write([]byte("JPEGDATA")) })
	ts := httptest.NewServer(fake)
	defer ts.Close()

	e := newSeriesEnv(t, ts.URL)
	ctx := context.Background()
	e.scan(t, e.lib)
	st := &store.Store{Pool: e.pool}
	tm := metadata.New(ts.URL, ts.URL+"/img", e.configDir, st)
	if err := os.MkdirAll(filepath.Join(e.configDir, "secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.configDir, "secrets", "tmdb.key"), []byte("clave"), 0o600)

	pending, err := st.ItemsNeedingMetadata(ctx, e.lib, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pendientes de TMDB (las 2 series): %d %v", len(pending), err)
	}
	for _, p := range pending {
		if p.Kind != "series" {
			t.Fatalf("debería pedirse como serie: %+v", p)
		}
		if _, err := tm.Enrich(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	seasons, err := st.SeasonsNeedingMetadata(ctx, &e.lib, nil, 10)
	if err != nil || len(seasons) != 3 {
		t.Fatalf("temporadas pendientes (solo las de la serie identificada): %d %v", len(seasons), err)
	}
	for _, p := range seasons {
		if _, err := tm.EnrichSeason(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if again, _ := st.SeasonsNeedingMetadata(ctx, &e.lib, nil, 10); len(again) != 0 {
		t.Fatalf("ninguna temporada debería quedar pendiente: %d", len(again))
	}

	items := e.list(t, "")
	var serieA uuid.UUID
	for _, it := range items {
		if it.Title == "Serie A oficial" {
			serieA = it.ID
		}
	}
	if serieA == uuid.Nil {
		t.Fatalf("la serie debería llamarse como en TMDB: %+v", items)
	}
	s := e.item(t, serieA)
	t1 := e.item(t, s.Seasons[0].ID)
	if t1.Overview == nil || *t1.Overview != "La primera." {
		t.Fatalf("sinopsis de la temporada: %v", t1.Overview)
	}
	if len(t1.Episodes) != 2 || t1.Episodes[0].Title != "Se acerca el invierno" || t1.Episodes[1].Title != "Segundo" ||
		!t1.Episodes[0].HasThumb || t1.Episodes[1].HasThumb || t1.Episodes[0].RuntimeMS == nil || *t1.Episodes[0].RuntimeMS != 62*60_000 {
		t.Fatalf("episodios con datos de TMDB (el título sin traducir no pisa el del archivo): %+v", t1.Episodes)
	}
	var tmdbEp string
	_ = e.pool.QueryRow(ctx, `select provider_ids->>'tmdb_episode' from media_items where id = $1`, t1.Episodes[0].ID).Scan(&tmdbEp)
	if tmdbEp != "9001" {
		t.Fatalf("id de TMDB del episodio: %q", tmdbEp)
	}
	// La temporada usa su póster de TMDB y el episodio su miniatura.
	for _, path := range []string{"/api/v1/items/" + s.Seasons[0].ID.String() + "/images/poster", "/api/v1/items/" + t1.Episodes[0].ID.String() + "/images/thumb"} {
		r := e.raw(t, "GET", path, nil)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("%s: %d", path, r.StatusCode)
		}
	}

	// Al corregir la identificación de la serie (I2), temporadas y episodios dejan los datos de la anterior y toman los de la
	// nueva; lo que la nueva no tiene vuelve al título del nombre del archivo.
	if c := e.call(t, "POST", "/api/v1/admin/items/"+serieA.String()+"/identify", e.token, map[string]any{"tmdb_id": 78}, nil); c != 200 {
		t.Fatalf("identificar la serie: %d", c)
	}
	var queued int
	_ = e.pool.QueryRow(ctx, `select count(*) from jobs where kind = 'series_children' and payload->>'series_id' = $1`, serieA.String()).Scan(&queued)
	if queued != 1 {
		t.Fatalf("debería encolarse la puesta al día de temporadas y episodios: %d", queued)
	}
	cleared, enriched, err := tm.RefreshSeriesChildren(ctx, serieA)
	if err != nil || cleared != 5 || enriched != 3 { // 3 temporadas y los 2 episodios con datos de la T1
		t.Fatalf("puesta al día: limpiadas %d, temporadas %d, %v", cleared, enriched, err)
	}
	t1 = e.item(t, s.Seasons[0].ID)
	if t1.Overview == nil || *t1.Overview != "Otra serie." || t1.Episodes[0].Title != "Piloto nuevo" || t1.Episodes[1].Title != "Segundo" ||
		t1.Episodes[0].HasThumb || t1.Episodes[0].RuntimeMS == nil || *t1.Episodes[0].RuntimeMS != 6000 {
		t.Fatalf("tras reidentificar, episodios con los datos de la serie nueva (y la duración del archivo): %+v", t1.Episodes)
	}
	var ids map[string]string
	_ = e.pool.QueryRow(ctx, `select provider_ids from media_items where id = $1`, t1.Episodes[1].ID).Scan(&ids)
	if ids["tmdb_series"] != "" || ids["tmdb_episode"] != "" {
		t.Fatalf("el episodio 2 no está en la ficha nueva: no debe conservar el id del anterior: %v", ids)
	}
	var oldImages int
	_ = e.pool.QueryRow(ctx, `select count(*) from item_images m join media_items i on i.id = m.item_id
		where i.series_id = $1 and m.source = 'tmdb'`, serieA).Scan(&oldImages)
	if oldImages != 0 {
		t.Fatalf("las imágenes de TMDB de la serie anterior deberían quitarse: %d", oldImages)
	}
	if again, _ := st.SeasonsNeedingMetadata(ctx, &e.lib, nil, 10); len(again) != 0 {
		t.Fatalf("ninguna temporada debería quedar pendiente: %d", len(again))
	}
	// Una imagen pedida con la identificación anterior ya no se publica.
	if ok, _, _ := st.SetChildProviderImage(ctx, t1.Episodes[1].ID, "thumb", "x.jpg", "77"); ok {
		t.Fatal("no debería publicarse una imagen de la identificación anterior")
	}
}

// E2: intro y créditos detectados comparando el audio de los episodios, servidos como capítulos a los reproductores.
func TestSeriesIntroCreditsDetection(t *testing.T) {
	e := newE1Env(t, "")
	ctx := context.Background()
	intro := "aevalsrc=exprs='0.3*sin(2*PI*t*(300+60*floor(mod(t*3\\,9))))+0.2*sin(2*PI*t*(520+40*floor(mod(t*2\\,5))))':s=44100:d=40"
	credits := "aevalsrc=exprs='0.3*sin(2*PI*t*(700-50*floor(mod(t*4\\,7))))+0.2*sin(2*PI*t*(250+30*floor(mod(t\\,6))))':s=44100:d=30"
	pres := []float64{30, 12, 45}
	dir := filepath.Join(e.mediaRoot, "Series", "Serie Sintonía", "Season 1")
	_ = os.MkdirAll(dir, 0o755)
	for i, pre := range pres {
		graph := fmt.Sprintf("anoisesrc=color=pink:seed=%d:amplitude=0.4:d=%.0f:r=44100[a];%s[b];anoisesrc=color=pink:seed=%d:amplitude=0.4:d=400:r=44100[c];%s[d];[a][b][c][d]concat=n=4:v=0:a=1[out]",
			10+i, pre, intro, 20+i, credits)
		out := filepath.Join(dir, fmt.Sprintf("Serie Sintonía - 1x0%d.mkv", i+1))
		if b, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-filter_complex", graph, "-map", "[out]", "-c:a", "aac", out).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, b)
		}
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Series", "kind": "tv", "path": filepath.Join(e.mediaRoot, "Series")}, &created)
	if res := e.scan(t, created.Library.ID); res.Episodes != 3 {
		t.Fatalf("escaneo: %+v", res)
	}
	st := &store.Store{Pool: e.pool}
	sum, err := markers.RunPending(ctx, st, []string{e.mediaRoot}, time.Minute, nil)
	if err != nil || sum.Seasons != 1 || sum.Files != 3 || sum.Found != 3 {
		t.Fatalf("detección: %+v %v", sum, err)
	}
	if again, _ := markers.RunPending(ctx, st, []string{e.mediaRoot}, time.Minute, nil); again.Seasons != 0 {
		t.Fatalf("lo ya analizado no se repite: %+v", again)
	}
	rows, _ := e.pool.Query(ctx, `select f.id, f.rel_path, f.duration_ms from media_files f order by f.rel_path`)
	type file struct {
		id  uuid.UUID
		rel string
		dur int64
	}
	var files []file
	for rows.Next() {
		var f file
		_ = rows.Scan(&f.id, &f.rel, &f.dur)
		if strings.Contains(f.rel, "Sintonía") {
			files = append(files, f)
		}
	}
	rows.Close()
	if len(files) != 3 {
		t.Fatalf("archivos: %d", len(files))
	}
	for i, f := range files {
		var out struct {
			Chapters []media.Chapter `json:"chapters"`
		}
		if c := e.call(t, "GET", "/api/v1/stream/files/"+f.id.String()+"/chapters", e.token, nil, &out); c != 200 {
			t.Fatalf("capítulos: %d", c)
		}
		got := map[string]media.Chapter{}
		for _, c := range out.Chapters {
			got[c.Kind] = c
		}
		in, cr := got["intro"], got["credits"]
		near := func(a, b float64) bool { return a-b <= 2.5 && b-a <= 2.5 }
		if !in.Detected || !near(in.Start, pres[i]) || !near(in.End, pres[i]+40) {
			t.Errorf("%s: intro %+v; se esperaba %.0f–%.0f", f.rel, in, pres[i], pres[i]+40)
		}
		dur := float64(f.dur) / 1000
		if !cr.Detected || !near(cr.Start, dur-30) || !near(cr.End, dur) {
			t.Errorf("%s: créditos %+v; se esperaba %.0f–%.0f", f.rel, cr, dur-30, dur)
		}
	}
}
