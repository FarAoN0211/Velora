-- VELORA · E1.2: géneros de cada título (de TMDB) para filtrar las bibliotecas. Reversible.
-- genres null = aún no se han pedido; '{}' = pedidos y sin géneros (o sin ficha de TMDB). Los títulos que ya tienen ficha
-- de TMDB quedan en null y el siguiente escaneo rellena solo sus géneros (sin volver a descargar imágenes).

-- +goose Up
alter table media_items add column genres text[];
update media_items set genres = '{}' where metadata_refreshed_at is not null and not (provider_ids ? 'tmdb');
create index media_items_genres_idx on media_items using gin (genres);

-- +goose Down
drop index if exists media_items_genres_idx;
alter table media_items drop column if exists genres;
