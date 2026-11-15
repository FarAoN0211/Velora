package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestThumbnailPreservesAlphaAndBounds(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1000, 400))
	src.SetNRGBA(500, 200, color.NRGBA{R: 255, A: 128})
	var b bytes.Buffer
	_ = png.Encode(&b, src)
	result, err := Thumbnail(b.Bytes(), "logo", 320)
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 320 || result.Height != 128 || result.Ext != "png" {
		t.Fatalf("wrong thumbnail: %+v", result)
	}
	decoded, _, err := image.Decode(bytes.NewReader(result.Data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, a := decoded.At(0, 0).RGBA()
	if a != 0 {
		t.Fatal("logo transparency lost")
	}
	if _, err = Thumbnail([]byte("not an image"), "poster", 320); err == nil {
		t.Fatal("invalid image accepted")
	}
}
