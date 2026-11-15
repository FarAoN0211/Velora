-- VELORA · E1 (biblioteca de películas, Direct Play, progreso) + bloqueo por intentos de login (adelantado de E3).
-- Subconjunto de replica/schema.sql. Reversible.

-- +goose Up
create table login_attempts (
  id bigint generated always as identity primary key,
  username citext,
  ip inet not null,
  kind text not null check (kind in ('password','pin','totp','device_code','setup')),
  success boolean not null,
  created_at timestamptz not null default now()
);
create index on login_attempts (username, created_at desc);
create index on login_attempts (ip, created_at desc);

create table libraries (
  id uuid primary key default gen_random_uuid(),
  name text not null unique,
  kind text not null check (kind in ('movies','tv','music','photos','other','audiobooks','books')),
  realtime_monitor boolean not null default true,
  scan_interval_minutes int not null default 360 check (scan_interval_minutes > 0),
  metadata_language text not null default 'es-ES',
  options jsonb not null default '{}',
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger libraries_touch before update on libraries for each row execute function touch_updated_at();

create table library_paths (
  id uuid primary key default gen_random_uuid(),
  library_id uuid not null references libraries(id) on delete cascade,
  path text not null unique,
  kind text not null default 'local' check (kind in ('local','network','remote_fuse')),
  status text not null default 'pending_sync' check (status in ('available','unavailable','pending_sync')),
  status_changed_at timestamptz not null default now(),
  last_check_at timestamptz,
  last_scan_at timestamptz,
  next_retry_at timestamptz,
  probe_daily_limit int check (probe_daily_limit is null or probe_daily_limit > 0),
  heavy_jobs_enabled boolean not null default true,
  created_at timestamptz not null default now()
);
create index on library_paths (library_id);

create table media_items (
  id uuid primary key default gen_random_uuid(),
  library_id uuid not null references libraries(id) on delete cascade,
  parent_id uuid references media_items(id) on delete cascade,
  kind text not null check (kind in ('movie','series','season','episode','artist','album','track',
                                      'photo','photo_album','video','audiobook','book')),
  title text not null,
  sort_title text not null,
  original_title text,
  year smallint,
  overview text,
  tagline text,
  runtime_ms bigint,
  official_rating text,
  rating_age smallint,
  community_rating numeric(3,1),
  content_path text not null,                     -- carpeta relativa a la ruta de biblioteca (clave del escáner)
  library_path_id uuid not null references library_paths(id) on delete cascade,
  locked boolean not null default false,
  locked_fields text[] not null default '{}',
  provider_ids jsonb not null default '{}',
  metadata_refreshed_at timestamptz,
  search_text text generated always as (lower(f_unaccent(coalesce(title,'') || ' ' || coalesce(original_title,'')))) stored,
  added_at timestamptz not null default now(),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (library_path_id, content_path)
);
create index on media_items (library_id, kind, sort_title);
create index on media_items (library_id, added_at desc);
create index on media_items using gin (search_text gin_trgm_ops);
create trigger media_items_touch before update on media_items for each row execute function touch_updated_at();

create table media_files (
  id uuid primary key default gen_random_uuid(),
  item_id uuid not null references media_items(id) on delete cascade,
  library_path_id uuid not null references library_paths(id) on delete cascade,
  rel_path text not null,                          -- relativa a la ruta de biblioteca: se abre siempre con os.Root
  size_bytes bigint not null,
  mtime timestamptz not null,
  container text,
  duration_ms bigint,
  bitrate_kbps int,
  version_label text,
  part_index smallint not null default 1,
  probe jsonb not null default '{}',
  probed_at timestamptz,
  missing_since timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (library_path_id, rel_path)
);
create index on media_files (item_id, part_index);
create trigger media_files_touch before update on media_files for each row execute function touch_updated_at();

create table media_streams (
  id uuid primary key default gen_random_uuid(),
  file_id uuid not null references media_files(id) on delete cascade,
  stream_index int not null,
  kind text not null check (kind in ('video','audio','subtitle','attachment')),
  codec text not null,
  profile text,
  language text,
  title text,
  is_default boolean not null default false,
  is_forced boolean not null default false,
  width int, height int, bit_depth smallint,
  hdr_format text check (hdr_format in ('sdr','hdr10','hdr10plus','hlg','dovi')),
  channels smallint,
  unique (file_id, stream_index)
);
create index on media_streams (file_id, kind);

create table item_images (
  id uuid primary key default gen_random_uuid(),
  item_id uuid not null references media_items(id) on delete cascade,
  kind text not null check (kind in ('poster','backdrop','logo','thumb','banner','disc','art')),
  source text not null check (source in ('local','tmdb','fanart','embedded','user','generated')),
  path text not null,                              -- local: relativa a la ruta de biblioteca; tmdb: relativa a /config/metadata
  library_path_id uuid references library_paths(id) on delete cascade,
  created_at timestamptz not null default now(),
  unique (item_id, kind, source)
);
create index on item_images (item_id, kind);

create table metadata_cache (
  provider text not null,
  cache_key text not null,
  payload jsonb not null,
  fetched_at timestamptz not null default now(),
  expires_at timestamptz not null,
  primary key (provider, cache_key),
  constraint tmdb_max_cache check (provider <> 'tmdb' or expires_at <= fetched_at + interval '180 days')
);

create table user_item_data (
  user_id uuid not null references users(id) on delete cascade,
  item_id uuid not null references media_items(id) on delete cascade,
  position_ms bigint not null default 0 check (position_ms >= 0),
  played boolean not null default false,
  play_count int not null default 0,
  favorite boolean not null default false,
  last_played_at timestamptz,
  client_updated_at timestamptz,
  continue_hidden_at timestamptz,
  audio_language text,
  subtitle_language text,
  subtitle_mode text,
  primary key (user_id, item_id)
);
create index on user_item_data (item_id);
create index on user_item_data (user_id, last_played_at desc) where position_ms > 0 and not played and continue_hidden_at is null;

insert into scheduled_tasks (id, cron) values ('library_scan_all', '15 */6 * * *'), ('login_attempts_purge', '30 3 * * *')
  on conflict (id) do nothing;

-- +goose Down
delete from scheduled_tasks where id in ('library_scan_all', 'login_attempts_purge');
drop table if exists user_item_data;
drop table if exists metadata_cache;
drop table if exists item_images;
drop table if exists media_streams;
drop table if exists media_files;
drop table if exists media_items;
drop table if exists library_paths;
drop table if exists libraries;
drop table if exists login_attempts;
