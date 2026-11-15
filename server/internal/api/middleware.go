package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/go-chi/chi/v5/middleware"

	"velora/internal/auth"
	"velora/internal/authz"
	"velora/internal/store"
)

const (
	sessionCookie = "velora_session"
	csrfCookie    = "velora_csrf"
	csrfHeader    = "X-CSRF-Token"
)

// authenticate resuelve la sesión (Bearer para clientes nativos, cookie para la web). Si no hay sesión válida,
// la petición sigue como anónima y la política de la ruta decide.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, viaCookie := "", false
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		} else if c, err := r.Cookie(sessionCookie); err == nil {
			token, viaCookie = c.Value, true
		}
		if token != "" {
			sessionID, u, err := s.store.SessionUser(r.Context(), auth.HashToken(token), s.cfg.SessionTTL)
			switch {
			case err == nil:
				p := &authz.Principal{UserID: u.ID, SessionID: sessionID, Username: u.Username, IsAdmin: u.IsAdmin, ViaCookie: viaCookie,
					Perms: authz.Permissions{Play: u.CanPlay, Download: u.CanDownload, Delete: u.CanDelete, Remote: u.CanRemote}, Locked: u.Locked}
				r = r.WithContext(authz.WithPrincipal(r.Context(), p))
			case !errors.Is(err, store.ErrNotFound):
				slog.Error("error al resolver la sesión", "error", err)
				writeError(w, http.StatusServiceUnavailable, "no_disponible", "El servidor no puede comprobar la sesión ahora mismo.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// require aplica la política de la ruta y, para sesiones por cookie, la protección anti-CSRF (doble envío).
func (s *Server) require(p authz.Policy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			who := authz.FromContext(r.Context())
			if !authz.Allows(p, who) {
				if who == nil {
					writeError(w, http.StatusUnauthorized, "sin_sesion", "Inicia sesión para continuar.")
				} else {
					// 404 y no 403: no se revela que el recurso existe (§8).
					writeError(w, http.StatusNotFound, "no_encontrado", "No existe este recurso.")
				}
				return
			}
			if who != nil && who.ViaCookie && !safeMethod(r.Method) && !s.validCSRF(r, who.SessionID) {
				writeError(w, http.StatusForbidden, "csrf", "La petición no lleva un token anti-CSRF válido. Recarga la página.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// csrfToken liga el token anti-CSRF a la sesión (E0.1, punto 2): HMAC-SHA256(clave, id de sesión).
// Una cookie inyectada o un token de otra sesión no sirven, porque no se pueden calcular sin la clave del servidor.
func (s *Server) csrfToken(sessionID uuid.UUID) string {
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write(sessionID[:])
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) validCSRF(r *http.Request, sessionID uuid.UUID) bool {
	got := r.Header.Get(csrfHeader)
	want := s.csrfToken(sessionID)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// deriveKey obtiene una subclave por uso a partir de la clave maestra (nunca se usa la maestra directamente).
func deriveKey(master []byte, label string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte(label))
	return m.Sum(nil)
}

// fromBrowser detecta peticiones de navegador por cabeceras que el navegador pone siempre y que el JavaScript de una
// página no puede quitar ni falsificar (cabeceras prohibidas de Fetch). Un cliente nativo no las envía (E0.1, punto 3).
func fromBrowser(r *http.Request) bool {
	return r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" || r.Header.Get("Sec-Fetch-Mode") != ""
}

// sameOrigin indica si una petición de navegador viene de la propia web de VELORA: esquema, host y puerto completos.
// Con VELORA_PUBLIC_URLS se exige uno de esos orígenes exactos (necesario detrás de un proxy con HTTPS); sin ella,
// el mismo host y el esquema de la conexión (revisión de ChatGPT a E0.1).
func sameOrigin(r *http.Request, allowed []string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host == "" {
			return false
		}
		origin := strings.ToLower(u.Scheme + "://" + u.Host)
		if len(allowed) > 0 {
			for _, a := range allowed {
				if origin == a {
					return true
				}
			}
			return false
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		return strings.EqualFold(u.Scheme, scheme) && strings.EqualFold(u.Host, r.Host)
	}
	// Sin Origin, una petición de navegador solo se acepta si el propio navegador declara que viene de la misma web
	// (same-origin) o de una acción directa del usuario (none). Un Sec-Fetch-Site vacío ya no basta (revisión de código).
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	}
	return false
}

// originGuard rechaza las peticiones de navegador que cambian algo y vienen de otro origen, también en rutas
// públicas como el inicio de sesión o el asistente (CSRF de login). Los clientes nativos no pasan por aquí.
func (s *Server) originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && fromBrowser(r) && !sameOrigin(r, s.cfg.PublicOrigins) {
			writeError(w, http.StatusForbidden, "origen_no_permitido", "La petición viene de otro sitio web y se ha bloqueado.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// logRequests registra método, ruta, estado y duración. Nunca cabeceras de autenticación ni cuerpos.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if r.URL.Path == "/healthz" {
			return
		}
		slog.Info("http", "metodo", r.Method, "ruta", r.URL.Path, "estado", ww.Status(),
			"ms", time.Since(start).Milliseconds(), "id", middleware.GetReqID(r.Context()))
	})
}

func isTrusted(trusted []netip.Prefix, a netip.Addr) bool {
	for _, p := range trusted {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	return false
}

// clientIP es la IP de la conexión. X-Forwarded-For solo se tiene en cuenta si la conexión viene de un proxy de confianza,
// y entonces se toma la última dirección añadida que no sea otro proxy de confianza (las anteriores las pudo falsificar el
// cliente).
func (s *Server) clientIP(r *http.Request) string { return clientIPWith(r, s.cfg.TrustedProxies) }

func clientIPWith(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(trusted, peer) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return host
		}
		if !isTrusted(trusted, a) {
			return a.Unmap().String()
		}
	}
	return host
}
