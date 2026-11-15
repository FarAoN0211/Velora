package store

// E1.3 I2 · Identificación manual e imágenes propias. Todo esto cambia solo los metadatos de la ficha: nunca archivos,
// rutas, media_files ni el progreso de los usuarios (user_item_data). Una identificación se aplica en una sola
// transacción, con una instantánea previa para «Deshacer» (las 5 últimas por ficha).

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"velora/internal/db"
)

var (
	// ErrItemLocked: la ficha está bloqueada entera; hay que desbloquearla antes de identificarla.
	ErrItemLocked = errors.New("la ficha está bloqueada")
	// ErrNoSnapshot: no hay nada que deshacer.
	ErrNoSnapshot = errors.New("no hay nada que deshacer")
)

// ItemRef es lo necesario para administrar una ficha.
type ItemRef struct {
	ID          uuid.UUID
	Kind        string
	ParentID    *uuid.UUID
	ProviderIDs map[string]string
	Locked      bool
	ContentPath string
}

// ItemRefByID lee una ficha administrable.
func (s *Store) ItemRefByID(ctx context.Context, id uuid.UUID) (ItemRef, error) {
	var r ItemRef
	err := s.Pool.QueryRow(ctx, `select id, kind, parent_id, provider_ids, locked, content_path from media_items where id = $1 and kind in `+adminItemKinds, id).
		Scan(&r.ID, &r.Kind, &r.ParentID, &r.ProviderIDs, &r.Locked, &r.ContentPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// TMDBIDsInLibrary indica qué IDs de TMDB ya tiene otra ficha del mismo tipo (aviso «Ya en tu biblioteca»: no impide
// elegirlo, porque puede ser justo el duplicado que se quiere).
func (s *Store) TMDBIDsInLibrary(ctx context.Context, kind string, except uuid.UUID, ids []string) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `select distinct provider_ids->>'tmdb' from media_items where kind = $1 and id <> $2 and provider_ids->>'tmdb' = any($3)`, kind, except, ids)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, id := range found {
		out[id] = true
	}
	return out, nil
}

// snapshot guarda en la transacción lo que hay ahora: campos, identificación, bloqueos y las imágenes de los orígenes
// que la operación va a cambiar. Bloquea la fila hasta el final de la transacción, también frente a escaneos y trabajos.
func snapshot(ctx context.Context, tx pgx.Tx, id uuid.UUID, by *uuid.UUID, reason string, sources []string) error {
	tag, err := tx.Exec(ctx, `insert into metadata_snapshots (item_id, created_by, reason, data)
		select i.id, $2, $3, jsonb_build_object(
		  'item', jsonb_build_object('title', i.title, 'sort_title', i.sort_title, 'original_title', i.original_title, 'year', i.year,
		    'overview', i.overview, 'tagline', i.tagline, 'runtime_ms', i.runtime_ms, 'official_rating', i.official_rating,
		    'presentation', i.presentation, 'rating_age', i.rating_age, 'community_rating', i.community_rating, 'genres', i.genres, 'locked_fields', i.locked_fields,
		    'locked', i.locked, 'provider_ids', i.provider_ids, 'match_source', i.match_source, 'metadata_refreshed_at', i.metadata_refreshed_at),
		  'image_sources', to_jsonb($4::text[]),
		  'images', coalesce((select jsonb_agg(jsonb_build_object('kind', m.kind, 'source', m.source, 'path', m.path))
		                      from item_images m where m.item_id = i.id and m.source = any($4)), '[]'::jsonb))
		from media_items i where i.id = $1`, id, by, reason, sources)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = tx.Exec(ctx, `delete from metadata_snapshots where item_id = $1 and id not in
		(select id from metadata_snapshots where item_id = $1 order by id desc limit 5)`, id)
	return err
}

// IdentifyInput es una identificación ya descargada: Images (tipo → ruta bajo /config/metadata) solo cuenta con ReplaceImages.
type IdentifyInput struct {
	Fields        MetadataFields
	SortTitle     string
	ReplaceLocked bool
	ReplaceImages bool
	Images        map[string]string
	By            uuid.UUID
}

// ApplyIdentification aplica la película o serie elegida. Los campos que TMDB no trae se vacían (son de la ficha
// equivocada), salvo los bloqueados, que se conservan si no se pide lo contrario. Las imágenes elegidas a mano no se tocan.
func (s *Store) ApplyIdentification(ctx context.Context, id uuid.UUID, in IdentifyInput) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var locked bool
	err = tx.QueryRow(ctx, `select locked from media_items where id = $1 and kind in ('movie', 'series') for update`, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if locked {
		return ErrItemLocked
	}
	sources := []string{}
	if in.ReplaceImages {
		sources = []string{"tmdb", "fanart", "tvdb", "omdb"}
	}
	if err := snapshot(ctx, tx, id, &in.By, "identify", sources); err != nil {
		return err
	}
	m := in.Fields
	ids, _ := json.Marshal(m.ProviderIDs)
	_, err = tx.Exec(ctx, `update media_items set
		title = case when (not $13 and 'title' = any(locked_fields)) or $2 = '' then title else $2 end,
		sort_title = case when (not $13 and ('title' = any(locked_fields) or 'sort_title' = any(locked_fields))) or $2 = '' then sort_title else $3 end,
		original_title = case when not $13 and 'original_title' = any(locked_fields) then original_title else nullif($4, '') end,
		overview = case when not $13 and 'overview' = any(locked_fields) then overview else nullif($5, '') end,
		tagline = case when not $13 and 'tagline' = any(locked_fields) then tagline else nullif($6, '') end,
		year = case when not $13 and 'year' = any(locked_fields) then year else nullif($7::int, 0) end,
		runtime_ms = nullif($8::bigint, 0),
		official_rating = case when not $13 and 'official_rating' = any(locked_fields) then official_rating else nullif($9, '') end,
		rating_age = case when not $13 and 'official_rating' = any(locked_fields) then rating_age else $10 end,
		community_rating = nullif($11::numeric, 0),
		genres = case when not $13 and 'genres' = any(locked_fields) then genres else $12 end,
		-- La identificación anterior no se mezcla con la nueva (un IMDb antiguo sería de otra película).
		provider_ids = (provider_ids - 'tmdb' - 'imdb' - 'tvdb') || $14,
		presentation = $15::jsonb, match_source = 'manual', metadata_refreshed_at = now()
		where id = $1`, id, m.Title, in.SortTitle, m.OriginalTitle, m.Overview, m.Tagline, m.Year, m.RuntimeMS, m.OfficialRating,
		m.RatingAge, m.CommunityRating, nonNilStrings(m.Genres), in.ReplaceLocked, ids, presentationJSON(m.Presentation))
	if err != nil {
		return err
	}
	if in.ReplaceImages {
		if _, err := tx.Exec(ctx, `delete from item_images where item_id = $1 and source in ('tmdb','fanart','tvdb','omdb')`, id); err != nil {
			return err
		}
		for kind, rel := range in.Images {
			if _, err := tx.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, $2, 'tmdb', $3)`, id, kind, rel); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// UndoIdentification vuelve a la última instantánea y la consume. Devuelve los archivos de las imágenes que deja de
// usar, para que se borren si ya nada los necesita.
func (s *Store) UndoIdentification(ctx context.Context, id uuid.UUID) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var kind string
	err = tx.QueryRow(ctx, `select kind from media_items where id = $1 and kind in `+adminItemKinds+` for update`, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var snapID int64
	var data []byte
	err = tx.QueryRow(ctx, `select id, data from metadata_snapshots where item_id = $1 order by id desc limit 1`, id).Scan(&snapID, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSnapshot
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `update media_items i set title = r.title, sort_title = r.sort_title, original_title = r.original_title, year = r.year,
		overview = r.overview, tagline = r.tagline, runtime_ms = r.runtime_ms, official_rating = r.official_rating, rating_age = r.rating_age,
		presentation = coalesce(r.presentation, '{}'::jsonb), community_rating = r.community_rating, genres = r.genres, locked_fields = r.locked_fields, locked = r.locked,
		provider_ids = r.provider_ids, match_source = r.match_source, metadata_refreshed_at = r.metadata_refreshed_at
		from jsonb_populate_record(null::media_items, ($2::jsonb)->'item') r where i.id = $1`, id, data)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `delete from item_images where item_id = $1 and source in (select jsonb_array_elements_text(($2::jsonb)->'image_sources')) returning path`, id, data)
	if err != nil {
		return nil, err
	}
	dropped, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `insert into item_images (item_id, kind, source, path)
		select $1, x.kind, x.source, x.path from jsonb_to_recordset(($2::jsonb)->'images') as x(kind text, source text, path text)`, id, data)
	if err != nil {
		return nil, err
	}
	if err := touchImages(ctx, tx, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `delete from metadata_snapshots where id = $1`, snapID); err != nil {
		return nil, err
	}
	return dropped, tx.Commit(ctx)
}

// ResetInput es lo que se deduce del nombre de la carpeta al volver a la identificación automática.
type ResetInput struct {
	Title, SortTitle string
	Year             int
	ProviderIDs      map[string]string
	RestoreImages    bool
	By               uuid.UUID
}

// ResetIdentification vuelve a la identificación automática: quita la elección manual y los candados y deja la ficha
// como la vería un escaneo nuevo (lo siguiente que la actualice la completará). Con «Deshacer» se recupera todo.
func (s *Store) ResetIdentification(ctx context.Context, id uuid.UUID, in ResetInput) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var kind string
	err = tx.QueryRow(ctx, `select kind from media_items where id = $1 and kind in ('movie', 'series') for update`, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	sources := []string{"tmdb", "fanart", "tvdb", "omdb"}
	if in.RestoreImages {
		sources = append(sources, "user")
	}
	if err := snapshot(ctx, tx, id, &in.By, "reset", sources); err != nil {
		return err
	}
	ids, _ := json.Marshal(in.ProviderIDs)
	_, err = tx.Exec(ctx, `update media_items set title = case when $2 = '' then title else $2 end,
		sort_title = case when $2 = '' then sort_title else $3 end, year = nullif($4::int, 0),
		original_title = null, overview = null, tagline = null, runtime_ms = null, official_rating = null, rating_age = null,
		presentation = '{}'::jsonb, community_rating = null, genres = null, locked = false, locked_fields = '{}', provider_ids = $5, match_source = 'auto',
		metadata_refreshed_at = null where id = $1`, id, in.Title, in.SortTitle, in.Year, ids)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `delete from item_images where item_id = $1 and source = any($2)`, id, sources); err != nil {
		return err
	}
	if err := touchImages(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// touchImages da versión nueva a las imágenes de una ficha cuando desaparece la que se mostraba: la que vuelve a verse
// tiene una fecha antigua y las URL podrían coincidir con otra ya guardada en la caché del navegador.
func touchImages(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `update item_images set updated_at = now() where item_id = $1`, id)
	return err
}

// SetProviderImage publica una imagen de TMDB solo si la ficha sigue identificada con ese ID. Bloquea primero la ficha,
// igual que ApplyIdentification y UndoIdentification: comprobar y publicar es un único paso frente a ellas, y un trabajo
// en curso no puede colar la imagen de la película anterior. Devuelve si se publicó y el archivo que sustituye.
func (s *Store) SetProviderImage(ctx context.Context, itemID uuid.UUID, kind, path, tmdbID string) (bool, string, error) {
	return s.SetArtworkImage(ctx, itemID, kind, path, tmdbID, "tmdb")
}

func (s *Store) SetArtworkImage(ctx context.Context, itemID uuid.UUID, kind, path, tmdbID, source string) (bool, string, error) {
	if source != "tmdb" && source != "fanart" && source != "tvdb" && source != "omdb" {
		return false, "", ErrNotFound
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx)
	var current string
	err = tx.QueryRow(ctx, `select coalesce(provider_ids->>'tmdb', '') from media_items where id = $1 for update`, itemID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current != tmdbID) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if kind == "logo" {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from item_images where item_id=$1 and kind='logo')`, itemID).Scan(&exists); err != nil {
			return false, "", err
		}
		if exists {
			return false, "", nil
		}
	}
	var old string
	err = tx.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = $2 and source = $3`, itemID, kind, source).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	_, err = tx.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, $2, $4, $3)
		on conflict (item_id, kind, source) do update set path = excluded.path, updated_at = now()`, itemID, kind, path, source)
	if err != nil {
		return false, "", err
	}
	return true, old, tx.Commit(ctx)
}

// ImageSlot es de dónde sale ahora cada tipo de imagen de una ficha.
type ImageSlot struct {
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"` // user, local, tmdb…; vacío si no hay
}

// ImageSources devuelve el origen que se muestra de cada tipo de imagen y la versión de las imágenes de la ficha.
func (s *Store) ImageSources(ctx context.Context, id uuid.UUID) (map[string]string, string, error) {
	rows, err := s.Pool.Query(ctx, `select distinct on (kind) kind, source from item_images where item_id = $1
		order by kind, case source when 'user' then 0 when 'local' then 1 else 2 end`, id)
	if err != nil {
		return nil, "", err
	}
	out := map[string]string{}
	var k, src string
	if _, err := pgx.ForEachRow(rows, []any{&k, &src}, func() error { out[k] = src; return nil }); err != nil {
		return nil, "", err
	}
	var rev string
	err = s.Pool.QueryRow(ctx, `select coalesce((extract(epoch from max(updated_at)) * 1000000)::bigint::text, '') from item_images where item_id = $1`, id).Scan(&rev)
	return out, rev, err
}

// CustomImageKinds lista los tipos con imagen elegida a mano.
func (s *Store) CustomImageKinds(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `select kind from item_images where item_id = $1 and source = 'user' order by kind`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// SetUserImage guarda la elección manual de un tipo de imagen; devuelve el archivo que tenía antes, si había.
func (s *Store) SetUserImage(ctx context.Context, id uuid.UUID, kind, path string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select 1 from media_items where id = $1 for update`, id); err != nil {
		return "", err
	}
	var old string
	err = tx.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = $2 and source = 'user'`, id, kind).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, $2, 'user', $3)
		on conflict (item_id, kind, source) do update set path = excluded.path, updated_at = now()`, id, kind, path)
	if err != nil {
		return "", err
	}
	return old, tx.Commit(ctx)
}

// DeleteUserImage quita la elección manual (vuelve a verse la automática) y devuelve su archivo; ErrNotFound si no hay.
func (s *Store) DeleteUserImage(ctx context.Context, id uuid.UUID, kind string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select 1 from media_items where id = $1 for update`, id); err != nil {
		return "", err
	}
	var path string
	err = tx.QueryRow(ctx, `delete from item_images where item_id = $1 and kind = $2 and source = 'user' returning path`, id, kind).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if err := touchImages(ctx, tx, id); err != nil {
		return "", err
	}
	return path, tx.Commit(ctx)
}

// ImageFileInUse indica si un archivo sigue en uso: como imagen de la ficha o en alguna instantánea («Deshacer» lo necesitaría).
func (s *Store) ImageFileInUse(ctx context.Context, id uuid.UUID, path string) bool {
	var used bool
	err := s.Pool.QueryRow(ctx, `select exists (select 1 from item_images where item_id = $1 and path = $3 and source <> 'local')
		or exists (select 1 from metadata_snapshots where item_id = $1 and position($2 in data::text) > 0)`, id, jsonPath(path), path).Scan(&used)
	return err != nil || used // ante la duda, no se borra
}

// RemoveImageFiles borra de metadataDir los archivos que ya no usa ni la ficha ni ninguna instantánea. Con os.Root: no se
// puede salir de ese directorio, y los medios nunca pasan por aquí. Durante una copia de seguridad no borra nada, porque esa
// copia puede necesitarlos; un archivo que quede es inofensivo.
func (s *Store) RemoveImageFiles(ctx context.Context, id uuid.UUID, metadataDir string, rels ...string) {
	if len(rels) == 0 {
		return
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var free bool
	if err := tx.QueryRow(ctx, `select pg_try_advisory_xact_lock_shared($1)`, db.ImageFilesLock).Scan(&free); err != nil || !free {
		slog.Info("archivos de imagen sin borrar: hay una copia de seguridad en curso", "cuantos", len(rels))
		return
	}
	root, err := os.OpenRoot(metadataDir)
	if err != nil {
		return
	}
	defer root.Close()
	for _, rel := range rels {
		if !s.ImageFileInUse(ctx, id, rel) {
			_ = root.Remove(rel)
		}
	}
}

func jsonPath(p string) string { return strings.Trim(mustJSON(p), `"`) }

func mustJSON(v string) string { b, _ := json.Marshal(v); return string(b) }
