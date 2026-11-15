-- +goose Up
create table collection_overrides (
 collection_id integer not null check(collection_id>0),
 library_id uuid not null default '00000000-0000-0000-0000-000000000000',
 updated_at timestamptz not null default now(),
 title text not null default '',
 overview text not null default '',
 poster_item_id uuid references media_items(id) on delete set null,
 primary key(collection_id,library_id)
);
-- +goose Down
drop table collection_overrides;
