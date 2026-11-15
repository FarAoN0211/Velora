package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"velora/internal/db"
	"velora/internal/store"
)

func testDB(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("VELORA_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("VELORA_TEST_DATABASE_URL no definida")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("velora_bk_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "drop database if exists "+pgx.Identifier{name}.Sanitize()+" with (force)")
		admin.Close(context.Background())
	})
	if err := db.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, u.String()
}

// E0.1, punto 4: un usuario creado mientras se hace la copia no la invalida; la copia refleja la instantánea.
func TestBackupConsistentWithSnapshot(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into users (username, display_name) values ('ana', 'Ana'), ('luis', 'Luis')`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := &Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, Keep: 7, KeepWeeks: 4}
	s.afterSnapshot = func(ctx context.Context) {
		if _, err := pool.Exec(ctx, `insert into users (username, display_name) values ('nuevo', 'Nuevo')`); err != nil {
			t.Errorf("insertar durante la copia: %v", err)
		}
	}
	meta, err := s.Run(ctx)
	if err != nil {
		t.Fatalf("la copia debería verificarse aunque la base cambie durante el volcado: %v", err)
	}
	if !meta.Verified || meta.Users != 2 {
		t.Fatalf("la copia debería tener los 2 usuarios de la instantánea: %+v", meta)
	}
	var live int
	_ = pool.QueryRow(ctx, `select count(*) from users`).Scan(&live)
	if live != 3 {
		t.Fatalf("la base viva debería tener 3 usuarios, tiene %d", live)
	}

	// E0.1, punto 5: el volcado nace con 0640 (nunca legible por "otros").
	st, err := os.Stat(filepath.Join(dir, meta.File))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o007 != 0 {
		t.Fatalf("el volcado es accesible para otros: %o", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(dir, meta.File+".partial")); !os.IsNotExist(err) {
		t.Fatal("no debe quedar el fichero .partial")
	}
	// Ninguna base temporal de verificación queda viva.
	var leftovers int
	_ = pool.QueryRow(ctx, `select count(*) from pg_database where starts_with(datname, $1)`,
		VerifyDBPrefix(strings.TrimPrefix(mustPath(t, dbURL), "/"))).Scan(&leftovers)
	if leftovers != 0 {
		t.Fatalf("quedan %d bases temporales de verificación", leftovers)
	}
}

// E0.1, punto 5: la contraseña nunca va en los argumentos del proceso y se tacha de los errores.
func TestPgCommandKeepsPasswordOutOfArgs(t *testing.T) {
	s := &Service{DatabaseURL: "postgres://velora:s3cr3t-P4ss@db:5433/velora?sslmode=disable"}
	cmd, err := s.pgCommand(context.Background(), "pg_dump", "", "--format=custom")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "s3cr3t") {
		t.Fatalf("la contraseña aparece en los argumentos: %v", cmd.Args)
	}
	env := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"PGPASSWORD=s3cr3t-P4ss", "PGHOST=db", "PGPORT=5433", "PGUSER=velora", "PGDATABASE=velora", "PGSSLMODE=disable"} {
		if !strings.Contains(env, want) {
			t.Errorf("falta %s en el entorno", want)
		}
	}
	if got := s.redact("FATAL: password s3cr3t-P4ss rejected"); strings.Contains(got, "s3cr3t") {
		t.Fatalf("el error no se ha tachado: %s", got)
	}
}

// E0.1, punto 8: el espacio exigido incluye el volcado y la base temporal de verificación.
func TestRequiredBytes(t *testing.T) {
	if got := RequiredBytes(100<<20, 30<<30); got != 200<<20+30<<30 {
		t.Fatalf("espacio exigido incorrecto: %d", got)
	}
}

// El directorio de copias no puede ser escribible por cualquiera.
func TestCheckDirRejectsWorldWritable(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	s := &Service{Dir: dir}
	if err := s.checkDir(); err == nil {
		t.Fatal("un directorio 777 debería rechazarse")
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := s.checkDir(); err != nil {
		t.Fatalf("un directorio 750 propio debería aceptarse: %v", err)
	}
}

func mustPath(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path
}

// Una copia anterior a las imágenes sigue siendo válida: se lista y restaurar no hace nada; una copia nueva no la toca.
func TestOldBackupsStayCompatible(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	dir := t.TempDir()
	old := Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, Keep: 7, KeepWeeks: 4} // como una versión anterior
	meta, err := old.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ImagesIncluded || meta.ImagesFile != "" {
		t.Fatalf("la copia antigua no debe tener imágenes: %+v", meta)
	}
	// El JSON de una copia antigua de verdad no tiene estos campos.
	jsonPath := filepath.Join(dir, strings.TrimSuffix(meta.File, ".dump")+".json")
	raw, _ := json.Marshal(map[string]any{"file": meta.File, "created_at": meta.CreatedAt, "size_bytes": meta.SizeBytes, "verified": true, "users": 0})
	if err := os.WriteFile(jsonPath, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	dumpBefore, _ := os.ReadFile(filepath.Join(dir, meta.File))
	s := Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: t.TempDir(), Keep: 7, KeepWeeks: 4}
	res, err := s.RestoreImages(ctx, meta.File)
	if err != nil || !res.Legacy || res.Restored != 0 {
		t.Fatalf("restaurar una copia antigua: %+v %v", res, err)
	}
	time.Sleep(1100 * time.Millisecond) // el nombre de la copia lleva los segundos
	if m, err := s.Run(ctx); err != nil || !m.ImagesIncluded || !m.Verified || m.ImagesFile != "" {
		t.Fatalf("copia nueva sin imágenes: %+v %v", m, err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("listado: %d %v", len(list), err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, meta.File))
	if !bytes.Equal(after, dumpBefore) {
		t.Fatal("una copia nueva ha modificado la antigua")
	}
	if rawAfter, _ := os.ReadFile(jsonPath); !bytes.Equal(rawAfter, raw) {
		t.Fatal("una restauración o una copia nueva ha modificado los datos de la copia antigua")
	}
	for _, bad := range []string{"../x.dump", "/etc/passwd", "x.json", "no-existe.dump"} {
		if _, err := s.RestoreImages(ctx, bad); err == nil {
			t.Errorf("nombre %q aceptado", bad)
		}
	}
}

// Un archivo manipulado no restaura nada: ni rutas fuera del directorio ni contenido que no sea una imagen.
func TestRestoreRejectsTamperedArchives(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	const item = "11111111-2222-3333-4444-555555555555"
	const good = item + "/user-poster-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jpg"
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 80, 120)), nil); err != nil {
		t.Fatal(err)
	}
	build := func(entries ...tar.Header) []byte {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for _, h := range entries {
			h := h
			data := jpg.Bytes()
			if h.Typeflag == tar.TypeSymlink {
				data = nil
			}
			h.Size, h.Mode = int64(len(data)), 0o640
			if err := tw.WriteHeader(&h); err != nil {
				t.Fatal(err)
			}
			tw.Write(data)
		}
		tw.Close()
		return b.Bytes()
	}
	reg := func(n string) tar.Header { return tar.Header{Typeflag: tar.TypeReg, Name: n} }
	sum := func(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
	notImage := func() []byte {
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: good, Size: 5, Mode: 0o640})
		tw.Write([]byte("#!/sh"))
		tw.Close()
		return b.Bytes()
	}()
	cases := map[string][]byte{
		"salir del directorio": build(reg(good), reg("../../evil.jpg")),
		"ruta absoluta":        build(reg("/tmp/evil.jpg")),
		"otro nombre":          build(reg(item + "/poster.jpg")),
		"ejecutable":           build(reg(item + "/user-poster-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.sh")),
		"enlace simbólico":     build(tar.Header{Typeflag: tar.TypeSymlink, Name: good, Linkname: "/etc/passwd"}),
		"no es una imagen":     notImage,
	}
	for name, raw := range cases {
		dir, md := t.TempDir(), t.TempDir()
		s := Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: md}
		os.WriteFile(filepath.Join(dir, "velora-1.dump"), []byte("x"), 0o640)
		os.WriteFile(filepath.Join(dir, "velora-1.images.tar"), raw, 0o640)
		writeMeta(filepath.Join(dir, "velora-1.dump"), Meta{File: "velora-1.dump", ImagesIncluded: true, ImagesFile: "velora-1.images.tar", ImagesCount: 1, ImagesSHA256: sum(raw)})
		if _, err := s.RestoreImages(ctx, "velora-1.dump"); !errors.Is(err, ErrImagesCorrupt) {
			t.Errorf("%s: %v", name, err)
		}
		if entries, _ := os.ReadDir(md); len(entries) != 0 {
			t.Errorf("%s: ha escrito %d entradas", name, len(entries))
		}
	}
	// Resumen que no coincide: nada. Con el correcto: se restaura; repetir no cambia nada; un archivo distinto no se pisa.
	raw := build(reg(good))
	dir, md := t.TempDir(), t.TempDir()
	s := Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: md}
	os.WriteFile(filepath.Join(dir, "velora-1.dump"), []byte("x"), 0o640)
	os.WriteFile(filepath.Join(dir, "velora-1.images.tar"), raw, 0o640)
	meta := Meta{File: "velora-1.dump", ImagesIncluded: true, ImagesFile: "velora-1.images.tar", ImagesCount: 1, ImagesSHA256: "00"}
	writeMeta(filepath.Join(dir, "velora-1.dump"), meta)
	if _, err := s.RestoreImages(ctx, "velora-1.dump"); !errors.Is(err, ErrImagesCorrupt) {
		t.Fatalf("resumen distinto: %v", err)
	}
	meta.ImagesSHA256 = sum(raw)
	writeMeta(filepath.Join(dir, "velora-1.dump"), meta)
	if r, err := s.RestoreImages(ctx, "velora-1.dump"); err != nil || r.Restored != 1 {
		t.Fatalf("restaurar: %+v %v", r, err)
	}
	if r, err := s.RestoreImages(ctx, "velora-1.dump"); err != nil || r.Restored != 0 || r.Present != 1 {
		t.Fatalf("repetir: %+v %v", r, err)
	}
	target := filepath.Join(md, filepath.FromSlash(good))
	os.WriteFile(target, []byte("otro contenido"), 0o640)
	if r, err := s.RestoreImages(ctx, "velora-1.dump"); err != nil || len(r.Conflicts) != 1 {
		t.Fatalf("conflicto: %+v %v", r, err)
	}
	if cur, _ := os.ReadFile(target); string(cur) != "otro contenido" {
		t.Fatal("ha sobrescrito un archivo existente")
	}
}

// Prepara una ficha con imágenes propias en base de datos y disco. Devuelve la ficha y los nombres.
func customImages(t *testing.T, pool *pgxpool.Pool, metaDir string, kinds ...string) (uuid.UUID, []string) {
	t.Helper()
	ctx := context.Background()
	var lib, path, item uuid.UUID
	if err := pool.QueryRow(ctx, `insert into libraries (name, kind) values ('L', 'movies') returning id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into library_paths (library_id, path) values ($1, '/m') returning id`, lib).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `insert into media_items (library_id, library_path_id, kind, title, sort_title, content_path) values ($1, $2, 'movie', 'T', 't', 'T') returning id`, lib, path).Scan(&item); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range kinds {
		rel := writeJPEG(t, metaDir, item, k)
		if _, err := pool.Exec(ctx, `insert into item_images (item_id, kind, source, path) values ($1, $2, 'user', $3)`, item, k, rel); err != nil {
			t.Fatal(err)
		}
		names = append(names, rel)
	}
	return item, names
}

func writeJPEG(t *testing.T, metaDir string, item uuid.UUID, kind string) string {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 80, 120)), nil); err != nil {
		t.Fatal(err)
	}
	rel := item.String() + "/user-" + kind + "-" + uuid.NewString() + ".jpg"
	if err := os.MkdirAll(filepath.Join(metaDir, item.String()), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, rel), b.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	return rel
}

// Una imagen que ya no está en disco: la copia no se da por buena, y la rotación conserva la última completa.
func TestBackupWithMissingImageIsIncomplete(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	metaDir, dir := t.TempDir(), t.TempDir()
	_, names := customImages(t, pool, metaDir, "poster", "backdrop")
	s := &Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: metaDir, Keep: 1, KeepWeeks: 0}
	good, err := s.Run(ctx)
	if err != nil || !good.Verified || good.Incomplete || good.ImagesCount != 2 {
		t.Fatalf("copia completa: %+v %v", good, err)
	}
	if err := os.Remove(filepath.Join(metaDir, names[1])); err != nil {
		t.Fatal(err)
	}
	bad, err := s.Run(ctx)
	if !errors.Is(err, ErrImagesIncomplete) {
		t.Fatalf("una copia a la que le faltan imágenes debe avisar: %v", err)
	}
	if !bad.Incomplete || bad.ImagesCount != 1 || len(bad.ImagesMissing) != 1 || bad.ImagesMissing[0] != names[1] {
		t.Fatalf("datos de la copia incompleta: %+v", bad)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("la rotación debe conservar la última completa: %d %v", len(list), err)
	}
	var found bool
	for _, m := range list {
		if m.File == bad.File {
			found = m.Incomplete
		}
	}
	if !found {
		t.Fatalf("el listado debe indicar que está incompleta: %+v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, good.ImagesFile)); err != nil {
		t.Fatalf("archivo de la copia completa: %v", err)
	}
}

// Sustituir una imagen mientras se hace la copia no la deja incompleta: el archivo anterior no se borra hasta terminar de
// reunirlas, y una copia espera a que acabe un borrado ya empezado.
func TestImageReplacedDuringBackup(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	metaDir, dir := t.TempDir(), t.TempDir()
	item, names := customImages(t, pool, metaDir, "poster")
	st := &store.Store{Pool: pool}
	s := &Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: metaDir, Keep: 7, KeepWeeks: 4}
	var replaced string
	s.afterSnapshot = func(ctx context.Context) {
		replaced = writeJPEG(t, metaDir, item, "poster")
		old, err := st.SetUserImage(ctx, item, "poster", replaced)
		if err != nil || old != names[0] {
			t.Errorf("sustituir: %q %v", old, err)
		}
		st.RemoveImageFiles(ctx, item, metaDir, old)
		if _, err := os.Stat(filepath.Join(metaDir, old)); err != nil {
			t.Errorf("se ha borrado una imagen que la copia necesita: %v", err)
		}
	}
	meta, err := s.Run(ctx)
	if err != nil || !meta.Verified || meta.Incomplete || meta.ImagesCount != 1 {
		t.Fatalf("copia durante la sustitución: %+v %v", meta, err)
	}
	names2, err := readImages(filepath.Join(dir, meta.ImagesFile), nil)
	if err != nil || len(names2) != 1 || names2[0] != names[0] {
		t.Fatalf("el archivo debe llevar la imagen de la instantánea: %v %v", names2, err)
	}
	// Terminada la copia, la limpieza vuelve a funcionar.
	st.RemoveImageFiles(ctx, item, metaDir, names[0])
	if _, err := os.Stat(filepath.Join(metaDir, names[0])); !os.IsNotExist(err) {
		t.Fatalf("la imagen sustituida debería poder borrarse ya: %v", err)
	}
	// Un borrado en curso: la copia lo espera antes de tomar su instantánea.
	s.afterSnapshot = nil
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock_shared($1)`, db.ImageFilesLock); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("la copia no ha esperado al borrado: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Dos copias del mismo segundo no comparten archivos, y una copia que falla no se lleva el archivo de otra.
func TestBackupsInSameSecondKeepTheirOwnFiles(t *testing.T) {
	pool, dbURL := testDB(t)
	ctx := context.Background()
	metaDir, dir := t.TempDir(), t.TempDir()
	customImages(t, pool, metaDir, "poster")
	at := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	s := &Service{Pool: pool, DatabaseURL: dbURL, Dir: dir, MetadataDir: metaDir, Keep: 7, KeepWeeks: 4, clock: func() time.Time { return at }}
	first, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstTar := filepath.Join(dir, first.ImagesFile)
	before, _ := os.ReadFile(firstTar)
	beforeInfo, _ := os.Stat(firstTar)
	second, err := s.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.File == first.File || second.ImagesFile == first.ImagesFile {
		t.Fatalf("comparten nombre: %s %s", first.ImagesFile, second.ImagesFile)
	}
	afterInfo, _ := os.Stat(firstTar)
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("la segunda copia ha reemplazado el archivo de la primera")
	}
	// Con el volcado fallando después de escribir las imágenes.
	t.Setenv("PATH", "")
	if _, err := s.Run(ctx); err == nil {
		t.Fatal("sin pg_dump la copia debería fallar")
	}
	if after, _ := os.ReadFile(firstTar); !bytes.Equal(after, before) {
		t.Fatal("una copia fallida ha tocado el archivo de otra")
	}
	if _, err := os.Stat(filepath.Join(dir, second.ImagesFile)); err != nil {
		t.Fatalf("una copia fallida ha borrado el archivo de otra: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") || strings.HasSuffix(e.Name(), ".reserved") {
			t.Errorf("queda %s", e.Name())
		}
	}
	if n := len(entries); n != 6 {
		t.Errorf("debería haber solo los 3 archivos de cada una de las 2 copias: %d", n)
	}
	// El archivo de una copia que falla al publicar nunca reemplaza otro.
	if err := publish(firstTar, filepath.Join(dir, second.ImagesFile)); !errors.Is(err, os.ErrExist) {
		t.Fatalf("publicar sobre otro archivo: %v", err)
	}
}
