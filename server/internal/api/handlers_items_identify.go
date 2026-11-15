package api

// E1.3 I2 · «Identificar»: buscar en TMDB por título, año o ID, comparar y aplicar sobre la misma ficha. Solo cambia
// metadatos: el archivo, la ruta de reproducción y el progreso de los usuarios no se tocan. Nunca se aplica nada sin una
// elección explícita del administrador, y un fallo del proveedor deja la ficha como estaba.

import (
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"velora/internal/authz"
	"velora/internal/jobs"
	"velora/internal/library"
	"velora/internal/metadata"
	"velora/internal/store"
)

func (s *Server) identifyRoutes(r chi.Router) {
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/identify/search", authz.Admin, s.identifySearch)
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/identify/preview", authz.Admin, s.identifyPreview)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/identify", authz.Admin, s.identifyApply)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/identify/undo", authz.Admin, s.identifyUndo)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/identify/reset", authz.Admin, s.identifyReset)
	s.handle(r, http.MethodGet, "/api/v1/admin/metadata/thumb", authz.Admin, s.metadataThumb)
}

// identifiableItem lee la ficha y responde si no se puede identificar: temporadas y episodios van por su serie.
func (s *Server) identifiableItem(w http.ResponseWriter, r *http.Request) (store.ItemRef, bool) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return store.ItemRef{}, false
	}
	ref, err := s.store.ItemRefByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return ref, false
	}
	if err != nil {
		s.internal(w, "admin/items/identify", err)
		return ref, false
	}
	if ref.Kind != "movie" && ref.Kind != "series" {
		writeError(w, http.StatusUnprocessableEntity, "por_su_serie", "Las temporadas y los episodios se identifican desde su serie.")
		return ref, false
	}
	return ref, true
}

// providerError traduce un fallo de TMDB a un mensaje claro; la ficha no ha cambiado.
func (s *Server) providerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrNotConfigured):
		writeError(w, http.StatusConflict, "sin_tmdb", "Añade la clave de TMDB en Panel de control → Metadatos. Mientras tanto puedes editar la información a mano.")
	case errors.Is(err, metadata.ErrProviderNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "TMDB no tiene ese identificador.")
	case errors.Is(err, metadata.ErrProviderInvalid):
		writeError(w, http.StatusBadGateway, "proveedor", "TMDB ha devuelto una ficha incompleta. La ficha no ha cambiado; prueba otra vez o edítala a mano.")
	default:
		writeError(w, http.StatusBadGateway, "proveedor", "TMDB no responde ahora mismo. La ficha no ha cambiado; prueba otra vez o edítala a mano.")
	}
}

func queryYear(r *http.Request) (int, bool) {
	v := strings.TrimSpace(r.URL.Query().Get("year"))
	if v == "" {
		return 0, true
	}
	y, err := strconv.Atoi(v)
	return y, err == nil && y >= 1870 && y <= 2200
}

func queryTMDBID(v string) (int, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(v))
	return id, err == nil && id > 0 && id < 1_000_000_000
}

func (s *Server) identifySearch(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.identifiableItem(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if v := strings.TrimSpace(r.URL.Query().Get("tmdb_id")); v != "" {
		id, ok := queryTMDBID(v)
		if !ok {
			writeError(w, http.StatusBadRequest, "parametro", "El identificador de TMDB debe ser un número.")
			return
		}
		q = "tmdb-" + strconv.Itoa(id)
	}
	year, ok := queryYear(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "parametro", "El año no es válido.")
		return
	}
	if q == "" || len(q) > 300 {
		writeError(w, http.StatusBadRequest, "parametro", "Escribe un título o un identificador.")
		return
	}
	found, err := s.tmdb.Search(r.Context(), ref.Kind, q, year)
	if err != nil {
		s.providerError(w, err)
		return
	}
	type candidate struct {
		metadata.Candidate
		InLibrary bool `json:"in_library"`
		Current   bool `json:"current"`
	}
	ids := make([]string, 0, len(found))
	for _, c := range found {
		ids = append(ids, strconv.Itoa(c.TMDBID))
	}
	have, err := s.store.TMDBIDsInLibrary(r.Context(), ref.Kind, ref.ID, ids)
	if err != nil {
		s.internal(w, "admin/items/identify/search", err)
		return
	}
	out := make([]candidate, 0, len(found))
	for _, c := range found {
		tid := strconv.Itoa(c.TMDBID)
		out = append(out, candidate{Candidate: c, InLibrary: have[tid], Current: ref.ProviderIDs["tmdb"] == tid})
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out})
}

func (s *Server) identifyPreview(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.identifiableItem(w, r)
	if !ok {
		return
	}
	tmdbID, ok := queryTMDBID(r.URL.Query().Get("tmdb_id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "parametro", "El identificador de TMDB debe ser un número.")
		return
	}
	cand, err := s.tmdb.Identify(r.Context(), ref.Kind, tmdbID)
	if err != nil {
		s.providerError(w, err)
		return
	}
	cur, err := s.store.GetEditableMetadata(r.Context(), ref.ID)
	if err != nil {
		s.internal(w, "admin/items/identify/preview", err)
		return
	}
	custom, err := s.store.CustomImageKinds(r.Context(), ref.ID)
	if err != nil {
		s.internal(w, "admin/items/identify/preview", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current": map[string]any{"title": cur.Title, "year": cur.Year, "overview": cur.Overview, "tmdb_id": cur.ProviderIDs["tmdb"]},
		"candidate": map[string]any{"tmdb_id": tmdbID, "title": cand.Fields.Title, "original_title": cand.Fields.OriginalTitle, "year": cand.Fields.Year,
			"overview": cand.Fields.Overview, "poster_path": cand.PosterPath, "backdrop_path": cand.BackdropPath},
		"locked_fields": cur.LockedFields, "locked": cur.Locked, "custom_images": custom,
	})
}

func (s *Server) identifyApply(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.identifiableItem(w, r)
	if !ok {
		return
	}
	var req struct {
		TMDBID        int  `json:"tmdb_id"`
		ReplaceLocked bool `json:"replace_locked"`
		ReplaceImages bool `json:"replace_images"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "parametro", "Falta el resultado elegido.")
		return
	}
	if ref.Locked {
		writeError(w, http.StatusConflict, "bloqueada", "Esta ficha está bloqueada entera. Desbloquéala en «Editar información» para identificarla.")
		return
	}
	// Primero se descarga todo; solo si todo ha ido bien se escribe, en una transacción.
	cand, err := s.tmdb.Identify(r.Context(), ref.Kind, req.TMDBID)
	if err != nil {
		s.providerError(w, err)
		return
	}
	var images map[string]string
	if req.ReplaceImages {
		if images, err = s.tmdb.DownloadIdentityImages(r.Context(), ref.ID, cand); err != nil {
			s.providerError(w, err)
			return
		}
	}
	who := authz.FromContext(r.Context())
	err = s.store.ApplyIdentification(r.Context(), ref.ID, store.IdentifyInput{Fields: cand.Fields, SortTitle: library.SortTitle(cand.Fields.Title),
		ReplaceLocked: req.ReplaceLocked, ReplaceImages: req.ReplaceImages, Images: images, By: who.UserID})
	if err != nil { // no se ha publicado nada: lo descargado para esta operación sobra
		for _, rel := range images {
			s.tmdb.RemoveImageFiles(r.Context(), ref.ID, rel)
		}
	}
	switch {
	case errors.Is(err, store.ErrItemLocked):
		writeError(w, http.StatusConflict, "bloqueada", "Esta ficha está bloqueada entera. Desbloquéala en «Editar información» para identificarla.")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	case err != nil:
		s.internal(w, "admin/items/identify", err)
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "item.identified", ref.ID.String(), map[string]any{"tmdb_anterior": ref.ProviderIDs["tmdb"], "tmdb": req.TMDBID,
		"replace_locked": req.ReplaceLocked, "replace_images": req.ReplaceImages}, s.clientIP(r))
	_, _ = s.queue.Enqueue(r.Context(), "logos_fill", map[string]string{}, "logos")
	s.refreshSeriesChildren(r, ref.ID, ref.Kind)
	s.respondMetadata(w, r, ref.ID)
}

func (s *Server) respondMetadata(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	m, err := s.store.GetEditableMetadata(r.Context(), id)
	if err != nil {
		s.internal(w, "admin/items/metadata", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metadata": m})
}

func (s *Server) identifyUndo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	dropped, err := s.store.UndoIdentification(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	case errors.Is(err, store.ErrNoSnapshot):
		writeError(w, http.StatusConflict, "nada_que_deshacer", "No hay ninguna identificación que deshacer.")
		return
	case err != nil:
		s.internal(w, "admin/items/identify/undo", err)
		return
	}
	s.tmdb.RemoveImageFiles(r.Context(), id, dropped...)
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "item.identify_undone", id.String(), nil, s.clientIP(r))
	if ref, err := s.store.ItemRefByID(r.Context(), id); err == nil {
		s.refreshSeriesChildren(r, id, ref.Kind)
	}
	s.respondMetadata(w, r, id)
}

// identifyReset vuelve a lo automático: la ficha se deduce otra vez del nombre de la carpeta, como en el primer escaneo.
func (s *Server) identifyReset(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.identifiableItem(w, r)
	if !ok {
		return
	}
	var req struct {
		RestoreImages bool `json:"restore_images"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	who := authz.FromContext(r.Context())
	p := library.ParseName(path.Base(ref.ContentPath))
	err := s.store.ResetIdentification(r.Context(), ref.ID, store.ResetInput{Title: p.Title, SortTitle: library.SortTitle(p.Title), Year: p.Year,
		ProviderIDs: p.ProviderIDs, RestoreImages: req.RestoreImages, By: who.UserID})
	if err != nil {
		s.internal(w, "admin/items/identify/reset", err)
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "item.identify_reset", ref.ID.String(), map[string]any{"restore_images": req.RestoreImages}, s.clientIP(r))
	refreshed := false
	if s.tmdb.Configured() {
		_, err := s.queue.Enqueue(r.Context(), "item_refresh", map[string]string{"item_id": ref.ID.String(), "mode": "all"}, "refresh:"+ref.ID.String())
		refreshed = err == nil
	}
	if !refreshed { // «Actualizar» ya pone al día temporadas y episodios; sin él, al menos se quitan los datos ajenos
		s.refreshSeriesChildren(r, ref.ID, ref.Kind)
	}
	m, err := s.store.GetEditableMetadata(r.Context(), ref.ID)
	if err != nil {
		s.internal(w, "admin/items/metadata", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metadata": m, "refreshing": refreshed})
}

// metadataThumb sirve una vista previa de TMDB; solo imágenes de verdad (nunca lo que responda el proveedor sin más).
func (s *Server) metadataThumb(w http.ResponseWriter, r *http.Request) {
	size := r.URL.Query().Get("size")
	if size == "" {
		size = "w185"
	}
	if !metadata.ThumbSizes[size] {
		writeError(w, http.StatusBadRequest, "parametro", "Tamaño de imagen no válido.")
		return
	}
	raw, err := s.tmdb.Thumb(r.Context(), size, r.URL.Query().Get("path"))
	if err != nil {
		if strings.Contains(err.Error(), "no válida") {
			writeError(w, http.StatusBadRequest, "parametro", "Ruta de imagen no válida.")
			return
		}
		writeError(w, http.StatusBadGateway, "proveedor", "TMDB no responde ahora mismo.")
		return
	}
	ct := http.DetectContentType(raw)
	if !strings.HasPrefix(ct, "image/") {
		writeError(w, http.StatusBadGateway, "proveedor", "TMDB no ha devuelto una imagen.")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(raw)
}

// refreshSeriesChildren encola la puesta al día de las temporadas y episodios de una serie cuya identificación acaba de
// cambiar (E2): sin esto conservarían los títulos, sinopsis e imágenes de la serie anterior.
func (s *Server) refreshSeriesChildren(r *http.Request, id uuid.UUID, kind string) {
	if kind != "series" {
		return
	}
	if _, err := s.queue.Enqueue(r.Context(), "series_children", map[string]string{"series_id": id.String()}, "children:"+id.String()); err != nil &&
		!errors.Is(err, jobs.ErrDuplicate) {
		slog.Warn("no se pudo encolar la actualización de temporadas y episodios", "serie", id, "error", err)
	}
}
