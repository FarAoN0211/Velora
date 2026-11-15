package api

// E1.3 I2 · «Editar imágenes»: galería de TMDB y subida de imágenes propias. La elección se guarda procesada en
// /config/metadata como fila source = 'user' de item_images: TMDB y el escaneo escriben en otras filas y no la pisan.
// Nunca se escribe junto a los medios (montajes de solo lectura) ni se confía en los bytes recibidos.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"velora/internal/artwork"
	"velora/internal/authz"
	"velora/internal/imaging"
	"velora/internal/metadata"
	"velora/internal/store"
)

func (s *Server) imageAdminRoutes(r chi.Router) {
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/images", authz.Admin, s.itemImages)
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/images/gallery", authz.Admin, s.itemImageGallery)
	s.handle(r, http.MethodGet, "/api/v1/admin/items/{id}/images/preview", authz.Admin, s.previewExternalImage)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/images/select", authz.Admin, s.selectItemImage)
	s.handle(r, http.MethodPost, "/api/v1/admin/items/{id}/images/upload", authz.Admin, s.uploadItemImage)
	s.handle(r, http.MethodDelete, "/api/v1/admin/items/{id}/images/{kind}", authz.Admin, s.restoreItemImage)
}

// imageKindsFor son los tipos de imagen que tienen sentido para cada elemento.
func imageKindsFor(itemKind string) []string {
	switch itemKind {
	case "season":
		return []string{"poster", "thumb"}
	case "episode":
		return []string{"thumb"}
	}
	return []string{"poster", "art", "backdrop", "banner", "box", "boxrear", "disc", "logo", "menu", "thumb"}
}

func (s *Server) imageItem(w http.ResponseWriter, r *http.Request) (store.ItemRef, bool) {
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
		s.internal(w, "admin/items/images", err)
		return ref, false
	}
	return ref, true
}

func (s *Server) respondImages(w http.ResponseWriter, r *http.Request, ref store.ItemRef) {
	sources, rev, err := s.store.ImageSources(r.Context(), ref.ID)
	if err != nil {
		s.internal(w, "admin/items/images", err)
		return
	}
	slots := []store.ImageSlot{}
	for _, k := range imageKindsFor(ref.Kind) {
		slots = append(slots, store.ImageSlot{Kind: k, Source: sources[k]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots, "image_rev": rev, "tmdb_configured": s.tmdb.Configured(),
		"limits": map[string]int{"max_bytes": imaging.MaxUploadBytes, "max_side": imaging.MaxSide, "max_pixels": imaging.MaxPixels}})
}

func (s *Server) itemImages(w http.ResponseWriter, r *http.Request) {
	if ref, ok := s.imageItem(w, r); ok {
		s.respondImages(w, r, ref)
	}
}

// galleryTarget averigua de qué ficha de TMDB salen las imágenes. Temporadas y episodios usan el ID de su serie y sus
// números (provider_ids: tmdb_season, tmdb_episode; los rellenará E2). Sin esos datos no hay galería.
func (s *Server) galleryTarget(r *http.Request, ref store.ItemRef) (metadata.GalleryTarget, bool) {
	tg := metadata.GalleryTarget{Kind: ref.Kind, TMDBID: ref.ProviderIDs["tmdb"]}
	if ref.Kind == "movie" || ref.Kind == "series" {
		return tg, tg.TMDBID != ""
	}
	tg.Season, _ = strconv.Atoi(ref.ProviderIDs["tmdb_season"])
	if ref.ParentID == nil || ref.ProviderIDs["tmdb_season"] == "" {
		return tg, false
	}
	parentID := *ref.ParentID
	if ref.Kind == "episode" {
		tg.Episode, _ = strconv.Atoi(ref.ProviderIDs["tmdb_episode"])
		if ref.ProviderIDs["tmdb_episode"] == "" {
			return tg, false
		}
		season, err := s.store.ItemRefByID(r.Context(), parentID)
		if err != nil || season.ParentID == nil {
			return tg, false
		}
		parentID = *season.ParentID
	}
	series, err := s.store.ItemRefByID(r.Context(), parentID)
	if err != nil || series.ProviderIDs["tmdb"] == "" {
		return tg, false
	}
	tg.TMDBID = series.ProviderIDs["tmdb"]
	return tg, true
}

func (s *Server) itemImageGallery(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.imageItem(w, r)
	if !ok {
		return
	}
	empty := metadata.Gallery{Posters: []metadata.GalleryImage{}, Backdrops: []metadata.GalleryImage{}, Logos: []metadata.GalleryImage{}, Stills: []metadata.GalleryImage{}}
	tg, identified := s.galleryTarget(r, ref)
	if !identified { // sin identificar no hay galería; se puede identificar antes o subir una imagen
		external, warnings := s.externalGallery(r, ref)
		writeJSON(w, http.StatusOK, map[string]any{"gallery": empty, "external": external, "warnings": warnings, "identified": len(ref.ProviderIDs) > 0})
		return
	}
	g, err := s.tmdb.Gallery(r.Context(), tg)
	external, warnings := s.externalGallery(r, ref)
	if err != nil {
		g = empty
		warnings = append(warnings, "TMDB no está disponible; se muestran las otras fuentes.")
	}
	if external == nil {
		external = []artwork.Image{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"gallery": g, "external": external, "warnings": warnings, "identified": true})
}

func imageErrorMessage(err error, kind string) string {
	switch {
	case errors.Is(err, imaging.ErrFormat):
		return "El archivo no es una imagen JPEG, PNG o WebP válida."
	case errors.Is(err, imaging.ErrTooLarge):
		return "La imagen pesa demasiado: el máximo son 10 MB."
	case errors.Is(err, imaging.ErrDimensions):
		return "La imagen es demasiado pequeña o demasiado grande: hasta 6000 píxeles por lado y 40 megapíxeles."
	case errors.Is(err, imaging.ErrRatio):
		if kind == "poster" {
			return "Un cartel debe ser vertical. Recórtala con esa forma antes de guardarla."
		}
		return "Esta imagen no tiene la proporción adecuada. Recórtala antes de guardarla."
	}
	return "Tipo de imagen no válido."
}

// storeImage procesa raw y la guarda como elección manual, de una en una. El archivo es siempre nuevo y se abre con
// os.Root: no se puede salir de /config/metadata. Si algo falla, la imagen anterior sigue siendo la que se muestra.
func (s *Server) storeImage(w http.ResponseWriter, r *http.Request, ref store.ItemRef, kind string, raw []byte, action string) bool {
	select {
	case s.imgSlot <- struct{}{}:
		defer func() { <-s.imgSlot }()
	case <-time.After(10 * time.Second):
		writeError(w, http.StatusServiceUnavailable, "ocupado", "Se está procesando otra imagen. Prueba otra vez en un momento.")
		return false
	case <-r.Context().Done():
		return false
	}
	res, err := imaging.Process(raw, kind)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "imagen_no_valida", imageErrorMessage(err, kind))
		return false
	}
	base := filepath.Join(s.cfg.ConfigDir, "metadata")
	if err := os.MkdirAll(base, 0o750); err != nil {
		s.internal(w, "admin/items/images", err)
		return false
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		s.internal(w, "admin/items/images", err)
		return false
	}
	defer root.Close()
	if err := root.Mkdir(ref.ID.String(), 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		s.internal(w, "admin/items/images", err)
		return false
	}
	rel := filepath.Join(ref.ID.String(), fmt.Sprintf("user-%s-%s.%s", kind, uuid.NewString(), res.Ext))
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		s.internal(w, "admin/items/images", err)
		return false
	}
	_, err = f.Write(res.Data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(rel)
		s.internal(w, "admin/items/images", err)
		return false
	}
	old, err := s.store.SetUserImage(r.Context(), ref.ID, kind, rel)
	if err != nil {
		_ = root.Remove(rel)
		s.internal(w, "admin/items/images", err)
		return false
	}
	if old != "" {
		s.store.RemoveImageFiles(r.Context(), ref.ID, base, old)
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, action, ref.ID.String(), map[string]any{"kind": kind, "width": res.Width, "height": res.Height}, s.clientIP(r))
	return true
}

func (s *Server) selectItemImage(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.imageItem(w, r)
	if !ok {
		return
	}
	var req struct {
		Kind     string `json:"kind"`
		FilePath string `json:"file_path"`
		Provider string `json:"provider"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !slices.Contains(imageKindsFor(ref.Kind), req.Kind) {
		writeError(w, http.StatusUnprocessableEntity, "tipo_no_valido", "Esta ficha no admite ese tipo de imagen.")
		return
	}
	tg, identified := s.galleryTarget(r, ref)
	if !identified && (req.Provider == "" || req.Provider == "tmdb") {
		writeError(w, http.StatusUnprocessableEntity, "sin_identificar", "Esta ficha no está identificada: no hay galería de TMDB.")
		return
	}
	var raw []byte
	var err error
	if req.Provider != "" && req.Provider != "tmdb" {
		raw, err = s.externalImage(r, ref, req.Provider, req.Kind, req.FilePath)
	} else {
		raw, err = s.tmdb.GalleryChoice(r.Context(), tg, req.Kind, req.FilePath)
	}
	if errors.Is(err, metadata.ErrProviderNotFound) || errors.Is(err, imaging.ErrKind) {
		writeError(w, http.StatusUnprocessableEntity, "imagen_no_disponible", "Esa imagen no está disponible para este tipo.")
		return
	}
	if err != nil {
		s.providerError(w, err)
		return
	}
	if s.storeImage(w, r, ref, req.Kind, raw, "item.image_selected") {
		s.respondImages(w, r, ref)
	}
}

func (s *Server) uploadItemImage(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.imageItem(w, r)
	if !ok {
		return
	}
	// Margen para las cabeceras del formulario; el archivo se limita aparte. Se lee en memoria: nunca se guarda sin procesar.
	r.Body = http.MaxBytesReader(w, r.Body, imaging.MaxUploadBytes+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "parametro", "La subida no tiene un formato válido.")
		return
	}
	var kind string
	var raw []byte
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "demasiado_grande", "La imagen pesa demasiado: el máximo son 10 MB.")
			return
		}
		switch part.FormName() {
		case "kind":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			kind = string(b)
		case "file":
			raw, err = io.ReadAll(io.LimitReader(part, imaging.MaxUploadBytes+1))
			if err != nil {
				writeError(w, http.StatusRequestEntityTooLarge, "demasiado_grande", "La imagen pesa demasiado: el máximo son 10 MB.")
				return
			}
		}
		part.Close()
	}
	if !slices.Contains(imageKindsFor(ref.Kind), kind) {
		writeError(w, http.StatusUnprocessableEntity, "tipo_no_valido", "Esta ficha no admite ese tipo de imagen.")
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "parametro", "Falta la imagen.")
		return
	}
	if len(raw) > imaging.MaxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "demasiado_grande", "La imagen pesa demasiado: el máximo son 10 MB.")
		return
	}
	if s.storeImage(w, r, ref, kind, raw, "item.image_uploaded") {
		s.respondImages(w, r, ref)
	}
}

// restoreItemImage vuelve a la imagen automática de ese tipo (la local o la de TMDB).
func (s *Server) restoreItemImage(w http.ResponseWriter, r *http.Request) {
	ref, ok := s.imageItem(w, r)
	if !ok {
		return
	}
	kind := chi.URLParam(r, "kind")
	if !imaging.Valid(kind) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta imagen.")
		return
	}
	file, err := s.store.DeleteUserImage(r.Context(), ref.ID, kind)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "Esta ficha no tiene una imagen propia de ese tipo.")
		return
	}
	if err != nil {
		s.internal(w, "admin/items/images", err)
		return
	}
	s.store.RemoveImageFiles(r.Context(), ref.ID, filepath.Join(s.cfg.ConfigDir, "metadata"), file)
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "item.image_restored", ref.ID.String(), map[string]any{"kind": kind}, s.clientIP(r))
	s.respondImages(w, r, ref)
}
