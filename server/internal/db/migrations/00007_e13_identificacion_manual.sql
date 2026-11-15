-- VELORA · E1.3 I2: identificación manual (replica/PLAN_IDENTIFICACION_Y_TRAILERS.md). Reversible.
-- match_source: «manual» cuando un administrador eligió la película en TMDB; el escaneo y el nombre de la carpeta ya no
-- cambian su identificación (no sustituye a los candados de cada campo).
-- metadata_snapshots: lo que había antes de corregir o restablecer, para «Deshacer» (se guardan las 5 últimas por ficha).

-- +goose Up
alter table media_items add column match_source text not null default 'auto' check (match_source in ('auto', 'manual'));

create table metadata_snapshots (
  id bigint generated always as identity primary key,
  item_id uuid not null references media_items(id) on delete cascade,
  created_at timestamptz not null default now(),
  created_by uuid references users(id) on delete set null,
  reason text not null check (reason in ('identify', 'reset')),
  data jsonb not null
);
create index on metadata_snapshots (item_id, id desc);

-- +goose Down
drop table if exists metadata_snapshots;
alter table media_items drop column if exists match_source;
