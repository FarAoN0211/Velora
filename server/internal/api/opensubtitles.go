package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"velora/internal/media"
	"velora/internal/opensubtitles"
	"velora/internal/store"
)

func (s *Server) openSubtitlesStatus(w http.ResponseWriter, r *http.Request) {
	c, err := opensubtitles.Load(s.cfg.ConfigDir)
	if err != nil {
		writeJSON(w, 200, map[string]any{"configured": false, "enabled": false, "username": "", "languages": ""})
		return
	}
	writeJSON(w, 200, map[string]any{"configured": true, "enabled": c.Config.Enabled, "username": c.Config.Username, "languages": c.Config.Languages})
}
func (s *Server) openSubtitlesSave(w http.ResponseWriter, r *http.Request) {
	var cfg opensubtitles.Config
	if !readJSON(w, r, &cfg) {
		return
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Languages = strings.ToLower(strings.ReplaceAll(cfg.Languages, " ", ""))
	if old, err := opensubtitles.Load(s.cfg.ConfigDir); err == nil {
		if cfg.APIKey == "" {
			cfg.APIKey = old.Config.APIKey
		}
		if cfg.Password == "" && cfg.Username == old.Config.Username {
			cfg.Password = old.Config.Password
		}
	}
	if !opensubtitles.Valid(cfg) {
		writeError(w, 422, "datos_no_validos", "Indica una clave API, y usuario y contraseña juntos si usas una cuenta. Los idiomas usan códigos separados por comas.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := func() error {
		if !cfg.Enabled {
			return nil
		}
		return opensubtitles.New(cfg).Check(ctx)
	}(); err != nil {
		writeError(w, 422, "proveedor_no_disponible", err.Error())
		return
	}
	if err := opensubtitles.Save(s.cfg.ConfigDir, cfg); err != nil {
		s.internal(w, "opensubtitles/save", err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) openSubtitlesDelete(w http.ResponseWriter, r *http.Request) {
	err := os.Remove(filepath.Join(s.cfg.ConfigDir, "secrets", "opensubtitles.json"))
	if err != nil && !os.IsNotExist(err) {
		s.internal(w, "opensubtitles/delete", err)
		return
	}
	w.WriteHeader(204)
}

// Identity search avoids hashing/reading the remote movie and competing with playback.
func (s *Server) onlineSubtitles(r *http.Request, info store.FileInfo) ([]media.SubTrack, error) {
	client, err := opensubtitles.Load(s.cfg.ConfigDir)
	if err != nil || !client.Config.Enabled || info.Extra {
		return nil, nil
	}
	_, itemID, err := s.store.FileForPlayback(r.Context(), viewer(r), info.ID)
	if err != nil {
		return nil, err
	}
	item, err := s.store.Item(r.Context(), viewer(r), itemID)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if item.Kind == "episode" {
		if item.SeasonNumber == nil || item.EpisodeNumber == nil {
			return nil, nil
		}
		q.Set("type", "episode")
		q.Set("season_number", strconv.Itoa(*item.SeasonNumber))
		q.Set("episode_number", strconv.Itoa(*item.EpisodeNumber))
		if item.ProviderIDs["imdb"] != "" {
			q.Set("imdb_id", strings.TrimPrefix(item.ProviderIDs["imdb"], "tt"))
		} else if item.SeriesID != nil {
			series, e := s.store.Item(r.Context(), viewer(r), *item.SeriesID)
			if e != nil {
				return nil, e
			}
			if series.ProviderIDs["imdb"] != "" {
				q.Set("parent_imdb_id", strings.TrimPrefix(series.ProviderIDs["imdb"], "tt"))
			} else {
				q.Set("query", series.Title)
			}
		} else {
			return nil, nil
		}
	} else if item.Kind == "movie" {
		q.Set("type", "movie")
		if item.ProviderIDs["imdb"] != "" {
			q.Set("imdb_id", strings.TrimPrefix(item.ProviderIDs["imdb"], "tt"))
		} else {
			q.Set("query", item.Title)
			if item.Year != nil {
				q.Set("year", fmt.Sprint(*item.Year))
			}
		}
	} else {
		return nil, nil
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(q.Encode()+":"+client.Config.Languages)))
	var results []opensubtitles.Result
	if cached, ok := s.store.CacheGet(r.Context(), "opensubtitles", key); ok {
		_ = json.Unmarshal(cached, &results)
	} else {
		results, err = client.Search(r.Context(), q)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(results)
		_ = s.store.CachePut(r.Context(), "opensubtitles", key, raw, 6*time.Hour)
	}
	out := []media.SubTrack{}
	for _, result := range results {
		track := media.SubTrack{ID: "o" + strconv.Itoa(result.FileID), Language: result.Language, Title: result.Release, Codec: "srt", Forced: result.Forced, HearingImpaired: result.HearingImpaired, External: true, Supported: true, Provider: "OpenSubtitles"}
		media.LabelSubtitle(&track)
		out = append(out, track)
	}
	return out, nil
}
func (s *Server) onlineSubtitleVTT(w http.ResponseWriter, r *http.Request, info store.FileInfo, sid string) {
	tracks, err := s.onlineSubtitles(r, info)
	if err != nil {
		writeError(w, 503, "proveedor_no_disponible", "OpenSubtitles no responde o ha agotado su cuota.")
		return
	}
	valid := false
	for _, t := range tracks {
		if t.ID == sid {
			valid = true
			break
		}
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	key := fmt.Sprintf("%s-%s-%d-%d", info.ID, sid, info.SizeBytes, info.MTime.UnixNano())
	cache := filepath.Join(s.cfg.ConfigDir, "cache", "subtitles-online")
	target := filepath.Join(cache, key+".vtt")
	if b, err := os.ReadFile(target); err == nil {
		serveVTT(w, b)
		return
	}
	result, err, _ := presentationRequests.Do("subtitle:"+key, func() (any, error) {
		if b, e := os.ReadFile(target); e == nil {
			return b, nil
		}
		c, e := opensubtitles.Load(s.cfg.ConfigDir)
		if e != nil {
			return nil, e
		}
		id, e := strconv.Atoi(strings.TrimPrefix(sid, "o"))
		if e != nil {
			return nil, e
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		raw, e := c.Download(ctx, id)
		if e != nil {
			return nil, e
		}
		if e = os.MkdirAll(cache, 0750); e != nil {
			return nil, e
		}
		tmp, e := os.CreateTemp(cache, "sub-*.srt")
		if e != nil {
			return nil, e
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if _, e = tmp.Write(raw); e != nil {
			return nil, e
		}
		if _, e = tmp.Seek(0, 0); e != nil {
			return nil, e
		}
		vtt, e := media.ConvertSubtitle(ctx, tmp, media.ExternalSubArgs("srt"))
		if e != nil {
			return nil, e
		}
		output, e := os.CreateTemp(cache, "vtt-*")
		if e != nil {
			return nil, e
		}
		defer os.Remove(output.Name())
		if _, e = output.Write(vtt); e != nil {
			output.Close()
			return nil, e
		}
		if e = output.Close(); e != nil {
			return nil, e
		}
		if e = os.Rename(output.Name(), target); e != nil {
			return nil, e
		}
		return vtt, nil
	})
	if err != nil {
		writeError(w, 422, "subtitulo_fallido", err.Error())
		return
	}
	// Playback access is rechecked after a potentially slow download.
	if _, _, e := s.store.FileForPlayback(r.Context(), viewer(r), info.ID); e != nil {
		http.NotFound(w, r)
		return
	}
	serveVTT(w, result.([]byte))
}
