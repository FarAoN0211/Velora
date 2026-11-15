package api

import (
	"io"
	"strings"
	"testing"
	"velora/internal/artwork"
)

func TestImageProviderSettingsNeverExposeSecrets(t *testing.T) {
	e := newE11Env(t)
	if err := artwork.Save(e.configDir, "fanart", artwork.Config{APIKey: "very-private", ClientKey: "private-client", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	res := e.raw(t, "GET", "/api/v1/admin/metadata/image-providers", nil)
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.Contains(string(b), "very-private") || strings.Contains(string(b), "private-client") {
		t.Fatalf("respuesta insegura: %d %s", res.StatusCode, b)
	}
	if c := e.call(t, "GET", "/api/v1/admin/metadata/image-providers", e.tokenB, nil, nil); c != 404 {
		t.Fatal("acceso no admin", c)
	}
	if c := e.call(t, "DELETE", "/api/v1/admin/metadata/image-providers/fanart", e.token, nil, nil); c != 200 {
		t.Fatal(c)
	}
	saved, _ := artwork.Load(e.configDir)
	if saved["fanart"].APIKey != "" || saved["fanart"].Enabled {
		t.Fatal("no eliminó la configuración")
	}
}
func TestExternalImageCannotProxyArbitraryURL(t *testing.T) {
	e := newI2Env(t)
	c := e.call(t, "GET", e.base+"/images/preview?provider=fanart&kind=logo&path=https%3A%2F%2F127.0.0.1%2Fprivate", e.token, nil, nil)
	if c != 422 {
		t.Fatalf("proxy: %d", c)
	}
	c = e.call(t, "POST", e.base+"/images/select", e.token, map[string]string{"kind": "logo", "provider": "fanart", "file_path": "https://127.0.0.1/private"}, nil)
	if c != 422 {
		t.Fatalf("selección: %d", c)
	}
}
