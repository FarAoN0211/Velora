package api

// E1.2 · Listado de la biblioteca: más órdenes, filtro por género y por vistas, y el resumen del panel de control.

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"velora/internal/store"
)

func TestLibrarySortAndFilters(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	larga := uuid.Nil
	if err := e.pool.QueryRow(ctx, `select id from media_items where title = 'Larga'`).Scan(&larga); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`update media_items set genres = '{Animación,Familia}', year = 2003, community_rating = 6.5 where title = 'Dibujos'`,
		`update media_items set genres = '{Drama}', year = 2010, community_rating = 8.1 where title = 'Larga'`,
		`update media_items set genres = '{Drama,Acción}', year = 2020 where library_id = '` + e.pelis.ID.String() + `'`,
	} {
		if _, err := e.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	e.grantB(t, e.anim.ID)
	list := func(token, query string) []uuid.UUID {
		t.Helper()
		var out struct {
			Items []store.ItemCard `json:"items"`
		}
		if c := e.call(t, "GET", "/api/v1/items?library_id="+e.anim.ID.String()+"&"+query, token, nil, &out); c != 200 {
			t.Fatalf("%s: %d", query, c)
		}
		ids := []uuid.UUID{}
		for _, i := range out.Items {
			ids = append(ids, i.ID)
		}
		return ids
	}
	same := func(got []uuid.UUID, want ...uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// Órdenes: año (recientes primero) y su inverso; puntuación; los nulos siempre al final.
	if got := list(e.tokenB, "sort=year"); !same(got, larga, e.animItem) {
		t.Fatalf("por año: %v", got)
	}
	if got := list(e.tokenB, "sort=year&order=reverse"); !same(got, e.animItem, larga) {
		t.Fatalf("por año inverso: %v", got)
	}
	if got := list(e.tokenB, "sort=rating"); !same(got, larga, e.animItem) {
		t.Fatalf("por puntuación: %v", got)
	}
	for _, s := range []string{"title", "added", "runtime", "last_played", "resolution"} {
		if got := list(e.tokenB, "sort="+s); len(got) != 2 {
			t.Fatalf("orden %s: %v", s, got)
		}
	}
	if c := e.call(t, "GET", "/api/v1/items?sort=nombre", e.tokenB, nil, nil); c != 400 {
		t.Fatalf("un orden desconocido debe dar 400: %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/items?played=quizas", e.tokenB, nil, nil); c != 400 {
		t.Fatalf("un filtro de vistas desconocido debe dar 400: %d", c)
	}

	// Género: solo los títulos de ese género, y la lista de géneros solo cuenta lo que el usuario puede ver.
	if got := list(e.tokenB, "genre=Drama"); !same(got, larga) {
		t.Fatalf("género Drama: %v", got)
	}
	genres := func(token string) map[string]int {
		var out struct {
			Genres []store.GenreCount `json:"genres"`
		}
		e.call(t, "GET", "/api/v1/genres", token, nil, &out)
		m := map[string]int{}
		for _, g := range out.Genres {
			m[g.Name] = g.Count
		}
		return m
	}
	if g := genres(e.tokenB); g["Drama"] != 1 || g["Acción"] != 0 || g["Animación"] != 1 {
		t.Fatalf("géneros de B (sin la biblioteca Películas): %v", g)
	}
	if g := genres(e.token); g["Drama"] != 2 || g["Acción"] != 1 {
		t.Fatalf("géneros del administrador: %v", g)
	}
	if c := e.call(t, "GET", "/api/v1/genres?library_id="+e.pelis.ID.String(), e.tokenB, nil, nil); c != 200 {
		t.Fatalf("géneros de una biblioteca sin acceso: %d", c)
	}
	if g := func() int {
		var out struct {
			Genres []store.GenreCount `json:"genres"`
		}
		e.call(t, "GET", "/api/v1/genres?library_id="+e.pelis.ID.String(), e.tokenB, nil, &out)
		return len(out.Genres)
	}(); g != 0 {
		t.Fatalf("una biblioteca sin acceso no debe revelar sus géneros: %d", g)
	}

	// Vistas / no vistas, por usuario.
	if c := e.call(t, "POST", "/api/v1/me/items/"+e.animItem.String()+"/played", e.tokenB, nil, nil); c != 204 {
		t.Fatalf("marcar vista: %d", c)
	}
	if got := list(e.tokenB, "played=played"); !same(got, e.animItem) {
		t.Fatalf("vistas: %v", got)
	}
	if got := list(e.tokenB, "played=unplayed"); !same(got, larga) {
		t.Fatalf("no vistas: %v", got)
	}
	if got := list(e.token, "played=played"); len(got) != 0 {
		t.Fatalf("lo visto por B no cuenta para el administrador: %v", got)
	}
}

func TestAdminOverview(t *testing.T) {
	e := newE11Env(t)
	if c := e.call(t, "GET", "/api/v1/admin/overview", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("un usuario normal no ve el panel (404, como el resto de rutas de administración): %d", c)
	}
	var out struct {
		Version string               `json:"version"`
		Counts  store.OverviewCounts `json:"counts"`
		Max     int                  `json:"max_optimized_streams"`
	}
	if c := e.call(t, "GET", "/api/v1/admin/overview", e.token, nil, &out); c != 200 {
		t.Fatalf("panel: %d", c)
	}
	if out.Counts.Libraries != 2 || out.Counts.Movies != 3 || out.Counts.Users != 2 || out.Max < 1 || out.Version == "" {
		t.Fatalf("resumen: %+v", out)
	}
}

// Paginación de una en una con cada orden, en los dos sentidos y con valores vacíos (sin año, sin puntuación, sin
// género): se recorre todo sin repetir ni saltar títulos («Cargar más» no debe duplicar nada).
func TestLibraryPagingIsStable(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `update media_items set year = null, community_rating = null, genres = null where title = 'Larga'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set year = 2003, community_rating = 7 where title <> 'Larga'`); err != nil {
		t.Fatal(err) // empates a propósito: solo desempata el título y el id
	}
	all := e.itemIDs(t, e.token, "")
	if len(all) != 3 {
		t.Fatalf("se esperaban 3 películas, hay %d", len(all))
	}
	for _, sort := range []string{"title", "added", "year", "rating", "runtime", "last_played", "resolution"} {
		for _, order := range []string{"", "&order=reverse"} {
			seen := map[uuid.UUID]bool{}
			for offset := 0; offset < 5; offset++ {
				var out struct {
					Items []store.ItemCard `json:"items"`
				}
				e.call(t, "GET", "/api/v1/items?limit=1&offset="+strconv.Itoa(offset)+"&sort="+sort+order, e.token, nil, &out)
				for _, i := range out.Items {
					if seen[i.ID] {
						t.Fatalf("%s%s: %s repetido en la posición %d", sort, order, i.Title, offset)
					}
					seen[i.ID] = true
				}
			}
			if len(seen) != len(all) {
				t.Fatalf("%s%s: se recorrieron %d de %d", sort, order, len(seen), len(all))
			}
		}
	}
	// Sin género: no aparece al filtrar por uno, pero sí sin filtro.
	var out struct {
		Items []store.ItemCard `json:"items"`
	}
	e.call(t, "GET", "/api/v1/items?genre=Drama", e.token, nil, &out)
	for _, i := range out.Items {
		if i.Title == "Larga" {
			t.Fatal("un título sin géneros no debe salir al filtrar por género")
		}
	}
}

// La calidad se decide por el ancho o el alto: las películas panorámicas de 1920×800 son
// 1080p, no 720p. Afecta a la tarjeta, al orden «Calidad de imagen» y a la ficha.
func TestQualityByWidthOrHeight(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	quality := func(id uuid.UUID) string {
		t.Helper()
		var out struct {
			Items []store.ItemCard `json:"items"`
		}
		e.call(t, "GET", "/api/v1/items?limit=50", e.token, nil, &out)
		for _, i := range out.Items {
			if i.ID == id {
				return i.Quality
			}
		}
		t.Fatalf("no está %s", id)
		return ""
	}
	cases := []struct {
		w, h int
		want string
	}{
		{1920, 800, "1080p"}, {1920, 1080, "1080p"}, {1918, 802, "1080p"}, {1920, 960, "1080p"},
		{3840, 1600, "4K"}, {3840, 2160, "4K"}, {1280, 536, "720p"}, {1280, 720, "720p"},
		{1080, 608, "SD"}, {720, 576, "SD"}, {708, 892, "720p"},
	}
	for _, c := range cases {
		if _, err := e.pool.Exec(ctx, `update media_streams set width = $2, height = $3 where kind = 'video' and file_id = $1`, e.pelisFile, c.w, c.h); err != nil {
			t.Fatal(err)
		}
		if got := quality(e.pelisItem); got != c.want {
			t.Errorf("%dx%d → %q; esperaba %q", c.w, c.h, got, c.want)
		}
	}
	// La ficha trae el ancho para que la web aplique la misma regla.
	var detail struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	var w *int
	for _, st := range detail.Item.Files[0].Streams {
		if st.Kind == "video" {
			w = st.Width
		}
	}
	if w == nil || *w != 708 {
		t.Fatalf("la ficha debe traer el ancho del vídeo: %v", w)
	}
	// Orden «Calidad de imagen»: un 1920×800 (1080p) va por delante de un 1280×720 (720p).
	if _, err := e.pool.Exec(ctx, `update media_streams set width = 1920, height = 800 where kind = 'video' and file_id = $1`, e.pelisFile); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `update media_streams st set width = 1280, height = 720 from media_files f
		where st.file_id = f.id and st.kind = 'video' and f.item_id = $1`, e.animItem); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items []store.ItemCard `json:"items"`
	}
	e.call(t, "GET", "/api/v1/items?sort=resolution&limit=50", e.token, nil, &out)
	pos := map[uuid.UUID]int{}
	for i, it := range out.Items {
		pos[it.ID] = i
	}
	if pos[e.pelisItem] > pos[e.animItem] {
		t.Fatalf("1080p panorámico debe ir antes que 720p: %v", out.Items)
	}
}
