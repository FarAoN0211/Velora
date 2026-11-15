// Package metadata enriquece las películas con TMDB (opcional; VELORA funciona sin clave con los nombres de carpeta).
//
// Términos de TMDB (architecture.md §13): uso no comercial con atribución visible, caché de 6 meses como máximo
// (aquí 30 días) y clave propia de cada instalación, guardada en /config/secrets/tmdb.key (0600), nunca en la base.
package metadata

import (
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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"velora/internal/imaging"
	"velora/internal/library"
	"velora/internal/store"
)

const cacheTTL = 30 * 24 * time.Hour

// ErrNotConfigured indica que no hay clave de TMDB.
var ErrNotConfigured = errors.New("TMDB no está configurado")

// TMDB es el cliente.
type TMDB struct {
	BaseURL   string // https://api.themoviedb.org
	ImageBase string // https://image.tmdb.org/t/p
	ConfigDir string
	Store     *store.Store
	HTTP      *http.Client
	Language  string
	throttle  <-chan time.Time
}

// New crea el cliente con un ritmo prudente (4 peticiones por segundo).
func New(baseURL, imageBase, configDir string, st *store.Store) *TMDB {
	return &TMDB{BaseURL: strings.TrimRight(baseURL, "/"), ImageBase: strings.TrimRight(imageBase, "/"), ConfigDir: configDir,
		Store: st, HTTP: &http.Client{Timeout: 20 * time.Second}, Language: "es-ES", throttle: time.Tick(250 * time.Millisecond)}
}

func (t *TMDB) keyPath() string { return filepath.Join(t.ConfigDir, "secrets", "tmdb.key") }

func (t *TMDB) key() (string, error) {
	raw, err := os.ReadFile(t.keyPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotConfigured
	}
	return strings.TrimSpace(string(raw)), err
}

// Configured indica si hay clave.
func (t *TMDB) Configured() bool { _, err := t.key(); return err == nil }

// SaveKey comprueba la clave contra TMDB y la guarda (0600). Acepta la API key (v3) o el token de lectura (v4).
func (t *TMDB) SaveKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 512 || strings.ContainsAny(key, " \n\r\t") {
		return errors.New("la clave no tiene un formato válido")
	}
	if _, err := t.get(ctx, key, "/3/configuration", nil); err != nil {
		return fmt.Errorf("TMDB ha rechazado la clave: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(t.keyPath()), 0o700); err != nil {
		return err
	}
	tmp := t.keyPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(key), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.keyPath())
}

// DeleteKey quita la clave.
func (t *TMDB) DeleteKey() error {
	if err := os.Remove(t.keyPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ErrProviderNotFound: TMDB no tiene esa ficha.
var ErrProviderNotFound = errors.New("TMDB respondió 404")

// errNotFound es el nombre interno de ErrProviderNotFound.
var errNotFound = ErrProviderNotFound

// ErrProviderInvalid: TMDB ha respondido, pero no con la ficha pedida (otro ID o sin título). Nunca se aplica ni se guarda en caché.
var ErrProviderInvalid = errors.New("TMDB ha devuelto una ficha no válida")

func (t *TMDB) get(ctx context.Context, key, path string, q url.Values) ([]byte, error) {
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if strings.Count(key, ".") == 2 && len(key) > 100 { // token de lectura v4 (JWT)
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		q.Set("api_key", key)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")
	if t.throttle != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.throttle:
		}
	}
	res, err := t.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("no se pudo contactar con TMDB") // sin la URL: llevaría la clave
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TMDB respondió %d", res.StatusCode)
	}
	return body, nil
}

func (t *TMDB) cachedGet(ctx context.Context, key, cacheKey, path string, q url.Values) ([]byte, error) {
	if raw, ok := t.Store.CacheGet(ctx, "tmdb", cacheKey); ok {
		return raw, nil
	}
	raw, err := t.get(ctx, key, path, q)
	if err != nil {
		return nil, err
	}
	_ = t.Store.CachePut(ctx, "tmdb", cacheKey, raw, cacheTTL)
	return raw, nil
}

// detailsGet pide una ficha y comprueba que es la pedida y que tiene título. Una respuesta incompleta no se guarda en
// caché, y una ya guardada se vuelve a pedir: un fallo puntual de TMDB no debe durar 30 días.
func (t *TMDB) detailsGet(ctx context.Context, key, cacheKey, path, kind, id string, q url.Values) ([]byte, error) {
	if raw, ok := t.Store.CacheGet(ctx, "tmdb", cacheKey); ok && validDetails(raw, kind, id) {
		return raw, nil
	}
	raw, err := t.get(ctx, key, path, q)
	if err != nil {
		return nil, err
	}
	if !validDetails(raw, kind, id) {
		return nil, ErrProviderInvalid
	}
	_ = t.Store.CachePut(ctx, "tmdb", cacheKey, raw, cacheTTL)
	return raw, nil
}

// validDetails exige el título en el campo que se va a leer: «name» en las series y «title» en las películas.
func validDetails(raw []byte, kind, id string) bool {
	var d struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.ID <= 0 || strconv.Itoa(d.ID) != id {
		return false
	}
	if kind == "series" {
		return strings.TrimSpace(d.Name) != ""
	}
	return strings.TrimSpace(d.Title) != ""
}

type presentationPerson struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character,omitempty"`
	Job         string `json:"job,omitempty"`
	ProfilePath string `json:"profile_path,omitempty"`
}
type presentationFields struct {
	Collection *store.MovieCollection `json:"belongs_to_collection"`
	Credits    struct {
		Cast []presentationPerson `json:"cast"`
		Crew []presentationPerson `json:"crew"`
	} `json:"credits"`
	Companies []struct {
		Name string `json:"name"`
	} `json:"production_companies"`
	Countries []struct {
		Name string `json:"name"`
	} `json:"production_countries"`
	Status           string `json:"status"`
	OriginalLanguage string `json:"original_language"`
}

func (p presentationFields) payload() json.RawMessage {
	// Limit the UI payload without exposing the provider response wholesale.
	if len(p.Credits.Cast) > 30 {
		p.Credits.Cast = p.Credits.Cast[:30]
	}
	crew := make([]presentationPerson, 0)
	for _, v := range p.Credits.Crew {
		if v.Job == "Director" || v.Job == "Writer" || v.Job == "Screenplay" || v.Job == "Creator" || v.Job == "Original Music Composer" {
			crew = append(crew, v)
		}
	}
	if len(crew) > 30 {
		crew = crew[:30]
	}
	p.Credits.Crew = crew
	raw, _ := json.Marshal(p)
	return raw
}

type movieDetails struct {
	presentationFields
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	Tagline       string  `json:"tagline"`
	ReleaseDate   string  `json:"release_date"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
	ImdbID        string  `json:"imdb_id"`
	TVDBID        int     `json:"-"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

// Calificaciones españolas (ICAA) normalizadas a edad (architecture.md §6).
var esRatingAge = map[string]int{"TP": 0, "A": 0, "APTA": 0, "7": 7, "12": 12, "13": 13, "16": 16, "18": 18, "X": 18}

// Enrich busca la película y aplica sus datos e imágenes. Devuelve false si TMDB no la reconoce.
func (t *TMDB) Enrich(ctx context.Context, it store.PendingMetadata) (bool, error) {
	return t.enrich(ctx, it, false)
}

// Refresh vuelve a pedir la ficha de una película o serie (I1, «Actualizar información»), respetando siempre los bloqueos.
// onlyMissing («Completar lo que falta»): solo rellena campos vacíos y solo descarga las imágenes que falten; si no,
// («Volver a descargar todo») sustituye los campos no bloqueados y las imágenes de TMDB (las locales y las elegidas a
// mano nunca se tocan: son otras filas, con prioridad al mostrarse).
func (t *TMDB) Refresh(ctx context.Context, it store.PendingMetadata, onlyMissing bool) (bool, error) {
	return t.enrich(ctx, it, onlyMissing)
}

func (t *TMDB) enrich(ctx context.Context, it store.PendingMetadata, onlyMissing bool) (bool, error) {
	key, err := t.key()
	if err != nil {
		return false, err
	}
	expected := it.ProviderIDs["tmdb"] // la identidad al empezar: si cambia mientras tanto, esta actualización ya no vale
	id := expected
	if id == "" {
		endpoint, yearParam := "/3/search/movie", "year"
		cachePrefix := "search:"
		if it.Kind == "series" {
			endpoint, yearParam, cachePrefix = "/3/search/tv", "first_air_date_year", "searchtv:"
		}
		q := url.Values{"query": {it.Title}, "language": {t.Language}, "include_adult": {"false"}}
		if it.Year != nil {
			q.Set(yearParam, strconv.Itoa(int(*it.Year)))
		}
		raw, err := t.cachedGet(ctx, key, cachePrefix+q.Encode(), endpoint, q)
		if err != nil {
			return false, err
		}
		var sr struct {
			Results []struct {
				ID int `json:"id"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &sr); err != nil || len(sr.Results) == 0 || sr.Results[0].ID <= 0 {
			return false, nil
		}
		id = strconv.Itoa(sr.Results[0].ID)
	}
	m, err := t.detailsFor(ctx, key, it.Kind, id)
	if err != nil {
		return false, err
	}
	f := m.fields()
	applied, err := t.Store.ApplyMetadataIfIdentity(ctx, it.ID, f, library.SortTitle(f.Title), onlyMissing, expected)
	if err != nil {
		return false, err
	}
	if !applied { // un administrador la ha identificado mientras tanto: su elección manda y aquí no se escribe nada
		return true, nil
	}
	for kind, p := range map[string]string{"poster": m.PosterPath, "backdrop": m.BackdropPath} {
		if p == "" || (onlyMissing && t.Store.HasImage(ctx, it.ID, kind, "tmdb")) {
			continue
		}
		size := "w500"
		if kind == "backdrop" {
			size = "w1280"
		}
		rel, err := t.download(ctx, it.ID, kind, size, p)
		if err != nil {
			continue
		}
		published, old, err := t.Store.SetProviderImage(ctx, it.ID, kind, rel, id)
		if err != nil || !published {
			t.RemoveImageFiles(ctx, it.ID, rel) // la ficha ha cambiado de identidad: esta imagen ya no le corresponde
			continue
		}
		if old != "" {
			t.RemoveImageFiles(ctx, it.ID, old)
		}
	}
	logoItem := it
	logoItem.ProviderIDs = map[string]string{"tmdb": id}
	_, _ = t.EnsureLogo(ctx, logoItem)
	return true, nil
}

func (t *TMDB) details(ctx context.Context, key, id string) (movieDetails, error) {
	var m movieDetails
	q := url.Values{"language": {t.Language}, "append_to_response": {"release_dates,credits"}}
	raw, err := t.detailsGet(ctx, key, "movie:"+id+":"+t.Language+":presentation-v1", "/3/movie/"+url.PathEscape(id), "movie", id, q)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(raw, &m)
	return m, err
}

// detailsFor pide la ficha según el tipo de elemento: las series tienen otro formato en TMDB y se convierten al común.
func (t *TMDB) detailsFor(ctx context.Context, key, kind, id string) (movieDetails, error) {
	if kind != "series" {
		return t.details(ctx, key, id)
	}
	var d tvDetails
	q := url.Values{"language": {t.Language}, "append_to_response": {"external_ids,content_ratings,credits"}}
	raw, err := t.detailsGet(ctx, key, "tv:"+id+":"+t.Language+":presentation-v1", "/3/tv/"+url.PathEscape(id), "series", id, q)
	if err != nil {
		return movieDetails{}, err
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return movieDetails{}, err
	}
	return d.common(), nil
}

// tvDetails es la ficha de una serie en TMDB.
type tvDetails struct {
	presentationFields
	ID             int     `json:"id"`
	Name           string  `json:"name"`
	OriginalName   string  `json:"original_name"`
	Overview       string  `json:"overview"`
	Tagline        string  `json:"tagline"`
	FirstAirDate   string  `json:"first_air_date"`
	EpisodeRunTime []int   `json:"episode_run_time"`
	VoteAverage    float64 `json:"vote_average"`
	PosterPath     string  `json:"poster_path"`
	BackdropPath   string  `json:"backdrop_path"`
	Genres         []struct {
		Name string `json:"name"`
	} `json:"genres"`
	ExternalIDs struct {
		ImdbID string `json:"imdb_id"`
		TVDBID int    `json:"tvdb_id"`
	} `json:"external_ids"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
}

func (d tvDetails) common() movieDetails {
	m := movieDetails{presentationFields: d.presentationFields, ID: d.ID, Title: d.Name, OriginalTitle: d.OriginalName, Overview: d.Overview, Tagline: d.Tagline,
		ReleaseDate: d.FirstAirDate, VoteAverage: d.VoteAverage, ImdbID: d.ExternalIDs.ImdbID, PosterPath: d.PosterPath,
		BackdropPath: d.BackdropPath, Genres: d.Genres, TVDBID: d.ExternalIDs.TVDBID}
	if len(d.EpisodeRunTime) > 0 {
		m.Runtime = d.EpisodeRunTime[0]
	}
	for _, r := range d.ContentRatings.Results {
		if r.Country == "ES" {
			m.ReleaseDates.Results = append(m.ReleaseDates.Results, struct {
				Country string `json:"iso_3166_1"`
				Dates   []struct {
					Certification string `json:"certification"`
				} `json:"release_dates"`
			}{Country: "ES", Dates: []struct {
				Certification string `json:"certification"`
			}{{Certification: r.Rating}}})
		}
	}
	return m
}

func (m movieDetails) genreNames() []string {
	out := make([]string, 0, len(m.Genres))
	for _, g := range m.Genres {
		if n := strings.TrimSpace(g.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// FillGenres completa solo los géneros de una película que ya tiene ficha de TMDB (sin tocar el resto ni las imágenes).
func (t *TMDB) FillGenres(ctx context.Context, it store.PendingMetadata) error {
	key, err := t.key()
	if err != nil {
		return err
	}
	m, err := t.details(ctx, key, it.ProviderIDs["tmdb"])
	if errors.Is(err, errNotFound) { // la ficha ya no existe en TMDB: sin géneros, y no se vuelve a pedir
		return t.Store.SetGenres(ctx, it.ID, nil)
	}
	if err != nil {
		return err
	}
	return t.Store.SetGenres(ctx, it.ID, m.genreNames())
}

// download guarda la imagen en un archivo NUEVO de /config/metadata/<item>/ (nunca junto a los medios). Un archivo ya
// guardado no se reescribe jamás: lo que se ve ahora y lo que guardan las instantáneas de «Deshacer» no pueden cambiar
// por una descarga, y una descarga fallida no afecta a nada. Solo cuenta cuando su ruta se publica en item_images.
func (t *TMDB) download(ctx context.Context, itemID uuid.UUID, kind, size, imgPath string) (string, error) {
	if !strings.HasPrefix(imgPath, "/") || strings.Contains(imgPath, "..") {
		return "", errors.New("ruta de imagen no válida")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.ImageBase+"/"+size+imgPath, nil)
	if err != nil {
		return "", err
	}
	res, err := t.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("imagen: %d", res.StatusCode)
	}
	rel := filepath.Join(itemID.String(), kind+"-"+uuid.NewString()+".jpg")
	dst := filepath.Join(t.ConfigDir, "metadata", rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.LimitReader(res.Body, 10<<20)); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return rel, os.Rename(tmp, dst)
}

// RemoveImageFiles borra archivos de imagen que ya no se usan (ver store.RemoveImageFiles).
func (t *TMDB) RemoveImageFiles(ctx context.Context, itemID uuid.UUID, rels ...string) {
	t.Store.RemoveImageFiles(ctx, itemID, filepath.Join(t.ConfigDir, "metadata"), rels...)
}

// fields convierte la ficha de TMDB en los campos de VELORA (clasificación española de ICAA si la hay).
func (m movieDetails) fields() store.MetadataFields {
	f := store.MetadataFields{Presentation: m.payload(), Title: m.Title, OriginalTitle: m.OriginalTitle, Overview: m.Overview, Tagline: m.Tagline,
		RuntimeMS: int64(m.Runtime) * 60_000, CommunityRating: m.VoteAverage,
		ProviderIDs: map[string]string{"tmdb": strconv.Itoa(m.ID)}, Genres: m.genreNames()}
	if m.TVDBID > 0 {
		f.ProviderIDs["tvdb"] = strconv.Itoa(m.TVDBID)
	}
	if m.ImdbID != "" {
		f.ProviderIDs["imdb"] = m.ImdbID
	}
	if len(m.ReleaseDate) >= 4 {
		f.Year, _ = strconv.Atoi(m.ReleaseDate[:4])
	}
	for _, r := range m.ReleaseDates.Results {
		if r.Country != "ES" {
			continue
		}
		for _, d := range r.Dates {
			if c := strings.ToUpper(strings.TrimSpace(d.Certification)); c != "" {
				f.OfficialRating = c
				if age, ok := esRatingAge[c]; ok {
					f.RatingAge = &age
				}
				break
			}
		}
	}
	return f
}

// Candidate es un resultado de búsqueda para «Corregir identificación» (I2).
type Candidate struct {
	TMDBID        int    `json:"tmdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Year          int    `json:"year,omitempty"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path,omitempty"`
}

var (
	tmdbIDQuery = regexp.MustCompile(`(?i)^tmdb[-:= ]?(\d{1,9})$`)
	imdbIDQuery = regexp.MustCompile(`(?i)^(?:imdb[-:= ]?)?(tt\d{5,10})$`)
)

func yearOf(date string) int {
	if len(date) >= 4 {
		y, _ := strconv.Atoi(date[:4])
		return y
	}
	return 0
}

// Search busca candidatos en TMDB para una película o una serie (kind): «tmdb-603» o «imdb-tt0133093» van directos a esa
// ficha; si no, por título (y año, si se indica). Como mucho 20 resultados.
func (t *TMDB) Search(ctx context.Context, kind, query string, year int) ([]Candidate, error) {
	key, err := t.key()
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	out := []Candidate{}
	if m := tmdbIDQuery.FindStringSubmatch(query); m != nil {
		d, err := t.detailsFor(ctx, key, kind, m[1])
		if errors.Is(err, errNotFound) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		return append(out, Candidate{TMDBID: d.ID, Title: d.Title, OriginalTitle: d.OriginalTitle, Year: yearOf(d.ReleaseDate),
			Overview: d.Overview, PosterPath: d.PosterPath}), nil
	}
	var raw []byte
	if m := imdbIDQuery.FindStringSubmatch(query); m != nil {
		q := url.Values{"external_source": {"imdb_id"}, "language": {t.Language}}
		raw, err = t.cachedGet(ctx, key, "find:"+m[1]+":"+t.Language, "/3/find/"+url.PathEscape(strings.ToLower(m[1])), q)
		if err != nil {
			return nil, err
		}
		var fr struct {
			Movies []searchResult `json:"movie_results"`
			TV     []searchResult `json:"tv_results"`
		}
		if err := json.Unmarshal(raw, &fr); err != nil {
			return nil, err
		}
		if kind == "series" {
			return toCandidates(fr.TV), nil
		}
		return toCandidates(fr.Movies), nil
	}
	endpoint, yearParam, cachePrefix := "/3/search/movie", "year", "search:"
	if kind == "series" {
		endpoint, yearParam, cachePrefix = "/3/search/tv", "first_air_date_year", "searchtv:"
	}
	q := url.Values{"query": {query}, "language": {t.Language}, "include_adult": {"false"}}
	if year > 0 {
		q.Set(yearParam, strconv.Itoa(year))
	}
	raw, err = t.cachedGet(ctx, key, cachePrefix+q.Encode(), endpoint, q)
	if err != nil {
		return nil, err
	}
	var sr struct {
		Results []searchResult `json:"results"`
	}
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, err
	}
	return toCandidates(sr.Results), nil
}

// searchResult vale para películas (title, release_date) y series (name, first_air_date).
type searchResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
}

func toCandidates(rs []searchResult) []Candidate {
	out := []Candidate{}
	for _, r := range rs {
		if len(out) >= 20 {
			break
		}
		if r.ID <= 0 || strings.TrimSpace(r.Title+r.Name) == "" { // sin identificador o título no se puede elegir
			continue
		}
		c := Candidate{TMDBID: r.ID, Title: r.Title, OriginalTitle: r.OriginalTitle, Year: yearOf(r.ReleaseDate),
			Overview: r.Overview, PosterPath: r.PosterPath}
		if c.Title == "" {
			c.Title, c.OriginalTitle, c.Year = r.Name, r.OriginalName, yearOf(r.FirstAirDate)
		}
		out = append(out, c)
	}
	return out
}

// Identification es lo que se aplicaría al identificar una película con un ID de TMDB (para comparar antes y aplicar).
type Identification struct {
	Fields       store.MetadataFields
	PosterPath   string
	BackdropPath string
}

// Identify descarga la ficha de una película o serie de TMDB (sin aplicar nada).
func (t *TMDB) Identify(ctx context.Context, kind string, tmdbID int) (Identification, error) {
	key, err := t.key()
	if err != nil {
		return Identification{}, err
	}
	m, err := t.detailsFor(ctx, key, kind, strconv.Itoa(tmdbID))
	if err != nil {
		return Identification{}, err
	}
	return Identification{Fields: m.fields(), PosterPath: m.PosterPath, BackdropPath: m.BackdropPath}, nil
}

// DownloadIdentityImages baja la carátula y el fondo de una identificación a archivos nuevos. Devuelve tipo → ruta relativa
// a /config/metadata, que la identificación publica al confirmar. Si una falla, borra lo bajado y devuelve el error: una
// identificación nunca queda a medias y lo que se ve no cambia.
func (t *TMDB) DownloadIdentityImages(ctx context.Context, itemID uuid.UUID, id Identification) (map[string]string, error) {
	out := map[string]string{}
	for kind, p := range map[string]string{"poster": id.PosterPath, "backdrop": id.BackdropPath} {
		if p == "" {
			continue
		}
		size := "w500"
		if kind == "backdrop" {
			size = "w1280"
		}
		rel, err := t.download(ctx, itemID, kind, size, p)
		if err != nil {
			for _, done := range out {
				t.RemoveImageFiles(ctx, itemID, done)
			}
			return nil, err
		}
		out[kind] = rel
	}
	return out, nil
}

var thumbPath = regexp.MustCompile(`^/[A-Za-z0-9_-]{1,64}\.(jpg|jpeg|png|webp)$`)

// ThumbSizes son los tamaños de vista previa admitidos (nunca un tamaño cualquiera en la URL de TMDB).
var ThumbSizes = map[string]bool{"w185": true, "w300": true, "w500": true, "w780": true}

// Thumb devuelve una vista previa de TMDB para candidatos y galerías, con caché en /config/cache/tmdb-thumbs. Solo rutas
// de imagen de TMDB (nunca una URL cualquiera): el navegador no llama a TMDB y el servidor no descarga de otros sitios.
func (t *TMDB) Thumb(ctx context.Context, size, imgPath string) ([]byte, error) {
	if !thumbPath.MatchString(imgPath) || !ThumbSizes[size] {
		return nil, errors.New("ruta de imagen no válida")
	}
	cache := filepath.Join(t.ConfigDir, "cache", "tmdb-thumbs", size, strings.TrimPrefix(imgPath, "/"))
	if b, err := os.ReadFile(cache); err == nil {
		return b, nil
	}
	b, err := t.fetchImage(ctx, size, imgPath, 2<<20)
	if err != nil {
		return nil, err
	}
	if os.MkdirAll(filepath.Dir(cache), 0o750) == nil {
		_ = os.WriteFile(cache, b, 0o640)
	}
	return b, nil
}

func (t *TMDB) fetchImage(ctx context.Context, size, imgPath string, limit int64) ([]byte, error) {
	if !thumbPath.MatchString(imgPath) {
		return nil, errors.New("ruta de imagen no válida")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.ImageBase+"/"+size+imgPath, nil)
	if err != nil {
		return nil, err
	}
	res, err := t.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("no se pudo contactar con TMDB")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("imagen: %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

// GalleryTarget indica de qué ficha de TMDB se piden las imágenes. Temporadas y episodios usan el ID de su serie y sus
// números; sin ellos no hay galería, pero se puede subir una imagen propia.
type GalleryTarget struct {
	Kind            string // movie, series, season o episode
	TMDBID          string
	Season, Episode int
}

// GalleryImage es una imagen disponible en el proveedor.
type GalleryImage struct {
	Provider string  `json:"provider,omitempty"`
	FilePath string  `json:"file_path"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Language string  `json:"language,omitempty"`
	Vote     float64 `json:"vote"`
}

// Gallery son las imágenes de una ficha, separadas por categoría: un fondo nunca aparece como cartel, ni al revés.
type Gallery struct {
	Posters   []GalleryImage `json:"posters"`
	Backdrops []GalleryImage `json:"backdrops"`
	Logos     []GalleryImage `json:"logos"`
	Stills    []GalleryImage `json:"stills"`
}

const galleryMax = 500

// Gallery pide la lista de imágenes a TMDB (solo la lista: no descarga ninguna imagen).
func (t *TMDB) Gallery(ctx context.Context, tg GalleryTarget) (Gallery, error) {
	key, err := t.key()
	if err != nil {
		return Gallery{}, err
	}
	var path string
	id := url.PathEscape(tg.TMDBID)
	switch tg.Kind {
	case "movie":
		path = "/3/movie/" + id + "/images"
	case "series":
		path = "/3/tv/" + id + "/images"
	case "season":
		path = fmt.Sprintf("/3/tv/%s/season/%d/images", id, tg.Season)
	case "episode":
		path = fmt.Sprintf("/3/tv/%s/season/%d/episode/%d/images", id, tg.Season, tg.Episode)
	default:
		return Gallery{}, errNotFound
	}
	q := url.Values{} // galería completa: no excluir otros idiomas del editor
	raw, err := t.cachedGet(ctx, key, "images:all:"+path, path, q)
	if err != nil {
		return Gallery{}, err
	}
	var d struct {
		Posters, Backdrops, Logos, Stills []struct {
			FilePath string  `json:"file_path"`
			Width    int     `json:"width"`
			Height   int     `json:"height"`
			Language string  `json:"iso_639_1"`
			Vote     float64 `json:"vote_average"`
		}
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return Gallery{}, err
	}
	conv := func(in []struct {
		FilePath string  `json:"file_path"`
		Width    int     `json:"width"`
		Height   int     `json:"height"`
		Language string  `json:"iso_639_1"`
		Vote     float64 `json:"vote_average"`
	}, keep func(w, h int) bool) []GalleryImage {
		out := []GalleryImage{}
		for _, i := range in {
			// Fuera lo que no se puede procesar (TMDB también tiene logotipos en SVG) y lo que no encaja en la categoría.
			if !thumbPath.MatchString(i.FilePath) || !keep(i.Width, i.Height) || len(out) >= galleryMax {
				continue
			}
			out = append(out, GalleryImage{FilePath: i.FilePath, Width: i.Width, Height: i.Height, Language: i.Language, Vote: i.Vote})
		}
		return out
	}
	vertical := func(w, h int) bool { return h > w }
	horizontal := func(w, h int) bool { return w > h }
	return Gallery{Posters: conv(d.Posters, vertical), Backdrops: conv(d.Backdrops, horizontal),
		Logos: conv(d.Logos, func(int, int) bool { return true }), Stills: conv(d.Stills, horizontal)}, nil
}

// GalleryChoice busca imgPath en la lista de la categoría que corresponde a kind y descarga la imagen. Solo se admite lo
// que TMDB ofrece para esa categoría: así no se puede pedir una ruta inventada ni un fondo como cartel.
func (t *TMDB) GalleryChoice(ctx context.Context, tg GalleryTarget, kind, imgPath string) ([]byte, error) {
	g, err := t.Gallery(ctx, tg)
	if err != nil {
		return nil, err
	}
	for _, c := range []struct {
		kinds string
		list  []GalleryImage
		size  string
	}{{"poster", g.Posters, "w780"}, {"backdrop banner thumb", g.Backdrops, "w1280"}, {"logo", g.Logos, "w500"}, {"thumb", g.Stills, "original"}} {
		if !slices.Contains(strings.Fields(c.kinds), kind) {
			continue
		}
		for _, i := range c.list {
			if i.FilePath == imgPath {
				return t.fetchImage(ctx, c.size, imgPath, 10<<20)
			}
		}
	}
	return nil, ErrProviderNotFound
}

// Presentation fetches descriptive information only; it never opens a media file.
func (t *TMDB) Presentation(ctx context.Context, it store.PendingMetadata) (json.RawMessage, error) {
	key, err := t.key()
	if err != nil {
		return nil, err
	}
	m, err := t.detailsFor(ctx, key, it.Kind, it.ProviderIDs["tmdb"])
	if err != nil {
		return nil, err
	}
	return m.payload(), nil
}
func (t *TMDB) ProfileImage(ctx context.Context, path string) ([]byte, error) {
	return t.ProfileImageSize(ctx, path, "w185")
}

// ProfileImageSize descarga la foto de una persona en un tamaño de TMDB fijo (w185 para el reparto, h632 para su ficha).
func (t *TMDB) ProfileImageSize(ctx context.Context, path, size string) ([]byte, error) {
	if !regexp.MustCompile(`^/[a-zA-Z0-9]+\.(jpg|png)$`).MatchString(path) {
		return nil, errors.New("invalid profile path")
	}
	if size != "w185" && size != "h632" {
		return nil, errors.New("invalid profile size")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.ImageBase+"/"+size+path, nil)
	if err != nil {
		return nil, err
	}
	res, err := t.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("profile: %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, imaging.MaxUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > imaging.MaxUploadBytes {
		return nil, errors.New("profile too large")
	}
	return data, nil
}

// Person son los datos de la ficha de una persona del reparto (TMDB /person/{id}).
type Person struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Biography    string `json:"biography"`
	Birthday     string `json:"birthday"`
	Deathday     string `json:"deathday"`
	PlaceOfBirth string `json:"place_of_birth"`
	ProfilePath  string `json:"profile_path"`
	Department   string `json:"known_for_department"`
}

// PersonDetails devuelve la ficha de una persona en el idioma configurado. Si TMDB no tiene su biografía en ese idioma,
// usa la inglesa (lo habitual en actores poco conocidos en España). Las respuestas se guardan en caché como el resto.
func (t *TMDB) PersonDetails(ctx context.Context, id int) (Person, error) {
	var p Person
	key, err := t.key()
	if err != nil {
		return p, err
	}
	get := func(lang string) (Person, error) {
		var out Person
		raw, err := t.cachedGet(ctx, key, fmt.Sprintf("person:%d:%s", id, lang), fmt.Sprintf("/3/person/%d", id), url.Values{"language": {lang}})
		if err != nil {
			return out, err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
		if out.ID != id || out.Name == "" {
			return out, ErrProviderInvalid
		}
		return out, nil
	}
	if p, err = get(t.Language); err != nil {
		return p, err
	}
	if strings.TrimSpace(p.Biography) == "" && !strings.HasPrefix(t.Language, "en") {
		if en, err := get("en-US"); err == nil {
			p.Biography = en.Biography
		}
	}
	return p, nil
}

// PersonCredits resolves the filmography once, without scanning media or fetching every library detail.
func (t *TMDB) PersonCredits(ctx context.Context, id int) (movies, series []string, err error) {
	key, err := t.key()
	if err != nil {
		return nil, nil, err
	}
	raw, err := t.cachedGet(ctx, key, fmt.Sprintf("person:%d:credits", id), fmt.Sprintf("/3/person/%d/combined_credits", id), nil)
	if err != nil {
		return nil, nil, err
	}
	var credits struct {
		Cast, Crew []struct {
			ID        int    `json:"id"`
			MediaType string `json:"media_type"`
		}
	}
	if err = json.Unmarshal(raw, &credits); err != nil {
		return nil, nil, err
	}
	movies = []string{}
	series = []string{}
	for _, list := range [][]struct {
		ID        int    `json:"id"`
		MediaType string `json:"media_type"`
	}{credits.Cast} {
		for _, credit := range list {
			if credit.ID <= 0 {
				continue
			}
			if credit.MediaType == "movie" {
				movies = append(movies, strconv.Itoa(credit.ID))
			}
			if credit.MediaType == "tv" {
				series = append(series, strconv.Itoa(credit.ID))
			}
		}
	}
	return movies, series, nil
}

// CollectionMembership reads TMDB identity only, without replacing edited titles or artwork.
func (t *TMDB) CollectionMembership(ctx context.Context, it store.PendingMetadata) (json.RawMessage, error) {
	key, err := t.key()
	if err != nil {
		return nil, err
	}
	id := it.ProviderIDs["tmdb"]
	m, err := t.details(ctx, key, id)
	if err != nil {
		return nil, err
	}
	if m.Collection != nil && (m.Collection.ID <= 0 || m.Collection.Name == "") {
		return nil, errors.New("invalid collection identity")
	}
	return json.Marshal(m.Collection)
}
