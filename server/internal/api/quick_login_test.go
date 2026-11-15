package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"velora/internal/auth"
	"velora/internal/store"
)

type pairTest struct {
	Token string `json:"device_token"`
	Code  string `json:"user_code"`
}

func createPair(t *testing.T, e *testEnv) pairTest {
	t.Helper()
	var p pairTest
	if c := e.call(t, "POST", "/api/v1/auth/device", "", map[string]string{"device_name": "Mi Box"}, &p); c != 201 || len(p.Token) != 43 || len(p.Code) != 6 {
		t.Fatalf("start %d %+v", c, p)
	}
	return p
}
func approvePair(t *testing.T, e *testEnv, p pairTest, token string) {
	t.Helper()
	if c := e.call(t, "POST", "/api/v1/auth/device/approve", token, map[string]string{"code": p.Code}, nil); c != 204 {
		t.Fatalf("approve %d", c)
	}
}
func TestQuickLoginApprovalSingleUseAndCookies(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	p := createPair(t, e)
	if c := e.call(t, "POST", "/api/v1/auth/device/approve", "", map[string]string{"code": p.Code}, nil); c != 401 {
		t.Fatal(c)
	}
	var pending map[string]any
	if c := e.call(t, "POST", "/api/v1/auth/device/poll", "", map[string]string{"device_token": p.Token}, &pending); c != 200 || pending["status"] != "pending" {
		t.Fatal(c, pending)
	}
	if len(pending) != 1 {
		t.Fatal("pending leaked identity or session")
	}
	approvePair(t, e, p, admin)
	if _, err := e.pool.Exec(context.Background(), `update device_authorizations set last_poll_at=null`); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"device_token": p.Token})
	req, _ := http.NewRequest("POST", e.server.URL+"/api/v1/auth/device/poll", bytes.NewReader(body))
	req.Header.Set("Origin", e.server.URL)
	req.Header.Set("X-Velora-Client", "android")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 || out["user"] == nil || out["token"] != nil {
		t.Fatal("browser session", res.StatusCode, out)
	}
	found := false
	for _, cookie := range res.Cookies() {
		if cookie.Name == sessionCookie && cookie.HttpOnly {
			found = true
		}
	}
	if !found {
		t.Fatal("missing HttpOnly cookie")
	}
	if c := e.call(t, "POST", "/api/v1/auth/device/poll", "", map[string]string{"device_token": p.Token}, nil); c != 410 {
		t.Fatal("approval reusable", c)
	}
}
func TestQuickLoginConcurrentConsumptionAndRevocation(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	p := createPair(t, e)
	approvePair(t, e, p, admin)
	var success atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 6; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result map[string]any
			if e.call(t, "POST", "/api/v1/auth/device/poll", "", map[string]string{"device_token": p.Token}, &result) == 200 && result["token"] != nil {
				success.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal(success.Load())
	}
	p = createPair(t, e)
	approvePair(t, e, p, admin)
	e.call(t, "POST", "/api/v1/auth/logout", admin, nil, nil)
	if c := e.call(t, "POST", "/api/v1/auth/device/poll", "", map[string]string{"device_token": p.Token}, nil); c != 410 {
		t.Fatal("revoked approver accepted", c)
	}
}
func TestQuickLoginExpiryRateAndNoStoredSecrets(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	p := createPair(t, e)
	var equal bool
	if err := e.pool.QueryRow(context.Background(), `select code_hash=$1 and device_hash=$2 from device_authorizations`, auth.HashToken(p.Code), auth.HashToken(p.Token)).Scan(&equal); err != nil || !equal {
		t.Fatal(err, equal)
	}
	if _, err := e.pool.Exec(context.Background(), `update device_authorizations set expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if c := e.call(t, "POST", "/api/v1/auth/device/inspect", admin, map[string]string{"code": p.Code}, nil); c != 404 {
		t.Fatal(c)
	}
	for n := 0; n < 6; n++ {
		code := "999999"
		if p.Code == code {
			code = "888888"
		}
		c := e.call(t, "POST", "/api/v1/auth/device/inspect", admin, map[string]string{"code": code}, nil)
		if n == 5 && c != 429 {
			t.Fatal("guessing unlimited", c)
		}
	}
}
func TestRecoveryCodesRevokeSessionsAndCannotBeReused(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	var codes struct {
		Codes []string `json:"codes"`
	}
	if c := e.call(t, "POST", "/api/v1/me/recovery-codes", admin, map[string]string{"password": "contraseña-larga"}, &codes); c != 200 || len(codes.Codes) != 10 {
		t.Fatal(c, codes)
	}
	var count int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from recovery_codes where octet_length(code_hash)=32`).Scan(&count); err != nil || count != 10 {
		t.Fatal(err, count)
	}
	req := map[string]string{"username": "marta", "code": strings.ToLower(codes.Codes[0]), "password": "nueva-contraseña-segura"}
	if c := e.call(t, "POST", "/api/v1/auth/recover", "", req, nil); c != 204 {
		t.Fatal(c)
	}
	if c := e.call(t, "GET", "/api/v1/me", admin, nil, nil); c != 401 {
		t.Fatal("old session alive", c)
	}
	for _, code := range codes.Codes[:2] {
		req["code"] = code
		if c := e.call(t, "POST", "/api/v1/auth/recover", "", req, nil); c != 400 {
			t.Fatal("old recovery code valid", c)
		}
	}
	var login struct {
		Token string `json:"token"`
	}
	if c := e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "marta", "password": "nueva-contraseña-segura"}, &login); c != 200 || login.Token == "" {
		t.Fatal("new password rejected", c)
	}
}
func TestRecoveryGenerationRequiresReauthAndAdminReset(t *testing.T) {
	e := newEnv(t)
	admin := adminSetup(t, e)
	if c := e.call(t, "POST", "/api/v1/me/recovery-codes", admin, map[string]string{"password": "incorrecta"}, nil); c != 401 {
		t.Fatal(c)
	}
	var user struct {
		ID uuid.UUID `json:"id"`
	}
	if c := e.call(t, "POST", "/api/v1/admin/users", admin, map[string]string{"username": "lucia", "display_name": "Lucía", "password": "contraseña-antigua"}, &user); c != 201 {
		t.Fatal(c)
	}
	if c := e.call(t, "POST", "/api/v1/admin/users/"+user.ID.String()+"/password", admin, map[string]string{"admin_password": "contraseña-larga", "password": "contraseña-nueva"}, nil); c != 200 {
		t.Fatal(c)
	}
	var login struct {
		Token string `json:"token"`
	}
	e.call(t, "POST", "/api/v1/auth/login", "", map[string]string{"username": "lucia", "password": "contraseña-nueva"}, &login)
	if c := e.call(t, "POST", "/api/v1/admin/users/"+user.ID.String()+"/password", login.Token, map[string]string{"admin_password": "contraseña-nueva", "password": "contraseña-otra"}, nil); c != 404 {
		t.Fatal("normal user resets", c)
	}
	// A credential reset between claim and session issuance must invalidate the grant.
	p := createPair(t, e)
	approvePair(t, e, p, admin)
	st := &store.Store{Pool: e.pool}
	u, approval, err := st.ClaimDeviceAuthorization(context.Background(), auth.HashToken(p.Token))
	if err != nil || u == nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("ultima-contraseña")
	if err = st.ResetUserPassword(context.Background(), u.ID, hash); err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateSessionAuthorized(context.Background(), u.ID, auth.HashToken("token"), 3600000000000, store.DeviceInfo{}, approval)
	if err != store.ErrNotFound {
		t.Fatal("revoked grant race", err)
	}
}
