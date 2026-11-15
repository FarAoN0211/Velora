package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"velora/internal/media"
)

// ErrDuplicate indica que ya existe (nombre de biblioteca o ruta).
var ErrDuplicate = errors.New("ya existe")

// LibraryPath es un origen de archivos de una biblioteca (local o remoto). Una ruta nunca concede acceso.
type LibraryPath struct {
	ID         uuid.UUID  `json:"id"`
	LibraryID  uuid.UUID  `json:"-"`
	Path       string     `json:"path"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	LastScanAt *time.Time `json:"last_scan_at"`
	DetachedAt *time.Time `json:"detached_at,omitempty"`
}

// Library es una biblioteca: nombre y tipo libres, orden, visibilidad en el menú y sus rutas.
type Library struct {
	ID         uuid.UUID     `json:"id"`
	Name       string        `json:"name"`
	Kind       string        `json:"kind"`
	SortOrder  int           `json:"sort_order"`
	ShowInMenu bool          `json:"show_in_menu"`
	Status     string        `json:"status"` // el peor estado de sus rutas
	LastScanAt *time.Time    `json:"last_scan_at"`
	Items      int           `json:"items"`
	Paths      []LibraryPath `json:"paths"`                // activas: se escanean y su contenido se ve
	Detached   []LibraryPath `json:"detached"`             // quitadas pero recuperables (fichas y progreso intactos)
	RetiredAt  *time.Time    `json:"retired_at,omitempty"` // retirada: invisible para todos, recuperable
	InMenu     bool          `json:"in_menu"`              // en la barra lateral de QUIEN CONSULTA (preferencia propia o show_in_menu)
	// Orden con el que QUIEN CONSULTA ve esta biblioteca (lo eligió la última vez; vacío = título A→Z).
	ViewSort    string `json:"view_sort"`
	ViewReverse bool   `json:"view_reverse"`
}

// Viewer es quien consulta el catálogo. Los administradores ven todas las bibliotecas; los demás, solo las concedidas
// en library_access. Toda consulta de catálogo, imágenes, progreso y reproducción pasa por este filtro (404 si no).
type Viewer struct {
	UserID uuid.UUID
	Admin  bool
}

// visible es el predicado de acceso para un alias de media_items; uid y admin son los números de parámetro de la
// consulta. Además del permiso, excluye lo que pertenece a carpetas desvinculadas o a bibliotecas retiradas (recuperables).
// hasMain: el elemento tiene al menos un archivo principal registrado (presente o ausente). Sin ninguno está «pendiente»
// (E1.3 T0: una carpeta con solo su tráiler): oculto a los usuarios, visible para el administrador en el panel.
func hasMain(alias string, admin int) string {
	return fmt.Sprintf(`($%d or exists (select 1 from media_files hm where hm.item_id = %[2]s.id)
		or (%[2]s.kind = 'series' and exists (select 1 from media_items he join media_files hm on hm.item_id = he.id where he.series_id = %[2]s.id))
		or (%[2]s.kind = 'season' and exists (select 1 from media_items he join media_files hm on hm.item_id = he.id where he.parent_id = %[2]s.id)))`,
		admin, alias)
}

// present: el elemento tiene algún archivo principal presente (una serie o temporada, en alguno de sus episodios). Es lo
// que decide si aparece en los listados.
func present(alias string) string {
	return fmt.Sprintf(`(exists (select 1 from media_files pf where pf.item_id = %[1]s.id and pf.missing_since is null)
		or (%[1]s.kind = 'series' and exists (select 1 from media_items pe join media_files pf on pf.item_id = pe.id
		    where pe.series_id = %[1]s.id and pf.missing_since is null))
		or (%[1]s.kind = 'season' and exists (select 1 from media_items pe join media_files pf on pf.item_id = pe.id
		    where pe.parent_id = %[1]s.id and pf.missing_since is null)))`, alias)
}

func visible(alias string, uid, admin int) string {
	return fmt.Sprintf(`(($%d or exists (select 1 from library_access a where a.user_id = coalesce((select h.owner_id from users vu join households h on h.id=vu.household_id where vu.id=$%d),$%[2]d) and a.library_id = %[3]s.library_id and a.path_id is null))
		and exists (select 1 from library_paths lp join libraries lb on lb.id = lp.library_id
		            where lp.id = %[3]s.library_path_id and lp.detached_at is null and lb.retired_at is null))`,
		admin, uid, alias)
}

// ErrPathKindChanged: al recuperar una carpeta, su tipo de montaje ya no coincide con el guardado.
var ErrPathKindChanged = errors.New("el tipo de montaje de la carpeta ha cambiado")

// ErrLastPath: una biblioteca no puede quedarse sin rutas.
var ErrLastPath = errors.New("la biblioteca debe tener al menos una ruta")

// CreateLibrary crea la biblioteca, al final del orden, con su primera ruta. No concede acceso a nadie.
func (s *Store) CreateLibrary(ctx context.Context, name, kind, path, pathKind string) (Library, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback(ctx)
	l := Library{Name: name, Kind: kind, ShowInMenu: true, Status: "pending_sync"}
	err = tx.QueryRow(ctx, `insert into libraries (name, kind, sort_order)
		values ($1, $2, (select coalesce(max(sort_order), 0) + 1 from libraries)) returning id, sort_order`, name, kind).Scan(&l.ID, &l.SortOrder)
	if isUnique(err) {
		return Library{}, ErrDuplicate
	}
	if err != nil {
		return Library{}, err
	}
	p := LibraryPath{LibraryID: l.ID, Path: path, Kind: pathKind, Status: "pending_sync"}
	err = tx.QueryRow(ctx, `insert into library_paths (library_id, path, kind, heavy_jobs_enabled) values ($1, $2, $3, $3 = 'local') returning id`,
		l.ID, path, pathKind).Scan(&p.ID)
	if isUnique(err) {
		return Library{}, ErrDuplicate
	}
	if err != nil {
		return Library{}, err
	}
	l.Paths = []LibraryPath{p}
	return l, tx.Commit(ctx)
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// Libraries lista todas las bibliotecas con sus rutas (administración y worker).
func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	return s.libraries(ctx, Viewer{Admin: true}, nil)
}

// LibrariesFor lista las bibliotecas a las que tiene acceso quien consulta.
func (s *Store) LibrariesFor(ctx context.Context, v Viewer) ([]Library, error) {
	return s.libraries(ctx, v, nil)
}

func (s *Store) libraries(ctx context.Context, v Viewer, only *uuid.UUID) ([]Library, error) {
	rows, err := s.Pool.Query(ctx, `select l.id, l.name, l.kind, l.sort_order, l.show_in_menu, l.retired_at,
		(select count(*) from media_items i join library_paths lp on lp.id = i.library_path_id
		 where i.library_id = l.id and i.parent_id is null and lp.detached_at is null)
		from libraries l
		where ($2 or exists (select 1 from library_access a where a.user_id = coalesce((select h.owner_id from users vu join households h on h.id=vu.household_id where vu.id=$1),$1) and a.library_id = l.id and a.path_id is null))
		  and ($3::uuid is null or l.id = $3) and ($2 or l.retired_at is null)
		order by coalesce((select ord::int from users mu, jsonb_array_elements_text(coalesce(mu.preferences->'menu_order','[]'::jsonb)) with ordinality as mo(id,ord) where mu.id=$1 and mo.id=l.id::text limit 1),1000000), l.sort_order, l.name`, v.UserID, v.Admin, only)
	if err != nil {
		return nil, err
	}
	libs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Library, error) {
		var l Library
		err := r.Scan(&l.ID, &l.Name, &l.Kind, &l.SortOrder, &l.ShowInMenu, &l.RetiredAt, &l.Items)
		return l, err
	})
	if err != nil || len(libs) == 0 {
		return libs, err
	}
	ids := make([]uuid.UUID, len(libs))
	for i, l := range libs {
		ids[i] = l.ID
	}
	prows, err := s.Pool.Query(ctx, `select id, library_id, path, kind, status, last_scan_at, detached_at from library_paths
		where library_id = any($1) order by created_at`, ids)
	if err != nil {
		return nil, err
	}
	paths, err := pgx.CollectRows(prows, func(r pgx.CollectableRow) (LibraryPath, error) {
		var p LibraryPath
		err := r.Scan(&p.ID, &p.LibraryID, &p.Path, &p.Kind, &p.Status, &p.LastScanAt, &p.DetachedAt)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	rank := map[string]int{"available": 0, "pending_sync": 1, "unavailable": 2}
	for i := range libs {
		l := &libs[i]
		l.Status = "available"
		for _, p := range paths {
			if p.LibraryID != l.ID {
				continue
			}
			if p.DetachedAt != nil {
				l.Detached = append(l.Detached, p)
				continue
			}
			l.Paths = append(l.Paths, p)
			if rank[p.Status] > rank[l.Status] {
				l.Status = p.Status
			}
			if p.LastScanAt != nil && (l.LastScanAt == nil || p.LastScanAt.After(*l.LastScanAt)) {
				l.LastScanAt = p.LastScanAt
			}
		}
	}
	return libs, nil
}

// LibraryByID devuelve una biblioteca con sus rutas.
func (s *Store) LibraryByID(ctx context.Context, id uuid.UUID) (Library, error) {
	libs, err := s.libraries(ctx, Viewer{Admin: true}, &id)
	if err != nil {
		return Library{}, err
	}
	if len(libs) == 0 {
		return Library{}, ErrNotFound
	}
	return libs[0], nil
}

// UpdateLibrary cambia el nombre o la visibilidad en el menú. No toca rutas, contenido ni permisos.
func (s *Store) UpdateLibrary(ctx context.Context, id uuid.UUID, name *string, showInMenu *bool) error {
	tag, err := s.Pool.Exec(ctx, `update libraries set name = coalesce($2, name), show_in_menu = coalesce($3, show_in_menu)
		where id = $1`, id, name, showInMenu)
	if isUnique(err) {
		return ErrDuplicate
	}
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetLibraryOrder fija el orden de aparición según la lista de ids (las que no aparezcan van al final).
func (s *Store) SetLibraryOrder(ctx context.Context, ids []uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update libraries l set sort_order = coalesce(x.n, 100000 + l.sort_order)
		from (select l2.id, o.n::int as n from libraries l2
		      left join unnest($1::uuid[]) with ordinality as o(id, n) on o.id = l2.id) x
		where x.id = l.id`, ids)
	return err
}

// AddLibraryPath añade un origen a una biblioteca.
// Si la misma carpeta estaba quitada de esta biblioteca, se recupera con todo su contenido.
func (s *Store) AddLibraryPath(ctx context.Context, libraryID uuid.UUID, path, pathKind string) (LibraryPath, error) {
	p := LibraryPath{LibraryID: libraryID, Path: path, Kind: pathKind, Status: "pending_sync"}
	var oldKind string
	err := s.Pool.QueryRow(ctx, `select id, kind from library_paths where library_id = $1 and path = $2 and detached_at is not null`,
		libraryID, path).Scan(&p.ID, &oldKind)
	switch {
	case err == nil && oldKind != pathKind:
		// El montaje ya no es del mismo tipo (p. ej. era local y ahora es remoto): no se recupera a ciegas.
		return p, ErrPathKindChanged
	case err == nil:
		_, err = s.Pool.Exec(ctx, `update library_paths set detached_at = null, status = 'pending_sync' where id = $1`, p.ID)
		return p, err
	case !errors.Is(err, pgx.ErrNoRows):
		return p, err
	}
	err = s.Pool.QueryRow(ctx, `insert into library_paths (library_id, path, kind, heavy_jobs_enabled) values ($1, $2, $3, $3 = 'local') returning id`,
		libraryID, path, pathKind).Scan(&p.ID)
	if isUnique(err) {
		return p, ErrDuplicate
	}
	var fk *pgconn.PgError
	if errors.As(err, &fk) && fk.Code == "23503" {
		return p, ErrNotFound
	}
	return p, err
}

// DetachLibraryPath quita un origen de la biblioteca SIN borrar nada: sus fichas, historial y progreso dejan de verse y
// de escanearse, pero se recuperan con RestoreLibraryPath (o volviendo a añadir la misma carpeta). No puede ser la última.
func (s *Store) DetachLibraryPath(ctx context.Context, libraryID, pathID uuid.UUID) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Se bloquea primero la fila de la biblioteca: dos desvinculaciones simultáneas no pueden dejarla sin rutas activas.
	if err := tx.QueryRow(ctx, `select 1 from libraries where id = $1 for update`, libraryID).Scan(new(int)); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var active int
	if err := tx.QueryRow(ctx, `select count(*) from library_paths where library_id = $1 and detached_at is null`, libraryID).Scan(&active); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `update library_paths set detached_at = now() where id = $2 and library_id = $1 and detached_at is null and $3 > 1`,
		libraryID, pathID, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if active <= 1 {
			return ErrLastPath
		}
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// DetachedPath devuelve una ruta desvinculada (para revalidarla antes de recuperarla).
func (s *Store) DetachedPath(ctx context.Context, libraryID, pathID uuid.UUID) (LibraryPath, error) {
	p := LibraryPath{ID: pathID, LibraryID: libraryID}
	err := s.Pool.QueryRow(ctx, `select path, kind from library_paths where id = $2 and library_id = $1 and detached_at is not null`,
		libraryID, pathID).Scan(&p.Path, &p.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// RestoreLibraryPath vuelve a vincular un origen quitado, con todo su contenido, si su tipo de montaje sigue siendo el
// que se comprobó ahora (pathKind).
func (s *Store) RestoreLibraryPath(ctx context.Context, libraryID, pathID uuid.UUID, pathKind string) error {
	tag, err := s.Pool.Exec(ctx, `update library_paths set detached_at = null, status = 'pending_sync'
		where id = $2 and library_id = $1 and detached_at is not null and kind = $3`, libraryID, pathID, pathKind)
	if err == nil && tag.RowsAffected() == 0 {
		if _, derr := s.DetachedPath(ctx, libraryID, pathID); derr == nil {
			return ErrPathKindChanged
		}
		return ErrNotFound
	}
	return err
}

// PurgeLibraryPath borra definitivamente las fichas de un origen YA quitado (nunca los archivos). Solo se puede purgar lo
// que antes se ha desvinculado: una acción corriente no puede perder datos de forma irreversible.
func (s *Store) PurgeLibraryPath(ctx context.Context, libraryID, pathID uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `delete from library_paths where id = $2 and library_id = $1 and detached_at is not null`, libraryID, pathID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SessionStillValid comprueba, durante una reproducción abierta, que la sesión sigue vigente y sin revocar y que el
// usuario sigue habilitado y con permiso de reproducir.
func (s *Store) SessionStillValid(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from sessions s join users u on u.id = s.user_id
		where s.id = $1 and s.revoked_at is null and s.expires_at > now() and not u.is_disabled and u.can_play)`, sessionID).Scan(&ok)
	return ok, err
}

// RetireLibrary retira una biblioteca: deja de verse y de escanearse para todos, pero rutas, fichas, metadatos, permisos e
// historial se conservan y se recuperan con RestoreLibrary.
func (s *Store) RetireLibrary(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `update libraries set retired_at = now() where id = $1 and retired_at is null`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// RestoreLibrary recupera una biblioteca retirada con sus permisos anteriores, pero OCULTA del menú: así no reaparece
// de golpe en las cuentas de otros usuarios hasta que el administrador decida mostrarla.
func (s *Store) RestoreLibrary(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `update libraries set retired_at = null, show_in_menu = false where id = $1 and retired_at is not null`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PurgeLibrary borra definitivamente una biblioteca YA retirada con todas sus fichas, permisos e historial (nunca los
// archivos).
func (s *Store) PurgeLibrary(ctx context.Context, id uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `delete from libraries where id = $1 and retired_at is not null`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetPathStatus actualiza el estado de salud de una ruta.
func (s *Store) SetPathStatus(ctx context.Context, pathID uuid.UUID, status string, scanned bool) error {
	_, err := s.Pool.Exec(ctx, `update library_paths set status = $2, last_check_at = now(),
		status_changed_at = case when status <> $2 then now() else status_changed_at end,
		last_scan_at = case when $3 then now() else last_scan_at end where id = $1`, pathID, status, scanned)
	return err
}

// MovieUpsert son los datos que el escáner deduce de una carpeta.
type MovieUpsert struct {
	LibraryID, PathID uuid.UUID
	ContentPath       string
	Title             string
	Year              int
	ProviderIDs       map[string]string
	SortTitle         string
	Kind              string // «movie» (por defecto) o «series» (E2: la carpeta de una serie)
}

// UpsertMovie crea o actualiza la película de una carpeta. Nunca pisa campos bloqueados ni un título ya enriquecido.
func (s *Store) UpsertMovie(ctx context.Context, m MovieUpsert) (uuid.UUID, error) {
	ids, _ := json.Marshal(m.ProviderIDs)
	var year *int
	if m.Year > 0 {
		year = &m.Year
	}
	kind := m.Kind
	if kind == "" {
		kind = "movie"
	}
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, year, content_path, provider_ids)
		values ($1, $2, $8, $3, $4, $5, $6, $7)
		on conflict (library_path_id, content_path) do update set
		  -- Una ficha identificada a mano (I2) no cambia de identificación por el nombre de la carpeta.
		  provider_ids = case when media_items.match_source = 'manual' then media_items.provider_ids else media_items.provider_ids || excluded.provider_ids end,
		  -- Reparación: un título vacío (versiones anteriores lo dejaban así con nombres como «Spanish Affair 2») se
		  -- sustituye por el del nombre de la carpeta y se vuelve a pedir a TMDB. Nunca se toca un título con contenido.
		  title = case when trim(media_items.title) = '' and not media_items.locked and not ('title' = any(media_items.locked_fields))
		               and excluded.title <> '' then excluded.title else media_items.title end,
		  sort_title = case when trim(media_items.title) = '' and not media_items.locked and not ('title' = any(media_items.locked_fields))
		                    and excluded.title <> '' then excluded.sort_title else media_items.sort_title end,
		  metadata_refreshed_at = case when trim(media_items.title) = '' and not media_items.locked and excluded.title <> ''
		                               then null else media_items.metadata_refreshed_at end,
		  year = case when media_items.locked or 'year' = any(media_items.locked_fields) or media_items.metadata_refreshed_at is not null
		              then media_items.year else excluded.year end
		returning id`, m.LibraryID, m.PathID, m.Title, m.SortTitle, year, m.ContentPath, ids, kind).Scan(&id)
	return id, err
}

// FileUpsert es un fichero visto por el escáner.
type FileUpsert struct {
	ItemID, PathID uuid.UUID
	RelPath        string
	Size           int64
	MTime          time.Time
	VersionLabel   string
}

// UpsertFile registra el fichero y devuelve si hay que (re)analizarlo (nuevo o cambiado).
func (s *Store) UpsertFile(ctx context.Context, f FileUpsert) (uuid.UUID, bool, error) {
	var id uuid.UUID
	var needProbe bool
	err := s.Pool.QueryRow(ctx, `insert into media_files (item_id, library_path_id, rel_path, size_bytes, mtime, version_label)
		values ($1, $2, $3, $4, $5, nullif($6, ''))
		on conflict (library_path_id, rel_path) do update set item_id = excluded.item_id, missing_since = null,
		  version_label = excluded.version_label,
		  probed_at = case when media_files.size_bytes <> excluded.size_bytes or media_files.mtime <> excluded.mtime
		                   then null else media_files.probed_at end,
		  size_bytes = excluded.size_bytes, mtime = excluded.mtime
		returning id, probed_at is null`, f.ItemID, f.PathID, f.RelPath, f.Size, f.MTime, f.VersionLabel).Scan(&id, &needProbe)
	return id, needProbe, err
}

// StreamRow es una pista para guardar.
type StreamRow struct {
	Index    int
	Kind     string
	Codec    string
	Profile  string
	Language string
	Title    string
	Default  bool
	Forced   bool
	Width    int
	Height   int
	BitDepth int
	HDR      string
	Channels int
}

// SaveProbe guarda el análisis de un fichero y sus pistas.
func (s *Store) SaveProbe(ctx context.Context, fileID uuid.UUID, container string, durationMS int64, bitrate int, raw []byte, streams []StreamRow) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update media_files set container = $2, duration_ms = $3, bitrate_kbps = $4, probe = $5, probed_at = now() where id = $1`,
		fileID, container, durationMS, bitrate, raw); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from media_streams where file_id = $1`, fileID); err != nil {
		return err
	}
	for _, st := range streams {
		var hdr *string
		if st.Kind == "video" {
			hdr = &st.HDR
		}
		if _, err := tx.Exec(ctx, `insert into media_streams (file_id, stream_index, kind, codec, profile, language, title, is_default,
			is_forced, width, height, bit_depth, hdr_format, channels)
			values ($1, $2, $3, $4, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, $9, nullif($10, 0), nullif($11, 0), nullif($12, 0), $13, nullif($14, 0))`,
			fileID, st.Index, st.Kind, st.Codec, st.Profile, st.Language, st.Title, st.Default, st.Forced,
			st.Width, st.Height, st.BitDepth, hdr, st.Channels); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update media_items set runtime_ms = coalesce(runtime_ms, $2) where id = (select item_id from media_files where id = $1)`,
		fileID, durationMS); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MarkMissing marca como desaparecidos los ficheros de una ruta que no se han visto en el escaneo (no los borra).
func (s *Store) MarkMissing(ctx context.Context, pathID uuid.UUID, seen []string) (int64, error) {
	// Freno ante montajes que fallan a medias: no se marca nada si desaparecería más del 20 % de lo conocido (y más de
	// 10 archivos) o, en bibliotecas pequeñas, más de la mitad (y al menos 3). Un montaje caído no debe parecer un borrado.
	var present, gone int64
	if err := s.Pool.QueryRow(ctx, `select count(*) filter (where missing_since is null),
		count(*) filter (where missing_since is null and not (rel_path = any($2)))
		from media_files where library_path_id = $1`, pathID, seen).Scan(&present, &gone); err != nil {
		return 0, err
	}
	if (gone > 10 && gone*5 > present) || (gone >= 3 && gone*2 > present) {
		return gone, ErrMassMissing
	}
	tag, err := s.Pool.Exec(ctx, `update media_files set missing_since = coalesce(missing_since, now())
		where library_path_id = $1 and not (rel_path = any($2))`, pathID, seen)
	return tag.RowsAffected(), err
}

// ErrMassMissing: el escaneo vería desaparecer demasiados archivos de golpe; no se marca ninguno como ausente.
var ErrMassMissing = errors.New("desaparecerían demasiados archivos de golpe; no se marca ninguno como ausente")

// SetImage registra una imagen (local o descargada) del elemento.
func (s *Store) SetImage(ctx context.Context, itemID uuid.UUID, kind, source, path string, pathID *uuid.UUID) error {
	// El escaneo vuelve a registrar las imágenes locales: sin cambios no cuenta como versión nueva (no invalida la caché).
	_, err := s.Pool.Exec(ctx, `insert into item_images (item_id, kind, source, path, library_path_id) values ($1, $2, $3, $4, $5)
		on conflict (item_id, kind, source) do update set path = excluded.path, library_path_id = excluded.library_path_id,
		updated_at = case when excluded.source = 'local' and item_images.path = excluded.path then item_images.updated_at else now() end`,
		itemID, kind, source, path, pathID)
	return err
}

// Image es una imagen a servir: local (relativa a una ruta de biblioteca) o de metadatos (relativa a /config/metadata).
type Image struct {
	UpdatedAt time.Time

	Source  string
	Path    string
	LibPath string // ruta absoluta de la biblioteca (solo si Source = local)
}

// ImageFor devuelve la mejor imagen de un tipo: primero la elegida a mano, luego la local y luego la de TMDB.
func (s *Store) ImageFor(ctx context.Context, v Viewer, itemID uuid.UUID, kind string) (Image, error) {
	var img Image
	var lib *string
	// Una temporada o un episodio sin imagen propia usa la de su serie (salvo la miniatura 16:9, que es solo suya).
	err := s.Pool.QueryRow(ctx, `select m.source, m.path, p.path, m.updated_at from item_images m join media_items i on i.id = m.item_id
		left join library_paths p on p.id = m.library_path_id
		where (m.item_id = $1 or (m.kind <> 'thumb' and m.item_id = (select o.series_id from media_items o where o.id = $1)))
		  and m.kind = $2 and `+visible("i", 3, 4)+`
		order by (m.item_id = $1) desc, case m.source when 'user' then 0 when 'local' then 1 else 2 end limit 1`,
		itemID, kind, v.UserID, v.Admin).Scan(&img.Source, &img.Path, &lib, &img.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return img, ErrNotFound
	}
	if lib != nil {
		img.LibPath = *lib
	}
	return img, err
}

// ItemCard es una película en una rejilla o fila.
type ItemCard struct {
	Library4K   bool       `json:"library_4k"`
	ID          uuid.UUID  `json:"id"`
	LibraryID   uuid.UUID  `json:"library_id"`
	Title       string     `json:"title"`
	Year        *int16     `json:"year"`
	HasPoster   bool       `json:"has_poster"`
	PositionMS  int64      `json:"position_ms"`
	RuntimeMS   *int64     `json:"runtime_ms"`
	Played      bool       `json:"played"`
	AddedAt     time.Time  `json:"added_at"`
	LastPlayed  *time.Time `json:"last_played_at,omitempty"`
	HasBackdrop bool       `json:"has_backdrop"`
	Quality     string     `json:"quality,omitempty"`    // 4K, 1080p, 720p o SD según la mejor pista de vídeo
	TrailerID   *uuid.UUID `json:"trailer_id,omitempty"` // tráiler para «Ver tráiler» del menú ⋮ (E1.3), si hay
	ImageRev    string     `json:"image_rev,omitempty"`  // versión de sus imágenes: cambia en cuanto cambia alguna
	HasLogo     bool       `json:"has_logo,omitempty"`
	HasBanner   bool       `json:"has_banner,omitempty"` // imagen destacada propia para la cabecera del inicio
	// E2 S1: series, temporadas y episodios. Una película es kind «movie» y no trae el resto.
	Kind          string     `json:"kind"`
	SeriesID      *uuid.UUID `json:"series_id,omitempty"`
	SeriesTitle   *string    `json:"series_title,omitempty"`
	SeasonNumber  *int       `json:"season_number,omitempty"`  // del episodio (o de la propia temporada)
	EpisodeNumber *int       `json:"episode_number,omitempty"` // 0 o ausente: episodio sin número
	EpisodeEnd    *int       `json:"episode_end,omitempty"`    // multiepisodio «E01–E02»
	HasThumb      bool       `json:"has_thumb,omitempty"`      // imagen 16:9 propia del episodio
}

// cardCols: la duración sale de TMDB y, si no hay, del archivo más largo (para «Quedan…» también sin metadatos).
const cardCols = `i.id, i.library_id, i.title, i.year, exists (select 1 from item_images m where m.item_id in (i.id, i.series_id) and m.kind = 'poster'),
	coalesce(d.position_ms, 0),
	coalesce(i.runtime_ms, (select max(f.duration_ms) from media_files f where f.item_id = i.id and f.missing_since is null)),
	` + playedExpr + `, i.added_at, d.last_played_at,
	exists (select 1 from item_images m where m.item_id in (i.id, i.series_id) and m.kind = 'backdrop'),
	coalesce((select case max(` + qualityRank + `) when 4 then '4K' when 3 then '1080p' when 2 then '720p' when 1 then 'SD' end
	          from media_streams st join media_files f on f.id = st.file_id
	          where f.item_id = i.id and f.missing_since is null and st.kind = 'video'), '')` + `,
	(select x.id from media_extras x where x.item_id = i.id and x.kind = 'trailer' and x.missing_since is null
	 order by ` + trailerOrder + ` limit 1),
	coalesce((select (extract(epoch from max(m.updated_at)) * 1000000)::bigint::text from item_images m where m.item_id in (i.id, i.series_id)), ''),
	exists (select 1 from item_images m where m.item_id in (i.id, i.series_id) and m.kind = 'logo'),
	exists (select 1 from item_images m where m.item_id = i.id and m.kind = 'banner'),
	i.kind, i.series_id, (select se.title from media_items se where se.id = i.series_id),
	case when i.kind = 'season' then i.index_number else (select ps.index_number from media_items ps where ps.id = i.parent_id and i.kind = 'episode') end,
	case when i.kind = 'episode' then i.index_number end, i.index_number_end,
	exists (select 1 from item_images m where m.item_id = i.id and m.kind = 'thumb'),
 (i.kind='movie' and exists(select 1 from libraries ql where ql.id=i.library_id and ql.name ~* '(4[[:space:]]*k|uhd)'))`

// playedExpr: una serie está vista cuando lo están todos sus episodios presentes. Todas las consultas con cardCols
// llevan el usuario en $1.
const playedExpr = `(case when i.kind = 'series' then not exists (select 1 from media_items pe where pe.series_id = i.id
	  and pe.kind = 'episode' and exists (select 1 from media_files pf where pf.item_id = pe.id and pf.missing_since is null)
	  and not exists (select 1 from user_item_data pd where pd.item_id = pe.id and pd.user_id = $1 and pd.played))
	else coalesce(d.played, false) end)`

// trailerOrder elige el tráiler de una película de forma estable: primero el principal («trailer.mkv» o
// «Película-trailer.mkv»), luego los numerados (trailer1, trailer-2…) y luego los de la subcarpeta Trailers/, por nombre.
const trailerOrder = `(x.rel_path ~* '(^|/)([^/]*-)?trailer\.[a-z0-9]+$') desc, (x.rel_path ~* '^[^/]+/[^/]+$') desc, length(x.rel_path), x.rel_path`

// qualityRank clasifica una pista de vídeo por su ancho O su alto (como Plex y Jellyfin): una película panorámica de
// 1920×800 es 1080p aunque mida menos de 1000 de alto. 4 = 4K, 3 = 1080p, 2 = 720p, 1 = SD, 0 = sin datos.
// La web aplica la misma regla en qualityLabel (lib/catalog.ts).
const mainVideoStream = `(st.kind='video' and lower(st.codec) not in ('mjpeg','png','jpeg','bmp','webp','gif','tiff')
 and not exists(select 1 from jsonb_array_elements(coalesce(f.probe->'streams','[]'::jsonb)) ps
 where ps->>'index'=st.stream_index::text and ps->'disposition'->>'attached_pic'='1'))`

const qualityRank = `(case when not ` + mainVideoStream + ` then 0 when coalesce(st.width, 0) >= 3200 or coalesce(st.height, 0) >= 1600 then 4
	when coalesce(st.width, 0) >= 1800 or coalesce(st.height, 0) >= 1000 then 3
	when coalesce(st.width, 0) >= 1200 or coalesce(st.height, 0) >= 700 then 2
	when coalesce(st.width, 0) > 0 or coalesce(st.height, 0) > 0 then 1 else 0 end)`

const itemQualityRank = `(select coalesce(max(` + qualityRank + `),0) from media_streams st join media_files f on f.id=st.file_id where f.item_id=i.id and f.missing_since is null)`

func cardDest(c *ItemCard) []any {
	return []any{&c.ID, &c.LibraryID, &c.Title, &c.Year, &c.HasPoster, &c.PositionMS, &c.RuntimeMS, &c.Played, &c.AddedAt, &c.LastPlayed,
		&c.HasBackdrop, &c.Quality, &c.TrailerID, &c.ImageRev, &c.HasLogo, &c.HasBanner,
		&c.Kind, &c.SeriesID, &c.SeriesTitle, &c.SeasonNumber, &c.EpisodeNumber, &c.EpisodeEnd, &c.HasThumb, &c.Library4K}
}

func scanCards(rows pgx.Rows) ([]ItemCard, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemCard, error) {
		var c ItemCard
		err := r.Scan(cardDest(&c)...)
		return c, err
	})
}

// ItemQuery filtra y ordena el listado de una biblioteca (o de todas).
type ItemQuery struct {
	PersonID                   int
	PersonMovies, PersonSeries []string
	LibraryID                  *uuid.UUID
	Search, Genre              string
	Sort                       string // ver itemSorts
	Desc                       bool   // invierte el orden natural de Sort
	Played                     string // "", "played" o "unplayed"
	Limit, Offset              int
}

// itemSorts: orden natural de cada criterio (el primero que se ve sin invertir). Los nulos van siempre al final y el id
// desempata, para que «Cargar más» no repita ni salte títulos.
var itemSorts = map[string]struct{ expr, dir string }{
	"title": {"i.sort_title", "asc"},
	// Una serie sube con su último episodio nuevo.
	"added":       {"(case when i.kind = 'series' then coalesce((select max(e.added_at) from media_items e where e.series_id = i.id and e.kind = 'episode'), i.added_at) else i.added_at end)", "desc"},
	"year":        {"i.year", "desc"},
	"rating":      {"i.community_rating", "desc"},
	"runtime":     {"coalesce(i.runtime_ms, (select max(f.duration_ms) from media_files f where f.item_id = i.id and f.missing_since is null))", "desc"},
	"last_played": {"d.last_played_at", "desc"},
	// Por categoría (4K, 1080p, 720p, SD) y, dentro de cada una, por píxeles.
	"resolution": {`(select max(` + qualityRank + ` * 100000000 + coalesce(st.width, 0) * coalesce(st.height, 0)) from media_streams st
		join media_files f on f.id = st.file_id where f.item_id = i.id and f.missing_since is null and ` + mainVideoStream + `)`, "desc"},
}

// ValidSort indica si el criterio de orden existe.
func ValidSort(sort string) bool { _, ok := itemSorts[sort]; return ok }

// Items lista las películas visibles con búsqueda sin tildes, filtro por género y estado de visto, y orden.
func (s *Store) Items(ctx context.Context, v Viewer, q ItemQuery) ([]ItemCard, error) {
	o, ok := itemSorts[q.Sort]
	if !ok {
		o = itemSorts["title"]
	}
	dir := o.dir
	if q.Desc {
		dir = map[string]string{"asc": "desc", "desc": "asc"}[dir]
	}
	order := o.expr + " " + dir + " nulls last, i.sort_title, i.id"
	rows, err := s.Pool.Query(ctx, `select `+cardCols+` from media_items i
		left join lateral effective_item_data($1,i.id) d on true
		where i.kind in ('movie', 'series') and ($2::uuid is null or i.library_id = $2)
		  and ($3 = '' or i.search_text like '%' || lower(f_unaccent($3)) || '%')
		  and ($7 = '' or i.genres @> array[$7::text])
		  and ($8 = '' or ($8 = 'played') = `+playedExpr+`)
 and ($9::int = 0 or i.presentation->'credits'->'cast' @> jsonb_build_array(jsonb_build_object('id',$9::int)) or (i.kind='movie' and i.provider_ids->>'tmdb'=any($10::text[])) or (i.kind='series' and i.provider_ids->>'tmdb'=any($11::text[])))
		  and `+present("i")+`
		  and `+visible("i", 1, 6)+`
		order by `+order+` limit $4 offset $5`, v.UserID, q.LibraryID, q.Search, q.Limit, q.Offset, v.Admin, q.Genre, q.Played, q.PersonID, q.PersonMovies, q.PersonSeries)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

// GenreCount es un género con el número de títulos visibles que lo tienen.
type GenreCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Genres lista los géneros de las películas visibles de una biblioteca (o de todas), de más a menos frecuente.
func (s *Store) Genres(ctx context.Context, v Viewer, libraryID *uuid.UUID) ([]GenreCount, error) {
	rows, err := s.Pool.Query(ctx, `select g, count(*) from media_items i, unnest(i.genres) g
		where i.kind in ('movie', 'series') and ($2::uuid is null or i.library_id = $2)
		  and `+present("i")+`
		  and `+visible("i", 1, 3)+`
		group by g order by count(*) desc, g`, v.UserID, libraryID, v.Admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (GenreCount, error) {
		var g GenreCount
		err := r.Scan(&g.Name, &g.Count)
		return g, err
	})
}

// Featured es una película para la cabecera destacada del inicio.
type Featured struct {
	ItemCard
	Overview     *string `json:"overview"`
	OfficialRate *string `json:"official_rating"`
}

// FeaturedItems elige para la cabecera del inicio las novedades visibles con fondo; si no hay ninguna con fondo, las que
// tienen carátula (la web compone el fondo con ella). Nunca títulos sin imagen: la cabecera no debe quedar vacía.
func (s *Store) FeaturedItems(ctx context.Context, v Viewer, limit int) ([]Featured, error) {
	rows, err := s.Pool.Query(ctx, `select `+cardCols+`, i.overview, i.official_rating from media_items i
		left join lateral effective_item_data($1,i.id) d on true
		where i.kind in ('movie', 'series') and `+visible("i", 1, 3)+`
		  and `+present("i")+`
		  and exists (select 1 from item_images m where m.item_id = i.id and m.kind in ('backdrop', 'poster'))
		order by exists (select 1 from item_images m where m.item_id = i.id and m.kind in ('banner', 'backdrop')) desc,
		         (i.overview is not null) desc, i.added_at desc
		limit $2`, v.UserID, limit, v.Admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Featured, error) {
		var f Featured
		err := r.Scan(append(cardDest(&f.ItemCard), &f.Overview, &f.OfficialRate)...)
		return f, err
	})
}

// ContinueWatching son las películas empezadas y no terminadas, por última reproducción (diseño rev. 2, S03).
func (s *Store) ContinueWatching(ctx context.Context, v Viewer, limit int) ([]ItemCard, error) {
	rows, err := s.Pool.Query(ctx, `select * from (select distinct on
 (case when i.kind='movie' and i.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$' then 'tmdb:'||(i.provider_ids->>'tmdb') else i.id::text end)
 `+cardCols+` from media_items i join lateral effective_item_data($1,i.id) d on true
 where d.position_ms>0 and not d.played and d.continue_hidden_at is null and `+visible("i", 1, 3)+`
 and exists(select 1 from media_files f where f.item_id=i.id and f.missing_since is null)
 order by (case when i.kind='movie' and i.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$' then 'tmdb:'||(i.provider_ids->>'tmdb') else i.id::text end),
 (i.id=d.item_id) desc,i.id) films order by 10 desc nulls last limit $2`, v.UserID, limit, v.Admin)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

// FileInfo describe un fichero de una película.
type FileInfo struct {
	ID           uuid.UUID       `json:"id"`
	RelPath      string          `json:"-"`
	LibPath      string          `json:"-"`
	Ext          string          `json:"-"`
	Container    *string         `json:"container"`
	DurationMS   *int64          `json:"duration_ms"`
	SizeBytes    int64           `json:"size_bytes"`
	VersionLabel *string         `json:"version_label"`
	Missing      bool            `json:"missing"`
	Probe        json.RawMessage `json:"-"`
	Streams      []StreamInfo    `json:"streams"`
	// Tech: insignias técnicas de la ficha (resolución, HDR, códecs, canales, extras, contenedor), calculadas del
	// análisis guardado. Solo la rellena Item; lo que no se conoce no aparece.
	Tech  []media.TechBadge `json:"tech,omitempty"`
	Extra bool              `json:"-"` // es un tráiler o extra (nunca guarda progreso)
	// MTime: fecha de modificación registrada por el escáner (solo la rellena FileForPlayback). Con el tamaño y el
	// análisis identifica la versión del archivo: una sustitución de igual tamaño también la cambia.
	MTime time.Time `json:"-"`
}

// StreamInfo es una pista para la ficha.
type StreamInfo struct {
	Index    int     `json:"index"`
	Kind     string  `json:"kind"`
	Codec    string  `json:"codec"`
	Language *string `json:"language"`
	Title    *string `json:"title"`
	Default  bool    `json:"default"`
	Width    *int    `json:"width,omitempty"`
	Height   *int    `json:"height,omitempty"`
	BitDepth *int16  `json:"bit_depth,omitempty"`
	HDR      *string `json:"hdr,omitempty"`
	Channels *int16  `json:"channels,omitempty"`
}

// ItemDetail es la ficha completa (S05; S06 para series, temporadas y episodios).
type ItemDetail struct {
	ItemCard
	SeriesDetail
	OriginalTitle   *string           `json:"original_title"`
	CommunityRating *float64          `json:"community_rating"`
	Presentation    json.RawMessage   `json:"presentation"`
	Overview        *string           `json:"overview"`
	Tagline         *string           `json:"tagline"`
	OfficialRate    *string           `json:"official_rating"`
	ProviderIDs     map[string]string `json:"provider_ids"`
	Genres          []string          `json:"genres"`
	Extras          []ExtraInfo       `json:"extras"`
	Files           []FileInfo        `json:"files"`
}

// Item devuelve la ficha de una película con sus ficheros y pistas.
func (s *Store) Item(ctx context.Context, v Viewer, id uuid.UUID) (ItemDetail, error) {
	var d ItemDetail
	err := s.Pool.QueryRow(ctx, `select `+cardCols+`, i.overview, i.tagline, i.official_rating, i.provider_ids, coalesce(i.genres, '{}'), i.original_title, i.community_rating, i.presentation
		from media_items i left join lateral effective_item_data($1,i.id) d on true
		where i.id = $2 and i.kind in ('movie', 'series', 'season', 'episode') and `+visible("i", 1, 3)+` and `+hasMain("i", 3), v.UserID, id, v.Admin).
		Scan(append(cardDest(&d.ItemCard), &d.Overview, &d.Tagline, &d.OfficialRate, &d.ProviderIDs, &d.Genres, &d.OriginalTitle, &d.CommunityRating, &d.Presentation)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	// Los archivos se leen con el mismo filtro de acceso: si el permiso se revoca entre las dos consultas, no se
	// devuelve nada de la segunda.
	rows, err := s.Pool.Query(ctx, `select f.id, f.rel_path, p.path, f.container, f.duration_ms, f.size_bytes, f.version_label, f.missing_since is not null, f.probe
		from media_files f join library_paths p on p.id = f.library_path_id join media_items i on i.id = f.item_id
		where f.item_id = $1 and `+visible("i", 2, 3)+` order by f.missing_since nulls first,
          case when i.kind = 'episode' then `+episodeAudioRank+` else 0 end desc, f.size_bytes desc, f.id
          limit case when (select kind from media_items where id = $1) = 'episode' then 1 else null end`, id, v.UserID, v.Admin)
	if err != nil {
		return d, err
	}
	d.Files, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FileInfo, error) {
		var f FileInfo
		err := r.Scan(&f.ID, &f.RelPath, &f.LibPath, &f.Container, &f.DurationMS, &f.SizeBytes, &f.VersionLabel, &f.Missing, &f.Probe)
		return f, err
	})
	if err != nil {
		return d, err
	}
	for i := range d.Files {
		d.Files[i].Tech = media.TechBadgesJSON(d.Files[i].Probe, d.Files[i].RelPath)
		srows, err := s.Pool.Query(ctx, `select stream_index, kind, codec, language, title, is_default, width, height, bit_depth, hdr_format, channels
			from media_streams where file_id = $1 order by stream_index`, d.Files[i].ID)
		if err != nil {
			return d, err
		}
		d.Files[i].Streams, err = pgx.CollectRows(srows, func(r pgx.CollectableRow) (StreamInfo, error) {
			var st StreamInfo
			err := r.Scan(&st.Index, &st.Kind, &st.Codec, &st.Language, &st.Title, &st.Default, &st.Width, &st.Height, &st.BitDepth, &st.HDR, &st.Channels)
			return st, err
		})
		if err != nil {
			return d, err
		}
		var probe media.Probe
		_ = json.Unmarshal(d.Files[i].Probe, &probe)
		attached := map[int]bool{}
		for _, ps := range probe.Streams {
			if ps.Disposition["attached_pic"] == 1 {
				attached[ps.Index] = true
			}
		}
		actual := d.Files[i].Streams[:0]
		for _, st := range d.Files[i].Streams {
			if st.Kind == "video" && (attached[st.Index] || st.Codec == "mjpeg" || st.Codec == "png" || st.Codec == "jpeg" || st.Codec == "bmp" || st.Codec == "webp" || st.Codec == "gif" || st.Codec == "tiff") {
				continue
			}
			actual = append(actual, st)
		}
		d.Files[i].Streams = actual
	}
	d.Extras, err = s.itemExtras(ctx, id)
	if err != nil {
		return d, err
	}
	err = s.seriesDetail(ctx, v, &d)
	return d, err
}

// ExtraInfo es un tráiler o extra presente de una película (E1.3 T1). Las pistas salen de su análisis: los extras no
// tienen filas en media_streams.
type ExtraInfo struct {
	ID         uuid.UUID    `json:"id"`
	Kind       string       `json:"kind"`
	Title      string       `json:"title"` // vacío = la web usa el nombre del tipo («Tráiler»)
	DurationMS *int64       `json:"duration_ms"`
	Streams    []StreamInfo `json:"streams"`
}

// Orden de presentación: primero los tráileres, luego el resto en un orden fijo.
const extraOrder = `array_position(array['trailer','featurette','behindthescenes','interview','scene','deleted','short','extra'], x.kind)`

func (s *Store) itemExtras(ctx context.Context, itemID uuid.UUID) ([]ExtraInfo, error) {
	rows, err := s.Pool.Query(ctx, `select x.id, x.kind, x.rel_path, x.duration_ms,
		  coalesce((select jsonb_agg(jsonb_build_object('index', st->'index', 'kind', st->>'codec_type', 'codec', coalesce(st->>'codec_name', ''),
		      'language', st->'tags'->>'language', 'title', st->'tags'->>'title', 'default', coalesce((st->'disposition'->>'default')::int, 0) = 1,
		      'width', st->'width', 'height', st->'height', 'channels', st->'channels') order by (st->>'index')::int)
		    from jsonb_array_elements(coalesce(x.probe->'streams', '[]')) st where st->>'codec_type' in ('video', 'audio', 'subtitle')), '[]')
		from media_extras x where x.item_id = $1 and x.missing_since is null
		order by `+extraOrder+`, x.rel_path`, itemID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ExtraInfo, error) {
		var x ExtraInfo
		var rel string
		var streams []byte
		if err := r.Scan(&x.ID, &x.Kind, &rel, &x.DurationMS, &streams); err != nil {
			return x, err
		}
		x.Title = extraTitle(rel)
		err := json.Unmarshal(streams, &x.Streams)
		return x, err
	})
	if out == nil {
		out = []ExtraInfo{}
	}
	return out, err
}

var (
	extraNameSuffix = regexp.MustCompile(`(?i)[-_. ](trailer|featurette|behindthescenes|deleted|interview|scene|short|other|extra)\d*$`)
	bareTrailerName = regexp.MustCompile(`(?i)^trailer([-_. ]?\d+|\.f\d+)?$`)
)

// extraTitle saca un título legible del nombre del archivo: «trailer.mkv» → "" (la web pone «Tráiler»),
// «Origen-featurette.mkv» → «Origen», «Trailers/Teaser final.mkv» → «Teaser final».
func extraTitle(rel string) string {
	base := path.Base(rel)
	base = strings.TrimSuffix(base, path.Ext(base))
	if bareTrailerName.MatchString(base) {
		return ""
	}
	base = extraNameSuffix.ReplaceAllString(base, "")
	return strings.TrimSpace(strings.NewReplacer("_", " ", ".", " ").Replace(base))
}

// FileForPlayback devuelve lo necesario para reproducir un fichero visible (ruta de biblioteca + ruta relativa + análisis).
func (s *Store) FileForPlayback(ctx context.Context, v Viewer, fileID uuid.UUID) (FileInfo, uuid.UUID, error) {
	var f FileInfo
	var itemID uuid.UUID
	// Un tráiler o extra se reproduce por la misma vía que un archivo de la película (mismos permisos y revocación);
	// el de una ficha pendiente solo lo ve el administrador.
	err := s.Pool.QueryRow(ctx, `select f.id, f.item_id, f.rel_path, p.path, f.probe, f.missing_since is not null, f.size_bytes, false, f.mtime
		from media_files f join library_paths p on p.id = f.library_path_id join media_items i on i.id = f.item_id
		where f.id = $1 and `+visible("i", 2, 3)+`
		union all
		select x.id, x.item_id, x.rel_path, p.path, x.probe, x.missing_since is not null, x.size_bytes, true, x.mtime
		from media_extras x join library_paths p on p.id = x.library_path_id join media_items i on i.id = x.item_id
		where x.id = $1 and `+visible("i", 2, 3)+` and `+hasMain("i", 3), fileID, v.UserID, v.Admin).
		Scan(&f.ID, &itemID, &f.RelPath, &f.LibPath, &f.Probe, &f.Missing, &f.SizeBytes, &f.Extra, &f.MTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, itemID, ErrNotFound
	}
	return f, itemID, err
}

// SaveProgress guarda la posición; se marca como vista al pasar del 90 % (umbral por defecto, VF-057 en E6).
// Gana la actualización más reciente del cliente (sin conexión, §14).
func (s *Store) SaveProgress(ctx context.Context, userID, itemID uuid.UUID, positionMS, durationMS int64, clientAt time.Time) error {
	played := durationMS > 0 && positionMS*100 >= durationMS*90
	if played {
		positionMS = 0
	}
	_, err := s.Pool.Exec(ctx, `with original as(select * from media_items where id=$2),
 previous as(select * from effective_item_data($1,$2)),
 copies as(select peer.id from original i join media_items peer on peer.id=i.id or
  (i.kind='movie' and peer.kind='movie' and i.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$' and peer.provider_ids->>'tmdb'=i.provider_ids->>'tmdb'))
 insert into user_item_data(user_id,item_id,position_ms,played,play_count,last_played_at,client_updated_at)
 select $1,c.id,$3, $4 or coalesce((select played from previous),false),
 coalesce((select play_count from previous),0)+case when $4 and not coalesce((select played from previous),false) then 1 else 0 end,
 case when c.id=$2 then now() else (select last_played_at from previous) end,$5
 from copies c where not exists(select 1 from previous where client_updated_at>$5) order by c.id
 on conflict(user_id,item_id) do update set position_ms=excluded.position_ms,played=excluded.played,
 play_count=excluded.play_count,last_played_at=excluded.last_played_at,client_updated_at=excluded.client_updated_at,continue_hidden_at=null
 where user_item_data.client_updated_at is null or user_item_data.client_updated_at<=excluded.client_updated_at`, userID, itemID, positionMS, played, clientAt)
	return err
}

// SetPlayed marca o desmarca como vista. En una serie o una temporada se aplica a todos sus episodios.
func (s *Store) SetPlayed(ctx context.Context, userID, itemID uuid.UUID, played bool) error {
	_, err := s.Pool.Exec(ctx, `insert into user_item_data (user_id, item_id, played, play_count,client_updated_at)
		select $1, e.id, $3, case when $3 then 1 else 0 end,now() from media_items i join media_items e
		  on e.id = i.id and i.kind not in ('series', 'season')
		  or (i.kind='movie' and e.kind='movie' and i.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$' and e.provider_ids->>'tmdb'=i.provider_ids->>'tmdb')
		  or (i.kind = 'series' and e.series_id = i.id and e.kind = 'episode')
		  or (i.kind = 'season' and e.parent_id = i.id and e.kind = 'episode')
		where i.id = $2 order by e.id
		on conflict (user_id, item_id) do update set played = $3, position_ms = 0,client_updated_at=now()`, userID, itemID, played)
	return err
}

// HideContinue quita (o devuelve) un elemento de «Continuar viendo» sin marcarlo como visto.
func (s *Store) HideContinue(ctx context.Context, userID, itemID uuid.UUID, hide bool) error {
	_, err := s.Pool.Exec(ctx, `update user_item_data set continue_hidden_at = case when $3 then now() end where user_id=$1 and item_id in(select peer.id from media_items i join media_items peer on peer.id=i.id or
 (i.kind='movie' and peer.kind='movie' and i.provider_ids->>'tmdb' ~ '^[1-9][0-9]*$' and peer.provider_ids->>'tmdb'=i.provider_ids->>'tmdb') where i.id=$2)`,
		userID, itemID, hide)
	return err
}

// ItemVisible comprueba que la película existe y quien consulta tiene acceso (404 en ambos casos si no).
func (s *Store) ItemVisible(ctx context.Context, v Viewer, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from media_items i where i.id = $1 and `+visible("i", 2, 3)+` and `+hasMain("i", 3)+`)`,
		id, v.UserID, v.Admin).Scan(&ok)
	return ok, err
}

// MetadataFields son los datos que aporta un proveedor.
type MetadataFields struct {
	Title, OriginalTitle, Overview, Tagline, OfficialRating string
	Year                                                    int
	RuntimeMS                                               int64
	RatingAge                                               *int
	CommunityRating                                         float64
	ProviderIDs                                             map[string]string
	Genres                                                  []string
	Presentation                                            json.RawMessage
}

// ApplyMetadata aplica los datos del proveedor respetando los campos bloqueados (VF-017). Con onlyMissing («Completar
// lo que falta», I1) solo rellena los campos vacíos: nunca sustituye un valor que ya existe.
func (s *Store) ApplyMetadata(ctx context.Context, itemID uuid.UUID, m MetadataFields, sortTitle string, onlyMissing ...bool) error {
	om := len(onlyMissing) > 0 && onlyMissing[0]
	_, err := s.applyMetadata(ctx, itemID, m, sortTitle, om, nil)
	return err
}

// ApplyMetadataIfIdentity es ApplyMetadata solo si la ficha sigue teniendo el ID de TMDB con el que se empezó: un trabajo
// en curso no puede pisar con la película anterior una identificación manual hecha mientras tanto. Devuelve false si
// cambió, y entonces no escribe nada.
func (s *Store) ApplyMetadataIfIdentity(ctx context.Context, itemID uuid.UUID, m MetadataFields, sortTitle string, onlyMissing bool, expected string) (bool, error) {
	return s.applyMetadata(ctx, itemID, m, sortTitle, onlyMissing, &expected)
}

func (s *Store) applyMetadata(ctx context.Context, itemID uuid.UUID, m MetadataFields, sortTitle string, om bool, expected *string) (bool, error) {
	ids, _ := json.Marshal(m.ProviderIDs)
	tag, err := s.Pool.Exec(ctx, `update media_items set
		title = case when locked or 'title' = any(locked_fields) or $2 = '' or ($14 and trim(title) <> '') then title else $2 end,
		sort_title = case when locked or 'title' = any(locked_fields) or 'sort_title' = any(locked_fields) or $2 = ''
		                   or ($14 and trim(title) <> '') then sort_title else $3 end,
		original_title = case when locked or 'original_title' = any(locked_fields) or ($14 and coalesce(original_title, '') <> '') then original_title else nullif($4, '') end,
		overview = case when locked or 'overview' = any(locked_fields) or ($14 and coalesce(overview, '') <> '') then overview else nullif($5, '') end,
		tagline = case when locked or 'tagline' = any(locked_fields) or ($14 and coalesce(tagline, '') <> '') then tagline else nullif($6, '') end,
		year = case when locked or 'year' = any(locked_fields) or $7 = 0 or ($14 and year is not null) then year else $7 end,
		runtime_ms = case when $8 > 0 and not ($14 and runtime_ms is not null) then $8 else runtime_ms end,
		official_rating = case when locked or 'official_rating' = any(locked_fields) or ($14 and coalesce(official_rating, '') <> '') then official_rating else nullif($9, '') end,
		rating_age = case when locked or 'official_rating' = any(locked_fields) or ($14 and coalesce(official_rating, '') <> '') then rating_age else $10 end,
		community_rating = case when $11 > 0 and not ($14 and community_rating is not null) then $11 else community_rating end,
		provider_ids = provider_ids || $12,
		genres = case when locked or 'genres' = any(locked_fields) or ($14 and cardinality(coalesce(genres, '{}')) > 0) then genres else $13 end,
		presentation = case when locked or ($14 and presentation <> '{}'::jsonb) then presentation else $16::jsonb end,
		metadata_refreshed_at = now()
		where id = $1 and ($15::text is null or coalesce(provider_ids->>'tmdb', '') = $15)`, itemID, m.Title, sortTitle, m.OriginalTitle, m.Overview, m.Tagline, m.Year, m.RuntimeMS,
		m.OfficialRating, m.RatingAge, m.CommunityRating, ids, nonNilStrings(m.Genres), om, expected, presentationJSON(m.Presentation))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// MarkMetadataTried evita reintentar en cada escaneo los elementos que el proveedor no reconoce.
func (s *Store) MarkMetadataTried(ctx context.Context, itemID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update media_items set metadata_refreshed_at = now(), genres = coalesce(genres, '{}') where id = $1`, itemID)
	return err
}

// PendingMetadata son las películas sin metadatos todavía.
type PendingMetadata struct {
	ID          uuid.UUID
	Kind        string // «series» pide la ficha de serie; el resto, de película
	Title       string
	Year        *int16
	ProviderIDs map[string]string
}

// ItemsNeedingMetadata devuelve películas sin enriquecer de una biblioteca.
func (s *Store) ItemsNeedingMetadata(ctx context.Context, libraryID uuid.UUID, limit int) ([]PendingMetadata, error) {
	rows, err := s.Pool.Query(ctx, `select id, kind, title, year, provider_ids from media_items
		where library_id = $1 and kind in ('movie', 'series') and metadata_refreshed_at is null and not locked order by added_at limit $2`, libraryID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingMetadata, error) {
		var p PendingMetadata
		err := r.Scan(&p.ID, &p.Kind, &p.Title, &p.Year, &p.ProviderIDs)
		return p, err
	})
}

// CacheGet lee la caché de un proveedor si no ha caducado.
func (s *Store) CacheGet(ctx context.Context, provider, key string) ([]byte, bool) {
	var raw []byte
	err := s.Pool.QueryRow(ctx, `select payload from metadata_cache where provider = $1 and cache_key = $2 and expires_at > now()`,
		provider, key).Scan(&raw)
	return raw, err == nil
}

// CachePut guarda en la caché (TMDB: como mucho 180 días por sus términos; lo garantiza un CHECK).
func (s *Store) CachePut(ctx context.Context, provider, key string, payload []byte, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx, `insert into metadata_cache (provider, cache_key, payload, expires_at) values ($1, $2, $3, now() + make_interval(secs => $4))
		on conflict (provider, cache_key) do update set payload = excluded.payload, fetched_at = now(), expires_at = excluded.expires_at`,
		provider, key, payload, ttl.Seconds())
	return err
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// ItemsNeedingGenres devuelve películas con ficha de TMDB a las que aún les faltan los géneros (instalaciones anteriores
// a la migración 00004): se rellenan sin volver a descargar imágenes.
func (s *Store) ItemsNeedingGenres(ctx context.Context, libraryID uuid.UUID, limit int) ([]PendingMetadata, error) {
	rows, err := s.Pool.Query(ctx, `select id, kind, title, year, provider_ids from media_items
		where library_id = $1 and kind in ('movie', 'series') and genres is null and provider_ids ? 'tmdb' and not locked
		order by added_at limit $2`, libraryID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingMetadata, error) {
		var p PendingMetadata
		err := r.Scan(&p.ID, &p.Kind, &p.Title, &p.Year, &p.ProviderIDs)
		return p, err
	})
}

// SetGenres guarda los géneros si el campo no está bloqueado.
func (s *Store) SetGenres(ctx context.Context, itemID uuid.UUID, genres []string) error {
	_, err := s.Pool.Exec(ctx, `update media_items set genres = case when locked or 'genres' = any(locked_fields) then coalesce(genres, '{}') else $2 end
		where id = $1`, itemID, nonNilStrings(genres))
	return err
}

// OverviewCounts es el recuento del panel de control (solo bibliotecas y carpetas activas).
type OverviewCounts struct {
	Libraries int   `json:"libraries"`
	Movies    int   `json:"movies"`
	Series    int   `json:"series"`
	Episodes  int   `json:"episodes"`
	Files     int   `json:"files"`
	SizeBytes int64 `json:"size_bytes"`
	Users     int   `json:"users"`
	Genres    int   `json:"pending_genres"` // películas con ficha de TMDB a las que aún les faltan los géneros
	Pending   int   `json:"pending_items"`  // películas sin archivo principal (solo tráiler): ocultas a los usuarios
}

// Overview cuenta lo que hay en el servidor.
func (s *Store) Overview(ctx context.Context) (OverviewCounts, error) {
	var c OverviewCounts
	err := s.Pool.QueryRow(ctx, `with live as (
		  select i.id, i.kind, i.genres, i.provider_ids, i.series_id from media_items i join library_paths lp on lp.id = i.library_path_id
		  join libraries lb on lb.id = lp.library_id where lp.detached_at is null and lb.retired_at is null and i.kind in ('movie', 'episode'))
		select (select count(*) from libraries where retired_at is null),
		  (select count(*) from live l where l.kind = 'movie' and exists (select 1 from media_files f where f.item_id = l.id and f.missing_since is null)),
		  (select count(*) from media_files f join live l on l.id = f.item_id where f.missing_since is null),
		  (select coalesce(sum(f.size_bytes), 0) from media_files f join live l on l.id = f.item_id where f.missing_since is null),
		  (select count(*) from users where not is_disabled),
		  (select count(*) from live where kind = 'movie' and genres is null and provider_ids ? 'tmdb'),
		  (select count(*) from live l where kind = 'movie' and not exists (select 1 from media_files f where f.item_id = l.id)),
		  (select count(distinct l.series_id) from live l where l.kind = 'episode' and exists (select 1 from media_files f where f.item_id = l.id and f.missing_since is null)),
		  (select count(*) from live l where l.kind = 'episode' and exists (select 1 from media_files f where f.item_id = l.id and f.missing_since is null))`).
		Scan(&c.Libraries, &c.Movies, &c.Files, &c.SizeBytes, &c.Users, &c.Genres, &c.Pending, &c.Series, &c.Episodes)
	return c, err
}

// ExtraUpsert es un tráiler o extra encontrado en la carpeta de una película.
type ExtraUpsert struct {
	ItemID, PathID uuid.UUID
	RelPath, Kind  string
	Size           int64
	MTime          time.Time
}

// UpsertExtra registra un extra. Si ese archivo estaba como archivo de la película (escaneos anteriores a E1.3), se
// reclasifica en la misma transacción: sale de media_files, conserva su análisis si no ha cambiado y deja rastro en
// origin_file_id. Así un archivo reclasificado nunca cuenta como desaparecido. Devuelve si hay que analizarlo.
func (s *Store) UpsertExtra(ctx context.Context, e ExtraUpsert) (uuid.UUID, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var needProbe bool
	err = tx.QueryRow(ctx, `insert into media_extras (item_id, library_path_id, rel_path, kind, size_bytes, mtime)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (library_path_id, rel_path) do update set item_id = excluded.item_id, kind = excluded.kind, missing_since = null,
		  probed_at = case when media_extras.size_bytes <> excluded.size_bytes or media_extras.mtime <> excluded.mtime
		                   then null else media_extras.probed_at end,
		  size_bytes = excluded.size_bytes, mtime = excluded.mtime
		returning id, probed_at is null`, e.ItemID, e.PathID, e.RelPath, e.Kind, e.Size, e.MTime).Scan(&id, &needProbe)
	if err != nil {
		return uuid.Nil, false, err
	}
	// Reclasificación: el análisis del archivo se conserva si no ha cambiado (no se vuelve a pasar ffprobe).
	var reused bool
	err = tx.QueryRow(ctx, `with old as (
		  delete from media_files where library_path_id = $2 and rel_path = $3
		  returning id, size_bytes, mtime, container, duration_ms, probe, probed_at)
		update media_extras x set origin_file_id = coalesce(x.origin_file_id, old.id),
		  origin_streams = coalesce(x.origin_streams, (select coalesce(jsonb_agg(to_jsonb(st) - 'id' - 'file_id'), '[]')
		                                               from media_streams st where st.file_id = old.id)),
		  container = case when old.probed_at is not null and old.size_bytes = x.size_bytes and old.mtime = x.mtime then old.container else x.container end,
		  duration_ms = case when old.probed_at is not null and old.size_bytes = x.size_bytes and old.mtime = x.mtime then old.duration_ms else x.duration_ms end,
		  probe = case when old.probed_at is not null and old.size_bytes = x.size_bytes and old.mtime = x.mtime then old.probe else x.probe end,
		  probed_at = case when old.probed_at is not null and old.size_bytes = x.size_bytes and old.mtime = x.mtime then old.probed_at else x.probed_at end
		from old where x.id = $1
		returning x.probed_at is not null`, id, e.PathID, e.RelPath).Scan(&reused)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return uuid.Nil, false, err
	case reused:
		needProbe = false
	}
	return id, needProbe, tx.Commit(ctx)
}

// SaveExtraProbe guarda el análisis de un extra (sin pistas aparte: el reproductor las lee del análisis).
func (s *Store) SaveExtraProbe(ctx context.Context, id uuid.UUID, container string, durationMS int64, raw []byte) error {
	_, err := s.Pool.Exec(ctx, `update media_extras set container = $2, duration_ms = $3, probe = $4, probed_at = now() where id = $1`,
		id, container, durationMS, raw)
	return err
}

// MarkExtrasMissing marca como ausentes los extras de la ruta que este escaneo no ha visto (reaparecen solos si vuelven).
func (s *Store) MarkExtrasMissing(ctx context.Context, pathID uuid.UUID, seen []string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `update media_extras set missing_since = coalesce(missing_since, now())
		where library_path_id = $1 and missing_since is null and not (rel_path = any($2))`, pathID, seen)
	return tag.RowsAffected(), err
}

// ItemForFolder devuelve la ficha ya existente de una carpeta (aunque hoy no tenga película). Sirve para que el tráiler
// de una carpeta sin película se asocie a su ficha (que queda «pendiente») sin crear ninguna nueva.
func (s *Store) ItemForFolder(ctx context.Context, pathID uuid.UUID, folder string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `select id from media_items where library_path_id = $1 and content_path = $2`, pathID, folder).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// PendingItem es una película sin archivo principal (por ejemplo, una carpeta con solo su tráiler): oculta a los
// usuarios hasta que aparezca la película; conserva su identificación y sus imágenes.
type PendingItem struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Year        *int16    `json:"year"`
	Library     string    `json:"library"`
	ContentPath string    `json:"content_path"` // carpeta relativa a la biblioteca (nunca la ruta del servidor)
	Extras      int       `json:"extras"`
	HasPoster   bool      `json:"has_poster"`
}

// PendingItems lista las películas pendientes de archivo principal en bibliotecas y carpetas activas.
func (s *Store) PendingItems(ctx context.Context) ([]PendingItem, error) {
	rows, err := s.Pool.Query(ctx, `select i.id, i.title, i.year, lb.name, i.content_path,
		  (select count(*) from media_extras x where x.item_id = i.id and x.missing_since is null),
		  exists (select 1 from item_images m where m.item_id = i.id and m.kind = 'poster')
		from media_items i join library_paths lp on lp.id = i.library_path_id join libraries lb on lb.id = lp.library_id
		where i.kind in ('movie', 'series') and lp.detached_at is null and lb.retired_at is null
		  and not exists (select 1 from media_files f where f.item_id = i.id)
		  and not exists (select 1 from media_items e join media_files f on f.item_id = e.id where e.series_id = i.id)
		order by i.sort_title, i.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingItem, error) {
		var p PendingItem
		err := r.Scan(&p.ID, &p.Title, &p.Year, &p.Library, &p.ContentPath, &p.Extras, &p.HasPoster)
		return p, err
	})
}

// IsExtra indica si el id es un tráiler o extra (el progreso de los extras se rechaza).
func (s *Store) IsExtra(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from media_extras where id = $1)`, id).Scan(&ok)
	return ok, err
}

// CountPendingMetadata cuenta las películas de la biblioteca a las que les falta la ficha de TMDB o los géneros (para el
// total de la fase «metadata» del escaneo).
func (s *Store) CountPendingMetadata(ctx context.Context, libraryID uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) filter (where metadata_refreshed_at is null and not locked)
		  + count(*) filter (where genres is null and provider_ids ? 'tmdb' and not locked)
		from media_items where library_id = $1 and kind in ('movie', 'series')`, libraryID).Scan(&n)
	return n, err
}

// EditableMetadata son los datos de una ficha que el administrador puede corregir a mano (I1), con sus candados.
type EditableMetadata struct {
	Title          string            `json:"title"`
	SortTitle      string            `json:"sort_title"`
	OriginalTitle  string            `json:"original_title"`
	Year           *int              `json:"year"`
	Tagline        string            `json:"tagline"`
	Overview       string            `json:"overview"`
	OfficialRating string            `json:"official_rating"`
	RatingAge      *int              `json:"rating_age"`
	Genres         []string          `json:"genres"`
	LockedFields   []string          `json:"locked_fields"`
	Locked         bool              `json:"locked"`
	ProviderIDs    map[string]string `json:"provider_ids"`
	Kind           string            `json:"kind,omitempty"`
	MatchSource    string            `json:"match_source,omitempty"` // auto o manual (identificada por un administrador)
	CanUndo        bool              `json:"can_undo,omitempty"`     // hay una instantánea a la que volver
}

// EditableFields son los nombres de campo que se pueden bloquear (los mismos que respeta ApplyMetadata).
var EditableFields = []string{"title", "sort_title", "original_title", "year", "tagline", "overview", "official_rating", "genres"}

// adminItemKinds son los elementos con ficha editable (temporadas y episodios también: sus datos y miniaturas son suyos).
const adminItemKinds = `('movie', 'series', 'season', 'episode')`

// GetEditableMetadata lee los datos editables de una ficha (de cualquier biblioteca: solo para administradores).
func (s *Store) GetEditableMetadata(ctx context.Context, id uuid.UUID) (EditableMetadata, error) {
	var m EditableMetadata
	var year, age *int16
	err := s.Pool.QueryRow(ctx, `select title, sort_title, coalesce(original_title, ''), year, coalesce(tagline, ''),
		coalesce(overview, ''), coalesce(official_rating, ''), rating_age, coalesce(genres, '{}'), locked_fields, locked, provider_ids,
		kind, match_source, exists (select 1 from metadata_snapshots n where n.item_id = media_items.id)
		from media_items where id = $1 and kind in `+adminItemKinds, id).
		Scan(&m.Title, &m.SortTitle, &m.OriginalTitle, &year, &m.Tagline, &m.Overview, &m.OfficialRating, &age, &m.Genres,
			&m.LockedFields, &m.Locked, &m.ProviderIDs, &m.Kind, &m.MatchSource, &m.CanUndo)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	if year != nil {
		y := int(*year)
		m.Year = &y
	}
	if age != nil {
		a := int(*age)
		m.RatingAge = &a
	}
	return m, err
}

// SaveEditedMetadata guarda la edición manual en una transacción. Los campos que cambian quedan bloqueados (no los
// vuelve a cambiar ni TMDB ni el escaneo), además de los que el administrador bloquee a mano. Devuelve los nombres de
// los campos cambiados (para la auditoría; nunca su contenido).
func (s *Store) SaveEditedMetadata(ctx context.Context, id uuid.UUID, m EditableMetadata) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var cur EditableMetadata
	var year, age *int16
	err = tx.QueryRow(ctx, `select title, sort_title, coalesce(original_title, ''), year, coalesce(tagline, ''), coalesce(overview, ''),
		coalesce(official_rating, ''), rating_age, coalesce(genres, '{}') from media_items where id = $1 and kind in `+adminItemKinds+` for update`, id).
		Scan(&cur.Title, &cur.SortTitle, &cur.OriginalTitle, &year, &cur.Tagline, &cur.Overview, &cur.OfficialRating, &age, &cur.Genres)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	same := func(a *int16, c *int) bool {
		if a == nil || c == nil {
			return a == nil && c == nil
		}
		return int(*a) == *c
	}
	var changed []string
	add := func(name string, diff bool) {
		if diff {
			changed = append(changed, name)
		}
	}
	add("title", cur.Title != m.Title)
	add("sort_title", cur.SortTitle != m.SortTitle)
	add("original_title", cur.OriginalTitle != m.OriginalTitle)
	add("year", !same(year, m.Year))
	add("tagline", cur.Tagline != m.Tagline)
	add("overview", cur.Overview != m.Overview)
	add("official_rating", cur.OfficialRating != m.OfficialRating || !same(age, m.RatingAge))
	add("genres", strings.Join(cur.Genres, "\x00") != strings.Join(m.Genres, "\x00"))
	locks := map[string]bool{}
	for _, f := range append(append([]string{}, m.LockedFields...), changed...) {
		locks[f] = true
	}
	var lockList []string
	for _, f := range EditableFields {
		if locks[f] {
			lockList = append(lockList, f)
		}
	}
	_, err = tx.Exec(ctx, `update media_items set title = $2, sort_title = $3, original_title = nullif($4, ''), year = $5,
		tagline = nullif($6, ''), overview = nullif($7, ''), official_rating = nullif($8, ''), rating_age = $9, genres = $10,
		locked_fields = $11, locked = $12 where id = $1`,
		id, m.Title, m.SortTitle, m.OriginalTitle, m.Year, m.Tagline, m.Overview, m.OfficialRating, m.RatingAge,
		nonNilStrings(m.Genres), nonNilStrings(lockList), m.Locked)
	if err != nil {
		return nil, err
	}
	return changed, tx.Commit(ctx)
}

// ItemForRefresh devuelve lo necesario para volver a pedir la ficha a TMDB (I1, «Actualizar información»).
func (s *Store) ItemForRefresh(ctx context.Context, id uuid.UUID) (PendingMetadata, bool, error) {
	var p PendingMetadata
	var locked bool
	err := s.Pool.QueryRow(ctx, `select id, kind, title, year, provider_ids, locked from media_items where id = $1 and kind in ('movie', 'series')`, id).
		Scan(&p.ID, &p.Kind, &p.Title, &p.Year, &p.ProviderIDs, &locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, ErrNotFound
	}
	return p, locked, err
}

// HasImage indica si la película ya tiene una imagen de ese tipo y origen.
func (s *Store) HasImage(ctx context.Context, itemID uuid.UUID, kind, source string) bool {
	var ok bool
	_ = s.Pool.QueryRow(ctx, `select exists (select 1 from item_images where item_id = $1 and kind = $2 and source = $3)`, itemID, kind, source).Scan(&ok)
	return ok
}

// AdminFile es un archivo de una película para «Ver archivos» (rutas relativas a la carpeta de la biblioteca; nunca la
// ruta del servidor).
type AdminFile struct {
	Library    string  `json:"library"`
	RelPath    string  `json:"rel_path"`
	Kind       string  `json:"kind"` // película, o el tipo de extra
	SizeBytes  int64   `json:"size_bytes"`
	Container  *string `json:"container"`
	DurationMS *int64  `json:"duration_ms"`
	Missing    bool    `json:"missing"`
	Remote     bool    `json:"remote"`
}

// AdminItemFiles lista los archivos principales y los extras de una película.
func (s *Store) AdminItemFiles(ctx context.Context, id uuid.UUID) ([]AdminFile, error) {
	rows, err := s.Pool.Query(ctx, `select * from (
		  select lb.name, f.rel_path, 'movie' as kind, f.size_bytes, f.container, f.duration_ms, f.missing_since is not null, lp.kind = 'remote_fuse'
		  from media_files f join library_paths lp on lp.id = f.library_path_id join libraries lb on lb.id = lp.library_id where f.item_id = $1
		  union all
		  select lb.name, x.rel_path, x.kind, x.size_bytes, x.container, x.duration_ms, x.missing_since is not null, lp.kind = 'remote_fuse'
		  from media_extras x join library_paths lp on lp.id = x.library_path_id join libraries lb on lb.id = lp.library_id where x.item_id = $1
		) u order by (u.kind = 'movie') desc, u.rel_path`, id)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AdminFile, error) {
		var f AdminFile
		err := r.Scan(&f.Library, &f.RelPath, &f.Kind, &f.SizeBytes, &f.Container, &f.DurationMS, &f.Missing, &f.Remote)
		return f, err
	})
	if out == nil {
		out = []AdminFile{}
	}
	return out, err
}

func presentationJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}
