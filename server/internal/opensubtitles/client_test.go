package opensubtitles

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchUsesMetadataAndPreservesFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "test-key" || r.Header.Get("User-Agent") == "" {
			t.Error("missing API headers")
		}
		if r.URL.Query().Get("parent_imdb_id") != "123" || r.URL.Query().Get("languages") != "es,en" {
			t.Error("lost identity or languages")
		}
		fmt.Fprint(w, `{"data":[{"attributes":{"language":"es","release":"Edition A","foreign_parts_only":true,"hearing_impaired":false,"files":[{"file_id":42}]}},{"attributes":{"language":"en","hearing_impaired":true,"files":[{"file_id":43}]}}]}`)
	}))
	defer srv.Close()
	c := New(Config{APIKey: "test-key", Languages: "es,en"})
	c.Base = srv.URL
	got, err := c.Search(context.Background(), url.Values{"parent_imdb_id": {"123"}})
	if err != nil || len(got) != 2 || !got[0].Forced || !got[1].HearingImpaired {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestQuotaAndCredentialStorage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); fmt.Fprint(w, `{"message":"quota"}`) }))
	defer srv.Close()
	c := New(Config{APIKey: "key"})
	c.Base = srv.URL
	if _, err := c.Search(context.Background(), url.Values{}); err == nil {
		t.Fatal("quota error ignored")
	}
	dir := t.TempDir()
	cfg := Config{APIKey: "key", Enabled: true, Username: "user", Password: "secret"}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "secrets", "opensubtitles.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credentials must be private", err)
	}
	loaded, err := Load(dir)
	if err != nil || loaded.Config.Password != "secret" {
		t.Fatal("configuration round trip", err)
	}
	if Valid(Config{APIKey: "key", Languages: "../../etc"}) {
		t.Fatal("invalid language accepted")
	}
}
