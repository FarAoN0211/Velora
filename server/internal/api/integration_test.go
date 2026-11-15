package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velora/internal/audit"
	"velora/internal/backup"
	"velora/internal/config"
	"velora/internal/db"
	"velora/internal/jobs"
	"velora/internal/secrets"
	"velora/internal/setup"
	"velora/internal/store"
)

// testEnv crea una base de datos efímera por test (en el PostgreSQL de velora-dev) y la borra al terminar.
type testEnv struct {
	pool      *pgxpool.Pool
	dbURL     string
	server    *httptest.Server
	bk        *backup.Service
	code      string // código de instalación vigente
	configDir string
	srv       *Server
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	base := os.Getenv("VELORA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("VELORA_TEST_DATABASE_URL no definida: se omiten las pruebas de integración")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("no se puede conectar a la base de pruebas: %v", err)
	}
	name := fmt.Sprintf("velora_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	dbURL := u.String()
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
		admin.Close(context.Background())
	})
	if err := db.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("migraciones: %v", err)
	}
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	cfg := config.Config{DatabaseURL: dbURL, SessionTTL: time.Hour, BackupDir: t.TempDir(), ConfigDir: t.TempDir(), Location: time.UTC}
	bk := &backup.Service{Pool: pool, DatabaseURL: dbURL, Dir: cfg.BackupDir, Keep: 7, KeepWeeks: 4}
	key, err := secrets.EnsureMasterKey(cfg.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	code, err := setup.Ensure(ctx, pool, cfg.ConfigDir, false)
	if err != nil || code == "" {
		t.Fatalf("código de instalación: %q %v", code, err)
	}
	srv := New(cfg, key, &store.Store{Pool: pool}, audit.New(pool), jobs.NewQueue(pool), bk)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testEnv{pool: pool, dbURL: dbURL, server: ts, bk: bk, code: code, configDir: cfg.ConfigDir, srv: srv}
}

func (e *testEnv) call(t *testing.T, method, path, token string, body any, out any) int {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.server.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Velora-Client", "android")
	req.Header.Set("X-Velora-Device-Id", "test-device-1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestE0Flow(t *testing.T) {
	e := newEnv(t)

	var info map[string]any
	if c := e.call(t, "GET", "/api/v1/system/info", "", nil, &info); c != 200 || info["setup_required"] != true {
		t.Fatalf("system/info: %d %v", c, info)
	}

	body := map[string]string{"server_name": "Casa", "username": "marta", "display_name": "Marta", "password": "contraseña-larga"}
	// E0.1, punto 1: sin código o con un código falso no se crea el administrador.
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 403 {
		t.Fatalf("setup sin código debería dar 403, dio %d", c)
	}
	body["setup_code"] = "AAAA-BBBB-CCCC"
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 403 {
		t.Fatalf("setup con código falso debería dar 403, dio %d", c)
	}
	body["setup_code"] = strings.ToLower(e.code) // se acepta en minúsculas y con guiones
	var sess struct {
		Token string `json:"token"`
		User  struct {
			IsAdmin bool `json:"is_admin"`
		} `json:"user"`
	}
	if c := e.call(t, "POST", "/api/v1/setup", "", body, &sess); c != 201 || sess.Token == "" || !sess.User.IsAdmin {
		t.Fatalf("setup: %d %+v", c, sess)
	}
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 409 {
		t.Fatalf("un segundo setup debería dar 409, dio %d", c)
	}
	if _, err := os.Stat(filepath.Join(e.configDir, "secrets", "setup-code")); !os.IsNotExist(err) {
		t.Fatalf("el fichero del código debería borrarse tras usarlo: %v", err)
	}

	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "mala-contraseña"}, nil); c != 401 {
		t.Fatalf("login con contraseña mala: %d", c)
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "nadie", "password": "x"}, nil); c != 401 {
		t.Fatalf("login de usuario inexistente: %d", c)
	}
	var login struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "MARTA", "password": "contraseña-larga"}, &login); c != 200 || login.Token == "" {
		t.Fatalf("login: %d", c)
	}

	var me struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if c := e.call(t, "GET", "/api/v1/me", login.Token, nil, &me); c != 200 || me.User.Username != "marta" {
		t.Fatalf("me: %d %+v", c, me)
	}
	if c := e.call(t, "GET", "/api/v1/me", "token-inventado", nil, nil); c != 401 {
		t.Fatalf("token falso debería dar 401, dio %d", c)
	}

	// Un usuario normal no ve las rutas de administración (404, no 403).
	if _, err := e.pool.Exec(context.Background(),
		`insert into users (username, display_name, password_hash) select 'invitado', 'Invitado', password_hash from users where username = 'marta'`); err != nil {
		t.Fatal(err)
	}
	var guest struct {
		Token string `json:"token"`
	}
	e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "invitado", "password": "contraseña-larga"}, &guest)
	if c := e.call(t, "GET", "/api/v1/admin/backups", guest.Token, nil, nil); c != 404 {
		t.Fatalf("admin/backups como invitado debería dar 404, dio %d", c)
	}

	var profiles struct {
		Profiles []map[string]any `json:"profiles"`
	}
	e.call(t, "GET", "/api/v1/auth/profiles", "", nil, &profiles)
	if len(profiles.Profiles) != 2 {
		t.Fatalf("esperaba 2 perfiles, hay %d", len(profiles.Profiles))
	}
	for _, p := range profiles.Profiles {
		if _, leaked := p["username"]; leaked {
			t.Fatal("los perfiles públicos no deben exponer el nombre de usuario")
		}
	}

	// Copia de seguridad: se encola, no se duplica, y el worker la hace y la verifica.
	if c := e.call(t, "POST", "/api/v1/admin/backup", login.Token, nil, nil); c != 202 {
		t.Fatalf("admin/backup: %d", c)
	}
	if c := e.call(t, "POST", "/api/v1/admin/backup", login.Token, nil, nil); c != 409 {
		t.Fatalf("una segunda copia simultánea debería dar 409, dio %d", c)
	}
	meta, err := e.bk.Run(context.Background())
	if err != nil {
		t.Fatalf("copia: %v", err)
	}
	if !meta.Verified || meta.Users != 2 || meta.SchemaVersion < 1 {
		t.Fatalf("copia no verificada correctamente: %+v", meta)
	}
	var backups struct {
		Backups []backup.Meta `json:"backups"`
	}
	if c := e.call(t, "GET", "/api/v1/admin/backups", login.Token, nil, &backups); c != 200 || len(backups.Backups) != 1 || !backups.Backups[0].Verified {
		t.Fatalf("admin/backups: %d %+v", c, backups)
	}
	rep, err := e.bk.RecordStorage(context.Background())
	if err != nil || rep.Samples != 1 || rep.Latest.DBBytes == 0 {
		t.Fatalf("registro de almacenamiento: %v %+v", err, rep)
	}

	if c := e.call(t, "POST", "/api/v1/auth/logout", login.Token, nil, nil); c != 204 {
		t.Fatalf("logout: %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/me", login.Token, nil, nil); c != 401 {
		t.Fatalf("la sesión cerrada debería dar 401, dio %d", c)
	}

	var audits int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from audit_log`).Scan(&audits)
	if audits < 5 {
		t.Fatalf("la auditoría debería tener al menos 5 eventos, tiene %d", audits)
	}
}

// Dos asistentes simultáneos: solo uno puede crear el administrador.
func TestSetupRace(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.call(t, "POST", "/api/v1/setup", "", map[string]string{"setup_code": e.code,
				"server_name": "Casa", "username": fmt.Sprintf("usuario%d", i), "display_name": "A", "password": "contraseña-larga"}, nil)
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		if c == 201 {
			created++
		}
	}
	var admins int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from users where is_admin`).Scan(&admins)
	if created != 1 || admins != 1 {
		t.Fatalf("se crearon %d administradores (respuestas 201: %d)", admins, created)
	}
}

// Las sesiones por cookie exigen CSRF en las peticiones que cambian algo.
func TestCookieSessionNeedsCSRF(t *testing.T) {
	e := newEnv(t)
	raw, _ := json.Marshal(map[string]string{"setup_code": e.code, "server_name": "Casa", "username": "marta", "display_name": "C", "password": "contraseña-larga"})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/setup", bytes.NewReader(raw))
	req.Header.Set("X-Velora-Client", "web")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	var session, csrf *http.Cookie
	for _, c := range res.Cookies() {
		switch c.Name {
		case sessionCookie:
			session = c
		case csrfCookie:
			csrf = c
		}
	}
	if session == nil || csrf == nil || !session.HttpOnly || csrf.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookies incorrectas: %+v %+v", session, csrf)
	}
	logout := func(withCSRF bool) int {
		r, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/logout", nil)
		r.AddCookie(session)
		r.AddCookie(csrf)
		if withCSRF {
			r.Header.Set(csrfHeader, csrf.Value)
		}
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := logout(false); c != 403 {
		t.Fatalf("sin CSRF debería dar 403, dio %d", c)
	}
	// El token CSRF de OTRA sesión (otra pestaña, otro usuario o una cookie inyectada) no sirve.
	other := webLogin(t, e, "marta", "contraseña-larga")
	r, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/logout", nil)
	r.AddCookie(session)
	r.Header.Set(csrfHeader, other.csrf)
	res2, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != 403 {
		t.Fatalf("el CSRF de otra sesión debería dar 403, dio %d", res2.StatusCode)
	}
	if c := logout(true); c != 204 {
		t.Fatalf("con CSRF debería dar 204, dio %d", c)
	}
}

type webSession struct {
	session *http.Cookie
	csrf    string
	body    map[string]any
}

// webLogin entra como lo hace un navegador (con Origin y Sec-Fetch-*), declarando además ser Android.
func webLogin(t *testing.T, e *testEnv, user, pass string) webSession {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/login", bytes.NewReader(raw))
	req.Header.Set("Origin", e.server.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("X-Velora-Client", "android") // un navegador puede mentir aquí: no debe cambiar nada
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out webSession
	_ = json.NewDecoder(res.Body).Decode(&out.body)
	for _, c := range res.Cookies() {
		switch c.Name {
		case sessionCookie:
			out.session = c
		case csrfCookie:
			out.csrf = c.Value
		}
	}
	if res.StatusCode != 200 {
		t.Fatalf("login de navegador: %d %v", res.StatusCode, out.body)
	}
	return out
}

func adminSetup(t *testing.T, e *testEnv) string {
	t.Helper()
	var sess struct {
		Token string `json:"token"`
	}
	c := e.call(t, "POST", "/api/v1/setup", "", map[string]string{"setup_code": e.code, "server_name": "Casa",
		"username": "marta", "display_name": "C", "password": "contraseña-larga"}, &sess)
	if c != 201 {
		t.Fatalf("setup: %d", c)
	}
	return sess.Token
}

// E0.1, punto 3: un navegador nunca recibe un token Bearer en el cuerpo, declare lo que declare.
func TestBrowserNeverGetsBearer(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	ws := webLogin(t, e, "marta", "contraseña-larga")
	if _, leaked := ws.body["token"]; leaked {
		t.Fatal("un navegador ha recibido un token Bearer en el cuerpo")
	}
	if ws.session == nil || !ws.session.HttpOnly || ws.csrf == "" {
		t.Fatal("el navegador debería recibir cookie HttpOnly y token CSRF")
	}
	// Y un inicio de sesión desde otro origen se bloquea antes de comprobar la contraseña.
	raw, _ := json.Marshal(map[string]string{"username": "marta", "password": "contraseña-larga"})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/login", bytes.NewReader(raw))
	req.Header.Set("Origin", "http://malicioso.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("login desde otro origen debería dar 403, dio %d", res.StatusCode)
	}
}

// E0.1, punto 1: tras MaxFailures intentos fallidos el código se invalida; hace falta generar otro.
func TestSetupCodeLockout(t *testing.T) {
	e := newEnv(t)
	body := map[string]string{"setup_code": "ZZZZ-ZZZZ-ZZZZ", "server_name": "Casa", "username": "marta",
		"display_name": "C", "password": "contraseña-larga"}
	for i := 0; i < setup.MaxFailures; i++ {
		if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 403 {
			t.Fatalf("intento %d: %d", i, c)
		}
	}
	body["setup_code"] = e.code
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 403 {
		t.Fatalf("tras %d fallos, el código correcto ya no debería valer; dio %d", setup.MaxFailures, c)
	}
	// Reiniciar el servidor (Ensure sin forzar) NO da un código nuevo: el bloqueo persiste.
	if _, err := setup.Ensure(context.Background(), e.pool, e.configDir, false); !errors.Is(err, setup.ErrLocked) {
		t.Fatalf("tras el bloqueo, un reinicio no debería generar código; err=%v", err)
	}
	fresh, err := setup.Ensure(context.Background(), e.pool, e.configDir, true)
	if err != nil {
		t.Fatal(err)
	}
	body["setup_code"] = fresh
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 201 {
		t.Fatalf("con un código nuevo debería funcionar; dio %d", c)
	}
}

// E0.1, punto 6: una sesión revocada no se renueva aunque la revocación llegue entre la lectura y la renovación.
func TestRevokedSessionNotRefreshed(t *testing.T) {
	e := newEnv(t)
	token := adminSetup(t, e)
	st := &store.Store{Pool: e.pool}
	ctx := context.Background()
	var id uuid.UUID
	var before time.Time
	if err := e.pool.QueryRow(ctx, `select id, expires_at from sessions order by created_at desc limit 1`).Scan(&id, &before); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.RefreshSession(ctx, id, time.Hour); err != nil || !ok {
		t.Fatalf("una sesión vigente debería renovarse: %v %v", ok, err)
	}
	if err := st.RevokeSession(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.RefreshSession(ctx, id, 48*time.Hour); err != nil || ok {
		t.Fatalf("una sesión revocada no debería renovarse: %v %v", ok, err)
	}
	if c := e.call(t, "GET", "/api/v1/me", token, nil, nil); c != 401 {
		t.Fatalf("sesión revocada: %d", c)
	}
}
