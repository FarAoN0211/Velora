-- VELORA · Tarea nocturna que borra definitivamente los miembros con más de 30 días en la papelera (fase 2 de
-- replica/PLAN_HOGARES_Y_PIN.md). Reversible.

-- +goose Up
insert into scheduled_tasks (id, cron) values ('household_purge', '50 3 * * *') on conflict (id) do nothing;

-- +goose Down
delete from scheduled_tasks where id = 'household_purge';
