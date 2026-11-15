package api

import (
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

const profilesCookie = "velora_profiles"

type rememberedProfile struct {
	token   string
	session uuid.UUID
	user    store.User
}

// Only tokens from successful logins on this browser are remembered. They are
// HttpOnly and each is checked against revocation/expiry/disabled users. A
// public profile ID alone never grants access to that account.
func (s *Server) rememberedProfiles(r *http.Request) []rememberedProfile {
	tokens := []string{}
	if c, err := r.Cookie(profilesCookie); err == nil && len(c.Value) < 3000 {
		if b, e := base64.RawURLEncoding.DecodeString(c.Value); e == nil {
			_ = json.Unmarshal(b, &tokens)
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		tokens = append([]string{c.Value}, tokens...)
	}
	out := []rememberedProfile{}
	seen := map[uuid.UUID]bool{}
	if len(tokens) > 10 {
		tokens = tokens[:10]
	}
	for _, token := range tokens {
		if len(token) != 43 {
			continue
		}
		id, u, err := s.store.SessionUser(r.Context(), auth.HashToken(token), s.cfg.SessionTTL)
		if err != nil || seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		out = append(out, rememberedProfile{token, id, u})
	}
	return out
}
func (s *Server) saveRememberedProfiles(w http.ResponseWriter, list []rememberedProfile) {
	tokens := []string{}
	for _, p := range list {
		if len(tokens) == 8 {
			break
		}
		tokens = append(tokens, p.token)
	}
	raw, _ := json.Marshal(tokens)
	age := int(s.cfg.SessionTTL.Seconds())
	value := base64.RawURLEncoding.EncodeToString(raw)
	if len(tokens) == 0 {
		age = -1
		value = ""
	}
	http.SetCookie(w, &http.Cookie{Name: profilesCookie, Value: value, Path: "/", MaxAge: age, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
}
func (s *Server) rememberProfile(w http.ResponseWriter, r *http.Request, token string, session uuid.UUID, u store.User) {
	list := []rememberedProfile{{token, session, u}}
	for _, p := range s.rememberedProfiles(r) {
		if p.user.ID != u.ID {
			list = append(list, p)
		}
	}
	s.saveRememberedProfiles(w, list)
}
func (s *Server) savedProfiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	out := []profile{}
	seen := map[uuid.UUID]bool{}
	if c, e := r.Cookie(householdCookie); e == nil && len(c.Value) == 43 {
		_, hid, e := s.store.HouseholdDevice(r.Context(), auth.HashToken(c.Value))
		if e == nil {
			family, e := s.store.HouseholdProfiles(r.Context(), hid)
			if e != nil {
				s.internal(w, "profiles/household", e)
				return
			}
			for _, u := range family {
				if !u.IsDisabled {
					out = append(out, profile{ID: u.ID.String(), DisplayName: u.DisplayName, HasPIN: u.HasPIN})
					seen[u.ID] = true
				}
			}
		}
	}
	for _, p := range s.rememberedProfiles(r) {
		if !seen[p.user.ID] {
			out = append(out, profile{ID: p.user.ID.String(), DisplayName: p.user.DisplayName, HasPIN: p.user.HasPIN})
		}
	}
	writeJSON(w, 200, map[string]any{"profiles": out})
}
func (s *Server) switchProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		User uuid.UUID `json:"user_id"`
		PIN  string    `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	target, e := s.store.UserByID(r.Context(), req.User)
	if e == nil && !target.IsDisabled && target.DeletedAt == nil && target.HouseholdID != nil {
		if c, e := r.Cookie(householdCookie); e == nil && len(c.Value) == 43 {
			grant, hid, e := s.store.HouseholdDevice(r.Context(), auth.HashToken(c.Value))
			if e == nil && hid == *target.HouseholdID {
				if !s.verifyProfilePIN(w, r, target, req.PIN) {
					return
				}
				token, hash, e := auth.NewToken()
				if e != nil {
					s.internal(w, "switch/token", e)
					return
				}
				session, e := s.store.CreateHouseholdSession(r.Context(), grant, target.ID, target.PINHash, hash, s.cfg.SessionTTL)
				if e != nil {
					s.householdError(w, e, "switch/family")
					return
				}
				s.writeProfileSession(w, r, token, session, target)
				return
			}
		}
	}
	for _, p := range s.rememberedProfiles(r) {
		if p.user.HouseholdID != nil {
			continue
		} // family accounts must retain a live device grant
		if p.user.ID != req.User {
			continue
		}
		if !s.verifyProfilePIN(w, r, p.user, req.PIN) {
			return
		}
		s.useRememberedProfile(w, r, p, nil)
		return
	}
	writeError(w, 403, "perfil_no_recordado", "Esta cuenta debe iniciar sesión una vez en este dispositivo.")
}

func (s *Server) writeProfileSession(w http.ResponseWriter, r *http.Request, token string, session uuid.UUID, u store.User) {
	s.writeProfileSessionWith(w, r, token, session, u, nil)
}

// writeProfileSessionWith abre la sesión del perfil en este navegador. La respuesta incluye el aviso de intentos
// fallidos de PIN desde su anterior visita (pin_failures) y, si los hay, datos extra (p. ej. un código de recuperación).
func (s *Server) writeProfileSessionWith(w http.ResponseWriter, r *http.Request, token string, session uuid.UUID, u store.User, extra map[string]any) {
	// El perfil que se deja queda bloqueado en este dispositivo; el elegido (ya comprobado su PIN) queda desbloqueado.
	if prev := authz.FromContext(r.Context()); prev != nil && prev.SessionID != session {
		if err := s.store.LockSession(r.Context(), prev.SessionID); err != nil {
			s.internal(w, "switch/lock", err)
			return
		}
	}
	if err := s.store.UnlockSession(r.Context(), session); err != nil {
		s.internal(w, "switch/unlock", err)
		return
	}
	u.Locked = false
	s.rememberProfile(w, r, token, session, u)
	age := int(s.cfg.SessionTTL.Seconds())
	csrf := s.csrfToken(session)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: age, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/", MaxAge: age, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	actor := authz.FromContext(r.Context())
	s.audit.Record(r.Context(), &actor.UserID, "profile.switched", u.ID.String(), nil, s.clientIP(r))
	body := map[string]any{"user": u, "csrf_token": csrf}
	if notice := s.pinFailureNotice(r, u); notice != nil {
		body["pin_failures"] = notice
	}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, 200, body)
}

// useRememberedProfile vuelve a una cuenta ya recordada en este navegador (su sesión sigue viva).
func (s *Server) useRememberedProfile(w http.ResponseWriter, r *http.Request, p rememberedProfile, extra map[string]any) {
	s.writeProfileSessionWith(w, r, p.token, p.session, p.user, extra)
}
