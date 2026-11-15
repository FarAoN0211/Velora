package api

// E1.1 · Bibliotecas dinámicas (nombre, orden, visibilidad en el menú, varias rutas) y usuarios con permisos por
// biblioteca. Solo administradores (la política de cada ruta lo exige y, si no lo es, responde 404).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

var errPathInvalid = errors.New("ruta no válida")

type updateLibraryRequest struct {
	Name       *string `json:"name"`
	ShowInMenu *bool   `json:"show_in_menu"`
}

// updateLibrary renombra u oculta del menú. Ocultar no es revocar: el acceso y el contenido no cambian.
func (s *Server) updateLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	var req updateLibraryRequest
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || utf8.RuneCountInString(n) > 60 {
			writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "El nombre es obligatorio y no puede superar 60 caracteres.")
			return
		}
		req.Name = &n
	}
	err := s.store.UpdateLibrary(r.Context(), id, req.Name, req.ShowInMenu)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicada", "Ya existe una biblioteca con ese nombre.")
		return
	case err != nil:
		s.internal(w, "libraries/update", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "library.updated", id.String(), map[string]any{"name": req.Name, "show_in_menu": req.ShowInMenu}, s.clientIP(r))
	lib, err := s.store.LibraryByID(r.Context(), id)
	if err != nil {
		s.internal(w, "libraries/update", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"library": lib})
}

func (s *Server) setLibraryOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []uuid.UUID `json:"ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.store.SetLibraryOrder(r.Context(), req.IDs); err != nil {
		s.internal(w, "libraries/order", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addLibraryPath(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	real, msg := s.validateLibraryPath(req.Path)
	if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "ruta_no_valida", msg)
		return
	}
	p, err := s.store.AddLibraryPath(r.Context(), id, real, pathKind(real))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta biblioteca.")
		return
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicada", "Esa carpeta ya pertenece a una biblioteca.")
		return
	case errors.Is(err, store.ErrPathKindChanged):
		writeError(w, http.StatusConflict, "tipo_cambiado", "Esa carpeta estaba quitada y su tipo de montaje ha cambiado. Bórrala definitivamente o revisa el montaje.")
		return
	case err != nil:
		s.internal(w, "libraries/paths", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "library.path_added", id.String(), map[string]any{"path": real}, s.clientIP(r))
	_, _ = s.queue.Enqueue(r.Context(), "library_scan", map[string]string{"library_id": id.String()}, "scan:"+id.String())
	writeJSON(w, http.StatusCreated, map[string]any{"path": p})
}

// removeLibraryPath desvincula un origen: su contenido deja de verse y escanearse, pero fichas, historial y progreso se
// conservan y se recuperan con restoreLibraryPath. Los archivos no se tocan nunca.
func (s *Server) removeLibraryPath(w http.ResponseWriter, r *http.Request) {
	s.pathAction(w, r, "library.path_detached", func(lib, path uuid.UUID) error { return s.store.DetachLibraryPath(r.Context(), lib, path) })
}

// restoreLibraryPath recupera una carpeta quitada tras revalidarla como si se añadiera de nuevo (sigue existiendo dentro
// de las raíces de medios y su tipo de montaje no ha cambiado).
func (s *Server) restoreLibraryPath(w http.ResponseWriter, r *http.Request) {
	s.pathAction(w, r, "library.path_restored", func(lib, path uuid.UUID) error {
		p, err := s.store.DetachedPath(r.Context(), lib, path)
		if err != nil {
			return err
		}
		real, msg := s.validateLibraryPath(p.Path)
		if msg != "" || real != p.Path {
			return errPathInvalid
		}
		if err := s.store.RestoreLibraryPath(r.Context(), lib, path, pathKind(real)); err != nil {
			return err
		}
		_, _ = s.queue.Enqueue(r.Context(), "library_scan", map[string]string{"library_id": lib.String()}, "scan:"+lib.String())
		return nil
	})
}

// purgeLibraryPath borra definitivamente las fichas (y su progreso) de un origen ya desvinculado. Exige confirmación
// explícita en el cuerpo: {"confirm": "BORRAR"}.
func (s *Server) purgeLibraryPath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Confirm != "BORRAR" {
		writeError(w, http.StatusUnprocessableEntity, "sin_confirmar", "Para borrar las fichas definitivamente hay que confirmarlo.")
		return
	}
	s.pathAction(w, r, "library.path_purged", func(lib, path uuid.UUID) error { return s.store.PurgeLibraryPath(r.Context(), lib, path) })
}

func (s *Server) pathAction(w http.ResponseWriter, r *http.Request, event string, fn func(lib, path uuid.UUID) error) {
	id, ok1 := pathUUID(r, "id")
	pathID, ok2 := pathUUID(r, "pathId")
	if !ok1 || !ok2 {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta carpeta.")
		return
	}
	err := fn(id, pathID)
	switch {
	case errors.Is(err, store.ErrPathKindChanged):
		writeError(w, http.StatusConflict, "tipo_cambiado", "El tipo de montaje de esa carpeta ha cambiado (por ejemplo, de local a remoto). Añádela como carpeta nueva.")
		return
	case errors.Is(err, errPathInvalid):
		writeError(w, http.StatusConflict, "ruta_no_valida", "Esa carpeta ya no existe o ya no está dentro de las carpetas de medios.")
		return
	case errors.Is(err, store.ErrLastPath):
		writeError(w, http.StatusConflict, "ultima_ruta", "Una biblioteca necesita al menos una carpeta activa. Para quitarla entera, retira la biblioteca.")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe esta carpeta en ese estado.")
		return
	case err != nil:
		s.internal(w, event, err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, event, id.String(), map[string]any{"path_id": pathID}, s.clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users(r.Context())
	if err != nil {
		s.internal(w, "users", err)
		return
	}
	for i := range users {
		users[i].PINFailures, users[i].PINFailureLast = s.adminPINFailures(r, users[i].ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type createUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

// createUser crea un usuario normal sin acceso a ninguna biblioteca (VELORA_DECISIONES… §4).
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	msg := ""
	switch {
	case !validUsername(req.Username):
		msg = "El usuario debe tener entre 3 y 32 caracteres: letras, números, punto, guion o guion bajo."
	case req.DisplayName == "" || utf8.RuneCountInString(req.DisplayName) > 60:
		msg = "El nombre visible es obligatorio y no puede superar 60 caracteres."
	case utf8.RuneCountInString(req.Password) < auth.MinPasswordLen:
		msg = "La contraseña debe tener al menos 10 caracteres."
	case len(req.Password) > 256:
		msg = "La contraseña es demasiado larga."
	}
	if msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", msg)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, "users/create", err)
		return
	}
	id, err := s.store.CreateUser(r.Context(), req.Username, req.DisplayName, hash)
	if errors.Is(err, store.ErrDuplicate) {
		writeError(w, http.StatusConflict, "duplicado", "Ya existe un usuario con ese nombre.")
		return
	}
	if err != nil {
		s.internal(w, "users/create", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "user.created", req.Username, nil, s.clientIP(r))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// updateUser desactiva o reactiva una cuenta (al desactivarla se cierran sus sesiones) y fija sus límites para todo su
// hogar: reproducciones simultáneas y miembros que puede crear.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	var req struct {
		Disabled *bool `json:"is_disabled"`
		// max_streams: número (0 = sin límite) o null para usar el valor general; ausente, no cambia.
		MaxStreams json.RawMessage `json:"max_streams"`
		Members    *int            `json:"max_household_members"`
		// Permiso para crear (y ampliar) su propio hogar desde «Mi cuenta».
		Household *bool `json:"can_create_household"`
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este usuario.")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Disabled == nil && req.MaxStreams == nil && req.Members == nil && req.Household == nil {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "No hay cambios.")
		return
	}
	if req.MaxStreams != nil || req.Members != nil {
		var streams *int
		if req.MaxStreams != nil && string(req.MaxStreams) != "null" {
			var n int
			if err := json.Unmarshal(req.MaxStreams, &n); err != nil || n < 0 || n > 1000 {
				writeError(w, http.StatusUnprocessableEntity, "limites_reproduccion_invalidos", "Usa de 0 a 1000 reproducciones; 0 significa sin límite.")
				return
			}
			streams = &n
		}
		if req.Members != nil && (*req.Members < 0 || *req.Members > 19) {
			writeError(w, http.StatusUnprocessableEntity, "limite_hogar_invalido", "Un hogar puede tener de 0 a 19 miembros además del responsable.")
			return
		}
		if err := s.store.SetUserLimits(r.Context(), id, req.MaxStreams != nil, streams, req.Members); errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_encontrado", "Los límites se ponen al usuario o al responsable del hogar, no a un perfil familiar ni a un administrador.")
			return
		} else if err != nil {
			s.internal(w, "users/limits", err)
			return
		}
		who := authz.FromContext(r.Context())
		s.audit.Record(r.Context(), &who.UserID, "user.limits_set", id.String(), map[string]any{"max_streams": streams, "max_household_members": req.Members}, s.clientIP(r))
	}
	if req.Household != nil {
		if err := s.store.SetHouseholdPermission(r.Context(), id, *req.Household); errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no_encontrado", "El permiso de hogar se concede a una cuenta principal, no a un perfil familiar.")
			return
		} else if err != nil {
			s.internal(w, "users/household-permission", err)
			return
		}
		who := authz.FromContext(r.Context())
		s.audit.Record(r.Context(), &who.UserID, "user.household_permission", id.String(), map[string]any{"allowed": *req.Household}, s.clientIP(r))
	}
	if req.Disabled == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.SetUserDisabled(r.Context(), id, *req.Disabled); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este usuario (los administradores no se desactivan aquí).")
		return
	} else if err != nil {
		s.internal(w, "users/update", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "user.updated", id.String(), map[string]any{"is_disabled": *req.Disabled}, s.clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

// setUserLibraries fija las bibliotecas que puede ver y usar un usuario. Es efectivo en su siguiente petición, y los
// flujos que tenga abiertos se cortan en la siguiente comprobación periódica (como mucho 30 s).
func (s *Server) setUserLibraries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	var req struct {
		LibraryIDs []uuid.UUID `json:"library_ids"`
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este usuario.")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.LibraryIDs == nil {
		req.LibraryIDs = []uuid.UUID{}
	}
	if err := s.store.SetUserLibraries(r.Context(), id, req.LibraryIDs); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe el usuario o alguna de las bibliotecas.")
		return
	} else if err != nil {
		s.internal(w, "users/libraries", err)
		return
	}
	who := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &who.UserID, "user.libraries_set", id.String(), map[string]any{"libraries": req.LibraryIDs}, s.clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}
