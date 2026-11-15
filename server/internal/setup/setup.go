// Package setup gestiona el código de instalación de un solo uso (E0.1, revisión de ChatGPT, punto 1).
//
// Mientras no exista ningún administrador, el servidor genera un código aleatorio que solo se entrega por su consola
// (docker logs) y en /config/secrets/setup-code (0600). El asistente inicial (S01) lo exige y lo consume en la misma
// transacción que crea el administrador: quien llegue antes a la API no puede quedarse con el servidor sin él.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Alfabeto sin caracteres ambiguos (ni 0/O ni 1/I): 32 símbolos → 5 bits cada uno; 12 símbolos = 60 bits.
const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
const codeLen = 12

// MaxFailures es el número de intentos fallidos tras el que el código se invalida y hay que generar otro.
const MaxFailures = 10

const (
	hashKey     = "setup.code_hash"
	failuresKey = "setup.code_failures"
	lockedKey   = "setup.locked" // tras MaxFailures: solo `velora setup-code` (manual) genera otro código
	fileName    = "setup-code"
)

// ErrLocked indica que el código se invalidó por intentos fallidos y hace falta regenerarlo a mano.
var ErrLocked = errors.New("código de instalación bloqueado tras demasiados intentos: ejecuta `velora setup-code` en el servidor")

// Normalize quita guiones y espacios y pasa a mayúsculas.
func Normalize(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// Hash devuelve el SHA-256 del código normalizado (lo único que se guarda en la base de datos).
func Hash(code string) []byte {
	sum := sha256.Sum256([]byte(Normalize(code)))
	return sum[:]
}

// Format presenta el código en tres grupos: ABCD-EFGH-JKLM.
func Format(code string) string {
	c := Normalize(code)
	if len(c) != codeLen {
		return c
	}
	return c[0:4] + "-" + c[4:8] + "-" + c[8:12]
}

func newCode() (string, error) {
	raw := make([]byte, codeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, codeLen)
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)] // 256 es múltiplo de 32: sin sesgo
	}
	return string(out), nil
}

func filePath(configDir string) string { return filepath.Join(configDir, "secrets", fileName) }

// Ensure garantiza que, si falta el administrador, existe un código vigente. Devuelve el código (para mostrarlo en
// consola) o "" si el servidor ya está configurado. Con force, genera uno nuevo aunque ya existiera.
func Ensure(ctx context.Context, pool *pgxpool.Pool, configDir string, force bool) (string, error) {
	var hasAdmin bool
	if err := pool.QueryRow(ctx, `select exists (select 1 from users where is_admin)`).Scan(&hasAdmin); err != nil {
		return "", err
	}
	if hasAdmin {
		Discard(configDir)
		_, err := pool.Exec(ctx, `delete from settings where key in ($1, $2, $3)`, hashKey, failuresKey, lockedKey)
		return "", err
	}
	if !force {
		// El bloqueo persiste entre reinicios: reiniciar el servidor no da otro código (revisión de ChatGPT a E0.1).
		var locked bool
		if err := pool.QueryRow(ctx, `select exists (select 1 from settings where key = $1)`, lockedKey).Scan(&locked); err != nil {
			return "", err
		}
		if locked {
			return "", ErrLocked
		}
		if raw, err := os.ReadFile(filePath(configDir)); err == nil {
			var stored []byte
			err := pool.QueryRow(ctx, `select decode(value #>> '{}', 'hex') from settings where key = $1`, hashKey).Scan(&stored)
			if err == nil && string(stored) == string(Hash(string(raw))) {
				return Format(string(raw)), nil // el código vigente sigue siendo válido
			}
		}
	}
	code, err := newCode()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(filePath(configDir)), 0o700); err != nil {
		return "", err
	}
	tmp := filePath(configDir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(code), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filePath(configDir)); err != nil {
		return "", err
	}
	_, err = pool.Exec(ctx, `insert into settings (key, value) values ($1, to_jsonb(encode($2::bytea, 'hex'))), ($3, '0'::jsonb)
		on conflict (key) do update set value = excluded.value, updated_at = now()`, hashKey, Hash(code), failuresKey)
	if err != nil {
		return "", err
	}
	if _, err := pool.Exec(ctx, `delete from settings where key = $1`, lockedKey); err != nil {
		return "", err
	}
	return Format(code), nil
}

// Discard borra el fichero del código (tras usarlo o si el servidor ya está configurado).
func Discard(configDir string) {
	if err := os.Remove(filePath(configDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "no se pudo borrar el código de instalación: %v\n", err)
	}
}
