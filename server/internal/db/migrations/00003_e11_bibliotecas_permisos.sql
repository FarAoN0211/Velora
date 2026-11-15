-- VELORA · E1.1: bibliotecas dinámicas y permisos por biblioteca (VELORA_DECISIONES_BIBLIOTECAS_PERMISOS.md §1–6).
-- Tres conceptos separados: la biblioteca (nombre y tipo libres), sus rutas (origen de los archivos) y los permisos
-- (qué usuario puede acceder). Una ruta nunca concede acceso. Reversible.

-- +goose Up
alter table libraries
  add column sort_order int not null default 0,
  add column show_in_menu boolean not null default true;

-- Quitar una carpeta la desvincula (detached_at) sin borrar fichas, historial ni progreso: se puede recuperar. Borrar sus
-- fichas de verdad es una acción aparte y explícita.
alter table library_paths add column detached_at timestamptz;
-- Lo mismo para una biblioteca entera: retirarla la oculta a todos conservando rutas, fichas, permisos e historial.
alter table libraries add column retired_at timestamptz;

-- Orden inicial: el alfabético que se usaba en E1.
update libraries l set sort_order = o.n from (select id, row_number() over (order by name) as n from libraries) o where o.id = l.id;

-- VF-045: acceso a la biblioteca entera (path_id null) o, en el futuro, a una carpeta concreta.
-- Sin filas = sin acceso. Los administradores ven todo; a un usuario normal nunca se le concede nada automáticamente.
create table library_access (
  user_id uuid not null references users(id) on delete cascade,
  library_id uuid not null references libraries(id) on delete cascade,
  path_id uuid references library_paths(id) on delete cascade,
  granted_at timestamptz not null default now(),
  unique nulls not distinct (user_id, library_id, path_id)
);
create index on library_access (library_id);
create index on library_access (path_id);

-- +goose Down
drop table library_access;
alter table library_paths drop column detached_at;
alter table libraries drop column retired_at;
alter table libraries drop column show_in_menu, drop column sort_order;
