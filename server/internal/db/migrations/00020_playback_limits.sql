-- +goose Up
create table playback_leases (
 session_id uuid not null references sessions(id) on delete cascade,
 player_id text not null check(length(player_id) between 1 and 80),
 file_id uuid not null,
 ip inet not null,
 expires_at timestamptz not null,
 primary key(session_id,player_id)
);
create index on playback_leases(expires_at);
create index on playback_leases(ip);

-- +goose Down
drop table playback_leases;
