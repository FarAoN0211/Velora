package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// E2: intro y créditos detectados (migración 00010, paquete markers).

// MarkerSeason es una temporada con episodios aún sin analizar.
type MarkerSeason struct {
	ID    uuid.UUID
	Title string // «Serie · Temporada 1», para el avance
}

// markerFile elige el archivo mayor presente por episodio. El worker regula el análisis remoto y espera durante la reproducción.
const markerFile = `select distinct on (e.id) e.id ep, e.parent_id season, f.id file, f.rel_path, lp.path lib, f.duration_ms, f.size_bytes, f.mtime,
	  coalesce(e.index_number, 1000000) n, e.sort_title
	from media_items e join media_files f on f.item_id = e.id and f.missing_since is null and f.duration_ms >= 30000
	join library_paths lp on lp.id = f.library_path_id and lp.detached_at is null 
	join libraries lb on lb.id = lp.library_id and lb.retired_at is null
	where e.kind = 'episode'
	order by e.id, f.size_bytes desc, f.id`

// SeasonsNeedingMarkers devuelve temporadas con al menos dos episodios analizables y alguno sin analizar con la
// versión actual del método (o cambiado desde entonces).
func (s *Store) SeasonsNeedingMarkers(ctx context.Context, version, limit int) ([]MarkerSeason, error) {
	rows, err := s.Pool.Query(ctx, `with c as (`+markerFile+`)
		select c.season, coalesce(sr.title, '') || ' · ' || sn.title from c
		join media_items sn on sn.id = c.season left join media_items sr on sr.id = sn.series_id
		group by c.season, sr.title, sn.title, sr.added_at, sn.index_number
		having count(*) >= 2 and bool_or(not exists (select 1 from marker_analysis a where a.file_id = c.file
		  and a.size_bytes = c.size_bytes and floor(extract(epoch from a.mtime))=floor(extract(epoch from c.mtime)) and a.version = $1))
		order by sr.added_at, sn.index_number limit $2`, version, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MarkerSeason, error) {
		var m MarkerSeason
		err := r.Scan(&m.ID, &m.Title)
		return m, err
	})
}

// CountSeasonsNeedingMarkers cuenta las temporadas pendientes (para el avance).
func (s *Store) CountSeasonsNeedingMarkers(ctx context.Context, version int) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `with c as (`+markerFile+`) select count(*) from (select c.season from c group by c.season
		having count(*) >= 2 and bool_or(not exists (select 1 from marker_analysis a where a.file_id = c.file
		  and a.size_bytes = c.size_bytes and floor(extract(epoch from a.mtime))=floor(extract(epoch from c.mtime)) and a.version = $1))) p`, version).Scan(&n)
	return n, err
}

// MarkerFile es el archivo de un episodio que se analiza.
type MarkerFile struct {
	FileID     uuid.UUID
	RelPath    string
	LibPath    string
	DurationMS int64
	Size       int64
	MTime      time.Time
	Analyzed   bool // ya analizado con esta versión y sin cambios: solo sirve de vecino para comparar
}

// SeasonMarkerFiles devuelve los archivos a analizar de una temporada, en el orden de los episodios.
func (s *Store) SeasonMarkerFiles(ctx context.Context, seasonID uuid.UUID, version int) ([]MarkerFile, error) {
	rows, err := s.Pool.Query(ctx, `with c as (`+markerFile+`)
		select file, rel_path, lib, duration_ms, size_bytes, mtime,
		  exists (select 1 from marker_analysis a where a.file_id = c.file and a.size_bytes = c.size_bytes
		    and floor(extract(epoch from a.mtime))=floor(extract(epoch from c.mtime)) and a.version = $2)
		from c where season = $1 order by n, sort_title, ep`, seasonID, version)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (MarkerFile, error) {
		var m MarkerFile
		err := r.Scan(&m.FileID, &m.RelPath, &m.LibPath, &m.DurationMS, &m.Size, &m.MTime, &m.Analyzed)
		return m, err
	})
}

// Span es un tramo en milisegundos.
type Span struct{ StartMS, EndMS int64 }

// SaveMarkers guarda lo detectado en un archivo (nil: nada fiable) y lo da por analizado con esta versión. Lo
// marcado a mano nunca se sustituye.
func (s *Store) SaveMarkers(ctx context.Context, f MarkerFile, version int, intro, credits *Span) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `delete from media_markers where file_id = $1 and source = 'detected'`, f.FileID); err != nil {
		return err
	}
	for kind, sp := range map[string]*Span{"intro": intro, "credits": credits} {
		if sp == nil || sp.EndMS <= sp.StartMS {
			continue
		}
		if _, err := tx.Exec(ctx, `insert into media_markers (file_id, kind, start_ms, end_ms, source) values ($1, $2, $3, $4, 'detected')
			on conflict (file_id, kind) do nothing`, f.FileID, kind, sp.StartMS, sp.EndMS); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `insert into marker_analysis (file_id, size_bytes, mtime, version) values ($1, $2, $3, $4)
		on conflict (file_id) do update set size_bytes = excluded.size_bytes, mtime = excluded.mtime, version = excluded.version, analyzed_at = now()`,
		f.FileID, f.Size, f.MTime, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Fingerprints devuelve las huellas guardadas de un archivo si son de este mismo archivo (tamaño y fecha) y versión.
func (s *Store) Fingerprints(ctx context.Context, f MarkerFile, version int) (intro, credits []byte, ok bool) {
	err := s.Pool.QueryRow(ctx, `select intro, credits from marker_fingerprints where file_id = $1 and size_bytes = $2
		and floor(extract(epoch from mtime)) = floor(extract(epoch from $3::timestamptz)) and version = $4`,
		f.FileID, f.Size, f.MTime, version).Scan(&intro, &credits)
	return intro, credits, err == nil
}

// SaveFingerprints guarda las huellas de un archivo para no volver a leerlo si la temporada se interrumpe.
func (s *Store) SaveFingerprints(ctx context.Context, f MarkerFile, version int, intro, credits []byte) error {
	_, err := s.Pool.Exec(ctx, `insert into marker_fingerprints (file_id, size_bytes, mtime, version, intro, credits)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (file_id) do update set size_bytes = excluded.size_bytes, mtime = excluded.mtime, version = excluded.version,
		  intro = excluded.intro, credits = excluded.credits, created_at = now()`, f.FileID, f.Size, f.MTime, version, intro, credits)
	return err
}

// DropFingerprints borra las huellas de los archivos de una temporada ya resuelta.
func (s *Store) DropFingerprints(ctx context.Context, files []uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `delete from marker_fingerprints where file_id = any($1)`, files)
	return err
}

// Marker es una intro o unos créditos de un archivo.
type Marker struct {
	Kind           string
	StartMS, EndMS int64
}

// Markers devuelve la intro y los créditos guardados de un archivo.
func (s *Store) Markers(ctx context.Context, fileID uuid.UUID) ([]Marker, error) {
	rows, err := s.Pool.Query(ctx, `select kind, start_ms, end_ms from media_markers where file_id = $1 order by start_ms`, fileID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Marker, error) {
		var m Marker
		err := r.Scan(&m.Kind, &m.StartMS, &m.EndMS)
		return m, err
	})
}

// MarkerScanStatus counts analysed episodes, including reliable analyses that found no matching segment.
type MarkerScanStatus struct {
	Total    int  `json:"total"`
	Done     int  `json:"done"`
	Pending  int  `json:"pending"`
	Intros   int  `json:"intros"`
	Credits  int  `json:"credits"`
	Paused   bool `json:"paused"`
	Playback bool `json:"playback"`
	Running  bool `json:"running"`
}

func (s *Store) MarkerPauseState(ctx context.Context) (paused, playback bool, err error) {
	err = s.Pool.QueryRow(ctx, `select coalesce((select value='true'::jsonb from settings where key='markers.paused'),false),exists(select 1 from user_item_data where last_played_at>now()-interval '90 seconds')`).Scan(&paused, &playback)
	return
}
func (s *Store) MarkerStatus(ctx context.Context, version int) (MarkerScanStatus, error) {
	var p MarkerScanStatus
	err := s.Pool.QueryRow(ctx, `with c as (`+markerFile+`)
 select count(*),count(*) filter(where exists(select 1 from marker_analysis a where a.file_id=c.file and a.size_bytes=c.size_bytes and floor(extract(epoch from a.mtime))=floor(extract(epoch from c.mtime)) and a.version=$1)),
 (select count(*) from media_markers m join media_files f on f.id=m.file_id join library_paths lp on lp.id=f.library_path_id join libraries lb on lb.id=lp.library_id where m.kind='intro' and lp.detached_at is null and lb.retired_at is null),
 (select count(*) from media_markers m join media_files f on f.id=m.file_id join library_paths lp on lp.id=f.library_path_id join libraries lb on lb.id=lp.library_id where m.kind='credits' and lp.detached_at is null and lb.retired_at is null),
 exists(select 1 from jobs where kind='markers_detect' and state in ('queued','running')) from c`, version).Scan(&p.Total, &p.Done, &p.Intros, &p.Credits, &p.Running)
	if err != nil {
		return p, err
	}
	p.Pending = p.Total - p.Done
	p.Paused, p.Playback, err = s.MarkerPauseState(ctx)
	return p, err
}
