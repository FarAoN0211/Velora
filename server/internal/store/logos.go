package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) HasAnyImage(ctx context.Context, id uuid.UUID, kind string) bool {
	var ok bool
	_ = s.Pool.QueryRow(ctx, `select exists(select 1 from item_images where item_id=$1 and kind=$2)`, id, kind).Scan(&ok)
	return ok
}
func (s *Store) ItemsMissingLogos(ctx context.Context) ([]PendingMetadata, error) {
	rows, err := s.Pool.Query(ctx, `select i.id,i.kind,i.title,i.year,i.provider_ids from media_items i join libraries l on l.id=i.library_id where i.kind in ('movie','series') and not i.locked and l.retired_at is null and coalesce(i.provider_ids->>'tmdb','')<>'' and not exists(select 1 from item_images im where im.item_id=i.id and im.kind='logo') order by i.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PendingMetadata, error) {
		var p PendingMetadata
		err := r.Scan(&p.ID, &p.Kind, &p.Title, &p.Year, &p.ProviderIDs)
		return p, err
	})
}
