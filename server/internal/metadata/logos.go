package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"velora/internal/artwork"
	"velora/internal/imaging"
	"velora/internal/store"
)

// Idioma antes que puntuación: un logo español no pierde ante uno inglés más votado.
func rankedLogos(logos []GalleryImage) []GalleryImage {
	out := slices.Clone(logos)
	rank := func(lang string) int {
		switch strings.ToLower(lang) {
		case "es-es":
			return 0
		case "es":
			return 1
		case "", "null":
			return 2
		case "en":
			return 3
		default:
			return 4
		}
	}
	slices.SortStableFunc(out, func(a, b GalleryImage) int {
		if d := rank(a.Language) - rank(b.Language); d != 0 {
			return d
		}
		if a.Vote > b.Vote {
			return -1
		}
		if a.Vote < b.Vote {
			return 1
		}
		if a.Width > b.Width {
			return -1
		}
		if a.Width < b.Width {
			return 1
		}
		return strings.Compare(a.FilePath, b.FilePath)
	})
	return out
}

// EnsureLogo completa lo que falta sin tocar logos locales ni elegidos por el usuario.
func (t *TMDB) EnsureLogo(ctx context.Context, it store.PendingMetadata) (bool, error) {
	if (it.Kind != "movie" && it.Kind != "series") || it.ProviderIDs["tmdb"] == "" {
		return false, nil
	}
	if t.Store.HasAnyImage(ctx, it.ID, "logo") {
		return false, nil
	}
	key, keyErr := t.key()
	kind := "movie"
	if it.Kind == "series" {
		kind = "tv"
	}
	path := "/3/" + kind + "/" + url.PathEscape(it.ProviderIDs["tmdb"]) + "/images"
	// Sin filtrar otros idiomas: si no existe español se conserva la posibilidad del original.
	var raw []byte
	var providerErr error
	if keyErr == nil {
		raw, providerErr = t.cachedGet(ctx, key, "automatic-logos:"+path, path, url.Values{})
	}
	var response struct {
		Logos []struct {
			FilePath string  `json:"file_path"`
			Width    int     `json:"width"`
			Height   int     `json:"height"`
			Language string  `json:"iso_639_1"`
			Vote     float64 `json:"vote_average"`
		}
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &response); err != nil {
			providerErr = err
		}
	}
	logos := []GalleryImage{}
	for _, l := range response.Logos {
		if thumbPath.MatchString(l.FilePath) {
			logos = append(logos, GalleryImage{FilePath: l.FilePath, Width: l.Width, Height: l.Height, Language: l.Language, Vote: l.Vote})
		}
	}
	service := artwork.New(t.Store, t.ConfigDir)
	configs, _ := artwork.Load(t.ConfigDir)
	enabled := false
	for _, c := range configs {
		enabled = enabled || (c.Enabled && c.APIKey != "")
	}
	if enabled {
		ids := it.ProviderIDs
		if resolved, e := t.ArtworkIDs(ctx, it.Kind, it.ProviderIDs["tmdb"]); e == nil {
			ids = resolved
		}
		extra, _ := service.Gallery(ctx, artwork.Target{Kind: it.Kind, IDs: ids})
		for _, im := range extra {
			if im.Kind == "logo" {
				logos = append(logos, GalleryImage{FilePath: im.FilePath, Provider: im.Provider, Width: im.Width, Height: im.Height, Language: im.Language, Vote: im.Vote})
			}
		}
	}
	var last error = providerErr
	for _, l := range rankedLogos(logos) {
		var rel string
		var err error
		source := l.Provider
		if source == "" {
			source = "tmdb"
			rel, err = t.download(ctx, it.ID, "logo", "w500", l.FilePath)
		} else {
			var raw []byte
			raw, err = service.Download(ctx, source, l.FilePath)
			if err == nil {
				var processed imaging.Result
				processed, err = imaging.Process(raw, "logo")
				if err == nil {
					rel = filepath.Join(it.ID.String(), "logo-"+uuid.NewString()+"."+processed.Ext)
					dst := filepath.Join(t.ConfigDir, "metadata", rel)
					err = os.MkdirAll(filepath.Dir(dst), 0750)
					if err == nil {
						err = os.WriteFile(dst, processed.Data, 0640)
					}
				}
			}
		}
		if err != nil {
			last = err
			if rel != "" {
				t.RemoveImageFiles(ctx, it.ID, rel)
			}
			continue
		}
		published, old, err := t.Store.SetArtworkImage(ctx, it.ID, "logo", rel, it.ProviderIDs["tmdb"], source)
		if err != nil || !published {
			t.RemoveImageFiles(ctx, it.ID, rel)
			return false, err
		}
		if old != "" {
			t.RemoveImageFiles(ctx, it.ID, old)
		}
		return true, nil
	}
	if last != nil {
		return false, fmt.Errorf("descarga del logo: %w", last)
	}
	return false, nil
}

func (t *TMDB) LogosConfigured() bool {
	if t.Configured() {
		return true
	}
	cfg, _ := artwork.Load(t.ConfigDir)
	for _, c := range cfg {
		if c.Enabled && c.APIKey != "" {
			return true
		}
	}
	return false
}
