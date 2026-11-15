package api

// Ficha de una persona del reparto (página /persona/:id de la web): datos de TMDB y foto grande desde el mismo origen.
// No lee medios ni escanea: la filmografía en las bibliotecas sale de /api/v1/items?person=.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"

	"velora/internal/imaging"
	"velora/internal/metadata"
)

// personDetails es inyectable en pruebas.
var personDetails = func(s *Server, r *http.Request, id int) (metadata.Person, error) {
	return s.tmdb.PersonDetails(r.Context(), id)
}

func personParam(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "person"))
	return id, err == nil && id > 0 && strconv.Itoa(id) == chi.URLParam(r, "person")
}

func (s *Server) getPerson(w http.ResponseWriter, r *http.Request) {
	id, ok := personParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.tmdb.Configured() {
		writeError(w, http.StatusServiceUnavailable, "metadatos_no_disponibles", "Sin la clave de TMDB no hay datos de esta persona.")
		return
	}
	p, err := personDetails(s, r, id)
	if errors.Is(err, metadata.ErrProviderInvalid) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "metadatos_no_disponibles", "No se han podido consultar los datos de esta persona.")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"person": map[string]any{
		"id": p.ID, "name": p.Name, "biography": p.Biography, "birthday": p.Birthday, "deathday": p.Deathday,
		"place_of_birth": p.PlaceOfBirth, "department": p.Department, "has_photo": p.ProfilePath != "",
	}})
}

func (s *Server) personPhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := personParam(r)
	if !ok || !s.tmdb.Configured() {
		http.NotFound(w, r)
		return
	}
	p, err := personDetails(s, r, id)
	if err != nil || p.ProfilePath == "" {
		http.NotFound(w, r)
		return
	}
	dir := filepath.Join(s.cfg.ConfigDir, "image-cache")
	cached := filepath.Join(dir, fmt.Sprintf("person-large-%x.jpg", sha256.Sum256([]byte(p.ProfilePath))))
	if b, err := os.ReadFile(cached); err == nil {
		servePersonJPEG(w, b)
		return
	}
	data, err := s.tmdb.ProfileImageSize(r.Context(), p.ProfilePath, "h632")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	thumb, err := imaging.Thumbnail(data, "poster", 500)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	thumbnailEviction.Lock()
	if err := os.MkdirAll(dir, 0o750); err == nil {
		trimThumbnailCache(dir)
		if tmp, e := os.CreateTemp(dir, "person-*"); e == nil {
			name := tmp.Name()
			_, e = tmp.Write(thumb.Data)
			if cerr := tmp.Close(); e == nil && cerr == nil {
				_ = os.Rename(name, cached)
			} else {
				_ = os.Remove(name)
			}
		}
	}
	thumbnailEviction.Unlock()
	servePersonJPEG(w, thumb.Data)
}

func servePersonJPEG(w http.ResponseWriter, b []byte) {
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(b)
}
