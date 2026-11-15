package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"velora/internal/authz"
	"velora/internal/config"
)

var methods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

func specPolicies(t *testing.T) map[string]string {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatalf("openapi.yaml no es YAML válido: %v", err)
	}
	out := map[string]string{}
	for path, ops := range doc.Paths {
		for m, op := range ops {
			if !methods[m] {
				continue
			}
			pol, _ := op["x-velora-policy"].(string)
			out[strings.ToUpper(m)+" "+path] = pol
		}
	}
	return out
}

// Toda ruta del router debe estar en el registro con política, y coincidir con openapi.yaml (en ambos sentidos).
func TestEveryRouteHasPolicyAndIsDocumented(t *testing.T) {
	s := New(config.Config{}, []byte("clave-de-prueba"), nil, nil, nil, nil)
	spec := specPolicies(t)
	seen := map[string]bool{}
	err := chi.Walk(s.router.(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		seen[key] = true
		rt, ok := s.registry.Lookup(method, route)
		if !ok {
			t.Errorf("ruta %s registrada fuera de handle(): no tiene política", key)
			return nil
		}
		if !rt.Policy.Valid() {
			t.Errorf("ruta %s sin política válida", key)
		}
		pol, documented := spec[key]
		if !documented {
			t.Errorf("ruta %s no está en openapi.yaml", key)
		} else if pol != rt.Policy.String() {
			t.Errorf("ruta %s: política %q en el servidor y %q en openapi.yaml", key, rt.Policy, pol)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for key := range spec {
		if !seen[key] {
			t.Errorf("openapi.yaml documenta %s, pero el servidor no la sirve", key)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no se encontró ninguna ruta")
	}
}

func TestRegisterWithoutPolicyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registrar una ruta sin política debería entrar en pánico")
		}
	}()
	s := &Server{registry: authz.NewRegistry()}
	s.handle(chi.NewRouter(), http.MethodGet, "/x", authz.Policy(0), func(http.ResponseWriter, *http.Request) {})
}

func TestRequirePolicies(t *testing.T) {
	s := &Server{csrfKey: []byte("clave-de-prueba")}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	user := &authz.Principal{UserID: uuid.New()}
	admin := &authz.Principal{UserID: uuid.New(), IsAdmin: true}
	cookieUser := &authz.Principal{UserID: uuid.New(), ViaCookie: true}

	cases := []struct {
		name   string
		policy authz.Policy
		who    *authz.Principal
		method string
		csrf   bool
		want   int
	}{
		{"pública sin sesión", authz.Public, nil, http.MethodGet, false, http.StatusTeapot},
		{"usuario sin sesión", authz.User, nil, http.MethodGet, false, http.StatusUnauthorized},
		{"usuario con sesión", authz.User, user, http.MethodGet, false, http.StatusTeapot},
		{"admin sin serlo devuelve 404", authz.Admin, user, http.MethodGet, false, http.StatusNotFound},
		{"admin", authz.Admin, admin, http.MethodPost, false, http.StatusTeapot},
		{"cookie sin CSRF en POST", authz.User, cookieUser, http.MethodPost, false, http.StatusForbidden},
		{"cookie con CSRF de su sesión en POST", authz.User, cookieUser, http.MethodPost, true, http.StatusTeapot},
		{"cookie sin CSRF en GET", authz.User, cookieUser, http.MethodGet, false, http.StatusTeapot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/x", nil)
			if c.who != nil {
				req = req.WithContext(authz.WithPrincipal(req.Context(), c.who))
			}
			if c.csrf {
				req.Header.Set(csrfHeader, s.csrfToken(c.who.SessionID))
			}
			rec := httptest.NewRecorder()
			s.require(c.policy)(ok).ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("estado %d, esperaba %d", rec.Code, c.want)
			}
		})
	}
}

func TestSetupValidation(t *testing.T) {
	good := setupRequest{ServerName: "Casa", Language: "es-ES", Username: "marta", DisplayName: "Marta", Password: "contraseña-larga"}
	if msg := validateSetup(good); msg != "" {
		t.Fatalf("datos válidos rechazados: %s", msg)
	}
	bad := []setupRequest{
		{ServerName: "", Language: "es-ES", Username: "marta", DisplayName: "C", Password: "contraseña-larga"},
		{ServerName: "Casa", Language: "en-US", Username: "marta", DisplayName: "C", Password: "contraseña-larga"},
		{ServerName: "Casa", Language: "es-ES", Username: "admin", DisplayName: "C", Password: "contraseña-larga"},
		{ServerName: "Casa", Language: "es-ES", Username: "a b", DisplayName: "C", Password: "contraseña-larga"},
		{ServerName: "Casa", Language: "es-ES", Username: "marta", DisplayName: "C", Password: "corta"},
	}
	for i, b := range bad {
		if validateSetup(b) == "" {
			t.Errorf("caso %d aceptado y debería rechazarse", i)
		}
	}
}
