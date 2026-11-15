package api

// E1.2 · B2 (audio) y B3 (subtítulos): pruebas con un MKV real de dos audios y un subtítulo interno, y subtítulos
// externos junto a la película.

import (
	"context"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"velora/internal/markers"
	"velora/internal/media"
	"velora/internal/store"
)

const srtES = "1\n00:00:01,000 --> 00:00:03,000\nHola, esto es una prueba.\n\n2\n00:00:04,000 --> 00:00:05,500\nSegunda línea.\n"
const assEN = "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Arial,20,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,10,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:02.00,0:00:04.00,Default,,0,0,0,,{\\i1}Forced english{\\i0}\n"

// newTracksEnv prepara «Pistas (2021)»: vídeo H.264, audio 1 AC3 español (predeterminado), audio 2 AAC inglés,
// subtítulo interno SRT español y externos «.es.srt» y «.en.forced.ass».
func newTracksEnv(t *testing.T) (*e11Env, uuid.UUID) {
	t.Helper()
	e := newE11Env(t)
	dir := filepath.Join(e.mediaRoot, "Animacion", "Pistas (2021)")
	_ = os.MkdirAll(dir, 0o755)
	srt := filepath.Join(t.TempDir(), "in.srt")
	_ = os.WriteFile(srt, []byte(srtES), 0o644)
	video := filepath.Join(dir, "Pistas (2021).mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-f", "lavfi", "-i", "sine=frequency=880:duration=8", "-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a:0", "ac3", "-c:a:1", "aac", "-c:s", "srt",
		"-metadata:s:a:0", "language=spa", "-metadata:s:a:1", "language=eng", "-metadata:s:s:0", "language=spa",
		"-disposition:a:0", "default", "-disposition:a:1", "0", video)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	_ = os.WriteFile(filepath.Join(dir, "Pistas (2021).es.srt"), []byte(srtES), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "Pistas (2021).en.forced.ass"), []byte(assEN), 0o644)
	_ = os.Symlink("/etc/passwd", filepath.Join(dir, "Pistas (2021).de.srt")) // enlace hacia fuera: nunca debe ofrecerse
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	e.scanLib(t, lib)
	var file uuid.UUID
	if err := e.pool.QueryRow(context.Background(), `select f.id from media_files f join media_items i on i.id = f.item_id where i.title = 'Pistas'`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	e.grantB(t, e.anim.ID)
	return e, file
}

func TestAudioTrackChoice(t *testing.T) {
	e, file := newTracksEnv(t)
	var plan struct {
		Method       string `json:"method"`
		AudioIndex   int    `json:"audio_index"`
		ConvertAudio bool   `json:"convert_audio"`
		URL          string `json:"url"`
	}
	// Chromium no reproduce AC3: con la pista predeterminada (AC3) → optimizado convirtiendo; con la inglesa (AAC) →
	// optimizado copiando el audio.
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps}, &plan)
	if plan.Method != "direct_stream" || plan.AudioIndex != 1 || !plan.ConvertAudio {
		t.Fatalf("pista predeterminada: %+v", plan)
	}
	e.call(t, "POST", "/api/v1/playback/decide", e.tokenB, map[string]any{"file_id": file, "caps": chromiumCaps, "audio_index": 2}, &plan)
	if plan.Method != "direct_stream" || plan.AudioIndex != 2 || plan.ConvertAudio || !strings.Contains(plan.URL, "audio=2") {
		t.Fatalf("pista inglesa: %+v", plan)
	}
	// Salto después de cambiar a la pista inglesa: /seek mide con esa pista y devuelve un origen válido.
	var seek struct {
		Start float64 `json:"start"`
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file.String()+"/seek?t=5&audio=2", e.tokenB, nil, &seek); c != 200 || seek.Start > 5 || seek.Start < -1 {
		t.Fatalf("salto con la pista inglesa: %d, origen %.3f", c, seek.Start)
	}
	// El flujo con la pista elegida sale bien.
	res := e.raw(t, "GET", plan.URL+"&t=1", map[string]string{"Authorization": "Bearer " + e.tokenB})
	head := make([]byte, 64)
	_, _ = io.ReadFull(res.Body, head)
	res.Body.Close()
	if !strings.Contains(string(head), "ftyp") {
		t.Fatalf("el flujo con la otra pista no es MP4: %q", head)
	}
}

// Cambiar de pista o de posición varias veces seguidas no acumula flujos: la misma sesión solo tiene uno vivo.
func TestOneRemuxPerSession(t *testing.T) {
	var rs remuxSessions
	session := uuid.New()
	ctx1, done1 := rs.take(context.Background(), session)
	go func() {
		<-ctx1.Done() // el primer flujo termina al cancelarlo
		done1()
	}()
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	ctx2, done2 := rs.take(context.Background(), session)
	if ctx1.Err() == nil {
		t.Fatal("el flujo anterior de la sesión debe cancelarse")
	}
	time.Sleep(50 * time.Millisecond) // el anterior se cierra en segundo plano
	if time.Since(start) > 2*time.Second || rs.active() != 1 || ctx2.Err() != nil {
		t.Fatalf("debe quedar un único flujo vivo: %d", rs.active())
	}
	other, doneOther := rs.take(context.Background(), uuid.New())
	if ctx2.Err() != nil || other.Err() != nil || rs.active() != 2 {
		t.Fatal("otra sesión no debe afectar")
	}
	done2()
	doneOther()
	if rs.active() != 0 {
		t.Fatalf("al terminar no debe quedar nada: %d", rs.active())
	}
}

func TestSubtitles(t *testing.T) {
	e, file := newTracksEnv(t)
	base := "/api/v1/stream/files/" + file.String() + "/subtitles"
	var list struct {
		Subtitles []media.SubTrack `json:"subtitles"`
	}
	if c := e.call(t, "GET", base, e.tokenB, nil, &list); c != 200 {
		t.Fatalf("lista: %d", c)
	}
	ids := map[string]media.SubTrack{}
	for _, s := range list.Subtitles {
		ids[s.ID] = s
	}
	if len(list.Subtitles) != 3 || !ids["i3"].Supported || ids["i3"].Language != "spa" || ids["e0"].Language != "en" || !ids["e0"].Forced || ids["e1"].Language != "es" {
		t.Fatalf("pistas inesperadas (el enlace simbólico no debe aparecer): %+v", list.Subtitles)
	}
	get := func(sid string) (int, string) {
		res := e.raw(t, "GET", base+"/"+sid, map[string]string{"Authorization": "Bearer " + e.tokenB})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}
	for sid, want := range map[string]string{"i3": "Hola, esto es una prueba.", "e1": "Segunda línea.", "e0": "Forced english"} {
		code, body := get(sid)
		if code != 200 || !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, want) {
			t.Errorf("%s: %d %q", sid, code, body)
		}
	}
	// La segunda vez sale de la caché.
	if code, body := get("i3"); code != 200 || !strings.Contains(body, "Hola") {
		t.Fatalf("caché: %d", code)
	}
	// Identificadores manipulados, pistas que no son subtítulos o fuera de rango → 404.
	for _, bad := range []string{"e2", "e9", "i0", "i1", "x1", "e01", "e-1", "e1abc", "..%2F..%2Fetc%2Fpasswd"} {
		if code, _ := get(bad); code != 404 {
			t.Errorf("%s debería dar 404, dio %d", bad, code)
		}
	}
	// Sin permiso sobre la biblioteca no hay ni lista ni subtítulos.
	e.grantB(t)
	if c := e.call(t, "GET", base, e.tokenB, nil, nil); c != 404 {
		t.Fatalf("sin permiso, la lista debería dar 404: %d", c)
	}
	if code, _ := get("e1"); code != 404 {
		t.Fatalf("sin permiso, el subtítulo debería dar 404: %d", code)
	}
}

// Salto en Optimizado (corrige la desincronización del audio tras avanzar): con fotogramas B y un fotograma clave cada
// 10 s, FFmpeg puede caer un GOP antes de lo esperado. Para cada salto, en el flujo real que entrega VELORA, el vídeo y
// el audio deben corresponder al instante del archivo que indica /seek (origen + tiempo del flujo = tiempo del archivo):
// así van sincronizados entre sí y con el reloj y los subtítulos del reproductor. El audio de prueba es un pitido corto
// al principio de cada segundo: en el flujo decodificado, cada pitido tiene que caer en un segundo exacto del archivo.
func TestSeekKeepsAudioInSync(t *testing.T) {
	e := newE11Env(t)
	dir := filepath.Join(e.mediaRoot, "Animacion", "Claves (2022)")
	_ = os.MkdirAll(dir, 0o755)
	src := filepath.Join(dir, "Claves (2022).mkv")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24000/1001:duration=45",
		"-f", "lavfi", "-i", "aevalsrc=if(lt(mod(t\\,1)\\,0.04)\\,sin(2*PI*1000*t)\\,0):s=48000:d=45", "-c:v", "libx264", "-preset", "ultrafast", "-g", "240", "-keyint_min", "240",
		"-sc_threshold", "0", "-bf", "2", "-c:a", "ac3", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	e.scanLib(t, lib)
	var file string
	_ = e.pool.QueryRow(context.Background(), `select f.id::text from media_files f join media_items i on i.id = f.item_id where i.title = 'Claves'`).Scan(&file)
	var plan struct {
		URL string `json:"url"`
	}
	if c := e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": file, "caps": chromiumCaps}, &plan); c != 200 {
		t.Fatalf("decide: %d", c)
	}
	// Primer pts (s) de cada flujo de un archivo.
	first := func(path, sel string) float64 {
		t.Helper()
		out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", sel, "-show_entries", "packet=pts_time", "-of", "csv=p=0", path).CombinedOutput()
		if err != nil {
			t.Fatalf("ffprobe %s: %v %s", filepath.Base(path), err, out)
		}
		best := math.Inf(1)
		for _, l := range strings.Split(string(out), "\n") {
			if v, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(l), ","), 64); err == nil && v < best {
				best = v
			}
		}
		return best
	}
	for _, q := range []string{"20.040", "12.3", "33.5", "0"} {
		var seek struct {
			Start float64 `json:"start"`
		}
		if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/seek?t="+q+"&audio=1", e.token, nil, &seek); c != 200 {
			t.Fatalf("seek %s: %d", q, c)
		}
		// Flujo real de VELORA desde ese punto (los primeros segundos bastan).
		// Los flujos anteriores tardan un momento en soltar su hueco (máximo 2): si el servidor está ocupado, se reintenta.
		var res *http.Response
		var body []byte
		for try := 0; try < 20; try++ {
			res = e.raw(t, "GET", plan.URL+"&t="+q, nil)
			body, _ = io.ReadAll(io.LimitReader(res.Body, 2<<20))
			res.Body.Close()
			if res.StatusCode != http.StatusServiceUnavailable {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if res.StatusCode != 200 || len(body) < 1000 {
			t.Fatalf("flujo t=%s: %d %q", q, res.StatusCode, body[:min(len(body), 200)])
		}
		out := filepath.Join(t.TempDir(), "flujo.mp4")
		_ = os.WriteFile(out, body, 0o644)
		// Lo mismo del archivo original con sus marcas de tiempo, para saber a qué instante corresponde cada paquete.
		ref := filepath.Join(t.TempDir(), "ref.mkv")
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}
		if q != "0" {
			args = append(args, "-noaccurate_seek", "-ss", q)
		}
		args = append(args, "-copyts", "-i", src, "-map", "0:v:0", "-map", "0:1", "-c", "copy", "-frames:v", "8", ref)
		if o, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, o)
		}
		ov, oa := first(out, "v:0"), first(out, "a:0")
		sv := first(ref, "v:0")
		if math.Abs(seek.Start+ov-sv) > 0.06 {
			t.Errorf("t=%s: el vídeo del flujo empieza en %.3f del archivo, pero el reproductor creería %.3f", q, sv, seek.Start+ov)
		}
		// Primer pitido del audio entregado: su instante en el archivo debe ser un segundo exacto.
		pcm, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", out, "-map", "0:a:0", "-ac", "1", "-ar", "8000",
			"-f", "s16le", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		onset := -1
		for i := 0; i+1 < len(pcm); i += 2 {
			if v := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8); v > 8000 || v < -8000 {
				onset = i / 2
				break
			}
		}
		if onset < 0 {
			t.Fatalf("t=%s: el flujo no trae audio", q)
		}
		at := seek.Start + oa + float64(onset)/8000
		if d := math.Abs(at - math.Round(at)); d > 0.06 {
			t.Errorf("t=%s: AUDIO DESINCRONIZADO: el pitido del segundo %.0f se presenta en %.3f (%.2f s de desfase)", q, math.Round(at), at, d)
		}
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/seek?t=NaN", e.token, nil, nil); c != 400 {
		t.Errorf("t=NaN debería dar 400: %d", c)
	}
	// Saltos rápidos y cancelados (el usuario arrastra la barra): no se acumulan ni quedan FFmpeg vivos.
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(5+i*10)*time.Millisecond)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", e.server.URL+"/api/v1/stream/files/"+file+"/seek?t="+strconv.Itoa(5+i*3)+"&audio=1", nil)
			req.Header.Set("Authorization", "Bearer "+e.token)
			if res, err := http.DefaultClient.Do(req); err == nil {
				res.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, _ := exec.Command("pgrep", "-x", "ffmpeg").Output()
		if len(strings.TrimSpace(string(out))) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("quedan procesos FFmpeg tras saltos cancelados: %s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.grantB(t) // B sin acceso
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/seek?t=5", e.tokenB, nil, nil); c != 404 {
		t.Errorf("sin acceso debería dar 404: %d", c)
	}
}

// Capítulos: los titulados como intro o créditos se marcan; los demás no (nunca por duración).
func TestChaptersWithIntroAndCredits(t *testing.T) {
	e := newE11Env(t)
	dir := filepath.Join(e.mediaRoot, "Animacion", "Capitulos (2020)")
	_ = os.MkdirAll(dir, 0o755)
	meta := filepath.Join(t.TempDir(), "meta.txt")
	_ = os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=3000\ntitle=Intro\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=3000\nEND=8000\ntitle=La historia\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=8000\nEND=10000\ntitle=Créditos finales\n"), 0o644)
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=10",
		"-i", meta, "-map_metadata", "1", "-map_chapters", "1", "-c:v", "libx264", "-preset", "ultrafast", filepath.Join(dir, "Capitulos (2020).mkv"))
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(context.Background(), e.anim.ID)
	e.scanLib(t, lib)
	var file string
	_ = e.pool.QueryRow(context.Background(), `select f.id::text from media_files f join media_items i on i.id = f.item_id where i.title = 'Capitulos'`).Scan(&file)
	var out struct {
		Chapters []media.Chapter `json:"chapters"`
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/chapters", e.token, nil, &out); c != 200 || len(out.Chapters) != 3 {
		t.Fatalf("capítulos: %d %+v", c, out.Chapters)
	}
	if out.Chapters[0].Kind != "intro" || out.Chapters[1].Kind != "" || out.Chapters[2].Kind != "credits" || out.Chapters[2].Start != 8 {
		t.Fatalf("tipos mal reconocidos: %+v", out.Chapters)
	}
	e.grantB(t)
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/chapters", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("sin acceso debería dar 404: %d", c)
	}
}

// Capítulos y orígenes de salto: una consulta repetida no vuelve a abrir el archivo; otra posición, otra pista o una
// versión nueva del archivo sí. El permiso se comprueba siempre antes de servir lo guardado.
func TestPlaybackAnalysisIsReused(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	dir := filepath.Join(e.mediaRoot, "Animacion", "Reuso (2021)")
	_ = os.MkdirAll(dir, 0o755)
	meta := filepath.Join(t.TempDir(), "meta.txt")
	_ = os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=3000\ntitle=Intro\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=3000\nEND=10000\ntitle=Resto\n"), 0o644)
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-i", meta, "-map", "0:v", "-map", "1:a", "-map_metadata", "2", "-map_chapters", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-c:a", "aac", filepath.Join(dir, "Reuso (2021).mkv"))
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(ctx, e.anim.ID)
	e.scanLib(t, lib)
	var file string
	_ = e.pool.QueryRow(ctx, `select f.id::text from media_files f join media_items i on i.id = f.item_id where i.title = 'Reuso'`).Scan(&file)
	e.grantB(t, e.anim.ID)

	opens := func(do func()) int64 {
		t.Helper()
		before := playbackOpens.Load()
		do()
		return playbackOpens.Load() - before
	}
	chapters := func(tok string) int {
		var out struct {
			Chapters []media.Chapter `json:"chapters"`
		}
		c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/chapters", tok, nil, &out)
		if c == 200 && len(out.Chapters) != 2 {
			t.Fatalf("capítulos: %+v", out.Chapters)
		}
		return c
	}
	seek := func(q string) float64 {
		var out struct {
			Start float64 `json:"start"`
		}
		if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/seek?"+q, e.token, nil, &out); c != 200 {
			t.Fatalf("seek %s: %d", q, c)
		}
		return out.Start
	}
	if n := opens(func() { chapters(e.token) }); n != 1 {
		t.Fatalf("la primera consulta de capítulos debería abrir el archivo una vez: %d", n)
	}
	if n := opens(func() { chapters(e.token); chapters(e.tokenB) }); n != 0 {
		t.Fatalf("los capítulos repetidos no deberían reabrir el archivo: %d", n)
	}
	var first, again float64
	if n := opens(func() { first = seek("t=0&audio=1") }); n != 1 {
		t.Fatalf("el primer cálculo del inicio debería abrir el archivo: %d", n)
	}
	if n := opens(func() { again = seek("t=0.000&audio=1") }); n != 0 || again != first {
		t.Fatalf("el mismo salto no debería reabrir el archivo: %d (%.3f / %.3f)", n, first, again)
	}
	if n := opens(func() { seek("t=4.2&audio=1") }); n != 1 {
		t.Fatalf("otra posición se calcula: %d", n)
	}
	if n := opens(func() { seek("t=4.2&audio=-1") }); n != 1 {
		t.Fatalf("otra pista de audio se calcula: %d", n)
	}
	// Archivo sustituido y vuelto a analizar (otro tamaño): nada de lo guardado vale.
	if _, err := e.pool.Exec(ctx, `update media_files set size_bytes = size_bytes + 1 where id = $1`, file); err != nil {
		t.Fatal(err)
	}
	if n := opens(func() { chapters(e.token); seek("t=0&audio=1") }); n != 2 {
		t.Fatalf("una versión nueva del archivo debería volver a analizarse: %d", n)
	}
	// Sustituido con el mismo tamaño y el mismo análisis: el escáner solo ve otra fecha de modificación, y basta.
	if _, err := e.pool.Exec(ctx, `update media_files set mtime = mtime + interval '1 second' where id = $1`, file); err != nil {
		t.Fatal(err)
	}
	if n := opens(func() { chapters(e.token); seek("t=0&audio=1") }); n != 2 {
		t.Fatalf("otra fecha de modificación con igual tamaño debería volver a analizarse: %d", n)
	}
	if n := opens(func() { chapters(e.token); seek("t=0&audio=1") }); n != 0 {
		t.Fatalf("la versión nueva también se reutiliza: %d", n)
	}
	// Sustitución real en disco con otros capítulos, detectada por el escáner: se sirven los nuevos.
	video := filepath.Join(dir, "Reuso (2021).mkv")
	_ = os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=4000\ntitle=Cabecera\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=4000\nEND=10000\ntitle=Historia\n"), 0o644)
	regen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=25:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-i", meta, "-map", "0:v", "-map", "1:a", "-map_metadata", "2", "-map_chapters", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "50", "-c:a", "aac", video)
	if out, err := regen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(video, later, later)
	e.scanLib(t, lib)
	var replaced struct {
		Chapters []media.Chapter `json:"chapters"`
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/chapters", e.token, nil, &replaced); c != 200 || len(replaced.Chapters) != 2 || replaced.Chapters[0].Title != "Cabecera" {
		t.Fatalf("tras sustituir el archivo deberían servirse sus capítulos nuevos: %d %+v", c, replaced.Chapters)
	}
	// Permiso retirado: lo guardado no se sirve.
	e.grantB(t)
	if c := chapters(e.tokenB); c != 404 {
		t.Fatalf("sin acceso, los capítulos guardados no deberían servirse: %d", c)
	}
	if c := e.call(t, "GET", "/api/v1/stream/files/"+file+"/seek?t=0&audio=1", e.tokenB, nil, nil); c != 404 {
		t.Fatalf("sin acceso, el salto guardado no debería servirse: %d", c)
	}
}

// Remote episodes are eligible too; the worker now respects manual pause and playback before reading audio.
func TestRemoteFilesIncludedInControlledIntroAnalysis(t *testing.T) {
	e := newSeriesEnv(t, "")
	ctx := context.Background()
	if res := e.scan(t, e.lib); res.Episodes == 0 {
		t.Fatalf("scan: %+v", res)
	}
	// The generated test clips are short; make their catalogue durations eligible for intro comparison.
	if _, err := e.pool.Exec(ctx, `update media_files set duration_ms=60000`); err != nil {
		t.Fatal(err)
	}
	st := &store.Store{Pool: e.pool}
	local, err := st.CountSeasonsNeedingMarkers(ctx, markers.Version)
	if err != nil || local == 0 {
		t.Fatal(local, err)
	}
	if _, err = e.pool.Exec(ctx, `update library_paths set kind='remote_fuse' where library_id=$1`, e.lib); err != nil {
		t.Fatal(err)
	}
	if n, err := st.CountSeasonsNeedingMarkers(ctx, markers.Version); err != nil || n != local {
		t.Fatal("remote count", n, err)
	}
	seasons, err := st.SeasonsNeedingMarkers(ctx, markers.Version, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range seasons {
		files, err := st.SeasonMarkerFiles(ctx, sn.ID, markers.Version)
		if err != nil || len(files) == 0 {
			t.Fatal("remote episodes missing", err)
		}
	}
	if err = st.SetSetting(ctx, "markers.paused", true); err != nil {
		t.Fatal(err)
	}
	paused, _, err := st.MarkerPauseState(ctx)
	if err != nil || !paused {
		t.Fatal("manual pause lost", err)
	}
}

// Referencia reproducible de tiempos (go test -run TestPlaybackLatencyReference -v): capítulos y origen de un salto, la
// primera vez y repetidos, en local y con un origen remoto simulado (cada apertura espera 300 ms, como un montaje de
// Teldrive lento), y el efecto de dos flujos optimizados de otras sesiones sobre un salto nuevo. Con el origen lento,
// la consulta repetida no debe pagar la apertura.
func TestPlaybackLatencyReference(t *testing.T) {
	e := newE11Env(t)
	ctx := context.Background()
	dir := filepath.Join(e.mediaRoot, "Animacion", "Medida (2020)")
	_ = os.MkdirAll(dir, 0o755)
	meta := filepath.Join(t.TempDir(), "m.txt")
	_ = os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=20000\ntitle=Intro\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=20000\nEND=90000\ntitle=Resto\n"), 0o644)
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24:duration=90",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=90", "-i", meta, "-map", "0:v", "-map", "1:a", "-map_chapters", "2",
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "96", "-bf", "2", "-c:a", "ac3", filepath.Join(dir, "Medida (2020).mkv")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	st := &store.Store{Pool: e.pool}
	lib, _ := st.LibraryByID(ctx, e.anim.ID)
	e.scanLib(t, lib)
	var file string
	_ = e.pool.QueryRow(ctx, `select f.id::text from media_files f join media_items i on i.id = f.item_id where i.title = 'Medida'`).Scan(&file)
	e.grantB(t, e.anim.ID)
	ms := func(f func()) float64 { s := time.Now(); f(); return float64(time.Since(s).Microseconds()) / 1000 }
	median := func(v []float64) float64 { sort.Float64s(v); return v[len(v)/2] }
	get := func(p string) {
		if c := e.call(t, "GET", p, e.token, nil, nil); c != 200 {
			t.Fatalf("%s: %d", p, c)
		}
	}
	chPath, skPath := "/api/v1/stream/files/"+file+"/chapters", "/api/v1/stream/files/"+file+"/seek?t=45&audio=1"
	measure := func(label string) (chCold, chWarm, skCold, skWarm float64) {
		var a, b, c, d []float64
		for i := 0; i < 5; i++ {
			chapterCache, seekCache = media.NewResultCache[[]media.Chapter](512), media.NewResultCache[float64](4096)
			a = append(a, ms(func() { get(chPath) }))
			b = append(b, ms(func() { get(chPath) }))
			c = append(c, ms(func() { get(skPath) }))
			d = append(d, ms(func() { get(skPath) }))
		}
		chCold, chWarm, skCold, skWarm = median(a), median(b), median(c), median(d)
		t.Logf("%s · capítulos: primera %.1f ms, repetida %.1f ms · salto: primero %.1f ms, repetido %.1f ms", label, chCold, chWarm, skCold, skWarm)
		return
	}
	measure("local")
	playbackOpenDelay.Store(int64(300 * time.Millisecond))
	t.Cleanup(func() { playbackOpenDelay.Store(0) })
	chCold, chWarm, skCold, skWarm := measure("remoto simulado (apertura de 300 ms)")
	if chCold < 300 || skCold < 300 || chWarm > 150 || skWarm > 150 {
		t.Errorf("con un origen lento, lo repetido no debería volver a abrir el archivo: %.0f/%.0f %.0f/%.0f ms", chCold, chWarm, skCold, skWarm)
	}
	playbackOpenDelay.Store(0)
	// Competencia: dos flujos optimizados de sesiones distintas mientras se calcula un salto nuevo.
	var plan struct {
		URL string `json:"url"`
	}
	e.call(t, "POST", "/api/v1/playback/decide", e.token, map[string]any{"file_id": file, "caps": chromiumCaps}, &plan)
	var idle []float64
	for i := 0; i < 5; i++ {
		seekCache = media.NewResultCache[float64](4096)
		idle = append(idle, ms(func() { get(skPath) }))
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, tok := range []string{e.token, e.tokenB} {
		go func(tok string) {
			req, _ := http.NewRequestWithContext(cctx, "GET", e.server.URL+plan.URL+"&t=0", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			if res, err := http.DefaultClient.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}
		}(tok)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.srv.remux.InUse() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	busySlots := e.srv.remux.InUse()
	var busy []float64
	for i := 0; i < 5; i++ {
		seekCache = media.NewResultCache[float64](4096)
		busy = append(busy, ms(func() { get(skPath) }))
	}
	t.Logf("salto nuevo: %.1f ms sin otros flujos, %.1f ms con %d flujos optimizados activos", median(idle), median(busy), busySlots)
}
