package imaging

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.NRGBA{R: 200, A: 128})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// WebP mínimo de 128×192 sin pérdida, para comprobar que el decodificador está registrado.
const webp128x192 = "UklGRjYAAABXRUJQVlA4TCkAAAAvf8AvAAcQEf0PRET/M3/8/wMAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func TestProcessAcceptsAndRecodes(t *testing.T) {
	// Datos añadidos tras el JPEG (p. ej. un script): la salida es una imagen nueva y no los conserva.
	raw := append(jpegOf(t, 400, 600), []byte("<script>alert(1)</script>")...)
	r, err := Process(raw, "poster")
	if err != nil || r.Ext != "jpg" || r.Width != 400 || r.Height != 600 {
		t.Fatalf("cartel: %+v %v", r, err)
	}
	if bytes.Contains(r.Data, []byte("<script>")) {
		t.Fatal("la salida conserva datos añadidos al archivo")
	}
	if r, err = Process(pngOf(t, 800, 200), "logo"); err != nil || r.Ext != "png" {
		t.Fatalf("logotipo: %+v %v", r, err)
	}
	dec, _, err := image.Decode(bytes.NewReader(r.Data))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := dec.At(1, 1).RGBA(); a == 0xffff {
		t.Fatal("el logotipo ha perdido la transparencia")
	}
	if r, err = Process(jpegOf(t, 5000, 2812), "backdrop"); err != nil || r.Width != 3840 {
		t.Fatalf("una imagen enorme debe reducirse: %+v %v", r.Width, err)
	}
	w, err := base64.StdEncoding.DecodeString(webp128x192)
	if err != nil {
		t.Fatal(err)
	}
	if r, err = Process(w, "poster"); err != nil || r.Ext != "jpg" {
		t.Fatalf("webp: %+v %v", r.Ext, err)
	}
}

func TestProcessRejects(t *testing.T) {
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 200, 300), []color.Color{color.Black}), nil)
	bomb := pngOf(t, 100, 150)
	// Cabecera PNG manipulada (con CRC correcto): declara 60000×60000 sin ocupar nada. Se rechaza sin llegar a decodificar.
	binary.BigEndian.PutUint32(bomb[16:], 60000)
	binary.BigEndian.PutUint32(bomb[20:], 60000)
	binary.BigEndian.PutUint32(bomb[29:], crc32.ChecksumIEEE(bomb[12:29]))
	for name, c := range map[string]struct {
		raw  []byte
		kind string
		want error
	}{
		"texto con extensión de imagen": {[]byte("#!/bin/sh\nrm -rf /\n"), "poster", ErrFormat},
		"ejecutable ELF":                {append([]byte("\x7fELF"), make([]byte, 200)...), "poster", ErrFormat},
		"SVG":                           {[]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`), "logo", ErrFormat},
		"GIF no admitido":               {g.Bytes(), "poster", ErrFormat},
		"JPEG truncado":                 {jpegOf(t, 400, 600)[:40], "poster", ErrFormat},
		"demasiados bytes":              {make([]byte, MaxUploadBytes+1), "poster", ErrTooLarge},
		"bomba de descompresión":        {bomb, "poster", ErrDimensions},
		"demasiado pequeña":             {jpegOf(t, 20, 30), "poster", ErrDimensions},
		"horizontal como cartel":        {jpegOf(t, 1280, 720), "poster", ErrRatio},
		"vertical como fondo":           {jpegOf(t, 400, 600), "backdrop", ErrRatio},
		"vertical como destacada":       {jpegOf(t, 400, 600), "banner", ErrRatio},
		"vertical como miniatura":       {jpegOf(t, 400, 600), "thumb", ErrRatio},
		"tipo desconocido":              {jpegOf(t, 400, 600), "../../etc/passwd", ErrKind},
	} {
		if _, err := Process(c.raw, c.kind); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, esperaba %v", name, err, c.want)
		}
	}
}
