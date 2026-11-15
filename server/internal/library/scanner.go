package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"velora/internal/media"
	"velora/internal/store"
)

// Imágenes locales reconocidas dentro de la carpeta de una película.
var posterNames = []string{"poster", "folder", "cover", "movie"}
var backdropNames = []string{"fanart", "backdrop", "background"}
var imageExts = []string{".jpg", ".jpeg", ".png", ".webp"}

// Result resume un escaneo.
type Result struct {
	Items    int `json:"items"`
	Files    int `json:"files"`
	Probed   int `json:"probed"`
	Missing  int `json:"missing"`
	Errors   int `json:"errors"`
	Extras   int `json:"extras"`             // tráileres y extras asociados a su película
	Pending  int `json:"pending"`            // extras de carpetas sin película con ficha previa: la ficha queda pendiente (oculta)
	Series   int `json:"series,omitempty"`   // bibliotecas de series: series vistas en este escaneo
	Episodes int `json:"episodes,omitempty"` // y episodios
}

// ScanProgress es el avance de un escaneo, para la barra del panel. Fases: «listing» (buscando archivos; Total aún
// desconocido), «analyzing» (registrando y analizando), «extras» y «metadata» (fichas de TMDB, la pone el trabajo).
type ScanProgress struct {
	Phase string `json:"phase"`
	Path  int    `json:"path"`  // ruta que se escanea (1..Paths)
	Paths int    `json:"paths"` // rutas de la biblioteca
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Item  string `json:"item,omitempty"` // carpeta en curso (relativa a la ruta)
}

// Scanner recorre las bibliotecas.
type Scanner struct {
	Store *store.Store
	Roots []string                                                   // raíces de medios: se revalidan en cada escaneo
	Probe func(ctx context.Context, f *os.File) (media.Probe, error) // inyectable en pruebas
	// OnProgress recibe el avance (puede llamarse muy a menudo: quien lo guarda decide cada cuánto).
	OnProgress func(ScanProgress)
}

func (sc *Scanner) report(p ScanProgress) {
	if sc.OnProgress != nil {
		sc.OnProgress(p)
	}
}

// Tiempo máximo de ffprobe por archivo: un montaje remoto lento o colgado no puede detener el escaneo.
const (
	probeTimeoutLocal  = 60 * time.Second
	probeTimeoutRemote = 120 * time.Second
)

// NewScanner crea un escáner que analiza con ffprobe.
func NewScanner(st *store.Store, roots []string) *Scanner {
	return &Scanner{Store: st, Roots: roots, Probe: media.RunProbe}
}

// ScanLibrary escanea cada ruta de la biblioteca. Una ruta que no responde se marca como inaccesible sin tocar sus datos
// (§10.1) y no impide escanear las demás.
func (sc *Scanner) ScanLibrary(ctx context.Context, lib store.Library) (Result, error) {
	var total Result
	if lib.Kind != "movies" && lib.Kind != "tv" {
		slog.Info("tipo de biblioteca aún sin escáner", "biblioteca", lib.Name, "tipo", lib.Kind)
		return total, nil
	}
	var errs []error
	for i, p := range lib.Paths {
		res, err := sc.scanPath(ctx, lib, p, i+1, len(lib.Paths))
		total.Items += res.Items
		total.Files += res.Files
		total.Probed += res.Probed
		total.Missing += res.Missing
		total.Errors += res.Errors
		total.Extras += res.Extras
		total.Pending += res.Pending
		total.Series += res.Series
		total.Episodes += res.Episodes
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

func (sc *Scanner) scanPath(ctx context.Context, lib store.Library, lp store.LibraryPath, pathN, paths int) (Result, error) {
	var res Result
	root, err := OpenRoot(lp.Path, sc.Roots)
	if err != nil {
		_ = sc.Store.SetPathStatus(ctx, lp.ID, "unavailable", false)
		return res, fmt.Errorf("la ruta %s no está disponible: %w", lp.Path, err)
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(entries) == 0 {
		// Un montaje caído suele verse vacío: no se marca nada como desaparecido.
		_ = sc.Store.SetPathStatus(ctx, lp.ID, "unavailable", false)
		if err == nil {
			err = errors.New("la carpeta está vacía o el montaje no responde")
		}
		return res, err
	}

	seen := []string{}
	readErrors := 0
	probeTimeout := probeTimeoutLocal
	if lp.Kind == "remote_fuse" {
		probeTimeout = probeTimeoutRemote
	}
	items := map[string]uuid.UUID{}
	type foundExtra struct {
		rel, kind string
		info      fs.FileInfo
	}
	type foundVideo struct {
		rel  string
		info fs.FileInfo
	}
	var extras []foundExtra
	var videos []foundVideo
	// 1) Se lista todo (en un montaje remoto, solo directorios: barato) para saber el total y poder mostrar el avance.
	walkErr := fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			res.Errors++
			readErrors++
			slog.Warn("no se pudo leer una ruta de la biblioteca", "ruta", rel, "error", err)
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." {
				return fs.SkipDir
			}
			return nil
		}
		var kind string
		var isVideo bool
		if lib.Kind == "tv" { // Serie/archivo o Serie/Season N/archivo; los extras, solo de la serie
			if isVideo = IsEpisodeFile(rel); !isVideo {
				kind = SeriesExtraKind(rel)
			}
		} else {
			kind = ExtraKind(rel)
			isVideo = kind == "" && IsVideo(rel)
		}
		if kind == "" && !isVideo {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			// No se sabe si el archivo sigue ahí: cuenta como lectura incompleta y este escaneo no marcará ausencias.
			res.Errors++
			readErrors++
			return nil
		}
		if kind != "" {
			extras = append(extras, foundExtra{rel, kind, info}) // se registran al final, cuando se conocen las películas
		} else {
			videos = append(videos, foundVideo{rel, info})
		}
		if n := len(videos) + len(extras); n%50 == 0 {
			sc.report(ScanProgress{Phase: "listing", Path: pathN, Paths: paths, Done: n})
		}
		return nil
	})
	// 2) Películas: registrar y analizar.
	total := len(videos) + len(extras)
	for i, v := range videos {
		if walkErr != nil {
			break
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		rel, info := v.rel, v.info
		folder := ContentPath(rel)
		sc.report(ScanProgress{Phase: "analyzing", Path: pathN, Paths: paths, Done: i, Total: total, Item: folder})
		itemID, ok := items[folder]
		if lib.Kind == "tv" {
			var err error
			if itemID, err = sc.episodeItem(ctx, root, lib, lp, rel, items, &res); err != nil {
				return res, err
			}
			ok = true
		}
		if !ok {
			name := strings.TrimSuffix(folder, path.Ext(folder))
			if folder != rel { // carpeta propia: el nombre de la carpeta manda
				name = folder
			}
			p := ParseName(name)
			var err error
			itemID, err = sc.Store.UpsertMovie(ctx, store.MovieUpsert{LibraryID: lib.ID, PathID: lp.ID, ContentPath: folder,
				Title: p.Title, Year: p.Year, ProviderIDs: p.ProviderIDs, SortTitle: SortTitle(p.Title)})
			if err != nil {
				return res, err
			}
			items[folder] = itemID
			res.Items++
			if folder != rel {
				sc.findImages(ctx, root, folder, itemID, lp.ID)
			}
		}
		label := VersionLabel(folder, rel)
		if lib.Kind == "tv" {
			label = ""
		}
		fileID, needProbe, err := sc.Store.UpsertFile(ctx, store.FileUpsert{ItemID: itemID, PathID: lp.ID, RelPath: rel,
			Size: info.Size(), MTime: info.ModTime(), VersionLabel: label})
		if err != nil {
			return res, err
		}
		seen = append(seen, rel)
		res.Files++
		if needProbe {
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			err := sc.probeFile(pctx, root, rel, fileID)
			cancel()
			if err != nil {
				res.Errors++
				slog.Warn("no se pudo analizar un fichero", "ruta", rel, "error", err)
			} else {
				res.Probed++
			}
		}
	}
	if walkErr != nil {
		return res, walkErr
	}
	// Extras: se asocian a la ficha de su carpeta. Una carpeta sin película (solo el tráiler) nunca crea una ficha; si ya
	// la tenía de antes, el tráiler se le asocia y la ficha queda «pendiente»: oculta a los usuarios, con su
	// identificación e imágenes, hasta que aparezca la película (que entonces vuelve a la misma ficha).
	seenExtras := []string{}
	for i, x := range extras {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		sc.report(ScanProgress{Phase: "extras", Path: pathN, Paths: paths, Done: len(videos) + i, Total: total, Item: ContentPath(x.rel)})
		folder := ContentPath(x.rel)
		itemID, ok := items[folder]
		if !ok {
			existing, found, err := sc.Store.ItemForFolder(ctx, lp.ID, folder)
			if err != nil {
				return res, err
			}
			if !found {
				continue
			}
			itemID = existing
			res.Pending++
		}
		extraID, needProbe, err := sc.Store.UpsertExtra(ctx, store.ExtraUpsert{ItemID: itemID, PathID: lp.ID, RelPath: x.rel,
			Kind: x.kind, Size: x.info.Size(), MTime: x.info.ModTime()})
		if err != nil {
			return res, err
		}
		seenExtras = append(seenExtras, x.rel)
		res.Extras++
		if needProbe {
			pctx, cancel := context.WithTimeout(ctx, probeTimeout)
			err := sc.probeExtra(pctx, root, x.rel, extraID)
			cancel()
			if err != nil {
				res.Errors++
				slog.Warn("no se pudo analizar un extra", "ruta", x.rel, "error", err)
			}
		}
	}
	// Con errores de lectura no se sabe qué falta de verdad: no se marca nada como ausente.
	if readErrors == 0 {
		if _, err := sc.Store.MarkExtrasMissing(ctx, lp.ID, seenExtras); err != nil {
			return res, err
		}
		missing, err := sc.Store.MarkMissing(ctx, lp.ID, seen)
		if errors.Is(err, store.ErrMassMissing) {
			slog.Warn("escaneo prudente: no se marcan ausencias", "biblioteca", lib.Name, "desaparecerian", missing)
		} else if err != nil {
			return res, err
		} else {
			res.Missing = int(missing)
		}
	}
	return res, sc.Store.SetPathStatus(ctx, lp.ID, "available", true)
}

func (sc *Scanner) probeFile(ctx context.Context, root *os.Root, rel string, fileID uuid.UUID) error {
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := sc.Probe(ctx, f)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	var rows []store.StreamRow
	for _, s := range p.Streams {
		if s.CodecType != "video" && s.CodecType != "audio" && s.CodecType != "subtitle" {
			continue
		}
		row := store.StreamRow{Index: s.Index, Kind: s.CodecType, Codec: s.CodecName, Profile: s.Profile,
			Language: s.Language(), Title: s.Tags["title"], Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
			Width: s.Width, Height: s.Height, Channels: s.Channels}
		if s.CodecType == "video" {
			row.BitDepth, row.HDR = s.BitDepth(), s.HDR()
		}
		rows = append(rows, row)
	}
	container := p.Container()
	if container == "mkv" && strings.EqualFold(path.Ext(rel), ".webm") {
		container = "webm"
	}
	return sc.Store.SaveProbe(ctx, fileID, container, p.DurationMS(), p.BitrateKbps(), raw, rows)
}

func (sc *Scanner) probeExtra(ctx context.Context, root *os.Root, rel string, extraID uuid.UUID) error {
	f, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer f.Close()
	p, err := sc.Probe(ctx, f)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	return sc.Store.SaveExtraProbe(ctx, extraID, p.Container(), p.DurationMS(), raw)
}

// findImages registra el póster y el fondo locales de la carpeta (solo lectura; nunca se escribe en los medios).
func (sc *Scanner) findImages(ctx context.Context, root *os.Root, folder string, itemID, pathID uuid.UUID) {
	entries, err := fs.ReadDir(root.FS(), folder)
	if err != nil {
		return
	}
	names := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			names[strings.ToLower(e.Name())] = e.Name()
		}
	}
	pick := func(bases []string) string {
		for _, b := range bases {
			for _, ext := range imageExts {
				if n, ok := names[b+ext]; ok {
					return folder + "/" + n
				}
				if n, ok := names[strings.ToLower(folder)+"-"+b+ext]; ok {
					return folder + "/" + n
				}
			}
		}
		return ""
	}
	if p := pick(posterNames); p != "" {
		_ = sc.Store.SetImage(ctx, itemID, "poster", "local", p, &pathID)
	}
	if p := pick(backdropNames); p != "" {
		_ = sc.Store.SetImage(ctx, itemID, "backdrop", "local", p, &pathID)
	}
}

// episodeItem devuelve la ficha del episodio de rel, creando antes, si hacen falta, la serie (la carpeta de primer
// nivel) y la temporada. items guarda las ya vistas en este escaneo (serie, temporada y episodio por su clave).
func (sc *Scanner) episodeItem(ctx context.Context, root *os.Root, lib store.Library, lp store.LibraryPath, rel string,
	items map[string]uuid.UUID, res *Result) (uuid.UUID, error) {
	folder := ContentPath(rel)
	seriesID, ok := items[folder]
	if !ok {
		p := ParseName(folder)
		var err error
		seriesID, err = sc.Store.UpsertMovie(ctx, store.MovieUpsert{LibraryID: lib.ID, PathID: lp.ID, ContentPath: folder, Kind: "series",
			Title: p.Title, Year: p.Year, ProviderIDs: p.ProviderIDs, SortTitle: SortTitle(p.Title)})
		if err != nil {
			return uuid.Nil, err
		}
		items[folder] = seriesID
		res.Items++
		res.Series++
		sc.findImages(ctx, root, folder, seriesID, lp.ID)
	}
	ep := ParseEpisode(rel)
	seasonKey := SeasonKey(folder, ep.Season)
	seasonID, ok := items[seasonKey]
	if !ok {
		n := ep.Season
		var err error
		seasonID, err = sc.Store.UpsertChild(ctx, store.ChildUpsert{LibraryID: lib.ID, PathID: lp.ID, SeriesID: seriesID, ParentID: seriesID,
			Kind: "season", ContentPath: seasonKey, Title: SeasonTitle(n), SortTitle: pad2(n), Number: &n})
		if err != nil {
			return uuid.Nil, err
		}
		items[seasonKey] = seasonID
	}
	epKey := EpisodeKey(folder, ep, rel)
	if id, ok := items[epKey]; ok { // otra versión del mismo episodio
		return id, nil
	}
	c := store.ChildUpsert{LibraryID: lib.ID, PathID: lp.ID, SeriesID: seriesID, ParentID: seasonID, Kind: "episode", ContentPath: epKey,
		Title: ep.Title}
	if ep.Number > 0 {
		n := ep.Number
		c.Number = &n
		c.SortTitle = fmt.Sprintf("%04d", n)
		if c.Title == "" {
			c.Title = fmt.Sprintf("Episodio %d", n)
		}
		if ep.NumberEnd > 0 {
			end := ep.NumberEnd
			c.NumberEnd = &end
		}
	} else {
		if c.Title == "" {
			c.Title = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		}
		c.SortTitle = SortTitle(c.Title)
	}
	id, err := sc.Store.UpsertChild(ctx, c)
	if err != nil {
		return uuid.Nil, err
	}
	items[epKey] = id
	res.Items++
	res.Episodes++
	return id, nil
}
