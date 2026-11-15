package api

import (
	"context"
	"github.com/google/uuid"
	"sync"
	"testing"
	"time"
	"velora/internal/store"
)

func TestMovieProgressSharedAcrossQualitiesAndUsers(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"100"}' where id=any($1::uuid[])`, []uuid.UUID{e.pelisItem, e.animItem}); err != nil {
		t.Fatal(err)
	}
	e.grantB(t, e.anim.ID)
	read := func(id uuid.UUID, token string) store.ItemDetail {
		t.Helper()
		var out struct {
			Item store.ItemDetail `json:"item"`
		}
		if c := e.call(t, "GET", "/api/v1/items/"+id.String(), token, nil, &out); c != 200 {
			t.Fatal(c)
		}
		return out.Item
	}
	save := func(id uuid.UUID, pos int64, at time.Time) {
		t.Helper()
		if c := e.call(t, "PUT", "/api/v1/me/items/"+id.String()+"/progress", e.token, map[string]any{"position_ms": pos, "duration_ms": 7200000, "client_time": at}, nil); c != 204 {
			t.Fatal(c)
		}
	}
	now := time.Now().Add(-time.Minute)
	save(e.pelisItem, 3600000, now)
	if p := read(e.animItem, e.token).PositionMS; p != 3600000 {
		t.Fatalf("4K to HD: %d", p)
	}
	save(e.animItem, 3800000, now.Add(time.Second))
	if p := read(e.pelisItem, e.token).PositionMS; p != 3800000 {
		t.Fatalf("HD to 4K: %d", p)
	}
	// A delayed stop event from the other device must not reset newer progress.
	save(e.pelisItem, 1000000, now.Add(-time.Second))
	if p := read(e.animItem, e.token).PositionMS; p != 3800000 {
		t.Fatalf("stale update: %d", p)
	}
	// Rewinds are deliberate: latest timestamp wins rather than greatest position.
	save(e.pelisItem, 2500000, now.Add(2*time.Second))
	if p := read(e.animItem, e.token).PositionMS; p != 2500000 {
		t.Fatalf("rewind: %d", p)
	}
	if p := read(e.animItem, e.tokenB).PositionMS; p != 0 {
		t.Fatalf("cross-user leak: %d", p)
	}
	if c := e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.tokenB, nil, nil); c != 404 {
		t.Fatalf("cross-library visibility %d", c)
	}
	var home struct {
		Continue []store.ItemCard `json:"continue"`
	}
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 1 || home.Continue[0].ID != e.pelisItem {
		t.Fatalf("duplicate or wrong continuation: %+v", home.Continue)
	}
	if c := e.call(t, "POST", "/api/v1/me/items/"+e.animItem.String()+"/continue-hidden", e.token, nil, nil); c != 204 {
		t.Fatal(c)
	}
	e.call(t, "GET", "/api/v1/home", e.token, nil, &home)
	if len(home.Continue) != 0 {
		t.Fatal(home)
	}
	save(e.animItem, 7000000, now.Add(3*time.Second))
	if item := read(e.pelisItem, e.token); !item.Played || item.PositionMS != 0 {
		t.Fatalf("finished not shared: %+v", item.ItemCard)
	}
	if c := e.call(t, "DELETE", "/api/v1/me/items/"+e.pelisItem.String()+"/played", e.token, nil, nil); c != 204 {
		t.Fatal(c)
	}
	if item := read(e.animItem, e.token); item.Played || item.PositionMS != 0 {
		t.Fatal("unplayed not shared")
	}
}

func TestMovieProgressUsesExistingHistoryAndNewLibraryVersions(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"100"}' where id=any($1::uuid[])`, []uuid.UUID{e.pelisItem, e.animItem})
	if err != nil {
		t.Fatal(err)
	}
	// Old independent versions are reconciled at read time without rewriting history.
	_, err = e.pool.Exec(ctx, `insert into user_item_data(user_id,item_id,position_ms,client_updated_at,last_played_at)
 select id,$1,4000000,now()-interval '2 minutes',now()-interval '2 minutes' from users where is_admin;
 `, e.pelisItem)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.animItem.String(), e.token, nil, &out)
	if out.Item.PositionMS != 4000000 {
		t.Fatal("new version did not inherit history", out.Item.PositionMS)
	}
	_, err = e.pool.Exec(ctx, `insert into user_item_data(user_id,item_id,position_ms,client_updated_at,last_played_at)
 select id,$1,3000000,now()-interval '1 minute',now()-interval '1 minute' from users where is_admin`, e.animItem)
	if err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &out)
	if out.Item.PositionMS != 3000000 {
		t.Fatal("existing newest history not selected", out.Item.PositionMS)
	}
	// Same title without an identity must not merge unrelated films.
	_, err = e.pool.Exec(ctx, `update media_items set provider_ids='{}',title='Mismo título' where id=any($1::uuid[])`, []uuid.UUID{e.pelisItem, e.animItem})
	if err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &out)
	if out.Item.PositionMS != 4000000 {
		t.Fatal("unknown identities merged", out.Item.PositionMS)
	}
}

func TestMovieProgressConcurrentQualityUpdates(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"100"}' where id=any($1::uuid[])`, []uuid.UUID{e.pelisItem, e.animItem})
	if err != nil {
		t.Fatal(err)
	}
	var user uuid.UUID
	if err = e.pool.QueryRow(ctx, `select id from users where is_admin`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := e.pelisItem
			if n%2 == 1 {
				id = e.animItem
			}
			errs <- e.srv.store.SaveProgress(ctx, user, id, int64(100000+n*1000), 1000000, at.Add(time.Duration(n)*time.Second))
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{e.pelisItem, e.animItem} {
		var out struct {
			Item store.ItemDetail `json:"item"`
		}
		e.call(t, "GET", "/api/v1/items/"+id.String(), e.token, nil, &out)
		if out.Item.PositionMS != 111000 {
			t.Fatal("newest concurrent progress lost", out.Item.PositionMS)
		}
	}
}
