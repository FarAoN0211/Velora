-- VELORA · E0 (cimientos). Subconjunto de replica/schema.sql: usuarios, sesiones, dispositivos, ajustes,
-- auditoría, cola de trabajos y tareas programadas. Reversible.

-- +goose Up
create extension if not exists pg_trgm;
create extension if not exists unaccent;
create extension if not exists citext;

-- +goose StatementBegin
create or replace function f_unaccent(text) returns text
  language sql immutable parallel safe strict as $$ select public.unaccent('public.unaccent'::regdictionary, $1) $$;
-- +goose StatementEnd

-- +goose StatementBegin
create or replace function touch_updated_at() returns trigger language plpgsql as $$
begin new.updated_at := now(); return new; end $$;
-- +goose StatementEnd

create table users (
  id uuid primary key default gen_random_uuid(),
  username citext not null unique,
  display_name text not null,
  password_hash text,
  pin_hash text,
  auth_source text not null default 'local' check (auth_source in ('local','ldap')),
  is_admin boolean not null default false,
  is_hidden boolean not null default false,
  is_disabled boolean not null default false,
  lock_profile boolean not null default false,
  can_play boolean not null default true,
  can_download boolean not null default false,
  can_delete boolean not null default false,
  can_convert boolean not null default false,
  can_remote boolean not null default true,
  can_manage_collections boolean not null default false,
  max_concurrent_streams int check (max_concurrent_streams is null or max_concurrent_streams > 0),
  remote_bitrate_limit_kbps int check (remote_bitrate_limit_kbps is null or remote_bitrate_limit_kbps > 0),
  max_rating_age smallint check (max_rating_age between 0 and 21),
  unrated_policy text not null default 'allow' check (unrated_policy in ('allow','block')),
  blocked_tags text[] not null default '{}',
  allowed_tags text[] not null default '{}',
  device_restricted boolean not null default false,
  preferences jsonb not null default '{}',
  last_login_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create trigger users_touch before update on users for each row execute function touch_updated_at();

create table devices (
  id uuid primary key default gen_random_uuid(),
  user_id uuid references users(id) on delete cascade,
  client_id text not null unique,
  name text not null,
  client_kind text not null check (client_kind in ('web','android','android_tv','tizen','webos','dlna','kodi','other')),
  app_version text,
  capabilities jsonb not null default '{}',
  last_ip inet,
  last_seen_at timestamptz,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create index on devices (user_id);
create trigger devices_touch before update on devices for each row execute function touch_updated_at();

create table sessions (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  device_id uuid references devices(id) on delete set null,
  token_hash bytea not null unique,
  is_remote boolean not null default false,
  mfa_passed boolean not null default false,
  created_at timestamptz not null default now(),
  last_used_at timestamptz not null default now(),
  expires_at timestamptz not null,
  revoked_at timestamptz
);
create index on sessions (user_id);
create index on sessions (device_id);
create index on sessions (expires_at);

create table audit_log (
  id bigint generated always as identity primary key,
  actor_user_id uuid references users(id) on delete set null,
  action text not null,
  target text,
  details jsonb not null default '{}',
  ip inet,
  created_at timestamptz not null default now()
);
create index on audit_log (created_at desc);
create index on audit_log (actor_user_id);

create table settings (
  key text primary key,
  value jsonb not null,
  updated_at timestamptz not null default now()
);

create table jobs (
  id bigint generated always as identity primary key,
  kind text not null,
  payload jsonb not null default '{}',
  dedupe_key text,
  priority smallint not null default 100,
  state text not null default 'queued' check (state in ('queued','running','done','failed','cancelled')),
  attempts smallint not null default 0,
  max_attempts smallint not null default 3,
  run_after timestamptz not null default now(),
  locked_by text,
  locked_at timestamptz,
  last_error text,
  result jsonb,
  created_at timestamptz not null default now(),
  finished_at timestamptz
);
create index on jobs (state, priority, run_after) where state = 'queued';
create unique index jobs_dedupe_live on jobs (dedupe_key) where state in ('queued','running') and dedupe_key is not null;
create index on jobs (finished_at) where state in ('done','failed','cancelled');

create table scheduled_tasks (
  id text primary key,
  cron text not null,
  enabled boolean not null default true,
  window_start smallint check (window_start between 0 and 23),
  window_end smallint check (window_end between 0 and 23),
  last_run_at timestamptz,
  last_status text,
  updated_at timestamptz not null default now()
);

insert into scheduled_tasks (id, cron) values ('backup', '0 4 * * *');

-- +goose Down
drop table if exists scheduled_tasks;
drop table if exists jobs;
drop table if exists settings;
drop table if exists audit_log;
drop table if exists sessions;
drop table if exists devices;
drop table if exists users;
drop function if exists touch_updated_at();
drop function if exists f_unaccent(text);
-- Las extensiones se mantienen: pueden estar en uso por otras migraciones futuras y no contienen datos.
