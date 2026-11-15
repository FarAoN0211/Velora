package library

import "testing"

func TestParseName(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
		tmdb  string
		imdb  string
	}{
		{"Origen (2010)", "Origen", 2010, "", ""},
		{"Origen (2010) {tmdb-27205}", "Origen", 2010, "27205", ""},
		{"Casino Royale (2006) [tmdbid=36557]", "Casino Royale", 2006, "36557", ""},
		{"Dune.Part.Two.2024.2160p.UHD.BluRay.x265-GRP", "Dune Part Two", 2024, "", ""},
		{"2001 Una odisea del espacio (1968)", "2001 Una odisea del espacio", 1968, "", ""},
		{"Blade Runner 2049 (2017) {imdb-tt1856101}", "Blade Runner 2049", 2017, "", "tt1856101"},
		{"Amélie", "Amélie", 0, "", ""},
		{"El viaje de Chihiro (2001) 1080p Castellano", "El viaje de Chihiro", 2001, "", ""},
		{"Spanish Affair 2 (2015)", "Spanish Affair 2", 2015, "", ""},
		{"The Spanish Prisoner (1997)", "The Spanish Prisoner", 1997, "", ""},
		{"Latino Americano.2019.1080p.WEB-DL", "Latino Americano", 2019, "", ""},
		{"Multi Mundos 1080p", "Multi Mundos", 0, "", ""},
		{"4K Restoration (2020)", "4K Restoration", 2020, "", ""},
	}
	for _, c := range cases {
		p := ParseName(c.in)
		if p.Title != c.title || p.Year != c.year || p.ProviderIDs["tmdb"] != c.tmdb || p.ProviderIDs["imdb"] != c.imdb {
			t.Errorf("%q → %+v; esperaba %q %d tmdb=%q imdb=%q", c.in, p, c.title, c.year, c.tmdb, c.imdb)
		}
	}
}

func TestIsVideoAndContentPath(t *testing.T) {
	yes := []string{"Origen (2010)/Origen (2010).mkv", "Suelta.mp4", "Matrix (1999)/Matrix (1999) - 4K.mkv"}
	no := []string{"Origen (2010)/Extras/Entrevista.mkv", "Origen (2010)/origen-trailer.mkv", "Origen (2010)/poster.jpg",
		"Origen (2010)/Sample/sample.mkv", "Origen (2010)/origen.srt"}
	for _, p := range yes {
		if !IsVideo(p) {
			t.Errorf("%s debería ser vídeo principal", p)
		}
	}
	for _, p := range no {
		if IsVideo(p) {
			t.Errorf("%s no debería ser vídeo principal", p)
		}
	}
	if ContentPath("Origen (2010)/Origen (2010).mkv") != "Origen (2010)" || ContentPath("Suelta.mp4") != "Suelta.mp4" {
		t.Error("ContentPath incorrecto")
	}
	if VersionLabel("Matrix (1999)", "Matrix (1999)/Matrix (1999) - 4K.mkv") != "4K" {
		t.Error("VersionLabel incorrecto")
	}
}

func TestSortTitle(t *testing.T) {
	if SortTitle("El viaje de Chihiro") != "viaje de chihiro" || SortTitle("The Matrix") != "matrix" || SortTitle("Ella") != "ella" {
		t.Error("SortTitle incorrecto")
	}
}

func TestExtraKind(t *testing.T) {
	cases := map[string]string{
		"Origen (2010)/trailer.mkv":                      "trailer",
		"Origen (2010)/Trailer.MP4":                      "trailer",
		"Origen (2010)/trailer2.mkv":                     "trailer",
		"Origen (2010)/trailer-1.mkv":                    "trailer",
		"Origen (2010)/trailer.f137.mp4":                 "trailer",
		"Origen (2010)/Origen (2010)-trailer.mkv":        "trailer",
		"Origen (2010)/origen-featurette2.mkv":           "featurette",
		"Origen (2010)/origen-behindthescenes.mkv":       "behindthescenes",
		"Origen (2010)/origen-other.mkv":                 "extra",
		"Origen (2010)/Trailers/teaser.mkv":              "trailer",
		"Origen (2010)/Behind The Scenes/rodaje.mkv":     "behindthescenes",
		"Origen (2010)/Extras/entrevista.mkv":            "extra",
		"Origen (2010)/Deleted Scenes/escena.mkv":        "deleted",
		"Origen (2010)/Origen (2010).mkv":                "", // la película
		"Origen (2010)/origen-sample.mkv":                "", // muestra: se ignora
		"Origen (2010)/Sample/sample.mkv":                "",
		"Origen (2010)/.trailer.mkv":                     "", // oculto
		"Origen (2010)/trailer.f137.mp4.part":            "", // descarga a medias
		"Origen (2010)/trailer.srt":                      "",
		"Origen (2010)/Extras/Más/otra.mkv":              "", // demasiado profundo
		"trailer.mkv":                                    "", // raíz de la biblioteca: ambiguo
		"Trailer Park Boys (2006)/Trailer Park Boys.mkv": "",
	}
	for rel, want := range cases {
		if got := ExtraKind(rel); got != want {
			t.Errorf("ExtraKind(%q) = %q; esperaba %q", rel, got, want)
		}
	}
	// Ningún extra es película, y un título que empieza por «Trailer» sí lo es.
	for _, rel := range []string{"Origen (2010)/trailer.mkv", "Origen (2010)/trailer.f137.mp4", "Origen (2010)/Trailers/teaser.mkv", "Origen (2010)/.oculto.mkv"} {
		if IsVideo(rel) {
			t.Errorf("%s no debería ser película", rel)
		}
	}
	if !IsVideo("Trailer Park Boys (2006)/Trailer Park Boys.mkv") {
		t.Error("«Trailer Park Boys» es una película")
	}
}
