package api

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/netip"
	"regexp"
	"velora/internal/authz"
	"velora/internal/store"
)

var playbackIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func validPlaybackID(id string) bool { return id == "" || playbackIDPattern.MatchString(id) }
func playbackLeaseID(id string, file uuid.UUID) string {
	if id == "" {
		return "legacy-" + file.String()
	}
	return id
}
func streamingPlaybackRequest(r *http.Request, file uuid.UUID) bool {
	base := "/api/v1/stream/files/" + file.String()
	return r.URL.Path == base || r.URL.Path == base+"/remux" || r.URL.Path == base+"/transcode"
}
func (s *Server) admitPlayback(ctx context.Context, r *http.Request, file uuid.UUID, id string) error {
	who := authz.FromContext(r.Context())
	cfg := s.playbackPreferences(ctx)
	ip := s.clientIP(r)
	if a, err := netip.ParseAddr(ip); err == nil {
		ip = a.Unmap().String()
	}
	return s.store.ReservePlayback(ctx, who.SessionID, who.UserID, file, playbackLeaseID(id, file), ip, cfg.MaxStreamsUser, cfg.MaxStreamsIP)
}
func (s *Server) reservePlayback(w http.ResponseWriter, r *http.Request, file uuid.UUID, id string) bool {
	if !validPlaybackID(id) {
		writeError(w, 422, "reproductor_invalido", "Identificador de reproductor no válido.")
		return false
	}
	err := s.admitPlayback(r.Context(), r, file, id)
	if errors.Is(err, store.ErrPlaybackUserLimit) || errors.Is(err, store.ErrPlaybackIPLimit) {
		reason := "tu cuenta"
		if errors.Is(err, store.ErrPlaybackIPLimit) {
			reason = "esta dirección IP"
		}
		w.Header().Set("Retry-After", "5")
		writeError(w, 429, "limite_reproducciones", "Se ha alcanzado el límite de reproducciones simultáneas para "+reason+". Detén otra reproducción e inténtalo de nuevo.")
		return false
	}
	if err != nil {
		s.internal(w, "playback/admission", err)
		return false
	}
	return true
}
