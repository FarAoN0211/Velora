-- VELORA · Límites por usuario, para todo su hogar. Reversible.
-- max_streams: reproducciones simultáneas del usuario y de los perfiles de su hogar (null: el valor general de
-- Panel → Reproducción; 0: sin límite). max_household_members: miembros que el responsable puede crear en su hogar,
-- sin contarle a él (9 por defecto). Solo cuentan los del responsable; los perfiles familiares heredan los suyos.

-- +goose Up
alter table users add column max_streams int check (max_streams between 0 and 1000);
alter table users add column max_household_members int not null default 9 check (max_household_members between 0 and 19);

-- +goose Down
alter table users drop column max_household_members;
alter table users drop column max_streams;
