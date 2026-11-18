package library

import "testing"

func TestSeriesTitleHintsUseFilenamePrefix(t *testing.T) {
	hints := SeriesTitleHints([]string{"English Folder/Season 1/Las hijas de la criada S01E01 [1080p].mkv", "English Folder/Season 1/Las.hijas.de.la.criada.S01E02.mkv", "English Folder/Season 1/S01E03 Las hijas de la criada.mkv"})
	if len(hints) != 1 || hints[0].Title != "Las hijas de la criada" || hints[0].Episodes != 2 {
		t.Fatalf("%+v", hints)
	}
	if got := SeriesTitleHints([]string{"Folder/Season 1/Episodio 01.mkv", "Folder/Season 1/S01E01 - Episode title.mkv"}); len(got) != 0 {
		t.Fatal(got)
	}
	other := SeriesTitleHints([]string{"English Folder/Serie distinta (2025) 1x02 - Piloto.mkv"})
	if len(other) != 1 || other[0].Title != "Serie distinta" || other[0].Year != 2025 {
		t.Fatal(other)
	}
	if SeriesTitleKey("La Criáda’s") != SeriesTitleKey("la criada's") {
		t.Fatal("normalización de acentos/apóstrofo")
	}
}
