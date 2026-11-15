package library

import "testing"

// Nombres con los formatos habituales de descargas y bibliotecas, y casos de las convenciones de Plex/Emby.
func TestParseEpisode(t *testing.T) {
	cases := []struct {
		rel                 string
		season, number, end int
		title               string
	}{
		{"Game of Thrones/Season 1/Juego de tronos - 1x01 - Se acerca el invierno.mkv", 1, 1, 0, "Se acerca el invierno"},
		{"The Simpsons/Season 30/Los Simpson - 30x04 - La casa-árbol del terror XXIX.mkv", 30, 4, 0, "La casa-árbol del terror XXIX"},
		{"Hidden Passion/Season 1/Pasión.de.Gavilanes.2003.S01E02.1080p.VIX.Web-DL.AAC.2.0.h264-LatTeam.mkv", 1, 2, 0, ""},
		{"Narcos/Narcos S01E03 1080p NF WEB-DL DD+5.1 HDR10+ AV1-HDZ.mkv", 1, 3, 0, ""},
		{"The Girlfriend Experience/The Girlfriend Experience S01E13 [AMZN WEB-DL 1080p AVC ES-EN DD+ 5.1 Subs][HDO].mkv", 1, 13, 0, ""},
		{"Merlin/Season 02/Merlin S02E05 - El último dragón.mkv", 2, 5, 0, "El último dragón"},
		{"Serie/Season 1/Serie S01E01-E02 - Piloto.mkv", 1, 1, 2, "Piloto"},
		{"Serie/Season 1/Serie.S01E03E04.Doble.mkv", 1, 3, 4, "Doble"},
		{"Serie/Specials/Serie S00E01 - Especial de Navidad.mkv", 0, 1, 0, "Especial de Navidad"},
		{"Serie/Specials/Serie S02E01 - Rodaje.mkv", 0, 1, 0, "Rodaje"}, // la carpeta de especiales manda
		{"Serie/Temporada 3/03x07 - El regreso.mp4", 3, 7, 0, "El regreso"},
		{"Serie/Season 2/Episodio sin número.mkv", 2, 0, 0, "Episodio sin número"},
		{"The Final Problem/El.problema.final.2026.T1.Web-Dl.1080.x264.E-AC3.Spanish.Subs.mkv", 1, 0, 0, "El problema final"},
		{"Serie/Serie 1920x1080 S01E09.mkv", 1, 9, 0, ""},
	}
	for _, c := range cases {
		got := ParseEpisode(c.rel)
		if got.Season != c.season || got.Number != c.number || got.NumberEnd != c.end || got.Title != c.title {
			t.Errorf("%s: T%d E%d-%d %q; se esperaba T%d E%d-%d %q", c.rel, got.Season, got.Number, got.NumberEnd, got.Title,
				c.season, c.number, c.end, c.title)
		}
	}
}

func TestIsEpisodeFileAndSeriesExtras(t *testing.T) {
	episodes := []string{
		"Narcos/Narcos S01E01 1080p.mkv",
		"Merlin/Season 01/Merlin S01E01.mkv",
		"Carrie/Season 1/Carrie - Season 1/Carrie S01E01.mkv",
		"Serie/Specials/Serie S00E01.mkv",
		"Serie/Temporada 2/2x01.mkv",
		"Serie/Season 1/Serie S01E04 - Interview.mkv", // un episodio, aunque el título parezca un extra
	}
	for _, rel := range episodes {
		if !IsEpisodeFile(rel) {
			t.Errorf("%s debería ser un episodio", rel)
		}
	}
	notEpisodes := []string{
		"Serie/trailer.mkv", "Serie/Serie-trailer.mkv", "Serie/Trailers/Avance.mkv", "Serie/Season 1/sample.mkv",
		"Serie/Season 1/Serie S01E01-sample.mkv", "Serie/Season 1/Extras/Rodaje.mkv", "Serie/Notas.txt", "Serie/.oculto.mkv",
		"Serie/Fotos/Viaje.mkv",
	}
	for _, rel := range notEpisodes {
		if IsEpisodeFile(rel) {
			t.Errorf("%s no debería ser un episodio", rel)
		}
	}
	extras := map[string]string{
		"Serie/trailer.mkv": "trailer", "Serie/Serie-trailer.mkv": "trailer", "Serie/Trailers/Avance.mkv": "trailer",
		"Serie/Season 1/trailer.mkv": "", "Serie/Specials/Making of.mkv": "",
	}
	for rel, want := range extras {
		if got := SeriesExtraKind(rel); got != want {
			t.Errorf("SeriesExtraKind(%s) = %q; se esperaba %q", rel, got, want)
		}
	}
}

func TestEpisodeKeys(t *testing.T) {
	if k := SeasonKey("Serie", 1); k != "Serie/#S01" {
		t.Fatal(k)
	}
	if k := EpisodeKey("Serie", Episode{Season: 1, Number: 2}, "Serie/Season 1/x.mkv"); k != "Serie/#S01E02" {
		t.Fatal(k)
	}
	if k := EpisodeKey("Serie", Episode{Season: 1, Number: 1, NumberEnd: 2}, "x.mkv"); k != "Serie/#S01E01-E02" {
		t.Fatal(k)
	}
	if k := EpisodeKey("Serie", Episode{Season: 12}, "Serie/Season 12/Raro.mkv"); k != "Serie/#S12#Raro" {
		t.Fatal(k)
	}
}
