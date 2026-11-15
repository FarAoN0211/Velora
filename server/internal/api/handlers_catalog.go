package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"velora/internal/authz"
	"velora/internal/imaging"
	"velora/internal/jobs"
	"velora/internal/library"
	"velora/internal/store"
)

func (s *Server) catalogRoutes(r chi.Router) {
	s.handle(r, http.MethodGet, "/api/v1/collections", authz.User, s.collections)
	s.handle(r, http.MethodGet, "/api/v1/collections/{collection}", authz.User, s.collectionDetail)
	s.handle(r, http.MethodGet, "/api/v1/collections/{collection}/poster", authz.User, s.collectionPoster)
	s.handle(r, http.MethodPatch, "/api/v1/admin/collections/{collection}", authz.Admin, s.editCollection)
	s.handle(r, http.MethodGet, "/api/v1/libraries", authz.User, s.listLibraries)
	s.handle(r, http.MethodPost, "/api/v1/libraries", authz.Admin, s.createLibrary)
	s.handle(r, http.MethodPatch, "/api/v1/libraries/{id}", authz.Admin, s.updateLibrary)
	s.handle(r, http.MethodDelete, "/api/v1/libraries/{id}", authz.Admin, s.retireLibrary)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/restore", authz.Admin, s.restoreLibrary)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/purge", authz.Admin, s.purgeLibrary)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/paths", authz.Admin, s.addLibraryPath)
	s.handle(r, http.MethodDelete, "/api/v1/libraries/{id}/paths/{pathId}", authz.Admin, s.removeLibraryPath)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/paths/{pathId}/restore", authz.Admin, s.restoreLibraryPath)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/paths/{pathId}/purge", authz.Admin, s.purgeLibraryPath)
	s.handle(r, http.MethodPut, "/api/v1/admin/library-order", authz.Admin, s.setLibraryOrder)
	s.handle(r, http.MethodGet, "/api/v1/admin/users", authz.Admin, s.listUsers)
	s.handle(r, http.MethodPost, "/api/v1/admin/users", authz.Admin, s.createUser)
	s.handle(r, http.MethodPatch, "/api/v1/admin/users/{id}", authz.Admin, s.updateUser)
	s.handle(r, http.MethodPut, "/api/v1/admin/users/{id}/libraries", authz.Admin, s.setUserLibraries)
	s.handle(r, http.MethodPost, "/api/v1/libraries/{id}/scan", authz.Admin, s.scanLibrary)
	s.handle(r, http.MethodPost, "/api/v1/admin/libraries/scan", authz.Admin, s.scanAllLibraries)
	s.handle(r, http.MethodGet, "/api/v1/admin/scans", authz.Admin, s.listScans)
	s.handle(r, http.MethodGet, "/api/v1/admin/markers", authz.Admin, s.markerScanStatus)
	s.handle(r, http.MethodPost, "/api/v1/admin/markers", authz.Admin, s.markerScanStart)
	s.handle(r, http.MethodDelete, "/api/v1/admin/markers", authz.Admin, s.markerScanStop)
	s.handle(r, http.MethodGet, "/api/v1/admin/media-folders", authz.Admin, s.mediaFolders)
	s.handle(r, http.MethodGet, "/api/v1/admin/metadata/image-providers", authz.Admin, s.imageProviders)
	s.handle(r, http.MethodPut, "/api/v1/admin/metadata/image-providers/{provider}", authz.Admin, s.saveImageProvider)
	s.handle(r, http.MethodDelete, "/api/v1/admin/metadata/image-providers/{provider}", authz.Admin, s.saveImageProvider)
	s.handle(r, http.MethodGet, "/api/v1/admin/metadata/tmdb", authz.Admin, s.tmdbStatus)
	s.handle(r, http.MethodGet, "/api/v1/admin/metadata/opensubtitles", authz.Admin, s.openSubtitlesStatus)
	s.handle(r, http.MethodPut, "/api/v1/admin/metadata/opensubtitles", authz.Admin, s.openSubtitlesSave)
	s.handle(r, http.MethodDelete, "/api/v1/admin/metadata/opensubtitles", authz.Admin, s.openSubtitlesDelete)
	s.handle(r, http.MethodPut, "/api/v1/admin/metadata/tmdb", authz.Admin, s.tmdbSetKey)
	s.handle(r, http.MethodDelete, "/api/v1/admin/metadata/tmdb", authz.Admin, s.tmdbDeleteKey)
	s.handle(r, http.MethodGet, "/api/v1/catalog/revision", authz.User, s.catalogRevision)
	s.handle(r, http.MethodGet, "/api/v1/home", authz.User, s.home)
	s.handle(r, http.MethodGet, "/api/v1/items", authz.User, s.listItems)
	s.handle(r, http.MethodGet, "/api/v1/genres", authz.User, s.listGenres)
	s.handle(r, http.MethodGet, "/api/v1/admin/playback", authz.Admin, s.getPlaybackSettings)
	s.handle(r, http.MethodPut, "/api/v1/admin/playback", authz.Admin, s.setPlaybackSettings)
	s.handle(r, http.MethodGet, "/api/v1/admin/overview", authz.Admin, s.adminOverview)
	s.handle(r, http.MethodGet, "/api/v1/admin/pending-items", authz.Admin, s.pendingItems)
	s.handle(r, http.MethodGet, "/api/v1/items/{id}", authz.User, s.getItem)
	s.handle(r, http.MethodGet, "/api/v1/items/{id}/presentation", authz.User, s.getPresentation)
	s.handle(r, http.MethodGet, "/api/v1/items/{id}/people/{person}/image", authz.User, s.personImage)
	s.handle(r, http.MethodGet, "/api/v1/people/{person}", authz.User, s.getPerson)
	s.handle(r, http.MethodGet, "/api/v1/people/{person}/photo", authz.User, s.personPhoto)
	s.handle(r, http.MethodGet, "/api/v1/items/{id}/images/{kind}", authz.User, s.itemImage)
	s.handle(r, http.MethodPut, "/api/v1/me/items/{id}/progress", authz.User, s.saveProgress)
	s.handle(r, http.MethodPost, "/api/v1/me/items/{id}/played", authz.User, s.markPlayed)
	s.handle(r, http.MethodDelete, "/api/v1/me/items/{id}/played", authz.User, s.markPlayed)
	s.handle(r, http.MethodPost, "/api/v1/me/items/{id}/continue-hidden", authz.User, s.hideContinue)
	s.handle(r, http.MethodDelete, "/api/v1/me/items/{id}/continue-hidden", authz.User, s.hideContinue)
	s.handle(r, http.MethodPost, "/api/v1/playback/decide", authz.User, s.decide)
	s.handle(r, http.MethodPut, "/api/v1/me/libraries/order", authz.User, s.setMyLibraryOrder)
	s.handle(r, http.MethodPut, "/api/v1/me/libraries/{id}/menu", authz.User, s.setMyMenu)
	s.handle(r, http.MethodPut, "/api/v1/me/libraries/{id}/view", authz.User, s.setMyLibraryView)
}

// viewer es quien hace la petición, para filtrar el catálogo por sus bibliotecas permitidas.
func viewer(r *http.Request) store.Viewer {
	who := authz.FromContext(r.Context())
	return store.Viewer{UserID: who.UserID, Admin: who.IsAdmin}
}

func pathUUID(r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	return id, err == nil
}

// menuLibrary es lo que ve un usuario normal de una biblioteca: nunca las rutas del servidor.
type menuLibrary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	SortOrder   int       `json:"sort_order"`
	ShowInMenu  bool      `json:"show_in_menu"`
	Status      string    `json:"status"`
	Items       int       `json:"items"`
	InMenu      bool      `json:"in_menu"`
	ViewSort    string    `json:"view_sort"`
	ViewReverse bool      `json:"view_reverse"`
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	v := viewer(r)
	libs, err := s.store.LibrariesFor(r.Context(), v)
	if err != nil {
		s.internal(w, "libraries", err)
		return
	}
	prefs, err := s.store.MenuPrefs(r.Context(), v.UserID)
	if err != nil {
		s.internal(w, "libraries", err)
		return
	}
	views, err := s.store.LibraryViews(r.Context(), v.UserID)
	if err != nil {
		s.internal(w, "libraries", err)
		return
	}
	for i := range libs {
		libs[i].InMenu = libs[i].ShowInMenu
		if p, ok := prefs[libs[i].ID]; ok {
			libs[i].InMenu = p
		}
		libs[i].ViewSort, libs[i].ViewReverse = "title", false
		if lv, ok := views[libs[i].ID]; ok && librarySorts[lv.Sort] {
			libs[i].ViewSort, libs[i].ViewReverse = lv.Sort, lv.Reverse
		}
	}
	if v.Admin {
		writeJSON(w, http.StatusOK, map[string]any{"libraries": nonNilLibs(libs)})
		return
	}
	out := make([]menuLibrary, 0, len(libs))
	for _, l := range libs {
		out = append(out, menuLibrary{l.ID, l.Name, l.Kind, l.SortOrder, l.ShowInMenu, l.Status, l.Items, l.InMenu, l.ViewSort, l.ViewReverse})
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

func nonNilLibs(l []store.Library) []store.Library {
	if l == nil {
		return []store.Library{}
	}
	return l
}

type createLibraryRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Path string `json:"path"`
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req createLibraryRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 60 {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "El nombre es obligatorio y no puede superar 60 caracteres.")
		return
	}
	if req.Kind == "" {
		req.Kind = "movies"
	}
	if req.Kind != "movies" && req.Kind != "tv" { // «animación» es parte del nombre, no un tipo
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "El tipo debe ser películas o series.")
		return
	}
	real, msg := s.validateLibraryPath(req.Path)
	if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "ruta_no_valida", msg)
		return
	}
	lib, err := s.store.CreateLibrary(r.Context(), req.Name, req.Kind, real, pathKind(real))
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "duplicada", "Ya existe una biblioteca con ese nombre o esa carpeta.")
		return
	}
	if err != nil {
		s.internal(w, "libraries/create", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "library.created", lib.Name, map[string]any{"path": real}, s.clientIP(r))
	_, _ = s.queue.Enqueue(r.Context(), "library_scan", map[string]string{"library_id": lib.ID.String()}, "scan:"+lib.ID.String())
	writeJSON(w, http.StatusCreated, map[string]any{"library": lib})
}

// validateLibraryPath exige una carpeta existente DENTRO de una raíz de medios (resolviendo enlaces simbólicos).
func (s *Server) validateLibraryPath(p string) (string, string) {
	if p == "" || !filepath.IsAbs(p) {
		return "", "Elige una carpeta de la lista de carpetas de medios."
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", "La carpeta no existe o no se puede leer."
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", "La ruta no es una carpeta."
	}
	if library.Within(real, s.cfg.MediaRoots) {
		return real, ""
	}
	return "", "La carpeta debe estar dentro de las carpetas de medios montadas en el contenedor."
}

// pathKind detecta los montajes FUSE (rclone/Teldrive) para tratarlos como remotos (§10.1).
func pathKind(p string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(p, &st); err == nil && st.Type == 0x65735546 {
		return "remote_fuse"
	}
	return "local"
}

// retireLibrary retira la biblioteca (recuperable): deja de verse y escanearse; no se borra nada.
func (s *Server) retireLibrary(w http.ResponseWriter, r *http.Request) {
	s.libraryAction(w, r, "library.retired", s.store.RetireLibrary)
}

// restoreLibrary la recupera con sus permisos, oculta del menú hasta que el administrador la muestre.
func (s *Server) restoreLibrary(w http.ResponseWriter, r *http.Request) {
	s.libraryAction(w, r, "library.restored", s.store.RestoreLibrary)
}

// purgeLibrary borra definitivamente una biblioteca ya retirada. Exige {"confirm": "BORRAR"}.
func (s *Server) purgeLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Confirm != "BORRAR" {
		writeError(w, http.StatusUnprocessableEntity, "sin_confirmar", "Para borrar la biblioteca definitivamente hay que confirmarlo.")
		return
	}
	s.libraryAction(w, r, "library.purged", s.store.PurgeLibrary)
}

func (s *Server) libraryAction(w http.ResponseWriter, r *http.Request, event string, fn func(context.Context, uuid.UUID) error) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if err := fn(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca en ese estado.")
		return
	} else if err != nil {
		s.internal(w, event, err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, event, id.String(), nil, s.clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) scanLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if _, err := s.store.LibraryByID(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	jobID, err := s.queue.Enqueue(r.Context(), "library_scan", map[string]string{"library_id": id.String()}, "scan:"+id.String())
	if errors.Is(err, jobs.ErrDuplicate) {
		writeError(w, http.StatusConflict, "en_curso", "Ya hay un escaneo de esta biblioteca en cola o en curso.")
		return
	}
	if err != nil {
		s.internal(w, "libraries/scan", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": jobID})
}

// scanAllLibraries encola un escaneo por cada biblioteca activa; las que ya tienen uno en cola o en curso se cuentan
// aparte (no se duplican).
func (s *Server) scanAllLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.store.Libraries(r.Context())
	if err != nil {
		s.internal(w, "admin/libraries/scan", err)
		return
	}
	started, already := 0, 0
	for _, l := range libs {
		if l.RetiredAt != nil {
			continue
		}
		_, err := s.queue.Enqueue(r.Context(), "library_scan", map[string]string{"library_id": l.ID.String()}, "scan:"+l.ID.String())
		switch {
		case errors.Is(err, jobs.ErrDuplicate):
			already++
		case err != nil:
			s.internal(w, "admin/libraries/scan", err)
			return
		default:
			started++
		}
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "library.scan_all", "", map[string]any{"started": started, "already": already}, s.clientIP(r))
	writeJSON(w, http.StatusAccepted, map[string]int{"started": started, "already": already})
}

// listScans devuelve el último escaneo de cada biblioteca: en cola, en curso (con su progreso), terminado o fallido.
func (s *Server) listScans(w http.ResponseWriter, r *http.Request) {
	latest, err := s.queue.LatestByPayload(r.Context(), "library_scan", "library_id")
	if err != nil {
		s.internal(w, "admin/scans", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scans": latest})
}

type folder struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"` // local o remote_fuse (rclone/Teldrive)
}

// maxFolderEntries limita el listado (una carpeta de películas puede tener miles de subcarpetas).
const maxFolderEntries = 2000

// mediaFolders lista las carpetas que se pueden añadir a una biblioteca. Sin ?path=, las de primer nivel de cada raíz
// de medios; con ?path=, las subcarpetas de esa carpeta (explorador del panel), que tiene que estar dentro de una raíz
// (se resuelven los enlaces: no se puede salir). Solo lectura: nunca crea ni modifica nada.
func (s *Server) mediaFolders(w http.ResponseWriter, r *http.Request) {
	dirs := s.cfg.MediaRoots
	current, parent := "", ""
	if q := r.URL.Query().Get("path"); q != "" {
		real, msg := s.validateLibraryPath(q)
		if msg != "" {
			writeError(w, http.StatusBadRequest, "ruta_no_valida", msg)
			return
		}
		dirs, current = []string{real}, real
		if up := filepath.Dir(real); library.Within(up, s.cfg.MediaRoots) {
			parent = up
		} // si el padre es la raíz, se vuelve al primer nivel (parent vacío)
	}
	out := []folder{}
	truncated := false
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if len(out) >= maxFolderEntries {
				truncated = true
				break
			}
			p := filepath.Join(dir, e.Name())
			out = append(out, folder{Name: e.Name(), Path: p, Kind: pathKind(p)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{"folders": out, "path": current, "parent": parent, "truncated": truncated})
}

func (s *Server) tmdbStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"configured": s.tmdb.Configured()})
}

func (s *Server) tmdbSetKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.tmdb.SaveKey(r.Context(), req.APIKey); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "clave_no_valida", err.Error())
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "tmdb.key_set", "", nil, s.clientIP(r)) // nunca la clave
	writeJSON(w, http.StatusOK, map[string]bool{"configured": true})
}

func (s *Server) tmdbDeleteKey(w http.ResponseWriter, r *http.Request) {
	if err := s.tmdb.DeleteKey(); err != nil {
		s.internal(w, "tmdb/delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	v := viewer(r)
	cont, err := s.store.ContinueWatching(r.Context(), v, 20)
	if err != nil {
		s.internal(w, "home", err)
		return
	}
	nextUp, err := s.store.NextUpEpisodes(r.Context(), v, 20) // E2: «Siguiente episodio»
	if err != nil {
		s.internal(w, "home", err)
		return
	}
	featured, err := s.store.FeaturedItems(r.Context(), v, 5)
	if err != nil {
		s.internal(w, "home", err)
		return
	}
	// «Novedades» de cada biblioteca con acceso, en el orden de las bibliotecas.
	libs, err := s.store.LibrariesFor(r.Context(), v)
	if err != nil {
		s.internal(w, "home", err)
		return
	}
	type row struct {
		Library menuLibrary      `json:"library"`
		Items   []store.ItemCard `json:"items"`
	}
	rows := []row{}
	for _, l := range libs {
		if l.RetiredAt != nil { // ocultar del menú no la quita del inicio (E1.1)
			continue
		}
		lib := l.ID
		items, err := s.store.Items(r.Context(), v, store.ItemQuery{LibraryID: &lib, Sort: "added", Limit: 20})
		if err != nil {
			s.internal(w, "home", err)
			return
		}
		if len(items) > 0 {
			rows = append(rows, row{menuLibrary{l.ID, l.Name, l.Kind, l.SortOrder, l.ShowInMenu, l.Status, l.Items, l.ShowInMenu, "", false}, items})
		}
	}
	if featured == nil {
		featured = []store.Featured{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"featured": featured, "continue": nonNil(cont), "next_up": nonNil(nextUp), "rows": rows})
}

func nonNil(c []store.ItemCard) []store.ItemCard {
	if c == nil {
		return []store.ItemCard{}
	}
	return c
}

// libraryParam lee library_id (opcional). false si no es válido (ya respondido).
func libraryParam(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	v := r.URL.Query().Get("library_id")
	if v == "" {
		return nil, true
	}
	id, err := uuid.Parse(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parametro", "library_id no es válido.")
		return nil, false
	}
	return &id, true
}

func clip(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) > n {
		v = v[:n]
	}
	return v
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lib, ok := libraryParam(w, r)
	if !ok {
		return
	}
	iq := store.ItemQuery{LibraryID: lib, Search: clip(q.Get("q"), 100), Genre: clip(q.Get("genre"), 100),
		Sort: q.Get("sort"), Desc: q.Get("order") == "reverse", Played: q.Get("played")}
	if iq.Sort == "" {
		iq.Sort = "title"
	}
	if !store.ValidSort(iq.Sort) {
		writeError(w, http.StatusBadRequest, "parametro", "El orden pedido no existe.")
		return
	}
	if iq.Played != "" && iq.Played != "played" && iq.Played != "unplayed" {
		writeError(w, http.StatusBadRequest, "parametro", "El filtro de vistas no es válido.")
		return
	}
	iq.Limit, _ = strconv.Atoi(q.Get("limit"))
	if iq.Limit <= 0 || iq.Limit > 200 {
		iq.Limit = 100
	}
	iq.Offset, _ = strconv.Atoi(q.Get("offset"))
	if iq.Offset < 0 {
		iq.Offset = 0
	}
	if person := q.Get("person"); person != "" {
		iq.PersonID, _ = strconv.Atoi(person)
		if iq.PersonID <= 0 {
			writeError(w, 400, "parametro", "La persona no es válida.")
			return
		}
		if s.tmdb.Configured() {
			var err error
			iq.PersonMovies, iq.PersonSeries, err = s.tmdb.PersonCredits(r.Context(), iq.PersonID)
			if err != nil {
				writeError(w, 503, "metadatos_no_disponibles", "No se ha podido consultar la filmografía. Vuelve a intentarlo.")
				return
			}
		}
	}
	items, err := s.store.Items(r.Context(), viewer(r), iq)
	if err != nil {
		s.internal(w, "items", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

// listGenres: géneros de las películas que el usuario puede ver (para el filtro de la biblioteca).
func (s *Server) listGenres(w http.ResponseWriter, r *http.Request) {
	lib, ok := libraryParam(w, r)
	if !ok {
		return
	}
	genres, err := s.store.Genres(r.Context(), viewer(r), lib)
	if err != nil {
		s.internal(w, "genres", err)
		return
	}
	if genres == nil {
		genres = []store.GenreCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"genres": genres})
}

// pendingItems: películas sin archivo principal (carpetas con solo su tráiler), ocultas a los usuarios.
func (s *Server) pendingItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.PendingItems(r.Context())
	if err != nil {
		s.internal(w, "admin/pending-items", err)
		return
	}
	if items == nil {
		items = []store.PendingItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// adminOverview: resumen del panel de control (versión, recuento, reproducciones optimizadas y últimos escaneos).
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.Overview(r.Context())
	if err != nil {
		s.internal(w, "admin/overview", err)
		return
	}
	scans, err := s.queue.Recent(r.Context(), "library_scan", 5)
	if err != nil {
		s.internal(w, "admin/overview", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "counts": counts, "tmdb_configured": s.tmdb.Configured(),
		"optimized_streams": s.remux.InUse(), "max_optimized_streams": s.cfg.MaxRemux, "scans": scans,
	})
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	item, err := s.store.Item(r.Context(), viewer(r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	if err != nil {
		s.internal(w, "items/get", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "tmdb_attribution": s.tmdb.Configured()})
}

// itemImage sirve una imagen abriéndola SIEMPRE con os.Root (biblioteca o /config/metadata): no se puede salir.
func (s *Server) itemImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	kind := chi.URLParam(r, "kind")
	if !ok || !imaging.Valid(kind) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta imagen.")
		return
	}
	img, err := s.store.ImageFor(r.Context(), viewer(r), id, kind)
	if err != nil {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta imagen.")
		return
	}
	if s.serveThumbnail(w, r, img, kind) {
		return
	}
	var root *os.Root
	if img.Source == "local" {
		root, err = library.OpenRoot(img.LibPath, s.cfg.MediaRoots)
	} else {
		root, err = os.OpenRoot(filepath.Join(s.cfg.ConfigDir, "metadata"))
	}
	if err != nil {
		writeError(w, http.StatusNotFound, "no_encontrado", "La imagen no está disponible ahora.")
		return
	}
	defer root.Close()
	f, err := root.Open(img.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "no_encontrado", "La imagen no está disponible ahora.")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusNotFound, "no_encontrado", "La imagen no está disponible ahora.")
		return
	}
	// Con versión (?v=, de image_rev) la URL cambia con la imagen y se puede guardar; sin ella hay que revalidar siempre:
	// nadie puede quedarse con la imagen anterior tras una elección manual.
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "private, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.Header().Set("ETag", fmt.Sprintf(`"%s-%x-%x"`, img.Source, st.ModTime().UnixNano(), st.Size()))
	http.ServeContent(w, r, filepath.Base(img.Path), st.ModTime(), f)
}

type progressRequest struct {
	FileID     *uuid.UUID `json:"file_id"` // lo que se está viendo; si es un tráiler o extra, no se guarda progreso
	PositionMS int64      `json:"position_ms"`
	DurationMS int64      `json:"duration_ms"`
	ClientTime *time.Time `json:"client_time"`
}

func (s *Server) saveProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	var req progressRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.PositionMS < 0 || req.DurationMS < 0 {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "Posición no válida.")
		return
	}
	if ok, err := s.store.ItemVisible(r.Context(), viewer(r), id); err != nil || !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	if req.FileID != nil {
		if extra, err := s.store.IsExtra(r.Context(), *req.FileID); err != nil {
			s.internal(w, "progress", err)
			return
		} else if extra {
			writeError(w, http.StatusBadRequest, "extra_sin_progreso", "Los tráileres y extras no guardan progreso.")
			return
		}
	}
	at := time.Now()
	if req.ClientTime != nil && req.ClientTime.Before(at.Add(time.Minute)) {
		at = *req.ClientTime
	}
	who := authz.FromContext(r.Context())
	if err := s.store.SaveProgress(r.Context(), who.UserID, id, req.PositionMS, req.DurationMS, at); err != nil {
		s.internal(w, "progress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) markPlayed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if visible, err := s.store.ItemVisible(r.Context(), viewer(r), id); !ok || err != nil || !visible {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	who := authz.FromContext(r.Context())
	if err := s.store.SetPlayed(r.Context(), who.UserID, id, r.Method == http.MethodPost); err != nil {
		s.internal(w, "played", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) hideContinue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if visible, err := s.store.ItemVisible(r.Context(), viewer(r), id); !ok || err != nil || !visible {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este título.")
		return
	}
	who := authz.FromContext(r.Context())
	if err := s.store.HideContinue(r.Context(), who.UserID, id, r.Method == http.MethodPost); err != nil {
		s.internal(w, "continue-hidden", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// librarySorts son los órdenes que se pueden recordar (los mismos que ofrecen la web y la TV).
var librarySorts = map[string]bool{"title": true, "added": true, "year": true, "rating": true, "runtime": true, "last_played": true, "resolution": true}

// setMyLibraryView: cada usuario fija el orden con el que ve una biblioteca; se recuerda al volver y en otros aparatos.
func (s *Server) setMyLibraryView(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	var req struct {
		Sort    string `json:"sort"`
		Reverse bool   `json:"reverse"`
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !librarySorts[req.Sort] {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "Ese orden no existe.")
		return
	}
	v := viewer(r)
	libs, err := s.store.LibrariesFor(r.Context(), v)
	if err != nil {
		s.internal(w, "me/view", err)
		return
	}
	found := false
	for _, l := range libs {
		found = found || (l.ID == id && l.RetiredAt == nil)
	}
	if !found {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if err := s.store.SetLibraryView(r.Context(), v.UserID, id, store.LibraryView{Sort: req.Sort, Reverse: req.Reverse}); err != nil {
		s.internal(w, "me/view", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setMyMenu: cada usuario decide qué bibliotecas con acceso ve en SU barra lateral. No cambia permisos ni el inicio.
func (s *Server) setMyMenu(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	var req struct {
		Visible *bool `json:"visible"`
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Visible == nil {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "Indica si quieres verla en el menú.")
		return
	}
	v := viewer(r)
	libs, err := s.store.LibrariesFor(r.Context(), v)
	if err != nil {
		s.internal(w, "me/menu", err)
		return
	}
	found := false
	for _, l := range libs {
		found = found || (l.ID == id && l.RetiredAt == nil)
	}
	if !found { // sin acceso, retirada o inexistente: 404, como el resto del catálogo
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if err := s.store.SetMenuPref(r.Context(), v.UserID, id, *req.Visible); err != nil {
		s.internal(w, "me/menu", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setMyLibraryOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.IDs) > 1000 {
		writeError(w, 422, "orden_invalido", "Demasiadas bibliotecas.")
		return
	}
	v := viewer(r)
	libs, err := s.store.LibrariesFor(r.Context(), v)
	if err != nil {
		s.internal(w, "menu/order", err)
		return
	}
	allowed := map[uuid.UUID]bool{}
	for _, l := range libs {
		if l.RetiredAt == nil {
			allowed[l.ID] = true
		}
	}
	seen := map[uuid.UUID]bool{}
	for _, id := range req.IDs {
		if !allowed[id] || seen[id] {
			writeError(w, 422, "orden_invalido", "El orden contiene bibliotecas repetidas o sin acceso.")
			return
		}
		seen[id] = true
	}
	raw, _ := json.Marshal(req.IDs)
	_, err = s.store.Pool.Exec(r.Context(), `with changed as(update users set preferences=jsonb_set(coalesce(preferences,'{}'::jsonb),'{menu_order}',$2::jsonb) where id=$1 returning id) update catalog_revision set revision=revision+1 where singleton and exists(select 1 from changed)`, v.UserID, raw)
	if err != nil {
		s.internal(w, "menu/order", err)
		return
	}
	w.WriteHeader(204)
}
