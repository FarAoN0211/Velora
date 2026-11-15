-- +goose Up
create index media_items_movie_tmdb_progress on media_items ((provider_ids->>'tmdb')) where kind='movie';
-- Read the most recent progress, not the furthest point: a user can rewind.
-- Versions without a reliable identity remain independent. No history is rewritten.
-- +goose StatementBegin
create function effective_item_data(viewer_id uuid,target_id uuid) returns setof user_item_data
language sql stable rows 1 as $$
 select d.* from media_items target
 join media_items source on source.id=target.id or
  (target.kind='movie' and source.kind='movie' and target.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$'
   and source.provider_ids->>'tmdb'=target.provider_ids->>'tmdb')
 join user_item_data d on d.item_id=source.id and d.user_id=viewer_id
 where target.id=target_id
 order by coalesce(d.client_updated_at,d.last_played_at,'-infinity'::timestamptz) desc,
  d.last_played_at desc nulls last,(d.item_id=target_id) desc,d.item_id
 limit 1
$$;
-- +goose StatementEnd
-- +goose Down
drop function effective_item_data(uuid,uuid);
drop index media_items_movie_tmdb_progress;
