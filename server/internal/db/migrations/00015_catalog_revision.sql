-- +goose Up
create table catalog_revision (singleton boolean primary key default true check(singleton), revision bigint not null default 0);
insert into catalog_revision values(true,0);
-- +goose StatementBegin
create function bump_catalog_revision() returns trigger language plpgsql as $$
begin
 update catalog_revision set revision=revision+1 where singleton;
 return null;
end $$;
-- +goose StatementEnd
create trigger catalog_items_changed after insert or update or delete on media_items for each statement execute function bump_catalog_revision();
create trigger catalog_files_changed after insert or update or delete on media_files for each statement execute function bump_catalog_revision();
create trigger catalog_images_changed after insert or update or delete on item_images for each statement execute function bump_catalog_revision();
create trigger catalog_libraries_changed after insert or update or delete on libraries for each statement execute function bump_catalog_revision();
-- +goose Down
drop trigger catalog_libraries_changed on libraries;
drop trigger catalog_images_changed on item_images;
drop trigger catalog_files_changed on media_files;
drop trigger catalog_items_changed on media_items;
drop function bump_catalog_revision();
drop table catalog_revision;
