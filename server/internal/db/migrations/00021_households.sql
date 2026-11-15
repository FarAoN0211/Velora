-- +goose Up
create table households (
 id uuid primary key default gen_random_uuid(),
 owner_id uuid not null unique references users(id),
 name text not null,
 created_at timestamptz not null default now()
);
alter table users add column household_id uuid references households(id);
alter table users add column household_owner boolean not null default false;
alter table users add column passwordless boolean not null default false;
create index on users(household_id);
alter table users add constraint household_role check (not household_owner or household_id is not null);
create table household_devices (
 id uuid primary key default gen_random_uuid(),
 household_id uuid not null references households(id),
 token_hash bytea not null unique,
 name text not null,
 expires_at timestamptz not null,
 revoked_at timestamptz
);
alter table sessions add column household_device_id uuid references household_devices(id);
alter table device_authorizations add column approved_user_id uuid references users(id);
alter table device_authorizations add column approved_pin_hash text;
-- +goose Down
alter table device_authorizations drop column approved_pin_hash, drop column approved_user_id;
alter table sessions drop column household_device_id;
drop table household_devices;
alter table users drop constraint household_role;
alter table users drop column passwordless, drop column household_owner, drop column household_id;
drop table households;
