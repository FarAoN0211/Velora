package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/google/uuid"

	"velora/internal/library"
	"velora/internal/store"
)

// E2 S1: fichas de temporadas y episodios. Una sola petición por temporada (/3/tv/{id}/season/{n}) trae el nombre, la
// sinopsis, la fecha, la duración y la imagen de cada episodio. Los episodios se casan por número; los campos con
// candado y los títulos corregidos a mano nunca se tocan (ApplyMetadata).

// genericEpisodeName: lo que TMDB devuelve cuando el episodio no tiene título en el idioma pedido.
var genericEpisodeName = regexp.MustCompile(`(?i)^\s*(episodio|episode|cap[ií]tulo)\s*\d+\s*$`)

type tvSeason struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	AirDate    string `json:"air_date"`
	PosterPath string `json:"poster_path"`
	Number     *int   `json:"season_number"`
	Episodes   []struct {
		ID          int     `json:"id"`
		Number      int     `json:"episode_number"`
		Name        string  `json:"name"`
		Overview    string  `json:"overview"`
		AirDate     string  `json:"air_date"`
		Runtime     int     `json:"runtime"`
		StillPath   string  `json:"still_path"`
		VoteAverage float64 `json:"vote_average"`
	} `json:"episodes"`
}

// EnrichSeason aplica la ficha de TMDB a la temporada y a sus episodios. Devuelve false si TMDB no tiene esa temporada
// (la temporada queda marcada como intentada para esta identificación de la serie y no se vuelve a pedir).
func (t *TMDB) EnrichSeason(ctx context.Context, p store.PendingSeason) (bool, error) {
	key, err := t.key()
	if err != nil {
		return false, err
	}
	path := fmt.Sprintf("/3/tv/%s/season/%d", url.PathEscape(p.SeriesTMDB), p.Number)
	cacheKey := fmt.Sprintf("tvseason:%s:%d:%s", p.SeriesTMDB, p.Number, t.Language)
	raw, err := t.cachedGet(ctx, key, cacheKey, path, url.Values{"language": {t.Language}})
	ids := map[string]string{"tmdb_series": p.SeriesTMDB}
	if errors.Is(err, errNotFound) {
		return false, t.Store.ApplyMetadata(ctx, p.ID, store.MetadataFields{ProviderIDs: ids}, "", true)
	}
	if err != nil {
		return false, err
	}
	var s tvSeason
	if err := json.Unmarshal(raw, &s); err != nil || s.Number == nil || *s.Number != p.Number {
		return false, t.Store.ApplyMetadata(ctx, p.ID, store.MetadataFields{ProviderIDs: ids}, "", true)
	}
	ids["tmdb_season"] = strconv.Itoa(s.ID)
	sortTitle := fmt.Sprintf("%02d", p.Number)
	if err := t.Store.ApplyMetadata(ctx, p.ID, store.MetadataFields{Title: s.Name, Overview: s.Overview, Year: yearOf(s.AirDate),
		ProviderIDs: ids}, sortTitle); err != nil {
		return false, err
	}
	t.childImage(ctx, p.ID, "poster", "w500", s.PosterPath, p.SeriesTMDB)
	eps, err := t.Store.SeasonEpisodes(ctx, p.ID)
	if err != nil {
		return false, err
	}
	byNumber := map[int][]uuid.UUID{}
	for _, e := range eps {
		byNumber[e.Number] = append(byNumber[e.Number], e.ID)
	}
	for _, e := range s.Episodes {
		name := e.Name
		if genericEpisodeName.MatchString(name) { // sin traducir: mejor el título del nombre del archivo
			name = ""
		}
		for _, id := range byNumber[e.Number] {
			f := store.MetadataFields{Title: name, Overview: e.Overview, Year: yearOf(e.AirDate), RuntimeMS: int64(e.Runtime) * 60_000,
				CommunityRating: e.VoteAverage, ProviderIDs: map[string]string{"tmdb_series": p.SeriesTMDB, "tmdb_episode": strconv.Itoa(e.ID)}}
			if err := t.Store.ApplyMetadata(ctx, id, f, fmt.Sprintf("%04d", e.Number)); err != nil {
				return false, err
			}
			t.childImage(ctx, id, "thumb", "w300", e.StillPath, p.SeriesTMDB)
		}
	}
	return true, nil
}

// childImage descarga y publica una imagen de temporada o episodio; un fallo solo deja la imagen sin poner.
func (t *TMDB) childImage(ctx context.Context, itemID uuid.UUID, kind, size, imgPath, seriesTMDB string) {
	if imgPath == "" {
		return
	}
	rel, err := t.download(ctx, itemID, kind, size, imgPath)
	if err != nil {
		return
	}
	published, old, err := t.Store.SetChildProviderImage(ctx, itemID, kind, rel, seriesTMDB)
	if err != nil || !published {
		t.RemoveImageFiles(ctx, itemID, rel)
		return
	}
	if old != "" && old != rel {
		t.RemoveImageFiles(ctx, itemID, old)
	}
}

// RefreshSeriesChildren pone las temporadas y episodios de una serie al día con su identificación actual: primero quita
// los datos de TMDB que traían de una identificación anterior (vuelven al título de su nombre de archivo) y después
// pide la ficha de cada temporada pendiente. Sin clave de TMDB solo hace lo primero.
func (t *TMDB) RefreshSeriesChildren(ctx context.Context, seriesID uuid.UUID) (cleared, enriched int, err error) {
	stale, err := t.Store.StaleChildren(ctx, seriesID)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range stale {
		title, sortTitle := childTitle(c)
		paths, err := t.Store.ClearChildMetadata(ctx, c.ID, title, sortTitle)
		if err != nil {
			return cleared, 0, err
		}
		t.RemoveImageFiles(ctx, c.ID, paths...)
		cleared++
	}
	if !t.Configured() {
		return cleared, 0, nil
	}
	for {
		pending, err := t.Store.SeasonsNeedingMetadata(ctx, nil, &seriesID, 50)
		if err != nil || len(pending) == 0 {
			return cleared, enriched, err
		}
		for _, p := range pending {
			if _, err := t.EnrichSeason(ctx, p); err != nil {
				return cleared, enriched, err
			}
			enriched++
		}
	}
}

// childTitle es el título que daría el escáner: «Temporada N»/«Especiales», o el del nombre del archivo del episodio.
func childTitle(c store.StaleChild) (string, string) {
	n := 0
	if c.Number != nil {
		n = *c.Number
	}
	if c.Kind == "season" {
		return library.SeasonTitle(n), fmt.Sprintf("%02d", n)
	}
	title := ""
	if c.RelPath != nil {
		title = library.ParseEpisode(*c.RelPath).Title
	}
	if c.Number != nil {
		if title == "" {
			title = fmt.Sprintf("Episodio %d", n)
		}
		return title, fmt.Sprintf("%04d", n)
	}
	if title == "" {
		title = "Episodio"
	}
	return title, library.SortTitle(title)
}
