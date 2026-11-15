package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"velora/internal/authz"
	"velora/internal/media"
	"velora/internal/store"
)

// Live presence is deliberately transient: history is not evidence of an active player.
const playbackPresenceTTL = 45 * time.Second

type playbackPresence struct {
	Session  uuid.UUID `json:"-"`
	User     uuid.UUID `json:"-"`
	File     uuid.UUID `json:"-"`
	Item     uuid.UUID `json:"-"`
	ID       string    `json:"id"`
	State    string    `json:"state"`
	Position int64     `json:"position_ms"`
	Duration int64     `json:"duration_ms"`
	IP       string    `json:"ip"`
	Updated  time.Time `json:"updated_at"`
}
type playbackDecision struct {
	Plan    media.Plan
	Updated time.Time
	Encoder string
}
type playbackActivity struct {
	mu      sync.Mutex
	players map[string]playbackPresence
	plans   map[string]playbackDecision
}

func decisionKey(session, file uuid.UUID) string { return session.String() + ":" + file.String() }
func (a *playbackActivity) remember(session, file uuid.UUID, p media.Plan) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.plans == nil {
		a.plans = make(map[string]playbackDecision)
	}
	for k, v := range a.plans {
		if time.Since(v.Updated) > 8*time.Hour {
			delete(a.plans, k)
		}
	}
	a.plans[decisionKey(session, file)] = playbackDecision{Plan: p, Updated: time.Now()}
}
func (a *playbackActivity) encoder(session, file uuid.UUID, encoder string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := decisionKey(session, file)
	p, ok := a.plans[k]
	if ok {
		p.Encoder = encoder
		a.plans[k] = p
	}
}
func (a *playbackActivity) update(p playbackPresence) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.players == nil {
		a.players = make(map[string]playbackPresence)
	}
	for k, v := range a.players {
		if time.Since(v.Updated) > playbackPresenceTTL {
			delete(a.players, k)
		}
	}
	k := p.Session.String() + ":" + p.ID
	if p.State == "stopped" {
		delete(a.players, k)
		return
	}
	a.players[k] = p
}
func (a *playbackActivity) snapshot(now time.Time) []playbackPresence {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]playbackPresence, 0, len(a.players))
	for k, p := range a.players {
		if now.Sub(p.Updated) > playbackPresenceTTL {
			delete(a.players, k)
		} else {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}
func (s *Server) playbackHeartbeat(w http.ResponseWriter, r *http.Request) {
	who := authz.FromContext(r.Context())
	if !authz.Can(who, authz.ActPlay) {
		writeError(w, 403, "sin_permiso", "Tu perfil no tiene permiso para reproducir.")
		return
	}
	var req struct {
		ID       string    `json:"id"`
		File     uuid.UUID `json:"file_id"`
		State    string    `json:"state"`
		Position int64     `json:"position_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.ID) == 0 || len(req.ID) > 80 || req.Position < 0 || (req.State != "playing" && req.State != "paused" && req.State != "buffering" && req.State != "stopped") {
		writeError(w, 422, "datos_no_validos", "Estado de reproducción no válido.")
		return
	}
	f, item, err := s.store.FileForPlayback(r.Context(), viewer(r), req.File)
	if err != nil || f.Missing {
		writeError(w, 404, "no_encontrado", "No existe este archivo.")
		return
	}
	var probe media.Probe
	_ = json.Unmarshal(f.Probe, &probe)
	duration := probe.DurationMS()
	if duration > 0 && req.Position > duration {
		req.Position = duration
	}
	// Mientras el reproductor sigue abierto (reproduciendo, en pausa o cargando) el perfil nunca se bloquea.
	if req.State != "stopped" {
		if err := s.store.TouchSession(r.Context(), who.SessionID, true); err != nil {
			s.internal(w, "playback/heartbeat", err)
			return
		}
	}
	leaseID, err := s.store.PlaybackHeartbeatID(r.Context(), who.SessionID, req.File, req.ID, playbackLeaseID("", req.File))
	if err != nil {
		s.internal(w, "playback/heartbeat", err)
		return
	}
	if req.State == "stopped" {
		if err = s.store.ReleasePlayback(r.Context(), who.SessionID, req.File, leaseID); err != nil {
			s.internal(w, "playback/stop", err)
			return
		}
	} else if !s.reservePlayback(w, r, req.File, leaseID) {
		return
	}
	ip := s.clientIP(r)
	s.activity.update(playbackPresence{Session: who.SessionID, User: who.UserID, File: req.File, Item: item, ID: req.ID, State: req.State, Position: req.Position, Duration: duration, IP: ip, Updated: time.Now()})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) nowPlaying(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		playbackPresence
		Username     string         `json:"username"`
		Device       string         `json:"device"`
		Client       string         `json:"client"`
		Content      store.ItemCard `json:"item"`
		Method       media.Method   `json:"method"`
		Video        string         `json:"video"`
		Audio        string         `json:"audio"`
		ConvertVideo bool           `json:"convert_video"`
		ConvertAudio bool           `json:"convert_audio"`
		Encoder      string         `json:"encoder,omitempty"`
	}
	out := make([]entry, 0)
	for _, p := range s.activity.snapshot(time.Now()) {
		valid, err := s.store.SessionStillValid(r.Context(), p.Session)
		if err != nil || !valid {
			continue
		}
		u, err := s.store.UserByID(r.Context(), p.User)
		if err != nil || u.IsDisabled || !u.CanPlay {
			continue
		}
		v := store.Viewer{UserID: p.User, Admin: u.IsAdmin}
		f, _, err := s.store.FileForPlayback(r.Context(), v, p.File)
		if err != nil || f.Missing {
			continue
		}
		item, err := s.store.Item(r.Context(), v, p.Item)
		if err != nil {
			continue
		}
		e := entry{playbackPresence: p, Username: u.DisplayName, Content: item.ItemCard}
		if e.Username == "" {
			e.Username = u.Username
		}
		_ = s.store.Pool.QueryRow(r.Context(), `select coalesce(d.name,'Navegador'),coalesce(d.client_kind,'web') from sessions s left join devices d on d.id=s.device_id where s.id=$1`, p.Session).Scan(&e.Device, &e.Client)
		s.activity.mu.Lock()
		decision := s.activity.plans[decisionKey(p.Session, p.File)]
		s.activity.mu.Unlock()
		e.Method = decision.Plan.Method
		e.ConvertVideo = decision.Plan.ConvertVideo
		e.ConvertAudio = decision.Plan.ConvertAudio
		e.Encoder = decision.Encoder
		var probe media.Probe
		_ = json.Unmarshal(f.Probe, &probe)
		for _, st := range probe.Streams {
			if st.CodecType == "video" && e.Video == "" {
				e.Video = strings.ToUpper(st.CodecName)
				if st.Height > 0 {
					e.Video = fmt.Sprintf("%dp · %s", st.Height, e.Video)
				}
			}
			if st.CodecType == "audio" && st.Index == decision.Plan.AudioIndex {
				e.Audio = strings.ToUpper(st.CodecName)
				if st.Tags["language"] != "" {
					e.Audio = st.Tags["language"] + " · " + e.Audio
				}
				if st.Channels > 0 {
					e.Audio += fmt.Sprintf(" · %d canales", st.Channels)
				}
			}
		}
		if e.ConvertVideo {
			if decision.Plan.VideoCodec == "hevc" {
				e.Video += " → HEVC 10 bits HDR10"
			} else {
				e.Video += " → H.264"
			}
		}
		if e.ConvertAudio {
			e.Audio += " → " + strings.ToUpper(decision.Plan.AudioCodec) + " estéreo"
		}
		out = append(out, e)
	}
	writeJSON(w, 200, map[string]any{"items": out})
}
