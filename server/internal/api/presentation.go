package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/sync/singleflight"
	"velora/internal/imaging"
)

var presentationRequests singleflight.Group

// Optional details load after the main page, never before playback or by reading media.
func (s *Server) getPresentation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	item, err := s.store.Item(r.Context(), viewer(r), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	raw := item.Presentation
	if string(raw) == "{}" && (item.Kind == "movie" || item.Kind == "series") && s.tmdb.Configured() && item.ProviderIDs["tmdb"] != "" {
		value, err, _ := presentationRequests.Do(id.String()+":"+item.ProviderIDs["tmdb"], func() (any, error) {
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			it, locked, e := s.store.ItemForRefresh(ctx, id)
			if e != nil {
				return nil, e
			}
			if locked {
				return json.RawMessage(`{}`), nil
			}
			p, e := s.tmdb.Presentation(ctx, it)
			if e != nil {
				return nil, e
			}
			tag, e := s.store.Pool.Exec(ctx, `update media_items set presentation=$2 where id=$1 and not locked and provider_ids->>'tmdb'=$3`, id, p, it.ProviderIDs["tmdb"])
			if e != nil {
				return nil, e
			}
			if tag.RowsAffected() == 0 {
				return nil, errors.New("identity changed")
			}
			return p, nil
		})
		if err != nil {
			writeError(w, 503, "metadatos_no_disponibles", "No se ha podido completar la información ahora.")
			return
		}
		raw = value.(json.RawMessage)
	}
	visible, err := s.store.ItemVisible(r.Context(), viewer(r), id)
	if err != nil || !visible {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, map[string]any{"presentation": raw})
}
func (s *Server) personImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	personID, err := strconv.Atoi(chi.URLParam(r, "person"))
	if err != nil || personID <= 0 {
		http.NotFound(w, r)
		return
	}
	item, err := s.store.Item(r.Context(), viewer(r), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var p struct {
		Credits struct {
			Cast, Crew []struct {
				ID   int    `json:"id"`
				Path string `json:"profile_path"`
			}
		} `json:"credits"`
	}
	if json.Unmarshal(item.Presentation, &p) != nil {
		http.NotFound(w, r)
		return
	}
	path := ""
	for _, person := range append(p.Credits.Cast, p.Credits.Crew...) {
		if person.ID == personID {
			path = person.Path
			break
		}
	}
	if path == "" {
		http.NotFound(w, r)
		return
	}
	// Same-origin proxy: provider credentials and profile URLs never reach client requests.
	cached := filepath.Join(s.cfg.ConfigDir, "image-cache", fmt.Sprintf("person-%x.jpg", sha256.Sum256([]byte(path))))
	if bytes, err := os.ReadFile(cached); err == nil {
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(bytes)
		return
	}
	data, err := s.tmdb.ProfileImage(r.Context(), path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	thumb, err := imaging.Thumbnail(data, "poster", 160)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", "image/jpeg")
	thumbnailEviction.Lock()
	if err := os.MkdirAll(filepath.Dir(cached), 0750); err == nil {
		trimThumbnailCache(filepath.Dir(cached))
		if tmp, e := os.CreateTemp(filepath.Dir(cached), "person-*"); e == nil {
			name := tmp.Name()
			_, e = tmp.Write(thumb.Data)
			closeErr := tmp.Close()
			if e == nil && closeErr == nil {
				_ = os.Rename(name, cached)
			}
			_ = os.Remove(name)
		}
	}
	thumbnailEviction.Unlock()
	_, _ = w.Write(thumb.Data)
}
