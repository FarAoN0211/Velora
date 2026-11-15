package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"velora/internal/jobs"
	"velora/internal/metadata"
	"velora/internal/store"
)

func logosJob(st *store.Store, tmdb *metadata.TMDB) jobs.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !tmdb.LogosConfigured() {
			return map[string]string{"omitido": "TMDB no configurado"}, nil
		}
		items, err := st.ItemsMissingLogos(ctx)
		if err != nil {
			return nil, err
		}
		completed, unavailable, failed := 0, 0, 0
		for i, it := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			ok, err := tmdb.EnsureLogo(ctx, it)
			if err != nil {
				failed++
				slog.Warn("logo automático no disponible", "item", it.ID, "error", err)
			} else if ok {
				completed++
			} else {
				unavailable++
			}
			jobs.ReportProgress(ctx, map[string]any{"done": i + 1, "total": len(items), "item": it.Title, "logos": completed, "unavailable": unavailable, "failed": failed})
		}
		return map[string]int{"logos": completed, "unavailable": unavailable, "failed": failed}, nil
	}
}
