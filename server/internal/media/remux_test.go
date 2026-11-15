package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type failAfterFirst struct{ n int }

func (w *failAfterFirst) Write(p []byte) (int, error) {
	w.n++
	if w.n > 1 {
		return 0, errors.New("el cliente dejó de leer")
	}
	return len(p), nil
}

// Si el cliente deja de leer (p. ej. corte por inactividad), FFmpeg muere enseguida y no queda bloqueado en la tubería.
func TestRemuxStopsWhenClientStopsReading(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no disponible")
	}
	path := filepath.Join(t.TempDir(), "v.mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=60",
		"-f", "lavfi", "-i", "sine=duration=60", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "ac3", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	start := time.Now()
	err = Remux(context.Background(), f, RemuxArgs(Plan{AudioIndex: 1, ConvertAudio: true}, "h264", 0), &failAfterFirst{})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("Remux tardó %s en detenerse", d)
	}
}

// La caché de análisis está acotada: al llenarse descarta lo más antiguo y nunca crece sin fin.
func TestResultCacheIsBounded(t *testing.T) {
	c := NewResultCache[float64](3)
	for i, k := range []string{"a", "b", "c", "d"} {
		c.Put(k, float64(i))
	}
	if c.Len() != 3 {
		t.Fatalf("debería guardar 3: %d", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("lo más antiguo debería haberse descartado")
	}
	c.Put("b", 9) // actualizar no ocupa otro hueco
	if v, ok := c.Get("b"); !ok || v != 9 || c.Len() != 3 {
		t.Fatalf("actualizar: %v %v %d", v, ok, c.Len())
	}
}
