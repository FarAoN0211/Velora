// Package secrets gestiona la clave maestra de la instalación (architecture.md §8).
// Vive en /config/secrets/master.key (0600) y nunca entra en la base de datos ni en los backups normales.
package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const keyLen = 32

// EnsureMasterKey carga la clave maestra o la crea la primera vez.
func EnsureMasterKey(configDir string) ([]byte, error) {
	dir := filepath.Join(configDir, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("no se puede crear %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "master.key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != keyLen {
			return nil, fmt.Errorf("%s tiene un tamaño inesperado (%d bytes)", path, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// O_EXCL: si otro proceso la creó a la vez, no se sobrescribe.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return EnsureMasterKey(configDir)
		}
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return nil, err
	}
	return key, f.Sync()
}
