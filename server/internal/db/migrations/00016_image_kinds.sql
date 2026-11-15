-- +goose Up
alter table item_images drop constraint item_images_kind_check;
alter table item_images add constraint item_images_kind_check check(kind in ('poster','backdrop','logo','thumb','banner','disc','art','box','boxrear','menu'));
alter table item_images drop constraint item_images_source_check;
alter table item_images add constraint item_images_source_check check(source in ('local','tmdb','fanart','tvdb','omdb','embedded','user','generated'));
-- +goose Down
alter table item_images drop constraint item_images_source_check;
alter table item_images add constraint item_images_source_check check(source in ('local','tmdb','fanart','embedded','user','generated'));
-- Si hay imágenes de los tipos nuevos, la restricción impide revertir para no perder elecciones del usuario.
alter table item_images drop constraint item_images_kind_check;
alter table item_images add constraint item_images_kind_check check(kind in ('poster','backdrop','logo','thumb','banner','disc','art'));
