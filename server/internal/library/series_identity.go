package library

import (
	"golang.org/x/text/unicode/norm"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type SeriesTitleHint struct {
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	Episodes int    `json:"episodes"`
}

// SeriesTitleKey compares spelling, accents and punctuation, never translates a title.
func SeriesTitleKey(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, norm.NFD.String(title))
}

// SeriesTitleHints uses only the prefix BEFORE SxxExx/1xNN in each filename.
// Episode titles after the marker and parent/season directories are never hints.
// Callers provide one representative filename per distinct episode.
func SeriesTitleHints(names []string) []SeriesTitleHint {
	grouped := map[string]*SeriesTitleHint{}
	for _, name := range names {
		base := strings.TrimSuffix(path.Base(name), path.Ext(name))
		loc := sxxeyy.FindStringIndex(base)
		if loc == nil {
			loc = nxmm.FindStringIndex(base)
		}
		if loc == nil || loc[0] == 0 {
			continue
		}
		parsed := ParseName(strings.Trim(base[:loc[0]], " ._-"))
		key := SeriesTitleKey(parsed.Title)
		if len([]rune(key)) < 3 || key == "episode" || key == "episodio" || key == "capitulo" || key == "season" || key == "temporada" {
			continue
		}
		key += "/" + strconv.Itoa(parsed.Year)
		if hint := grouped[key]; hint != nil {
			hint.Episodes++
		} else {
			grouped[key] = &SeriesTitleHint{Title: parsed.Title, Year: parsed.Year, Episodes: 1}
		}
	}
	out := []SeriesTitleHint{}
	for _, hint := range grouped {
		out = append(out, *hint)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Episodes != out[j].Episodes {
			return out[i].Episodes > out[j].Episodes
		}
		return out[i].Title < out[j].Title
	})
	return out
}
