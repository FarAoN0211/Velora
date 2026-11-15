package api

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"velora/internal/store"
)

// Fase 3 de replica/PLAN_HOGARES_Y_PIN.md: PIN de 4 dígitos, código de recuperación, restablecimiento con sellado y
// cierre de las vías alternativas de acceso al historial protegido.
func TestPINRecoverySealingAndBackdoors(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	pair := createPair(t, e.testEnv)
	var made struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/accounts/passwordless", e.token, map[string]any{"code": pair.Code, "username": "casa",
		"display_name": "Casa", "library_ids": []uuid.UUID{e.pelis.ID}, "can_create_household": true}, &made); c != 201 {
		t.Fatal("cuenta", c)
	}
	b := newHouseholdBrowser(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": pair.Token}, nil); c != 200 {
		t.Fatal("dispositivo", c)
	}
	if c := b.call("POST", "/api/v1/me/household", nil, nil); c != 201 {
		t.Fatal("hogar", c)
	}
	var m struct {
		ID uuid.UUID `json:"id"`
	}
	if c := b.call("POST", "/api/v1/me/household/profiles", map[string]string{"display_name": "Ana"}, &m); c != 201 {
		t.Fatal("miembro", c)
	}
	ana := m.ID
	type pinReply struct {
		Code string `json:"recovery_code"`
	}
	sw := func(user uuid.UUID, pin string) int {
		t.Helper()
		body := map[string]any{"user_id": user}
		if pin != "" {
			body["pin"] = pin
		}
		return b.call("POST", "/api/v1/auth/switch", body, nil)
	}
	position := func() int64 {
		t.Helper()
		var it struct {
			Item store.ItemDetail `json:"item"`
		}
		if c := b.call("GET", "/api/v1/items/"+e.pelisItem.String(), nil, &it); c != 200 {
			t.Fatal("ficha", c)
		}
		return it.Item.PositionMS
	}

	// Ana activa su PIN: 4 dígitos y un código de recuperación que solo se ve ahora.
	if c := sw(ana, ""); c != 200 {
		t.Fatal("entrar como Ana", c)
	}
	var first pinReply
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "123456"}, nil); c != 422 {
		t.Fatal("un PIN nuevo de 6 dígitos no vale", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "1111"}, &first); c != 200 || len(first.Code) != 9 {
		t.Fatal("activar el PIN", c, first)
	}
	if _, err := e.pool.Exec(ctx, `insert into user_item_data (user_id, item_id, position_ms, last_played_at) values ($1, $2, 777000, now())`, ana, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `update users set preferences = '{"secreto":"de Ana"}' where id = $1`, ana); err != nil {
		t.Fatal(err)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "2222"}, nil); c != 403 {
		t.Fatal("una sesión abierta no basta para cambiar el PIN", c)
	}

	// 1. Ana olvida el PIN: con su código lo recupera y conserva todo. El código usado deja de valer.
	if c := sw(made.ID, ""); c != 200 {
		t.Fatal("volver al propietario", c)
	}
	var rec pinReply
	if c := b.call("POST", "/api/v1/auth/pin/recover", map[string]any{"user_id": ana, "recovery_code": first.Code, "pin": "3333"}, &rec); c != 200 || rec.Code == "" || rec.Code == first.Code {
		t.Fatal("recuperar con el código", c, rec)
	}
	if _, u := b.session(t); u.ID != ana {
		t.Fatal("tras recuperar se entra en el perfil de Ana", u.ID)
	}
	if p := position(); p != 777000 {
		t.Fatal("recuperar con el código conserva el historial", p)
	}
	beforeReset := rec.Code // el código que Ana tiene guardado cuando el propietario restablece
	if c := sw(made.ID, ""); c != 200 {
		t.Fatal(c)
	}
	if c := b.call("POST", "/api/v1/auth/pin/recover", map[string]any{"user_id": ana, "recovery_code": first.Code, "pin": "4444"}, nil); c != 403 {
		t.Fatal("un código ya usado no vale", c)
	}

	// 2. El administrador no puede quitar el PIN de un miembro ni vincular un dispositivo a su perfil sin él.
	if c := e.call(t, "POST", "/api/v1/admin/users/"+ana.String()+"/reset-pin", e.token, nil, nil); c != 403 {
		t.Fatal("el administrador no restablece el PIN de un miembro", c)
	}
	adminDevice := createPair(t, e.testEnv)
	if c := e.call(t, "POST", "/api/v1/auth/device/approve", e.token, map[string]any{"code": adminDevice.Code, "user_id": ana}, nil); c != 403 {
		t.Fatal("el administrador no vincula un dispositivo al perfil de Ana sin su PIN", c)
	}

	// 3. Ana pierde PIN y código: el propietario restablece, el perfil vuelve a usarse... vacío.
	if c := b.call("PATCH", "/api/v1/me/household/profiles/"+ana.String(), map[string]bool{"reset_pin": true}, nil); c != 204 {
		t.Fatal("restablecer", c)
	}
	var live, sealed, own int
	var prefs string
	_ = e.pool.QueryRow(ctx, `select (select count(*) from sessions where user_id = $1 and revoked_at is null),
		(select count(*) from sealed_item_data d join profile_seals p on p.id = d.seal_id where p.user_id = $1),
		(select count(*) from user_item_data where user_id = $1), (select preferences::text from users where id = $1)`, ana).Scan(&live, &sealed, &own, &prefs)
	if live != 0 || sealed != 1 || own != 0 || prefs != "{}" {
		t.Fatal("restablecer sella historial y preferencias y cierra sesiones", live, sealed, own, prefs)
	}
	if c := sw(ana, ""); c != 200 {
		t.Fatal("el perfil vuelve a poder usarse", c)
	}
	if p := position(); p != 0 {
		t.Fatal("EL PROPIETARIO NO DEBE VER EL HISTORIAL PROTEGIDO DE ANA", p)
	}
	var seals struct {
		Seals []store.Seal `json:"seals"`
	}
	if c := b.call("GET", "/api/v1/me/history/seals", nil, &seals); c != 200 || len(seals.Seals) != 1 || seals.Seals[0].Items != 1 {
		t.Fatal("historial sellado visible para su titular (solo fecha y número)", c, seals)
	}
	// Un código nuevo (el que se crea al poner el PIN después) no abre el historial sellado; el anterior, sí.
	var after pinReply
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "5555"}, &after); c != 200 {
		t.Fatal("Ana pone un PIN nuevo", c)
	}
	if c := b.call("POST", "/api/v1/me/history/unseal", map[string]string{"recovery_code": after.Code}, nil); c != 403 {
		t.Fatal("el código nuevo no devuelve el historial anterior", c)
	}
	var back struct {
		Items int `json:"items"`
	}
	if c := b.call("POST", "/api/v1/me/history/unseal", map[string]string{"recovery_code": beforeReset}, &back); c != 200 || back.Items != 1 {
		t.Fatal("el código anterior devuelve el historial", c, back)
	}
	if p := position(); p != 777000 {
		t.Fatal("historial devuelto", p)
	}
	_ = e.pool.QueryRow(ctx, `select preferences::text from users where id = $1`, ana).Scan(&prefs)
	if prefs != `{"secreto": "de Ana"}` {
		t.Fatal("preferencias devueltas", prefs)
	}

	// 4. El propietario con PIN debe teclearlo para restablecer (otro miembro no usa su sesión abierta).
	if c := sw(made.ID, ""); c != 200 {
		t.Fatal(c)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "9090"}, nil); c != 200 {
		t.Fatal("PIN del propietario", c)
	}
	if c := b.call("PATCH", "/api/v1/me/household/profiles/"+ana.String(), map[string]any{"reset_pin": true, "owner_pin": "0000"}, nil); c != 403 {
		t.Fatal("PIN del propietario incorrecto", c)
	}

	// 5. Contraseña restablecida por el administrador en una cuenta con PIN: sellado. Sin PIN: no.
	if c := e.call(t, "PUT", "/api/v1/me/pin", e.tokenB, map[string]string{"pin": "4321"}, nil); c != 200 {
		t.Fatal("PIN de Lucía", c)
	}
	if _, err := e.pool.Exec(ctx, `insert into user_item_data (user_id, item_id, position_ms) values ($1, $2, 1234)`, e.userB, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "POST", "/api/v1/admin/users/"+e.userB.String()+"/reset-pin", e.token, nil, nil); c != 409 {
		t.Fatal("una cuenta con contraseña recupera el PIN con ella, no el administrador", c)
	}
	var reset struct {
		Sealed bool `json:"sealed"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/users/"+e.userB.String()+"/password", e.token,
		map[string]string{"admin_password": "contraseña-larga", "password": "otra-contraseña-nueva"}, &reset); c != 200 || !reset.Sealed {
		t.Fatal("restablecer la contraseña de una cuenta con PIN sella su historial", c, reset)
	}
	_ = e.pool.QueryRow(ctx, `select count(*) from user_item_data where user_id = $1`, e.userB).Scan(&own)
	if own != 0 {
		t.Fatal("EL ADMINISTRADOR NO DEBE PODER VER EL HISTORIAL PROTEGIDO CON LA CONTRASEÑA NUEVA", own)
	}
	if c := e.call(t, "POST", "/api/v1/admin/users/"+e.userB.String()+"/password", e.token,
		map[string]string{"admin_password": "contraseña-larga", "password": "una-tercera-contraseña"}, &reset); c != 200 || reset.Sealed {
		t.Fatal("sin PIN no hay nada que sellar", c, reset)
	}

	// 6. El titular con contraseña recupera su propio PIN con ella (verificada ahora) y conserva su historial.
	var login struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "lucia", "password": "una-tercera-contraseña"}, &login); c != 200 {
		t.Fatal("login de Lucía", c)
	}
	if c := e.call(t, "PUT", "/api/v1/me/pin", login.Token, map[string]string{"pin": "1212"}, nil); c != 200 {
		t.Fatal(c)
	}
	if _, err := e.pool.Exec(ctx, `insert into user_item_data (user_id, item_id, position_ms) values ($1, $2, 999)`, e.userB, e.pelisItem); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "PUT", "/api/v1/me/pin", login.Token, map[string]string{"password": "mala", "pin": "3434"}, nil); c != 403 {
		t.Fatal("contraseña incorrecta", c)
	}
	if c := e.call(t, "PUT", "/api/v1/me/pin", login.Token, map[string]string{"password": "una-tercera-contraseña", "pin": "3434"}, nil); c != 200 {
		t.Fatal("recuperar el PIN propio con la contraseña", c)
	}
	_ = e.pool.QueryRow(ctx, `select count(*) from user_item_data where user_id = $1`, e.userB).Scan(&own)
	if own != 1 {
		t.Fatal("recuperar con la contraseña propia conserva el historial", own)
	}

	// 7. Intentos fallidos: aviso al entrar y, para el administrador, solo número y fecha.
	if c := sw(ana, "0000"); c != 403 {
		t.Fatal("PIN incorrecto", c)
	}
	var entered struct {
		Failures *struct {
			Count int `json:"count"`
		} `json:"pin_failures"`
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana, "pin": "5555"}, &entered); c != 200 || entered.Failures == nil || entered.Failures.Count < 1 {
		t.Fatal("aviso de intentos fallidos al entrar", c, entered)
	}
	var list struct {
		Users []store.UserSummary `json:"users"`
	}
	e.call(t, "GET", "/api/v1/admin/users", e.token, nil, &list)
	for _, u := range list.Users {
		if u.ID == ana && (u.PINFailures < 1 || u.PINFailureLast == nil) {
			t.Fatal("el administrador ve número y fecha de intentos", u)
		}
	}

	// 8. Bloqueo por intentos con el código y caducidad de los sellos.
	for i := 0; i < 6; i++ {
		b.call("POST", "/api/v1/auth/pin/recover", map[string]any{"user_id": ana, "recovery_code": "AAAA-AAAA", "pin": "1111"}, nil)
	}
	if c := b.call("POST", "/api/v1/auth/pin/recover", map[string]any{"user_id": ana, "recovery_code": after.Code, "pin": "1111"}, nil); c != 429 {
		t.Fatal("tras varios códigos erróneos hay que esperar, aunque el siguiente sea bueno", c)
	}
	if _, err := e.pool.Exec(ctx, `insert into profile_seals (user_id, reason, sealed_at, expires_at)
		values ($1, 'owner_reset', now() - interval '31 days', now() - interval '1 day')`, ana); err != nil {
		t.Fatal(err)
	}
	if n, err := e.srv.store.PurgeExpiredSeals(ctx); err != nil || n != 1 {
		t.Fatal("caducidad de los sellos a los 30 días", n, err)
	}
}
