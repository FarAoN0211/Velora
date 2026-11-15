// Package library escanea las bibliotecas (solo lectura, siempre a través de os.Root) y reconoce las películas.
package library

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// VideoExts son las extensiones que se consideran vídeo.
var VideoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".webm": true,
	".ts": true, ".m2ts": true, ".wmv": true, ".mpg": true, ".mpeg": true,
}

// Carpetas de extras que no son la película (architecture.md, convenciones de nombres; VF-012). El valor es el tipo de
// extra que se registra; "" = se ignora (muestras).
var extrasDirs = map[string]string{
	"extras": "extra", "extra": "extra", "featurettes": "featurette", "trailers": "trailer", "behind the scenes": "behindthescenes",
	"deleted scenes": "deleted", "interviews": "interview", "scenes": "scene", "shorts": "short", "sample": "", "samples": "",
	"other": "extra", "bonus": "extra", "specials": "extra",
}

// Sufijos de Plex y Emby («Película-trailer.mkv», «-featurette2»…); «-sample» se ignora.
var extrasSuffix = regexp.MustCompile(`(?i)[-_. ](trailer|sample|featurette|behindthescenes|deleted|interview|scene|short|other|extra)\d*$`)

// Tráiler suelto (convención de Jellyfin y de la colección de referencia): «trailer.mkv», «trailer2.mp4», «trailer-1.mkv»
// o restos de descargas como «trailer.f137.mp4». Nunca un título que empiece por «Trailer» («Trailer Park Boys»).
var bareTrailer = regexp.MustCompile(`(?i)^trailer([-_. ]?\d+|\.f\d+)?$`)

var suffixKind = map[string]string{"other": "extra"}

// ExtraKind clasifica un vídeo que acompaña a una película (PLAN_IDENTIFICACION_Y_TRAILERS.md, B3): devuelve el tipo
// (trailer, featurette, behindthescenes, deleted, interview, scene, short, extra) o "" si no es un extra.
// Solo dentro de la carpeta propia de una película: en su raíz o en una subcarpeta de extras de un nivel.
func ExtraKind(rel string) string {
	if !VideoExts[strings.ToLower(path.Ext(rel))] {
		return ""
	}
	parts := strings.Split(rel, "/")
	base := strings.TrimSuffix(parts[len(parts)-1], path.Ext(rel))
	if strings.HasPrefix(base, ".") {
		return ""
	}
	switch len(parts) {
	case 2: // Película/archivo
		if bareTrailer.MatchString(base) {
			return "trailer"
		}
		if m := extrasSuffix.FindStringSubmatch(base); m != nil {
			k := strings.ToLower(m[1])
			if k == "sample" {
				return ""
			}
			if v, ok := suffixKind[k]; ok {
				return v
			}
			return k
		}
	case 3: // Película/Trailers/archivo
		return extrasDirs[strings.ToLower(parts[1])]
	}
	return ""
}

// Parsed es lo que se deduce del nombre de la carpeta o del fichero.
type Parsed struct {
	Title       string
	Year        int
	ProviderIDs map[string]string // tmdb, imdb
}

var (
	idPattern   = regexp.MustCompile(`(?i)[\[{]\s*(tmdb|tmdbid|imdb|imdbid)\s*[-=]\s*([a-z0-9]+)\s*[\]}]`)
	yearPattern = regexp.MustCompile(`[(\[]?\b((?:19|20)\d{2})\b[)\]]?`)
	// Solo términos técnicos: desde el primero se descarta el resto del nombre. Las palabras de idioma («castellano»,
	// «spanish», «latino»…) no cortan nunca: forman parte de títulos reales («Spanish Affair 2», «The Spanish Prisoner») y,
	// si van tras el año, ya se descartan con él.
	junk = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|uhd|hdr10\+?|hdr|dv|dovi|remux|bluray|blu-ray|bdrip|brrip|web-?dl|webrip|hdtv|x264|x265|h\.?264|h\.?265|hevc|av1|10bit|aac|ac3|eac3|dts(-hd)?|truehd|atmos)\b`)
)

// ParseName deduce título, año e IDs de proveedor de un nombre como «Origen (2010) {tmdb-27205}».
func ParseName(name string) Parsed {
	p := Parsed{ProviderIDs: map[string]string{}}
	for _, m := range idPattern.FindAllStringSubmatch(name, -1) {
		key := strings.ToLower(strings.TrimSuffix(strings.ToLower(m[1]), "id"))
		p.ProviderIDs[key] = m[2]
	}
	name = idPattern.ReplaceAllString(name, " ")
	// El año es el último que aparezca (los títulos pueden contener números: «2001: Una odisea…»).
	if locs := yearPattern.FindAllStringSubmatchIndex(name, -1); len(locs) > 0 {
		last := locs[len(locs)-1]
		if last[0] > 0 { // un año al principio forma parte del título
			p.Year, _ = strconv.Atoi(name[last[2]:last[3]])
			name = name[:last[0]]
		}
	}
	name = stripJunk(name)
	name = strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, name)
	p.Title = strings.Join(strings.Fields(strings.Trim(name, " -([")), " ")
	if p.Title == "" {
		p.Title = strings.TrimSpace(name)
	}
	return p
}

// stripJunk corta el nombre en el primer término técnico que deje delante un título no vacío («4K Restoration» no se
// queda en nada).
func stripJunk(name string) string {
	for _, loc := range junk.FindAllStringIndex(name, -1) {
		if strings.Trim(name[:loc[0]], " .-_([") != "" {
			return name[:loc[0]]
		}
	}
	return name
}

// SortTitle quita artículos iniciales en español e inglés para ordenar.
func SortTitle(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, art := range []string{"el ", "la ", "los ", "las ", "un ", "una ", "the ", "a ", "an "} {
		if strings.HasPrefix(t, art) && len(t) > len(art) {
			t = t[len(art):]
			break
		}
	}
	return strings.TrimLeftFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// IsVideo indica si el fichero es un vídeo principal (no un extra, ni una muestra, ni un archivo oculto). Un tráiler
// nunca es película, esté donde esté.
func IsVideo(rel string) bool {
	ext := strings.ToLower(path.Ext(rel))
	if !VideoExts[ext] {
		return false
	}
	base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	if strings.HasPrefix(base, ".") || extrasSuffix.MatchString(base) || bareTrailer.MatchString(base) {
		return false
	}
	for _, part := range strings.Split(path.Dir(rel), "/") {
		if _, ok := extrasDirs[strings.ToLower(part)]; ok {
			return false
		}
	}
	return true
}

// ContentPath es la carpeta de la película: el primer componente de la ruta, o el propio fichero si está en la raíz.
func ContentPath(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return rel
}

// VersionLabel extrae la etiqueta de versión de «Película (2010) - Director's Cut.mkv».
func VersionLabel(folder, rel string) string {
	base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	if i := strings.LastIndex(base, " - "); i > 0 && strings.HasPrefix(strings.ToLower(base), strings.ToLower(folder)) {
		return strings.TrimSpace(base[i+3:])
	}
	return ""
}
