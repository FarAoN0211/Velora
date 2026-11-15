package api

// B3 de E1.2: subtítulos de texto como WebVTT. Mismo control de acceso que la reproducción (permiso de reproducir,
// biblioteca visible para quien llama, carpeta revalidada). Las pistas internas obligan a leer el archivo entero, así que
// el resultado se guarda en caché y como mucho se extrae una a la vez.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"velora/internal/media"
	"velora/internal/store"
)

// Plazos de conversión: los internos leen todo el archivo (en un HDD, minutos para una película grande; en Teldrive,
// por red, bastante más). El cliente no corta mientras tanto gracias a las NOTE de subKeepAlive.
var (
	internalSubTimeout = 15 * time.Minute
	externalSubTimeout = 30 * time.Second
	// subKeepAlive: mientras se extrae una pista interna, cada cuánto se manda una línea NOTE. El reproductor de TV
	// (APK 0.5–0.6) corta una petición a los 30 s sin recibir nada, y extraer una pista de una película grande tarda eso
	// o más: sin estas líneas, el subtítulo nunca llegaba en la TV. Los lectores de la web y de la TV solo miran las
	// líneas con «-->», así que ignoran las NOTE.
	subKeepAlive = 10 * time.Second
	// extractSubtitles es inyectable en pruebas (para simular una extracción lenta o fallida).
	extractSubtitles = media.ExtractSubtitles
)

// Extracciones de pistas internas en curso, por archivo y versión (tamaño y fecha). Una petición que llega mientras
// tanto espera a la misma extracción en lugar de leer otra vez la película.
var subJobs = struct {
	sync.Mutex
	running map[string]chan struct{}
}{running: map[string]chan struct{}{}}

func subCacheDir(configDir string) string { return filepath.Join(configDir, "cache", "subtitles") }

func subCacheFile(dir string, id uuid.UUID, sid string, st os.FileInfo) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%s-%d-%d.vtt", id, sid, st.Size(), st.ModTime().Unix()))
}

// prepareInternalSubs saca de una sola lectura todas las pistas internas de texto del archivo que aún no están en caché
// y devuelve un canal que se cierra al terminar. Se queda con f (lo cierra). Si ya hay una extracción del mismo
// archivo, devuelve la suya. Como mucho una extracción a la vez en todo el servidor (leen la película entera).
func (s *Server) prepareInternalSubs(info store.FileInfo, probe media.Probe, f *os.File, st os.FileInfo) <-chan struct{} {
	dir := subCacheDir(s.cfg.ConfigDir)
	outs := map[int]string{}
	for _, t := range media.SubtitleTracks(probe, nil) {
		if !t.Supported || t.External {
			continue
		}
		file := subCacheFile(dir, info.ID, t.ID, st)
		if _, err := os.Stat(file); err == nil {
			continue
		}
		n, _ := strconv.Atoi(t.ID[1:])
		outs[n] = file
	}
	done := make(chan struct{})
	if len(outs) == 0 {
		f.Close()
		close(done)
		return done
	}
	key := fmt.Sprintf("%s|%s|%d|%d", dir, info.ID, st.Size(), st.ModTime().Unix())
	subJobs.Lock()
	if running, ok := subJobs.running[key]; ok {
		subJobs.Unlock()
		f.Close()
		return running
	}
	subJobs.running[key] = done
	subJobs.Unlock()
	go func() {
		defer func() {
			f.Close()
			subJobs.Lock()
			delete(subJobs.running, key)
			subJobs.Unlock()
			close(done)
		}()
		s.subSlot <- struct{}{}
		defer func() { <-s.subSlot }()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			slog.Warn("no se pudo crear la caché de subtítulos", "error", err)
			return
		}
		tmp := map[int]string{}
		for n, file := range outs {
			tmp[n] = file + ".tmp"
		}
		ctx, cancel := context.WithTimeout(context.Background(), internalSubTimeout)
		defer cancel()
		start := time.Now()
		err := extractSubtitles(ctx, f, probe, tmp)
		for n, t := range tmp {
			b, rerr := os.ReadFile(t)
			if err == nil && rerr == nil && len(b) <= media.MaxVTTBytes &&
				strings.HasPrefix(strings.TrimPrefix(string(b), "\ufeff"), "WEBVTT") {
				_ = os.Chmod(t, 0o640)
				_ = os.Rename(t, outs[n])
			} else {
				_ = os.Remove(t)
			}
		}
		if err != nil {
			slog.Warn("no se pudieron preparar los subtítulos internos", "fichero", info.ID, "error", err)
			return
		}
		slog.Info("subtítulos internos preparados", "fichero", info.ID, "pistas", len(outs), "ms", time.Since(start).Milliseconds())
	}()
	return done
}

// prewarmInternalSubs empieza a preparar los subtítulos internos en cuanto se consulta la lista (al abrir la ficha o el
// reproductor), para que al elegir uno ya esté listo. Solo en carpetas locales: en Teldrive supondría descargar la
// película entera por abrir una ficha.
func (s *Server) prewarmInternalSubs(root *os.Root, info store.FileInfo, probe media.Probe) {
	if pathKind(info.LibPath) != "local" {
		return
	}
	hasText := false
	for _, t := range media.SubtitleTracks(probe, nil) {
		hasText = hasText || (t.Supported && !t.External)
	}
	if !hasText {
		return
	}
	f, err := root.Open(info.RelPath)
	if err != nil {
		return
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return
	}
	s.prepareInternalSubs(info, probe, f, st)
}

func (s *Server) listSubtitles(w http.ResponseWriter, r *http.Request) {
	root, info, ok := s.openPlaybackRoot(w, r)
	if !ok {
		return
	}
	defer root.Close()
	var probe media.Probe
	_ = json.Unmarshal(info.Probe, &probe)
	tracks := media.SubtitleTracks(probe, media.ExternalSubs(root, info.RelPath))
	s.prewarmInternalSubs(root, info, probe)
	var providerError string
	if len(tracks) == 0 {
		online, err := s.onlineSubtitles(r, info)
		if err != nil {
			providerError = "OpenSubtitles no está disponible. Revisa la configuración o la cuota."
		} else {
			tracks = online
		}
	}
	// Siempre una lista: sin subtítulos propios y con OpenSubtitles desactivado, onlineSubtitles devuelve nil y la API
	// respondía "subtitles": null, lo que tumbaba la ficha web (pantalla en negro en la TV con «Merlín», 2026-11-08).
	if tracks == nil {
		tracks = []media.SubTrack{}
	}
	media.ApplySubtitleDefaults(tracks)
	writeJSON(w, http.StatusOK, map[string]any{"subtitles": tracks, "provider_error": providerError})
}

func (s *Server) subtitleVTT(w http.ResponseWriter, r *http.Request) {
	root, info, ok := s.openPlaybackRoot(w, r)
	if !ok {
		return
	}
	defer root.Close()
	sid := chi.URLParam(r, "sid")
	if strings.HasPrefix(sid, "o") {
		var p media.Probe
		_ = json.Unmarshal(info.Probe, &p)
		if len(media.SubtitleTracks(p, media.ExternalSubs(root, info.RelPath))) > 0 {
			http.NotFound(w, r)
			return
		}
		s.onlineSubtitleVTT(w, r, info, sid)
		return
	}
	n, err := strconv.Atoi(strings.TrimLeft(sid, "ie"))
	if len(sid) < 2 || (sid[0] != 'i' && sid[0] != 'e') || err != nil || n < 0 || strconv.Itoa(n) != sid[1:] {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este subtítulo.")
		return
	}
	var probe media.Probe
	_ = json.Unmarshal(info.Probe, &probe)

	if sid[0] == 'i' {
		if _, err = media.InternalSubArgs(probe, n); err != nil {
			writeError(w, http.StatusNotFound, "no_encontrado", "Este subtítulo no se puede mostrar como texto (los de imagen llegan en E2).")
			return
		}
		src, err := root.Open(info.RelPath)
		if err != nil {
			writeError(w, http.StatusNotFound, "archivo_no_disponible", "Este archivo ya no está en la biblioteca.")
			return
		}
		st, err := src.Stat()
		if err != nil {
			src.Close()
			s.internal(w, "subtitles", err)
			return
		}
		cacheFile := subCacheFile(subCacheDir(s.cfg.ConfigDir), info.ID, sid, st)
		if b, err := os.ReadFile(cacheFile); err == nil {
			src.Close()
			serveVTT(w, b)
			return
		}
		s.streamInternalVTT(w, r, s.prepareInternalSubs(info, probe, src, st), cacheFile)
		return
	}
	var args []string
	var src *os.File
	var timeout time.Duration
	{
		subs := media.ExternalSubs(root, info.RelPath)
		if n >= len(subs) {
			writeError(w, http.StatusNotFound, "no_encontrado", "No existe este subtítulo.")
			return
		}
		rel := path.Join(path.Dir(info.RelPath), subs[n].Name)
		// Lstat dentro de la raíz: archivo normal (sin enlaces) y de tamaño razonable.
		st, err := root.Lstat(rel)
		if err != nil || !st.Mode().IsRegular() || st.Size() > media.MaxExternalSubBytes {
			writeError(w, http.StatusNotFound, "no_encontrado", "Este subtítulo no está disponible.")
			return
		}
		if src, err = root.Open(rel); err != nil {
			writeError(w, http.StatusNotFound, "no_encontrado", "Este subtítulo no está disponible.")
			return
		}
		args = media.ExternalSubArgs(subs[n].Format)
		timeout = externalSubTimeout
	}
	defer src.Close()

	// Caché por archivo, pista, tamaño y fecha: si el archivo cambia, se vuelve a convertir.
	st, err := src.Stat()
	if err != nil {
		s.internal(w, "subtitles", err)
		return
	}
	cacheDir := subCacheDir(s.cfg.ConfigDir)
	cacheFile := subCacheFile(cacheDir, info.ID, sid, st)
	if b, err := os.ReadFile(cacheFile); err == nil {
		serveVTT(w, b)
		return
	}
	// Los externos son pequeños: se convierten en segundos.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()
	vtt, err := media.ConvertSubtitle(ctx, src, args)
	if err != nil {
		slog.Warn("no se pudo preparar un subtítulo", "fichero", info.ID, "pista", sid, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "subtitulo_fallido", "No se ha podido preparar este subtítulo.")
		return
	}
	if err := os.MkdirAll(cacheDir, 0o750); err == nil {
		tmp := cacheFile + ".tmp"
		if os.WriteFile(tmp, vtt, 0o640) == nil {
			_ = os.Rename(tmp, cacheFile)
		}
	}
	serveVTT(w, vtt)
}

// streamInternalVTT espera a que esté lista la pista interna (extraída junto con las demás del archivo) y, mientras
// tanto, manda la cabecera WEBVTT y una línea NOTE cada subKeepAlive para que el cliente no corte la conexión. Si el
// Las frases completas se envían conforme aparecen, sin esperar al final. Si el
// cliente se va, la extracción sigue y queda en caché para la siguiente vez. Como la respuesta ya ha empezado con 200, un
// fallo se indica con una NOTE y sin pistas (la TV avisa «Este subtítulo está vacío»).
func (s *Server) streamInternalVTT(w http.ResponseWriter, r *http.Request, done <-chan struct{}, cacheFile string) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // la próxima petición sale de la caché del servidor al instante
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(b string) bool {
		if _, err := io.WriteString(w, b); err != nil {
			return false
		}
		_ = rc.Flush()
		return true
	}
	if !write("WEBVTT\n\n") {
		return
	}
	tick := time.NewTicker(subKeepAlive)
	defer tick.Stop()
	partial := time.NewTicker(250 * time.Millisecond)
	defer partial.Stop()
	sent := 0
	sendAvailable := func(file string, complete bool) bool {
		b, err := os.ReadFile(file)
		if err != nil {
			return true
		}
		body := vttBody(b)
		end := len(body)
		if !complete {
			end = strings.LastIndex(body, "\n\n") + 2
			if end < 2 {
				return true
			}
		}
		if end <= sent {
			return true
		}
		if !write(body[sent:end]) {
			return false
		}
		sent = end
		return true
	}
	for {
		select {
		case <-done:
			if _, err := os.Stat(cacheFile); err != nil {
				write("NOTE No se ha podido preparar este subtítulo.\n\n")
				return
			}
			sendAvailable(cacheFile, true)
			return
		case <-partial.C:
			if !sendAvailable(cacheFile+".tmp", false) {
				return
			}
		case <-tick.C:
			if !write("NOTE Preparando el subtítulo\n\n") {
				return
			}
		case <-r.Context().Done():
			return
		}
	}

}

// vttBody quita la cabecera «WEBVTT» (y la marca BOM) de una conversión, que ya se ha mandado.
func vttBody(vtt []byte) string {
	b := strings.TrimPrefix(string(vtt), "\ufeff")
	if i := strings.IndexByte(b, '\n'); i >= 0 && strings.HasPrefix(b, "WEBVTT") {
		return strings.TrimLeft(b[i+1:], "\r\n")
	}
	return b
}

func serveVTT(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(b)
}
