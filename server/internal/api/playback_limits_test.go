package api

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"testing"
	"velora/internal/auth"
	"velora/internal/store"
)

func TestPlaybackLimitsAdmissionRangesStopAndPolicy(t *testing.T) {
	e, file := newTracksEnv(t)
	decide := func(id string, dry bool) int {
		return e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps, "player_id": id, "dry_run": dry}, nil)
	}
	settings := func(user, ip int) {
		t.Helper()
		if c := e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]int{"max_streams_per_user": user, "max_streams_per_ip": ip}, nil); c != 200 {
			t.Fatal(c)
		}
	}
	settings(1, 0)
	var preserved playbackSettings
	if c := e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]string{"audio_language": "es-ES"}, &preserved); c != 200 || preserved.MaxStreamsUser != 1 {
		t.Fatal("legacy settings cleared limits", c, preserved)
	}
	if c := decide("first", false); c != 200 {
		t.Fatal("first", c)
	}
	if c := decide("second", true); c != 200 {
		t.Fatal("dry run reserved a slot", c)
	}
	if c := decide("first", false); c != 200 {
		t.Fatal("audio/seek counted twice", c)
	}
	if c := decide("second", false); c != 429 {
		t.Fatal("user cap bypass", c)
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file.String()+"?player_id=second", e.tokenB, nil, nil); c != 429 {
		t.Fatal("raw stream bypass", c)
	}
	beat := map[string]any{"id": "first", "file_id": file, "state": "paused", "position_ms": 0}
	if c := e.call(t, "POST", "/api/v1/playback/heartbeat", e.tokenB, beat, nil); c != 204 {
		t.Fatal(c)
	}
	beat["state"] = "stopped"
	e.call(t, "POST", "/api/v1/playback/heartbeat", e.tokenB, beat, nil)
	if c := decide("second", false); c != 200 {
		t.Fatal("stop didn't free slot", c)
	}
	settings(0, 1)
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": file, "caps": chromiumCaps, "player_id": "other-user"}, nil); c != 429 {
		t.Fatal("same IP other user bypass", c)
	}
	settings(2, 2)
	if c := decide("third", false); c != 200 {
		t.Fatal(c)
	}
	if c := decide("fourth", false); c != 429 {
		t.Fatal("combined cap bypass", c)
	}
	settings(0, 0)
	for _, id := range []string{"fourth", "fifth", "sixth"} {
		if c := decide(id, false); c != 200 {
			t.Fatal("unlimited", c)
		}
	}
	if c := e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]int{"max_streams_per_ip": -1}, nil); c != 400 {
		t.Fatal("negative accepted", c)
	}
	// Límite propio del usuario (para todo su hogar): sustituye al general, en los dos sentidos.
	user := "/api/v1/admin/users/" + e.userB.String()
	if c := e.call(t, "PATCH", user, e.token, map[string]any{"max_streams": 1}, nil); c != 204 {
		t.Fatal("set own limit", c)
	}
	if c := decide("seventh", false); c != 429 {
		t.Fatal("own limit ignored while general is unlimited", c)
	}
	if c := decide("fourth", false); c != 200 {
		t.Fatal("an admitted player must keep playing", c)
	}
	var list struct {
		Users []store.UserSummary `json:"users"`
	}
	e.call(t, "GET", "/api/v1/admin/users", e.token, nil, &list)
	for _, u := range list.Users {
		if u.ID == e.userB && (u.MaxStreams == nil || *u.MaxStreams != 1 || u.MaxHouseholdMembers != 9) {
			t.Fatal("limits not listed", u)
		}
	}
	if c := e.call(t, "PATCH", user, e.token, map[string]any{"max_streams": 0}, nil); c != 204 {
		t.Fatal(c)
	}
	settings(1, 0)
	if c := decide("eighth", false); c != 200 {
		t.Fatal("own 0 (unlimited) must override a general cap", c)
	}
	if c := e.call(t, "PATCH", user, e.token, map[string]any{"max_streams": nil}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := decide("ninth", false); c != 429 {
		t.Fatal("null must return to the general limit", c)
	}
	if c := e.call(t, "PATCH", user, e.token, map[string]any{"max_streams": -2}, nil); c != 422 {
		t.Fatal("invalid own limit accepted", c)
	}
	if c := e.call(t, "PATCH", "/api/v1/admin/users/"+e.userB.String(), e.tokenB, map[string]any{"max_streams": 50}, nil); c != 404 {
		t.Fatal("non-admin changed limits", c)
	}
}
func TestPlaybackLimitsConcurrentExpiryAndRevocation(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	session, _, err := e.srv.store.SessionUser(ctx, auth.HashToken(e.tokenB), e.srv.cfg.SessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := e.srv.store.ReservePlayback(ctx, session, e.userB, e.pelisFile, uuid.NewString(), "192.0.2.1", 1, 1)
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, store.ErrPlaybackUserLimit) {
				t.Errorf("reserve: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("non-atomic admission", winners.Load())
	}
	if _, err = e.pool.Exec(ctx, `update playback_leases set expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err = e.srv.store.ReservePlayback(ctx, session, e.userB, e.pelisFile, "after-expiry", "192.0.2.1", 1, 1); err != nil {
		t.Fatal("expiry", err)
	}
	// Changing IP must check the destination quota; IPv4-mapped addresses are
	// normalized at the API boundary and proxy headers use the trusted-proxy list.
	var sid uuid.UUID
	sid, _, err = e.srv.store.SessionUser(ctx, auth.HashToken(e.token), e.srv.cfg.SessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.srv.store.ReservePlayback(ctx, sid, uuid.Nil, e.pelisFile, "other-ip", "192.0.2.2", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err = e.srv.store.ReservePlayback(ctx, sid, uuid.Nil, e.pelisFile, "other-ip", "192.0.2.1", 0, 1); !errors.Is(err, store.ErrPlaybackIPLimit) {
		t.Fatal("IP movement bypass", err)
	}
	if _, err = e.pool.Exec(ctx, `update sessions set revoked_at=now() where id=$1`, session); err != nil {
		t.Fatal(err)
	}
	if err = e.srv.store.ReservePlayback(ctx, sid, uuid.Nil, e.pelisFile, "other-ip", "192.0.2.1", 0, 1); err != nil {
		t.Fatal("revoked still counted", err)
	}
}
