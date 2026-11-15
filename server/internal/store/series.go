package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"velora/internal/media"
)

// E2 S1: series, temporadas y episodios (replica/design/screens.md S06; components.md EpisodeList y NextUpCard).

// ChildUpsert es una temporada o un episodio deducido por el escáner.
type ChildUpsert struct {
	LibraryID, PathID  uuid.UUID
	SeriesID, ParentID uuid.UUID
	Kind               string // season o episode
	ContentPath        string // clave sintética: «Serie/#S01» o «Serie/#S01E02»
	Title, SortTitle   string
	Number             *int // nº de temporada o de episodio (nil: episodio sin número)
	NumberEnd          *int // último episodio de un multiepisodio
}

// UpsertChild crea o actualiza una temporada o un episodio. El título del nombre del archivo solo se usa mientras no
// haya ficha de TMDB ni candado: nunca pisa un título enriquecido o corregido a mano.
func (s *Store) UpsertChild(ctx context.Context, c ChildUpsert) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, content_path,
		  parent_id, series_id, index_number, index_number_end)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		on conflict (library_path_id, content_path) do update set
		  parent_id = excluded.parent_id, series_id = excluded.series_id,
		  index_number = excluded.index_number, index_number_end = excluded.index_number_end,
		  title = case when media_items.locked or 'title' = any(media_items.locked_fields) or media_items.metadata_refreshed_at is not null
		               then media_items.title else excluded.title end,
		  sort_title = case when media_items.locked or 'title' = any(media_items.locked_fields) or 'sort_title' = any(media_items.locked_fields)
		               then media_items.sort_title else excluded.sort_title end
		returning id`, c.LibraryID, c.PathID, c.Kind, c.Title, c.SortTitle, c.ContentPath, c.ParentID, c.SeriesID, c.Number, c.NumberEnd).Scan(&id)
	return id, err
}

// SeriesDetail es lo que la ficha añade según el tipo: temporadas y «siguiente» en una serie, episodios en una
// temporada y el episodio siguiente en un episodio.
type SeriesDetail struct {
	Seasons  []SeasonInfo  `json:"seasons,omitempty"`
	NextUp   *ItemCard     `json:"next_up,omitempty"`      // serie: el episodio que toca ver (o continuar)
	Episodes []EpisodeInfo `json:"episodes,omitempty"`     // temporada
	Next     *ItemCard     `json:"next_episode,omitempty"` // episodio: el siguiente en orden (sin especiales)
}

// SeasonInfo es una temporada en la ficha de su serie.
type SeasonInfo struct {
	ItemCard
	Overview      *string `json:"overview"`
	EpisodeCount  int     `json:"episode_count"`
	UnplayedCount int     `json:"unplayed_count"`
}

// EpisodeInfo es un episodio en la lista de su temporada.
type EpisodeInfo struct {
	ItemCard
	Overview *string    `json:"overview"`
	FileID   *uuid.UUID `json:"file_id"` // el archivo que se reproduce por defecto (el mayor presente)
	// Tech: insignias técnicas de ese archivo (véase FileInfo.Tech); vacía si no hay archivo o no se ha analizado.
	Tech []media.TechBadge `json:"tech"`
}

// episodeOrder ordena los episodios de una serie: temporadas en orden, especiales (temporada 0) al final, los episodios
// sin número al final de su temporada. sn es la temporada de e.
const episodeOrder = `(sn.index_number = 0), sn.index_number, coalesce(e.index_number, 1000000), e.sort_title, e.id`

func (s *Store) seriesDetail(ctx context.Context, v Viewer, d *ItemDetail) error {
	var err error
	switch d.Kind {
	case "series":
		if d.Seasons, err = s.seasons(ctx, v, d.ID); err != nil {
			return err
		}
		d.NextUp, err = s.NextUp(ctx, v, d.ID)
	case "season":
		d.Episodes, err = s.episodes(ctx, v, d.ID)
	case "episode":
		if d.SeriesID != nil {
			d.Next, err = s.nextAfter(ctx, v, *d.SeriesID, d.ID, false)
		}
	}
	return err
}

func (s *Store) seasons(ctx context.Context, v Viewer, seriesID uuid.UUID) ([]SeasonInfo, error) {
	rows, err := s.Pool.Query(ctx, `select `+cardCols+`, i.overview,
		  (select count(*) from media_items e where e.parent_id = i.id and e.kind = 'episode' and `+present("e")+`),
		  (select count(*) from media_items e where e.parent_id = i.id and e.kind = 'episode' and `+present("e")+`
		     and not exists (select 1 from user_item_data ud where ud.item_id = e.id and ud.user_id = $1 and ud.played))
		from media_items i left join lateral effective_item_data($1,i.id) d on true
		where i.parent_id = $2 and i.kind = 'season' and `+present("i")+` and `+visible("i", 1, 3)+`
		order by (i.index_number = 0), i.index_number, i.id`, v.UserID, seriesID, v.Admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SeasonInfo, error) {
		var si SeasonInfo
		err := r.Scan(append(cardDest(&si.ItemCard), &si.Overview, &si.EpisodeCount, &si.UnplayedCount)...)
		return si, err
	})
}

func (s *Store) episodes(ctx context.Context, v Viewer, seasonID uuid.UUID) ([]EpisodeInfo, error) {
	rows, err := s.Pool.Query(ctx, `select `+cardCols+`, i.overview, pf.id, pf.probe, pf.rel_path
		from media_items i left join lateral effective_item_data($1,i.id) d on true
		left join lateral (select f.id, f.probe, f.rel_path from media_files f where f.item_id = i.id and f.missing_since is null
		  order by `+episodeAudioRank+` desc, f.size_bytes desc, f.id limit 1) pf on true
		where i.parent_id = $2 and i.kind = 'episode' and `+present("i")+` and `+visible("i", 1, 3)+`
		order by coalesce(i.index_number, 1000000), i.sort_title, i.id`, v.UserID, seasonID, v.Admin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (EpisodeInfo, error) {
		var e EpisodeInfo
		var probe []byte
		var rel *string
		err := r.Scan(append(cardDest(&e.ItemCard), &e.Overview, &e.FileID, &probe, &rel)...)
		e.Tech = []media.TechBadge{}
		if err == nil && rel != nil {
			e.Tech = media.TechBadgesJSON(probe, *rel)
		}
		return e, err
	})
}

// card devuelve la tarjeta de un elemento visible.
func (s *Store) card(ctx context.Context, v Viewer, id uuid.UUID) (*ItemCard, error) {
	var c ItemCard
	err := s.Pool.QueryRow(ctx, `select `+cardCols+` from media_items i left join lateral effective_item_data($1,i.id) d on true
		where i.id = $2 and `+visible("i", 1, 3), v.UserID, id, v.Admin).Scan(cardDest(&c)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// nextAfter es el episodio que sigue a current en la serie (con archivo presente). Tras un episodio normal nunca se
// pasa a los especiales; tras un especial, solo al especial siguiente. unplayed: salta los que el usuario ya ha visto.
func (s *Store) nextAfter(ctx context.Context, v Viewer, seriesID, current uuid.UUID, unplayed bool) (*ItemCard, error) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `with cur as (
		  select e.id, sn.index_number s, coalesce(e.index_number, 1000000) n, e.sort_title t
		  from media_items e join media_items sn on sn.id = e.parent_id where e.id = $2)
		select e.id from media_items e join media_items sn on sn.id = e.parent_id, cur
		where e.series_id = $1 and e.kind = 'episode' and `+present("e")+`
		  and (sn.index_number <> 0 or cur.s = 0)
		  and ((sn.index_number = 0), sn.index_number, coalesce(e.index_number, 1000000), e.sort_title, e.id)
		      > ((cur.s = 0), cur.s, cur.n, cur.t, cur.id)
		  and (not $4 or not exists (select 1 from user_item_data ud where ud.item_id = e.id and ud.user_id = $3 and ud.played))
		order by `+episodeOrder+` limit 1`, seriesID, current, v.UserID, unplayed).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.card(ctx, v, id)
}

// NextUp es el episodio que toca en una serie (el botón «Siguiente: T2·E5» de la ficha): el que está a medias más
// reciente; si no, el siguiente sin ver tras el último visto; si no, el primero sin ver; y si ya se ha visto todo, el
// primero. nil si la serie no tiene episodios presentes.
func (s *Store) NextUp(ctx context.Context, v Viewer, seriesID uuid.UUID) (*ItemCard, error) {
	var id uuid.UUID
	var played bool
	err := s.Pool.QueryRow(ctx, `select e.id, d.played from user_item_data d join media_items e on e.id = d.item_id
		where d.user_id = $1 and e.series_id = $2 and e.kind = 'episode' and d.last_played_at is not null and `+present("e")+`
		  and (d.played or d.position_ms > 0)
		order by d.last_played_at desc limit 1`, v.UserID, seriesID).Scan(&id, &played)
	switch {
	case err == nil && !played:
		return s.card(ctx, v, id)
	case err == nil:
		if next, err := s.nextAfter(ctx, v, seriesID, id, true); err != nil || next != nil {
			return next, err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	// El primero sin ver (sin especiales si hay episodios normales) y, si está todo visto, el primero.
	err = s.Pool.QueryRow(ctx, `select e.id from media_items e join media_items sn on sn.id = e.parent_id
		where e.series_id = $1 and e.kind = 'episode' and `+present("e")+`
		order by exists (select 1 from user_item_data ud where ud.item_id = e.id and ud.user_id = $2 and ud.played), `+episodeOrder+` limit 1`,
		seriesID, v.UserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.card(ctx, v, id)
}

// NextUpEpisodes incluye historial reproducido y marcado/importado como visto, aunque
// este último no tenga last_played_at. Primero encuentra un siguiente disponible y
// después limita la fila: las series ya terminadas no consumen plazas.
func (s *Store) NextUpEpisodes(ctx context.Context, v Viewer, limit int) ([]ItemCard, error) {
	rows, err := s.Pool.Query(ctx, `with latest as (
   select distinct on(e.series_id) e.series_id,e.id,d.played,d.continue_hidden_at,
     sn.index_number season_number,coalesce(e.index_number,1000000) episode_number,e.sort_title,
     coalesce(d.client_updated_at,d.last_played_at,e.added_at) activity
   from user_item_data d join media_items e on e.id=d.item_id
   join media_items sn on sn.id=e.parent_id
   where d.user_id=$1 and e.kind='episode' and (d.played or d.position_ms>0)
   order by e.series_id,coalesce(d.client_updated_at,d.last_played_at,'-infinity'::timestamptz) desc,
     (sn.index_number=0),sn.index_number desc,coalesce(e.index_number,1000000) desc,e.sort_title desc,e.id desc
 )
 select `+cardCols+` from latest l
 join lateral (
   select e.id from media_items e join media_items sn on sn.id=e.parent_id
   where e.series_id=l.series_id and e.kind='episode' and `+present("e")+`
     and (sn.index_number<>0 or l.season_number=0)
     and ((sn.index_number=0),sn.index_number,coalesce(e.index_number,1000000),e.sort_title,e.id)
       > ((l.season_number=0),l.season_number,l.episode_number,l.sort_title,l.id)
     and not exists(select 1 from user_item_data ud where ud.user_id=$1 and ud.item_id=e.id and ud.played)
   order by `+episodeOrder+` limit 1
 ) nx on true
 join media_items i on i.id=nx.id
 left join lateral effective_item_data($1,i.id) d on true
 where l.played and l.continue_hidden_at is null and coalesce(d.position_ms,0)=0 and `+visible("i", 1, 3)+`
 order by greatest(l.activity,i.added_at) desc,i.id limit $2`, v.UserID, limit, v.Admin)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

// PendingSeason es una temporada a la que le falta la ficha de TMDB de su serie actual: nunca la tuvo, o la serie ha
// cambiado de identificación desde entonces (I2) y sus datos ya no le corresponden.
type PendingSeason struct {
	ID, SeriesID uuid.UUID
	SeriesTitle  string
	SeriesTMDB   string
	Number       int
}

const seasonPendingWhere = `s.kind = 'season' and not s.locked and sr.provider_ids ? 'tmdb'
	and (s.metadata_refreshed_at is null or coalesce(s.provider_ids->>'tmdb_series', '') <> sr.provider_ids->>'tmdb')`

// SeasonsNeedingMetadata devuelve temporadas pendientes de una biblioteca y/o de una serie (nil: sin ese filtro).
func (s *Store) SeasonsNeedingMetadata(ctx context.Context, libraryID, series *uuid.UUID, limit int) ([]PendingSeason, error) {
	rows, err := s.Pool.Query(ctx, `select s.id, sr.id, sr.title, sr.provider_ids->>'tmdb', coalesce(s.index_number, 0)
		from media_items s join media_items sr on sr.id = s.series_id
		where ($1::uuid is null or s.library_id = $1) and ($2::uuid is null or s.series_id = $2) and `+seasonPendingWhere+`
		order by sr.added_at, s.index_number limit $3`, libraryID, series, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingSeason, error) {
		var p PendingSeason
		err := r.Scan(&p.ID, &p.SeriesID, &p.SeriesTitle, &p.SeriesTMDB, &p.Number)
		return p, err
	})
}

// CountPendingSeasons cuenta las temporadas pendientes de la biblioteca (para la barra de la fase «metadata»).
func (s *Store) CountPendingSeasons(ctx context.Context, libraryID uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `select count(*) from media_items s join media_items sr on sr.id = s.series_id
		where s.library_id = $1 and `+seasonPendingWhere, libraryID).Scan(&n)
	return n, err
}

// ResetSeasonMetadata hace que las temporadas de una serie vuelvan a pedir su ficha («Actualizar» de la serie).
func (s *Store) ResetSeasonMetadata(ctx context.Context, seriesID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `update media_items set metadata_refreshed_at = null where series_id = $1 and kind = 'season'`, seriesID)
	return err
}

// SeasonEpisode es un episodio de una temporada, para casar sus datos de TMDB por número.
type SeasonEpisode struct {
	ID     uuid.UUID
	Number int
}

// SeasonEpisodes lista los episodios numerados de una temporada.
func (s *Store) SeasonEpisodes(ctx context.Context, seasonID uuid.UUID) ([]SeasonEpisode, error) {
	rows, err := s.Pool.Query(ctx, `select id, index_number from media_items where parent_id = $1 and kind = 'episode'
		and index_number is not null and not locked`, seasonID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SeasonEpisode, error) {
		var e SeasonEpisode
		err := r.Scan(&e.ID, &e.Number)
		return e, err
	})
}

// SetChildProviderImage publica la imagen de TMDB de una temporada o un episodio solo si su serie sigue teniendo la
// identificación con la que se pidió (como SetProviderImage para películas y series). Devuelve la ruta anterior.
func (s *Store) SetChildProviderImage(ctx context.Context, itemID uuid.UUID, kind, path, seriesTMDB string) (bool, string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx)
	var current string
	err = tx.QueryRow(ctx, `select coalesce(sr.provider_ids->>'tmdb', '') from media_items i join media_items sr on sr.id = i.series_id
		where i.id = $1 for update of i`, itemID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && current != seriesTMDB) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	var old string
	err = tx.QueryRow(ctx, `select path from item_images where item_id = $1 and kind = $2 and source = 'tmdb'`, itemID, kind).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	if _, err = tx.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, $2, 'tmdb', $3)
		on conflict (item_id, kind, source) do update set path = excluded.path, updated_at = now()`, itemID, kind, path); err != nil {
		return false, "", err
	}
	return true, old, tx.Commit(ctx)
}

// StaleChild es una temporada o un episodio con datos de TMDB de otra identificación de su serie (I2 + E2).
type StaleChild struct {
	ID      uuid.UUID
	Kind    string
	Number  *int
	RelPath *string // un archivo del episodio, para recuperar el título de su nombre
}

// StaleChildren lista las temporadas y episodios de la serie cuyos datos de TMDB no son de su identificación actual
// (o los tienen y la serie ya no está identificada). Las fichas bloqueadas enteras no se tocan.
func (s *Store) StaleChildren(ctx context.Context, seriesID uuid.UUID) ([]StaleChild, error) {
	rows, err := s.Pool.Query(ctx, `select i.id, i.kind, i.index_number,
		  (select f.rel_path from media_files f where f.item_id = i.id order by f.missing_since nulls first, f.size_bytes desc limit 1)
		from media_items i join media_items sr on sr.id = i.series_id
		where i.series_id = $1 and i.kind in ('season', 'episode') and not i.locked
		  and coalesce(i.provider_ids->>'tmdb_series', '') <> ''
		  and i.provider_ids->>'tmdb_series' <> coalesce(sr.provider_ids->>'tmdb', '')`, seriesID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StaleChild, error) {
		var c StaleChild
		err := r.Scan(&c.ID, &c.Kind, &c.Number, &c.RelPath)
		return c, err
	})
}

// ClearChildMetadata devuelve una temporada o un episodio a lo que sale de su nombre: el título indicado (salvo con
// candado), sin sinopsis, fecha, duración, puntuación ni identificadores de TMDB, y sin sus imágenes de TMDB (las
// elegidas a mano y las locales se quedan). Devuelve las rutas de las imágenes quitadas, para borrar sus archivos.
func (s *Store) ClearChildMetadata(ctx context.Context, id uuid.UUID, title, sortTitle string) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update media_items set
		  title = case when 'title' = any(locked_fields) then title else $2 end,
		  sort_title = case when 'title' = any(locked_fields) or 'sort_title' = any(locked_fields) then sort_title else $3 end,
		  overview = case when 'overview' = any(locked_fields) then overview end,
		  year = case when 'year' = any(locked_fields) then year end,
		  runtime_ms = null, community_rating = null,
		  provider_ids = provider_ids - 'tmdb_series' - 'tmdb_season' - 'tmdb_episode',
		  metadata_refreshed_at = null
		where id = $1 and not locked`, id, title, sortTitle); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `delete from item_images where item_id = $1 and source = 'tmdb' returning path`, id)
	if err != nil {
		return nil, err
	}
	paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	return paths, tx.Commit(ctx)
}

// SeriesWithStaleChildren lista las series de la biblioteca con temporadas o episodios de otra identificación (por
// ejemplo, corregidas antes de que existiera la puesta al día): el escaneo las arregla.
func (s *Store) SeriesWithStaleChildren(ctx context.Context, libraryID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `select distinct sr.id from media_items i join media_items sr on sr.id = i.series_id
		where sr.library_id = $1 and i.kind in ('season', 'episode') and not i.locked
		  and coalesce(i.provider_ids->>'tmdb_series', '') <> ''
		  and i.provider_ids->>'tmdb_series' <> coalesce(sr.provider_ids->>'tmdb', '')`, libraryID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}
