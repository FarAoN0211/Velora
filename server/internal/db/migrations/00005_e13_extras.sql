-- VELORA · E1.3 T0: tráileres y extras separados de los archivos de la película (PLAN_IDENTIFICACION_Y_TRAILERS.md, B3–B5).
-- Un extra nunca es una versión: no aparece en el selector de versión, no cuenta para la calidad ni la duración de la
-- tarjeta y no tiene progreso. item_id apunta a media_items, que ya admite series, temporadas y episodios (E2).
-- Solo cambia la base de datos de VELORA: ningún vídeo se toca. Reversible.

-- +goose Up
create table media_extras (
  id uuid primary key default gen_random_uuid(),
  item_id uuid not null references media_items(id) on delete cascade,
  library_path_id uuid not null references library_paths(id) on delete cascade,
  rel_path text not null,                          -- relativa a la ruta de biblioteca: se abre siempre con os.Root
  kind text not null check (kind in ('trailer','featurette','behindthescenes','deleted','interview','scene','short','extra')),
  title text,
  size_bytes bigint not null,
  mtime timestamptz not null,
  container text,
  duration_ms bigint,
  probe jsonb not null default '{}',
  probed_at timestamptz,
  missing_since timestamptz,
  origin_file_id uuid,                             -- id que tenía en media_files si se reclasificó (rastro y vuelta atrás)
  origin_streams jsonb,                            -- sus pistas analizadas, para devolverlas intactas al revertir
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (library_path_id, rel_path)
);
create index on media_extras (item_id, kind);
create trigger media_extras_touch before update on media_extras for each row execute function touch_updated_at();

-- Reclasificación estricta de lo ya escaneado: solo «trailer.*» (trailer, trailer2, trailer-1, trailer.f137) en la raíz
-- de la carpeta de SU película (la carpeta del archivo es la de la ficha). Si la película no tiene otro archivo (carpeta
-- con solo el tráiler), la ficha queda «pendiente»: oculta a los usuarios, con su identificación e imágenes, hasta que
-- aparezca la película. Una asociación insegura (tráiler fuera de la carpeta de su ficha) no se toca. Idempotente: lo
-- movido ya no está en media_files, y on conflict evita duplicados.
with moved as (
  insert into media_extras (item_id, library_path_id, rel_path, kind, size_bytes, mtime, container, duration_ms, probe,
                            probed_at, missing_since, origin_file_id, origin_streams, created_at)
  select f.item_id, f.library_path_id, f.rel_path, 'trailer', f.size_bytes, f.mtime, f.container, f.duration_ms, f.probe,
         f.probed_at, f.missing_since, f.id,
         (select coalesce(jsonb_agg(to_jsonb(st) - 'id' - 'file_id'), '[]') from media_streams st where st.file_id = f.id),
         f.created_at
  from media_files f join media_items i on i.id = f.item_id
  where f.rel_path ~* '^[^/]+/trailer([-_. ]?[0-9]+|\.f[0-9]+)?\.[a-z0-9]+$'
    and split_part(f.rel_path, '/', 1) = i.content_path
  on conflict (library_path_id, rel_path) do nothing
  returning origin_file_id
)
delete from media_files where id in (select origin_file_id from moved);

-- +goose Down
-- Lo reclasificado vuelve a media_files con su id original y sus pistas tal como estaban. Lo que el escáner registró
-- después como extra nuevo (sin origen) se descarta: la versión anterior lo ignoraba.
insert into media_files (id, item_id, library_path_id, rel_path, size_bytes, mtime, container, duration_ms, probe,
                         probed_at, missing_since, created_at)
select origin_file_id, item_id, library_path_id, rel_path, size_bytes, mtime, container, duration_ms, probe,
       case when origin_streams is null then null else probed_at end, missing_since, created_at
from media_extras where origin_file_id is not null
on conflict do nothing;
insert into media_streams (file_id, stream_index, kind, codec, profile, language, title, is_default, is_forced,
                           width, height, bit_depth, hdr_format, channels)
select x.origin_file_id, r.stream_index, r.kind, r.codec, r.profile, r.language, r.title, r.is_default, r.is_forced,
       r.width, r.height, r.bit_depth, r.hdr_format, r.channels
from media_extras x
cross join lateral jsonb_to_recordset(x.origin_streams) as r(stream_index int, kind text, codec text, profile text,
  language text, title text, is_default boolean, is_forced boolean, width int, height int, bit_depth smallint,
  hdr_format text, channels smallint)
where x.origin_file_id is not null and exists (select 1 from media_files f where f.id = x.origin_file_id)
on conflict do nothing;
drop table media_extras;
