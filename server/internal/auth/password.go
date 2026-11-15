// Package auth contiene el hash de contraseñas y la emisión de tokens de sesión.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Parámetros argon2id: mínimo recomendado por OWASP (19 MiB, t=2, p=1), adaptados a la RAM del N100 (§8).
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	saltLen        = 16
	// MinPasswordLen es la longitud mínima de contraseña aceptada.
	MinPasswordLen = 10
)

var b64 = base64.RawStdEncoding

// ErrMismatch indica que la contraseña no coincide.
var ErrMismatch = errors.New("contraseña incorrecta")

// HashPassword devuelve el hash en formato PHC: $argon2id$v=19$m=…,t=…,p=…$sal$hash
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword compara en tiempo constante. Respeta los parámetros guardados en el hash.
func VerifyPassword(encoded, password string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("hash con formato desconocido")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("versión de argon2 no admitida")
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return errors.New("parámetros de argon2 no válidos")
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return err
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// dummyHash se usa para gastar el mismo tiempo cuando el usuario no existe (evita enumerar usuarios por tiempo).
var dummyHash, _ = HashPassword("velora-dummy-password")

// BurnTime ejecuta una verificación ficticia.
func BurnTime(password string) { _ = VerifyPassword(dummyHash, password) }

// NewToken genera un token opaco de 256 bits y su hash SHA-256 (lo único que se guarda).
func NewToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken calcula el hash con el que se busca una sesión.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
