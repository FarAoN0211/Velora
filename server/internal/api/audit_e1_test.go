package api

// Pruebas de la auditoría de E1 pedida por ChatGPT (rutas, escaneo prudente, login atómico, IP del cliente).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"velora/internal/config"
	"velora/internal/library"
	"velora/internal/media"
	"velora/internal/setup"
	"velora/internal/store"
)

func firstFileID(t *testing.T, e *e1Env) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `select id from media_files limit 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Si la carpeta de una biblioteca se sustituye por un enlace simbólico hacia fuera de las raíces, ni la reproducción ni
// el escaneo la siguen, y no se marca nada como ausente.
func TestLibraryFolderSwappedForSymlink(t *testing.T) {
	e := newE1Env(t, "")
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Pelis", "path": filepath.Join(e.mediaRoot, "Peliculas")}, &created)
	e.scan(t, created.Library.ID)
	file := firstFileID(t, e)
	if res := e.raw(t, "GET", "/api/v1/stream/files/"+file.String(), map[string]string{"Range": "bytes=0-99"}); res.StatusCode != 206 {
		t.Fatalf("antes del cambio debería reproducirse: %d", res.StatusCode)
	}

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "Prueba sintética (2020)"), 0o755); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(e.mediaRoot, "Peliculas")
	if err := os.Rename(lib, filepath.Join(e.mediaRoot, "Peliculas.orig")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lib); err != nil {
		t.Fatal(err)
	}
	if res := e.raw(t, "GET", "/api/v1/stream/files/"+file.String(), nil); res.StatusCode != 503 {
		t.Fatalf("una biblioteca sustituida por un enlace no debe servirse: %d", res.StatusCode)
	}
	st := &store.Store{Pool: e.pool}
	l, _ := st.LibraryByID(context.Background(), created.Library.ID)
	if _, err := library.NewScanner(st, []string{e.mediaRoot}).ScanLibrary(context.Background(), l); !errors.Is(err, library.ErrPathMoved) {
		t.Fatalf("el escaneo debería negarse con ErrPathMoved: %v", err)
	}
	var missing int
	_ = e.pool.QueryRow(context.Background(), `select count(*) from media_files where missing_since is not null`).Scan(&missing)
	if missing != 0 {
		t.Fatalf("no debe marcarse nada como ausente, hay %d", missing)
	}
}

// Freno ante desapariciones masivas: con 12 archivos, si «desaparecen» 11 no se marca ninguno; si desaparece 1, sí.
func TestScanRefusesMassMissing(t *testing.T) {
	e := newE1Env(t, "")
	root := filepath.Join(e.mediaRoot, "Muchas")
	for i := 0; i < 12; i++ {
		dir := filepath.Join(root, fmt.Sprintf("Película %02d (2001)", i))
		_ = os.MkdirAll(dir, 0o755)
		if err := os.WriteFile(filepath.Join(dir, "p.mkv"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var created struct {
		Library store.Library `json:"library"`
	}
	e.call(t, "POST", "/api/v1/libraries", e.token, map[string]string{"name": "Muchas", "path": root}, &created)
	st := &store.Store{Pool: e.pool}
	sc := library.NewScanner(st, []string{e.mediaRoot})
	sc.Probe = func(context.Context, *os.File) (media.Probe, error) { return media.Probe{}, nil }
	lib, _ := st.LibraryByID(context.Background(), created.Library.ID)
	if _, err := sc.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	countMissing := func() (n int) {
		_ = e.pool.QueryRow(context.Background(), `select count(*) from media_files where missing_since is not null`).Scan(&n)
		return
	}
	for i := 1; i < 12; i++ {
		_ = os.RemoveAll(filepath.Join(root, fmt.Sprintf("Película %02d (2001)", i)))
	}
	if _, err := sc.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if n := countMissing(); n != 0 {
		t.Fatalf("una desaparición masiva no debe marcarse, se marcaron %d", n)
	}
	// Vuelven todas menos una: esa sí se marca.
	for i := 1; i < 11; i++ {
		dir := filepath.Join(root, fmt.Sprintf("Película %02d (2001)", i))
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "p.mkv"), []byte("x"), 0o644)
	}
	if _, err := sc.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	if n := countMissing(); n != 1 {
		t.Fatalf("debería marcarse 1 ausente, hay %d", n)
	}
}

// N intentos en paralelo no pueden probar más de 5 contraseñas: el resto recibe 429.
func TestLoginParallelIsAtomic(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "mala"}, nil)
			mu.Lock()
			codes[c]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[401] != 5 || codes[429] != 7 {
		t.Fatalf("se esperaban 5×401 y 7×429, hubo %v", codes)
	}
}

func loginFrom(t *testing.T, e *testEnv, xff, password string) int {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": "marta", "password": password})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Velora-Client", "android")
	req.Header.Set("X-Forwarded-For", xff)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// Un atacante desde otra IP no deja fuera al usuario legítimo (con un proxy de confianza que informa la IP real).
func TestLoginLockoutIsPerClient(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	e.srv.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	for i := 0; i < 6; i++ {
		loginFrom(t, e, "203.0.113.9", "mala")
	}
	if c := loginFrom(t, e, "203.0.113.9", "contraseña-larga"); c != 429 {
		t.Fatalf("el atacante debería estar bloqueado: %d", c)
	}
	if c := loginFrom(t, e, "192.168.1.50", "contraseña-larga"); c != 200 {
		t.Fatalf("el usuario legítimo desde su IP debería entrar: %d", c)
	}
}

// Sin proxies de confianza, X-Forwarded-For se ignora; con ellos, solo cuenta la última dirección no confiable.
func TestClientIPIgnoresSpoofedForwardedFor(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.20:5000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	plain := &Server{}
	proxied := &Server{cfg: config.Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}}}
	if ip := plain.clientIP(r); ip != "192.168.1.20" {
		t.Fatalf("sin proxies de confianza debe usarse la conexión: %s", ip)
	}
	r.RemoteAddr = "172.18.0.5:5000"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	if ip := proxied.clientIP(r); ip != "203.0.113.7" {
		t.Fatalf("debe tomarse la IP añadida por el proxy, no la falsificada: %s", ip)
	}
	// Dos servidores con configuraciones distintas no se influyen: el que no confía en ese proxy usa la conexión.
	if ip := plain.clientIP(r); ip != "172.18.0.5" {
		t.Fatalf("la configuración de proxies no debe ser global: %s", ip)
	}
}

// H2 (revisión de código): entrar por nombre o por id cuenta en el mismo contador, y un id inexistente también se limita.
func TestLoginByIDSharesLimit(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	var id string
	if err := e.pool.QueryRow(context.Background(), `select id::text from users where username = 'marta'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "mala"}, nil)
	}
	for i := 0; i < 2; i++ {
		e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"user_id": id, "password": "mala"}, nil)
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"user_id": id, "password": "contraseña-larga"}, nil); c != 429 {
		t.Fatalf("5 fallos mezclando nombre e id deberían bloquear: %d", c)
	}
	ghost := "00000000-0000-4000-8000-000000000001"
	codes := map[int]int{}
	for i := 0; i < 6; i++ {
		codes[e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"user_id": ghost, "password": "x"}, nil)]++
	}
	if codes[429] == 0 {
		t.Fatalf("un id inexistente también debe limitarse: %v", codes)
	}
}

// M1: la espera del cerrojo se cancela y el mapa no crece con nombres inventados.
func TestLoginLocksCancelAndCleanUp(t *testing.T) {
	var k keyedLocks
	unlock, err := k.lock(context.Background(), "u:ana")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := k.lock(ctx, "u:ana"); !errors.Is(err, context.Canceled) {
		t.Fatalf("la espera debería cancelarse: %v", err)
	}
	unlock()
	if n := k.size(); n != 0 {
		t.Fatalf("el mapa de cerrojos debería quedar vacío, tiene %d", n)
	}
	e := newEnv(t)
	adminSetup(t, e)
	for i := 0; i < 20; i++ {
		e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": fmt.Sprintf("inventado%d", i), "password": "x"}, nil)
	}
	if n := e.srv.loginLocks.size(); n != 0 {
		t.Fatalf("tras los intentos no deben quedar cerrojos: %d", n)
	}
}

// H1 (revisión de código): sin X-Velora-Client y sin cabeceras de navegador, el acceso entrega cookie, nunca un Bearer.
func TestLoginWithoutClientHeaderGetsCookieNotToken(t *testing.T) {
	e := newEnv(t)
	adminSetup(t, e)
	raw, _ := json.Marshal(map[string]string{"username": "marta", "password": "contraseña-larga"})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 200 || len(res.Cookies()) == 0 {
		t.Fatalf("debería entrar con cookie: %d %v", res.StatusCode, res.Cookies())
	}
	if _, leaked := body["token"]; leaked {
		t.Fatal("sin declararse cliente nativo no debe recibir un token Bearer")
	}
}

// M3 (revisión de código): una petición de navegador sin Origin y con Sec-Fetch-Site vacío se rechaza; un cliente nativo
// (sin cabeceras de navegador) no pasa por esta comprobación.
func TestBrowserRequestWithoutOriginNeedsFetchSite(t *testing.T) {
	r := httptest.NewRequest("POST", "http://velora.test/api/v1/auth/login", nil)
	r.Header.Set("Sec-Fetch-Mode", "cors")
	if sameOrigin(r, nil) {
		t.Fatal("sin Origin y con Sec-Fetch-Site vacío no debe aceptarse")
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOrigin(r, nil) {
		t.Fatal("same-origin debe aceptarse")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameOrigin(r, nil) {
		t.Fatal("cross-site no debe aceptarse")
	}
	native := httptest.NewRequest("POST", "http://velora.test/api/v1/auth/login", nil)
	if fromBrowser(native) {
		t.Fatal("un cliente sin cabeceras de navegador no es un navegador")
	}
}

// Hallazgo adicional (revisión de código): los fallos del código de instalación se cuentan dentro de la misma
// transacción. Con un pool de UNA conexión no se bloquea (antes necesitaba una segunda), y con intentos simultáneos el
// contador no pierde ninguno: al llegar al máximo el código deja de valer.
func TestSetupCodeFailuresAtomicWithTinyPool(t *testing.T) {
	e := newEnv(t)
	cfg, err := pgxpool.ParseConfig(e.dbURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	tiny, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tiny.Close()
	st := &store.Store{Pool: tiny}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := 0
	for i := 0; i < setup.MaxFailures+3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.CreateFirstAdmin(ctx, []byte("codigo-incorrecto"), setup.MaxFailures, "marta", "C", "x", "Casa", "es-ES")
			mu.Lock()
			if errors.Is(err, store.ErrBadSetupCode) {
				bad++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		t.Fatal("los intentos se bloquearon esperando una conexión del pool")
	}
	if bad != setup.MaxFailures+3 {
		t.Fatalf("todos los intentos deberían fallar como código incorrecto: %d", bad)
	}
	var failures int
	_ = e.pool.QueryRow(context.Background(), `select (value #>> '{}')::int from settings where key = 'setup.code_failures'`).Scan(&failures)
	if failures != setup.MaxFailures+3 {
		t.Fatalf("el contador debería sumar todos los intentos: %d", failures)
	}
	body := map[string]string{"setup_code": e.code, "server_name": "Casa", "username": "marta", "display_name": "C", "password": "contraseña-larga"}
	if c := e.call(t, "POST", "/api/v1/setup", "", body, nil); c != 403 {
		t.Fatalf("tras agotar los intentos el código correcto no debe valer: %d", c)
	}
}
