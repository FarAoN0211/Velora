// Package opensubtitles uses the supported REST API. Credentials stay on the server.
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const BaseURL = "https://api.opensubtitles.com/api/v1"

type Config struct {
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"api_key"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Languages string `json:"languages"`
}
type Client struct {
	Config Config
	Base   string
	HTTP   *http.Client
}

func Load(dir string) (*Client, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "secrets", "opensubtitles.json"))
	if err != nil {
		return nil, err
	}
	var c Config
	if err = json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return New(c), nil
}
func New(c Config) *Client {
	return &Client{Config: c, Base: BaseURL, HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 4 || r.URL.Host != via[0].URL.Host || r.URL.Scheme != via[0].URL.Scheme {
			return http.ErrUseLastResponse
		}
		return nil
	}}}
}
func Valid(c Config) bool {
	if strings.TrimSpace(c.APIKey) == "" || len(c.APIKey) > 512 || len(c.Password) > 512 || len(c.Username) > 256 {
		return false
	}
	if (c.Username == "") != (c.Password == "") {
		return false
	}
	if len(c.Languages) > 256 {
		return false
	}
	for _, l := range strings.Split(c.Languages, ",") {
		if l != "" && !regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,4})?$`).MatchString(l) {
			return false
		}
	}
	return true
}
func Save(dir string, c Config) error {
	if !Valid(c) {
		return errors.New("invalid configuration")
	}
	base := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
	f, err := os.CreateTemp(base, "opensubtitles-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(base, "opensubtitles.json"))
}
func (c *Client) request(ctx context.Context, method, path, token string, body any, out any) error {
	var in io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		in = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, in)
	if err != nil {
		return err
	}
	req.Header.Set("Api-Key", c.Config.APIKey)
	req.Header.Set("User-Agent", "Velora v0.14.0")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("OpenSubtitles no responde")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("OpenSubtitles: HTTP %d (revisa credenciales o cuota)", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}
func (c *Client) login(ctx context.Context) (string, error) {
	if c.Config.Username == "" {
		return "", nil
	}
	var r struct {
		Token string `json:"token"`
	}
	err := c.request(ctx, "POST", "/login", "", map[string]string{"username": c.Config.Username, "password": c.Config.Password}, &r)
	if err == nil && r.Token == "" {
		err = errors.New("OpenSubtitles no devolvió una sesión")
	}
	return r.Token, err
}
func (c *Client) Check(ctx context.Context) error {
	if c.Config.Username != "" {
		_, err := c.login(ctx)
		return err
	}
	var r any
	return c.request(ctx, "GET", "/infos/formats", "", nil, &r)
}

type Result struct {
	FileID          int    `json:"file_id"`
	Language        string `json:"language"`
	Release         string `json:"release"`
	Forced          bool   `json:"forced"`
	HearingImpaired bool   `json:"hearing_impaired"`
}

func (c *Client) Search(ctx context.Context, q url.Values) ([]Result, error) {
	for name, values := range q {
		for i, value := range values {
			value = strings.ToLower(value)
			if name == "imdb_id" || name == "parent_imdb_id" {
				value = strings.TrimLeft(strings.TrimPrefix(value, "tt"), "0")
			}
			values[i] = value
		}
		q[name] = values
	}
	if c.Config.Languages != "" {
		q.Set("languages", c.Config.Languages)
	}
	q.Set("order_by", "download_count")
	q.Set("order_direction", "desc")
	var r struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				Release         string `json:"release"`
				Forced          bool   `json:"foreign_parts_only"`
				HearingImpaired bool   `json:"hearing_impaired"`
				Files           []struct {
					ID int `json:"file_id"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.request(ctx, "GET", "/subtitles?"+q.Encode(), "", nil, &r); err != nil {
		return nil, err
	}
	out := []Result{}
	seen := map[int]bool{}
	for _, entry := range r.Data {
		a := entry.Attributes
		for _, f := range a.Files {
			if f.ID > 0 && !seen[f.ID] {
				out = append(out, Result{f.ID, a.Language, a.Release, a.Forced, a.HearingImpaired})
				seen[f.ID] = true
			}
			if len(out) >= 40 {
				return out, nil
			}
		}
	}
	return out, nil
}
func (c *Client) Download(ctx context.Context, id int) ([]byte, error) {
	token, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	var result struct {
		Link string `json:"link"`
	}
	if err = c.request(ctx, "POST", "/download", token, map[string]any{"file_id": id, "sub_format": "srt"}, &result); err != nil {
		return nil, err
	}
	u, err := url.Parse(result.Link)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !(u.Hostname() == "opensubtitles.com" || strings.HasSuffix(u.Hostname(), ".opensubtitles.com") || u.Hostname() == "opensubtitles.org" || strings.HasSuffix(u.Hostname(), ".opensubtitles.org")) {
		return nil, errors.New("OpenSubtitles devolvió una dirección no válida")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	// Download links never receive account headers or API keys.
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("No se pudo descargar el subtítulo")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Descarga de OpenSubtitles: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, (10<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > 10<<20 {
		return nil, errors.New("Tamaño de subtítulo no válido")
	}
	return b, nil
}
