package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"velora/internal/store"
)

func TestRememberedProfilesSwitchAndRevocation(t *testing.T) {
	e := newE11Env(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	origin, _ := url.Parse(e.server.URL)
	call := func(method, path string, body any, out any) int {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, e.server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", e.server.URL)
		for _, c := range jar.Cookies(origin) {
			if c.Name == csrfCookie {
				req.Header.Set(csrfHeader, c.Value)
			}
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if out != nil {
			_ = json.NewDecoder(res.Body).Decode(out)
		}
		return res.StatusCode
	}
	if c := call("POST", "/api/v1/auth/login", map[string]string{"username": "marta", "password": "contraseña-larga"}, nil); c != 200 {
		t.Fatal("admin login", c)
	}
	var me struct {
		User store.User `json:"user"`
	}
	call("GET", "/api/v1/me", nil, &me)
	admin := me.User.ID
	// Arbitrary public profile IDs are insufficient, even with an active admin.
	if c := call("POST", "/api/v1/auth/switch", map[string]any{"user_id": e.userB}, nil); c != 403 {
		t.Fatal("unremembered profile accessible", c)
	}
	if c := call("POST", "/api/v1/auth/login", map[string]string{"username": "lucia", "password": "otra-contraseña-larga"}, nil); c != 200 {
		t.Fatal("second login", c)
	}
	var list struct {
		Profiles []profile `json:"profiles"`
	}
	if c := call("GET", "/api/v1/me/profiles", nil, &list); c != 200 || len(list.Profiles) != 2 {
		t.Fatalf("saved profiles %d %+v", c, list)
	}
	if c := call("POST", "/api/v1/auth/switch", map[string]any{"user_id": admin}, &me); c != 200 || !me.User.IsAdmin {
		t.Fatal("switch to remembered admin", c, me)
	}
	if c := call("POST", "/api/v1/auth/switch", map[string]any{"user_id": e.userB}, &me); c != 200 || me.User.IsAdmin {
		t.Fatal("switch did not replace permissions", c, me)
	}
	if c := call("GET", "/api/v1/admin/now-playing", nil, nil); c != 404 {
		t.Fatal("old admin privileges retained", c)
	}
	if _, err := e.pool.Exec(context.Background(), `update sessions set revoked_at=now() where user_id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	if c := call("POST", "/api/v1/auth/switch", map[string]any{"user_id": admin}, nil); c != 403 {
		t.Fatal("revoked remembered session accepted", c)
	}
	if c := call("POST", "/api/v1/auth/logout", nil, nil); c != 204 {
		t.Fatal(c)
	}
	for _, c := range jar.Cookies(origin) {
		if c.Name == profilesCookie {
			t.Fatal("logout retained current remembered credential")
		}
	}
	if c := e.call(t, "GET", "/api/v1/me/profiles", "", nil, nil); c != 401 {
		t.Fatal("anonymous can list saved profiles", c)
	}
}

func TestPersonalLibraryOrderMatchesHome(t *testing.T) {
	e := newE11Env(t)
	e.grantB(t, e.pelis.ID, e.anim.ID)
	ids := []uuid.UUID{e.anim.ID, e.pelis.ID}
	if c := e.call(t, "PUT", "/api/v1/me/libraries/order", e.tokenB, map[string]any{"ids": ids}, nil); c != 204 {
		t.Fatal("order", c)
	}
	var libs struct {
		Libraries []store.Library `json:"libraries"`
	}
	e.call(t, "GET", "/api/v1/libraries", e.tokenB, nil, &libs)
	if len(libs.Libraries) != 2 || libs.Libraries[0].ID != ids[0] {
		t.Fatal("menu order", libs)
	}
	var home struct {
		Rows []struct {
			Library store.Library `json:"library"`
		} `json:"rows"`
	}
	e.call(t, "GET", "/api/v1/home", e.tokenB, nil, &home)
	if len(home.Rows) != 2 || home.Rows[0].Library.ID != ids[0] {
		t.Fatal("home order differs", home)
	}
	e.call(t, "GET", "/api/v1/libraries", e.token, nil, &libs)
	if libs.Libraries[0].ID != e.pelis.ID {
		t.Fatal("changed another user's order")
	}
	for _, bad := range [][]uuid.UUID{{ids[0], ids[0]}, {uuid.New()}} {
		if c := e.call(t, "PUT", "/api/v1/me/libraries/order", e.tokenB, map[string]any{"ids": bad}, nil); c != 422 {
			t.Fatal("bad order", c)
		}
	}
	e.grantB(t, e.pelis.ID)
	if c := e.call(t, "PUT", "/api/v1/me/libraries/order", e.tokenB, map[string]any{"ids": ids}, nil); c != 422 {
		t.Fatal("revoked library order allowed", c)
	}
}
