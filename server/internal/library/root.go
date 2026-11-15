package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathMoved indica que la carpeta de la biblioteca ya no es la que se validó al crearla (p. ej. se sustituyó por un
// enlace simbólico) o que ha quedado fuera de las raíces de medios.
var ErrPathMoved = errors.New("la carpeta de la biblioteca ha cambiado o está fuera de las carpetas de medios")

// Within indica si real (ya resuelta) está dentro de alguna raíz de medios, resolviendo también las raíces.
func Within(real string, roots []string) bool {
	for _, root := range roots {
		rootReal, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(rootReal, real); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") {
			return true
		}
	}
	return false
}

// OpenRoot abre la carpeta de una biblioteca revalidando, en cada apertura, que su ruta real sigue siendo la guardada y
// sigue dentro de las raíces de medios (EvalSymlinks resuelve también las carpetas padre). Para cerrar la carrera entre
// comprobar y abrir, tras abrir se vuelve a resolver la ruta y se exige que el directorio abierto sea el mismo (dispositivo
// e inodo) que el que hay ahora en la ruta validada: si algo se sustituyó entremedias, no se usa.
// Dentro de la carpeta, os.Root impide escapar con enlaces o con «..».
// Sin raíces configuradas (solo en pruebas unitarias) no se comprueba la pertenencia.
func OpenRoot(path string, roots []string) (*os.Root, error) {
	clean := filepath.Clean(path)
	check := func() error {
		real, err := filepath.EvalSymlinks(clean)
		if err != nil {
			return err
		}
		if real != clean || (len(roots) > 0 && !Within(real, roots)) {
			return ErrPathMoved
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(clean)
	if err != nil {
		return nil, err
	}
	opened, err1 := root.Stat(".")
	err2 := check()
	now, err3 := os.Lstat(clean)
	if err1 != nil || err2 != nil || err3 != nil || !os.SameFile(opened, now) {
		root.Close()
		return nil, ErrPathMoved
	}
	return root, nil
}
