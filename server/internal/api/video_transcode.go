package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"velora/internal/authz"
	"velora/internal/media"
)

type transcodeTicket struct {
	File    uuid.UUID  `json:"file"`
	Session uuid.UUID  `json:"session"`
	Version string     `json:"version"`
	Expires int64      `json:"expires"`
	Plan    media.Plan `json:"plan"`
}

func (s *Server) signTranscode(t transcodeTicket) string {
	data, _ := json.Marshal(t)
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte("transcode-v1:"))
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) readTranscode(token string) (transcodeTicket, error) {
	var t transcodeTicket
	if len(token) > 4096 {
		return t, fmt.Errorf("ticket inválido")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return t, fmt.Errorf("ticket inválido")
	}
	data, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return t, e
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return t, e
	}
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte("transcode-v1:"))
	mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return t, fmt.Errorf("firma inválida")
	}
	if e = json.Unmarshal(data, &t); e != nil {
		return t, e
	}
	if time.Now().Unix() > t.Expires || !t.Plan.ConvertVideo || t.Plan.Method != media.Transcode {
		return t, fmt.Errorf("ticket caducado o no válido")
	}
	return t, nil
}

type countedOutput struct {
	io.Writer
	n int64
}

func (w *countedOutput) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	w.n += int64(n)
	return n, e
}

func (s *Server) streamTranscode(w http.ResponseWriter, r *http.Request) {
	info, ok := s.authorizePlayback(w, r)
	if !ok {
		return
	}
	ticket, err := s.readTranscode(r.URL.Query().Get("ticket"))
	if err != nil || ticket.File != info.ID || ticket.Session != authz.FromContext(r.Context()).SessionID || ticket.Version != fileVersion(info) {
		writeError(w, 403, "plan_invalido", "Vuelve a iniciar la reproducción para renovar su plan.")
		return
	}
	cfg := s.playbackPreferences(r.Context())
	if cfg.TranscodingMode == "off" {
		writeError(w, 422, "conversion_desactivada", "La conversión de vídeo está desactivada.")
		return
	}
	start := 0.0
	if v := r.URL.Query().Get("t"); v != "" {
		start, err = strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(start) || math.IsInf(start, 0) || start < 0 {
			writeError(w, 400, "parametro", "La posición no es válida.")
			return
		}
	}
	var probe media.Probe
	if json.Unmarshal(info.Probe, &probe) != nil {
		writeError(w, 409, "sin_analizar", "El archivo no está analizado.")
		return
	}
	if start > float64(probe.DurationMS())/1000 {
		start = 0
	}
	ctx, done := s.remuxSessions.take(r.Context(), authz.FromContext(r.Context()).SessionID)
	defer done()
	if !s.videoTranscodes.Acquire(ctx, 3*time.Second) {
		w.Header().Set("Retry-After", "10")
		writeError(w, 503, "servidor_ocupado", "El servidor ya está adaptando un vídeo. Las reproducciones directas siguen disponibles.")
		return
	}
	defer s.videoTranscodes.Release()
	f, _, ok := s.openFileFor(w, info)
	if !ok {
		return
	}
	defer f.Close()
	encoder := s.transcoder.EncoderFor(cfg.TranscodingMode, ticket.Plan)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Velora-Method", "transcode")
	out := &countedOutput{Writer: media.IdleWriter{W: s.revocable(w, r, info.ID), RC: http.NewResponseController(w), Idle: remuxIdle}}
	if err := media.TranscodeAt(ctx, f, ticket.Plan, media.VideoStream(probe), encoder, start, out, func(mode string) { s.activity.encoder(ticket.Session, info.ID, mode) }); err != nil && ctx.Err() == nil {
		slog.Error("error adaptando vídeo", "fichero", info.ID, "encoder", encoder, "error", err)
		if out.n == 0 {
			writeError(w, 502, "conversion_fallida", "No se ha podido adaptar este vídeo. Prueba otra versión del archivo.")
		}
	}
}
