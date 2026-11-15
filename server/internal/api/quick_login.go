package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"math/big"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

func (s *Server) startDeviceLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Name string `json:"device_name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		s.internal(w, "device/start", err)
		return
	}
	for n := 0; n < 5; n++ {
		number, e := rand.Int(rand.Reader, big.NewInt(1000000))
		if e != nil {
			s.internal(w, "device/start", e)
			return
		}
		code := number.String()
		code = strings.Repeat("0", 6-len(code)) + code
		name := limit(req.Name, 80)
		if name == "" {
			name = "Dispositivo Velora"
		}
		expires, e := s.store.CreateDeviceAuthorization(r.Context(), auth.HashToken(code), hash, name, s.clientIP(r))
		if errors.Is(e, store.ErrDuplicate) {
			continue
		}
		if errors.Is(e, store.ErrRateLimited) {
			w.Header().Set("Retry-After", "300")
			writeError(w, 429, "limite_vinculacion", "Hay demasiadas solicitudes. Espera unos minutos.")
			return
		}
		if e != nil {
			s.internal(w, "device/start", e)
			return
		}
		writeJSON(w, 201, map[string]any{"device_token": token, "user_code": code, "expires_at": expires, "interval": 2})
		return
	}
	writeError(w, 503, "ocupado", "No se ha podido generar un código. Inténtalo de nuevo.")
}
func (s *Server) pollDeviceLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Token string `json:"device_token"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Token) != 43 {
		writeError(w, 400, "codigo_invalido", "Solicitud no válida.")
		return
	}
	u, session, err := s.store.ClaimDeviceAuthorization(r.Context(), auth.HashToken(req.Token))
	if errors.Is(err, store.ErrRateLimited) {
		w.Header().Set("Retry-After", "2")
		writeError(w, 429, "espera", "Espera antes de consultar de nuevo.")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 410, "codigo_caducado", "El código ha caducado o ya se ha usado.")
		return
	}
	if err != nil {
		s.internal(w, "device/poll", err)
		return
	}
	if u == nil {
		writeJSON(w, 200, map[string]string{"status": "pending"})
		return
	}
	s.startSessionUsing(w, r, *u, 200, session)
	s.audit.Record(r.Context(), &u.ID, "device.linked", "", nil, s.clientIP(r))
}

// Serializes six-digit lookups/approval by IP and account; unknown/expired codes share a response.
func (s *Server) deviceLookup(w http.ResponseWriter, r *http.Request, approve bool) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Code   string     `json:"code"`
		UserID *uuid.UUID `json:"user_id"`
		PIN    string     `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	who := authz.FromContext(r.Context())
	actor, e := s.store.UserByID(r.Context(), who.UserID)
	if e != nil {
		s.internal(w, "device/actor", e)
		return
	}
	if actor.HouseholdID != nil && !actor.HouseholdOwner {
		writeError(w, 403, "responsable_requerido", "Solo el responsable puede vincular dispositivos.")
		return
	}
	target := actor
	if approve {
		if req.UserID != nil && *req.UserID != actor.ID {
			target, e = s.store.UserByID(r.Context(), *req.UserID)
			if e != nil || target.IsDisabled || target.DeletedAt != nil || (!actor.IsAdmin && (!actor.HouseholdOwner || target.HouseholdID == nil || actor.HouseholdID == nil || *target.HouseholdID != *actor.HouseholdID)) {
				writeError(w, 404, "no_encontrado", "No existe el perfil.")
				return
			}
		}
		if !s.verifyProfilePIN(w, r, target, req.PIN) {
			return
		}
	}
	code := strings.TrimSpace(req.Code)
	valid := len(code) == 6
	for _, c := range code {
		valid = valid && c >= '0' && c <= '9'
	}
	err := s.loginLocks.with(r.Context(), "link:"+who.UserID.String(), s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), "link:"+who.UserID.String(), s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "demasiados_intentos", "Espera unos minutos antes de probar otro código.")
			return nil
		}
		if !valid {
			writeError(w, 400, "codigo_invalido", "Introduce los seis dígitos.")
			return nil
		}
		a, e := s.store.DeviceAuthorization(r.Context(), auth.HashToken(code))
		if errors.Is(e, store.ErrNotFound) {
			_ = s.store.RecordAttempt(r.Context(), "link:"+who.UserID.String(), s.clientIP(r), "device_code", false)
			writeError(w, 404, "codigo_invalido", "El código no existe, ha caducado o ya se ha autorizado.")
			return nil
		}
		if e != nil {
			return e
		}
		if approve {
			if e = s.store.ApproveDeviceForUser(r.Context(), auth.HashToken(code), who.SessionID, target.ID, target.PINHash); e != nil {
				if errors.Is(e, store.ErrNotFound) {
					writeError(w, 409, "codigo_usado", "El código ya no está disponible.")
					return nil
				}
				return e
			}
			s.audit.Record(r.Context(), &who.UserID, "device.approved", "", nil, s.clientIP(r))
			w.WriteHeader(204)
		} else {
			writeJSON(w, 200, a)
		}
		return nil
	})
	if err != nil {
		s.internal(w, "device/authorize", err)
	}
}
func (s *Server) inspectDeviceLogin(w http.ResponseWriter, r *http.Request) {
	s.deviceLookup(w, r, false)
}
func (s *Server) approveDeviceLogin(w http.ResponseWriter, r *http.Request) {
	s.deviceLookup(w, r, true)
}

func validNewPassword(p string) bool {
	return utf8.RuneCountInString(p) >= auth.MinPasswordLen && len(p) <= 256
}
func recoveryHash(code string) []byte {
	return auth.HashToken(strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", "")))
}
func (s *Server) verifySensitivePassword(w http.ResponseWriter, r *http.Request, password string) (string, bool) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.internal(w, "security/reauth", err)
		return "", false
	}
	ok := false
	err = s.loginLocks.with(r.Context(), "security:"+u.Username, s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), "security:"+u.Username, s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "demasiados_intentos", "Espera unos minutos y vuelve a intentarlo.")
			return nil
		}
		ok = auth.VerifyPassword(u.PasswordHash, password) == nil
		if e = s.store.RecordAttempt(r.Context(), "security:"+u.Username, s.clientIP(r), "password", ok); e != nil {
			return e
		}
		if !ok {
			writeError(w, 401, "credenciales", "La contraseña no es correcta.")
		}
		return nil
	})
	if err != nil {
		s.internal(w, "security/reauth", err)
		return "", false
	}
	return u.PasswordHash, ok
}
func (s *Server) generateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	verified, ok := s.verifySensitivePassword(w, r, req.Password)
	if !ok {
		return
	}
	codes := []string{}
	hashes := [][]byte{}
	for i := 0; i < 10; i++ {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			s.internal(w, "recovery/generate", err)
			return
		}
		code := strings.ToUpper(hex.EncodeToString(raw))
		hashes = append(hashes, recoveryHash(code))
		codes = append(codes, code[:8]+"-"+code[8:16]+"-"+code[16:24]+"-"+code[24:])
	}
	who := authz.FromContext(r.Context())
	if err := s.store.SetRecoveryCodes(r.Context(), who.UserID, hashes, 365*24*time.Hour, verified); err != nil {
		s.internal(w, "recovery/generate", err)
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "recovery.generated", "", nil, s.clientIP(r))
	writeJSON(w, 200, map[string]any{"codes": codes, "expires_in_days": 365})
}
func (s *Server) recoverPassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !validNewPassword(req.Password) {
		writeError(w, 422, "password_invalido", "Usa una contraseña de 10 a 256 caracteres.")
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Username))
	if len(name) > 32 || len(req.Code) > 100 {
		writeError(w, 400, "datos_invalidos", "Comprueba el usuario y el código.")
		return
	}
	err := s.loginLocks.with(r.Context(), "recovery:"+name, s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), "recovery:"+name, s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "demasiados_intentos", "Espera unos minutos y vuelve a intentarlo.")
			return nil
		}
		hash, e := auth.HashPassword(req.Password)
		if e != nil {
			return e
		}
		e = s.store.ResetPasswordWithRecovery(r.Context(), name, recoveryHash(req.Code), hash)
		if errors.Is(e, store.ErrNotFound) {
			_ = s.store.RecordAttempt(r.Context(), "recovery:"+name, s.clientIP(r), "password", false)
			writeError(w, 400, "recuperacion_invalida", "El usuario o el código no son válidos, o el código ha caducado.")
			return nil
		}
		if e != nil {
			return e
		}
		_ = s.store.RecordAttempt(r.Context(), "recovery:"+name, s.clientIP(r), "password", true)
		s.audit.Record(r.Context(), nil, "password.recovered", name, nil, s.clientIP(r))
		w.WriteHeader(204)
		return nil
	})
	if err != nil {
		s.internal(w, "recovery/reset", err)
	}
}
func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password      string `json:"password"`
		AdminPassword string `json:"admin_password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	uid, ok := pathUUID(r, "id")
	if !ok {
		return
	}
	if !validNewPassword(req.Password) {
		writeError(w, 422, "password_invalido", "Usa una contraseña de 10 a 256 caracteres.")
		return
	}
	if _, ok := s.verifySensitivePassword(w, r, req.AdminPassword); !ok {
		return
	}
	u, err := s.store.UserByID(r.Context(), uid)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "no_encontrado", "No existe ese usuario.")
		return
	}
	if err != nil {
		s.internal(w, "admin/password", err)
		return
	}
	if u.IsAdmin {
		writeError(w, 400, "administrador", "Para una cuenta administradora utiliza sus códigos de recuperación o el servidor.")
		return
	}
	// Si la cuenta tiene PIN, su historial se sella: el administrador conoce la contraseña nueva y con ella no debe
	// poder ver el historial protegido de esa persona.
	who := authz.FromContext(r.Context())
	hash, err := auth.HashPassword(req.Password)
	sealed := false
	if err == nil {
		sealed, err = s.store.ResetUserPasswordByAdmin(r.Context(), who.UserID, uid, hash)
	}
	if err != nil {
		s.internal(w, "admin/password", err)
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "password.admin_reset", uid.String(), map[string]any{"sealed": sealed}, s.clientIP(r))
	writeJSON(w, 200, map[string]any{"sealed": sealed})
}
