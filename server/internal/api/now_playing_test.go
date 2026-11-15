package api

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
	"velora/internal/media"
)

func TestPlaybackPresenceExpiresAndStops(t *testing.T) {
	var a playbackActivity
	p := playbackPresence{Session: uuid.New(), ID: "player", State: "paused", Updated: time.Now()}
	a.update(p)
	if len(a.snapshot(time.Now())) != 1 {
		t.Fatal("paused player missing")
	}
	if len(a.snapshot(time.Now().Add(playbackPresenceTTL+time.Second))) != 0 {
		t.Fatal("disconnected player retained")
	}
	a.update(p)
	p.State = "stopped"
	a.update(p)
	if len(a.snapshot(time.Now())) != 0 {
		t.Fatal("stopped player retained")
	}
}
func TestNowPlayingAuthorizationAndRevocation(t *testing.T) {
	e, file := newTracksEnv(t)
	var decision decideResponse
	caps := media.Caps{Containers: []string{"mkv"}, Video: []string{"h264"}, Audio: []string{"aac"}}
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": caps}, &decision); c != 200 {
		t.Fatal(c)
	}
	beat := map[string]any{"id": "test-player", "file_id": file, "state": "paused", "position_ms": 1000}
	if c := e.call(t, "POST", "/api/v1/playback/heartbeat", e.tokenB, beat, nil); c != 204 {
		t.Fatal(c)
	}
	if c := e.call(t, "GET", "/api/v1/admin/now-playing", e.tokenB, nil, nil); c != 404 {
		t.Fatal("non-admin sees activity", c)
	}
	var out struct {
		Items []struct {
			Username string       `json:"username"`
			State    string       `json:"state"`
			Method   media.Method `json:"method"`
			Position int64        `json:"position_ms"`
		}
	}
	if c := e.call(t, "GET", "/api/v1/admin/now-playing", e.token, nil, &out); c != 200 || len(out.Items) != 1 {
		t.Fatalf("%d %+v", c, out)
	}
	if out.Items[0].State != "paused" || out.Items[0].Position != 1000 || out.Items[0].Method != decision.Method || out.Items[0].Username == "" {
		t.Fatal(out)
	}
	beat["state"] = "stopped"
	e.call(t, "POST", "/api/v1/playback/heartbeat", e.tokenB, beat, nil)
	e.call(t, "GET", "/api/v1/admin/now-playing", e.token, nil, &out)
	if len(out.Items) != 0 {
		t.Fatal("stopped visible")
	}
	beat["state"] = "buffering"
	e.call(t, "POST", "/api/v1/playback/heartbeat", e.tokenB, beat, nil)
	if _, err := e.pool.Exec(context.Background(), `update sessions set revoked_at=now() where user_id=$1`, e.userB); err != nil {
		t.Fatal(err)
	}
	e.call(t, "GET", "/api/v1/admin/now-playing", e.token, nil, &out)
	if len(out.Items) != 0 {
		t.Fatal("revoked visible")
	}
}
