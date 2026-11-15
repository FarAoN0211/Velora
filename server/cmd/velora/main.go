// Comando velora: servidor (serve), trabajos de fondo (worker), migraciones (migrate) y copias (backup).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"velora/internal/api"
	"velora/internal/audit"
	"velora/internal/auth"
	"velora/internal/backup"
	"velora/internal/config"
	"velora/internal/db"
	"velora/internal/jobs"
	"velora/internal/library"
	"velora/internal/markers"
	"velora/internal/metadata"
	"velora/internal/secrets"
	"velora/internal/setup"
	"velora/internal/store"
)

const usage = `uso: velora <orden>

  serve            API y web (puerto VELORA_LISTEN, por defecto :8640)
  worker           trabajos de fondo y tareas programadas (copias de seguridad…)
  migrate up|down|status
  backup           hace una copia verificada ahora
  restore-images <copia.dump>   devuelve a /config/metadata las imágenes personalizadas de una copia (tras restaurar su base)
  setup-code       genera un código de instalación nuevo (solo si aún no hay administrador)
  recovery-code <usuario>   genera un código de recuperación de emergencia válido 30 minutos
  version
`

func main() {
	// Ficheros nuevos sin permisos para "otros" (copias, secretos, configuración): E0.1, punto 5.
	syscall.Umask(0o027)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1], os.Args[2:]); err != nil {
		slog.Error("error", "orden", os.Args[1], "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	if cmd == "version" {
		fmt.Println(api.Version)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch cmd {
	case "migrate":
		return migrate(ctx, cfg, args)
	case "serve", "worker", "backup", "setup-code", "restore-images", "recovery-code":
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("orden desconocida: %s", cmd)
	}

	if err := db.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migraciones: %w", err)
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := &store.Store{Pool: pool}
	queue := jobs.NewQueue(pool)
	bk := &backup.Service{Pool: pool, DatabaseURL: cfg.DatabaseURL, Dir: cfg.BackupDir, MetadataDir: filepath.Join(cfg.ConfigDir, "metadata"),
		MinFreeBytes: cfg.MinFreeBytes, Keep: 7, KeepWeeks: 4}

	switch cmd {
	case "recovery-code":
		if len(args) != 1 {
			return errors.New("uso: velora recovery-code <usuario>")
		}
		u, err := st.UserByUsername(ctx, args[0])
		if err != nil {
			return err
		}
		if u.IsDisabled {
			return errors.New("cuenta desactivada")
		}
		code, hash, err := auth.NewToken()
		if err != nil {
			return err
		}
		// Recovery normalizes case and separators, so store its normalized hash too.
		normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(code, "-", ""), "_", ""))
		hash = auth.HashToken(normalized)
		if err = st.SetRecoveryCodes(ctx, u.ID, [][]byte{hash}, 30*time.Minute); err != nil {
			return err
		}
		fmt.Printf("Código de recuperación para %s (30 minutos, un solo uso): %s\n", u.Username, normalized)
		return nil
	case "setup-code":
		code, err := setup.Ensure(ctx, pool, cfg.ConfigDir, true)
		if err != nil {
			return err
		}
		if code == "" {
			fmt.Println("El servidor ya tiene administrador: no hace falta código de instalación.")
			return nil
		}
		fmt.Printf("Código de instalación nuevo: %s (el anterior deja de valer)\n", code)
		return nil
	case "backup":
		meta, err := bk.Run(ctx)
		out, _ := json.MarshalIndent(meta, "", "  ")
		fmt.Println(string(out))
		return err
	case "restore-images":
		if len(args) != 1 {
			return errors.New("uso: velora restore-images <copia.dump>")
		}
		res, err := bk.RestoreImages(ctx, args[0])
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return err
	case "worker":
		tmdb := metadata.New(cfg.TMDBAPIURL, cfg.TMDBImageURL, cfg.ConfigDir, st)
		w := jobs.NewWorker(pool, map[string]jobs.Handler{
			"backup":               backupJob(bk, st),
			"library_scan":         scanJob(st, library.NewScanner(st, cfg.MediaRoots), tmdb, queue),
			"library_scan_all":     scanAllJob(st, queue),
			"item_refresh":         itemRefreshJob(st, tmdb),
			"series_children":      seriesChildrenJob(tmdb),
			"markers_detect":       markersJob(st, cfg.MediaRoots),
			"logos_fill":           logosJob(st, tmdb),
			"collections_fill":     collectionsJob(st, tmdb),
			"login_attempts_purge": func(ctx context.Context, _ json.RawMessage) (any, error) { return nil, st.PurgeAttempts(ctx) },
			// Hogares: miembros con más de 30 días en la papelera se borran definitivamente, con su historial.
			"household_purge": func(ctx context.Context, _ json.RawMessage) (any, error) {
				n, err := st.PurgeExpiredMembers(ctx)
				if err != nil {
					return nil, err
				}
				// Y los historiales sellados que nadie ha recuperado con su código en 30 días.
				seals, err := st.PurgeExpiredSeals(ctx)
				return map[string]int{"miembros_borrados": n, "historiales_sellados_borrados": seals}, err
			},
		})
		w.Timeouts = map[string]time.Duration{"library_scan": 3 * time.Hour, "markers_detect": markersTimeout, "logos_fill": 3 * time.Hour, "collections_fill": 3 * time.Hour}
		go func() {
			if err := jobs.NewScheduler(pool, queue, cfg.Location).Run(ctx); err != nil {
				slog.Error("planificador detenido", "error", err)
			}
		}()
		if tmdb.LogosConfigured() {
			_, _ = queue.Enqueue(ctx, "logos_fill", map[string]string{}, "logos")
		}
		if tmdb.Configured() {
			_, _ = queue.Enqueue(ctx, "collections_fill", map[string]string{}, "collections")
		}
		slog.Info("worker en marcha")
		return w.Run(ctx, "library_scan", "markers_detect", "logos_fill", "collections_fill")
	}

	// serve
	masterKey, err := secrets.EnsureMasterKey(cfg.ConfigDir)
	if err != nil {
		return fmt.Errorf("clave maestra: %w", err)
	}
	// Sin administrador, el asistente exige un código que solo se entrega por esta consola (E0.1, punto 1).
	code, err := setup.Ensure(ctx, pool, cfg.ConfigDir, false)
	if errors.Is(err, setup.ErrLocked) {
		fmt.Fprintf(os.Stderr, "\n  VELORA aún no está configurado y el código de instalación está BLOQUEADO tras demasiados intentos.\n"+
			"  Genera otro con: docker exec <contenedor> velora setup-code\n\n")
		err = nil
	}
	if err != nil {
		return fmt.Errorf("código de instalación: %w", err)
	}
	if code != "" {
		fmt.Fprintf(os.Stderr, "\n  VELORA aún no está configurado.\n  Código de instalación: %s\n"+
			"  (también en %s/secrets/setup-code; se borra al crear el administrador)\n\n", code, cfg.ConfigDir)
	}
	srv := api.New(cfg, masterKey, st, audit.New(pool), queue, bk)
	// Sin WriteTimeout global (rompería los flujos largos); el remux tiene su propio corte por inactividad.
	httpSrv := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	go func() {
		slog.Info("VELORA escuchando", "direccion", cfg.Listen, "version", api.Version)
		errc <- httpSrv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdown)
	}
	return nil
}

// backupJob hace la copia verificada y registra el crecimiento del almacenamiento (observación de ChatGPT).
func backupJob(bk *backup.Service, st *store.Store) jobs.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		meta, err := bk.Run(ctx)
		if rep, serr := bk.RecordStorage(ctx); serr == nil {
			_ = st.SetSetting(ctx, "storage.last_report", rep)
			if rep.WarnMarginInvasion {
				slog.Warn("la proyección a 30 días invade el margen de seguridad de disco",
					"libre_proyectado_mb", int64(rep.Projected30dFree)>>20, "margen_mb", rep.MarginBytes>>20)
			}
		} else {
			slog.Error("no se pudo medir el almacenamiento", "error", serr)
		}
		if errors.Is(err, backup.ErrImagesIncomplete) {
			return meta, nil // repetirla no traería las imágenes: ya consta como incompleta y se ve en el panel
		}
		return meta, err
	}
}

// scansInFlight registra los escaneos en curso. Como el carril de escaneos es de uno en uno, si al empezar un escaneo
// ya hay otro de la misma biblioteca, es uno abandonado por agotar su plazo que sigue bloqueado en el kernel (montaje
// FUSE colgado): Go no puede interrumpir esa llamada. Para no acumular gorutinas bloqueadas, esa biblioteca no se vuelve
// a escanear hasta que la llamada regrese (o se reinicie el worker), y con maxStuckScans bloqueados no se escanea nada.
var (
	scansMu       sync.Mutex
	scansInFlight = map[uuid.UUID]bool{}
)

const maxStuckScans = 2

func beginScan(id uuid.UUID) (func(), error) {
	scansMu.Lock()
	defer scansMu.Unlock()
	if scansInFlight[id] {
		return nil, errors.New("el escaneo anterior de esta biblioteca sigue bloqueado en el montaje; se reintentará más tarde")
	}
	if len(scansInFlight) >= maxStuckScans {
		return nil, fmt.Errorf("hay %d escaneos bloqueados en montajes que no responden; reinicia el worker cuando vuelvan", len(scansInFlight))
	}
	scansInFlight[id] = true
	return func() {
		scansMu.Lock()
		delete(scansInFlight, id)
		scansMu.Unlock()
	}, nil
}

// scanJob escanea una biblioteca y, si hay clave de TMDB, completa los metadatos de lo nuevo.
func scanJob(st *store.Store, sc *library.Scanner, tmdb *metadata.TMDB, q *jobs.Queue) jobs.Handler {
	return func(ctx context.Context, payload json.RawMessage) (any, error) {
		var p struct {
			LibraryID uuid.UUID `json:"library_id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		lib, err := st.LibraryByID(ctx, p.LibraryID)
		if err != nil {
			return nil, err
		}
		if lib.RetiredAt != nil {
			return map[string]string{"omitido": "biblioteca retirada"}, nil
		}
		done, err := beginScan(lib.ID)
		if err != nil {
			for _, p := range lib.Paths {
				_ = st.SetPathStatus(context.WithoutCancel(ctx), p.ID, "unavailable", false)
			}
			slog.Error("montaje bloqueado", "biblioteca", lib.Name, "error", err)
			return nil, err
		}
		defer done()
		// Progreso para la barra del panel: como mucho una escritura por segundo (la última fase siempre se guarda).
		var last time.Time
		report := func(p library.ScanProgress, force bool) {
			if force || time.Since(last) >= time.Second {
				last = time.Now()
				jobs.ReportProgress(ctx, p)
			}
		}
		scanner := *sc // copia por trabajo: OnProgress es de este escaneo
		scanner.OnProgress = func(p library.ScanProgress) { report(p, false) }
		res, err := scanner.ScanLibrary(ctx, lib)
		if err != nil {
			return res, err
		}
		if lib.Kind == "tv" {
			defer func() {
				if _, err := q.Enqueue(context.WithoutCancel(ctx), "markers_detect", map[string]string{}, "markers"); err != nil && !errors.Is(err, jobs.ErrDuplicate) {
					slog.Warn("no se pudo encolar la detección de intros", "error", err)
				}
			}()
		}
		defer func() {
			if tmdb.Configured() {
				_, _ = q.Enqueue(context.WithoutCancel(ctx), "collections_fill", map[string]string{}, "collections")
			}
			if tmdb.LogosConfigured() {
				_, _ = q.Enqueue(context.WithoutCancel(ctx), "logos_fill", map[string]string{}, "logos")
			}
		}()
		enriched := 0
		if tmdb.Configured() {
			total, _ := st.CountPendingMetadata(ctx, lib.ID)
			doneMeta := 0
			report(library.ScanProgress{Phase: "metadata", Total: total}, true)
			for {
				pending, err := st.ItemsNeedingMetadata(ctx, lib.ID, 50)
				if err != nil || len(pending) == 0 {
					break
				}
				for _, it := range pending {
					doneMeta++
					report(library.ScanProgress{Phase: "metadata", Done: doneMeta, Total: max(total, doneMeta), Item: it.Title}, false)
					ok, err := tmdb.Enrich(ctx, it)
					if err != nil {
						slog.Warn("TMDB no disponible; se reintentará en el próximo escaneo", "error", err)
						return map[string]any{"scan": res, "enriched": enriched}, nil
					}
					if ok {
						enriched++
					} else {
						_ = st.MarkMetadataTried(ctx, it.ID)
					}
				}
			}
			// Series cuya identificación cambió: sus temporadas y episodios dejan los datos de la anterior.
			if stale, err := st.SeriesWithStaleChildren(ctx, lib.ID); err == nil {
				for _, id := range stale {
					if _, _, err := tmdb.RefreshSeriesChildren(ctx, id); err != nil {
						slog.Warn("TMDB no disponible; las temporadas se completarán en el próximo escaneo", "error", err)
						return map[string]any{"scan": res, "enriched": enriched}, nil
					}
				}
			}
			// Temporadas y episodios (E2), una petición por temporada, cuando su serie ya tiene ficha.
			seasons, _ := st.CountPendingSeasons(ctx, lib.ID)
			total += seasons
			for {
				pending, err := st.SeasonsNeedingMetadata(ctx, &lib.ID, nil, 50)
				if err != nil || len(pending) == 0 {
					break
				}
				for _, p := range pending {
					doneMeta++
					report(library.ScanProgress{Phase: "metadata", Done: doneMeta, Total: max(total, doneMeta),
						Item: p.SeriesTitle + " · " + library.SeasonTitle(p.Number)}, false)
					if _, err := tmdb.EnrichSeason(ctx, p); err != nil {
						slog.Warn("TMDB no disponible; las temporadas se completarán en el próximo escaneo", "error", err)
						return map[string]any{"scan": res, "enriched": enriched}, nil
					}
				}
			}
			for {
				pending, err := st.ItemsNeedingGenres(ctx, lib.ID, 50)
				if err != nil || len(pending) == 0 {
					break
				}
				for _, it := range pending {
					doneMeta++
					report(library.ScanProgress{Phase: "metadata", Done: doneMeta, Total: max(total, doneMeta), Item: it.Title}, false)
					if err := tmdb.FillGenres(ctx, it); err != nil {
						slog.Warn("TMDB no disponible; los géneros se completarán en el próximo escaneo", "error", err)
						return map[string]any{"scan": res, "enriched": enriched}, nil
					}
				}
			}
		}
		if lib.Kind == "tv" { // E2: intro y créditos de los episodios nuevos, en su propio carril
			if _, err := q.Enqueue(ctx, "markers_detect", map[string]string{}, "markers"); err != nil && !errors.Is(err, jobs.ErrDuplicate) {
				slog.Warn("no se pudo encolar la detección de intros", "error", err)
			}
		}
		return map[string]any{"scan": res, "enriched": enriched}, nil
	}
}

// scanAllJob encola un escaneo por biblioteca (tarea programada cada 6 h).
func scanAllJob(st *store.Store, q *jobs.Queue) jobs.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		libs, err := st.Libraries(ctx)
		if err != nil {
			return nil, err
		}
		for _, l := range libs {
			if l.RetiredAt != nil {
				continue
			}
			if _, err := q.Enqueue(ctx, "library_scan", map[string]string{"library_id": l.ID.String()}, "scan:"+l.ID.String()); err != nil &&
				!errors.Is(err, jobs.ErrDuplicate) {
				return nil, err
			}
		}
		return map[string]int{"libraries": len(libs)}, nil
	}
}

func migrate(ctx context.Context, cfg config.Config, args []string) error {
	action := "up"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "up":
		return db.Migrate(ctx, cfg.DatabaseURL)
	case "down":
		return db.MigrateDown(ctx, cfg.DatabaseURL)
	case "status":
		cur, latest, err := db.SchemaVersion(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		fmt.Printf("esquema aplicado: %d · última disponible: %d\n", cur, latest)
		return nil
	}
	return fmt.Errorf("acción de migración desconocida: %s", action)
}

// itemRefreshJob vuelve a pedir a TMDB la ficha de una película (I1, «Actualizar información»): mode «missing» completa
// solo lo vacío; «all» vuelve a descargar lo no bloqueado. Una ficha bloqueada entera no se toca.
func itemRefreshJob(st *store.Store, tmdb *metadata.TMDB) jobs.Handler {
	return func(ctx context.Context, payload json.RawMessage) (any, error) {
		var p struct {
			ItemID uuid.UUID `json:"item_id"`
			Mode   string    `json:"mode"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		it, locked, err := st.ItemForRefresh(ctx, p.ItemID)
		if err != nil {
			return nil, err
		}
		if locked {
			return map[string]string{"omitido": "ficha bloqueada"}, nil
		}
		if !tmdb.Configured() {
			return map[string]string{"omitido": "sin clave de TMDB"}, nil
		}
		ok, err := tmdb.Refresh(ctx, it, p.Mode == "missing")
		if err != nil {
			return nil, err
		}
		if !ok {
			return map[string]string{"omitido": "TMDB no la reconoce"}, nil
		}
		if it.Kind == "series" { // E2: también sus temporadas y episodios
			if err := st.ResetSeasonMetadata(ctx, it.ID); err != nil {
				return nil, err
			}
			if _, _, err := tmdb.RefreshSeriesChildren(ctx, it.ID); err != nil {
				return nil, err
			}
		}
		return map[string]string{"actualizada": p.Mode}, nil
	}
}

// seriesChildrenJob pone al día las temporadas y episodios de una serie tras cambiar su identificación (I2 + E2).
func seriesChildrenJob(tmdb *metadata.TMDB) jobs.Handler {
	return func(ctx context.Context, payload json.RawMessage) (any, error) {
		var p struct {
			SeriesID uuid.UUID `json:"series_id"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, err
		}
		cleared, enriched, err := tmdb.RefreshSeriesChildren(ctx, p.SeriesID)
		if err != nil {
			return nil, err
		}
		return map[string]int{"limpiadas": cleared, "temporadas": enriched}, nil
	}
}

// La detección de intros y créditos se hace por tandas: cada trabajo analiza temporadas durante markersBudget y deja
// el resto para el siguiente (lo encola el próximo escaneo de series, como mucho cada 6 h, o se continúa solo).
const (
	markersTimeout = 2 * time.Hour
	markersBudget  = 90 * time.Minute
)

// markersJob detecta la intro y los créditos de las temporadas pendientes comparando el audio de sus episodios.
func markersJob(st *store.Store, roots []string) jobs.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		// El plazo de la tanda cuenta también las esperas por reproducción: el trabajo termina limpiamente antes de su
		// tiempo máximo (lo leído queda guardado) y la tarea de cada hora sigue con el resto.
		started := time.Now()
		var total markers.Summary
		for {
			left := markersBudget - time.Since(started)
			if left <= 0 {
				return total, nil
			}
			paused, playing, err := st.MarkerPauseState(ctx)
			if err != nil {
				return nil, err
			}
			if paused {
				return map[string]bool{"paused": true}, nil
			}
			if playing {
				jobs.ReportProgress(ctx, library.ScanProgress{Phase: "waiting_playback"})
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(2 * time.Second):
					continue
				}
			}
			work, cancel := context.WithCancel(ctx)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				tick := time.NewTicker(2 * time.Second)
				defer tick.Stop()
				for {
					select {
					case <-work.Done():
						return
					case <-tick.C:
						p, b, e := st.MarkerPauseState(work)
						if e == nil && (p || b) {
							cancel()
							return
						}
					}
				}
			}()
			result, err := markers.RunPending(work, st, roots, left, func(done, total int, item string) {
				jobs.ReportProgress(ctx, library.ScanProgress{Phase: "markers", Done: done, Total: total, Item: item})
			})
			total.Seasons += result.Seasons
			total.Files += result.Files
			total.Found += result.Found
			total.Left = result.Left
			interrupted := work.Err() != nil
			cancel()
			<-stopped
			if interrupted && ctx.Err() == nil {
				continue
			}
			return total, err
		}
	}
}
