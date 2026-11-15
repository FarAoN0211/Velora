package api

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"velora/internal/metadata"
	"velora/internal/store"
)

func TestCollectionsRequireTwoDistinctVisibleAvailableMovies(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	assign := func(id uuid.UUID, tmdb string) {
		t.Helper()
		_, err := e.pool.Exec(ctx, `update media_items set provider_ids=jsonb_build_object('tmdb',$2::text),presentation='{"belongs_to_collection":{"id":123,"name":"Una saga"}}'::jsonb where id=$1`, id, tmdb)
		if err != nil {
			t.Fatal(err)
		}
	}
	assign(e.pelisItem, "100")
	assign(e.animItem, "101")
	var out struct {
		Collections []store.CollectionCard `json:"collections"`
	}
	if c := e.call(t, "GET", "/api/v1/collections", e.token, nil, &out); c != 200 || len(out.Collections) != 1 || out.Collections[0].Count != 2 {
		t.Fatalf("%d %+v", c, out)
	}
	e.grantB(t, e.anim.ID)
	e.call(t, "GET", "/api/v1/collections", e.tokenB, nil, &out)
	if len(out.Collections) != 0 {
		t.Fatal("leaks inaccessible movie", out)
	}
	if c := e.call(t, "GET", "/api/v1/collections/123", e.tokenB, nil, nil); c != 404 {
		t.Fatal(c)
	}
	var detail struct {
		Items []store.ItemCard `json:"items"`
	}
	if c := e.call(t, "GET", "/api/v1/collections/123", e.token, nil, &detail); c != 200 || len(detail.Items) != 2 {
		t.Fatalf("%d %+v", c, detail)
	}
	assign(e.animItem, "100")
	e.call(t, "GET", "/api/v1/collections", e.token, nil, &out)
	if len(out.Collections) != 0 {
		t.Fatal("two versions counted as two movies", out)
	}
	assign(e.animItem, "101")
	if _, err := e.pool.Exec(ctx, `update media_files set missing_since=now() where item_id=$1`, e.animItem); err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/collections", e.token, nil, &out)
	if len(out.Collections) != 0 {
		t.Fatal("missing counted", out)
	}
}
func TestUHDEmblemFollowsLibrary(t *testing.T) {
	e := newE11Env(t)
	_, err := e.pool.Exec(context.Background(), `update libraries set name='Películas 4K' where id=$1`, e.pelis.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &out)
	if !out.Item.Library4K {
		t.Fatal("4k library not marked")
	}
	e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.token, nil, &out)
	if out.Item.Library4K {
		t.Fatal("non-4k library marked")
	}
}

func TestCollectionBackfillUsesTMDBAndKeepsCustomMetadata(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/42" || r.URL.Query().Get("language") != "es-ES" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":42,"title":"Título TMDB","belongs_to_collection":{"id":123,"name":"Saga española"}}`))
	}))
	defer fake.Close()
	os.MkdirAll(filepath.Join(e.configDir, "secrets"), 0700)
	if err := os.WriteFile(filepath.Join(e.configDir, "secrets", "tmdb.key"), []byte("test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `update media_items set title='Mi título',locked=true,provider_ids='{"tmdb":"42"}',presentation='{"custom":"kept"}' where id=$1`, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	tmdb := metadata.New(fake.URL, fake.URL, e.configDir, e.srv.store)
	it := store.PendingMetadata{ID: e.pelisItem, Kind: "movie", ProviderIDs: map[string]string{"tmdb": "42"}}
	raw, err := tmdb.CollectionMembership(ctx, it)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.srv.store.SaveMovieCollection(ctx, it, raw); err != nil {
		t.Fatal(err)
	}
	var title string
	var presentation json.RawMessage
	if err = e.pool.QueryRow(ctx, `select title,presentation from media_items where id=$1`, it.ID).Scan(&title, &presentation); err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Custom     string                `json:"custom"`
		Checked    bool                  `json:"collection_tmdb_checked"`
		Collection store.MovieCollection `json:"belongs_to_collection"`
	}
	if err = json.Unmarshal(presentation, &fields); err != nil {
		t.Fatal(err)
	}
	if title != "Mi título" || fields.Custom != "kept" || !fields.Checked || fields.Collection.ID != 123 || fields.Collection.Name != "Saga española" {
		t.Fatalf("%s %s", title, presentation)
	}
	// A concurrent reidentification must prevent a stale TMDB response being applied.
	e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"99"}',presentation='{}' where id=$1`, it.ID)
	if err = e.srv.store.SaveMovieCollection(ctx, it, raw); err != nil {
		t.Fatal(err)
	}
	e.pool.QueryRow(ctx, `select presentation from media_items where id=$1`, it.ID).Scan(&presentation)
	if string(presentation) != "{}" {
		t.Fatal("stale membership applied", string(presentation))
	}
}

func TestCollectionsKeepLibraryVersionsAndScopedOverrides(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"100"}',presentation='{"belongs_to_collection":{"id":123,"name":"Saga"}}' where id=any($1::uuid[])`, []uuid.UUID{e.pelisItem, e.animItem})
	if err != nil {
		t.Fatal(err)
	}
	clone := uuid.New()
	_, err = e.pool.Exec(ctx, `insert into media_items(id,library_id,kind,title,sort_title,content_path,library_path_id,provider_ids,presentation) select $2,library_id,'movie','Segunda','Segunda','segunda',library_path_id,'{"tmdb":"101"}',presentation from media_items where id=$1`, e.pelisItem, clone)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(ctx, `insert into media_files(item_id,library_path_id,rel_path,size_bytes,mtime) select $2,library_path_id,'segunda.mkv',100,now() from media_items where id=$1`, e.pelisItem, clone)
	if err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Collection store.CollectionCard `json:"collection"`
		Items      []store.ItemCard     `json:"items"`
	}
	if c := e.call(t, "GET", "/api/v1/collections/123", e.token, nil, &detail); c != 200 || len(detail.Items) != 3 || detail.Collection.Count != 2 {
		t.Fatalf("versions %d %+v", c, detail)
	}
	scope := "?library_id=" + e.pelis.ID.String()
	if c := e.call(t, "GET", "/api/v1/collections/123"+scope, e.token, nil, &detail); c != 200 || len(detail.Items) != 2 {
		t.Fatalf("scope %d %+v", c, detail)
	}
	if c := e.call(t, "GET", "/api/v1/collections/123?library_id="+e.anim.ID.String(), e.token, nil, nil); c != 404 {
		t.Fatalf("one film threshold %d", c)
	}
	body := map[string]any{"title": "Mi saga", "overview": "Sinopsis", "poster_id": clone}
	if c := e.call(t, "PATCH", "/api/v1/admin/collections/123"+scope, e.token, body, nil); c != 200 {
		t.Fatal(c)
	}
	e.call(t, "GET", "/api/v1/collections/123"+scope, e.token, nil, &detail)
	if detail.Collection.Name != "Mi saga" || detail.Collection.PosterID != clone || !detail.Collection.CustomPoster {
		t.Fatal(detail.Collection)
	}
	e.call(t, "GET", "/api/v1/collections/123", e.token, nil, &detail)
	if detail.Collection.Name != "Saga" {
		t.Fatal("scope leaked", detail.Collection)
	}
	body["poster_id"] = e.animItem
	if c := e.call(t, "PATCH", "/api/v1/admin/collections/123"+scope, e.token, body, nil); c != 422 {
		t.Fatalf("foreign poster %d", c)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/collections/123", e.tokenB, body, nil); c != 404 && c != 403 {
		t.Fatalf("nonadmin %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/collections?library_id=wrong", e.token, nil, nil); c != 400 {
		t.Fatal(c)
	}
}

func TestCollectionPosterPrefersSpanishAndCaches(t *testing.T) {
	e := newE11Env(t)
	calls := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/collection/123/images":
			calls++
			if r.URL.Query().Get("include_image_language") != "es,null,en" {
				t.Error(r.URL)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"posters":[{"file_path":"/english.jpg","iso_639_1":"en","vote_average":10,"width":1000},{"file_path":"/spanish.jpg","iso_639_1":"es","vote_average":1,"width":500}]}`))
		case "/w500/spanish.jpg":
			w.Write([]byte("spanish-image"))
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()
	os.MkdirAll(filepath.Join(e.configDir, "secrets"), 0700)
	os.WriteFile(filepath.Join(e.configDir, "secrets", "tmdb.key"), []byte("test-key"), 0600)
	tmdb := metadata.New(fake.URL, fake.URL, e.configDir, e.srv.store)
	for i := 0; i < 2; i++ {
		p, err := tmdb.CollectionPoster(context.Background(), 123)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		if err != nil || string(b) != "spanish-image" {
			t.Fatalf("%s %v", b, err)
		}
	}
	if calls != 1 {
		t.Fatal("cache missed", calls)
	}
}
