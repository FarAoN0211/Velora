// Package config lee la configuración de VELORA desde variables de entorno.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config agrupa todos los ajustes de arranque. Los ajustes editables desde la interfaz viven en la tabla settings.
type Config struct {
	Listen        string        // dirección HTTP, por defecto :8640
	DatabaseURL   string        // postgres://…
	ConfigDir     string        // /config: metadatos, imágenes y secretos
	BackupDir     string        // /backups
	WebDir        string        // carpeta con la web compilada (opcional)
	SecureCookies bool          // cookies con Secure (desactivable solo en desarrollo sin HTTPS)
	MinFreeBytes  uint64        // margen de seguridad de disco (D-11): no se escribe si quedaría menos
	SessionTTL    time.Duration // caducidad deslizante de las sesiones
	Location      *time.Location
	// PublicOrigins son los orígenes exactos (esquema://host[:puerto]) desde los que se usa la web, p. ej.
	// http://192.168.1.10:8640 o https://velora.midominio.es detrás de Nginx Proxy Manager. Vacío: mismo host y esquema.
	PublicOrigins []string
	MediaRoots    []string // carpetas montadas (solo lectura) bajo las que se pueden crear bibliotecas; por defecto /media
	MaxRemux      int      // FFmpeg simultáneos para Direct Stream (gobernador mínimo de E1)
	TMDBAPIURL    string
	TMDBImageURL  string
	// TrustedProxies (VELORA_TRUSTED_PROXIES, CIDR separados por comas): solo si la conexión llega desde uno de ellos se
	// usa X-Forwarded-For para conocer la IP del cliente. Vacío (por defecto): la cabecera se ignora siempre.
	TrustedProxies []netip.Prefix
}

// Load lee el entorno y aplica los valores por defecto documentados en architecture.md §10.
func Load() (Config, error) {
	c := Config{
		Listen:        env("VELORA_LISTEN", ":8640"),
		DatabaseURL:   os.Getenv("VELORA_DATABASE_URL"),
		ConfigDir:     env("VELORA_CONFIG_DIR", "/config"),
		BackupDir:     env("VELORA_BACKUP_DIR", "/backups"),
		WebDir:        os.Getenv("VELORA_WEB_DIR"),
		SecureCookies: envBool("VELORA_SECURE_COOKIES", true),
		SessionTTL:    30 * 24 * time.Hour,
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("falta VELORA_DATABASE_URL")
	}
	gb, err := strconv.ParseFloat(env("VELORA_MIN_FREE_GB", "30"), 64)
	if err != nil || gb < 0 {
		return c, fmt.Errorf("VELORA_MIN_FREE_GB no es un número válido")
	}
	c.MinFreeBytes = uint64(gb * (1 << 30))
	for _, o := range strings.Split(os.Getenv("VELORA_PUBLIC_URLS"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			c.PublicOrigins = append(c.PublicOrigins, strings.ToLower(o))
		}
	}
	for _, r := range strings.Split(env("VELORA_MEDIA_ROOTS", "/media"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			c.MediaRoots = append(c.MediaRoots, r)
		}
	}
	if c.MaxRemux, err = strconv.Atoi(env("VELORA_MAX_REMUX", "2")); err != nil || c.MaxRemux < 1 {
		return c, fmt.Errorf("VELORA_MAX_REMUX no es un número válido")
	}
	for _, p := range strings.Split(os.Getenv("VELORA_TRUSTED_PROXIES"), ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			p += map[bool]string{true: "/32", false: "/128"}[!strings.Contains(p, ":")]
		}
		pre, err := netip.ParsePrefix(p)
		if err != nil {
			return c, fmt.Errorf("VELORA_TRUSTED_PROXIES no válido: %q", p)
		}
		c.TrustedProxies = append(c.TrustedProxies, pre.Masked())
	}
	c.TMDBAPIURL = env("VELORA_TMDB_API_URL", "https://api.themoviedb.org")
	c.TMDBImageURL = env("VELORA_TMDB_IMAGE_URL", "https://image.tmdb.org/t/p")
	loc, err := time.LoadLocation(env("VELORA_TZ", "Europe/Madrid"))
	if err != nil {
		return c, fmt.Errorf("zona horaria no válida: %w", err)
	}
	c.Location = loc
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}
