package metadata

import (
	"context"
	"strconv"
)

// ArtworkIDs resuelve los identificadores del mismo título, sin búsquedas ambiguas por nombre.
func (t *TMDB) ArtworkIDs(ctx context.Context, kind, id string) (map[string]string, error) {
	key, err := t.key()
	if err != nil {
		return nil, err
	}
	m, err := t.detailsFor(ctx, key, kind, id)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{"tmdb": id}
	if m.ImdbID != "" {
		ids["imdb"] = m.ImdbID
	}
	if m.TVDBID > 0 {
		ids["tvdb"] = strconv.Itoa(m.TVDBID)
	}
	return ids, nil
}
