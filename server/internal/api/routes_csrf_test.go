package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"velora/internal/authz"
)

// E0.1, punto 2: el token anti-CSRF solo vale para su propia sesión.
func TestCSRFBoundToSession(t *testing.T) {
	s := &Server{csrfKey: []byte("clave-de-prueba")}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mine := &authz.Principal{UserID: uuid.New(), SessionID: uuid.New(), ViaCookie: true}
	other := uuid.New()

	cases := map[string]struct {
		token string
		want  int
	}{
		"token de su sesión":         {s.csrfToken(mine.SessionID), http.StatusTeapot},
		"token de otra sesión":       {s.csrfToken(other), http.StatusForbidden},
		"token manipulado":           {s.csrfToken(mine.SessionID) + "x", http.StatusForbidden},
		"sin token":                  {"", http.StatusForbidden},
		"valor arbitrario inyectado": {"abc", http.StatusForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			req = req.WithContext(authz.WithPrincipal(req.Context(), mine))
			if c.token != "" {
				req.Header.Set(csrfHeader, c.token)
			}
			rec := httptest.NewRecorder()
			s.require(authz.User)(ok).ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("estado %d, esperaba %d", rec.Code, c.want)
			}
		})
	}
	// Otra clave de servidor produce otro token: no se puede precalcular.
	s2 := &Server{csrfKey: []byte("otra-clave")}
	if s2.csrfToken(mine.SessionID) == s.csrfToken(mine.SessionID) {
		t.Fatal("el token no depende de la clave del servidor")
	}
}

// E0.1, punto 3: la detección de navegador y la guarda de origen.
func TestOriginGuardAndBrowserDetection(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"nativo sin cabeceras de navegador", http.MethodPost, nil, http.StatusTeapot},
		{"navegador mismo origen", http.MethodPost, map[string]string{"Origin": "http://velora.local"}, http.StatusTeapot},
		{"navegador otro origen", http.MethodPost, map[string]string{"Origin": "http://malicioso.example"}, http.StatusForbidden},
		{"navegador sin Origin pero cross-site", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"GET de otro origen no se bloquea", http.MethodGet, map[string]string{"Origin": "http://malicioso.example"}, http.StatusTeapot},
		{"mismo host con otro esquema", http.MethodPost, map[string]string{"Origin": "https://velora.local"}, http.StatusForbidden},
		{"mismo host con otro puerto", http.MethodPost, map[string]string{"Origin": "http://velora.local:9999"}, http.StatusForbidden},
	}
	s := &Server{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "http://velora.local/x", nil)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			s.originGuard(ok).ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("estado %d, esperaba %d", rec.Code, c.want)
			}
		})
	}
	browser := httptest.NewRequest(http.MethodPost, "/x", nil)
	browser.Header.Set("Sec-Fetch-Mode", "cors")
	if !fromBrowser(browser) || fromBrowser(httptest.NewRequest(http.MethodPost, "/x", nil)) {
		t.Fatal("detección de navegador incorrecta")
	}
}

// Detrás de Nginx Proxy Manager con HTTPS, el origen de confianza se fija en VELORA_PUBLIC_URLS.
func TestOriginGuardWithPublicOrigins(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	s := &Server{}
	s.cfg.PublicOrigins = []string{"https://velora.casa.example", "http://192.168.1.10:8640"}
	for origin, want := range map[string]int{
		"https://velora.casa.example":  http.StatusTeapot,
		"http://192.168.1.10:8640":     http.StatusTeapot,
		"http://velora.casa.example":   http.StatusForbidden,
		"https://otro.example":         http.StatusForbidden,
		"https://velora.casa.example.": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodPost, "http://interno:8640/x", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		s.originGuard(ok).ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: estado %d, esperaba %d", origin, rec.Code, want)
		}
	}
}
