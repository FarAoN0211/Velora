package api

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"velora/internal/store"
)

func TestQualityIgnoresEmbeddedArtworkEverywhere(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `delete from media_streams where file_id=$1`, e.pelisFile); err != nil {
		t.Fatal(err)
	}
	_, err := e.pool.Exec(ctx, `insert into media_streams(file_id,stream_index,kind,codec,width,height) values ($1,0,'video','h264',1920,1080),($1,1,'video','mjpeg',2000,3000),($1,2,'video','h264',5000,5000)`, e.pelisFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(ctx, `update media_files set probe='{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"index":1,"codec_type":"video","codec_name":"mjpeg","width":2000,"height":3000,"disposition":{"attached_pic":1}},{"index":2,"codec_type":"video","codec_name":"h264","width":5000,"height":5000,"disposition":{"attached_pic":1}}]}' where id=$1`, e.pelisFile)
	if err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Item store.ItemDetail `json:"item"`
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	if detail.Item.Quality != "1080p" {
		t.Fatalf("artwork counted as film: %q", detail.Item.Quality)
	}
	if len(detail.Item.Files[0].Streams) != 1 || detail.Item.Files[0].Streams[0].Codec != "h264" {
		t.Fatal("artwork exposed as playable video", detail.Item.Files[0].Streams)
	}
	var list struct {
		Items []store.ItemCard `json:"items"`
	}
	e.call(t, "GET", "/api/v1/items?sort=resolution", e.token, nil, &list)
	found := false
	for _, it := range list.Items {
		if it.ID == e.pelisItem {
			found = true
			if it.Quality != "1080p" {
				t.Fatal(it)
			}
		}
	}
	if !found {
		t.Fatal("missing film")
	}
	// Preserve genuine 2160p and cropped widescreen films.
	_, err = e.pool.Exec(ctx, `update media_streams set width=3840,height=1600 where file_id=$1 and stream_index=0`, e.pelisFile)
	if err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/items/"+e.pelisItem.String(), e.token, nil, &detail)
	if detail.Item.Quality != "4K" {
		t.Fatal(detail.Item.Quality)
	}
}
func TestCollectionKeepsDifferentResolutionsInSameLibrary(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set provider_ids=jsonb_build_object('tmdb',case when id=$1 then '100' else '101' end),presentation='{"belongs_to_collection":{"id":123,"name":"Saga"}}' where id=any($2::uuid[])`, e.pelisItem, []uuid.UUID{e.pelisItem, e.animItem})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(ctx, `update media_streams set width=1920,height=1080 where file_id=$1 and kind='video'`, e.pelisFile)
	if err != nil {
		t.Fatal(err)
	}
	clone, file := uuid.New(), uuid.New()
	_, err = e.pool.Exec(ctx, `insert into media_items(id,library_id,kind,title,sort_title,content_path,library_path_id,provider_ids,presentation) select $2,library_id,'movie',title,sort_title,'otra-calidad',library_path_id,provider_ids,presentation from media_items where id=$1`, e.pelisItem, clone)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(ctx, `insert into media_files(id,item_id,library_path_id,rel_path,size_bytes,mtime) select $3,$2,library_path_id,'otra-calidad.mkv',100,now() from media_items where id=$1`, e.pelisItem, clone, file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.pool.Exec(ctx, `insert into media_streams(file_id,stream_index,kind,codec,width,height) values($1,0,'video','hevc',1280,720)`, file)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items []store.ItemCard `json:"items"`
	}
	if c := e.call(t, "GET", "/api/v1/collections/123", e.token, nil, &out); c != 200 {
		t.Fatal(c)
	}
	found := map[uuid.UUID]string{}
	for _, it := range out.Items {
		found[it.ID] = it.Quality
	}
	if found[e.pelisItem] != "1080p" || found[clone] != "720p" {
		t.Fatal("resolution disappeared or moved", found)
	}
}
