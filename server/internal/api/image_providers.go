package api

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"slices"
	"time"
	"velora/internal/artwork"
	"velora/internal/imaging"
	"velora/internal/store"
)

func (s *Server) imageProviders(w http.ResponseWriter, r *http.Request) {
	cfg, err := artwork.Load(s.cfg.ConfigDir)
	if err != nil {
		s.internal(w, "image-providers", err)
		return
	}
	list := []map[string]any{}
	for _, p := range artwork.Providers {
		c := cfg[p]
		list = append(list, map[string]any{"id": p, "enabled": c.Enabled, "configured": c.APIKey != "", "has_client_key": c.ClientKey != "", "has_pin": c.PIN != ""})
	}
	writeJSON(w, 200, map[string]any{"providers": list})
}
func (s *Server) saveImageProvider(w http.ResponseWriter, r *http.Request) {
	p := chi.URLParam(r, "provider")
	if !slices.Contains(artwork.Providers, p) {
		writeError(w, 404, "no_encontrado", "No existe esa fuente.")
		return
	}
	cfgs, err := artwork.Load(s.cfg.ConfigDir)
	if err != nil {
		s.internal(w, "image-providers", err)
		return
	}
	cfg := artwork.Config{}
	if r.Method != "DELETE" {
		if !readJSON(w, r, &cfg) {
			return
		}
		old := cfgs[p]
		if cfg.APIKey == "" {
			cfg.APIKey = old.APIKey
		}
		if cfg.ClientKey == "" {
			cfg.ClientKey = old.ClientKey
		}
		if cfg.PIN == "" {
			cfg.PIN = old.PIN
		}
		if cfg.Enabled {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			if err = artwork.New(s.store, s.cfg.ConfigDir).Check(ctx, p, cfg); err != nil {
				writeError(w, 422, "fuente_no_disponible", err.Error())
				return
			}
		}
	}
	if err = artwork.Save(s.cfg.ConfigDir, p, cfg); err != nil {
		s.internal(w, "image-providers/save", err)
		return
	}
	if cfg.Enabled {
		_, _ = s.queue.Enqueue(r.Context(), "logos_fill", map[string]string{}, "logos")
	}
	s.imageProviders(w, r)
}
func (s *Server) externalGallery(r *http.Request, ref store.ItemRef) ([]artwork.Image, []string) {
	return s.externalGalleryFor(r, ref, "")
}
func (s *Server) externalGalleryFor(r *http.Request, ref store.ItemRef, provider string) ([]artwork.Image, []string) {
	configs, err := artwork.Load(s.cfg.ConfigDir)
	if err != nil {
		return []artwork.Image{}, []string{"No se pudo leer la configuración de fuentes."}
	}
	enabled := false
	for _, c := range configs {
		enabled = enabled || (c.Enabled && c.APIKey != "")
	}
	if !enabled {
		return []artwork.Image{}, nil
	}
	ids := map[string]string{}
	for k, v := range ref.ProviderIDs {
		ids[k] = v
	}
	needsIDs := ids["imdb"] == "" || ((provider == "" || provider == "fanart") && ref.Kind == "series" && ids["tvdb"] == "")
	if needsIDs && ids["tmdb"] != "" && (ref.Kind == "movie" || ref.Kind == "series") {
		if resolved, err := s.tmdb.ArtworkIDs(r.Context(), ref.Kind, ids["tmdb"]); err == nil {
			for k, v := range resolved {
				if ids[k] == "" {
					ids[k] = v
				}
			}
		}
	}
	tg, _ := s.galleryTarget(r, ref)
	service := artwork.New(s.store, s.cfg.ConfigDir)
	target := artwork.Target{Kind: ref.Kind, IDs: ids, Season: tg.Season}
	if provider != "" {
		images, err := service.GallerySource(r.Context(), target, provider)
		if err != nil {
			return []artwork.Image{}, []string{provider + ": " + err.Error()}
		}
		return images, nil
	}
	return service.Gallery(r.Context(), target)
}

// Solo se permite descargar una imagen que esté en la galería del título y tipo pedidos.
func (s *Server) externalImage(r *http.Request, ref store.ItemRef, provider, kind, path string) ([]byte, error) {
	if !slices.Contains(artwork.Providers, provider) {
		return nil, imaging.ErrKind
	}
	images, _ := s.externalGalleryFor(r, ref, provider)
	for _, im := range images {
		if im.Provider == provider && im.Kind == kind && im.FilePath == path {
			return artwork.New(s.store, s.cfg.ConfigDir).Download(r.Context(), provider, path)
		}
	}
	return nil, imaging.ErrKind
}
func (s *Server) previewExternalImage(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.imageItem(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if !slices.Contains(imageKindsFor(ref.Kind), kind) {
		writeError(w, 422, "tipo_no_valido", "Tipo de imagen no válido.")
		return
	}
	select {
	case s.imgSlot <- struct{}{}:
		defer func() { <-s.imgSlot }()
	case <-r.Context().Done():
		return
	}
	raw, err := s.externalImage(r, ref, r.URL.Query().Get("provider"), kind, r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, 422, "imagen_no_disponible", "La imagen ya no está disponible en esta galería.")
		return
	}
	width := 320
	if r.URL.Query().Get("large") == "1" {
		width = 1280
	}
	image, err := imaging.Thumbnail(raw, kind, width)
	if err != nil {
		writeError(w, 422, "imagen_no_valida", imageErrorMessage(err, kind))
		return
	}
	mime := "image/" + image.Ext
	if image.Ext == "jpg" {
		mime = "image/jpeg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(image.Data)
}
