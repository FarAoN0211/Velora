// Package imaging valida y vuelve a codificar las imágenes que se guardan por decisión de un administrador (subidas o
// elegidas en la galería del proveedor). Lo que se guarda nunca son los bytes recibidos: se decodifican de verdad, se
// comprueban formato, tamaño y proporción, y se escribe una imagen nueva sin metadatos (EXIF, ICC ni datos añadidos).
package imaging

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registra el decodificador de WebP
)

const (
	// MaxUploadBytes es el tamaño máximo de un archivo subido.
	MaxUploadBytes = 10 << 20
	// MaxSide y MaxPixels se comprueban ANTES de decodificar: una imagen pequeña en bytes puede ocupar gigas en memoria.
	MaxSide   = 6000
	MaxPixels = 40_000_000
	minSide   = 64
)

var (
	ErrFormat     = errors.New("el archivo no es una imagen JPEG, PNG o WebP válida")
	ErrTooLarge   = errors.New("la imagen es demasiado grande")
	ErrDimensions = errors.New("la imagen es demasiado pequeña o tiene demasiados píxeles")
	ErrRatio      = errors.New("la proporción de la imagen no sirve para este uso")
	ErrKind       = errors.New("tipo de imagen desconocido")
)

// spec es lo que admite cada tipo de imagen: proporción ancho/alto y tamaño máximo guardado.
type spec struct {
	minRatio, maxRatio float64
	boxW, boxH         int
	alpha              bool // conserva la transparencia (PNG); el resto se guarda en JPEG
}

var specs = map[string]spec{
	"poster":   {0.5, 0.85, 2000, 3000, false},
	"backdrop": {1.3, 2.6, 3840, 2160, false},
	"banner":   {1.3, 8, 3840, 2160, false},
	"thumb":    {1.3, 2.4, 1920, 1080, false},
	"logo":     {0.2, 12, 2000, 2000, true},
	"art":      {0.2, 12, 3000, 3000, true},
	"disc":     {0.75, 1.35, 2000, 2000, true},
	"box":      {0.3, 2, 3000, 3000, true},
	"boxrear":  {0.3, 2, 3000, 3000, false},
	"menu":     {1.3, 2.6, 3840, 2160, false},
}

// Valid indica si el tipo de imagen se puede personalizar.
func Valid(kind string) bool { _, ok := specs[kind]; return ok }

// Result es la imagen ya procesada.
type Result struct {
	Data          []byte
	Ext           string
	Width, Height int
}

// Process valida raw para el uso kind y devuelve la imagen recodificada.
func Process(raw []byte, kind string) (Result, error) { return process(raw, kind, 0) }

// Thumbnail preserves aspect ratio and alpha while reducing transfer size.
func Thumbnail(raw []byte, kind string, width int) (Result, error) { return process(raw, kind, width) }

func process(raw []byte, kind string, width int) (res Result, err error) {
	sp, ok := specs[kind]
	if !ok {
		return res, ErrKind
	}
	if width > 0 {
		sp.boxW = min(sp.boxW, width)
		sp.boxH = min(sp.boxH, width*3)
	}
	if len(raw) > MaxUploadBytes {
		return res, ErrTooLarge
	}
	// Un decodificador no debe tumbar el servidor con un archivo malicioso.
	defer func() {
		if r := recover(); r != nil {
			res, err = Result{}, ErrFormat
		}
	}()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return res, ErrFormat
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return res, ErrFormat
	}
	if cfg.Width < minSide || cfg.Height < minSide || cfg.Width > MaxSide || cfg.Height > MaxSide || cfg.Width*cfg.Height > MaxPixels {
		return res, ErrDimensions
	}
	ratio := float64(cfg.Width) / float64(cfg.Height)
	if ratio < sp.minRatio || ratio > sp.maxRatio {
		return res, ErrRatio
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return res, ErrFormat
	}
	w, h := fit(cfg.Width, cfg.Height, sp.boxW, sp.boxH)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if !sp.alpha {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src) // JPEG no tiene transparencia
	}
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if sp.alpha {
		res.Ext = "png"
		err = png.Encode(&out, dst)
	} else {
		res.Ext = "jpg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 88})
	}
	if err != nil {
		return Result{}, fmt.Errorf("codificar: %w", err)
	}
	res.Data, res.Width, res.Height = out.Bytes(), w, h
	return res, nil
}

// fit reduce w×h para que quepa en boxW×boxH sin deformar; nunca amplía.
func fit(w, h, boxW, boxH int) (int, int) {
	if w <= boxW && h <= boxH {
		return w, h
	}
	scale := min(float64(boxW)/float64(w), float64(boxH)/float64(h))
	return max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
}
