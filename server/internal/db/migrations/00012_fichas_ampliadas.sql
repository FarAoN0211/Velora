-- +goose Up
alter table media_items add column presentation jsonb not null default '{}'::jsonb;
-- +goose Down
alter table media_items drop column presentation;
