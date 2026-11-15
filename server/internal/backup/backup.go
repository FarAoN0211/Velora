// Package backup hace copias de PostgreSQL y las verifica restaurándolas en una base temporal (D-8, §7.9).
//
// Los secretos de /config/secrets no entran en estas copias (§8); se exportan aparte y cifrados. Las imágenes
// personalizadas de /config/metadata sí (images.go).
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velora/internal/db"
)

// Tablas que deben existir en una copia válida de E0.
var requiredTables = []string{"users", "sessions", "devices", "settings", "audit_log", "jobs", "scheduled_tasks", "goose_db_version"}

// Meta describe una copia; se guarda junto al .dump como .json.
type Meta struct {
	File          string    `json:"file"`
	CreatedAt     time.Time `json:"created_at"`
	SizeBytes     int64     `json:"size_bytes"`
	DBSizeBytes   int64     `json:"db_size_bytes"`
	SchemaVersion int64     `json:"schema_version"`
	Verified      bool      `json:"verified"`
	VerifyError   string    `json:"verify_error,omitempty"`
	Users         int64     `json:"users"`
	// Imágenes personalizadas. Una copia anterior no tiene estos campos y sigue siendo válida (ImagesIncluded = false).
	ImagesIncluded bool     `json:"images_included,omitempty"`
	ImagesFile     string   `json:"images_file,omitempty"`
	ImagesCount    int      `json:"images_count,omitempty"`
	ImagesBytes    int64    `json:"images_bytes,omitempty"`
	ImagesSHA256   string   `json:"images_sha256,omitempty"`
	ImagesMissing  []string `json:"images_missing,omitempty"` // las usa la base, pero ya no estaban en disco al copiar
	Incomplete     bool     `json:"incomplete,omitempty"`     // la base se puede restaurar, pero esas imágenes no se recuperarían
}

// Service ejecuta copias.
type Service struct {
	Pool         *pgxpool.Pool
	DatabaseURL  string
	Dir          string
	MetadataDir  string // /config/metadata; vacío: la copia no incluye imágenes
	MinFreeBytes uint64
	Keep         int // copias diarias que se conservan (7)
	KeepWeeks    int // semanas con una copia semanal (4)

	afterSnapshot func(context.Context) // solo para pruebas: cambios en la base mientras se vuelca
	clock         func() time.Time      // solo para pruebas: copias en el mismo segundo
}

// ErrNoSpace indica que la copia invadiría el margen de seguridad del disco.
var ErrNoSpace = errors.New("no hay espacio suficiente sin invadir el margen de seguridad")

// RequiredBytes es el espacio que exige una copia (E0.1, punto 8): el volcado (como mucho del tamaño de la base) más la
// base temporal de verificación (otra vez su tamaño) más el margen de seguridad. Supone que /backups y los datos de
// PostgreSQL están en el mismo disco, como en el despliegue previsto (architecture.md §10).
func RequiredBytes(dbSize int64, margin uint64) uint64 { return 2*uint64(dbSize) + margin }

// snapshot son los datos tomados en la misma instantánea que vuelca pg_dump (E0.1, punto 4).
type snapshot struct {
	id            string
	schemaVersion int64
	users         int64
}

// Run hace una copia completa: comprobar directorio y espacio → instantánea → pg_dump → verificar → rotar.
func (s *Service) Run(ctx context.Context) (Meta, error) {
	if err := s.checkDir(); err != nil {
		return Meta{}, err
	}
	var dbSize int64
	if err := s.Pool.QueryRow(ctx, `select pg_database_size(current_database())`).Scan(&dbSize); err != nil {
		return Meta{}, err
	}
	free, err := freeBytes(s.Dir)
	if err != nil {
		return Meta{}, err
	}
	if need := RequiredBytes(dbSize, s.MinFreeBytes); free < need {
		return Meta{}, fmt.Errorf("%w: libres %d MB, necesarios %d MB (volcado + base temporal) + margen %d MB",
			ErrNoSpace, free>>20, (need-s.MinFreeBytes)>>20, s.MinFreeBytes>>20)
	}

	// Transacción de solo lectura que exporta su instantánea: pg_dump vuelca exactamente lo que ve esta transacción,
	// y los recuentos de referencia se toman en ella. Los cambios posteriores no afectan a la verificación.
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return Meta{}, err
	}
	defer conn.Release()
	// Antes de la instantánea: lo que ella vea en item_images seguirá en disco hasta que se hayan reunido las imágenes.
	unlockImages := func() {}
	if s.MetadataDir != "" {
		if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, db.ImageFilesLock); err != nil {
			return Meta{}, err
		}
		locked := true
		unlockImages = func() {
			if locked {
				locked = false
				_, _ = conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, db.ImageFilesLock)
			}
		}
		defer unlockImages()
	}
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Meta{}, err
	}
	defer tx.Rollback(context.Background())
	var snap snapshot
	if err := tx.QueryRow(ctx, `select pg_export_snapshot()`).Scan(&snap.id); err != nil {
		return Meta{}, err
	}
	if err := tx.QueryRow(ctx, `select coalesce(max(version_id), 0) from goose_db_version where is_applied`).Scan(&snap.schemaVersion); err != nil {
		return Meta{}, err
	}
	if err := tx.QueryRow(ctx, `select count(*) from users`).Scan(&snap.users); err != nil {
		return Meta{}, err
	}
	var images, missingImages []string
	var imagesBytes int64
	if s.MetadataDir != "" {
		used, err := customImagePaths(ctx, tx)
		if err != nil {
			return Meta{}, err
		}
		if images, missingImages, imagesBytes, err = s.statImages(used); err != nil {
			return Meta{}, err
		}
		if need := RequiredBytes(dbSize, s.MinFreeBytes) + uint64(imagesBytes); free < need {
			return Meta{}, fmt.Errorf("%w: libres %d MB, necesarios %d MB (volcado + base temporal + imágenes) + margen %d MB",
				ErrNoSpace, free>>20, (need-s.MinFreeBytes)>>20, s.MinFreeBytes>>20)
		}
	}
	if s.afterSnapshot != nil {
		s.afterSnapshot(ctx)
	}

	now := time.Now().UTC()
	if s.clock != nil {
		now = s.clock()
	}
	name, release, err := s.reserve(now)
	if err != nil {
		return Meta{}, err
	}
	defer release()
	final := filepath.Join(s.Dir, name)
	tmp := final + ".partial"
	var imagesFile, imagesSum string
	if len(images) > 0 {
		candidate := imagesPath(final)
		if imagesSum, err = s.writeImages(candidate, images); err != nil {
			return Meta{}, fmt.Errorf("no se pudieron copiar las imágenes: %w", err)
		}
		imagesFile = candidate
	}
	unlockImages() // ya están reunidas: el volcado no necesita retener los borrados
	removeImages := func() {
		if imagesFile != "" {
			os.Remove(imagesFile) // solo el archivo de esta copia, con nombre reservado
		}
	}
	// El fichero lo crea VELORA con 0640 desde el primer byte (E0.1, punto 5): pg_dump escribe por su salida estándar.
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		removeImages()
		return Meta{}, err
	}
	dump, err := s.pgCommand(ctx, "pg_dump", "", "--format=custom", "--no-owner", "--snapshot="+snap.id)
	if err != nil {
		out.Close()
		os.Remove(tmp)
		removeImages()
		return Meta{}, err
	}
	var stderr strings.Builder
	dump.Stdout, dump.Stderr = out, &stderr
	runErr := dump.Run()
	closeErr := out.Close()
	_ = tx.Rollback(ctx) // la instantánea ya no hace falta
	if runErr != nil || closeErr != nil {
		os.Remove(tmp)
		removeImages()
		return Meta{}, fmt.Errorf("pg_dump falló: %v %v: %s", runErr, closeErr, s.redact(stderr.String()))
	}
	if err := publish(tmp, final); err != nil {
		os.Remove(tmp)
		removeImages()
		return Meta{}, err
	}
	st, err := os.Stat(final)
	if err != nil {
		return Meta{}, err
	}
	meta := Meta{File: name, CreatedAt: now, SizeBytes: st.Size(), DBSizeBytes: dbSize}
	if s.MetadataDir != "" {
		meta.ImagesIncluded, meta.ImagesCount, meta.ImagesBytes, meta.ImagesSHA256, meta.ImagesMissing = true, len(images), imagesBytes, imagesSum, missingImages
		if imagesFile != "" {
			meta.ImagesFile = filepath.Base(imagesFile)
		}
		meta.Incomplete = len(missingImages) > 0
		logMissingImages(name, missingImages)
	}

	meta.SchemaVersion, meta.Users, err = s.verify(ctx, final, snap, meta)
	if err != nil {
		meta.VerifyError = err.Error()
		slog.Error("la copia no se pudo verificar", "fichero", name, "error", err)
	} else {
		meta.Verified = true
	}
	if err := writeMeta(final, meta); err != nil {
		return meta, err
	}
	if err := s.rotate(); err != nil {
		slog.Error("no se pudieron rotar las copias", "error", err)
	}
	if !meta.Verified {
		return meta, fmt.Errorf("copia creada pero NO verificada: %s", meta.VerifyError)
	}
	if meta.Incomplete {
		return meta, fmt.Errorf("%w: faltan en disco %d imágenes personalizadas", ErrImagesIncomplete, len(meta.ImagesMissing))
	}
	return meta, nil
}

// checkDir crea el directorio de copias si falta y rechaza uno que otros usuarios puedan escribir o que no sea nuestro.
func (s *Service) checkDir() error {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	st, err := os.Stat(s.Dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s no es un directorio", s.Dir)
	}
	if st.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("el directorio de copias %s lo puede escribir cualquiera (permisos %o): corrígelo antes de copiar", s.Dir, st.Mode().Perm())
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("el directorio de copias %s pertenece a otro usuario (uid %d)", s.Dir, sys.Uid)
	}
	return nil
}

// pgCommand prepara pg_dump/pg_restore con la conexión en variables de entorno PG* (E0.1, punto 5): la contraseña nunca
// aparece en los argumentos del proceso, que cualquier usuario del sistema podría ver en /proc.
func (s *Service) pgCommand(ctx context.Context, bin, database string, args ...string) (*exec.Cmd, error) {
	u, err := url.Parse(s.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("URL de base de datos no válida")
	}
	pass, _ := u.User.Password()
	db := strings.TrimPrefix(u.Path, "/")
	if database != "" {
		db = database
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(),
		"PGHOST="+u.Hostname(), "PGPORT="+portOr(u.Port(), "5432"), "PGUSER="+u.User.Username(),
		"PGPASSWORD="+pass, "PGDATABASE="+db, "PGSSLMODE="+orDefault(u.Query().Get("sslmode"), "prefer"),
		"PGAPPNAME=velora-backup")
	return cmd, nil
}

// redact quita la contraseña de cualquier mensaje de error antes de guardarlo o mostrarlo.
func (s *Service) redact(msg string) string {
	msg = strings.TrimSpace(msg)
	if u, err := url.Parse(s.DatabaseURL); err == nil {
		if pass, ok := u.User.Password(); ok && pass != "" {
			msg = strings.ReplaceAll(msg, pass, "***")
		}
	}
	return msg
}

func portOr(p, def string) string { return orDefault(p, def) }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// verify restaura el volcado en una base temporal y lo compara con la instantánea en la que se tomó (no con la base
// viva, que puede haber cambiado mientras tanto). La base temporal se borra siempre.
func (s *Service) verify(ctx context.Context, file string, snap snapshot, meta Meta) (schemaVersion, users int64, err error) {
	// La base temporal lleva el nombre de su origen: varias instalaciones (Pruebas, Desarrollo) no se pisan.
	var current string
	if err := s.Pool.QueryRow(ctx, `select current_database()`).Scan(&current); err != nil {
		return 0, 0, err
	}
	verifyDB := VerifyDBPrefix(current) + fmt.Sprint(time.Now().UnixNano())
	if _, err := s.Pool.Exec(ctx, "create database "+pgx.Identifier{verifyDB}.Sanitize()); err != nil {
		return 0, 0, fmt.Errorf("no se pudo crear la base temporal: %w", err)
	}
	defer func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, derr := s.Pool.Exec(dropCtx, "drop database if exists "+pgx.Identifier{verifyDB}.Sanitize()+" with (force)"); derr != nil {
			slog.Error("no se pudo borrar la base temporal de verificación", "base", verifyDB, "error", derr)
		}
	}()
	restore, err := s.pgCommand(ctx, "pg_restore", verifyDB, "--no-owner", "--exit-on-error", "--dbname="+verifyDB, file)
	if err != nil {
		return 0, 0, err
	}
	if out, err := restore.CombinedOutput(); err != nil {
		return 0, 0, fmt.Errorf("pg_restore falló: %v: %s", err, s.redact(string(out)))
	}
	target, err := withDatabase(s.DatabaseURL, verifyDB)
	if err != nil {
		return 0, 0, err
	}
	conn, err := pgx.Connect(ctx, target)
	if err != nil {
		return 0, 0, fmt.Errorf("no se pudo abrir la base temporal: %s", s.redact(err.Error()))
	}
	defer conn.Close(ctx)
	for _, t := range requiredTables {
		var ok bool
		if err := conn.QueryRow(ctx, `select to_regclass($1) is not null`, "public."+t).Scan(&ok); err != nil {
			return 0, 0, err
		}
		if !ok {
			return 0, 0, fmt.Errorf("falta la tabla %s en la copia", t)
		}
	}
	if err := conn.QueryRow(ctx, `select coalesce(max(version_id), 0) from goose_db_version where is_applied`).Scan(&schemaVersion); err != nil {
		return 0, 0, err
	}
	if schemaVersion != snap.schemaVersion {
		return 0, 0, fmt.Errorf("versión de esquema de la copia %d distinta de la de la instantánea %d", schemaVersion, snap.schemaVersion)
	}
	if err := conn.QueryRow(ctx, `select count(*) from users`).Scan(&users); err != nil {
		return 0, 0, err
	}
	if users != snap.users {
		return 0, 0, fmt.Errorf("la copia tiene %d usuarios y la instantánea %d", users, snap.users)
	}
	if meta.ImagesIncluded {
		if err := verifyImages(ctx, conn, meta, imagesPath(file)); err != nil {
			return 0, 0, err
		}
	}
	return schemaVersion, users, nil
}

// VerifyDBPrefix es el prefijo de las bases temporales de verificación de una base (cabe en los 63 bytes de PostgreSQL).
func VerifyDBPrefix(db string) string {
	if len(db) > 36 {
		db = db[:36]
	}
	return db + "_vfy_"
}

// List devuelve las copias existentes, de la más reciente a la más antigua.
func (s *Service) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".dump") {
			continue
		}
		m, err := readMeta(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			m = Meta{File: e.Name(), VerifyError: "sin metadatos"}
			if info, ierr := e.Info(); ierr == nil {
				m.SizeBytes, m.CreatedAt = info.Size(), info.ModTime().UTC()
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// rotate conserva las Keep más recientes, una por semana de las últimas KeepWeeks y, siempre, la última verificada.
func (s *Service) rotate() error {
	all, err := s.List()
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for i, m := range all {
		if i < s.Keep {
			keep[m.File] = true
		}
	}
	for _, m := range all {
		if m.Verified {
			keep[m.File] = true // la última verificada nunca se borra
			break
		}
	}
	for _, m := range all {
		if m.Verified && !m.Incomplete {
			keep[m.File] = true // ni la última que tiene todas las imágenes
			break
		}
	}
	weeks := map[string]bool{}
	cutoff := time.Now().UTC().AddDate(0, 0, -7*s.KeepWeeks)
	for _, m := range all {
		if m.CreatedAt.Before(cutoff) {
			continue
		}
		y, w := m.CreatedAt.ISOWeek()
		key := fmt.Sprintf("%d-%02d", y, w)
		if !weeks[key] && m.Verified {
			weeks[key] = true
			keep[m.File] = true
		}
	}
	for _, m := range all {
		if keep[m.File] {
			continue
		}
		p := filepath.Join(s.Dir, m.File)
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		os.Remove(strings.TrimSuffix(p, ".dump") + ".json")
		os.Remove(imagesPath(p))
		slog.Info("copia antigua eliminada por rotación", "fichero", m.File)
	}
	return nil
}

func writeMeta(dumpPath string, m Meta) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(dumpPath, ".dump")+".json", raw, 0o640)
}

func readMeta(dumpPath string) (Meta, error) {
	var m Meta
	raw, err := os.ReadFile(strings.TrimSuffix(dumpPath, ".dump") + ".json")
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// DirSize suma el tamaño de los ficheros de un directorio (no recursivo).
func DirSize(dir string) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			total += info.Size()
		}
	}
	return total, nil
}

// FreeBytes expone el espacio libre del volumen de copias.
func (s *Service) FreeBytes() (uint64, error) { return freeBytes(s.Dir) }

func withDatabase(rawURL, db string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("URL de base de datos no válida: %w", err)
	}
	u.Path = "/" + db
	return u.String(), nil
}
