package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"velora/internal/store"
)

// A series caught up through manual/imported watched flags must come back as soon
// as the scanner discovers a new episode, even without a playback timestamp.
func TestNextUpNewArrivalAfterManualOrImportedWatched(t *testing.T) {
	e := newSeriesEnv(t, "")
	ctx := context.Background()
	ep2path := filepath.Join(e.mediaRoot, "Series", "Serie A/Season 1/Serie A - 1x02 - Segundo.mkv")
	season2path := filepath.Join(e.mediaRoot, "Series", "Serie A/Season 2/Serie.A.S02E01.1080p.WEB-DL.mkv")
	src, err := os.ReadFile(ep2path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{ep2path, season2path} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	e.scan(t, e.lib)
	var series uuid.UUID
	if err := e.pool.QueryRow(ctx, `select id from media_items where title='Serie A' and kind='series'`).Scan(&series); err != nil {
		t.Fatal(err)
	}
	e.call(t, "POST", "/api/v1/me/items/"+series.String()+"/played", e.token, nil, nil)
	var home struct {
		NextUp   []store.ItemCard `json:"next_up"`
		Continue []store.ItemCard `json:"continue"`
	}
	read := func() { t.Helper(); e.call(t, "GET", "/api/v1/home", e.token, nil, &home) }
	read()
	if len(home.NextUp) != 0 {
		t.Fatalf("caught up must be absent: %+v", home.NextUp)
	}
	// Legacy/imported history may have no timestamps at all.
	if _, err := e.pool.Exec(ctx, `update user_item_data d set client_updated_at=null,last_played_at=null from media_items i where i.id=d.item_id and i.series_id=$1`, series); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep2path, src, 0644); err != nil {
		t.Fatal(err)
	}
	e.scan(t, e.lib)
	read()
	if len(home.NextUp) != 1 || home.NextUp[0].EpisodeNumber == nil || *home.NextUp[0].EpisodeNumber != 2 {
		t.Fatalf("new episode missing after imported watched history: %+v", home.NextUp)
	}
	next := home.NextUp[0].ID
	var user, path uuid.UUID
	if err := e.pool.QueryRow(ctx, `select d.user_id,i.library_path_id from user_item_data d join media_items i on i.id=d.item_id where i.series_id=$1 limit 1`, series).Scan(&user, &path); err != nil {
		t.Fatal(err)
	}
	// A failed/brief opening with zero progress must not hide the new episode.
	if _, err := e.pool.Exec(ctx, `insert into user_item_data(user_id,item_id,last_played_at) values($1,$2,now())`, user, next); err != nil {
		t.Fatal(err)
	}
	read()
	if len(home.NextUp) != 1 || home.NextUp[0].ID != next {
		t.Fatal("zero-position opening masked new arrival")
	}
	// More than forty newer series without a next episode must not consume the limit.
	for n := 0; n < 45; n++ {
		sid, sn, ep := uuid.New(), uuid.New(), uuid.New()
		for _, row := range []struct {
			id             uuid.UUID
			kind           string
			parent, series *uuid.UUID
			number         *int
		}{
			{id: sid, kind: "series"}, {id: sn, kind: "season", parent: &sid, series: &sid, number: intPtr(1)}, {id: ep, kind: "episode", parent: &sn, series: &sid, number: intPtr(1)},
		} {
			if _, err := e.pool.Exec(ctx, `insert into media_items(id,library_id,library_path_id,kind,title,sort_title,content_path,parent_id,series_id,index_number) values($1,$2,$3,$4,$5,$5,$6,$7,$8,$9)`, row.id, e.lib, path, row.kind, fmt.Sprintf("Terminada %d", n), row.id.String(), row.parent, row.series, row.number); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.pool.Exec(ctx, `insert into user_item_data(user_id,item_id,played,last_played_at) values($1,$2,true,now())`, user, ep); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if len(home.NextUp) != 1 || home.NextUp[0].ID != next {
		t.Fatal("completed series consumed the row limit")
	}
	st := &store.Store{Pool: e.pool}
	if items, err := st.NextUpEpisodes(ctx, store.Viewer{UserID: user}, 20); err != nil || len(items) != 0 {
		t.Fatalf("library permission bypass: %v %v", items, err)
	}
	// In progress belongs in Continue Watching; a hidden series stays hidden.
	e.call(t, "PUT", "/api/v1/me/items/"+next.String()+"/progress", e.token, map[string]any{"position_ms": 2000, "duration_ms": 6000}, nil)
	read()
	if len(home.NextUp) != 0 || len(home.Continue) != 1 {
		t.Fatalf("in progress duplicated: %+v", home)
	}
	e.call(t, "PUT", "/api/v1/me/items/"+next.String()+"/progress", e.token, map[string]any{"position_ms": 5900, "duration_ms": 6000}, nil)
	if err := os.MkdirAll(filepath.Dir(season2path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(season2path, src, 0644); err != nil {
		t.Fatal(err)
	}
	e.scan(t, e.lib)
	read()
	if len(home.NextUp) != 1 || *home.NextUp[0].SeasonNumber != 2 {
		t.Fatalf("new season missing: %+v", home.NextUp)
	}
	if err := st.HideContinue(ctx, user, next, true); err != nil {
		t.Fatal(err)
	}
	read()
	if len(home.NextUp) != 0 {
		t.Fatal("explicitly hidden series resurfaced")
	}
}

func intPtr(n int) *int { return &n }
