package api

// E1.3 I1 · Administración de una ficha: editar la información con candados, actualizarla desde TMDB y ver sus
// archivos. Solo administradores (404 para el resto, como todas las rutas de administración). Nunca se toca un archivo
// multimedia: todo esto cambia solo la base de datos de VELORA.

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"velora/internal/authz"
	"velora/internal/jobs"
	"velora/internal/library"
	"velora/internal/store"
)

func (s *Server) itemAdminRoutes(r chi.Router) {
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/metadata", authz.Admin, s.getItemMetadata)
	s.handle(r, http.MethodPatch, "/api/v1/admin/items/{id}/metadata", authz.Admin, s.editItemMetadata)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/refresh", authz.Admin, s.refreshItem)
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/files", authz.Admin, s.itemFiles)
	s.identifyRoutes(r)
	s.imageAdminRoutes(r)
}

func (s *Server) getItemMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	m, err := s.store.GetEditableMetadata(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	if err != nil {
		s.internal(w, "admin/items/metadata", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metadata": m, "tmdb_configured": s.tmdb.Configured()})
}

// cleanText quita espacios sobrantes y caracteres de control (se guarda texto plano; la web lo muestra como texto).
func cleanText(v string, multiline bool) string {
	v = strings.Map(func(r rune) rune {
		if r == '\n' && multiline {
			return r
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
	return strings.TrimSpace(v)
}

// validateMetadata normaliza y valida la edición; devuelve el mensaje de error para el administrador.
func validateMetadata(m *store.EditableMetadata) string {
	m.Title = cleanText(m.Title, false)
	m.SortTitle = cleanText(m.SortTitle, false)
	m.OriginalTitle = cleanText(m.OriginalTitle, false)
	m.Tagline = cleanText(m.Tagline, false)
	m.Overview = cleanText(m.Overview, true)
	m.OfficialRating = cleanText(m.OfficialRating, false)
	if m.Title == "" {
		return "El título no puede quedar vacío."
	}
	if m.SortTitle == "" {
		m.SortTitle = library.SortTitle(m.Title)
	}
	for _, f := range []struct {
		v    string
		max  int
		name string
	}{{m.Title, 300, "El título"}, {m.SortTitle, 300, "El título para ordenar"}, {m.OriginalTitle, 300, "El título original"},
		{m.Tagline, 500, "El lema"}, {m.Overview, 5000, "La sinopsis"}, {m.OfficialRating, 20, "La clasificación"}} {
		if utf8.RuneCountInString(f.v) > f.max {
			return f.name + " es demasiado largo."
		}
	}
	if m.Year != nil && (*m.Year < 1870 || *m.Year > time.Now().Year()+5) {
		return "El año no es válido."
	}
	if m.RatingAge != nil && (*m.RatingAge < 0 || *m.RatingAge > 21) {
		return "La edad recomendada debe estar entre 0 y 21."
	}
	seen := map[string]bool{}
	genres := []string{}
	for _, g := range m.Genres {
		g = cleanText(g, false)
		if g == "" || seen[strings.ToLower(g)] {
			continue
		}
		if utf8.RuneCountInString(g) > 40 {
			return "Un género es demasiado largo."
		}
		seen[strings.ToLower(g)] = true
		genres = append(genres, g)
	}
	if len(genres) > 15 {
		return "Como mucho 15 géneros."
	}
	m.Genres = genres
	for _, f := range m.LockedFields {
		if !slices.Contains(store.EditableFields, f) {
			return "Campo bloqueado desconocido: " + f
		}
	}
	return ""
}

func (s *Server) editItemMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	var m store.EditableMetadata
	if !readJSON(w, r, &m) {
		return
	}
	if msg := validateMetadata(&m); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", msg)
		return
	}
	changed, err := s.store.SaveEditedMetadata(r.Context(), id, m)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	if err != nil {
		s.internal(w, "admin/items/metadata", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "item.metadata_edited", id.String(),
		map[string]any{"fields": changed, "locked_fields": m.LockedFields, "locked": m.Locked}, s.clientIP(r))
	saved, _ := s.store.GetEditableMetadata(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"metadata": saved, "changed": changed})
}

func (s *Server) refreshItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Mode != "missing" && req.Mode != "all" {
		writeError(w, http.StatusBadRequest, "parametro", "El modo debe ser «missing» o «all».")
		return
	}
	if _, _, err := s.store.ItemForRefresh(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	if !s.tmdb.Configured() {
		writeError(w, http.StatusConflict, "sin_tmdb", "Añade la clave de TMDB en Panel de control → Metadatos.")
		return
	}
	jobID, err := s.queue.Enqueue(r.Context(), "item_refresh", map[string]string{"item_id": id.String(), "mode": req.Mode}, "refresh:"+id.String())
	if errors.Is(err, jobs.ErrDuplicate) {
		writeError(w, http.StatusConflict, "en_curso", "Ya se está actualizando esta ficha.")
		return
	}
	if err != nil {
		s.internal(w, "admin/items/refresh", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "item.refresh", id.String(), map[string]any{"mode": req.Mode}, s.clientIP(r))
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

func (s *Server) itemFiles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	files, err := s.store.AdminItemFiles(r.Context(), id)
	if err != nil {
		s.internal(w, "admin/items/files", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}
