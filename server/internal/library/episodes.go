package library

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Reconocimiento de episodios (E2 S1). Convenciones habituales de Plex, Emby y Jellyfin:
//   Serie/Season 1/Serie S01E02 ….mkv      Serie/Season 01/Serie - 1x02 - Título.mkv      Serie/Serie S01E02.mkv
// y además «Temporada 1», «T1», «Specials»/«Especiales» (temporada 0) y multiepisodio «S01E01-E02» o «S01E01E02».

var (
	// S01E02, s1e2, S01.E02, S01 E02; multiepisodio S01E01-E02, S01E01E02, S01E01-02.
	sxxeyy = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,2})[ ._-]?e(\d{1,3})(?:(?:-?e|-)(\d{1,3}))?(?:[^0-9]|$)`)
	// 1x02, 01x02, 1x02-03 (no confunde 1920x1080: la temporada tiene 1 o 2 cifras y va separada).
	nxmm = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:-(\d{2,3}))?(?:[^0-9]|$)`)
	// Carpetas de temporada.
	seasonDir   = regexp.MustCompile(`(?i)^(?:season|temporada|series|staffel|saison|t|s)[ ._-]*(\d{1,3})$`)
	specialsDir = regexp.MustCompile(`(?i)^(?:specials|especiales|season[ ._-]*0+|temporada[ ._-]*0+)$`)
	// «T1» o «Temporada 1» dentro del nombre de un archivo sin número de episodio.
	seasonInName = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:t|temporada[ ._-]?)(\d{1,2})(?:[^0-9a-z]|$)`)
	bareSample   = regexp.MustCompile(`(?i)^sample([-_. ]?\d+)?$`)
	// Restos que suelen ir tras el título del episodio.
	bracketed = regexp.MustCompile(`[\[{(][^\]})]*[\]})]`)
)

// Episode es lo que se deduce de un archivo dentro de la carpeta de una serie.
type Episode struct {
	Season     int    // 0 = especiales
	Number     int    // 0 = sin número (se lista al final de su temporada)
	NumberEnd  int    // último episodio de un multiepisodio (0 si no lo es)
	Title      string // título del nombre del archivo; "" si no trae («Narcos S01E01 1080p…»)
	SeasonFrom string // "name" (del nombre), "folder" (de la carpeta) o "default" (temporada 1 por defecto)
}

// IsSeasonDir indica si una subcarpeta de la serie es de temporada (incluidos los especiales).
func IsSeasonDir(name string) bool {
	return specialsDir.MatchString(strings.TrimSpace(name)) || seasonDir.MatchString(strings.TrimSpace(name))
}

func seasonOfDir(name string) (int, bool) {
	name = strings.TrimSpace(name)
	if specialsDir.MatchString(name) {
		return 0, true
	}
	if m := seasonDir.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// IsEpisodeFile indica si rel (relativa a la ruta de la biblioteca: «Serie/…/archivo») es un episodio: un vídeo dentro
// de la carpeta de una serie, en su raíz o en una carpeta de temporada, que no sea un extra.
func IsEpisodeFile(rel string) bool {
	if !VideoExts[strings.ToLower(path.Ext(rel))] {
		return false
	}
	parts := strings.Split(rel, "/")
	base := strings.TrimSuffix(parts[len(parts)-1], path.Ext(rel))
	if strings.HasPrefix(base, ".") || bareTrailer.MatchString(base) || bareSample.MatchString(base) {
		return false
	}
	switch len(parts) {
	case 2: // Serie/archivo
	case 3: // Serie/Season 1/archivo
		if !IsSeasonDir(parts[1]) {
			return false
		}
	default:
		// Some uploaders wrap episodes in an extra directory inside a recognised season.
		// Require an explicit episode number; nested bonus material is never treated as an episode.
		if len(parts) < 4 || !IsSeasonDir(parts[1]) || !(sxxeyy.MatchString(base) || nxmm.MatchString(base)) {
			return false
		}
		for _, folder := range parts[2 : len(parts)-1] {
			if ExtraKind("Serie/"+folder+"/x.mkv") != "" {
				return false
			}
		}
	}
	// «Serie-trailer.mkv» o «-sample» no son episodios; uno con número sí lo es aunque su título acabe en «- Interview».
	if m := extrasSuffix.FindStringSubmatch(base); m != nil {
		numbered := sxxeyy.MatchString(base) || nxmm.MatchString(base)
		if !numbered || strings.EqualFold(m[1], "sample") {
			return false
		}
	}
	return true
}

// SeriesExtraKind clasifica los extras de una serie: solo los de la raíz de su carpeta («trailer.mkv», «Serie-trailer.mkv»)
// o de una subcarpeta de extras de un nivel («Serie/Trailers/x.mkv»). Nada dentro de una carpeta de temporada.
func SeriesExtraKind(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && IsSeasonDir(parts[1]) {
		return ""
	}
	return ExtraKind(rel)
}

// ParseEpisode deduce temporada, número y título de un episodio a partir de su ruta relativa («Serie/Season 1/x.mkv»).
func ParseEpisode(rel string) Episode {
	parts := strings.Split(rel, "/")
	base := strings.TrimSuffix(parts[len(parts)-1], path.Ext(rel))
	ep := Episode{Season: 1, SeasonFrom: "default"}
	if len(parts) >= 3 {
		if n, ok := seasonOfDir(parts[1]); ok {
			ep.Season, ep.SeasonFrom = n, "folder"
		}
	}
	loc, m := sxxeyy.FindStringSubmatchIndex(base), []string(nil)
	if loc == nil {
		loc = nxmm.FindStringSubmatchIndex(base)
	}
	if loc != nil {
		m = make([]string, 4)
		for i := 1; i <= 3; i++ {
			if loc[2*i] >= 0 {
				m[i] = base[loc[2*i]:loc[2*i+1]]
			}
		}
		s, _ := strconv.Atoi(m[1])
		// El nombre manda salvo que la carpeta sea de especiales (Plex: «Specials/Serie S00E01» o con su número real).
		if ep.SeasonFrom != "folder" || ep.Season != 0 {
			ep.Season, ep.SeasonFrom = s, "name"
		}
		ep.Number, _ = strconv.Atoi(m[2])
		if m[3] != "" {
			if end, _ := strconv.Atoi(m[3]); end > ep.Number {
				ep.NumberEnd = end
			}
		}
		end := loc[5] // fin del número de episodio (o del último de un multiepisodio)
		if loc[7] >= 0 {
			end = loc[7]
		}
		ep.Title = episodeTitle(base[end:])
		return ep
	}
	// Sin número de episodio («El.problema.final.2026.T1.Web-Dl.mkv»): se toma la temporada del nombre si la trae.
	if ep.SeasonFrom != "folder" {
		if sm := seasonInName.FindStringSubmatch(base); sm != nil {
			ep.Season, _ = strconv.Atoi(sm[1])
			ep.SeasonFrom = "name"
		}
	}
	ep.Title = ParseName(base).Title
	return ep
}

// episodeTitle limpia lo que va tras «S01E02»: «- Se acerca el invierno» → «Se acerca el invierno»; «1080p NF WEB-DL» → "".
func episodeTitle(rest string) string {
	rest = bracketed.ReplaceAllString(rest, " ")
	if !strings.Contains(rest, " ") { // «.Titulo.Del.Episodio.1080p» → con espacios
		rest = strings.NewReplacer(".", " ", "_", " ").Replace(rest)
	}
	if loc := junk.FindStringIndex(rest); loc != nil { // el título va siempre antes de los datos técnicos
		rest = rest[:loc[0]]
	}
	rest = strings.Trim(rest, " -._–")
	if loc := yearPattern.FindStringIndex(rest); loc != nil && loc[0] == 0 && loc[1] == len(rest) {
		rest = "" // solo un año
	}
	rest = strings.Join(strings.Fields(strings.Trim(rest, " -._–")), " ")
	if len([]rune(rest)) < 2 {
		return ""
	}
	return rest
}

// SeasonKey y EpisodeKey son las claves sintéticas (content_path) de la temporada y del episodio dentro de la serie.
func SeasonKey(series string, season int) string {
	return series + "/#S" + pad2(season)
}

// EpisodeKey: «Serie/#S01E02», «Serie/#S01E01-E02» o, sin número, «Serie/#S01#nombre del archivo».
func EpisodeKey(series string, ep Episode, file string) string {
	k := SeasonKey(series, ep.Season)
	switch {
	case ep.Number == 0:
		return k + "#" + strings.TrimSuffix(path.Base(file), path.Ext(file))
	case ep.NumberEnd > 0:
		return k + "E" + pad2(ep.Number) + "-E" + pad2(ep.NumberEnd)
	default:
		return k + "E" + pad2(ep.Number)
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// SeasonTitle es el nombre por defecto de una temporada (TMDB lo sustituye si hay ficha).
func SeasonTitle(n int) string {
	if n == 0 {
		return "Especiales"
	}
	return "Temporada " + strconv.Itoa(n)
}
