package api

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/google/uuid"
	"velora/internal/store"
)

func TestIdentifyShowsOriginalMovieSource(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set title='Identificación equivocada',sort_title='equivocada' where id=$1`, e.pelisItem)
	if err != nil {
		t.Fatal(err)
	}
	var rel string
	if err = e.pool.QueryRow(ctx, `select rel_path from media_files where item_id=$1 limit 1`, e.pelisItem).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Sources []store.ItemSource `json:"sources"`
	}
	endpoint := "/api/v1/admin/items/" + e.pelisItem.String() + "/metadata"
	if code := e.call(t, "GET", endpoint, e.token, nil, &response); code != 200 {
		t.Fatal(code)
	}
	if len(response.Sources) == 0 {
		t.Fatal("origen ausente")
	}
	first := response.Sources[0]
	if first.Filename != path.Base(rel) || first.Folder != path.Dir(rel) || first.Library == "" {
		t.Fatalf("%+v", first)
	}
	if strings.Contains(first.Folder, e.mediaRoot) {
		t.Fatal("se ha expuesto una ruta absoluta")
	}
	e.grantB(t, e.pelis.ID)
	if code := e.call(t, "GET", endpoint, e.tokenB, nil, nil); code != 404 {
		t.Fatalf("un usuario normal ve los orígenes: %d", code)
	}
	// Multiple versions remain separate and unavailable files are still identifiable.
	_, err = e.pool.Exec(ctx, `insert into media_files(item_id,library_path_id,rel_path,size_bytes,mtime,missing_since)
  select item_id,library_path_id,'Otra carpeta/Versión 4K.mkv',size_bytes,mtime,now()
  from media_files where item_id=$1 limit 1`, e.pelisItem)
	if err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", endpoint, e.token, nil, &response)
	if len(response.Sources) != 2 || !response.Sources[1].Missing || response.Sources[1].Filename != "Versión 4K.mkv" {
		t.Fatalf("%+v", response.Sources)
	}
}

func TestIdentifyShowsOnlySeriesFolder(t *testing.T) {
	e := newSeriesEnv(t, "")
	e.scan(t, e.lib)
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `select id from media_items where library_id=$1 and kind='series' and content_path='Serie A'`, e.lib).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Sources []map[string]any `json:"sources"`
	}
	if code := e.call(t, "GET", "/api/v1/admin/items/"+id.String()+"/metadata", e.token, nil, &response); code != 200 {
		t.Fatal(code)
	}
	if len(response.Sources) != 1 || response.Sources[0]["folder"] != "Serie A" {
		t.Fatalf("%+v", response.Sources)
	}
	if _, exists := response.Sources[0]["filename"]; exists {
		t.Fatal("la serie muestra nombres de episodios")
	}
}
