package metadata

import (
	"context"
	"strconv"
	"velora/internal/library"
	"velora/internal/store"
)

// Called only when the folder-title lookup found nothing and no TMDB identity
// exists. An agreed filename prefix is evidence, not permission to guess a show.
func (t *TMDB) seriesFallback(ctx context.Context, it store.PendingMetadata) (string, error) {
	names, err := t.Store.SeriesEpisodeNames(ctx, it.ID)
	if err != nil {
		return "", err
	}
	hints := library.SeriesTitleHints(names)
	if len(hints) == 0 || hints[0].Episodes < 2 {
		return "", nil
	}
	// Conflicting episode prefixes need manual review, even if one is a majority.
	if len(hints) > 1 {
		return "", nil
	}
	hint := hints[0]
	year := hint.Year
	if year == 0 && it.Year != nil {
		year = int(*it.Year)
	}
	candidates, err := t.Search(ctx, "series", hint.Title, year)
	if err != nil {
		return "", err
	}
	// No first-result guessing: a single, exact original/localized title only.
	if len(candidates) != 1 {
		return "", nil
	}
	c := candidates[0]
	key := library.SeriesTitleKey(hint.Title)
	if key != library.SeriesTitleKey(c.Title) && key != library.SeriesTitleKey(c.OriginalTitle) {
		return "", nil
	}
	if year > 0 && c.Year != year {
		return "", nil
	}
	return strconv.Itoa(c.TMDBID), nil
}
