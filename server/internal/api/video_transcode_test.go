package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"velora/internal/media"
)

func TestTranscodeAuthorizationAndRealOutput(t *testing.T) {
	e, file := newTracksEnv(t)
	caps := media.Caps{VideoTranscode: true, Containers: []string{"mp4"}, Video: []string{"h264"}, Audio: []string{"aac"}, SourceVideoUnsupported: true}
	var plan decideResponse
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": caps, "audio_index": 2}, &plan)
	if plan.Method != media.Transcode || !plan.ConvertVideo || plan.ConvertAudio || !strings.Contains(plan.URL, "ticket=") {
		t.Fatalf("%+v", plan)
	}
	// Missing/altered plan and another authenticated session cannot request expensive work.
	for _, url := range []string{"/api/v1/stream/files/" + file.String() + "/transcode?ticket=invalid", plan.URL + "x"} {
		// Appending to audio is not an alteration of a signed ticket; use a changed signature instead.
		if strings.HasSuffix(url, "x") {
			url = strings.Replace(plan.URL, "ticket=", "ticket=x", 1)
		}
		res := e.raw(t, "GET", url, map[string]string{"Authorization": "Bearer " + e.tokenB})
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("altered ticket: %d", res.StatusCode)
		}
	}
	res := e.raw(t, "GET", plan.URL, map[string]string{"Authorization": "Bearer " + e.token})
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("session binding: %d", res.StatusCode)
	}
	res = e.raw(t, "GET", plan.URL, map[string]string{"Authorization": "Bearer " + e.tokenB})
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	path := filepath.Join(t.TempDir(), "converted.mp4")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(f, res.Body)
	res.Body.Close()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	probe, err := media.RunProbe(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if media.VideoStream(probe).CodecName != "h264" {
		t.Fatal(probe)
	}
	// Setting software/off cannot affect a compatible direct play.
	e.call(t, "PUT", "/api/v1/admin/playback", e.token, map[string]any{"audio_language": "es-ES", "transcoding_mode": "off", "transcoding_max_height": 1080, "transcoding_bitrate_kbps": 6000}, nil)
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": caps}, &plan)
	if plan.Method != media.Unsupported {
		t.Fatal(plan)
	}
}
