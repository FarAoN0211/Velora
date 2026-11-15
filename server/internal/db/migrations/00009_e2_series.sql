-- VELORA · E2 S1: series, temporadas y episodios (replica/design/screens.md S06). Reversible.
-- La serie es la carpeta de primer nivel (content_path = «Serie»); la temporada y el episodio son fichas hijas con
-- claves sintéticas que nunca coinciden con una carpeta real: «Serie/#S01» y «Serie/#S01E02» (un nombre de carpeta de
-- primer nivel no puede contener «/»). Así dos versiones del mismo episodio comparten ficha y progreso.
-- series_id: la serie a la que pertenece una temporada o un episodio (parent_id es su padre directo: la temporada para
-- un episodio). index_number: nº de temporada o de episodio; index_number_end: último episodio de un multiepisodio.

-- +goose Up
alter table media_items add column series_id uuid references media_items(id) on delete cascade;
alter table media_items add column index_number int;
alter table media_items add column index_number_end int;
create index on media_items (series_id) where series_id is not null;
create index on media_items (parent_id, index_number) where parent_id is not null;

-- +goose Down
drop index if exists media_items_parent_id_index_number_idx;
drop index if exists media_items_series_id_idx;
alter table media_items drop column if exists index_number_end;
alter table media_items drop column if exists index_number;
alter table media_items drop column if exists series_id;
