package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Ficha de persona: datos de TMDB (biografía en inglés si no la hay en español), foto grande desde el mismo origen y en
// caché, e identificadores manipulados rechazados.
func TestPersonDetailsAndPhoto(t *testing.T) {
	var photo bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 421, 632))
	for y := 0; y < 632; y++ {
		for x := 0; x < 421; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 90, 255})
		}
	}
	if err := jpeg.Encode(&photo, img, nil); err != nil {
		t.Fatal(err)
	}
	var photos atomic.Int32
	fake := http.NewServeMux()
	fake.HandleFunc("/3/configuration", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) })
	fake.HandleFunc("/3/person/31", func(w http.ResponseWriter, r *http.Request) {
		bio := ""
		if r.URL.Query().Get("language") == "en-US" {
			bio = "An American actor."
		}
		w.Write([]byte(`{"id":31,"name":"Tom Hanks","biography":"` + bio + `","birthday":"1956-07-09","deathday":null,
			"place_of_birth":"Concord, California, USA","profile_path":"/tom.jpg","known_for_department":"Acting"}`))
	})
	fake.HandleFunc("/3/person/32", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":32,"name":"Sin foto","biography":"Biografía en español.","profile_path":null}`))
	})
	fake.HandleFunc("/img/h632/tom.jpg", func(w http.ResponseWriter, r *http.Request) { photos.Add(1); w.Write(photo.Bytes()) })
	ts := httptest.NewServer(fake)
	defer ts.Close()

	e := newE1Env(t, ts.URL)
	if c := e.call(t, "GET", "/api/v1/people/31", e.token, nil, nil); c != 503 {
		t.Fatalf("sin clave de TMDB debería dar 503: %d", c)
	}
	if c := e.call(t, "PUT", "/api/v1/admin/metadata/tmdb", e.token, map[string]string{"api_key": "clave"}, nil); c != 200 {
		t.Fatalf("guardar la clave: %d", c)
	}
	var got struct {
		Person struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			Biography    string `json:"biography"`
			Birthday     string `json:"birthday"`
			Deathday     string `json:"deathday"`
			PlaceOfBirth string `json:"place_of_birth"`
			HasPhoto     bool   `json:"has_photo"`
		} `json:"person"`
	}
	if c := e.call(t, "GET", "/api/v1/people/31", e.token, nil, &got); c != 200 {
		t.Fatalf("ficha: %d", c)
	}
	if p := got.Person; p.Name != "Tom Hanks" || p.Biography != "An American actor." || p.Birthday != "1956-07-09" || p.Deathday != "" ||
		p.PlaceOfBirth != "Concord, California, USA" || !p.HasPhoto {
		t.Fatalf("ficha inesperada: %+v", p)
	}
	if c := e.call(t, "GET", "/api/v1/people/32", e.token, nil, &got); c != 200 || got.Person.Biography != "Biografía en español." || got.Person.HasPhoto {
		t.Fatalf("persona sin foto: %d %+v", c, got.Person)
	}
	for i := 0; i < 2; i++ {
		res := e.raw(t, "GET", "/api/v1/people/31/photo", map[string]string{"Authorization": "Bearer " + e.token})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" || len(b) < 100 {
			t.Fatalf("foto %d: %d %s %d bytes", i, res.StatusCode, res.Header.Get("Content-Type"), len(b))
		}
		if cfg, err := jpeg.DecodeConfig(bytes.NewReader(b)); err != nil || cfg.Width > 500 {
			t.Fatalf("foto %d no es un JPEG de 500 px como mucho: %+v %v", i, cfg, err)
		}
	}
	if n := photos.Load(); n != 1 {
		t.Fatalf("la segunda vez la foto sale de la caché: se pidió %d veces a TMDB", n)
	}
	for _, path := range []string{"/api/v1/people/32/photo", "/api/v1/people/0", "/api/v1/people/031", "/api/v1/people/abc", "/api/v1/people/-1/photo"} {
		if c := e.call(t, "GET", path, e.token, nil, nil); c != 404 {
			t.Errorf("%s debería dar 404: %d", path, c)
		}
	}
	if c := e.call(t, "GET", "/api/v1/people/31", "", nil, nil); c != 401 {
		t.Fatalf("sin sesión debería dar 401: %d", c)
	}
}
