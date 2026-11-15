package backup

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// StoragePoint es una medición diaria del espacio (observación de ChatGPT: vigilar el crecimiento de la base y de las copias).
type StoragePoint struct {
	At          time.Time `json:"at"`
	DBBytes     int64     `json:"db_bytes"`
	BackupBytes int64     `json:"backup_bytes"`
	FreeBytes   uint64    `json:"free_bytes"`
}

// StorageReport resume la tendencia y si la proyección a 30 días invade el margen de seguridad.
type StorageReport struct {
	Latest             StoragePoint `json:"latest"`
	GrowthBytesPerDay  float64      `json:"growth_bytes_per_day"`
	Projected30dFree   float64      `json:"projected_30d_free_bytes"`
	MarginBytes        uint64       `json:"margin_bytes"`
	WarnMarginInvasion bool         `json:"warn_margin_invasion"`
	Samples            int          `json:"samples"`
}

const historyKey = "storage.history"

// RecordStorage mide, guarda el histórico (30 puntos) y calcula la proyección a 30 días con los últimos 7 días.
func (s *Service) RecordStorage(ctx context.Context) (StorageReport, error) {
	var p StoragePoint
	p.At = time.Now().UTC()
	if err := s.Pool.QueryRow(ctx, `select pg_database_size(current_database())`).Scan(&p.DBBytes); err != nil {
		return StorageReport{}, err
	}
	var err error
	if p.BackupBytes, err = DirSize(s.Dir); err != nil {
		return StorageReport{}, err
	}
	if p.FreeBytes, err = freeBytes(s.Dir); err != nil {
		return StorageReport{}, err
	}

	var history []StoragePoint
	var raw []byte
	err = s.Pool.QueryRow(ctx, `select value from settings where key = $1`, historyKey).Scan(&raw)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return StorageReport{}, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &history)
	}
	history = append(history, p)
	if len(history) > 30 {
		history = history[len(history)-30:]
	}
	out, _ := json.Marshal(history)
	if _, err := s.Pool.Exec(ctx, `insert into settings (key, value) values ($1, $2)
		on conflict (key) do update set value = excluded.value, updated_at = now()`, historyKey, out); err != nil {
		return StorageReport{}, err
	}

	rep := StorageReport{Latest: p, MarginBytes: s.MinFreeBytes, Samples: len(history)}
	// Tendencia con los puntos de los últimos 7 días.
	var first *StoragePoint
	for i := range history {
		if history[i].At.After(p.At.AddDate(0, 0, -7)) {
			first = &history[i]
			break
		}
	}
	if first != nil {
		days := p.At.Sub(first.At).Hours() / 24
		if days >= 1 {
			grown := float64((p.DBBytes + p.BackupBytes) - (first.DBBytes + first.BackupBytes))
			rep.GrowthBytesPerDay = grown / days
		}
	}
	rep.Projected30dFree = float64(p.FreeBytes) - 30*rep.GrowthBytesPerDay
	rep.WarnMarginInvasion = rep.Projected30dFree < float64(s.MinFreeBytes)
	return rep, nil
}
