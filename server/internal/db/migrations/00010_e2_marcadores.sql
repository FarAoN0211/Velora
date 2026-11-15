-- VELORA · E2: intro y créditos detectados comparando el audio de los episodios de una temporada. Reversible.
-- media_markers: tramo de un archivo (en ms). source «detected» lo escribe la detección; «manual» queda para editarlo a
-- mano y la detección nunca lo pisa. marker_analysis: qué archivos se han analizado ya, con su tamaño y fecha (si el
-- archivo cambia se vuelve a analizar) y la versión del método (si mejora, se repite).

-- +goose Up
create table media_markers (
  file_id uuid not null references media_files(id) on delete cascade,
  kind text not null check (kind in ('intro', 'credits')),
  start_ms bigint not null check (start_ms >= 0),
  end_ms bigint not null check (end_ms > start_ms),
  source text not null default 'detected' check (source in ('detected', 'manual')),
  created_at timestamptz not null default now(),
  primary key (file_id, kind)
);

create table marker_analysis (
  file_id uuid primary key references media_files(id) on delete cascade,
  size_bytes bigint not null,
  mtime timestamptz not null,
  version int not null,
  analyzed_at timestamptz not null default now()
);

-- +goose Down
drop table if exists marker_analysis;
drop table if exists media_markers;
