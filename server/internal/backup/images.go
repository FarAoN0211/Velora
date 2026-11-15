package backup

// Imágenes personalizadas en las copias. Las elegidas o subidas a mano (item_images.source = 'user') viven en
// /config/metadata y no se pueden volver a descargar, así que cada copia lleva junto al .dump un .images.tar con ellas,
// también con las que solo guarda una instantánea de «Deshacer». Las de TMDB no entran: se recuperan con «Actualizar».
// Los archivos de imagen nunca se reescriben (nombre único), y mientras se reúnen no se borra ninguno (db.ImageFilesLock), de
// modo que el .tar coincide con la instantánea del volcado aunque alguien sustituya una imagen en ese momento.

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // formatos que se guardan como imagen personalizada
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	imagesSuffix  = ".images.tar"
	maxImageBytes = 64 << 20 // un PNG de logotipo de 2000×2000 cabe de sobra
)

// customImageName es lo único que puede haber en un archivo de imágenes: <ficha>/user-<tipo>-<uuid>.<jpg|png>.
var customImageName = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}/user-(poster|backdrop|banner|logo|thumb)-[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}\.(jpg|png)$`)

// ErrImagesIncomplete: la base se puede restaurar, pero faltan imágenes personalizadas que no estaban en disco al copiar.
var ErrImagesIncomplete = errors.New("copia INCOMPLETA")

// ErrImagesCorrupt: el archivo de imágenes no es el de la copia o tiene entradas no válidas. No se restaura nada.
var ErrImagesCorrupt = errors.New("el archivo de imágenes de la copia no es válido")

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// customImagePaths lista las imágenes personalizadas que usa la base: las de las fichas y las de las instantáneas.
func customImagePaths(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `select path from item_images where source = 'user'
		union select e->>'path' from metadata_snapshots s cross join lateral jsonb_array_elements(s.data->'images') e where e->>'source' = 'user'
		order by 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func imagesPath(dumpPath string) string { return strings.TrimSuffix(dumpPath, ".dump") + imagesSuffix }

// statImages separa los archivos que existen de los que faltan y suma su tamaño, para comprobar el espacio antes de copiar.
func (s *Service) statImages(paths []string) (present, missing []string, total int64, err error) {
	if err := os.MkdirAll(s.MetadataDir, 0o750); err != nil {
		return nil, nil, 0, err
	}
	root, err := os.OpenRoot(s.MetadataDir)
	if err != nil {
		return nil, nil, 0, err
	}
	defer root.Close()
	for _, p := range paths {
		st, err := root.Lstat(filepath.FromSlash(p))
		if err != nil || !st.Mode().IsRegular() || !customImageName.MatchString(p) {
			missing = append(missing, p)
			continue
		}
		present = append(present, p)
		total += st.Size()
	}
	return present, missing, total, nil
}

// writeImages crea el archivo (0640 desde el primer byte, y solo se publica entero) y devuelve su resumen SHA-256.
func (s *Service) writeImages(final string, paths []string) (string, error) {
	root, err := os.OpenRoot(s.MetadataDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tmp := final + ".partial"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	sum := sha256.New()
	tw := tar.NewWriter(io.MultiWriter(out, sum))
	for _, p := range paths {
		if err := addImage(tw, root, p); err != nil {
			out.Close()
			return "", fmt.Errorf("imagen %s: %w", p, err)
		}
	}
	err = tw.Close()
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), publish(tmp, final)
}

// publish hace visible un archivo terminado sin reemplazar nunca otro: una copia existente no se puede pisar.
func publish(tmp, final string) error {
	err := os.Link(tmp, final)
	if err == nil {
		return os.Remove(tmp)
	}
	if errors.Is(err, os.ErrExist) {
		return err
	}
	if _, serr := os.Lstat(final); serr == nil { // sistema de archivos sin enlaces: se comprueba antes de renombrar
		return os.ErrExist
	}
	return os.Rename(tmp, final)
}

// reserve aparta un nombre libre para toda la copia (.dump, .json e .images.tar): dos copias del mismo segundo no comparten
// nada. Devuelve el nombre del .dump y cómo soltar la reserva.
func (s *Service) reserve(now time.Time) (string, func(), error) {
	base := "velora-" + now.Format("20060102-150405")
	for i := 0; i < 1000; i++ {
		stem := base
		if i > 0 {
			stem = fmt.Sprintf("%s-%d", base, i)
		}
		mark := filepath.Join(s.Dir, stem+".reserved")
		f, err := os.OpenFile(mark, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		f.Close()
		taken := false
		for _, suffix := range []string{".dump", ".json", imagesSuffix} {
			if _, err := os.Lstat(filepath.Join(s.Dir, stem+suffix)); err == nil {
				taken = true
			}
		}
		if taken {
			os.Remove(mark)
			continue
		}
		return stem + ".dump", func() { os.Remove(mark) }, nil
	}
	return "", nil, errors.New("no hay ningún nombre libre para la copia")
}

func addImage(tw *tar.Writer, root *os.Root, p string) error {
	f, err := root.Open(filepath.FromSlash(p))
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: p, Mode: 0o640, Size: st.Size(), ModTime: st.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// readImages recorre el archivo y comprueba cada entrada: nombre exacto, archivo normal, tamaño e imagen de verdad. Si se
// indica visit, se le pasa cada una (y su contenido) solo tras validar el archivo entero en una pasada anterior.
func readImages(file string, visit func(name string, r io.Reader) error) ([]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrImagesCorrupt, err)
		}
		if h.Typeflag != tar.TypeReg || !customImageName.MatchString(h.Name) || path.Clean(h.Name) != h.Name || h.Size <= 0 || h.Size > maxImageBytes {
			return nil, fmt.Errorf("%w: entrada no admitida", ErrImagesCorrupt)
		}
		names = append(names, h.Name)
		if visit != nil {
			if err := visit(h.Name, tr); err != nil {
				return nil, err
			}
			continue
		}
		if _, format, err := image.DecodeConfig(io.LimitReader(tr, maxImageBytes)); err != nil || (format != "jpeg" && format != "png") {
			return nil, fmt.Errorf("%w: %s no es una imagen", ErrImagesCorrupt, h.Name)
		}
	}
}

// verifyImages comprueba la copia restaurada en la base temporal: el archivo tiene exactamente las imágenes personalizadas
// que usa esa base y su contenido es el que se acaba de escribir. Las que ya faltaban en disco no impiden restaurar la base,
// pero dejan la copia marcada como incompleta (Meta.Incomplete).
func verifyImages(ctx context.Context, conn querier, meta Meta, file string) error {
	want, err := customImagePaths(ctx, conn)
	if err != nil {
		return err
	}
	var names []string
	if meta.ImagesFile != "" {
		if err := checkImagesSum(file, meta.ImagesSHA256); err != nil {
			return err
		}
		if names, err = readImages(file, nil); err != nil {
			return err
		}
	}
	for _, p := range want {
		if !slices.Contains(names, p) && !slices.Contains(meta.ImagesMissing, p) {
			return fmt.Errorf("la imagen %s de la copia no está en su archivo de imágenes", p)
		}
	}
	if len(names) != meta.ImagesCount {
		return fmt.Errorf("el archivo tiene %d imágenes y se esperaban %d", len(names), meta.ImagesCount)
	}
	return nil
}

func checkImagesSum(file, want string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("%w: el resumen no coincide", ErrImagesCorrupt)
	}
	return nil
}

// RestoreResult es lo que ha hecho RestoreImages.
type RestoreResult struct {
	Legacy    bool     `json:"legacy"`              // la copia es anterior a las imágenes: no trae ninguna, y sigue siendo válida
	Restored  int      `json:"restored"`            // archivos escritos
	Present   int      `json:"already_present"`     // ya estaban, idénticos
	Conflicts []string `json:"conflicts,omitempty"` // ya estaban y son distintos: no se tocan
	Missing   []string `json:"missing,omitempty"`   // la base las usa y no están en disco ni en la copia
}

// RestoreImages devuelve a /config/metadata las imágenes de una copia, después de restaurar su base de datos. Valida el
// archivo entero antes de escribir nada, solo escribe dentro de ese directorio (os.Root) y nunca sobrescribe. Repetirlo no
// cambia nada. No modifica la copia.
func (s *Service) RestoreImages(ctx context.Context, dumpName string) (RestoreResult, error) {
	var res RestoreResult
	if s.MetadataDir == "" {
		return res, errors.New("falta el directorio de metadatos")
	}
	if dumpName != filepath.Base(dumpName) || !strings.HasSuffix(dumpName, ".dump") {
		return res, errors.New("indica el nombre de una copia, como velora-20261106-030000.dump")
	}
	dump := filepath.Join(s.Dir, dumpName)
	if _, err := os.Stat(dump); err != nil {
		return res, fmt.Errorf("no existe la copia %s", dumpName)
	}
	meta, err := readMeta(dump)
	if err != nil {
		return res, fmt.Errorf("no se pueden leer los datos de la copia: %w", err)
	}
	if err := os.MkdirAll(s.MetadataDir, 0o750); err != nil {
		return res, err
	}
	res.Legacy = !meta.ImagesIncluded
	if meta.ImagesFile != "" {
		file := filepath.Join(s.Dir, filepath.Base(meta.ImagesFile))
		if err := checkImagesSum(file, meta.ImagesSHA256); err != nil {
			return res, err
		}
		if _, err := readImages(file, nil); err != nil {
			return res, err
		}
		root, err := os.OpenRoot(s.MetadataDir)
		if err != nil {
			return res, err
		}
		defer root.Close()
		if _, err := readImages(file, func(name string, r io.Reader) error { return restoreOne(root, name, r, &res) }); err != nil {
			return res, err
		}
	}
	// Contra la base ya restaurada: qué imágenes personalizadas siguen sin archivo.
	wanted, err := customImagePaths(ctx, s.Pool)
	if err != nil {
		return res, err
	}
	_, res.Missing, _, err = s.statImages(wanted)
	return res, err
}

func restoreOne(root *os.Root, name string, r io.Reader, res *RestoreResult) error {
	rel := filepath.FromSlash(name)
	if err := root.Mkdir(filepath.Dir(rel), 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return err
	}
	if cur, err := root.Open(rel); err == nil {
		same := false
		if raw, rerr := io.ReadAll(io.LimitReader(cur, maxImageBytes+1)); rerr == nil {
			same = sha256.Sum256(raw) == sha256.Sum256(data)
		}
		cur.Close()
		if same {
			res.Present++
		} else {
			res.Conflicts = append(res.Conflicts, name)
		}
		return nil
	}
	out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(rel)
		return err
	}
	res.Restored++
	return nil
}

func logMissingImages(name string, missing []string) {
	if len(missing) > 0 {
		slog.Error("imágenes personalizadas que faltan en disco y no entran en la copia", "copia", name, "cuantas", len(missing))
	}
}
