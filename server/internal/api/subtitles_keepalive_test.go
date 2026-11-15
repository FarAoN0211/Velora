package api

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"velora/internal/media"
)

// Una pista interna de una película grande tarda 30 s o más en extraerse y el reproductor de TV corta a los 30 s sin
// datos. El servidor manda la cabecera al instante y una NOTE periódica; al final, las pistas. Si falla, una NOTE y nada más.
func TestInternalSubtitleKeepsConnectionAlive(t *testing.T) {
	e, file := newTracksEnv(t)
	base := "/api/v1/stream/files/" + file.String() + "/subtitles/i3"
	oldKeep, oldExtract := subKeepAlive, extractSubtitles
	t.Cleanup(func() { subKeepAlive, extractSubtitles = oldKeep, oldExtract })
	subKeepAlive = 30 * time.Millisecond
	get := func() (int, string) {
		res := e.raw(t, "GET", base, map[string]string{"Authorization": "Bearer " + e.tokenB})
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}

	// Fallo: 200 con la cabecera y una NOTE, sin ninguna pista (y no queda en caché).
	extractSubtitles = func(ctx context.Context, f *os.File, p media.Probe, outs map[int]string) error {
		time.Sleep(80 * time.Millisecond)
		return errors.New("simulado")
	}
	if code, body := get(); code != 200 || !strings.HasPrefix(body, "WEBVTT") || strings.Contains(body, "-->") ||
		!strings.Contains(body, "NOTE No se ha podido preparar este subtítulo.") {
		t.Fatalf("fallo: %d %q", code, body)
	}

	// Extracción lenta: la cabecera llega antes de terminar, luego NOTE de espera y al final el texto.
	extractSubtitles = func(ctx context.Context, f *os.File, p media.Probe, outs map[int]string) error {
		time.Sleep(200 * time.Millisecond)
		return oldExtract(ctx, f, p, outs)
	}
	start := time.Now()
	res := e.raw(t, "GET", base, map[string]string{"Authorization": "Bearer " + e.tokenB})
	head := make([]byte, 6)
	if _, err := io.ReadFull(res.Body, head); err != nil || string(head) != "WEBVTT" {
		t.Fatalf("cabecera: %q %v", head, err)
	}
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Fatalf("la cabecera debe llegar enseguida, no al terminar la extracción: %v", d)
	}
	rest, _ := io.ReadAll(res.Body)
	res.Body.Close()
	body := string(head) + string(rest)
	if res.Header.Get("Content-Type") != "text/vtt; charset=utf-8" || !strings.Contains(body, "NOTE Preparando el subtítulo") ||
		!strings.Contains(body, "-->") || !strings.Contains(body, "Hola, esto es una prueba.") || strings.Count(body, "WEBVTT") != 1 {
		t.Fatalf("respuesta: %q", body)
	}

	// La siguiente vez sale de la caché, entera y sin NOTE.
	extractSubtitles = func(context.Context, *os.File, media.Probe, map[int]string) error {
		return errors.New("no debe llamarse")
	}
	if code, body := get(); code != 200 || strings.Contains(body, "NOTE") || !strings.Contains(body, "Hola, esto es una prueba.") {
		t.Fatalf("caché: %d %q", code, body)
	}
}

// Consultar la lista (al abrir la ficha o el reproductor) ya empieza a preparar las pistas internas de un archivo local,
// y las peticiones que llegan mientras tanto comparten esa misma extracción: el archivo se lee una sola vez.
func TestInternalSubtitlesPrewarmedOnceFromList(t *testing.T) {
	e, file := newTracksEnv(t)
	base := "/api/v1/stream/files/" + file.String() + "/subtitles"
	oldExtract := extractSubtitles
	t.Cleanup(func() { extractSubtitles = oldExtract })
	var calls atomic.Int32
	release := make(chan struct{})
	extractSubtitles = func(ctx context.Context, f *os.File, p media.Probe, outs map[int]string) error {
		calls.Add(1)
		<-release
		return oldExtract(ctx, f, p, outs)
	}
	if c := e.call(t, "GET", base, e.tokenB, nil, nil); c != 200 {
		t.Fatalf("lista: %d", c)
	}
	var wg sync.WaitGroup
	bodies := make([]string, 3)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := e.raw(t, "GET", base+"/i3", map[string]string{"Authorization": "Bearer " + e.tokenB})
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			bodies[i] = string(b)
		}(i)
	}
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("el archivo debe leerse una sola vez, se leyó %d", n)
	}
	for i, b := range bodies {
		if !strings.Contains(b, "Hola, esto es una prueba.") {
			t.Fatalf("petición %d: %q", i, b)
		}
	}
}

func TestVTTBody(t *testing.T) {
	for in, want := range map[string]string{
		"WEBVTT\n\n00:01.000 --> 00:02.000\nHola\n":              "00:01.000 --> 00:02.000\nHola\n",
		"\xef\xbb\xbfWEBVTT\r\n\r\n00:01.000 --> 00:02.000\nA\n": "00:01.000 --> 00:02.000\nA\n",
	} {
		if got := vttBody([]byte(in)); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}

// La primera frase debe llegar antes de que termine la extracción de todo el episodio.
func TestInternalSubtitleCuesArriveBeforeExtractionEnds(t *testing.T) {
	e, file := newTracksEnv(t)
	oldExtract := extractSubtitles
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	t.Cleanup(func() { extractSubtitles = oldExtract })
	extractSubtitles = func(ctx context.Context, f *os.File, p media.Probe, outs map[int]string) error {
		for _, out := range outs {
			if err := os.WriteFile(out, []byte("WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nFrase inmediata\n\n"), 0600); err != nil {
				return err
			}
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	res := e.raw(t, "GET", "/api/v1/stream/files/"+file.String()+"/subtitles/i3", map[string]string{"Authorization": "Bearer " + e.tokenB})
	defer res.Body.Close()
	received := make(chan string, 1)
	go func() {
		b := make([]byte, 4096)
		var text string
		for {
			n, err := res.Body.Read(b)
			text += string(b[:n])
			if strings.Contains(text, "Frase inmediata") || err != nil {
				received <- text
				return
			}
		}
	}()
	select {
	case text := <-received:
		if !strings.Contains(text, "Frase inmediata") {
			t.Fatal(text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("esperó al final de la extracción")
	}
	once.Do(func() { close(release) })
	rest, _ := io.ReadAll(res.Body)
	if strings.Contains(string(rest), "Frase inmediata") {
		t.Fatal("frase duplicada al terminar")
	}
}
