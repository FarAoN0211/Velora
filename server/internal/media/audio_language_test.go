package media

import "testing"

func TestPreferredAudioLanguage(t *testing.T) {
	p := Probe{Streams: []Stream{
		{Index: 1, CodecType: "audio", Tags: map[string]string{"language": "eng"}, Disposition: map[string]int{"default": 1}},
		{Index: 2, CodecType: "audio", Tags: map[string]string{"language": "spa", "title": "Español latino"}},
		{Index: 3, CodecType: "audio", Tags: map[string]string{"language": "spa", "title": "Castellano"}},
		{Index: 4, CodecType: "audio", Tags: map[string]string{"language": "spa", "title": "Castellano audiodescripción"}},
	}}
	for _, c := range []struct {
		lang string
		want int
	}{{"es-ES", 3}, {"es-419", 2}, {"en", 1}, {"fr", -1}, {"", -1}} {
		if got := PreferredAudio(p, c.lang); got != c.want {
			t.Errorf("%s: %d, want %d", c.lang, got, c.want)
		}
	}
}
