package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
	"velora/internal/jobs"
	"velora/internal/metadata"
	"velora/internal/store"
)

func collectionsJob(st *store.Store, tmdb *metadata.TMDB) jobs.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		items, err := st.MoviesMissingCollection(ctx)
		if err != nil {
			return nil, err
		}
		done := 0
		failed := 0
		for _, it := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			raw, err := tmdb.CollectionMembership(ctx, it)
			if err != nil {
				failed++
				slog.Warn("colección TMDB pendiente", "item", it.ID, "error", err)
				continue
			}
			if err = st.SaveMovieCollection(ctx, it, raw); err != nil {
				return nil, err
			}
			done++
			jobs.ReportProgress(ctx, map[string]any{"done": done, "total": len(items), "failed": failed, "item": it.Title})
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
		if failed > 0 {
			return map[string]int{"updated": done, "failed": failed}, fmt.Errorf("colecciones pendientes de TMDB: %d", failed)
		}
		return map[string]int{"updated": done}, nil
	}
}
