package api

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestMarkerMemorySurvivesReidentifiedEpisodeAndRejectsDifferentEdition(t *testing.T) {
	e, file := newTracksEnv(t)
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	run := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	run(`update media_files set rel_path='Pistas/Pistas S01E01.mkv' where id=$1`, file)
	run(`create temp table old_file on commit drop as select * from media_files where id=$1`, file)
	run(`create temp table old_item on commit drop as select i.* from media_items i join old_file f on f.item_id=i.id`)
	run(`insert into media_markers(file_id,kind,start_ms,end_ms,source) values($1,'intro',1000,2000,'manual'),($1,'credits',3000,4000,'detected')`, file)
	run(`delete from media_items where id=(select item_id from old_file)`)
	var count int
	if err := tx.QueryRow(ctx, `select count(*) from media_marker_memory`).Scan(&count); err != nil || count != 2 {
		t.Fatal("memory lost on cascade", count, err)
	}
	item := uuid.New()
	replacement := uuid.New()
	run(`insert into media_items(id,library_id,library_path_id,kind,title,sort_title,content_path) select $1,library_id,library_path_id,kind,title,sort_title,content_path from old_item`, item)
	run(`insert into media_files select (jsonb_populate_record(null::media_files,to_jsonb(f)||jsonb_build_object('id',$1::uuid,'item_id',$2::uuid))).* from old_file f`, replacement, item)
	check := func(want int) {
		t.Helper()
		if err := tx.QueryRow(ctx, `select count(*) from media_markers where file_id=$1`, replacement).Scan(&count); err != nil || count != want {
			t.Fatalf("marks %d want %d: %v", count, want, err)
		}
	}
	check(2)
	run(`update media_files set rel_path='Pistas/Season 1/Nueva carpeta/Pistas S01E01.mkv' where id=$1`, replacement)
	check(2)
	run(`update media_files set rel_path='Pistas/Season 1/Pistas - 1x01.mkv' where id=$1`, replacement)
	check(2)
	run(`update media_files set size_bytes=size_bytes+1 where id=$1`, replacement)
	check(0)
	run(`update media_files set size_bytes=size_bytes-1 where id=$1`, replacement)
	check(2)
	run(`delete from media_markers where file_id=$1 and kind='intro'`, replacement)
	check(1)
	run(`update media_files set duration_ms=duration_ms where id=$1`, replacement)
	check(1)
}

func TestMarkerScanControlAndProgress(t *testing.T) {
	e, file := newTracksEnv(t)
	ctx := context.Background()
	_, err := e.pool.Exec(ctx, `update media_items set kind='episode' where id=(select item_id from media_files where id=$1)`, file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.pool.Exec(ctx, `update media_files set duration_ms=60000 where id=$1`, file); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/admin/markers"
	var status struct {
		Total, Done, Pending, Intros int
		Paused                       bool
	}
	if c := e.call(t, "GET", endpoint, e.token, nil, &status); c != 200 || status.Total != 1 || status.Pending != 1 {
		t.Fatal(c, status)
	}
	if c := e.call(t, "POST", endpoint, e.tokenB, nil, nil); c != 404 {
		t.Fatal("non-admin started scan", c)
	}
	if c := e.call(t, "POST", endpoint, e.token, nil, &status); c != 200 || status.Paused {
		t.Fatal(c, status)
	}
	if c := e.call(t, "DELETE", endpoint, e.token, nil, &status); c != 200 || !status.Paused {
		t.Fatal(c, status)
	}
	_, err = e.pool.Exec(ctx, `insert into marker_analysis(file_id,size_bytes,mtime,version) select id,size_bytes,mtime,1 from media_files where id=$1`, file)
	if err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "GET", endpoint, e.token, nil, &status); c != 200 || status.Done != 1 || status.Pending != 0 {
		t.Fatal(c, status)
	}
	if c := e.call(t, "POST", endpoint, e.token, nil, &status); c != 200 || status.Paused {
		t.Fatal(c, status)
	}
}
