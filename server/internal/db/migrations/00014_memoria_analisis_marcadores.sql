-- +goose Up
create table marker_analysis_memory (
 rel_path text not null, size_bytes bigint not null, mtime_seconds bigint not null,
 duration_ms bigint not null, version int not null, analyzed_at timestamptz not null,
 primary key(rel_path,size_bytes,mtime_seconds,duration_ms,version)
);
create index marker_analysis_memory_version on marker_analysis_memory(size_bytes,mtime_seconds,duration_ms);
insert into marker_analysis_memory
 select f.rel_path,f.size_bytes,floor(extract(epoch from f.mtime))::bigint,f.duration_ms,a.version,a.analyzed_at
 from media_files f join marker_analysis a on a.file_id=f.id where f.duration_ms>0 on conflict do nothing;
-- +goose StatementBegin
create function remember_marker_analysis() returns trigger language plpgsql as $$
begin
 insert into marker_analysis_memory
 select f.rel_path,f.size_bytes,floor(extract(epoch from f.mtime))::bigint,f.duration_ms,new.version,new.analyzed_at
 from media_files f where f.id=new.file_id and f.duration_ms>0
 on conflict(rel_path,size_bytes,mtime_seconds,duration_ms,version) do update set analyzed_at=excluded.analyzed_at;
 return new;
end $$;
-- +goose StatementEnd
create trigger remember_marker_analysis after insert or update on marker_analysis for each row execute function remember_marker_analysis();
-- +goose StatementBegin
create function rebind_marker_analysis() returns trigger language plpgsql as $$
begin
 insert into marker_analysis(file_id,size_bytes,mtime,version,analyzed_at)
 select new.id,new.size_bytes,new.mtime,a.version,a.analyzed_at from marker_analysis_memory a
 where a.size_bytes=new.size_bytes and a.mtime_seconds=floor(extract(epoch from new.mtime))::bigint and a.duration_ms=new.duration_ms
 and (a.rel_path=new.rel_path or marker_episode_key(a.rel_path)=marker_episode_key(new.rel_path))
 order by a.version desc,a.analyzed_at desc limit 1
 on conflict(file_id) do nothing;
 return new;
end $$;
-- +goose StatementEnd
create trigger zz_rebind_marker_analysis after insert or update of rel_path,size_bytes,mtime,duration_ms on media_files
 for each row execute function rebind_marker_analysis();
-- +goose Down
drop trigger if exists zz_rebind_marker_analysis on media_files;
drop function if exists rebind_marker_analysis();
drop trigger if exists remember_marker_analysis on marker_analysis;
drop function if exists remember_marker_analysis();
drop table marker_analysis_memory;
