package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/store"
)

// Fase 4 de replica/PLAN_HOGARES_Y_PIN.md: bloqueo del perfil aplicado por el servidor. El paso del tiempo se simula
// moviendo el plazo de desbloqueo de la sesión.
func TestProfileLockAppliedByServer(t *testing.T) {
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
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana}, nil); c != 200 {
		t.Fatal("entrar como Ana", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin", map[string]string{"pin": "1111"}, nil); c != 200 {
		t.Fatal("PIN de Ana", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin-lock", map[string]int{"minutes": 30}, nil); c != 422 {
		t.Fatal("30 minutos no es una opción", c)
	}
	if c := b.call("PUT", "/api/v1/me/pin-lock", map[string]int{"minutes": 15}, nil); c != 204 {
		t.Fatal("15 minutos", c)
	}
	session := func() uuid.UUID {
		t.Helper()
		id, _ := b.session(t)
		return id
	}
	expire := func(id uuid.UUID, in time.Duration) {
		t.Helper()
		if _, err := e.pool.Exec(ctx, `update sessions set profile_unlocked_until = now() + make_interval(secs => $2) where id = $1`, id, in.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := func(id uuid.UUID) time.Duration {
		t.Helper()
		var until time.Time
		if err := e.pool.QueryRow(ctx, `select profile_unlocked_until from sessions where id = $1`, id).Scan(&until); err != nil {
			t.Fatal(err)
		}
		return time.Until(until)
	}
	item := "/api/v1/items/" + e.pelisItem.String()
	anaSession := session()

	// Pasado el plazo, el servidor no da nada privado: solo /me (con locked), desbloquear, cambiar de perfil y salir.
	expire(anaSession, -time.Minute)
	if c := b.call("GET", item, nil, nil); c != 423 {
		t.Fatal("ficha con el perfil bloqueado", c)
	}
	if c := b.call("GET", "/api/v1/home", nil, nil); c != 423 {
		t.Fatal("inicio con el perfil bloqueado", c)
	}
	if c := b.call("POST", "/api/v1/playback/decide", map[string]any{"file_id": e.pelisFile, "caps": chromiumCaps, "player_id": "tv"}, nil); c != 423 {
		t.Fatal("reproducir con el perfil bloqueado", c)
	}
	var me struct {
		User store.User `json:"user"`
	}
	if c := b.call("GET", "/api/v1/me", nil, &me); c != 200 || !me.User.Locked || me.User.ID != ana {
		t.Fatal("/me dice que está bloqueado", c, me.User)
	}
	if c := b.call("GET", "/api/v1/catalog/revision", nil, nil); c != 200 {
		t.Fatal("la comprobación automática del catálogo sigue respondiendo", c)
	}
	if c := b.call("POST", "/api/v1/me/activity", map[string]bool{"interaction": true}, nil); c != 423 {
		t.Fatal("la actividad no desbloquea un perfil bloqueado", c)
	}
	if c := b.call("POST", "/api/v1/me/unlock", map[string]string{"pin": "0000"}, nil); c != 403 {
		t.Fatal("PIN incorrecto", c)
	}
	if c := b.call("POST", "/api/v1/me/unlock", map[string]string{"pin": "1111"}, nil); c != 200 {
		t.Fatal("desbloquear", c)
	}
	if c := b.call("GET", item, nil, nil); c != 200 {
		t.Fatal("desbloqueado", c)
	}
	if d := deadline(anaSession); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatal("el desbloqueo dura lo que eligió Ana (15 min)", d)
	}

	// Las consultas automáticas no alargan el plazo; las interacciones, sí; «a la vista» solo con «siempre».
	expire(anaSession, 30*time.Second)
	for i := 0; i < 3; i++ {
		b.call("GET", "/api/v1/catalog/revision", nil, nil)
		b.call("GET", item, nil, nil)
	}
	if d := deadline(anaSession); d > 31*time.Second {
		t.Fatal("las consultas automáticas no deben alargar el desbloqueo", d)
	}
	if c := b.call("POST", "/api/v1/me/activity", map[string]bool{"interaction": false}, nil); c != 204 {
		t.Fatal(c)
	}
	if d := deadline(anaSession); d > 31*time.Second {
		t.Fatal("con 15 minutos, tener la app a la vista no cuenta", d)
	}
	if c := b.call("POST", "/api/v1/me/activity", map[string]bool{"interaction": true}, nil); c != 204 {
		t.Fatal(c)
	}
	if d := deadline(anaSession); d < 14*time.Minute {
		t.Fatal("una interacción alarga el desbloqueo", d)
	}

	// Nunca se bloquea con el reproductor abierto, aunque esté en pausa y nadie toque nada.
	if c := b.call("POST", "/api/v1/playback/decide", map[string]any{"file_id": e.pelisFile, "caps": chromiumCaps, "player_id": "tv"}, nil); c != 200 {
		t.Fatal("reproducir", c)
	}
	for i := 0; i < 3; i++ {
		expire(anaSession, 5*time.Second)
		if c := b.call("POST", "/api/v1/playback/heartbeat", map[string]any{"id": "tv", "file_id": e.pelisFile, "state": "paused", "position_ms": 1000}, nil); c != 204 {
			t.Fatal("latido en pausa", c)
		}
		if d := deadline(anaSession); d < 14*time.Minute {
			t.Fatal("un latido del reproductor, aun en pausa, mantiene el perfil desbloqueado", d)
		}
	}
	// Si los latidos se paran (la TV suspende la app) y pasa el plazo, al volver se pide el PIN.
	expire(anaSession, -time.Second)
	if c := b.call("POST", "/api/v1/playback/heartbeat", map[string]any{"id": "tv", "file_id": e.pelisFile, "state": "playing", "position_ms": 2000}, nil); c != 423 {
		t.Fatal("tras pasar el plazo sin latidos se pide el PIN", c)
	}
	if c := b.call("POST", "/api/v1/me/unlock", map[string]string{"pin": "1111"}, nil); c != 200 {
		t.Fatal(c)
	}

	// «Siempre»: 2 minutos, que también alarga tener la app a la vista.
	if c := b.call("PUT", "/api/v1/me/pin-lock", map[string]int{"minutes": 0}, nil); c != 204 {
		t.Fatal(c)
	}
	if d := deadline(anaSession); d < 100*time.Second || d > 125*time.Second {
		t.Fatal("con «siempre» el plazo es de 2 minutos", d)
	}
	expire(anaSession, 10*time.Second)
	if c := b.call("POST", "/api/v1/me/activity", map[string]bool{"interaction": false}, nil); c != 204 {
		t.Fatal(c)
	}
	if d := deadline(anaSession); d < 100*time.Second {
		t.Fatal("con «siempre», tener la app a la vista mantiene el desbloqueo", d)
	}

	// Cambiar de perfil bloquea el anterior en este dispositivo; volver exige su PIN.
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": made.ID}, nil); c != 200 {
		t.Fatal("cambiar al propietario", c)
	}
	if d := deadline(anaSession); d > 0 {
		t.Fatal("el perfil de Ana queda bloqueado al cambiar", d)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana}, nil); c != 403 {
		t.Fatal("volver a Ana exige su PIN", c)
	}
	// Un perfil sin PIN nunca se bloquea.
	expire(session(), -time.Hour)
	if c := b.call("GET", item, nil, nil); c != 200 {
		t.Fatal("un perfil sin PIN no se bloquea", c)
	}

	// Cada dispositivo se desbloquea por separado. (Vincular otro dispositivo es cosa del propietario.)
	other := createPair(t, e.testEnv)
	if c := b.call("POST", "/api/v1/auth/device/approve", map[string]any{"code": other.Code, "user_id": ana, "pin": "1111"}, nil); c != 204 {
		t.Fatal("el propietario vincula otro dispositivo a Ana, con su PIN", c)
	}
	b2 := newHouseholdBrowser(t, e.testEnv)
	if c := b2.call("POST", "/api/v1/auth/device/poll", map[string]string{"device_token": other.Token}, nil); c != 200 {
		t.Fatal(c)
	}
	if c := b.call("POST", "/api/v1/auth/switch", map[string]any{"user_id": ana, "pin": "1111"}, nil); c != 200 {
		t.Fatal(c)
	}
	expire(session(), -time.Second)
	if c := b.call("GET", item, nil, nil); c != 423 {
		t.Fatal("bloqueado en el primer dispositivo", c)
	}
	if c := b2.call("GET", item, nil, nil); c != 200 {
		t.Fatal("el otro dispositivo sigue desbloqueado", c)
	}

	// Medida de recuperación: el ajuste general desactiva el bloqueo sin tocar los PIN.
	if _, err := e.pool.Exec(ctx, `insert into settings (key, value) values ('profiles.lock_enabled', 'false')`); err != nil {
		t.Fatal(err)
	}
	if c := b.call("GET", item, nil, nil); c != 200 {
		t.Fatal("con el bloqueo desactivado no se pide el PIN", c)
	}
	if _, err := e.pool.Exec(ctx, `delete from settings where key = 'profiles.lock_enabled'`); err != nil {
		t.Fatal(err)
	}
	if c := b.call("GET", item, nil, nil); c != 423 {
		t.Fatal("reactivado, vuelve a pedirse", c)
	}
}
