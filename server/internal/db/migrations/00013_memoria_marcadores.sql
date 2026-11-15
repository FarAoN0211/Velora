-- +goose Up
-- +goose StatementBegin
create function marker_episode_key(p text) returns text language plpgsql immutable as $$
declare m text[];
begin
 m:=regexp_match(lower(p),'s([0-9]{1,2})[ ._-]?e([0-9]{1,3})');
 if m is null then m:=regexp_match(lower(p),'(?:^|[^0-9])([0-9]{1,2})x([0-9]{2,3})(?:[^0-9]|$)'); end if;
 if m is null then return null; end if;
 return (m[1]::int)::text||':'||(m[2]::int)::text;
end $$;
-- +goose StatementEnd
-- Markers survive catalogue IDs and library-source replacement. Identity never uses series/episode UUIDs.
create table media_marker_memory (
 rel_path text not null,
 size_bytes bigint not null,
 mtime_seconds bigint not null,
 duration_ms bigint not null,
 kind text not null check(kind in ('intro','credits')),
 start_ms bigint not null check(start_ms>=0),
 end_ms bigint not null check(end_ms>start_ms),
 source text not null check(source in ('detected','manual')),
 created_at timestamptz not null,
 primary key(rel_path,size_bytes,mtime_seconds,duration_ms,kind)
);
create index media_marker_memory_version on media_marker_memory(size_bytes,mtime_seconds,duration_ms);
insert into media_marker_memory
 select f.rel_path,f.size_bytes,floor(extract(epoch from f.mtime))::bigint,f.duration_ms,m.kind,m.start_ms,m.end_ms,m.source,m.created_at
 from media_files f join media_markers m on m.file_id=f.id where f.duration_ms>0
 on conflict do nothing;

-- +goose StatementBegin
create function remember_media_marker() returns trigger language plpgsql as $$
begin
 insert into media_marker_memory
 select f.rel_path,f.size_bytes,floor(extract(epoch from f.mtime))::bigint,f.duration_ms,new.kind,new.start_ms,new.end_ms,new.source,new.created_at
 from media_files f where f.id=new.file_id and f.duration_ms>0
 on conflict(rel_path,size_bytes,mtime_seconds,duration_ms,kind) do update
 set start_ms=excluded.start_ms,end_ms=excluded.end_ms,source=excluded.source,created_at=excluded.created_at
 where media_marker_memory.source<>'manual' or excluded.source='manual';
 return new;
end $$;
-- +goose StatementEnd
create trigger remember_media_marker after insert or update on media_markers for each row execute function remember_media_marker();

-- An explicit marker deletion removes its memory. Cascades and version changes retain the old version's memory.
-- +goose StatementBegin
create function forget_media_marker() returns trigger language plpgsql as $$
begin
 if pg_trigger_depth()=1 then
 delete from media_marker_memory a using media_files f
 where f.id=old.file_id and (a.rel_path=f.rel_path or marker_episode_key(a.rel_path)=marker_episode_key(f.rel_path)) and a.size_bytes=f.size_bytes
 and a.mtime_seconds=floor(extract(epoch from f.mtime))::bigint and a.duration_ms=f.duration_ms and a.kind=old.kind and a.source=old.source;
 end if;
 return old;
end $$;
-- +goose StatementEnd
create trigger forget_media_marker after delete on media_markers for each row execute function forget_media_marker();

-- +goose StatementBegin
create function rebind_media_markers() returns trigger language plpgsql as $$
begin
 if tg_op='UPDATE' then
 if new.size_bytes<>old.size_bytes or floor(extract(epoch from new.mtime))<>floor(extract(epoch from old.mtime))
 or (old.duration_ms>0 and new.duration_ms is distinct from old.duration_ms) then
 delete from media_markers where file_id=new.id;
 delete from marker_analysis where file_id=new.id;
 end if;
 end if;
 insert into media_markers(file_id,kind,start_ms,end_ms,source,created_at)
 select distinct on(a.kind) new.id,a.kind,a.start_ms,a.end_ms,a.source,a.created_at from media_marker_memory a
 where (a.rel_path=new.rel_path or (marker_episode_key(a.rel_path) is not null and marker_episode_key(a.rel_path)=marker_episode_key(new.rel_path))) and a.size_bytes=new.size_bytes
 and a.mtime_seconds=floor(extract(epoch from new.mtime))::bigint and a.duration_ms=new.duration_ms
 and a.end_ms<=new.duration_ms
 order by a.kind,(a.source='manual') desc,a.created_at desc
 on conflict(file_id,kind) do nothing;
 return new;
end $$;
-- +goose StatementEnd
create trigger rebind_media_markers after insert or update of rel_path,size_bytes,mtime,duration_ms on media_files
 for each row execute function rebind_media_markers();

-- +goose Down
drop trigger if exists rebind_media_markers on media_files;
drop function if exists rebind_media_markers();
drop trigger if exists forget_media_marker on media_markers;
drop function if exists forget_media_marker();
drop trigger if exists remember_media_marker on media_markers;
drop function if exists remember_media_marker();
drop table media_marker_memory;

drop function if exists marker_episode_key(text);
