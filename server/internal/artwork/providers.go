package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"velora/internal/store"
)

type Image struct {
	FilePath string  `json:"file_path"`
	Provider string  `json:"provider"`
	Kind     string  `json:"kind"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Language string  `json:"language,omitempty"`
	Vote     float64 `json:"vote,omitempty"`
}
type Target struct {
	Kind   string
	IDs    map[string]string
	Season int
}
type Service struct {
	Store                       *store.Store
	Dir                         string
	HTTP                        *http.Client
	FanartURL, TVDBURL, OMDbURL string
}

func New(st *store.Store, dir string) *Service {
	return &Service{Store: st, Dir: dir, HTTP: &http.Client{Timeout: 12 * time.Second}, FanartURL: "https://webservice.fanart.tv/v3.2", TVDBURL: "https://api4.thetvdb.com/v4", OMDbURL: "https://www.omdbapi.com/"}
}
func (s *Service) request(ctx context.Context, method, endpoint string, body any, headers map[string]string, out any) error {
	var data io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, data)
	if err != nil {
		return errors.New("petición del proveedor no válida")
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := s.HTTP.Do(req)
	if err != nil {
		return errors.New("no se pudo contactar con la fuente")
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("la fuente respondió %d", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return errors.New("respuesta incompleta de la fuente")
	}
	return json.Unmarshal(raw, out)
}
func (s *Service) cached(ctx context.Context, p, key string, c Config, fetch func() ([]Image, error)) ([]Image, error) {
	sum := sha256.Sum256([]byte(c.APIKey + "|" + c.ClientKey + "|" + c.PIN))
	key = fmt.Sprintf("%x:%s", sum[:8], key)
	if s.Store != nil {
		if raw, ok := s.Store.CacheGet(ctx, "artwork-"+p, key); ok {
			var images []Image
			if json.Unmarshal(raw, &images) == nil {
				return images, nil
			}
		}
	}
	images, err := fetch()
	if err == nil && s.Store != nil {
		raw, _ := json.Marshal(images)
		_ = s.Store.CachePut(ctx, "artwork-"+p, key, raw, 24*time.Hour)
	}
	return images, err
}
func (s *Service) Gallery(ctx context.Context, t Target) ([]Image, []string) {
	configs, err := Load(s.Dir)
	if err != nil {
		return []Image{}, []string{"No se pudo leer la configuración de imágenes."}
	}
	images := []Image{}
	warnings := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range Providers {
		c := configs[p]
		if !c.Enabled || c.APIKey == "" {
			continue
		}
		wg.Add(1)
		go func(p string, c Config) {
			defer wg.Done()
			list, err := s.cached(ctx, p, t.Kind+":"+t.IDs["tmdb"]+":"+t.IDs["imdb"]+":"+t.IDs["tvdb"]+":"+strconv.Itoa(t.Season), c, func() ([]Image, error) { return s.images(ctx, p, c, t) })
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				warnings = append(warnings, p+": "+err.Error())
			} else {
				images = append(images, list...)
			}
		}(p, c)
	}
	wg.Wait()
	return images, warnings
}
func (s *Service) images(ctx context.Context, p string, c Config, t Target) ([]Image, error) {
	switch p {
	case "fanart":
		return s.fanart(ctx, c, t)
	case "tvdb":
		return s.tvdb(ctx, c, t)
	case "omdb":
		return s.omdb(ctx, c, t)
	}
	return nil, errors.New("fuente desconocida")
}
func (s *Service) Check(ctx context.Context, p string, c Config) error {
	if !validProvider(p) || strings.TrimSpace(c.APIKey) == "" {
		return errors.New("falta la clave API")
	}
	if p == "tvdb" {
		_, err := s.token(ctx, c)
		return err
	}
	_, err := s.images(ctx, p, c, Target{Kind: "movie", IDs: map[string]string{"tmdb": "550", "imdb": "tt0137523"}})
	return err
}
func number(v json.RawMessage) int {
	var n int
	if json.Unmarshal(v, &n) == nil {
		return n
	}
	var text string
	_ = json.Unmarshal(v, &text)
	n, _ = strconv.Atoi(text)
	return n
}
func (s *Service) fanart(ctx context.Context, c Config, t Target) ([]Image, error) {
	resource, id := "movies", t.IDs["tmdb"]
	if id == "" {
		id = t.IDs["imdb"]
	}
	if t.Kind == "series" || t.Kind == "season" {
		resource, id = "tv", t.IDs["tvdb"]
	}
	if id == "" || t.Kind == "episode" {
		return []Image{}, nil
	}
	raw := map[string]json.RawMessage{}
	headers := map[string]string{"api-key": c.APIKey}
	if c.ClientKey != "" {
		headers["client-key"] = c.ClientKey
	}
	if err := s.request(ctx, "GET", s.FanartURL+"/"+resource+"/"+url.PathEscape(id), nil, headers, &raw); err != nil {
		return nil, err
	}
	kinds := map[string]string{"movieposter": "poster", "tvposter": "poster", "seasonposter": "poster", "moviebackground": "backdrop", "showbackground": "backdrop", "hdmovielogo": "logo", "movielogo": "logo", "hdtvlogo": "logo", "clearlogo": "logo", "hdmovieclearart": "art", "movieart": "art", "hdclearart": "art", "clearart": "art", "moviedisc": "disc", "moviebanner": "banner", "tvbanner": "banner", "seasonbanner": "banner", "moviethumb": "thumb", "tvthumb": "thumb", "seasonthumb": "thumb"}
	result := []Image{}
	for key, kind := range kinds {
		var list []struct {
			URL    string          `json:"url"`
			Lang   string          `json:"lang"`
			Likes  json.RawMessage `json:"likes"`
			Width  json.RawMessage `json:"width"`
			Height json.RawMessage `json:"height"`
			Season string          `json:"season"`
		}
		if json.Unmarshal(raw[key], &list) != nil {
			continue
		}
		for _, im := range list {
			if t.Kind == "season" && im.Season != "" && im.Season != "all" && im.Season != strconv.Itoa(t.Season) {
				continue
			}
			im.URL = NormalizeURL("fanart", im.URL)
			if im.URL == "" {
				continue
			}
			lang := im.Lang
			if lang == "00" {
				lang = ""
			}
			result = append(result, Image{FilePath: im.URL, Provider: "fanart", Kind: kind, Width: number(im.Width), Height: number(im.Height), Language: lang, Vote: float64(number(im.Likes))})
		}
	}
	return result, nil
}

var tokens = struct {
	sync.Mutex
	m map[string]struct {
		value string
		until time.Time
	}
}{m: make(map[string]struct {
	value string
	until time.Time
})}

func (s *Service) token(ctx context.Context, c Config) (string, error) {
	sum := sha256.Sum256([]byte(s.TVDBURL + c.APIKey + c.PIN))
	key := fmt.Sprintf("%x", sum)
	tokens.Lock()
	cached := tokens.m[key]
	tokens.Unlock()
	if cached.until.After(time.Now()) {
		return cached.value, nil
	}
	body := map[string]string{"apikey": c.APIKey}
	if c.PIN != "" {
		body["pin"] = c.PIN
	}
	var r struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := s.request(ctx, "POST", s.TVDBURL+"/login", body, nil, &r); err != nil {
		return "", err
	}
	if r.Data.Token == "" {
		return "", errors.New("TheTVDB no aceptó las credenciales")
	}
	tokens.Lock()
	tokens.m[key] = struct {
		value string
		until time.Time
	}{r.Data.Token, time.Now().Add(20 * 24 * time.Hour)}
	tokens.Unlock()
	return r.Data.Token, nil
}
func (s *Service) tvdb(ctx context.Context, c Config, t Target) ([]Image, error) {
	if t.Kind != "series" && t.Kind != "movie" {
		return []Image{}, nil
	}
	token, err := s.token(ctx, c)
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	id := t.IDs["tvdb"]
	if t.Kind == "movie" {
		id = ""
	} // Los IDs TVDB de series no son IDs de películas.
	if id == "" && t.IDs["imdb"] != "" {
		var r struct {
			Data []struct {
				Series struct {
					ID int `json:"id"`
				} `json:"series"`
				Movie struct {
					ID int `json:"id"`
				} `json:"movie"`
			} `json:"data"`
		}
		if err := s.request(ctx, "GET", s.TVDBURL+"/search/remoteid/"+url.PathEscape(t.IDs["imdb"]), nil, headers, &r); err != nil {
			return nil, err
		}
		for _, v := range r.Data {
			n := v.Series.ID
			if t.Kind == "movie" {
				n = v.Movie.ID
			}
			if n > 0 {
				id = strconv.Itoa(n)
				break
			}
		}
	}
	if id == "" {
		return []Image{}, nil
	}
	// Tipos consultados a la API, sin depender de números de tipo que el proveedor pueda ampliar.
	var types struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := s.request(ctx, "GET", s.TVDBURL+"/artwork/types", nil, headers, &types); err != nil {
		return nil, err
	}
	kinds := map[int]string{}
	for _, v := range types.Data {
		n := strings.ToLower(v.Name)
		switch {
		case strings.Contains(n, "box rear"), strings.Contains(n, "back cover"):
			kinds[v.ID] = "boxrear"
		case strings.Contains(n, "box"):
			kinds[v.ID] = "box"
		case strings.Contains(n, "menu"):
			kinds[v.ID] = "menu"
		case strings.Contains(n, "clearart"):
			kinds[v.ID] = "art"
		case strings.Contains(n, "clearlogo"), strings.Contains(n, "logo"):
			kinds[v.ID] = "logo"
		case strings.Contains(n, "poster"):
			kinds[v.ID] = "poster"
		case strings.Contains(n, "banner"):
			kinds[v.ID] = "banner"
		case strings.Contains(n, "background"), strings.Contains(n, "fanart"):
			kinds[v.ID] = "backdrop"
		case strings.Contains(n, "disc"):
			kinds[v.ID] = "disc"
		case strings.Contains(n, "icon"), strings.Contains(n, "thumbnail"):
			kinds[v.ID] = "thumb"
		}
	}
	resource := "series"
	if t.Kind == "movie" {
		resource = "movies"
	}
	var r struct {
		Data struct {
			Artworks []struct {
				Image    string  `json:"image"`
				Type     int     `json:"type"`
				Width    int     `json:"width"`
				Height   int     `json:"height"`
				Language string  `json:"language"`
				Score    float64 `json:"score"`
			} `json:"artworks"`
		} `json:"data"`
	}
	endpoint := s.TVDBURL + "/" + resource + "/" + id + "/extended"
	if t.Kind == "series" {
		endpoint = s.TVDBURL + "/series/" + id + "/artworks"
	}
	if err := s.request(ctx, "GET", endpoint, nil, headers, &r); err != nil {
		return nil, err
	}
	result := []Image{}
	for _, im := range r.Data.Artworks {
		kind := kinds[im.Type]
		im.Image = NormalizeURL("tvdb", im.Image)
		if kind == "" || im.Image == "" {
			continue
		}
		lang := im.Language
		if lang == "spa" {
			lang = "es"
		}
		if lang == "eng" {
			lang = "en"
		}
		result = append(result, Image{FilePath: im.Image, Provider: "tvdb", Kind: kind, Width: im.Width, Height: im.Height, Language: lang, Vote: im.Score})
	}
	return result, nil
}
func (s *Service) omdb(ctx context.Context, c Config, t Target) ([]Image, error) {
	if t.IDs["imdb"] == "" || t.Kind == "season" || t.Kind == "episode" {
		return []Image{}, nil
	}
	u, _ := url.Parse(s.OMDbURL)
	q := u.Query()
	q.Set("apikey", c.APIKey)
	q.Set("i", t.IDs["imdb"])
	u.RawQuery = q.Encode()
	var r struct {
		Poster   string `json:"Poster"`
		Response string `json:"Response"`
		Error    string `json:"Error"`
	}
	if err := s.request(ctx, "GET", u.String(), nil, nil, &r); err != nil {
		return nil, err
	}
	if r.Response == "False" {
		return nil, errors.New("OMDb rechazó la consulta; revisa la clave y la cuota")
	}
	r.Poster = NormalizeURL("omdb", r.Poster)
	if r.Poster == "" {
		return []Image{}, nil
	}
	return []Image{{FilePath: r.Poster, Provider: "omdb", Kind: "poster"}}, nil
}

// Las referencias antiguas HTTP de un dominio admitido se solicitan siempre mediante HTTPS.
func NormalizeURL(provider, raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "http" || u.Scheme == "" {
		u.Scheme = "https"
	}
	if AllowedURL(provider, u.String()) {
		return u.String()
	}
	return ""
}
func AllowedURL(provider, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	switch provider {
	case "fanart":
		return host == "assets.fanart.tv"
	case "tvdb":
		return host == "artworks.thetvdb.com" || host == "thetvdb.com" || host == "www.thetvdb.com"
	case "omdb":
		return host == "m.media-amazon.com" || host == "ia.media-imdb.com" || host == "images-na.ssl-images-amazon.com"
	}
	return false
}
func (s *Service) Download(ctx context.Context, p, raw string) ([]byte, error) {
	if !AllowedURL(p, raw) {
		return nil, errors.New("URL de imagen no válida")
	}
	client := *s.HTTP
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || !AllowedURL(p, req.URL.String()) {
			return errors.New("redirección de imagen no válida")
		}
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", raw, nil)
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("no se pudo descargar la imagen")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("imagen respondió %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (10<<20)+1))
	if err != nil || len(data) > 10<<20 {
		return nil, errors.New("imagen demasiado grande o incompleta")
	}
	return data, nil
}

// GallerySource verifica una imagen de una sola fuente: una caída ajena no debe bloquear su miniatura.
func (s *Service) GallerySource(ctx context.Context, t Target, provider string) ([]Image, error) {
	if !validProvider(provider) {
		return nil, errors.New("fuente desconocida")
	}
	configs, err := Load(s.Dir)
	if err != nil {
		return nil, err
	}
	c := configs[provider]
	if !c.Enabled || c.APIKey == "" {
		return []Image{}, nil
	}
	key := t.Kind + ":" + t.IDs["tmdb"] + ":" + t.IDs["imdb"] + ":" + t.IDs["tvdb"] + ":" + strconv.Itoa(t.Season)
	return s.cached(ctx, provider, key, c, func() ([]Image, error) { return s.images(ctx, provider, c, t) })
}
