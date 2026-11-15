package api

import (
	"net/http"
	"testing"
)

func TestPlaybackLanguagePreferences(t *testing.T) {
	e, file := newTracksEnv(t)
	var cfg playbackSettings
	e.call(t, "GET", "/api/v1/admin/playback", e.token, nil, &cfg)
	if cfg.AudioLanguage != "es-ES" {
		t.Fatalf("default: %+v", cfg)
	}
	e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]string{"audio_language": "en"}, &cfg)
	var plan decideResponse
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps}, &plan)
	if plan.AudioIndex != 2 {
		t.Fatalf("preferred: %+v", plan)
	}
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps, "audio_index": 1}, &plan)
	if plan.AudioIndex != 1 {
		t.Fatal("se ignoró elección manual")
	}
	res := e.raw(t, "PUT", "/api/v1/admin/playback", map[string]string{"Authorization": "Bearer " + e.tokenB, "Content-Type": "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("permisos: %d", res.StatusCode)
	}
}

func TestPlaybackAudioDynamics(t *testing.T) {
	e, file := newTracksEnv(t)
	var plan decideResponse
	for _, mode := range []string{"balanced", "night", "off"} {
		var cfg playbackSettings
		e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]string{"audio_dynamics": mode}, &cfg)
		if cfg.AudioDynamics != mode {
			t.Fatalf("settings: %+v", cfg)
		}
		e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps}, &plan)
		if plan.AudioDynamics != mode || plan.ConvertVideo {
			t.Fatalf("local processing must not convert video: %+v", plan)
		}
	}
}
