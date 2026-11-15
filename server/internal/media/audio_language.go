package media

import (
	"golang.org/x/text/language"
	"strings"
)

// PreferredAudio diferencia castellano y latino cuando el idioma o el título lo permiten.
// Si no hay coincidencia se mantiene la pista por defecto del archivo.
func PreferredAudio(p Probe, preferred string) int {
	if preferred == "" {
		return -1
	}
	wanted, err := language.Parse(preferred)
	if err != nil {
		return -1
	}
	base, _ := wanted.Base()
	region, _ := wanted.Region()
	best, score := -1, -10000
	for _, s := range p.Streams {
		if s.CodecType != "audio" {
			continue
		}
		tag, err := language.Parse(s.Language())
		if err != nil {
			continue
		}
		b, _ := tag.Base()
		if b != base {
			continue
		}
		r, _ := tag.Region()
		n := 100
		title := strings.ToLower(s.Tags["title"])
		if r == region {
			n += 30
		}
		if preferred == "es-ES" {
			if strings.Contains(title, "castellano") || strings.Contains(title, "castilian") || strings.Contains(title, "españa") || strings.Contains(title, "spain") {
				n += 60
			}
			if strings.Contains(title, "latin") || strings.Contains(title, "mexic") || strings.Contains(title, "latam") || r.String() == "419" || r.String() == "MX" {
				n -= 60
			}
		}
		if region.String() == "419" && (strings.Contains(title, "latin") || strings.Contains(title, "latam")) {
			n += 60
		}
		if s.Disposition["default"] == 1 {
			n += 5
		}
		if s.Disposition["comment"] == 1 || s.Disposition["visual_impaired"] == 1 || strings.Contains(title, "comentario") || strings.Contains(title, "commentary") || strings.Contains(title, "audiodescri") {
			n -= 200
		}
		if n > score {
			best = s.Index
			score = n
		}
	}
	return best
}
