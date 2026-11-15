package api

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestEpisodeSingleCastilianVersion(t *testing.T) {
	e := newSeriesEnv(t, "")
	e.scan(t, e.lib)
	ctx := context.Background()
	var episode, season, latin uuid.UUID
	if err := e.pool.QueryRow(ctx, `select i.id,i.parent_id,f.id from media_items i join media_files f on f.item_id=i.id where i.kind='episode' and i.title='Piloto' limit 1`).Scan(&episode, &season, &latin); err != nil {
		t.Fatal(err)
	}
	// Reproduce the real issue: the Latin file is larger and sorted first.
	if _, err := e.pool.Exec(ctx, `update media_streams set language='spa',title='Spanish [Latinoamericano]' where file_id=$1 and kind='audio'`, latin); err != nil {
		t.Fatal(err)
	}
	cast := uuid.New()
	if _, err := e.pool.Exec(ctx, `insert into media_files(id,item_id,library_path_id,rel_path,size_bytes,mtime,probe)
 select $2,item_id,library_path_id,rel_path||'.castellano.mkv',1,mtime,probe from media_files where id=$1`, latin, cast); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `insert into media_streams(file_id,stream_index,kind,codec,language,title) values($1,1,'audio','aac','spa','Castellano')`, cast); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		d := e.item(t, episode)
		if len(d.Files) != 1 || d.Files[0].ID != cast {
			t.Fatalf("expected only Castilian %s, got %+v", cast, d.Files)
		}
		sn := e.item(t, season)
		for _, ep := range sn.Episodes {
			if ep.ID == episode && (ep.FileID == nil || *ep.FileID != cast) {
				t.Fatalf("season chose wrong file: %+v", ep)
			}
		}
	}
	check()
	// Rediscovery/updating the obsolete file must not change the selected dub.
	if _, err := e.pool.Exec(ctx, `update media_files set size_bytes=size_bytes+1000000,mtime=now() where id=$1`, latin); err != nil {
		t.Fatal(err)
	}
	check()
	// If the preferred copy disappears, the remaining file must still play.
	if _, err := e.pool.Exec(ctx, `update media_files set missing_since=now() where id=$1`, cast); err != nil {
		t.Fatal(err)
	}
	if d := e.item(t, episode); len(d.Files) != 1 || d.Files[0].ID != latin {
		t.Fatalf("missing fallback: %+v", d.Files)
	}
	var count int
	if err := e.pool.QueryRow(ctx, `select count(*) from media_files where item_id=$1`, episode).Scan(&count); err != nil || count != 2 {
		t.Fatalf("physical records removed: %d %v", count, err)
	}
}
