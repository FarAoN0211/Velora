package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"
	"velora/internal/auth"
	"velora/internal/store"
)

type householdBrowser struct {
	t      *testing.T
	e      *testEnv
	client *http.Client
	origin *url.URL
}

func newHouseholdBrowser(t *testing.T, e *testEnv) *householdBrowser {
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(e.server.URL)
	return &householdBrowser{t, e, &http.Client{Jar: jar}, u}
}
func (b *householdBrowser) call(method, path string, body, out any) int {
	b.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, b.e.server.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.e.server.URL)
	for _, c := range b.client.Jar.Cookies(b.origin) {
		if c.Name == csrfCookie {
			req.Header.Set(csrfHeader, c.Value)
		}
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}
func (b *householdBrowser) session(t *testing.T) (uuid.UUID, store.User) {
	for _, c := range b.client.Jar.Cookies(b.origin) {
		if c.Name == sessionCookie {
			id, u, e := b.e.srv.store.SessionUser(context.Background(), auth.HashToken(c.Value), b.e.srv.cfg.SessionTTL)
			if e != nil {
				t.Fatal(e)
			}
			return id, u
		}
	}
	t.Fatal("missing session")
	return uuid.Nil, store.User{}
}
func TestHouseholdPasswordlessIsolationPINAndRevocation(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	pair := createPair(t, e.testEnv)
	var made struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.tokenB, map[string]any{"code": pair.Code, "can_create_household": true, "username": "hogar", "display_name": "Familia", "library_ids": []uuid.UUID{e.pelis.ID}}, nil); c != 404 {
		t.Fatal("member could create household", c)
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.token, map[string]any{"code": pair.Code, "can_create_household": true, "username": "hogar", "display_name": "Familia", "library_ids": []uuid.UUID{e.pelis.ID}}, &made); c != 201 {
		t.Fatal("create", c)
	}
	b := newHouseholdBrowser(t, e.testEnv)
	var logged struct {
		User store.User `json:"user"`
	}
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, &logged); c != 200 || logged.User.IsAdmin || logged.User.HouseholdOwner || !logged.User.Passwordless || logged.User.ID != made.ID || !logged.User.CanCreateHousehold {
		t.Fatal("initial device wrong account", c, logged)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, &logged); c != 201 || !logged.User.HouseholdOwner || logged.User.HouseholdID == nil {
		t.Fatal("crear mi hogar", c, logged)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 409 {
		t.Fatal("un segundo hogar debe rechazarse", c)
	}
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 410 {
		t.Fatal("reused code", c)
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "hogar", "password": ""}, nil); c != 401 {
		t.Fatal("empty password login", c)
	}
	var libs struct {
		Libraries []store.Library `json:"libraries"`
	}
	if c := b.call("GET", "/api/v1/libraries", nil, &libs); c != 200 || len(libs.Libraries) != 1 || libs.Libraries[0].ID != e.pelis.ID {
		t.Fatal("owner libraries", c, libs)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "1234"}, nil); c != 200 { // devuelve el código de recuperación
		t.Fatal("owner pin", c)
	}
	var child struct {
		ID uuid.UUID `json:"id"`
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Familiar"}, &child); c != 201 {
		t.Fatal("create child", c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": child.ID}, &logged); c != 200 || logged.User.HouseholdOwner {
		t.Fatal("switch child", c, logged)
	}
	childSession, _ := b.session(t)
	if c := b.call("GET", "/api/v1/libraries", nil, &libs); c != 200 || len(libs.Libraries) != 1 {
		t.Fatal("inherited libraries", c, libs)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Nieto"}, nil); c != 403 {
		t.Fatal("nested users allowed", c)
	}
	if c := b.call("POST", "/api/v1/auth/device/inspect", map[string]string{"code": "123456"}, nil); c != 403 {
		t.Fatal("child linking allowed", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "5678"}, nil); c != 200 { // devuelve el código de recuperación
		t.Fatal("child pin", c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": made.ID}, nil); c != 403 {
		t.Fatal("owner pin bypass", c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": made.ID, "pin": "1234"}, nil); c != 200 {
		t.Fatal("owner PIN", c)
	}
	ownerSession, _ := b.session(t)
	if err := e.srv.store.ReservePlayback(ctx, ownerSession, made.ID, e.pelisFile, "owner", "192.0.2.1", 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.store.ReservePlayback(ctx, childSession, child.ID, e.pelisFile, "child", "192.0.2.2", 1, 0); !errors.Is(err, store.ErrPlaybackUserLimit) {
		t.Fatal("family bypasses concurrent cap", err)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": e.userB}, nil); c != 403 {
		t.Fatal("unrelated account accessible", c)
	}
	next := createPair(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/approve", map[string]any{"code": next.Code, "user_id": child.ID}, nil); c != 403 {
		t.Fatal("link without PIN", c)
	}
	if c := b.call("POST", "/api/v1/auth/device/approve", map[string]any{"code": next.Code, "user_id": child.ID, "pin": "5678"}, nil); c != 204 {
		t.Fatal("link child", c)
	}
	b2 := newHouseholdBrowser(t, e.testEnv)
	if c := b2.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": next.Token}, &logged); c != 200 || logged.User.ID != child.ID {
		t.Fatal("wrong linked child", c, logged)
	}
	if c := b.call("PATCH", "/api/v1/me/household/profiles/"+child.ID.String(), map[string]bool{"reset_pin": true}, nil); c != 403 {
		t.Fatal("restablecer sin el PIN del propietario", c)
	}
	if c := b.call("PATCH", "/api/v1/me/household/profiles/"+child.ID.String(), map[string]any{"reset_pin": true, "owner_pin": "1234"}, nil); c != 204 {
		t.Fatal("reset", c)
	}
	if c := b2.call("GET", "/api/v1/me", nil, nil); c != 401 {
		t.Fatal("reset did not revoke sessions", c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": child.ID}, nil); c != 200 {
		t.Fatal("reset profile inaccessible", c)
	}
	if c := b.call("GET", "/api/v1/items/"+e.animItem.String(), nil, nil); c != 404 {
		t.Fatal("outside household library", c)
	}
	if c := e.call(t, "PUT", "/api/v1/admin/users/"+made.ID.String()+"/libraries", e.token, map[string]any{"library_ids": []uuid.UUID{}}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := b.call("GET", "/api/v1/libraries", nil, &libs); c != 200 || len(libs.Libraries) != 0 {
		t.Fatal("removed library retained", c, libs)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+made.ID.String(), e.token, map[string]bool{"is_disabled": true}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := b.call("GET", "/api/v1/me", nil, nil); c != 401 {
		t.Fatal("disabled owner retained family session", c)
	}
}
func TestHouseholdFailedCreationAtomicAndPINLockout(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	ctx := context.Background()
	pair := createPair(t, e)
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", admin, map[string]any{"code": pair.Code, "can_create_household": true, "username": "atomic", "display_name": "Atomic", "library_ids": []uuid.UUID{uuid.New()}}, nil); c != 404 {
		t.Fatal(c)
	}
	var exists bool
	_ = e.pool.QueryRow(ctx, `select exists(select 1 from users where username='atomic')`).Scan(&exists)
	if exists {
		t.Fatal("orphan user")
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", admin, map[string]any{"code": pair.Code, "can_create_household": true, "username": "atomic", "display_name": "Atomic"}, nil); c != 201 {
		t.Fatal("code consumed on failure", c)
	}
	b := newHouseholdBrowser(t, e)
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 201 {
		t.Fatal("crear mi hogar", c)
	}
	_, u := b.session(t)
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "2468"}, nil); c != 200 { // devuelve el código de recuperación
		t.Fatal(c)
	}
	for i := 0; i < 5; i++ {
		if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": u.ID, "pin": "0000"}, nil); c != 403 {
			t.Fatal("wrong PIN", i, c)
		}
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": u.ID, "pin": "2468"}, nil); c != 429 {
		t.Fatal("PIN lockout bypass", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"current_pin": "2468", "pin": "1357"}, nil); c != 429 {
		t.Fatal("PIN change bypass lock", c)
	}
}

// Límite de miembros del hogar: 9 por defecto, lo cambia el administrador en el responsable; los perfiles familiares no
// tienen límites propios.
func TestHouseholdMemberLimit(t *testing.T) {
	e := newE11Env(t)
	pair := createPair(t, e.testEnv)
	var made struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.token, map[string]any{"code": pair.Code, "can_create_household": true, "username": "casa", "display_name": "Casa", "library_ids": []uuid.UUID{e.pelis.ID}}, &made); c != 201 {
		t.Fatal("create", c)
	}
	b := newHouseholdBrowser(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 200 {
		t.Fatal("owner device", c)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 201 {
		t.Fatal("crear mi hogar", c)
	}
	var house struct {
		Max     int `json:"max_members"`
		Members int `json:"members"`
	}
	if c := b.call("GET", "/api/v1/me/household", nil, &house); c != 200 || house.Max != 9 || house.Members != 0 {
		t.Fatal("default limit", c, house)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+made.ID.String(), e.token, map[string]any{"max_household_members": 1}, nil); c != 204 {
		t.Fatal("set members", c)
	}
	var child struct {
		ID uuid.UUID `json:"id"`
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Uno"}, &child); c != 201 {
		t.Fatal("first member", c)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Dos"}, nil); c != 429 {
		t.Fatal("limit bypass", c)
	}
	if c := b.call("GET", "/api/v1/me/household", nil, &house); c != 200 || house.Max != 1 || house.Members != 1 {
		t.Fatal("limit shown", c, house)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+child.ID.String(), e.token, map[string]any{"max_household_members": 5}, nil); c != 404 {
		t.Fatal("family profile got its own limit", c)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+made.ID.String(), e.token, map[string]any{"max_household_members": 20}, nil); c != 422 {
		t.Fatal("over 19 accepted", c)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+made.ID.String(), e.token, map[string]any{"max_household_members": 2}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Dos"}, nil); c != 201 {
		t.Fatal("raised limit", c)
	}
}

// Fase 1 de replica/PLAN_HOGARES_Y_PIN.md: el permiso de crear hogar lo concede y retira solo el administrador, a
// cuentas principales; retirarlo no toca a los miembros; la cuenta lo ve en /me junto con su opción de bloqueo.
func TestHouseholdPermissionByAdmin(t *testing.T) {
	e := newE11Env(t)
	pair := createPair(t, e.testEnv)
	var made struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.token, map[string]any{"code": pair.Code, "can_create_household": true, "username": "casa", "display_name": "Casa", "library_ids": []uuid.UUID{e.pelis.ID}}, &made); c != 201 {
		t.Fatal("create", c)
	}
	b := newHouseholdBrowser(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 200 {
		t.Fatal("owner device", c)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 201 {
		t.Fatal("crear mi hogar", c)
	}
	var child struct {
		ID uuid.UUID `json:"id"`
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Uno"}, &child); c != 201 {
		t.Fatal("member", c)
	}
	list := func() map[uuid.UUID]store.UserSummary {
		var out struct {
			Users []store.UserSummary `json:"users"`
		}
		if c := e.call(t, "GET", "/api/v1/admin/users", e.token, nil, &out); c != 200 {
			t.Fatal("list", c)
		}
		m := map[uuid.UUID]store.UserSummary{}
		for _, u := range out.Users {
			m[u.ID] = u
		}
		return m
	}
	users := list()
	if !users[made.ID].CanCreateHousehold || users[e.userB].CanCreateHousehold {
		t.Fatal("permiso inicial: el responsable lo conserva y una cuenta nueva no lo tiene", users[made.ID], users[e.userB])
	}
	user := "/api/v1/admin/users/" + e.userB.String()
	if c := e.call(t, "PATCH", user, e.token, map[string]bool{"can_create_household": true}, nil); c != 204 {
		t.Fatal("grant", c)
	}
	var me struct {
		User store.User `json:"user"`
	}
	if c := e.call(t, "GET", "/api/v1/me", e.tokenB, nil, &me); c != 200 || !me.User.CanCreateHousehold || me.User.PinLockMinutes != 60 {
		t.Fatal("la cuenta debe ver su permiso y el bloqueo predeterminado de 1 hora", c, me.User)
	}
	if c := e.call(t, "PATCH", user, e.tokenB, map[string]bool{"can_create_household": false}, nil); c != 404 {
		t.Fatal("una cuenta normal no puede cambiar permisos", c)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+child.ID.String(), e.token, map[string]bool{"can_create_household": true}, nil); c != 404 {
		t.Fatal("un perfil familiar no puede recibir el permiso de hogar", c)
	}
	// Retirar el permiso al responsable no elimina ni desactiva a sus miembros.
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+made.ID.String(), e.token, map[string]bool{"can_create_household": false}, nil); c != 204 {
		t.Fatal("revoke", c)
	}
	var house struct {
		Profiles []struct {
			ID       uuid.UUID `json:"id"`
			Disabled bool      `json:"is_disabled"`
		} `json:"profiles"`
	}
	if c := b.call("GET", "/api/v1/me/household", nil, &house); c != 200 || len(house.Profiles) != 2 {
		t.Fatal("el hogar sigue con sus dos perfiles tras retirar el permiso", c, house)
	}
	for _, p := range house.Profiles {
		if p.Disabled {
			t.Fatal("retirar el permiso no desactiva a nadie", p)
		}
	}
	if users = list(); users[made.ID].CanCreateHousehold {
		t.Fatal("el permiso retirado debe verse en el panel")
	}
}

// Fase 2 de replica/PLAN_HOGARES_Y_PIN.md: «Crear mi hogar» del propietario, miembros, papelera de 30 días y purga.
func TestHouseholdOwnerFlowAndTrash(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	pair := createPair(t, e.testEnv)
	var made struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.token, map[string]any{"code": pair.Code, "username": "casa",
		"display_name": "Casa", "library_ids": []uuid.UUID{e.pelis.ID}}, &made); c != 201 {
		t.Fatal("cuenta sin contraseña", c)
	}
	b := newHouseholdBrowser(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 200 {
		t.Fatal("dispositivo", c)
	}
	// Sin permiso del administrador no hay hogar; con él, un botón y listo.
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 403 {
		t.Fatal("sin permiso no se crea hogar", c)
	}
	owner := "/api/v1/admin/users/" + made.ID.String()
	if c := e.call(t, "PATCH", owner, e.token, map[string]any{"can_create_household": true, "max_household_members": 2}, nil); c != 204 {
		t.Fatal("permiso", c)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 201 {
		t.Fatal("crear mi hogar", c)
	}
	type house struct {
		Name     string `json:"name"`
		Members  int    `json:"members"`
		Max      int    `json:"max_members"`
		CanAdd   bool   `json:"can_add"`
		CanGrow  bool   `json:"can_grow"`
		Streams  int    `json:"max_streams"`
		Profiles []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"display_name"`
		} `json:"profiles"`
		Trash []store.TrashedMember `json:"trash"`
	}
	get := func() house {
		t.Helper()
		var h house
		if c := b.call("GET", "/api/v1/me/household", nil, &h); c != 200 {
			t.Fatal("hogar", c)
		}
		return h
	}
	if h := get(); h.Name != "Familia de Casa" || h.Members != 0 || h.Max != 2 || !h.CanAdd || !h.CanGrow || h.Streams != 0 || len(h.Trash) != 0 {
		t.Fatal("hogar recién creado", h)
	}
	if c := b.call("PATCH", "/api/v1/me/household", map[string]string{"name": "Los Pérez"}, nil); c != 204 || get().Name != "Los Pérez" {
		t.Fatal("renombrar el hogar", c)
	}
	add := func(name string) uuid.UUID {
		t.Helper()
		var m struct {
			ID uuid.UUID `json:"id"`
		}
		if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": name}, &m); c != 201 {
			t.Fatal("añadir", name, c)
		}
		return m.ID
	}
	ana, luis := add("Ana"), add("Luis")
	if h := get(); h.Members != 2 || h.CanAdd {
		t.Fatal("hogar lleno", h)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Tercero"}, nil); c != 429 {
		t.Fatal("límite superado", c)
	}
	if c := b.call("PATCH", "/api/v1/me/household/profiles/"+ana.String(), map[string]string{"display_name": "Ana María"}, nil); c != 204 {
		t.Fatal("renombrar miembro", c)
	}

	// Ana usa su perfil y reproduce; un miembro no gestiona el hogar.
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana}, nil); c != 200 {
		t.Fatal("cambiar a Ana", c)
	}
	if c := b.call("POST", "/api/v1/playback/decide", map[string]any{"file_id": e.pelisFile, "caps": chromiumCaps, "player_id": "tv-ana"}, nil); c != 200 {
		t.Fatal("Ana reproduce", c)
	}
	_, me := b.session(t)
	if me.Username == "casa" || me.DisplayName != "Ana María" {
		t.Fatal("el perfil activo debe ser Ana", me)
	}
	for _, try := range []struct{ method, path string }{{"DELETE", "/api/v1/me/household/profiles/" + luis.String()},
		{"POST", "/api/v1/me/household/profiles/" + luis.String() + "/restore"}, {"PATCH", "/api/v1/me/household"},
		{"POST", "/api/v1/me/household/profiles"}} {
		if c := b.call(try.method, try.path, map[string]string{"name": "x", "display_name": "x"}, nil); c != 403 {
			t.Fatal("un miembro no gestiona el hogar", try, c)
		}
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 409 {
		t.Fatal("un miembro no crea otro hogar", c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": made.ID}, nil); c != 200 {
		t.Fatal("volver al propietario", c)
	}

	// Papelera: Ana deja de poder usarse y de reproducir al instante, y no cuenta en el límite.
	if _, err := e.pool.Exec(ctx, `insert into user_item_data (user_id, item_id, position_ms) values ($1, $2, 4242)`, ana, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if c := b.call("DELETE", "/api/v1/me/household/profiles/"+ana.String(), nil, nil); c != 204 {
		t.Fatal("eliminar", c)
	}
	var live, leases int
	_ = e.pool.QueryRow(ctx, `select count(*) filter (where revoked_at is null), (select count(*) from playback_leases l join sessions x on x.id = l.session_id where x.user_id = $1)
		from sessions where user_id = $1`, ana).Scan(&live, &leases)
	if live != 0 || leases != 0 {
		t.Fatal("un eliminado no puede tener sesiones ni reproducciones", live, leases)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana}, nil); c == 200 {
		t.Fatal("un eliminado no se puede seleccionar")
	}
	h := get()
	if h.Members != 1 || !h.CanAdd || len(h.Trash) != 1 || h.Trash[0].ID != ana || h.Trash[0].PurgeAt.Sub(h.Trash[0].DeletedAt) != 30*24*time.Hour {
		t.Fatal("papelera", h)
	}
	for _, p := range h.Profiles {
		if p.ID == ana {
			t.Fatal("un eliminado no aparece entre los perfiles")
		}
	}
	// Recuperar nunca supera el límite.
	carla := add("Carla")
	if c := b.call("POST", "/api/v1/me/household/profiles/"+ana.String()+"/restore", nil, nil); c != 429 {
		t.Fatal("recuperar sin hueco", c)
	}
	if c := b.call("DELETE", "/api/v1/me/household/profiles/"+carla.String(), nil, nil); c != 204 {
		t.Fatal(c)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles/"+ana.String()+"/restore", nil, nil); c != 204 {
		t.Fatal("recuperar con hueco", c)
	}
	var pos int64
	_ = e.pool.QueryRow(ctx, `select position_ms from user_item_data where user_id = $1`, ana).Scan(&pos)
	if pos != 4242 {
		t.Fatal("Ana vuelve con su historial intacto", pos)
	}

	// Sin el permiso de hogar: nadie se elimina, pero no se añade ni se recupera.
	if c := e.call(t, "PATCH", owner, e.token, map[string]bool{"can_create_household": false}, nil); c != 204 {
		t.Fatal(c)
	}
	if h := get(); h.Members != 2 || h.CanAdd || h.CanGrow {
		t.Fatal("permiso retirado", h)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles/"+carla.String()+"/restore", nil, nil); c != 403 {
		t.Fatal("sin permiso no se recupera", c)
	}
	if c := e.call(t, "PATCH", owner, e.token, map[string]any{"can_create_household": true, "max_household_members": 3}, nil); c != 204 {
		t.Fatal(c)
	}

	// Eliminar ya: solo lo que está en la papelera.
	if c := b.call("DELETE", "/api/v1/me/household/profiles/"+luis.String()+"/permanent", nil, nil); c != 404 {
		t.Fatal("no se borra de golpe a quien no está en la papelera", c)
	}
	if c := b.call("DELETE", "/api/v1/me/household/profiles/"+carla.String()+"/permanent", nil, nil); c != 204 {
		t.Fatal("eliminar ya", c)
	}
	var exists bool
	_ = e.pool.QueryRow(ctx, `select exists (select 1 from users where id = $1)`, carla).Scan(&exists)
	if exists {
		t.Fatal("Carla debe estar borrada definitivamente")
	}

	// A los 30 días: ya no se recupera y la tarea nocturna lo borra con su historial.
	if c := b.call("DELETE", "/api/v1/me/household/profiles/"+ana.String(), nil, nil); c != 204 {
		t.Fatal(c)
	}
	if _, err := e.pool.Exec(ctx, `update users set deleted_at = now() - interval '31 days' where id = $1`, ana); err != nil {
		t.Fatal(err)
	}
	if c := b.call("POST", "/api/v1/me/household/profiles/"+ana.String()+"/restore", nil, nil); c != 410 {
		t.Fatal("papelera caducada", c)
	}
	n, err := e.srv.store.PurgeExpiredMembers(ctx)
	if err != nil || n != 1 {
		t.Fatal("purga", n, err)
	}
	var history int
	_ = e.pool.QueryRow(ctx, `select count(*) from user_item_data where user_id = $1`, ana).Scan(&history)
	_ = e.pool.QueryRow(ctx, `select exists (select 1 from users where id = $1)`, ana).Scan(&exists)
	if exists || history != 0 {
		t.Fatal("la purga borra el perfil y su historial", exists, history)
	}
	if n, _ := e.srv.store.PurgeExpiredMembers(ctx); n != 0 {
		t.Fatal("la purga no toca nada más", n)
	}

	// El administrador ve la estructura sin el contenido de los perfiles.
	var list struct {
		Users []store.UserSummary `json:"users"`
	}
	e.call(t, "GET", "/api/v1/admin/users", e.token, nil, &list)
	for _, u := range list.Users {
		if u.ID == made.ID && u.HouseholdMembers != 1 {
			t.Fatal("estructura del hogar en el panel", u)
		}
	}
}
