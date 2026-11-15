package api

import (
	"context"
	"encoding/json"
	"errors"
	"hash/fnv"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"velora/internal/authz"
	"velora/internal/library"
	"velora/internal/media"
	"velora/internal/store"
)

// revokeCheckEvery: cada cuánto vuelve a comprobar el servidor, durante un flujo abierto, que el usuario sigue teniendo
// acceso al archivo. Revocar un permiso corta una reproducción ya iniciada en, como mucho, este plazo; las peticiones
// nuevas (saltos, Range) lo ven al instante porque se autorizan antes de abrir nada.
var revokeCheckEvery = 30 * time.Second

var errRevoked = errors.New("acceso revocado durante la reproducción")

// revocableWriter corta el flujo si, al comprobarlo periódicamente, el permiso ya no existe. Si la comprobación falla
// (base de datos caída…), se deniega: nunca se sigue sirviendo sin poder confirmar el acceso.
type revocableWriter struct {
	http.ResponseWriter
	allowed func() bool
	next    time.Time
}

func (w *revocableWriter) Write(p []byte) (int, error) {
	if time.Now().After(w.next) {
		if !w.allowed() {
			return 0, errRevoked
		}
		w.next = time.Now().Add(revokeCheckEvery)
	}
	return w.ResponseWriter.Write(p)
}

func (w *revocableWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// revocable comprueba a la vez la sesión (no revocada, usuario habilitado y con permiso de reproducir) y el acceso actual a
// la biblioteca del archivo.
func (s *Server) revocable(w http.ResponseWriter, r *http.Request, fileID uuid.UUID) *revocableWriter {
	v := viewer(r)
	sessionID := authz.FromContext(r.Context()).SessionID
	return &revocableWriter{ResponseWriter: w, next: time.Now().Add(revokeCheckEvery), allowed: func() bool {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if ok, err := s.store.SessionStillValid(ctx, sessionID); err != nil || !ok {
			return false
		}
		_, _, err := s.store.FileForPlayback(ctx, v, fileID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("no se pudo comprobar el permiso durante la reproducción; se corta", "error", err)
		}
		if err != nil {
			return false
		}
		return s.admitPlayback(ctx, r, fileID, r.URL.Query().Get("player_id")) == nil
	}}
}

// remuxIdle: si el cliente no lee en este tiempo (pausa larga, pestaña olvidada) se corta el flujo y se libera FFmpeg.
// El reproductor web reanuda solo desde la posición actual.
var remuxIdle = 2 * time.Minute

func (s *Server) streamRoutes(r chi.Router) {
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}", authz.User, s.streamDirect)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/remux", authz.User, s.streamRemux)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/transcode", authz.User, s.streamTranscode)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/seek", authz.User, s.seekPoint)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/chapters", authz.User, s.chapters)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/subtitles", authz.User, s.listSubtitles)
	s.handle(r, http.MethodGet, "/api/v1/stream/files/{id}/subtitles/{sid}", authz.User, s.subtitleVTT)
}

type decideRequest struct {
	PlayerID   string     `json:"player_id"`
	FileID     uuid.UUID  `json:"file_id"`
	Caps       media.Caps `json:"caps"`
	AudioIndex *int       `json:"audio_index"`
	DryRun     bool       `json:"dry_run"`
}

type decideResponse struct {
	media.Plan
	URL        string `json:"url,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Seekable   bool   `json:"seekable"` // Direct Play: el navegador busca por sí mismo; Direct Stream: el cliente pide ?t=
}

// decide aplica el motor de decisión (§5.1). Con dry_run solo estima ("En este dispositivo", diseño rev. 2):
// no reserva recursos ni lanza FFmpeg.
func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	var req decideRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !validPlaybackID(req.PlayerID) {
		writeError(w, 422, "reproductor_invalido", "Identificador de reproductor no válido.")
		return
	}
	who := authz.FromContext(r.Context())
	if !req.DryRun && !authz.Can(who, authz.ActPlay) {
		writeError(w, http.StatusForbidden, "sin_permiso", "Tu perfil no tiene permiso para reproducir.")
		return
	}
	if !req.DryRun { // empezar a reproducir es uso real del perfil
		_ = s.store.TouchSession(r.Context(), who.SessionID, true)
	}
	f, _, err := s.store.FileForPlayback(r.Context(), viewer(r), req.FileID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este archivo.")
		return
	}
	if err != nil {
		s.internal(w, "decide", err)
		return
	}
	if f.Missing {
		writeError(w, http.StatusNotFound, "archivo_no_disponible", "Este archivo ya no está en la biblioteca. Prueba otra versión.")
		return
	}
	var probe media.Probe
	if err := json.Unmarshal(f.Probe, &probe); err != nil || len(probe.Streams) == 0 {
		writeError(w, http.StatusConflict, "sin_analizar", "El archivo todavía no se ha analizado. Espera a que termine el escaneo.")
		return
	}
	pref := media.PreferredAudio(probe, s.playbackPreferences(r.Context()).AudioLanguage)
	if req.AudioIndex != nil && *req.AudioIndex >= 0 {
		pref = *req.AudioIndex
	}
	settings := s.playbackPreferences(r.Context())
	plan := media.DecideWithTranscode(probe, filepath.Ext(f.RelPath), req.Caps, pref, media.TranscodeOptions{Disabled: settings.TranscodingMode == "off" || !req.Caps.VideoTranscode, MaxHeight: settings.TranscodingMaxHeight, BitrateKbps: settings.TranscodingBitrate})
	plan.AudioDynamics = settings.AudioDynamics
	if plan.ConvertVideo && plan.Method == media.Transcode {
		plan.Encoder = s.transcoder.EncoderFor(settings.TranscodingMode, plan)
	}
	if !req.DryRun && plan.Method != media.Unsupported {
		if !s.reservePlayback(w, r, f.ID, req.PlayerID) {
			return
		}
		s.activity.remember(who.SessionID, f.ID, plan)
	}
	resp := decideResponse{Plan: plan, DurationMS: probe.DurationMS()}
	switch plan.Method {
	case media.Transcode:
		token := s.signTranscode(transcodeTicket{File: f.ID, Session: who.SessionID, Version: fileVersion(f), Expires: time.Now().Add(8 * time.Hour).Unix(), Plan: plan})
		resp.URL = "/api/v1/stream/files/" + f.ID.String() + "/transcode?ticket=" + token + "&audio=" + strconv.Itoa(plan.AudioIndex)
	case media.DirectPlay:
		resp.URL = "/api/v1/stream/files/" + f.ID.String()
		resp.Seekable = true
	case media.DirectStream:
		resp.URL = "/api/v1/stream/files/" + f.ID.String() + "/remux?audio=" + strconv.Itoa(plan.AudioIndex) +
			"&convert=" + strconv.FormatBool(plan.ConvertAudio) + "&container=" + plan.OutputContainer
	}
	if resp.URL != "" && req.PlayerID != "" {
		sep := "?"
		if strings.Contains(resp.URL, "?") {
			sep = "&"
		}
		resp.URL += sep + "player_id=" + req.PlayerID
	}
	writeJSON(w, http.StatusOK, resp)
}

// authorizePlayback comprueba el permiso de reproducir y el acceso al archivo, sin abrir nada. Va siempre antes de servir
// un resultado guardado: la caché nunca se salta un permiso retirado.
func (s *Server) authorizePlayback(w http.ResponseWriter, r *http.Request) (store.FileInfo, bool) {
	who := authz.FromContext(r.Context())
	if !authz.Can(who, authz.ActPlay) {
		writeError(w, http.StatusForbidden, "sin_permiso", "Tu perfil no tiene permiso para reproducir.")
		return store.FileInfo{}, false
	}
	id, ok := pathUUID(r, "id")
	if !ok {
		writeError(w, http.StatusNotFound, "no_encontrado", "No existe este archivo.")
		return store.FileInfo{}, false
	}
	info, _, err := s.store.FileForPlayback(r.Context(), viewer(r), id)
	if err != nil || info.Missing {
		writeError(w, http.StatusNotFound, "archivo_no_disponible", "Este archivo no está disponible.")
		return info, false
	}
	if streamingPlaybackRequest(r, id) && !s.reservePlayback(w, r, id, r.URL.Query().Get("player_id")) {
		return info, false
	}
	return info, true
}

// playbackOpens cuenta los archivos abiertos para reproducir o analizar (pruebas: una consulta repetida no reabre).
var playbackOpens atomic.Int64

// playbackOpenDelay simula en pruebas la apertura lenta de un montaje remoto (Teldrive): se espera antes de abrir.
var playbackOpenDelay atomic.Int64 // nanosegundos

// fileVersion identifica la versión de un archivo: su ruta, tamaño, fecha de modificación (la que registra el escáner
// al detectar una sustitución, aunque tenga el mismo tamaño y el mismo análisis) y la huella de su análisis.
func fileVersion(info store.FileInfo) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(info.LibPath + "\x00" + info.RelPath + "\x00"))
	_, _ = h.Write(info.Probe)
	return info.ID.String() + ":" + strconv.FormatInt(info.SizeBytes, 10) + ":" + strconv.FormatInt(info.MTime.UnixNano(), 10) + ":" +
		strconv.FormatUint(h.Sum64(), 16)
}

// Resultados de análisis reutilizables, por archivo y versión (ver media.ResultCache).
var (
	chapterCache = media.NewResultCache[[]media.Chapter](512)
	seekCache    = media.NewResultCache[float64](4096)
)

// openPlaybackRoot comprueba el permiso y abre la carpeta de la biblioteca del archivo con os.Root (revalidada).
func (s *Server) openPlaybackRoot(w http.ResponseWriter, r *http.Request) (*os.Root, store.FileInfo, bool) {
	info, ok := s.authorizePlayback(w, r)
	if !ok {
		return nil, info, false
	}
	return s.openRootFor(w, info)
}

func (s *Server) openRootFor(w http.ResponseWriter, info store.FileInfo) (*os.Root, store.FileInfo, bool) {
	root, err := library.OpenRoot(info.LibPath, s.cfg.MediaRoots)
	if err != nil {
		slog.Warn("biblioteca no disponible para reproducir", "ruta", info.LibPath, "error", err)
		writeError(w, http.StatusServiceUnavailable, "montaje_inaccesible", "La biblioteca no responde ahora. Inténtalo en unos minutos.")
		return nil, info, false
	}
	return root, info, true
}

// openForPlayback comprueba el permiso y abre el fichero confinado a su biblioteca con os.Root.
func (s *Server) openForPlayback(w http.ResponseWriter, r *http.Request) (*os.File, store.FileInfo, bool) {
	info, ok := s.authorizePlayback(w, r)
	if !ok {
		return nil, info, false
	}
	return s.openFileFor(w, info)
}

// openFileFor abre un archivo ya autorizado (authorizePlayback).
func (s *Server) openFileFor(w http.ResponseWriter, info store.FileInfo) (*os.File, store.FileInfo, bool) {
	if d := time.Duration(playbackOpenDelay.Load()); d > 0 {
		time.Sleep(d)
	}
	root, info, ok := s.openRootFor(w, info)
	if !ok {
		return nil, info, false
	}
	defer root.Close()
	f, err := root.Open(info.RelPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "archivo_no_disponible", "Este archivo ya no está en la biblioteca.")
		return nil, info, false
	}
	playbackOpens.Add(1)
	return f, info, true
}

// streamDirect: Direct Play con HTTP Range (sendfile), sin cargar el fichero en memoria.
func (s *Server) streamDirect(w http.ResponseWriter, r *http.Request) {
	f, info, ok := s.openForPlayback(w, r)
	if !ok {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.internal(w, "stream", err)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(info.RelPath)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else if filepath.Ext(info.RelPath) == ".mkv" {
		w.Header().Set("Content-Type", "video/x-matroska")
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	http.ServeContent(s.revocable(w, r, info.ID), r, filepath.Base(info.RelPath), st.ModTime(), f)
}

// streamRemux: Direct Stream. El vídeo se copia tal cual y el audio se copia o se convierte a AAC estéreo; sale en MP4
// fragmentado. Para buscar, el cliente vuelve a pedir con ?t=segundos. FFmpeg muere al cerrarse la conexión.
func (s *Server) streamRemux(w http.ResponseWriter, r *http.Request) {
	f, info, ok := s.openForPlayback(w, r)
	if !ok {
		return
	}
	defer f.Close()
	var probe media.Probe
	if err := json.Unmarshal(info.Probe, &probe); err != nil {
		writeError(w, http.StatusConflict, "sin_analizar", "El archivo todavía no se ha analizado.")
		return
	}
	q := r.URL.Query()
	audio, err := strconv.Atoi(q.Get("audio"))
	if err != nil {
		audio = -1
	}
	plan := media.Plan{AudioIndex: -1}
	if q.Get("container") == "webm" || q.Get("container") == "mkv" {
		plan.OutputContainer = q.Get("container")
	}
	videoCodec := ""
	for _, st := range probe.Streams {
		if st.CodecType == "video" && videoCodec == "" {
			videoCodec = st.CodecName
		}
		if st.CodecType == "audio" && st.Index == audio {
			plan.AudioIndex = audio
			// Se convierte si lo pide el plan o si el códec no cabe en MP4 (nunca se confía solo en el cliente).
			plan.ConvertAudio = q.Get("convert") == "true" || !media.FMP4Audio(st.CodecName)
		}
	}
	if plan.OutputContainer == "webm" {
		if videoCodec != "vp8" && videoCodec != "vp9" && videoCodec != "av1" {
			writeError(w, 422, "no_compatible", "Este vídeo no se puede copiar a WebM.")
			return
		}
		for _, st := range probe.Streams {
			if st.Index == plan.AudioIndex {
				plan.ConvertAudio = q.Get("convert") == "true" || (st.CodecName != "opus" && st.CodecName != "vorbis")
			}
		}
	}
	if plan.OutputContainer == "mkv" {
		plan.ConvertAudio = q.Get("convert") == "true"
	}
	if videoCodec == "" || (plan.OutputContainer != "webm" && plan.OutputContainer != "mkv" && !media.FMP4Video(videoCodec)) {
		writeError(w, http.StatusUnprocessableEntity, "no_compatible", "Este vídeo necesita conversión, que llegará en la etapa E2.")
		return
	}
	start := 0.0
	if v := q.Get("t"); v != "" {
		var err error
		start, err = strconv.ParseFloat(v, 64)
		// NaN e infinitos no llegan nunca a los argumentos de FFmpeg (NaN supera cualquier comparación de rango).
		if err != nil || math.IsNaN(start) || math.IsInf(start, 0) {
			writeError(w, http.StatusBadRequest, "parametro", "La posición de inicio no es válida.")
			return
		}
		if start < 0 || start > float64(probe.DurationMS())/1000 {
			start = 0
		}
	}
	// Un solo flujo optimizado por sesión: un salto o un cambio de audio cierra el anterior (y su FFmpeg) antes de
	// ocupar hueco, así cambiar de pista varias veces seguidas no acumula procesos ni agota los huecos.
	ctx, done := s.remuxSessions.take(r.Context(), authz.FromContext(r.Context()).SessionID)
	defer done()
	if !s.remux.Acquire(ctx, 3*time.Second) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "servidor_ocupado",
			"El servidor está adaptando otras reproducciones. Inténtalo en unos segundos.")
		return
	}
	defer s.remux.Release()
	w.Header().Set("Content-Type", "video/mp4")
	if plan.OutputContainer == "webm" {
		w.Header().Set("Content-Type", "video/webm")
	}
	if plan.OutputContainer == "mkv" {
		w.Header().Set("Content-Type", "video/x-matroska")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Velora-Method", "direct_stream")
	out := media.IdleWriter{W: s.revocable(w, r, info.ID), RC: http.NewResponseController(w), Idle: remuxIdle}
	if err := media.Remux(ctx, f, media.RemuxArgs(plan, videoCodec, start), out); err != nil {
		slog.Error("error en Direct Stream", "fichero", info.ID, "error", err)
	}
}

// remuxSessions guarda el flujo optimizado vivo de cada sesión para cerrarlo cuando la misma sesión pide otro.
type remuxSessions struct {
	mu sync.Mutex
	m  map[uuid.UUID]*remuxEntry
}

type remuxEntry struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// take cancela el flujo anterior de la sesión y registra el nuevo sin esperar a que el anterior termine de cerrarse
// (lo hace en segundo plano: corta su escritura y mata su FFmpeg). El límite global de huecos sigue mandando, así que
// los cambios rápidos no acumulan procesos. La función devuelta se llama al terminar el flujo.
func (rs *remuxSessions) take(parent context.Context, session uuid.UUID) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	e := &remuxEntry{cancel: cancel, done: make(chan struct{})}
	rs.mu.Lock()
	if rs.m == nil {
		rs.m = map[uuid.UUID]*remuxEntry{}
	}
	prev := rs.m[session]
	rs.m[session] = e
	rs.mu.Unlock()
	if prev != nil {
		prev.cancel()
	}
	return ctx, func() {
		cancel()
		close(e.done)
		rs.mu.Lock()
		if rs.m[session] == e {
			delete(rs.m, session)
		}
		rs.mu.Unlock()
	}
}

// active devuelve cuántas sesiones tienen un flujo optimizado vivo (pruebas).
func (rs *remuxSessions) active() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.m)
}

// seekPoint: dónde empezará de verdad un flujo optimizado pedido en ?t= (el fotograma clave anterior). El reproductor lo
// usa como origen del tiempo para que la posición y los subtítulos coincidan con la imagen.
func (s *Server) seekPoint(w http.ResponseWriter, r *http.Request) {
	t, err := strconv.ParseFloat(r.URL.Query().Get("t"), 64)
	if err != nil || math.IsNaN(t) || math.IsInf(t, 0) || t < 0 {
		writeError(w, http.StatusBadRequest, "parametro", "La posición no es válida.")
		return
	}
	info, ok := s.authorizePlayback(w, r)
	if !ok {
		return
	}
	// La misma pista de audio que llevará el flujo (?audio=, como en /remux); si no es una pista de audio del archivo,
	// se mide solo con el vídeo.
	audio := -1
	if a, err := strconv.Atoi(r.URL.Query().Get("audio")); err == nil {
		var p media.Probe
		if json.Unmarshal(info.Probe, &p) == nil {
			for _, st := range p.Streams {
				if st.CodecType == "audio" && st.Index == a {
					audio = a
				}
			}
		}
	}
	// El mismo salto (archivo, versión, milisegundo pedido y pista) cae siempre en el mismo punto: se reutiliza sin abrir
	// el archivo. Es lo habitual al empezar desde el principio, al reanudar y al reabrir tras un corte.
	key := fileVersion(info) + "|" + strconv.FormatInt(int64(math.Round(t*1000)), 10) + "|" + strconv.Itoa(audio)
	if start, ok := seekCache.Get(key); ok {
		writeJSON(w, http.StatusOK, map[string]float64{"start": start})
		return
	}
	// Límites: 10 s en total (incluidas la espera de hueco y la apertura) y 2 cálculos a la vez. FFmpeg muere con el
	// contexto: si el cliente cancela el salto (otro salto encima), no queda ningún proceso vivo.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	select {
	case s.seekSlots <- struct{}{}:
		defer func() { <-s.seekSlots }()
	case <-ctx.Done():
		writeJSON(w, http.StatusOK, map[string]float64{"start": t}) // sin hueco a tiempo: el reproductor usa t
		return
	}
	f, _, ok := s.openFileFor(w, info)
	if !ok {
		return
	}
	defer f.Close()
	start, err := media.SeekOrigin(ctx, f, t, audio)
	if err != nil {
		slog.Warn("no se pudo medir el origen del flujo tras el salto; se usa la posición pedida", "error", err)
	} else {
		seekCache.Put(key, start)
	}
	writeJSON(w, http.StatusOK, map[string]float64{"start": start})
}

// chapters: capítulos del archivo para la barra del reproductor y «Saltar intro/créditos» (solo si el archivo trae un
// capítulo con ese título; la detección automática es de una etapa posterior).
func (s *Server) chapters(w http.ResponseWriter, r *http.Request) {
	info, ok := s.authorizePlayback(w, r)
	if !ok {
		return
	}
	// Los capítulos del archivo no cambian mientras no cambie el archivo: se leen una vez por versión. Las marcas
	// detectadas (abajo) sí se consultan siempre, porque el análisis de intros puede añadirlas después.
	key := fileVersion(info)
	cached, hit := chapterCache.Get(key)
	list := append([]media.Chapter{}, cached...)
	if !hit {
		f, _, ok := s.openFileFor(w, info)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		read, err := media.RunChapters(ctx, f)
		cancel()
		f.Close()
		if err != nil {
			slog.Warn("no se pudieron leer los capítulos", "error", err)
			read = []media.Chapter{}
		} else {
			chapterCache.Put(key, read)
		}
		list = append(list, read...)
	}
	// E2: la intro y los créditos detectados, salvo que el propio archivo ya los marque en sus capítulos (mandan esos).
	if markers, err := s.store.Markers(r.Context(), info.ID); err == nil {
		has := map[string]bool{}
		for _, c := range list {
			has[c.Kind] = true
		}
		for _, m := range markers {
			if !has[m.Kind] {
				title := map[string]string{"intro": "Introducción", "credits": "Créditos"}[m.Kind]
				list = append(list, media.Chapter{Start: float64(m.StartMS) / 1000, End: float64(m.EndMS) / 1000, Title: title, Kind: m.Kind, Detected: true})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapters": list})
}
