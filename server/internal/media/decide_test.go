package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func probeOf(container, video, pix, transfer string, height int, audios ...string) Probe {
	var p Probe
	p.Format.FormatName = container
	p.Streams = append(p.Streams, Stream{Index: 0, CodecType: "video", CodecName: video, PixFmt: pix, ColorTransfer: transfer, Height: height})
	for i, a := range audios {
		s := Stream{Index: i + 1, CodecType: "audio", CodecName: a, Disposition: map[string]int{}}
		if i == 0 {
			s.Disposition["default"] = 1
		}
		p.Streams = append(p.Streams, s)
	}
	return p
}

// Capacidades típicas de Chromium (sin AC3/EAC3, sin MKV declarado).
var chromium = Caps{Containers: []string{"mp4", "webm"}, Video: []string{"h264", "vp9", "av1"}, Audio: []string{"aac", "mp3", "opus", "flac"}}

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		p       Probe
		ext     string
		caps    Caps
		method  Method
		convert bool
	}{
		{"MP4 H.264 AAC → directo", probeOf("mov,mp4,m4a,3gp,3g2,mj2", "h264", "yuv420p", "bt709", 1080, "aac"), ".mp4", chromium, DirectPlay, false},
		{"MKV H.264 EAC3 → optimizado con audio convertido", probeOf("matroska,webm", "h264", "yuv420p", "bt709", 1080, "eac3"), ".mkv", chromium, DirectStream, true},
		{"MKV H.264 Opus → optimizado copiando audio", probeOf("matroska,webm", "h264", "yuv420p", "bt709", 1080, "opus"), ".mkv", chromium, DirectStream, false},
		{"MKV HEVC 10 bits en Chromium → no compatible", probeOf("matroska,webm", "hevc", "yuv420p10le", "smpte2084", 2160, "truehd"), ".mkv", chromium, Unsupported, false},
		{"MKV HEVC con TV que lo admite y EAC3 → directo si acepta MKV", probeOf("matroska,webm", "hevc", "yuv420p10le", "smpte2084", 2160, "eac3"), ".mkv",
			Caps{Containers: []string{"mkv", "mp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "ac3", "eac3"}}, DirectPlay, false},
		{"WebM VP9 Opus → directo", probeOf("matroska,webm", "vp9", "yuv420p", "bt709", 1080, "opus"), ".webm", chromium, DirectPlay, false},
		{"TrueHD → audio convertido", probeOf("matroska,webm", "av1", "yuv420p10le", "bt709", 1080, "truehd"), ".mkv", chromium, DirectStream, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := Decide(c.p, c.ext, c.caps, -1)
			if plan.Method != c.method || plan.ConvertAudio != c.convert {
				t.Fatalf("plan %+v; esperaba %s convert=%v", plan, c.method, c.convert)
			}
			if plan.Method != DirectPlay && len(plan.Reasons) == 0 {
				t.Fatal("toda degradación debe explicar su motivo")
			}
			if plan.Summary == "" {
				t.Fatal("falta el resumen para «En este dispositivo»")
			}
		})
	}
}

func TestDecideHDRReasonMentionsHDR(t *testing.T) {
	plan := Decide(probeOf("matroska,webm", "hevc", "yuv420p10le", "smpte2084", 2160, "eac3"), ".mkv", chromium, -1)
	if !strings.Contains(plan.Reasons[0], "HDR10") || !strings.Contains(plan.Reasons[0], "HEVC 10 bits") {
		t.Fatalf("el motivo debería nombrar HEVC 10 bits y HDR10: %q", plan.Reasons[0])
	}
}

func TestPreferredAudio(t *testing.T) {
	p := probeOf("matroska,webm", "h264", "yuv420p", "bt709", 1080, "eac3", "aac")
	if plan := Decide(p, ".mkv", chromium, 2); plan.AudioIndex != 2 || plan.ConvertAudio {
		t.Fatalf("debería elegir la pista 2 (AAC) sin convertir: %+v", plan)
	}
}

func TestRemuxArgsNeverTakePaths(t *testing.T) {
	args := RemuxArgs(Plan{AudioIndex: 1, ConvertAudio: true}, "hevc", 90.5)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-ss 90.500", "-i /dev/fd/3", "-c:v copy", "-tag:v hvc1", "-c:a aac -ac 2", "frag_keyframe", "pipe:1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("falta %q en %s", want, joined)
		}
	}
	if strings.Count(joined, "-i ") != 1 {
		t.Fatal("solo debe haber una entrada, el descriptor heredado")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2)
	if !l.TryAcquire() || !l.TryAcquire() || l.TryAcquire() {
		t.Fatal("el limitador debería admitir exactamente 2")
	}
	l.Release()
	if !l.TryAcquire() {
		t.Fatal("tras liberar debería admitir otro")
	}
}

// B2: elegir una pista de audio que no es la predeterminada en un archivo que iría en Directo obliga a reempaquetar
// (el navegador solo reproduciría la predeterminada), copiando el audio si el navegador lo admite.
func TestDecideNonDefaultAudioTrack(t *testing.T) {
	p := probeOf("mov,mp4,m4a,3gp,3g2,mj2", "h264", "yuv420p", "bt709", 1080, "aac", "aac")
	if got := Decide(p, ".mp4", chromium, -1); got.Method != DirectPlay || got.AudioIndex != 1 {
		t.Fatalf("pista por defecto: %+v", got)
	}
	if got := Decide(p, ".mp4", chromium, 1); got.Method != DirectPlay {
		t.Fatalf("elegir la predeterminada sigue siendo directo: %+v", got)
	}
	got := Decide(p, ".mp4", chromium, 2)
	if got.Method != DirectStream || got.ConvertAudio || got.AudioIndex != 2 {
		t.Fatalf("otra pista en Chromium → optimizado copiando el audio: %+v", got)
	}
	safari := chromium
	safari.SelectAudio = true
	if got := Decide(p, ".mp4", safari, 2); got.Method != DirectPlay {
		t.Fatalf("un cliente que sabe elegir pista sigue en directo: %+v", got)
	}
}

func TestExternalSubNames(t *testing.T) {
	dir := t.TempDir()
	movie := filepath.Join(dir, "Peli (2020)")
	_ = os.MkdirAll(movie, 0o755)
	for _, n := range []string{"Peli (2020).mkv", "Peli (2020).srt", "Peli (2020).spa.forced.srt", "Peli (2020).en.ass", "Peli (2020).nfo", "Otra.srt"} {
		_ = os.WriteFile(filepath.Join(movie, n), []byte("x"), 0o644)
	}
	_ = os.Symlink("/etc/passwd", filepath.Join(movie, "Peli (2020).de.srt"))
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	subs := ExternalSubs(root, "Peli (2020)/Peli (2020).mkv")
	if len(subs) != 3 {
		t.Fatalf("se esperaban 3 externos (sin el enlace, el .nfo ni el de otra película): %+v", subs)
	}
	got := map[string]ExternalSub{}
	for _, s := range subs {
		got[s.Name] = s
	}
	if s := got["Peli (2020).spa.forced.srt"]; s.Language != "spa" || !s.Forced || s.Format != "srt" {
		t.Fatalf("spa.forced: %+v", s)
	}
	if s := got["Peli (2020).en.ass"]; s.Language != "en" || s.Format != "ass" {
		t.Fatalf("en.ass: %+v", s)
	}
}

// Un códec anunciado no basta: si el decodificador del aparato no llega a la resolución del archivo, se dice claramente
// en lugar de reempaquetar (el vídeo se copia tal cual). Lo que sí cabe sigue en directo.
func TestDecideRespectsDecoderMaxHeight(t *testing.T) {
	box := Caps{Containers: []string{"mp4", "mkv"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "eac3"},
		MaxHeights: map[string]int{"hevc": 1088, "h264": 1088}}
	uhd := Decide(probeOf("matroska,webm", "hevc", "yuv420p10le", "bt709", 2160, "eac3"), ".mkv", box, -1)
	if uhd.Method != Unsupported || len(uhd.Reasons) != 1 || !strings.Contains(uhd.Reasons[0], "1088") || !strings.Contains(uhd.Reasons[0], "2160") {
		t.Fatalf("4K en un decodificador de 1080: %+v", uhd)
	}
	hd := Decide(probeOf("matroska,webm", "hevc", "yuv420p10le", "bt709", 1080, "eac3"), ".mkv", box, -1)
	if hd.Method != DirectPlay {
		t.Fatalf("1080 cabe y debe ir en directo: %+v", hd)
	}
	// Sin límite declarado para el códec (clientes antiguos o la web), nada cambia.
	box.MaxHeights = map[string]int{"h264": 1088}
	if p := Decide(probeOf("matroska,webm", "hevc", "yuv420p10le", "bt709", 2160, "eac3"), ".mkv", box, -1); p.Method != DirectPlay {
		t.Fatalf("sin límite para HEVC debe seguir en directo: %+v", p)
	}
}
