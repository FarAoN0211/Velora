-- VELORA · E1.3: progreso de los trabajos largos (escaneo de bibliotecas: fase, hechos, total, ruta). Reversible.

-- +goose Up
alter table jobs add column progress jsonb;

-- +goose Down
alter table jobs drop column if exists progress;
