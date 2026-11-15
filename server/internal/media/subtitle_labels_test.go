package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubtitleTypesAndLanguages(t *testing.T) {
	for _, tt := range []struct {
		lang, title, kind, contains string
		forced, sdh                 bool
	}{
		{"spa", "Spanish Forced", "forced", "español", false, false},
		{"eng", "English SDH", "sdh", "inglés", false, false},
		{"fra", "Français", "full", "francés", false, false},
		{"ja", "日本語 CC", "sdh", "japonés", false, false},
		{"deu", "Deutsch", "forced", "alemán", true, false},
		{"ar", "العربية", "sdh", "árabe", false, true},
		{"und", "Unknown", "full", "sin identificar", false, false},
	} {
		s := SubTrack{Language: tt.lang, Title: tt.title, Forced: tt.forced, HearingImpaired: tt.sdh}
		LabelSubtitle(&s)
		if s.Kind != tt.kind || !strings.Contains(strings.ToLower(s.Label), tt.contains) {
			t.Errorf("%+v -> %+v", tt, s)
		}
	}
}

func TestExternalHindiIsNotHearingImpaired(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"Movie.hi.srt", "Movie.en.hi.srt", "Movie.fr.sdh.srt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("subtitle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	tracks := ExternalSubs(root, "Movie.mkv")
	if len(tracks) != 3 {
		t.Fatalf("%+v", tracks)
	}
	for _, track := range tracks {
		if track.Name == "Movie.hi.srt" {
			if track.Language != "hi" || track.HearingImpaired {
				t.Errorf("Hindi: %+v", track)
			}
		} else if !track.HearingImpaired {
			t.Errorf("SDH: %+v", track)
		}
	}
}

func TestSpanishForcedDefaultsOnly(t *testing.T) {
	tracks := []SubTrack{{ID: "en", Language: "eng", Forced: true, Supported: true, Default: true}, {ID: "full", Language: "spa", Supported: true, Default: true}, {ID: "pgs", Language: "es-ES", Forced: true}, {ID: "text", Language: "spa", Forced: true, Supported: true}}
	ApplySubtitleDefaults(tracks)
	for _, s := range tracks {
		if s.Default != (s.ID == "text") {
			t.Fatalf("wrong default %+v", tracks)
		}
	}
	ApplySubtitleDefaults(tracks[:2])
	for _, s := range tracks[:2] {
		if s.Default {
			t.Fatal("no Spanish forced available", s)
		}
	}
}
