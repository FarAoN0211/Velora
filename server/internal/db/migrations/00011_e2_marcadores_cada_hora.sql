-- VELORA · E2: la detección de intros y créditos sigue cada hora con lo que quede pendiente (cada tanda dura como mucho
-- 90 min; sin nada pendiente termina al instante). Reversible.

-- +goose Up
insert into scheduled_tasks (id, cron) values ('markers_detect', '40 * * * *') on conflict (id) do nothing;

-- +goose Down
delete from scheduled_tasks where id = 'markers_detect';
