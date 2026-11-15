package api

// E1.3 I1 · Editar información con candados, actualizar desde TMDB y ver archivos (solo administradores).

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"velora/internal/store"
)

func TestEditMetadataWithLocks(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	st := &store.Store{Pool: e.pool}
	base := "/api/v1/admin/items/" + e.pelisItem.String()
	fileBefore, _ := os.Stat(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))

	type metaResp struct {
		Metadata store.EditableMetadata `json:"metadata"`
		Changed  []string               `json:"changed"`
	}
	var cur metaResp
	if c := e.call(t, "GET", base+"/metadata", e.token, nil, &cur); c != 200 || cur.Metadata.Title == "" {
		t.Fatalf("leer: %d %+v", c, cur)
	}
	// Solo administradores, en todas las rutas.
	e.grantB(t, e.pelis.ID)
	for _, rt := range []struct{ m, p string }{{"GET", "/metadata"}, {"PATCH", "/metadata"}, {"POST", "/refresh"}, {"GET", "/files"}} {
		if c := e.call(t, rt.m, base+rt.p, e.tokenB, map[string]any{"title": "x", "mode": "all"}, nil); c != 404 {
			t.Fatalf("%s %s para un usuario normal: %d", rt.m, rt.p, c)
		}
	}

	// Editar: los campos cambiados quedan bloqueados solos; el texto se guarda tal cual (sin interpretar HTML).
	edit := cur.Metadata
	edit.Title = "Título corregido <b>"
	edit.Overview = "Sinopsis escrita a mano.\nSegunda línea."
	edit.Genres = []string{"Drama", "drama", " Suspense "}
	var saved metaResp
	if c := e.call(t, "PATCH", base+"/metadata", e.token, edit, &saved); c != 200 {
		t.Fatalf("guardar: %d", c)
	}
	m := saved.Metadata
	if m.Title != "Título corregido <b>" || m.Overview != "Sinopsis escrita a mano.\nSegunda línea." || strings.Join(m.Genres, ",") != "Drama,Suspense" {
		t.Fatalf("guardado: %+v", m)
	}
	for _, f := range []string{"title", "overview", "genres"} {
		if !slices.Contains(m.LockedFields, f) || !slices.Contains(saved.Changed, f) {
			t.Fatalf("%s debería quedar bloqueado y como cambiado: %v / %v", f, m.LockedFields, saved.Changed)
		}
	}
	if slices.Contains(m.LockedFields, "tagline") {
		t.Fatal("un campo sin cambiar no se bloquea solo")
	}
	// TMDB (o un escaneo) no cambia lo bloqueado y sí lo demás.
	if err := st.ApplyMetadata(ctx, e.pelisItem, store.MetadataFields{Title: "De TMDB", Overview: "Sinopsis de TMDB",
		Tagline: "Lema de TMDB", Genres: []string{"Acción"}, ProviderIDs: map[string]string{}}, "de tmdb"); err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", base+"/metadata", e.token, nil, &cur)
	if cur.Metadata.Title != "Título corregido <b>" || cur.Metadata.Overview != "Sinopsis escrita a mano.\nSegunda línea." ||
		strings.Join(cur.Metadata.Genres, ",") != "Drama,Suspense" || cur.Metadata.Tagline != "Lema de TMDB" {
		t.Fatalf("los bloqueos no se respetaron: %+v", cur.Metadata)
	}
	// Desbloquear a mano el título (sin cambiarlo): TMDB ya puede actualizarlo.
	edit = cur.Metadata
	edit.LockedFields = []string{"overview", "genres"}
	e.call(t, "PATCH", base+"/metadata", e.token, edit, &saved)
	if slices.Contains(saved.Metadata.LockedFields, "title") {
		t.Fatalf("el título debería quedar desbloqueado: %v", saved.Metadata.LockedFields)
	}
	_ = st.ApplyMetadata(ctx, e.pelisItem, store.MetadataFields{Title: "De TMDB", ProviderIDs: map[string]string{}}, "de tmdb")
	e.call(t, "GET", base+"/metadata", e.token, nil, &cur)
	if cur.Metadata.Title != "De TMDB" {
		t.Fatalf("con el candado abierto TMDB debe poder cambiarlo: %q", cur.Metadata.Title)
	}
	// «Completar lo que falta»: no sustituye lo que existe y rellena lo vacío.
	edit = cur.Metadata
	edit.Tagline = ""
	edit.LockedFields = nil
	e.call(t, "PATCH", base+"/metadata", e.token, edit, &saved)
	// (al vaciar el lema queda bloqueado por haberlo cambiado: se desbloquea para la prueba)
	edit = saved.Metadata
	edit.LockedFields = nil
	e.call(t, "PATCH", base+"/metadata", e.token, edit, &saved)
	_ = st.ApplyMetadata(ctx, e.pelisItem, store.MetadataFields{Title: "Otro", Overview: "Otra sinopsis", Tagline: "Lema nuevo",
		ProviderIDs: map[string]string{}}, "otro", true)
	e.call(t, "GET", base+"/metadata", e.token, nil, &cur)
	if cur.Metadata.Title != "De TMDB" || cur.Metadata.Overview != "Sinopsis escrita a mano.\nSegunda línea." || cur.Metadata.Tagline != "Lema nuevo" {
		t.Fatalf("completar lo que falta: %+v", cur.Metadata)
	}

	// Validación.
	for _, bad := range []map[string]any{{"title": "  "}, {"title": "x", "year": 1700}, {"title": "x", "rating_age": 40},
		{"title": "x", "locked_fields": []string{"inventado"}}} {
		if c := e.call(t, "PATCH", base+"/metadata", e.token, bad, nil); c != 422 {
			t.Fatalf("%v debería rechazarse: %d", bad, c)
		}
	}
	// Sin clave de TMDB, actualizar avisa; un modo desconocido es un error.
	if c := e.call(t, "POST", base+"/refresh", e.token, map[string]string{"mode": "all"}, nil); c != 409 {
		t.Fatalf("sin TMDB: %d", c)
	}
	if c := e.call(t, "POST", base+"/refresh", e.token, map[string]string{"mode": "todo"}, nil); c != 400 {
		t.Fatalf("modo desconocido: %d", c)
	}

	// Ver archivos: rutas relativas a la biblioteca, nunca la del servidor.
	var files struct {
		Files []store.AdminFile `json:"files"`
	}
	e.call(t, "GET", base+"/files", e.token, nil, &files)
	if len(files.Files) == 0 || strings.HasPrefix(files.Files[0].RelPath, "/") || files.Files[0].Library != "Películas" || files.Files[0].Kind != "movie" {
		t.Fatalf("archivos: %+v", files.Files)
	}
	// Ningún archivo multimedia se ha tocado.
	fileAfter, _ := os.Stat(filepath.Join(e.movieDir, "Prueba sintética (2020).mkv"))
	if fileAfter.Size() != fileBefore.Size() || !fileAfter.ModTime().Equal(fileBefore.ModTime()) {
		t.Fatal("el archivo de la película no debe cambiar")
	}
}
