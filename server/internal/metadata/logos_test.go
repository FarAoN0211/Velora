package metadata

import "testing"

func TestLogoLanguageBeforeVotes(t *testing.T) {
	logos := []GalleryImage{{FilePath: "/en.png", Language: "en", Vote: 10}, {FilePath: "/neutral.png", Vote: 10}, {FilePath: "/es.png", Language: "es", Vote: 1}, {FilePath: "/es-es.png", Language: "es-ES", Vote: 0}}
	ranked := rankedLogos(logos)
	for i, want := range []string{"/es-es.png", "/es.png", "/neutral.png", "/en.png"} {
		if ranked[i].FilePath != want {
			t.Fatalf("%d: %+v", i, ranked)
		}
	}
	if logos[0].FilePath != "/en.png" {
		t.Fatal("modificó la galería original")
	}
}
