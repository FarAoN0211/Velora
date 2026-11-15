package api

// E1.3 I1 · «Actualizar información» contra un TMDB simulado: respeta los candados en el momento de aplicar (también lo
// editado a mano mientras la actualización esperaba en la cola), no toca las imágenes locales y no admite dos
// actualizaciones de la misma ficha a la vez.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"velora/internal/metadata"
	"velora/internal/store"
)

func TestRefreshRespectsLocksAndLocalImages(t *testing.T) {
	fake := http.NewServeMux()
	fake.HandleFunc("/3/configuration", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	fake.HandleFunc("/3/search/movie", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"results":[{"id":603}]}`)) })
	fake.HandleFunc("/3/movie/603", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":603,"title":"Matrix","original_title":"The Matrix","overview":"Sinopsis de TMDB.","tagline":"Lema de TMDB",
			"release_date":"1999-03-30","runtime":136,"poster_path":"/p.jpg","backdrop_path":"/b.jpg","genres":[{"name":"Acción"}]}`))
	})
	fake.HandleFunc("/img/w500/p.jpg", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("POSTER-TMDB")) })
	fake.HandleFunc("/img/w1280/b.jpg", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("FONDO-TMDB")) })
	ts := httptest.NewServer(fake)
	defer ts.Close()

	e := newE1Env(t, ts.URL)
	ctx := context.Background()
	if c := e.call(t, "PUT", "/api/v1/admin/metadata/tmdb", e.token, map[string]string{"api_key": "clave"}, nil); c != 200 {
		t.Fatalf("clave: %d", c)
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Pelis", "path": filepath.Join(e.mediaRoot, "Peliculas")}, &created)
	e.scan(t, created.Library.ID)
	var id uuid.UUID
	if err := e.pool.QueryRow(ctx, `select id from media_items limit 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/admin/items/" + id.String()
	st := &store.Store{Pool: e.pool}
	tm := metadata.New(ts.URL, ts.URL+"/img", e.configDir, st)

	// Título editado a mano (queda bloqueado).
	var meta struct {
		Metadata store.EditableMetadata `json:"metadata"`
	}
	e.call(t, "GET", base+"/metadata", e.token, nil, &meta)
	edit := meta.Metadata
	edit.Title = "Mi título"
	if c := e.call(t, "PATCH", base+"/metadata", e.token, edit, &meta); c != 200 {
		t.Fatalf("editar: %d", c)
	}

	// Se pide «Volver a descargar todo»; una segunda petición mientras está en cola se rechaza.
	if c := e.call(t, "POST", base+"/refresh", e.token, map[string]string{"mode": "all"}, nil); c != 202 {
		t.Fatalf("actualizar: %d", c)
	}
	if c := e.call(t, "POST", base+"/refresh", e.token, map[string]string{"mode": "missing"}, nil); c != 409 {
		t.Fatalf("una segunda actualización a la vez debe rechazarse: %d", c)
	}
	// Mientras espera en la cola, el administrador escribe la sinopsis a mano: también debe respetarse.
	e.call(t, "GET", base+"/metadata", e.token, nil, &meta)
	edit = meta.Metadata
	edit.Overview = "Sinopsis mía."
	e.call(t, "PATCH", base+"/metadata", e.token, edit, &meta)

	// El trabajo se ejecuta (la misma llamada que hace item_refresh).
	it, _, err := st.ItemForRefresh(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := tm.Refresh(ctx, it, false); err != nil || !ok {
		t.Fatalf("Refresh: %v %v", ok, err)
	}
	e.call(t, "GET", base+"/metadata", e.token, nil, &meta)
	m := meta.Metadata
	if m.Title != "Mi título" || m.Overview != "Sinopsis mía." {
		t.Fatalf("lo editado a mano se ha sobrescrito: %q / %q", m.Title, m.Overview)
	}
	if m.Tagline != "Lema de TMDB" || m.OriginalTitle != "The Matrix" {
		t.Fatalf("lo no bloqueado debería actualizarse: %+v", m)
	}
	// Imágenes: la carátula local de la carpeta (poster.jpg) sigue siendo la que se muestra; la de TMDB se guarda aparte.
	img, err := st.ImageFor(ctx, store.Viewer{Admin: true}, id, "poster")
	if err != nil || img.Source != "local" {
		t.Fatalf("debe seguir mostrándose la carátula local: %+v %v", img, err)
	}
	var tmdbPoster string
	if err := e.pool.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = 'poster' and source = 'tmdb'`, id).Scan(&tmdbPoster); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(e.configDir, "metadata", tmdbPoster)); string(raw) != "POSTER-TMDB" {
		t.Fatal("la carátula de TMDB debería haberse descargado aparte")
	}
	if local, _ := os.ReadFile(filepath.Join(e.movieDir, "poster.jpg")); len(local) == 0 || string(local) == "POSTER-TMDB" {
		t.Fatal("la carátula local no debe tocarse")
	}
	// Ficha bloqueada entera: «Actualizar» no cambia nada.
	edit = m
	edit.Locked = true
	e.call(t, "PATCH", base+"/metadata", e.token, edit, &meta)
	_, locked, _ := st.ItemForRefresh(ctx, id)
	if !locked {
		t.Fatal("la ficha debería figurar como bloqueada (el trabajo la omite)")
	}
}
