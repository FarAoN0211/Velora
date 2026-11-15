package api

import (
	"context"
	"strconv"
	"testing"
)

func TestCatalogRevisionCommittedChanges(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	revision := func() int64 {
		var r struct {
			Revision string `json:"revision"`
		}
		if code := e.call(t, "GET", "/api/v1/catalog/revision", e.tokenB, nil, &r); code != 200 {
			t.Fatal(code)
		}
		n, err := strconv.ParseInt(r.Revision, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := revision()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `update media_items set title='Título detectado' where id=$1`, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if revision() != before {
		t.Fatal("publicó datos antes del commit")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after := revision()
	if after <= before {
		t.Fatal("no avisó del título")
	}
	if _, err = e.pool.Exec(ctx, `insert into item_images(item_id,kind,source,path) values($1,'logo','tmdb','logo.png')`, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if revision() <= after {
		t.Fatal("no avisó del logo")
	}
	if code := e.call(t, "GET", "/api/v1/catalog/revision", "", nil, nil); code != 401 {
		t.Fatalf("sin sesión: %d", code)
	}
}
func TestAutomaticLogoPreservesManual(t *testing.T) {
	e := newI2Env(t)
	e.identify(t, e.base, 20, false)
	it, _, err := e.st.ItemForRefresh(context.Background(), e.item)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := e.tm.EnsureLogo(context.Background(), it); err != nil || !ok {
		t.Fatalf("logo: %t %v", ok, err)
	}
	if !e.st.HasAnyImage(context.Background(), e.item, "logo") {
		t.Fatal("logo no publicado")
	}
	if ok, err := e.tm.EnsureLogo(context.Background(), it); err != nil || ok {
		t.Fatalf("descarga repetida: %t %v", ok, err)
	}
	if _, err = e.pool.Exec(context.Background(), `delete from item_images where item_id=$1 and kind='logo';`, e.item); err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(context.Background(), `insert into item_images(item_id,kind,source,path) values($1,'logo','user','manual.png')`, e.item); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.tm.EnsureLogo(context.Background(), it); err != nil || ok {
		t.Fatalf("pisó manual: %t %v", ok, err)
	}
}
