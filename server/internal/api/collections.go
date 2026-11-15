package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) collections(w http.ResponseWriter, r *http.Request) {
	scope, err := collectionScope(r)
	if err != nil {
		writeError(w, 400, "biblioteca_invalida", "La biblioteca no es válida.")
		return
	}
	cards, err := s.store.Collections(r.Context(), viewer(r), scope)
	if err != nil {
		s.internal(w, "collections", err)
		return
	}
	writeJSON(w, 200, map[string]any{"collections": cards})
}
func (s *Server) collectionDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "collection"))
	if err != nil || id <= 0 {
		writeError(w, 404, "no_encontrado", "No existe esta colección.")
		return
	}
	scope, err := collectionScope(r)
	if err != nil {
		writeError(w, 400, "biblioteca_invalida", "La biblioteca no es válida.")
		return
	}
	cards, err := s.store.Collections(r.Context(), viewer(r), scope)
	if err != nil {
		s.internal(w, "collections", err)
		return
	}
	for _, c := range cards {
		if c.ID == id {
			items, err := s.store.CollectionMovies(r.Context(), viewer(r), id, scope)
			if err != nil {
				s.internal(w, "collections", err)
				return
			}
			writeJSON(w, 200, map[string]any{"collection": c, "items": items})
			return
		}
	}
	writeError(w, 404, "no_encontrado", "No existe esta colección.")
}

func collectionScope(r *http.Request) (uuid.UUID, error) {
	raw := r.URL.Query().Get("library_id")
	if raw == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(raw)
}
func (s *Server) editCollection(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "collection"))
	if err != nil || id <= 0 {
		writeError(w, 404, "no_encontrado", "No existe esta colección.")
		return
	}
	scope, err := collectionScope(r)
	if err != nil {
		writeError(w, 400, "biblioteca_invalida", "La biblioteca no es válida.")
		return
	}
	cards, err := s.store.Collections(r.Context(), viewer(r), scope)
	if err != nil {
		s.internal(w, "collections", err)
		return
	}
	found := false
	for _, c := range cards {
		if c.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, 404, "no_encontrado", "No existe esta colección.")
		return
	}
	var req struct {
		Title    string     `json:"title"`
		Overview string     `json:"overview"`
		PosterID *uuid.UUID `json:"poster_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if len(req.Title) > 500 || len(req.Overview) > 20000 {
		writeError(w, 422, "datos_invalidos", "El texto es demasiado largo.")
		return
	}
	if req.PosterID != nil {
		items, e := s.store.CollectionMovies(r.Context(), viewer(r), id, scope)
		if e != nil {
			s.internal(w, "collections", e)
			return
		}
		valid := false
		for _, it := range items {
			if it.ID == *req.PosterID {
				valid = true
				break
			}
		}
		if !valid {
			writeError(w, 422, "imagen_invalida", "Elige la carátula de una película de esta colección.")
			return
		}
	}
	if err = s.store.SaveCollectionOverride(r.Context(), id, scope, req.Title, req.Overview, req.PosterID); err != nil {
		s.internal(w, "collections", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"saved": true})
}

func (s *Server) collectionPoster(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(chi.URLParam(r, "collection"))
	if err != nil || id <= 0 {
		writeError(w, 404, "no_encontrado", "No existe esta colección.")
		return
	}
	scope, err := collectionScope(r)
	if err != nil {
		writeError(w, 400, "biblioteca_invalida", "La biblioteca no es válida.")
		return
	}
	cards, err := s.store.Collections(r.Context(), viewer(r), scope)
	if err != nil {
		s.internal(w, "collections", err)
		return
	}
	for _, c := range cards {
		if c.ID == id {
			if !c.CustomPoster {
				if file, e := s.tmdb.CollectionPoster(r.Context(), id); e == nil {
					w.Header().Set("Cache-Control", "private, max-age=3600")
					http.ServeFile(w, r, file)
					return
				}
			}
			http.Redirect(w, r, "/api/v1/items/"+c.PosterID.String()+"/images/poster", http.StatusTemporaryRedirect)
			return
		}
	}
	writeError(w, 404, "no_encontrado", "No existe esta colección.")
}
