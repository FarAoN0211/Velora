package api

import (
	"context"
	"golang.org/x/text/language"
	"net/http"
)

type playbackSettings struct {
	MaxStreamsUser       int    `json:"max_streams_per_user"`
	MaxStreamsIP         int    `json:"max_streams_per_ip"`
	AudioDynamics        string `json:"audio_dynamics"`
	AudioLanguage        string `json:"audio_language"`
	TranscodingMode      string `json:"transcoding_mode"`
	TranscodingMaxHeight int    `json:"transcoding_max_height"`
	TranscodingBitrate   int    `json:"transcoding_bitrate_kbps"`
}

func defaultPlaybackSettings() playbackSettings {
	return playbackSettings{AudioDynamics: "balanced", AudioLanguage: "es-ES", TranscodingMode: "auto", TranscodingMaxHeight: 1080, TranscodingBitrate: 6000}
}

func (s *Server) playbackPreferences(ctx context.Context) playbackSettings {
	cfg := defaultPlaybackSettings()
	_ = s.store.Setting(ctx, "playback.preferences", &cfg)
	return cfg
}
func (s *Server) getPlaybackSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.playbackPreferences(r.Context())
	hardware, detail := s.transcoder.Hardware()
	writeJSON(w, http.StatusOK, struct {
		playbackSettings
		Hardware bool   `json:"hardware_available"`
		Detail   string `json:"hardware_detail"`
		Active   int    `json:"active_video_transcodes"`
	}{cfg, hardware, detail, s.videoTranscodes.InUse()})
}
func (s *Server) setPlaybackSettings(w http.ResponseWriter, r *http.Request) {
	cfg := s.playbackPreferences(r.Context())
	if !readJSON(w, r, &cfg) {
		return
	}
	if cfg.MaxStreamsUser < 0 || cfg.MaxStreamsUser > 1000 || cfg.MaxStreamsIP < 0 || cfg.MaxStreamsIP > 1000 {
		writeError(w, 400, "limites_reproduccion_invalidos", "Usa de 0 a 1000 reproducciones; 0 significa sin límite.")
		return
	}
	if cfg.AudioDynamics != "off" && cfg.AudioDynamics != "balanced" && cfg.AudioDynamics != "night" {
		writeError(w, 400, "dinamica_invalida", "Elige sonido original, equilibrado o nocturno.")
		return
	}
	if cfg.TranscodingMode != "auto" && cfg.TranscodingMode != "software" && cfg.TranscodingMode != "off" {
		writeError(w, 400, "modo_invalido", "Elige automático, software o desactivado.")
		return
	}
	if cfg.TranscodingMaxHeight < 240 || cfg.TranscodingMaxHeight > 2160 || cfg.TranscodingBitrate < 500 || cfg.TranscodingBitrate > 20000 {
		writeError(w, 400, "limites_invalidos", "Usa resolución de 240 a 2160 y bitrate de 500 a 20000 kb/s.")
		return
	}
	if cfg.AudioLanguage != "" {
		tag, err := language.Parse(cfg.AudioLanguage)
		if err != nil {
			writeError(w, 400, "idioma_invalido", "Usa un idioma válido, por ejemplo es-ES.")
			return
		}
		cfg.AudioLanguage = tag.String()
	}
	if err := s.store.SetSetting(r.Context(), "playback.preferences", cfg); err != nil {
		s.internal(w, "playback/settings", err)
		return
	}
	writeJSON(w, 200, cfg)
}
