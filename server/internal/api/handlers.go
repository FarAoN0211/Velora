package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/jobs"
	"velora/internal/setup"
	"velora/internal/store"
)

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Pool.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "bd_no_disponible", "La base de datos no responde.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"estado": "ok"})
}

func (s *Server) openapi(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openAPISpec)
}

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	setupRequired, err := s.store.SetupRequired(r.Context())
	if err != nil {
		s.internal(w, "system/info", err)
		return
	}
	name, lang := "VELORA", "es-ES"
	_ = s.store.Setting(r.Context(), "server.name", &name)
	_ = s.store.Setting(r.Context(), "server.language", &lang)
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "version": Version, "language": lang, "setup_required": setupRequired,
	})
}

type setupRequest struct {
	SetupCode   string `json:"setup_code"`
	ServerName  string `json:"server_name"`
	Language    string `json:"language"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

var supportedLanguages = map[string]bool{"es-ES": true}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.ServerName = strings.TrimSpace(req.ServerName)
	req.Username = strings.TrimSpace(req.Username)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Language == "" {
		req.Language = "es-ES"
	}
	if msg := validateSetup(req); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", msg)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, "setup", err)
		return
	}
	u, err := s.store.CreateFirstAdmin(r.Context(), setup.Hash(req.SetupCode), setup.MaxFailures,
		req.Username, req.DisplayName, hash, req.ServerName, req.Language)
	if errors.Is(err, store.ErrAlreadySetup) {
		writeError(w, http.StatusConflict, "ya_configurado", "Este servidor ya está configurado.")
		return
	}
	if errors.Is(err, store.ErrBadSetupCode) {
		s.audit.Record(r.Context(), nil, "setup.bad_code", "", nil, s.clientIP(r))
		writeError(w, http.StatusForbidden, "codigo_instalacion",
			"El código de instalación no es válido. Aparece en la consola del servidor y en /config/secrets/setup-code.")
		return
	}
	if err != nil {
		s.internal(w, "setup", err)
		return
	}
	setup.Discard(s.cfg.ConfigDir)
	s.audit.Record(r.Context(), &u.ID, "setup.completed", u.Username, map[string]any{"server_name": req.ServerName}, s.clientIP(r))
	s.startSession(w, r, u, http.StatusCreated)
}

func validateSetup(req setupRequest) string {
	switch {
	case req.ServerName == "" || utf8.RuneCountInString(req.ServerName) > 60:
		return "El nombre del servidor es obligatorio y no puede superar 60 caracteres."
	case !supportedLanguages[req.Language]:
		return "Por ahora solo está disponible el castellano (es-ES)."
	case !validUsername(req.Username):
		return "El usuario debe tener entre 3 y 32 caracteres: letras, números, punto, guion o guion bajo."
	case strings.EqualFold(req.Username, "admin") || strings.EqualFold(req.Username, "root"):
		return "Por seguridad, el usuario administrador no puede llamarse «admin» ni «root»."
	case req.DisplayName == "" || utf8.RuneCountInString(req.DisplayName) > 60:
		return "El nombre visible es obligatorio y no puede superar 60 caracteres."
	case utf8.RuneCountInString(req.Password) < auth.MinPasswordLen:
		return "La contraseña debe tener al menos 10 caracteres."
	case len(req.Password) > 256:
		return "La contraseña es demasiado larga."
	}
	return ""
}

func validUsername(u string) bool {
	if len(u) < 3 || len(u) > 32 {
		return false
	}
	for _, c := range u {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

type profile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	HasPIN      bool   `json:"has_pin"`
}

func (s *Server) profiles(w http.ResponseWriter, r *http.Request) {
	enabled := true
	_ = s.store.Setting(r.Context(), "auth.profiles_public", &enabled)
	if !enabled {
		writeJSON(w, http.StatusOK, map[string]any{"profiles": []profile{}})
		return
	}
	users, err := s.store.PublicProfiles(r.Context())
	if err != nil {
		s.internal(w, "auth/profiles", err)
		return
	}
	out := make([]profile, 0, len(users))
	for _, u := range users {
		// Sin nombre de usuario: el selector muestra el nombre visible y se entra por id.
		out = append(out, profile{ID: u.ID.String(), DisplayName: u.DisplayName, HasPIN: u.HasPIN})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

type loginRequest struct {
	Username string `json:"username"`
	UserID   string `json:"user_id"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !readJSON(w, r, &req) {
		return
	}
	var u store.User
	var err error
	switch {
	case req.UserID != "":
		u, err = s.userByIDString(r, req.UserID)
	case strings.TrimSpace(req.Username) != "":
		u, err = s.store.UserByUsername(r.Context(), strings.TrimSpace(req.Username))
	default:
		writeError(w, http.StatusUnprocessableEntity, "datos_no_validos", "Indica el usuario y la contraseña.")
		return
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, "auth/login", err)
		return
	}
	// Bloqueo por intentos (VF-075): espera exponencial por (usuario, IP), por usuario y por IP. Durante el bloqueo no se
	// comprueba la contraseña, así que no sirve para adivinarla. Comprobar, verificar y registrar ocurren bajo un cerrojo por
	// usuario y por IP: N peticiones en paralelo no pueden probar N contraseñas.
	ip := s.clientIP(r)
	// Identidad canónica del contador: la cuenta encontrada (por nombre o por id, el mismo contador); si no existe, lo
	// que se pidió, también por id. Así no hay forma de probar contraseñas fuera de los límites por usuario.
	name := strings.ToLower(strings.TrimSpace(req.Username))
	switch {
	case u.Username != "":
		name = strings.ToLower(u.Username)
	case name == "":
		name = "id:" + strings.ToLower(strings.TrimSpace(req.UserID))
	}
	var ok bool
	lockErr := s.loginLocks.with(r.Context(), name, ip, func() error {
		wait, err := s.store.LockedFor(r.Context(), name, ip)
		if err != nil {
			return err
		}
		if wait > 0 {
			mins := int(wait.Minutes()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "demasiados_intentos",
				fmt.Sprintf("Demasiados intentos fallidos. Espera %d min y vuelve a probar.", mins))
			return nil
		}
		fail := func(actor *uuid.UUID) error {
			if err := s.store.RecordAttempt(r.Context(), name, ip, "password", false); err != nil {
				return err
			}
			s.audit.Record(r.Context(), actor, "login.failed", name, map[string]any{"motivo": "credenciales"}, ip)
			writeError(w, http.StatusUnauthorized, "credenciales", "Usuario o contraseña incorrectos.")
			return nil
		}
		// El mismo mensaje y un coste similar en todos los fallos: no se revela si el usuario existe.
		if errors.Is(err, store.ErrNotFound) || u.IsDisabled || u.Passwordless || u.PasswordHash == "" {
			auth.BurnTime(req.Password)
			return fail(nil)
		}
		if err := auth.VerifyPassword(u.PasswordHash, req.Password); err != nil {
			return fail(&u.ID)
		}
		ok = true
		return s.store.RecordAttempt(r.Context(), name, ip, "password", true)
	})
	if r.Context().Err() != nil { // el cliente se fue mientras esperaba: no hay a quién responder
		return
	}
	if errors.Is(lockErr, errLockTimeout) {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "ocupado", "Hay demasiados intentos a la vez. Inténtalo de nuevo en unos segundos.")
		return
	}
	if lockErr != nil {
		s.internal(w, "auth/login", lockErr)
		return
	}
	if !ok {
		return
	}
	s.audit.Record(r.Context(), &u.ID, "login.ok", u.Username, nil, ip)
	s.startSession(w, r, u, http.StatusOK)
}

func parseUUID(s string) (uuid.UUID, error) { return uuid.Parse(s) }

func (s *Server) userByIDString(r *http.Request, id string) (store.User, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return store.User{}, store.ErrNotFound
	}
	return s.store.UserByID(r.Context(), uid)
}

// startSession crea la sesión (E0.1, punto 3). Un navegador recibe SIEMPRE cookies (sesión HttpOnly + token anti-CSRF
// ligado a la sesión) y nunca un token reutilizable en el cuerpo, declare lo que declare X-Velora-Client: la decisión
// se toma con cabeceras que el JavaScript no puede falsificar. X-Velora-Client solo sirve para etiquetar el dispositivo.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u store.User, status int) {
	s.startSessionUsing(w, r, u, status, nil)
}
func (s *Server) startSessionUsing(w http.ResponseWriter, r *http.Request, u store.User, status int, approval *uuid.UUID) {
	token, hash, err := auth.NewToken()
	if err != nil {
		s.internal(w, "session", err)
		return
	}
	kind := clientKind(r.Header.Get("X-Velora-Client"))
	cookieMode := fromBrowser(r) || kind == "web"
	dev := store.DeviceInfo{
		ClientID:   limit(r.Header.Get("X-Velora-Device-Id"), 128),
		Name:       limit(r.Header.Get("X-Velora-Device-Name"), 80),
		Kind:       kind,
		AppVersion: limit(r.Header.Get("X-Velora-App-Version"), 40),
		IP:         s.clientIP(r),
	}
	if dev.Name == "" {
		dev.Name = "Navegador"
	}
	sessionID, err := s.store.CreateSessionAuthorized(r.Context(), u.ID, hash, s.cfg.SessionTTL, dev, approval, u.PINHash)
	if err != nil {
		s.internal(w, "session", err)
		return
	}
	if cookieMode && u.HouseholdID != nil {
		if e := s.grantHouseholdDevice(w, r, u, sessionID, dev.Name); e != nil {
			s.internal(w, "household/device", e)
			return
		}
	}
	body := map[string]any{"user": u}
	if cookieMode {
		s.rememberProfile(w, r, token, sessionID, u)
		csrf := s.csrfToken(sessionID)
		maxAge := int(s.cfg.SessionTTL.Seconds())
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: maxAge,
			HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
		http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", MaxAge: maxAge,
			HttpOnly: false, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
		body["csrf_token"] = csrf
	} else {
		body["token"] = token
	}
	writeJSON(w, status, body)
}

func clientKind(h string) string {
	switch h {
	case "web", "android", "android_tv", "tizen", "webos", "dlna", "kodi":
		return h
	case "":
		return "web"
	}
	return "other"
}

func limit(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	if err := s.store.RevokeSession(r.Context(), who.SessionID); err != nil {
		s.internal(w, "auth/logout", err)
		return
	}
	list := []rememberedProfile{}
	for _, p := range s.rememberedProfiles(r) {
		if p.session != who.SessionID {
			list = append(list, p)
		}
	}
	s.saveRememberedProfiles(w, list)
	if err := s.store.RevokeHouseholdDevice(r.Context(), who.SessionID); err != nil {
		s.internal(w, "household/logout", err)
		return
	}
	for _, name := range []string{sessionCookie, csrfCookie, householdCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: name != csrfCookie, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	}
	s.audit.Record(r.Context(), &who.UserID, "logout", who.Username, nil, s.clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	u, err := s.store.UserByID(r.Context(), who.UserID)
	if err != nil {
		s.internal(w, "me", err)
		return
	}
	u.Locked = who.Locked // un perfil bloqueado ve la pantalla del PIN, no el catálogo
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) startBackup(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	id, err := s.queue.Enqueue(r.Context(), "backup", map[string]any{"requested_by": who.UserID}, "backup:manual")
	if errors.Is(err, jobs.ErrDuplicate) {
		writeError(w, http.StatusConflict, "en_curso", "Ya hay una copia de seguridad en cola o en curso.")
		return
	}
	if err != nil {
		s.internal(w, "admin/backup", err)
		return
	}
	s.audit.Record(r.Context(), &who.UserID, "backup.requested", "", map[string]any{"job_id": id}, s.clientIP(r))
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	list, err := s.backups.List()
	if err != nil {
		s.internal(w, "admin/backups", err)
		return
	}
	recent, err := s.queue.Recent(r.Context(), "backup", 5)
	if err != nil {
		s.internal(w, "admin/backups", err)
		return
	}
	var storage any
	_ = s.store.Setting(r.Context(), "storage.last_report", &storage)
	free, _ := s.backups.FreeBytes()
	writeJSON(w, http.StatusOK, map[string]any{
		"backups": list, "jobs": recent, "storage": storage,
		"free_bytes": free, "margin_bytes": s.cfg.MinFreeBytes,
	})
}

func (s *Server) internal(w http.ResponseWriter, where string, err error) {
	slog.Error("error interno", "donde", where, "error", err)
	writeError(w, http.StatusInternalServerError, "interno", "Ha ocurrido un error en el servidor. Inténtalo de nuevo.")
}

// grantHouseholdDevice autoriza este dispositivo (cookie HttpOnly) a cambiar entre los perfiles del hogar de u.
// Cada perfil con PIN sigue exigiéndolo al seleccionarlo.
func (s *Server) grantHouseholdDevice(w http.ResponseWriter, r *http.Request, u store.User, sessionID uuid.UUID, name string) error {
	grant, grantHash, err := auth.NewToken()
	if err != nil {
		return err
	}
	if err = s.store.NewHouseholdDevice(r.Context(), u, sessionID, grantHash, name, s.cfg.SessionTTL); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: householdCookie, Value: grant, Path: "/", MaxAge: int(s.cfg.SessionTTL.Seconds()), HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	return nil
}
