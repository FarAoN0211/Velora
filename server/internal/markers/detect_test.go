package markers

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// episodio sintético: preIntro s de ruido propio, la intro común (40 s), cuerpo propio y los créditos comunes (30 s).
// Cada uno con otro ruido, otro volumen y otra calidad de AAC: como capítulos de una serie codificados por separado.
func makeEpisode(t *testing.T, dir string, n int, preIntro, body float64) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("ep%d.mkv", n))
	intro := "aevalsrc=exprs='0.3*sin(2*PI*t*(300+60*floor(mod(t*3\\,9))))+0.2*sin(2*PI*t*(520+40*floor(mod(t*2\\,5))))':s=44100:d=40"
	credits := "aevalsrc=exprs='0.3*sin(2*PI*t*(700-50*floor(mod(t*4\\,7))))+0.2*sin(2*PI*t*(250+30*floor(mod(t\\,6))))':s=44100:d=30"
	noise := func(seed int, d float64) string {
		return fmt.Sprintf("anoisesrc=color=pink:seed=%d:amplitude=0.4:d=%.1f:r=44100", seed, d)
	}
	graph := fmt.Sprintf("%s[a];%s[b];%s[c];%s[d];[a][b][c][d]concat=n=4:v=0:a=1,volume=%.2f[out]",
		noise(100+n, preIntro), intro, noise(200+n, body), credits, 0.6+0.2*float64(n%3))
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-filter_complex", graph, "-map", "[out]",
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", 64+32*n), path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	return path
}

func TestDetectSeasonFindsIntroAndCredits(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no disponible")
	}
	dir := t.TempDir()
	type spec struct{ pre, body float64 }
	specs := []spec{{30, 400}, {12, 430}, {45, 380}} // ~8 min: la intro cae dentro del 40 % inicial en que se busca
	var eps []Episode
	for i, s := range specs {
		p := makeEpisode(t, dir, i, s.pre, s.body)
		eps = append(eps, Episode{FileID: uuid.New(), Duration: s.pre + 40 + s.body + 30, Open: func() (*os.File, error) { return os.Open(p) }})
	}
	got, err := DetectSeason(context.Background(), eps)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range specs {
		r := got[eps[i].FileID]
		if r.Intro == nil || r.Credits == nil {
			t.Fatalf("episodio %d: intro %v, créditos %v", i, r.Intro, r.Credits)
		}
		near := func(a, b float64) bool { return math.Abs(a-b) <= 2.5 }
		if !near(r.Intro.Start, s.pre) || !near(r.Intro.End, s.pre+40) {
			t.Errorf("episodio %d: intro %.1f–%.1f; se esperaba %.1f–%.1f", i, r.Intro.Start, r.Intro.End, s.pre, s.pre+40)
		}
		dur := eps[i].Duration
		if !near(r.Credits.Start, dur-30) || r.Credits.End != dur {
			t.Errorf("episodio %d: créditos %.1f–%.1f; se esperaba %.1f–%.1f", i, r.Credits.Start, r.Credits.End, dur-30, dur)
		}
	}
}

func TestDetectSeasonNothingInCommon(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no disponible")
	}
	dir := t.TempDir()
	var eps []Episode
	for i := 0; i < 2; i++ {
		p := filepath.Join(dir, fmt.Sprintf("solo%d.mkv", i))
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
			fmt.Sprintf("anoisesrc=color=pink:seed=%d:amplitude=0.4:d=120:r=44100", 7+i), "-c:a", "aac", p)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		eps = append(eps, Episode{FileID: uuid.New(), Duration: 120, Open: func() (*os.File, error) { return os.Open(p) }})
	}
	got, err := DetectSeason(context.Background(), eps)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(eps) {
		t.Fatalf("dos audios distintos no deben dar intro ni créditos: %+v", got)
	}
	for _, r := range got {
		if r.Intro != nil || r.Credits != nil {
			t.Fatal("unrelated audio matched", r)
		}
	}
}

func TestBestMatchShiftedCopies(t *testing.T) {
	// La misma secuencia en dos posiciones distintas, entre datos al azar.
	common := make([]uint32, 300)
	for i := range common {
		common[i] = uint32(i*2654435761) ^ 0x5bd1e995
	}
	rnd := func(seed, n int) []uint32 {
		out := make([]uint32, n)
		x := uint32(seed)
		for i := range out {
			x ^= x << 13
			x ^= x >> 17
			x ^= x << 5
			out[i] = x
		}
		return out
	}
	a := append(append(rnd(1, 100), common...), rnd(2, 50)...)
	b := append(append(rnd(3, 220), common...), rnd(4, 80)...)
	m, ok := BestMatch(a, b, 150) // con datos al azar, ventanas de 64 y recorte de bordes: ±3 pasos
	if !ok || m.AStart < 95 || m.AStart > 105 || m.BStart < 215 || m.BStart > 225 || m.Length < 290 || m.Length > 310 {
		t.Fatalf("coincidencia: %+v %v", m, ok)
	}
}

func TestUnreadableEpisodeIsNotReportedAsAnalyzed(t *testing.T) {
	id := uuid.New()
	got, err := DetectSeason(context.Background(), []Episode{{FileID: id, Duration: 120, Open: func() (*os.File, error) { return nil, os.ErrNotExist }}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[id]; ok {
		t.Fatal("unreadable episode must remain pending")
	}
}

// Una temporada interrumpida entre dos episodios no vuelve a leer lo ya leído: la siguiente llamada usa las huellas
// guardadas y solo abre los archivos que faltaban.
func TestDetectSeasonResumesFromStoredFingerprints(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no disponible")
	}
	dir := t.TempDir()
	specs := []struct{ pre, body float64 }{{30, 400}, {12, 430}, {45, 380}}
	stored := map[uuid.UUID][2][]uint32{}
	opened := map[uuid.UUID]int{}
	var eps []Episode
	for i, s := range specs {
		p := makeEpisode(t, dir, i, s.pre, s.body)
		id := uuid.New()
		eps = append(eps, Episode{FileID: id, Duration: s.pre + 40 + s.body + 30,
			Open: func() (*os.File, error) { opened[id]++; return os.Open(p) },
			Load: func() ([]uint32, []uint32, bool) {
				v, ok := stored[id]
				return v[0], v[1], ok
			},
			Store: func(in, cr []uint32) { stored[id] = [2][]uint32{in, cr} },
		})
	}
	reads := 0
	_, err := DetectSeasonUntil(context.Background(), eps, func() bool { reads++; return reads > 2 })
	if err != ErrStopped || len(stored) != 2 {
		t.Fatalf("debería parar tras dos episodios con sus huellas guardadas: %v, %d", err, len(stored))
	}
	got, err := DetectSeasonUntil(context.Background(), eps, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range eps {
		if opened[e.FileID] != 1 {
			t.Fatalf("episodio %d abierto %d veces; cada archivo debe leerse una sola vez", i, opened[e.FileID])
		}
		if r := got[e.FileID]; r.Intro == nil || r.Credits == nil {
			t.Fatalf("episodio %d sin intro o créditos tras reanudar: %+v", i, r)
		}
	}
}
