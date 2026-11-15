-- +goose Up
create table device_authorizations (
 id uuid primary key default gen_random_uuid(),
 code_hash bytea not null unique,
 device_hash bytea not null unique,
 device_name text not null,
 ip text not null,
 created_at timestamptz not null default now(),
 expires_at timestamptz not null,
 approved_session uuid references sessions(id) on delete cascade,
 consumed_at timestamptz,
 last_poll_at timestamptz
);
create index on device_authorizations(expires_at);
create table recovery_codes (
 user_id uuid not null references users(id) on delete cascade,
 code_hash bytea not null,
 expires_at timestamptz not null,
 primary key(user_id,code_hash)
);
-- +goose Down
drop table recovery_codes;
drop table device_authorizations;
