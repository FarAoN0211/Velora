package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"net/http"
	"testing"
)

func TestPresentationAndThumbnailCacheKeepAccessChecks(t *testing.T) {
	e := newI2Env(t)
	if status := e.identify(t, e.base, 20, true); status != 200 {
		t.Fatalf("identify: %d", status)
	}
	_, err := e.pool.Exec(context.Background(), `update media_items set presentation='{}' where id=$1`, e.item)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/items/" + e.item.String() + "/presentation"
	var out struct {
		Presentation struct {
			Credits struct {
				Cast []struct {
					Name string `json:"name"`
				} `json:"cast"`
			} `json:"credits"`
		} `json:"presentation"`
	}
	if status := e.call(t, "GET", endpoint, e.token, nil, &out); status != 200 {
		t.Fatalf("presentation: %d", status)
	}
	if len(out.Presentation.Credits.Cast) != 1 || out.Presentation.Credits.Cast[0].Name != "Actriz de prueba" {
		t.Fatalf("credits not loaded: %+v", out)
	}
	res, body := e.image(t, e.token, e.item.String(), "poster", "?w=160&v=test", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail: %d %s", res.StatusCode, body)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || cfg.Width > 160 {
		t.Fatalf("thumbnail bounds: %+v %v", cfg, err)
	}
	res, cached := e.image(t, e.token, e.item.String(), "poster", "?w=160&v=test", nil)
	if res.StatusCode != 200 || !bytes.Equal(body, cached) {
		t.Fatal("cache does not return the same thumbnail")
	}
	res, _ = e.image(t, "", e.item.String(), "poster", "?w=160&v=test", nil)
	if res.StatusCode != 401 {
		t.Fatal("cache bypasses authentication", res.StatusCode)
	}
	if status := e.call(t, "GET", endpoint, "", nil, nil); status != 401 {
		t.Fatalf("anonymous presentation: %d", status)
	}
	var stored json.RawMessage
	if err = e.pool.QueryRow(context.Background(), `select presentation from media_items where id=$1`, e.item).Scan(&stored); err != nil || !bytes.Contains(stored, []byte("Actriz de prueba")) {
		t.Fatal("details not persisted", err)
	}
	if after := mediaState(t, e.mediaRoot); after != e.mediaSum {
		t.Fatal("presentation changed media files")
	}
}

func TestActorFilmographyMatchesLibraryWithoutOpeningEveryDetail(t *testing.T) {
	e := newI2Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"20"}',presentation='{}' where id=$1`, e.item)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Items []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	query := "/api/v1/items?person=100"
	check := func(token string, count int) {
		t.Helper()
		result.Items = nil
		if c := e.call(t, "GET", query, token, nil, &result); c != 200 || len(result.Items) != count {
			t.Fatalf("%d: %+v", c, result)
		}
	}
	check(e.userTok, 1)
	if result.Items[0].ID != e.item.String() {
		t.Fatal("wrong library title")
	}
	if c := e.call(t, "GET", "/api/v1/items?person=101", e.userTok, nil, &result); c != 200 || len(result.Items) != 0 {
		t.Fatal("crew work was treated as acting", c, result)
	}
	// The same TMDB number belongs to separate movie and series namespaces.
	_, err = e.pool.Exec(ctx, `update media_items set kind='series',provider_ids='{"tmdb":"77"}' where id=$1`, e.item)
	if err != nil {
		t.Fatal(err)
	}
	check(e.userTok, 1)
	if result.Items[0].Kind != "series" {
		t.Fatal("series missing")
	}
	_, err = e.pool.Exec(ctx, `delete from library_access`)
	if err != nil {
		t.Fatal(err)
	}
	check(e.userTok, 0)
	check(e.token, 1)
	if c := e.call(t, "GET", query, "", nil, nil); c != 401 {
		t.Fatal("anonymous filmography", c)
	}
	if c := e.call(t, "GET", "/api/v1/items?person=bad", e.token, nil, nil); c != 400 {
		t.Fatal("invalid person", c)
	}
	if after := mediaState(t, e.mediaRoot); after != e.mediaSum {
		t.Fatal("filmography read changed media")
	}
}
