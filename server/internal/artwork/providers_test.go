package artwork

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFanartAndOMDbByExactIdentity(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movies/550":
			if r.Header.Get("api-key") != "project" {
				t.Error("falta credencial fanart")
			}
			w.Write([]byte(`{"hdmovielogo":[{"url":"https://assets.fanart.tv/fanart/logo.png","lang":"es","likes":"7","width":800,"height":"310"}],"moviedisc":[{"url":"https://assets.fanart.tv/fanart/disc.png","lang":"00"}],"movieposter":[{"url":"http://127.0.0.1/private.jpg"}]}`))
		case "/tv/42":
			w.Write([]byte(`{"hdtvlogo":[{"url":"https://assets.fanart.tv/fanart/tv.png","lang":"es"}]}`))
		case "/":
			if r.URL.Query().Get("i") != "tt0137523" {
				t.Error("no usó IMDb")
			}
			w.Write([]byte(`{"Response":"True","Poster":"https://m.media-amazon.com/poster.jpg"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	s := New(nil, t.TempDir())
	s.FanartURL = ts.URL
	s.OMDbURL = ts.URL
	list, err := s.fanart(context.Background(), Config{APIKey: "project"}, Target{Kind: "movie", IDs: map[string]string{"tmdb": "550"}})
	if err != nil || len(list) != 2 {
		t.Fatalf("fanart: %+v %v", list, err)
	}
	found := false
	for _, im := range list {
		if im.Kind == "logo" {
			found = true
			if im.Language != "es" || im.Width != 800 || im.Height != 310 {
				t.Fatal(im)
			}
		}
	}
	if !found {
		t.Fatal("sin logo")
	}
	tv, err := s.fanart(context.Background(), Config{APIKey: "project"}, Target{Kind: "series", IDs: map[string]string{"tvdb": "42", "tmdb": "999"}})
	if err != nil || len(tv) != 1 {
		t.Fatalf("TV: %+v %v", tv, err)
	}
	omdb, err := s.omdb(context.Background(), Config{APIKey: "omdb"}, Target{Kind: "movie", IDs: map[string]string{"imdb": "tt0137523"}})
	if err != nil || len(omdb) != 1 || omdb[0].Kind != "poster" {
		t.Fatalf("OMDb: %+v %v", omdb, err)
	}
}
func TestTVDBLoginAndDynamicArtworkTypes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" && r.Header.Get("Authorization") != "Bearer token" {
			t.Error("sin bearer")
		}
		switch r.URL.Path {
		case "/login":
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["apikey"] != "api" || b["pin"] != "pin" {
				t.Error("credenciales incompletas")
			}
			w.Write([]byte(`{"data":{"token":"token"}}`))
		case "/search/remoteid/tt42":
			w.Write([]byte(`{"data":[{"movie":{"id":42}}]}`))
		case "/artwork/types":
			w.Write([]byte(`{"data":[{"id":99,"name":"Movie ClearLogo"},{"id":98,"name":"Movie Poster"}]}`))
		case "/movies/42/extended":
			w.Write([]byte(`{"data":{"artworks":[{"image":"https://artworks.thetvdb.com/logo.png","type":99,"language":"spa","width":800,"height":310}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	s := New(nil, t.TempDir())
	s.TVDBURL = ts.URL
	list, err := s.tvdb(context.Background(), Config{APIKey: "api", PIN: "pin"}, Target{Kind: "movie", IDs: map[string]string{"imdb": "tt42"}})
	if err != nil || len(list) != 1 || list[0].Kind != "logo" || list[0].Language != "es" {
		t.Fatalf("TVDB: %+v %v", list, err)
	}
}
func TestSecretsAndUnsafeURLs(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "fanart", Config{Enabled: true, APIKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "secrets", "image-providers.json"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("permisos: %v %v", st, err)
	}
	c, err := Load(dir)
	if err != nil || c["fanart"].APIKey != "secret" {
		t.Fatal(c, err)
	}
	for _, raw := range []string{"http://assets.fanart.tv/x", "https://assets.fanart.tv.evil.test/x", "https://127.0.0.1/x", "https://user:pass@assets.fanart.tv/x", "https://assets.fanart.tv:444/x"} {
		if AllowedURL("fanart", raw) {
			t.Fatal("admitió", raw)
		}
	}
	if NormalizeURL("fanart", "http://assets.fanart.tv/fanart/x.png") != "https://assets.fanart.tv/fanart/x.png" {
		t.Fatal("no actualizó la referencia antigua a HTTPS")
	}
	if !AllowedURL("fanart", "https://assets.fanart.tv/fanart/x.png") {
		t.Fatal("rechazó dominio válido")
	}
	if err := Save(dir, "../secrets", Config{}); err == nil {
		t.Fatal("admitió ruta")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "secrets", "image-providers.json"))
	if strings.Contains(string(data), "../") {
		t.Fatal("guardó fuente inválida")
	}
}
func TestSourcePreviewDoesNotWaitForOtherProvider(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/movies/") {
			calls++
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`{"Response":"True","Poster":"https://m.media-amazon.com/poster.jpg"}`))
	}))
	defer ts.Close()
	dir := t.TempDir()
	Save(dir, "fanart", Config{APIKey: "a", Enabled: true})
	Save(dir, "omdb", Config{APIKey: "b", Enabled: true})
	s := New(nil, dir)
	s.FanartURL = ts.URL
	s.OMDbURL = ts.URL
	images, err := s.GallerySource(context.Background(), Target{Kind: "movie", IDs: map[string]string{"tmdb": "550", "imdb": "tt0137523"}}, "omdb")
	if err != nil || len(images) != 1 || calls != 0 {
		t.Fatalf("consulta cruzada: %+v %v %d", images, err, calls)
	}
}
