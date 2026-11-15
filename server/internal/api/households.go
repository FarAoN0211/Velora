package api

import (
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"unicode/utf8"
	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

const householdCookie = "velora_household"

func validPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 8 {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func validDeviceCode(code string) bool { return len(code) == 6 && validPIN(code) }

// All profile entry points use the same lock/counters; switching or changing IP
// does not reset the user's PIN attempts.
func (s *Server) verifyProfilePIN(w http.ResponseWriter, r *http.Request, u store.User, pin string) bool {
	if !u.HasPIN {
		return true
	}
	ok := false
	err := s.loginLocks.with(r.Context(), "pin:"+u.ID.String(), s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), "pin:"+u.ID.String(), s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "pin_bloqueado", "Demasiados intentos. Espera unos minutos.")
			return nil
		}
		ok = validPIN(pin) && auth.VerifyPassword(u.PINHash, pin) == nil
		if e = s.store.RecordAttempt(r.Context(), "pin:"+u.ID.String(), s.clientIP(r), "pin", ok); e != nil {
			return e
		}
		if !ok {
			writeError(w, 403, "pin_incorrecto", "Introduce el PIN correcto del perfil.")
		}
		return nil
	})
	if err != nil {
		s.internal(w, "profile/pin", err)
		return false
	}
	return ok
}
func (s *Server) householdError(w http.ResponseWriter, err error, where string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "no_encontrado", "No existe el recurso o el código ya no está disponible.")
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, 409, "duplicado", "Ya existe ese nombre de usuario.")
	case errors.Is(err, store.ErrRateLimited):
		writeError(w, 429, "limite_hogar", "No hay perfiles libres en tu hogar: elimina otro miembro o pide al administrador que amplíe el límite.")
	case errors.Is(err, store.ErrHouseholdNotAllowed):
		writeError(w, 403, "hogar_no_permitido", "El administrador no te ha dado permiso para crear o ampliar un hogar.")
	case errors.Is(err, store.ErrAlreadyInHousehold):
		writeError(w, 409, "ya_en_hogar", "Esta cuenta ya pertenece a un hogar.")
	case errors.Is(err, store.ErrNoPIN):
		writeError(w, 409, "sin_pin", "Este perfil no tiene PIN.")
	case errors.Is(err, store.ErrTrashExpired):
		writeError(w, 410, "papelera_caducada", "Han pasado más de 30 días: este perfil ya no se puede recuperar.")
	default:
		s.internal(w, where, err)
	}
}

// createPasswordlessAccount: el administrador crea una cuenta principal sin contraseña con el código de su dispositivo.
// Ya no crea un hogar; si le concede el permiso, el titular lo creará desde «Mi cuenta».
func (s *Server) createPasswordlessAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code      string      `json:"code"`
		Username  string      `json:"username"`
		Name      string      `json:"display_name"`
		Libraries []uuid.UUID `json:"library_ids"`
		Household bool        `json:"can_create_household"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Username = strings.TrimSpace(req.Username)
	if !validDeviceCode(req.Code) || !validUsername(req.Username) || req.Name == "" || utf8.RuneCountInString(req.Name) > 60 {
		writeError(w, 422, "datos_no_validos", "Indica código de seis dígitos, usuario válido y nombre de hasta 60 caracteres.")
		return
	}
	if req.Libraries == nil {
		req.Libraries = []uuid.UUID{}
	}
	who := authz.FromContext(r.Context())
	var id uuid.UUID
	err := s.loginLocks.with(r.Context(), "link:"+who.UserID.String(), s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), "link:"+who.UserID.String(), s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "demasiados_intentos", "Espera unos minutos antes de probar otro código.")
			return nil
		}
		id, e = s.store.CreatePasswordlessAccount(r.Context(), who.SessionID, auth.HashToken(req.Code), req.Username, req.Name, req.Libraries, req.Household)
		if errors.Is(e, store.ErrNotFound) {
			if re := s.store.RecordAttempt(r.Context(), "link:"+who.UserID.String(), s.clientIP(r), "device_code", false); re != nil {
				return re
			}
		}
		return e
	})
	if err != nil {
		s.householdError(w, err, "account/passwordless")
		return
	}
	if id == uuid.Nil {
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "account.passwordless_created", id.String(), map[string]any{"can_create_household": req.Household}, s.clientIP(r))
	writeJSON(w, 201, map[string]any{"id": id})
}

// createOwnHousehold: «Crear mi hogar». Sin configuraciones: «Familia de <nombre>» con el titular como propietario.
// Este dispositivo queda autorizado para cambiar entre los perfiles del hogar.
func (s *Server) createOwnHousehold(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	if _, err := s.store.CreateOwnHousehold(r.Context(), who.UserID); err != nil {
		s.householdError(w, err, "household/create")
		return
	}
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.internal(w, "household/create", err)
		return
	}
	if fromBrowser(r) || clientKind(r.Header.Get("X-Velora-Client")) == "web" {
		name := limit(r.Header.Get("X-Velora-Device-Name"), 80)
		if name == "" {
			name = "Navegador"
		}
		if err := s.grantHouseholdDevice(w, r, u, who.SessionID, name); err != nil {
			s.internal(w, "household/device", err)
			return
		}
	}
	s.audit.Record(r.Context(), &who.UserID, "household.created", u.HouseholdID.String(), nil, s.clientIP(r))
	writeJSON(w, 201, map[string]any{"user": u})
}

// renameHousehold: el propietario cambia el nombre de su hogar.
func (s *Server) renameHousehold(w http.ResponseWriter, r *http.Request) {
	u, ok := s.householdUser(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 60 {
		writeError(w, 422, "nombre_invalido", "Indica un nombre de hasta 60 caracteres.")
		return
	}
	if err := s.store.RenameHousehold(r.Context(), u.ID, req.Name); err != nil {
		s.householdError(w, err, "household/rename")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "household.renamed", u.HouseholdID.String(), nil, s.clientIP(r))
	w.WriteHeader(204)
}

// memberAction ejecuta una acción del propietario sobre un miembro de su hogar ({id} en la ruta).
func (s *Server) memberAction(w http.ResponseWriter, r *http.Request, what string, run func(owner, id uuid.UUID) error) {
	u, ok := s.householdUser(w, r, true)
	if !ok {
		return
	}
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, 404, "no_encontrado", "No existe el perfil familiar.")
		return
	}
	if err := run(u.ID, id); err != nil {
		s.householdError(w, err, "household/"+what)
		return
	}
	s.audit.Record(r.Context(), &u.ID, "household.profile_"+what, id.String(), nil, s.clientIP(r))
	w.WriteHeader(204)
}

// trashFamilyProfile: «Eliminar» lleva el miembro a la papelera (recuperable durante 30 días).
func (s *Server) trashFamilyProfile(w http.ResponseWriter, r *http.Request) {
	s.memberAction(w, r, "trashed", func(owner, id uuid.UUID) error { return s.store.TrashFamilyProfile(r.Context(), owner, id) })
}

// restoreFamilyProfile: «Recuperar» desde la papelera (exige un hueco libre y el permiso de hogar).
func (s *Server) restoreFamilyProfile(w http.ResponseWriter, r *http.Request) {
	s.memberAction(w, r, "restored", func(owner, id uuid.UUID) error { return s.store.RestoreFamilyProfile(r.Context(), owner, id) })
}

// purgeFamilyProfile: «Eliminar ya» borra definitivamente un miembro que ya está en la papelera.
func (s *Server) purgeFamilyProfile(w http.ResponseWriter, r *http.Request) {
	s.memberAction(w, r, "purged", func(owner, id uuid.UUID) error { return s.store.PurgeFamilyProfile(r.Context(), owner, id) })
}
func (s *Server) householdUser(w http.ResponseWriter, r *http.Request, owner bool) (store.User, bool) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.householdError(w, err, "household/user")
		return u, false
	}
	if u.HouseholdID == nil || (owner && !u.HouseholdOwner) {
		writeError(w, 403, "responsable_requerido", "Esta acción corresponde al responsable de tu hogar.")
		return u, false
	}
	return u, true
}
func (s *Server) household(w http.ResponseWriter, r *http.Request) {
	u, ok := s.householdUser(w, r, false)
	if !ok {
		return
	}
	profiles, err := s.store.HouseholdProfiles(r.Context(), *u.HouseholdID)
	if err != nil {
		s.householdError(w, err, "household/list")
		return
	}
	if !u.HouseholdOwner {
		profiles = []store.User{u}
	}
	devices := []store.HouseholdDeviceSummary{}
	if u.HouseholdOwner {
		devices, err = s.store.HouseholdDevices(r.Context(), *u.HouseholdID)
		if err != nil {
			s.householdError(w, err, "household/devices")
			return
		}
	}
	type row struct {
		store.User
		Disabled bool `json:"is_disabled"`
	}
	out := []row{}
	for _, p := range profiles {
		out = append(out, row{p, p.IsDisabled})
	}
	maxMembers, members, err := s.store.HouseholdMemberLimit(r.Context(), *u.HouseholdID)
	if err != nil {
		s.householdError(w, err, "household/limit")
		return
	}
	name, err := s.store.HouseholdName(r.Context(), *u.HouseholdID)
	if err != nil {
		s.householdError(w, err, "household/name")
		return
	}
	body := map[string]any{"name": name, "profiles": out, "devices": devices, "can_manage": u.HouseholdOwner,
		"max_members": maxMembers, "members": members}
	if u.HouseholdOwner {
		trash, err := s.store.TrashedMembers(r.Context(), *u.HouseholdID)
		if err != nil {
			s.householdError(w, err, "household/trash")
			return
		}
		own, err := s.store.HouseholdStreamLimit(r.Context(), *u.HouseholdID)
		if err != nil {
			s.householdError(w, err, "household/streams")
			return
		}
		streams := s.playbackPreferences(r.Context()).MaxStreamsUser
		if own != nil {
			streams = *own
		}
		if trash == nil {
			trash = []store.TrashedMember{}
		}
		body["trash"] = trash
		body["max_streams"] = streams // 0 = sin límite
		body["can_grow"] = u.CanCreateHousehold
		body["can_add"] = u.CanCreateHousehold && members < maxMembers
	}
	writeJSON(w, 200, body)
}
func (s *Server) createFamilyProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := s.householdUser(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"display_name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 60 {
		writeError(w, 422, "nombre_invalido", "Indica un nombre de hasta 60 caracteres.")
		return
	}
	id, err := s.store.CreateFamilyProfile(r.Context(), u.ID, req.Name)
	if err != nil {
		s.householdError(w, err, "household/profile")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "household.profile_created", id.String(), nil, s.clientIP(r))
	writeJSON(w, 201, map[string]any{"id": id})
}
func (s *Server) updateFamilyProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := s.householdUser(w, r, true)
	if !ok {
		return
	}
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, 404, "no_encontrado", "No existe el perfil.")
		return
	}
	var req struct {
		Disabled *bool   `json:"is_disabled"`
		Reset    bool    `json:"reset_pin"`
		Name     *string `json:"display_name"`
		// PIN del propietario: si lo tiene, restablecer el de un miembro lo exige (otro miembro no puede usar la
		// sesión abierta del propietario para hacerlo).
		OwnerPIN string `json:"owner_pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	target, err := s.store.UserByID(r.Context(), id)
	if err != nil || target.HouseholdID == nil || *target.HouseholdID != *u.HouseholdID || target.HouseholdOwner || target.IsAdmin || target.DeletedAt != nil {
		writeError(w, 404, "no_encontrado", "No existe el perfil familiar.")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || utf8.RuneCountInString(name) > 60 {
			writeError(w, 422, "nombre_invalido", "Indica un nombre de hasta 60 caracteres.")
			return
		}
		err = s.store.RenameFamilyProfile(r.Context(), u.ID, id, name)
	} else if req.Reset {
		// Restablecer con privacidad: el perfil vuelve a poder usarse, sin PIN y vacío; su historial queda sellado
		// (solo el código de recuperación anterior del miembro lo devuelve). El propietario nunca lo ve.
		if u.HasPIN && !s.checkProfileSecret(w, r, u, "pin", func() bool { return validPIN(req.OwnerPIN) && auth.VerifyPassword(u.PINHash, req.OwnerPIN) == nil }) {
			return
		}
		err = s.store.ResetMemberPIN(r.Context(), u.ID, id)
	} else if req.Disabled != nil {
		err = s.store.SetFamilyDisabled(r.Context(), u.ID, id, *req.Disabled)
	} else {
		writeError(w, 422, "sin_cambios", "No hay cambios.")
		return
	}
	if err != nil {
		s.householdError(w, err, "household/update")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "household.profile_updated", id.String(), map[string]any{"reset_pin": req.Reset}, s.clientIP(r))
	w.WriteHeader(204)
}
func (s *Server) removeHouseholdDevice(w http.ResponseWriter, r *http.Request) {
	u, ok := s.householdUser(w, r, true)
	if !ok {
		return
	}
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, 404, "no_encontrado", "No existe el dispositivo.")
		return
	}
	if err := s.store.RemoveHouseholdDevice(r.Context(), *u.HouseholdID, id); err != nil {
		s.householdError(w, err, "household/revoke")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "household.device_revoked", id.String(), nil, s.clientIP(r))
	w.WriteHeader(204)
}
func (s *Server) adminResetPIN(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, 404, "no_encontrado", "No existe el usuario.")
		return
	}
	u, err := s.store.UserByID(r.Context(), id)
	if err != nil {
		s.householdError(w, err, "pin/admin")
		return
	}
	if u.IsAdmin {
		writeError(w, 403, "admin_protegido", "El PIN del administrador se cambia desde su propia cuenta.")
		return
	}
	// El administrador no restablece el PIN de un miembro de un hogar: lo recupera el propio miembro con su código o
	// el propietario (con sellado). Tampoco el de una cuenta con contraseña: su titular lo recupera con ella.
	if u.HouseholdID != nil && !u.HouseholdOwner {
		writeError(w, 403, "miembro_de_hogar", "El PIN de un miembro lo recupera él mismo con su código o el propietario de su hogar.")
		return
	}
	if u.PasswordHash != "" {
		writeError(w, 409, "tiene_contraseña", "Esta cuenta tiene contraseña: su titular recupera el PIN con ella.")
		return
	}
	who := authz.FromContext(r.Context())
	if err = s.store.AdminResetPIN(r.Context(), who.UserID, id); err != nil {
		s.householdError(w, err, "pin/admin-reset")
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "profile.pin_reset", id.String(), map[string]any{"sealed": true}, s.clientIP(r))
	writeJSON(w, 200, map[string]any{"sealed": true})
}
