-- VELORA · E1.3 I2: imágenes elegidas o subidas a mano (replica/RECON_IDENTIFICACION_E_IMAGENES.md). Reversible.
-- La elección manual es la fila source = 'user' de item_images, que ya tiene prioridad al mostrarse: TMDB y el escaneo
-- escriben en sus propias filas y nunca la pisan. updated_at da la versión de las URL de imagen, para que un cambio se
-- vea en cualquier dispositivo sin esperar a que caduque la caché del navegador.

-- +goose Up
alter table item_images add column updated_at timestamptz not null default now();

-- +goose Down
alter table item_images drop column if exists updated_at;
