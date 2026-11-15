-- VELORA · Detección de intros: huellas de audio ya calculadas de los episodios de una temporada en curso. Reversible.
-- Leer el audio de un episodio remoto (Teldrive) lleva minutos; sin guardarlo, cada interrupción (alguien reproduce,
-- se agota la tanda, se reinicia el worker) obligaba a empezar la temporada de cero y las temporadas largas no
-- terminaban nunca. Es una caché: se borra al guardar el resultado del archivo y no entra en las decisiones.

-- +goose Up
create table marker_fingerprints (
  file_id uuid primary key references media_files(id) on delete cascade,
  size_bytes bigint not null,
  mtime timestamptz not null,
  version int not null,
  intro bytea not null,
  credits bytea not null,
  created_at timestamptz not null default now()
);

-- +goose Down
drop table if exists marker_fingerprints;
