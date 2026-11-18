package api

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
	"velora/internal/metadata"
	"velora/internal/store"
)

func TestSeriesIdentityFromEpisodeNames(t *testing.T) {
	for _, scenario := range []string{"clear", "ambiguous", "conflicting", "single_episode_versions", "known_identity"} {
		t.Run(scenario, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/3/configuration", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
			mux.HandleFunc("/3/search/tv", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("query") != "Título canónico" {
					fmt.Fprint(w, `{"results":[]}`)
					return
				}
				if scenario == "ambiguous" {
					fmt.Fprint(w, `{"results":[{"id":900,"name":"Título canónico"},{"id":901,"name":"Título canónico"}]}`)
					return
				}
				fmt.Fprint(w, `{"results":[{"id":900,"name":"Título canónico","original_name":"Título canónico","first_air_date":"2025-01-01"}]}`)
			})
			for _, id := range []int{900, 777} {
				mux.HandleFunc(fmt.Sprintf("/3/tv/%d", id), func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprintf(w, `{"id":%d,"name":"Título canónico","original_name":"Título canónico","first_air_date":"2025-01-01","overview":"Ficha de prueba","genres":[]}`, id)
				})
			}
			ts := httptest.NewServer(mux)
			defer ts.Close()
			e := newSeriesEnv(t, ts.URL)
			e.scan(t, e.lib)
			ctx := context.Background()
			var id uuid.UUID
			if err := e.pool.QueryRow(ctx, `select id from media_items where library_id=$1 and kind='series' and content_path='Serie A'`, e.lib).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if _, err := e.pool.Exec(ctx, `update media_items set title='English alias',sort_title='english alias' where id=$1`, id); err != nil {
				t.Fatal(err)
			}
			// Only catalogue names change: no matching media files exist on disk. The
			// resolver must not open them or rely on another application.
			if _, err := e.pool.Exec(ctx, `update media_files f set rel_path='English Folder/Season 1/Título canónico S01E'||lpad(e.index_number::text,2,'0')||'-'||e.id::text||'.mkv' from media_items e where f.item_id=e.id and e.series_id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if scenario == "conflicting" {
				_, err := e.pool.Exec(ctx, `update media_files set rel_path=replace(rel_path,'Título canónico','Otra serie') where id=(select f.id from media_files f join media_items e on e.id=f.item_id where e.series_id=$1 limit 1)`, id)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "single_episode_versions" {
				var episode uuid.UUID
				if err := e.pool.QueryRow(ctx, `select id from media_items where series_id=$1 and kind='episode' limit 1`, id).Scan(&episode); err != nil {
					t.Fatal(err)
				}
				_, err := e.pool.Exec(ctx, `delete from media_files where item_id in (select id from media_items where series_id=$1 and kind='episode') and item_id<>$2`, id, episode)
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.pool.Exec(ctx, `insert into media_files(item_id,library_path_id,rel_path,size_bytes,mtime) select item_id,library_path_id,'English Folder/Título canónico S01E01 4K.mkv',size_bytes,mtime from media_files where item_id=$1 limit 1`, episode)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "known_identity" {
				if _, err := e.pool.Exec(ctx, `update media_items set provider_ids='{"tmdb":"777"}' where id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			st := &store.Store{Pool: e.pool}
			tm := metadata.New(ts.URL, ts.URL+"/img", e.configDir, st)
			if err := tm.SaveKey(ctx, "test-key"); err != nil {
				t.Fatal(err)
			}
			it, _, err := st.ItemForRefresh(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := tm.Refresh(ctx, it, false)
			if err != nil {
				t.Fatal(err)
			}
			if applied != (scenario == "clear" || scenario == "known_identity") {
				t.Fatalf("applied=%v", applied)
			}
			saved, _, err := st.ItemForRefresh(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if scenario == "clear" {
				want = "900"
			}
			if scenario == "known_identity" {
				want = "777"
			}
			if saved.ProviderIDs["tmdb"] != want {
				t.Fatalf("identidad alterada: %+v", saved)
			}
			var response struct {
				SeriesTitles []struct {
					Title string `json:"title"`
				} `json:"series_titles"`
			}
			if code := e.call(t, "GET", "/api/v1/admin/items/"+id.String()+"/metadata", e.token, nil, &response); code != 200 || len(response.SeriesTitles) == 0 {
				t.Fatalf("sin sugerencias: %d %+v", code, response)
			}
		})
	}
}
