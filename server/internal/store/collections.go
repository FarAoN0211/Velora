package store

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strconv"
)

type MovieCollection struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type CollectionCard struct {
	MovieCollection
	Count        int       `json:"count"`
	PosterID     uuid.UUID `json:"poster_id"`
	Overview     string    `json:"overview"`
	PosterRev    string    `json:"poster_rev"`
	Library4K    bool      `json:"library_4k"`
	CustomPoster bool      `json:"poster_custom"`
}

// Count distinct TMDB films, so two versions of one movie do not create a collection.
// The threshold is evaluated after permissions and file availability, never globally.
func (s *Store) Collections(ctx context.Context, v Viewer, library ...uuid.UUID) ([]CollectionCard, error) {
	scope := uuid.Nil
	if len(library) > 0 {
		scope = library[0]
	}
	rows, err := s.Pool.Query(ctx, `with cards as (select (i.presentation->'belongs_to_collection'->>'id')::int as id,
 max(i.presentation->'belongs_to_collection'->>'name') as name,count(distinct coalesce(nullif(i.provider_ids->>'tmdb',''),i.id::text)) as count,
 (array_agg(i.id order by i.year nulls last,i.sort_title,i.id))[1] as poster_id, bool_and(exists(select 1 from libraries cl where cl.id=i.library_id and cl.name ~* '(4[[:space:]]*k|uhd)')) as library_4k
 from media_items i where i.kind='movie' and (i.presentation->'belongs_to_collection'->>'id') ~ '^[1-9][0-9]{0,8}$'
 and ($3::uuid='00000000-0000-0000-0000-000000000000' or i.library_id=$3) and `+present("i")+` and `+visible("i", 1, 2)+`
 group by i.presentation->'belongs_to_collection'->>'id'
 having count(distinct coalesce(nullif(i.provider_ids->>'tmdb',''),i.id::text))>=2
  ) select c.*, coalesce(o.title,''),coalesce(o.overview,''),
 case when exists(select 1 from media_items i where i.id=o.poster_item_id and i.presentation->'belongs_to_collection'->>'id'=c.id::text and ($3::uuid='00000000-0000-0000-0000-000000000000' or i.library_id=$3) and `+present("i")+` and `+visible("i", 1, 2)+`) then o.poster_item_id else c.poster_id end, o.poster_item_id is not null,coalesce(o.updated_at::text,'auto')
 from cards c left join collection_overrides o on o.collection_id=c.id and o.library_id=$3 order by coalesce(nullif(o.title,''),c.name)`, v.UserID, v.Admin, scope)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (CollectionCard, error) {
		var c CollectionCard
		var title string
		var originalPoster uuid.UUID
		err := r.Scan(&c.ID, &c.Name, &c.Count, &originalPoster, &c.Library4K, &title, &c.Overview, &c.PosterID, &c.CustomPoster, &c.PosterRev)
		if title != "" {
			c.Name = title
		}
		return c, err
	})
}
func (s *Store) CollectionMovies(ctx context.Context, v Viewer, id int, library ...uuid.UUID) ([]ItemCard, error) {
	scope := uuid.Nil
	if len(library) > 0 {
		scope = library[0]
	}
	rows, err := s.Pool.Query(ctx, `select * from (select distinct on (i.library_id,coalesce(nullif(i.provider_ids->>'tmdb',''),i.id::text), `+itemQualityRank+`) `+cardCols+` from media_items i left join lateral effective_item_data($1,i.id) d on true
 where ($4::uuid='00000000-0000-0000-0000-000000000000' or i.library_id=$4) and i.kind='movie' and i.presentation->'belongs_to_collection'->>'id'=$3 and `+present("i")+` and `+visible("i", 1, 2)+`
 order by i.library_id,coalesce(nullif(i.provider_ids->>'tmdb',''),i.id::text), `+itemQualityRank+` desc, i.id) films order by 4 nulls last,3,1`, v.UserID, v.Admin, strconv.Itoa(id), scope)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}
func (s *Store) MoviesMissingCollection(ctx context.Context) ([]PendingMetadata, error) {
	rows, err := s.Pool.Query(ctx, `select i.id,i.kind,i.title,i.year,i.provider_ids from media_items i join libraries l on l.id=i.library_id where i.kind='movie' and l.retired_at is null and coalesce(i.provider_ids->>'tmdb','')<>'' and coalesce(i.presentation->>'collection_tmdb_checked','false')<>'true' order by i.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingMetadata, error) {
		var p PendingMetadata
		err := r.Scan(&p.ID, &p.Kind, &p.Title, &p.Year, &p.ProviderIDs)
		return p, err
	})
}
func (s *Store) SaveMovieCollection(ctx context.Context, it PendingMetadata, raw json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `update media_items set presentation=jsonb_set(coalesce(presentation,'{}'::jsonb),'{belongs_to_collection}',$3::jsonb)||'{"collection_tmdb_checked":true}'::jsonb where id=$1 and kind='movie' and provider_ids->>'tmdb'=$2`, it.ID, it.ProviderIDs["tmdb"], raw)
	return err
}

func (s *Store) SaveCollectionOverride(ctx context.Context, id int, scope uuid.UUID, title, overview string, poster *uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `insert into collection_overrides(collection_id,library_id,title,overview,poster_item_id) values($1,$2,$3,$4,$5) on conflict(collection_id,library_id) do update set title=excluded.title,overview=excluded.overview,poster_item_id=excluded.poster_item_id,updated_at=now()`, id, scope, title, overview, poster)
	return err
}
