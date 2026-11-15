// Package artwork consulta fuentes de imágenes adicionales sin exponer credenciales al cliente.
package artwork

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Enabled   bool   `json:"enabled"`
	APIKey    string `json:"api_key"`
	ClientKey string `json:"client_key,omitempty"`
	PIN       string `json:"pin,omitempty"`
}

var configLock sync.Mutex
var Providers = []string{"fanart", "tvdb", "omdb"}

func validProvider(p string) bool {
	for _, v := range Providers {
		if p == v {
			return true
		}
	}
	return false
}
func Load(dir string) (map[string]Config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "secrets", "image-providers.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c map[string]Config
	err = json.Unmarshal(data, &c)
	return c, err
}
func Save(dir, provider string, cfg Config) error {
	if !validProvider(provider) {
		return errors.New("fuente desconocida")
	}
	configLock.Lock()
	defer configLock.Unlock()
	all, err := Load(dir)
	if err != nil {
		return err
	}
	all[provider] = cfg
	data, err := json.Marshal(all)
	if err != nil {
		return err
	}
	base := filepath.Join(dir, "secrets")
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(base, "image-providers-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(base, "image-providers.json"))
}
