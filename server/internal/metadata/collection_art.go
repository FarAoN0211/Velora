package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CollectionPoster caches the collection's own artwork. Spanish artwork wins
// over textless/English fallback images; no movie artwork is modified.
func (t *TMDB) CollectionPoster(ctx context.Context, id int) (string, error) {
	dst := filepath.Join(t.ConfigDir, "metadata", "collections", fmt.Sprintf("%d.jpg", id))
	if st, e := os.Stat(dst); e == nil && time.Since(st.ModTime()) < cacheTTL {
		return dst, nil
	}
	key, err := t.key()
	if err != nil {
		return "", err
	}
	raw, err := t.cachedGet(ctx, key, fmt.Sprintf("collection-art-%d", id), fmt.Sprintf("/3/collection/%d/images", id), url.Values{"include_image_language": {"es,null,en"}})
	if err != nil {
		return "", err
	}
	var data struct {
		Posters []struct {
			Path     string  `json:"file_path"`
			Language string  `json:"iso_639_1"`
			Vote     float64 `json:"vote_average"`
			Width    int     `json:"width"`
		} `json:"posters"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return "", err
	}
	rank := func(s string) int {
		if s == "es" {
			return 3
		}
		if s == "" {
			return 2
		}
		if s == "en" {
			return 1
		}
		return 0
	}
	sort.SliceStable(data.Posters, func(i, j int) bool {
		a, b := data.Posters[i], data.Posters[j]
		if rank(a.Language) != rank(b.Language) {
			return rank(a.Language) > rank(b.Language)
		}
		if a.Vote != b.Vote {
			return a.Vote > b.Vote
		}
		return a.Width > b.Width
	})
	if len(data.Posters) == 0 {
		return "", errors.New("no collection poster")
	}
	p := data.Posters[0].Path
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return "", errors.New("invalid image path")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", t.ImageBase+"/w500"+p, nil)
	if err != nil {
		return "", err
	}
	r, err := t.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("image status %d", r.StatusCode)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), "collection-*.jpg")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, io.LimitReader(r.Body, 10<<20+1))
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > 10<<20 {
		return "", errors.New("image too large")
	}
	os.Chmod(f.Name(), 0640)
	return dst, os.Rename(f.Name(), dst)
}
