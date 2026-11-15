-- VELORA · Hogares creados por el propietario, papelera, PIN con recuperación y bloqueo de perfiles (fase 1 de
-- replica/PLAN_HOGARES_Y_PIN.md). Solo estructura: el comportamiento llega en las fases 2 a 4. Reversible.
--
-- users.can_create_household: lo concede el administrador; sin él no se puede crear ni ampliar un hogar.
-- users.deleted_at: papelera de miembros (30 días). Solo perfiles familiares, nunca el propietario.
-- users.pin_lock_minutes: cuándo se vuelve a pedir el PIN (0 = siempre al abrir o seleccionar; 15, 60 o 240 min).
-- users.pin_recovery_hash: hash del código de recuperación del PIN (el código solo se muestra una vez).
-- sessions.profile_unlocked_until / last_activity_at: desbloqueo del perfil por dispositivo, aplicado por el servidor.
-- profile_seals + sealed_item_data: historial sellado tras un restablecimiento hecho por alguien que no es el titular.
--   Almacén aparte que ninguna consulta de Velora lee; solo el código de recuperación anterior lo devuelve.

-- +goose Up
alter table users add column can_create_household boolean not null default false;
alter table users add column deleted_at timestamptz;
alter table users add column pin_lock_minutes int not null default 60 check (pin_lock_minutes in (0, 15, 60, 240));
alter table users add column pin_recovery_hash bytea;
alter table users add constraint deleted_only_members check (deleted_at is null or (household_id is not null and not household_owner));
create index users_deleted_at_idx on users (deleted_at) where deleted_at is not null;
-- Quien ya es responsable de un hogar (creado por el administrador con el flujo anterior) conserva el permiso.
update users set can_create_household = true where household_owner;

alter table sessions add column profile_unlocked_until timestamptz;
alter table sessions add column last_activity_at timestamptz;

create table profile_seals (
  id uuid primary key default gen_random_uuid(),
  user_id uuid not null references users(id) on delete cascade,
  recovery_hash bytea,
  preferences jsonb not null default '{}',
  reason text not null check (reason in ('owner_reset', 'admin_password_reset', 'admin_pin_reset')),
  sealed_by uuid references users(id) on delete set null,
  sealed_at timestamptz not null default now(),
  expires_at timestamptz not null,
  check (expires_at > sealed_at)
);
create index profile_seals_user_idx on profile_seals (user_id);
create index profile_seals_expires_idx on profile_seals (expires_at);

create table sealed_item_data (
  seal_id uuid not null references profile_seals(id) on delete cascade,
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
  primary key (seal_id, item_id)
);

-- +goose Down
drop table if exists sealed_item_data;
drop table if exists profile_seals;
alter table sessions drop column if exists last_activity_at;
alter table sessions drop column if exists profile_unlocked_until;
drop index if exists users_deleted_at_idx;
alter table users drop constraint if exists deleted_only_members;
alter table users drop column if exists pin_recovery_hash;
alter table users drop column if exists pin_lock_minutes;
alter table users drop column if exists deleted_at;
alter table users drop column if exists can_create_household;
