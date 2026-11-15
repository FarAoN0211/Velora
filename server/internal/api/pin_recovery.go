package api

import (
	"crypto/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

// Fase 3 de replica/PLAN_HOGARES_Y_PIN.md: PIN de 4 dígitos, código de recuperación y historial sellado.
//
// - El PIN nuevo tiene exactamente 4 dígitos (los antiguos de 4 a 8 siguen valiendo hasta que se cambien).
// - Al poner el PIN se muestra UNA vez un código de recuperación de 8 caracteres; solo se guarda su hash (argon2id).
// - «He olvidado mi PIN»: el código (o la contraseña de la cuenta, verificada en el momento) pone un PIN nuevo y
//   conserva todo. Una sesión abierta nunca basta para cambiar el PIN sin una de esas pruebas.
// - Cualquier restablecimiento hecho por otra persona sella el historial; solo el código anterior lo devuelve.

// newPINValid: un PIN nuevo tiene exactamente 4 dígitos.
func newPINValid(pin string) bool { return len(pin) == 4 && validPIN(pin) }

// Alfabeto del código de recuperación: sin 0/O, 1/I/L ni U, para dictarlo y copiarlo sin errores.
const recoveryAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// newRecoveryCode devuelve un código «XXXX-XXXX» y su hash para guardar.
func newRecoveryCode() (string, []byte, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	b := make([]byte, 8)
	for i, v := range raw {
		b[i] = recoveryAlphabet[int(v)%len(recoveryAlphabet)]
	}
	code := string(b[:4]) + "-" + string(b[4:])
	hash, err := auth.HashPassword(normalizeRecoveryCode(code))
	return code, []byte(hash), err
}

func normalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// checkProfileSecret comprueba un secreto del perfil u (PIN, código o contraseña) con el contador común de intentos
// del perfil: cambiar de método, de dispositivo o de IP no da intentos nuevos. Escribe el error si falla. kind es el
// tipo que se anota («pin» también para el código de recuperación, que protege lo mismo; «password» para la cuenta).
func (s *Server) checkProfileSecret(w http.ResponseWriter, r *http.Request, u store.User, kind string, ok func() bool) bool {
	key := "pin:" + u.ID.String()
	good := false
	err := s.loginLocks.with(r.Context(), key, s.clientIP(r), func() error {
		wait, e := s.store.LockedFor(r.Context(), key, s.clientIP(r))
		if e != nil {
			return e
		}
		if wait > 0 {
			writeError(w, 429, "pin_bloqueado", "Demasiados intentos. Espera unos minutos.")
			return nil
		}
		good = ok()
		if e = s.store.RecordAttempt(r.Context(), key, s.clientIP(r), kind, good); e != nil {
			return e
		}
		if !good {
			writeError(w, 403, "credencial_incorrecta", "No es correcto. Comprueba lo que has escrito.")
		}
		return nil
	})
	if err != nil {
		s.internal(w, "profile/secret", err)
		return false
	}
	return good
}

// profilePIN: activar, cambiar o quitar el PIN del perfil propio. Exige el PIN actual o, si se ha olvidado y la cuenta
// tiene contraseña, la contraseña (verificada ahora). Al activarlo o cambiarlo devuelve un código de recuperación nuevo.
func (s *Server) profilePIN(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.householdError(w, err, "pin/user")
		return
	}
	var req struct {
		Current  string `json:"current_pin"`
		Password string `json:"password"`
		PIN      string `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.PIN != "" && !newPINValid(req.PIN) {
		writeError(w, 422, "pin_invalido", "El PIN debe tener 4 dígitos.")
		return
	}
	if req.Password != "" {
		if u.PasswordHash == "" {
			writeError(w, 422, "sin_contraseña", "Esta cuenta no tiene contraseña: usa tu código de recuperación.")
			return
		}
		if !s.checkProfileSecret(w, r, u, "password", func() bool { return auth.VerifyPassword(u.PasswordHash, req.Password) == nil }) {
			return
		}
	} else if u.HasPIN && !s.checkProfileSecret(w, r, u, "pin", func() bool { return validPIN(req.Current) && auth.VerifyPassword(u.PINHash, req.Current) == nil }) {
		return
	}
	pinHash, code := "", ""
	var recovery []byte
	if req.PIN != "" {
		if pinHash, err = auth.HashPassword(req.PIN); err == nil {
			code, recovery, err = newRecoveryCode()
		}
		if err != nil {
			s.internal(w, "pin/hash", err)
			return
		}
	}
	if err = s.store.SetPINWithRecovery(r.Context(), u.ID, who.SessionID, u.PINHash, pinHash, recovery); err != nil {
		s.householdError(w, err, "pin/change")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "profile.pin_changed", u.ID.String(), nil, s.clientIP(r))
	if code == "" {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"recovery_code": code})
}

// regenerateRecoveryCode: un código nuevo (el anterior deja de valer). Exige el PIN actual.
func (s *Server) regenerateRecoveryCode(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.householdError(w, err, "pin/user")
		return
	}
	var req struct {
		Current string `json:"current_pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !u.HasPIN {
		writeError(w, 409, "sin_pin", "Activa primero un PIN.")
		return
	}
	if !s.checkProfileSecret(w, r, u, "pin", func() bool { return validPIN(req.Current) && auth.VerifyPassword(u.PINHash, req.Current) == nil }) {
		return
	}
	code, recovery, err := newRecoveryCode()
	if err == nil {
		err = s.store.SetPINWithRecovery(r.Context(), u.ID, who.SessionID, u.PINHash, u.PINHash, recovery)
	}
	if err != nil {
		s.internal(w, "pin/recovery-code", err)
		return
	}
	s.audit.Record(r.Context(), &u.ID, "profile.recovery_code_renewed", u.ID.String(), nil, s.clientIP(r))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"recovery_code": code})
}

// recoverPIN: «He olvidado mi PIN» desde la pantalla de perfiles. Con el código de recuperación se elige un PIN nuevo,
// se conserva todo y se entra en el perfil. Solo para perfiles seleccionables en ESTE dispositivo (hogar autorizado o
// cuenta recordada): el código no abre perfiles desde cualquier sitio.
func (s *Server) recoverPIN(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		User uuid.UUID `json:"user_id"`
		Code string    `json:"recovery_code"`
		PIN  string    `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !newPINValid(req.PIN) {
		writeError(w, 422, "pin_invalido", "El PIN nuevo debe tener 4 dígitos.")
		return
	}
	target, err := s.store.UserByID(r.Context(), req.User)
	if err != nil || target.IsDisabled || target.DeletedAt != nil {
		writeError(w, 403, "perfil_no_disponible", "Este perfil no está disponible en este dispositivo.")
		return
	}
	// ¿Se puede seleccionar aquí? Hogar autorizado en este dispositivo, o cuenta recordada en él.
	var grant uuid.UUID
	var remembered *rememberedProfile
	if target.HouseholdID != nil {
		if c, e := r.Cookie(householdCookie); e == nil && len(c.Value) == 43 {
			if g, hid, e := s.store.HouseholdDevice(r.Context(), auth.HashToken(c.Value)); e == nil && hid == *target.HouseholdID {
				grant = g
			}
		}
	} else {
		for _, p := range s.rememberedProfiles(r) {
			if p.user.ID == target.ID {
				p := p
				remembered = &p
			}
		}
	}
	if grant == uuid.Nil && remembered == nil {
		writeError(w, 403, "perfil_no_disponible", "Este perfil no está disponible en este dispositivo.")
		return
	}
	if len(target.PINRecoveryHash) == 0 {
		writeError(w, 409, "sin_codigo", "Este perfil no tiene código de recuperación. Pide al propietario del hogar que restablezca tu PIN.")
		return
	}
	if !s.checkProfileSecret(w, r, target, "pin", func() bool {
		return auth.VerifyPassword(string(target.PINRecoveryHash), normalizeRecoveryCode(req.Code)) == nil
	}) {
		return
	}
	pinHash, err := auth.HashPassword(req.PIN)
	if err != nil {
		s.internal(w, "pin/recover", err)
		return
	}
	code, recovery, err := newRecoveryCode()
	if err != nil {
		s.internal(w, "pin/recover", err)
		return
	}
	keep := uuid.Nil
	if remembered != nil {
		keep = remembered.session
	}
	if err = s.store.SetPINWithRecovery(r.Context(), target.ID, keep, target.PINHash, pinHash, recovery); err != nil {
		s.householdError(w, err, "pin/recover")
		return
	}
	s.audit.Record(r.Context(), &target.ID, "profile.pin_recovered", target.ID.String(), nil, s.clientIP(r))
	target.PINHash, target.HasPIN = pinHash, true
	extra := map[string]any{"recovery_code": code}
	if remembered != nil {
		remembered.user = target
		s.useRememberedProfile(w, r, *remembered, extra)
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		s.internal(w, "pin/recover", err)
		return
	}
	session, err := s.store.CreateHouseholdSession(r.Context(), grant, target.ID, pinHash, hash, s.cfg.SessionTTL)
	if err != nil {
		s.householdError(w, err, "pin/recover")
		return
	}
	s.writeProfileSessionWith(w, r, token, session, target, extra)
}

// historySeals: los historiales sellados del perfil propio (fecha, caducidad y número de títulos).
func (s *Server) historySeals(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	seals, err := s.store.Seals(r.Context(), who.UserID)
	if err != nil {
		s.internal(w, "seals", err)
		return
	}
	if seals == nil {
		seals = []store.Seal{}
	}
	writeJSON(w, 200, map[string]any{"seals": seals})
}

// unsealHistory: «Recuperar mi historial anterior» con el código de recuperación que se tenía ANTES del
// restablecimiento. Es la única forma de devolverlo: ni el propietario ni el administrador pueden.
func (s *Server) unsealHistory(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.householdError(w, err, "unseal/user")
		return
	}
	var req struct {
		Code string `json:"recovery_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	seals, err := s.store.Seals(r.Context(), u.ID)
	if err != nil {
		s.internal(w, "unseal", err)
		return
	}
	var match *store.Seal
	if !s.checkProfileSecret(w, r, u, "pin", func() bool {
		code := normalizeRecoveryCode(req.Code)
		for i := range seals {
			if len(seals[i].Lock) > 0 && auth.VerifyPassword(string(seals[i].Lock), code) == nil {
				match = &seals[i]
				return true
			}
		}
		return false
	}) {
		return
	}
	n, err := s.store.Unseal(r.Context(), u.ID, match.ID)
	if err != nil {
		s.householdError(w, err, "unseal")
		return
	}
	s.audit.Record(r.Context(), &u.ID, "profile.history_unsealed", u.ID.String(), map[string]any{"items": n}, s.clientIP(r))
	writeJSON(w, 200, map[string]any{"items": n})
}

// pinFailureNotice: intentos fallidos de PIN o código desde la anterior visita al perfil, para avisar al entrar.
func (s *Server) pinFailureNotice(r *http.Request, u store.User) map[string]any {
	if !u.HasPIN {
		return nil
	}
	n, last, err := s.store.PINFailures(r.Context(), u.ID, nil)
	if err != nil || n == 0 {
		return nil
	}
	return map[string]any{"count": n, "last_at": last}
}

// adminPINFailures: lo que ve el administrador de cada perfil: número y fecha de intentos fallidos en 30 días.
func (s *Server) adminPINFailures(r *http.Request, uid uuid.UUID) (int, *time.Time) {
	since := time.Now().Add(-30 * 24 * time.Hour)
	n, last, err := s.store.PINFailures(r.Context(), uid, &since)
	if err != nil {
		return 0, nil
	}
	return n, last
}

// unlockProfile: desbloquear el perfil propio en este dispositivo con su PIN (fase 4). Con el aviso de intentos
// fallidos desde la anterior visita.
func (s *Server) unlockProfile(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.householdError(w, err, "unlock/user")
		return
	}
	var req struct {
		PIN string `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if u.HasPIN && !s.checkProfileSecret(w, r, u, "pin", func() bool { return validPIN(req.PIN) && auth.VerifyPassword(u.PINHash, req.PIN) == nil }) {
		return
	}
	if err = s.store.UnlockSession(r.Context(), who.SessionID); err != nil {
		s.internal(w, "unlock", err)
		return
	}
	body := map[string]any{"user": u}
	if notice := s.pinFailureNotice(r, u); notice != nil {
		body["pin_failures"] = notice
	}
	writeJSON(w, 200, body)
}

// profileActivity: la web y la app avisan de uso real. interaction=true: el usuario ha tocado algo (mando, teclas,
// toques, clics); false: solo que la app está a la vista (cuenta para la opción «siempre»). Las consultas automáticas
// no pasan por aquí, así que no mantienen el perfil desbloqueado.
func (s *Server) profileActivity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Interaction bool `json:"interaction"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.store.TouchSession(r.Context(), authz.FromContext(r.Context()).SessionID, req.Interaction); err != nil {
		s.internal(w, "activity", err)
		return
	}
	w.WriteHeader(204)
}

// setPinLock: cada miembro elige cuándo se vuelve a pedir su PIN (0 = siempre; 15, 60 o 240 minutos de inactividad).
func (s *Server) setPinLock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes int `json:"minutes"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Minutes != 0 && req.Minutes != 15 && req.Minutes != 60 && req.Minutes != 240 {
		writeError(w, 422, "opcion_invalida", "Elige siempre, 15 minutos, 1 hora o 4 horas.")
		return
	}
	who := authz.FromContext(r.Context())
	if err := s.store.SetPinLockMinutes(r.Context(), who.UserID, req.Minutes); err != nil {
		s.internal(w, "pin-lock", err)
		return
	}
	_ = s.store.UnlockSession(r.Context(), who.SessionID) // el plazo nuevo se aplica desde ahora
	w.WriteHeader(204)
}
