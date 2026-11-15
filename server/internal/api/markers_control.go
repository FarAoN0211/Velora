package api

import (
	"errors"
	"net/http"
	"velora/internal/jobs"
	"velora/internal/markers"
)

func (s *Server) markerScanStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.MarkerStatus(r.Context(), markers.Version)
	if err != nil {
		s.internal(w, "markers/status", err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) markerScanStart(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetSetting(r.Context(), "markers.paused", false); err != nil {
		s.internal(w, "markers/start", err)
		return
	}
	if _, err := s.queue.Enqueue(r.Context(), "markers_detect", map[string]bool{"manual": true}, "markers"); err != nil && !errors.Is(err, jobs.ErrDuplicate) {
		s.internal(w, "markers/enqueue", err)
		return
	}
	s.markerScanStatus(w, r)
}
func (s *Server) markerScanStop(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetSetting(r.Context(), "markers.paused", true); err != nil {
		s.internal(w, "markers/stop", err)
		return
	}
	if _, err := s.store.Pool.Exec(r.Context(), `update jobs set state='cancelled',finished_at=now() where kind='markers_detect' and state='queued'`); err != nil {
		s.internal(w, "markers/cancel", err)
		return
	}
	s.markerScanStatus(w, r)
}
