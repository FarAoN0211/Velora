// Package api expone la API REST /api/v1 de VELORA.
package api

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"velora/internal/audit"
	"velora/internal/authz"
	"velora/internal/backup"
	"velora/internal/config"
	"velora/internal/jobs"
	"velora/internal/media"
	"velora/internal/metadata"
	"velora/internal/store"
)

//go:embed openapi.yaml
var openAPISpec []byte

// Version se fija al compilar con -ldflags "-X velora/internal/api.Version=…".
var Version = "0.0.0-e0"

// Server reúne las dependencias de los handlers.
type Server struct {
	cfg             config.Config
	csrfKey         []byte // derivada de la clave maestra: firma los tokens anti-CSRF ligados a la sesión
	store           *store.Store
	audit           *audit.Logger
	queue           *jobs.Queue
	backups         *backup.Service
	registry        *authz.Registry
	router          chi.Router
	tmdb            *metadata.TMDB
	remux           *media.Limiter
	videoTranscodes *media.Limiter
	transcoder      *media.Transcoder
	remuxSessions   remuxSessions
	activity        playbackActivity
	subSlot         chan struct{} // una extracción de subtítulos internos a la vez
	imgSlot         chan struct{} // un procesado de imagen a la vez (decodificar ocupa mucha memoria)
	seekSlots       chan struct{} // como mucho 2 cálculos de salto (FFmpeg corto) a la vez: los saltos rápidos no se acumulan
	loginLocks      keyedLocks
}

// New construye el servidor y registra todas las rutas.
func New(cfg config.Config, masterKey []byte, st *store.Store, au *audit.Logger, q *jobs.Queue, b *backup.Service) *Server {
	s := &Server{cfg: cfg, csrfKey: deriveKey(masterKey, "velora:csrf:v1"), store: st, audit: au, queue: q, backups: b,
		registry: authz.NewRegistry(), tmdb: metadata.New(cfg.TMDBAPIURL, cfg.TMDBImageURL, cfg.ConfigDir, st),
		remux: media.NewLimiter(cfg.MaxRemux), videoTranscodes: media.NewLimiter(1), transcoder: &media.Transcoder{}, subSlot: make(chan struct{}, 1), imgSlot: make(chan struct{}, 1),
		seekSlots: make(chan struct{}, 2)}
	s.router = s.routes()
	return s
}

// Handler devuelve el http.Handler raíz.
func (s *Server) Handler() http.Handler { return s.router }

// Registry expone el registro de rutas (tests y documentación).
func (s *Server) Registry() *authz.Registry { return s.registry }

func (s *Server) routes() chi.Router {
	root := chi.NewRouter()
	// Sin middleware.RealIP: X-Forwarded-For solo se aceptará de proxies de confianza (E3, §8).
	root.Use(middleware.RequestID, s.logRequests, middleware.Recoverer, securityHeaders, s.originGuard)
	root.Use(s.authenticate)
	// Las rutas normales tienen un tope de 60 s; las de streaming no (las corta el cierre de la conexión del cliente).
	api := root.With(middleware.Timeout(60 * time.Second))

	// Toda ruta pasa por handle(): sin política no se puede registrar (architecture.md §8).
	s.handle(api, http.MethodGet, "/healthz", authz.Public, s.healthz)
	s.handle(api, http.MethodGet, "/api/v1/openapi.yaml", authz.Public, s.openapi)
	s.handle(api, http.MethodGet, "/api/v1/system/info", authz.Public, s.systemInfo)
	s.handle(api, http.MethodPost, "/api/v1/setup", authz.Public, s.setup)
	s.handle(api, http.MethodGet, "/api/v1/auth/profiles", authz.Public, s.profiles)
	s.handle(api, http.MethodPost, "/api/v1/auth/login", authz.Public, s.login)
	s.handle(api, http.MethodPost, "/api/v1/auth/logout", authz.User, s.logout)
	s.handle(api, http.MethodGet, "/api/v1/me/profiles", authz.User, s.savedProfiles)
	s.handle(api, http.MethodPost, "/api/v1/admin/accounts/passwordless", authz.Admin, s.createPasswordlessAccount)
	s.handle(api, http.MethodPost, "/api/v1/admin/users/{id}/reset-pin", authz.Admin, s.adminResetPIN)
	s.handle(api, http.MethodGet, "/api/v1/me/household", authz.User, s.household)
	s.handle(api, http.MethodPost, "/api/v1/me/household", authz.User, s.createOwnHousehold)
	s.handle(api, http.MethodPatch, "/api/v1/me/household", authz.User, s.renameHousehold)
	s.handle(api, http.MethodDelete, "/api/v1/me/household/profiles/{id}", authz.User, s.trashFamilyProfile)
	s.handle(api, http.MethodPost, "/api/v1/me/household/profiles/{id}/restore", authz.User, s.restoreFamilyProfile)
	s.handle(api, http.MethodDelete, "/api/v1/me/household/profiles/{id}/permanent", authz.User, s.purgeFamilyProfile)
	s.handle(api, http.MethodPost, "/api/v1/me/household/profiles", authz.User, s.createFamilyProfile)
	s.handle(api, http.MethodPatch, "/api/v1/me/household/profiles/{id}", authz.User, s.updateFamilyProfile)
	s.handle(api, http.MethodDelete, "/api/v1/me/household/devices/{id}", authz.User, s.removeHouseholdDevice)
	s.handle(api, http.MethodPut, "/api/v1/me/pin", authz.User, s.profilePIN)
	s.handle(api, http.MethodPost, "/api/v1/me/pin/recovery-code", authz.User, s.regenerateRecoveryCode)
	s.handle(api, http.MethodPost, "/api/v1/me/unlock", authz.User, s.unlockProfile)
	s.handle(api, http.MethodPost, "/api/v1/me/activity", authz.User, s.profileActivity)
	s.handle(api, http.MethodPut, "/api/v1/me/pin-lock", authz.User, s.setPinLock)
	s.handle(api, http.MethodPost, "/api/v1/auth/pin/recover", authz.User, s.recoverPIN)
	s.handle(api, http.MethodGet, "/api/v1/me/history/seals", authz.User, s.historySeals)
	s.handle(api, http.MethodPost, "/api/v1/me/history/unseal", authz.User, s.unsealHistory)
	s.handle(api, http.MethodPost, "/api/v1/auth/switch", authz.User, s.switchProfile)
	s.handle(api, http.MethodGet, "/api/v1/me", authz.User, s.me)
	s.handle(api, http.MethodPost, "/api/v1/admin/backup", authz.Admin, s.startBackup)
	s.handle(api, http.MethodGet, "/api/v1/admin/backups", authz.Admin, s.listBackups)
	s.handle(api, http.MethodPost, "/api/v1/playback/heartbeat", authz.User, s.playbackHeartbeat)
	s.handle(api, http.MethodGet, "/api/v1/admin/now-playing", authz.Admin, s.nowPlaying)
	s.handle(api, http.MethodPost, "/api/v1/auth/device", authz.Public, s.startDeviceLogin)
	s.handle(api, http.MethodPost, "/api/v1/auth/device/poll", authz.Public, s.pollDeviceLogin)
	s.handle(api, http.MethodPost, "/api/v1/auth/device/inspect", authz.User, s.inspectDeviceLogin)
	s.handle(api, http.MethodPost, "/api/v1/auth/device/approve", authz.User, s.approveDeviceLogin)
	s.handle(api, http.MethodPost, "/api/v1/me/recovery-codes", authz.User, s.generateRecoveryCodes)
	s.handle(api, http.MethodPost, "/api/v1/auth/recover", authz.Public, s.recoverPassword)
	s.handle(api, http.MethodPost, "/api/v1/admin/users/{id}/password", authz.Admin, s.adminResetPassword)
	s.catalogRoutes(api)
	s.itemAdminRoutes(api)
	s.streamRoutes(root)

	// Web compilada (pública, sin datos): cualquier ruta que no sea de la API devuelve la SPA.
	root.NotFound(s.spa())
	return root
}

// handle registra una ruta con su política y aplica la comprobación antes del handler.
func (s *Server) handle(r chi.Router, method, pattern string, p authz.Policy, h http.HandlerFunc) {
	s.registry.Add(method, pattern, p) // entra en pánico si falta la política
	if p != authz.Public && !lockAllowed[method+" "+pattern] {
		h = lockGuard(h)
	}
	r.With(s.require(p)).Method(method, pattern, h)
}

func (s *Server) spa() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || s.cfg.WebDir == "" || r.Method != http.MethodGet {
			writeError(w, http.StatusNotFound, "no_encontrado", "No existe este recurso.")
			return
		}
		// Ruta limpia y siempre dentro de WebDir; si no es un fichero, se sirve index.html (enrutado del cliente).
		clean := filepath.Clean("/" + r.URL.Path)
		p := filepath.Join(s.cfg.WebDir, clean)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			p = filepath.Join(s.cfg.WebDir, "index.html")
			w.Header().Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, p)
	}
}

// lockAllowed son las únicas rutas que puede usar un perfil bloqueado (fase 4 de replica/PLAN_HOGARES_Y_PIN.md):
// saber quién es y que está bloqueado, desbloquearse, recuperar el PIN, cambiar de perfil, salir y la comprobación
// automática del catálogo (no da datos del perfil). Todo lo demás (historial, progreso, fichas, reproducción) espera
// al PIN.
var lockAllowed = map[string]bool{
	"GET /api/v1/me":                true,
	"POST /api/v1/me/unlock":        true,
	"POST /api/v1/auth/pin/recover": true,
	"POST /api/v1/auth/switch":      true,
	"GET /api/v1/me/profiles":       true,
	"POST /api/v1/auth/logout":      true,
	"GET /api/v1/catalog/revision":  true,
}

// lockGuard responde 423 «perfil_bloqueado» a un perfil bloqueado.
func lockGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if who := authz.FromContext(r.Context()); who != nil && who.Locked {
			writeError(w, http.StatusLocked, "perfil_bloqueado", "Este perfil está bloqueado. Escribe su PIN para seguir.")
			return
		}
		next(w, r)
	}
}
