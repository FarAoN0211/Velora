// Package media analiza ficheros con ffprobe, decide cómo reproducirlos y los remultiplexa con FFmpeg.
//
// Seguridad: los ficheros se abren SIEMPRE con os.Root (confinados a su biblioteca) y se pasan a FFmpeg como
// descriptor heredado (/dev/fd/3). FFmpeg nunca recibe una ruta construida a partir de datos del cliente.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Stream es una pista según ffprobe.
type Stream struct {
	Index         int    `json:"index"`
	CodecType     string `json:"codec_type"`
	CodecName     string `json:"codec_name"`
	Profile       string `json:"profile"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	PixFmt        string `json:"pix_fmt"`
	ColorTransfer string `json:"color_transfer"`
	Channels      int    `json:"channels"`
	// Para las insignias técnicas (techbadges.go). Los análisis guardados antes de añadirlos no los tienen: entonces
	// la insignia correspondiente (FPS, 7.1.4) no se muestra hasta que el archivo se vuelve a analizar.
	AvgFrameRate     string            `json:"avg_frame_rate,omitempty"`
	RFrameRate       string            `json:"r_frame_rate,omitempty"`
	ChannelLayout    string            `json:"channel_layout,omitempty"`
	BitsPerRawSample string            `json:"bits_per_raw_sample,omitempty"`
	Tags             map[string]string `json:"tags"`
	Disposition      map[string]int    `json:"disposition"`
	SideData         []map[string]any  `json:"side_data_list"`
}

// Probe es el resultado resumido de ffprobe.
type Probe struct {
	Streams []Stream `json:"streams"`
	Format  struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
}

// DurationMS devuelve la duración en milisegundos.
func (p Probe) DurationMS() int64 {
	f, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return int64(f * 1000)
}

// BitrateKbps devuelve la tasa de bits total.
func (p Probe) BitrateKbps() int {
	b, _ := strconv.Atoi(p.Format.BitRate)
	return b / 1000
}

// Container normaliza el contenedor: mp4, webm, mkv, avi, ts, mov…
func (p Probe) Container() string { return NormalizeContainer(p.Format.FormatName) }

// NormalizeContainer traduce el format_name de ffprobe.
func NormalizeContainer(formatName string) string {
	switch {
	case strings.HasPrefix(formatName, "matroska"):
		return "mkv" // ffprobe dice "matroska,webm" tanto para MKV como para WebM; se distingue por extensión al escanear
	case strings.Contains(formatName, "mp4"):
		return "mp4"
	case formatName == "avi":
		return "avi"
	case formatName == "mpegts":
		return "ts"
	}
	return formatName
}

// HDR deduce el formato de alto rango dinámico de la pista de vídeo.
func (s Stream) HDR() string {
	for _, sd := range s.SideData {
		if t, _ := sd["side_data_type"].(string); strings.Contains(strings.ToLower(t), "dovi") {
			return "dovi"
		}
	}
	switch s.ColorTransfer {
	case "smpte2084":
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	}
	return "sdr"
}

// BitDepth deduce la profundidad de color.
func (s Stream) BitDepth() int {
	if strings.Contains(s.PixFmt, "10") {
		return 10
	}
	if strings.Contains(s.PixFmt, "12") {
		return 12
	}
	return 8
}

// Language devuelve la etiqueta de idioma o "".
func (s Stream) Language() string {
	l := s.Tags["language"]
	if l == "und" {
		return ""
	}
	return l
}

// RunProbe analiza un fichero ya abierto (con os.Root). Lee solo las cabeceras.
func RunProbe(ctx context.Context, f *os.File) (Probe, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	var p Probe
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return p, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return p, err
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return p, fmt.Errorf("salida de ffprobe no válida: %w", err)
	}
	return p, nil
}

// Chapter es un capítulo del archivo, con su tipo cuando el título lo deja claro.
type Chapter struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title,omitempty"`
	Kind  string  `json:"kind,omitempty"` // intro o credits; vacío si no se reconoce. Nunca se adivina por la duración.
	// Detected: no es un capítulo del archivo, sino la intro o los créditos detectados comparando episodios (E2). Los
	// reproductores lo usan para «Saltar intro» y «Siguiente episodio», y la lista de capítulos no lo muestra.
	Detected bool `json:"detected,omitempty"`
}

var (
	introTitle   = regexp.MustCompile(`(?i)^\s*(intro(duction)?|opening|op|cabecera|apertura|sintonía|sintonia|recap|resumen)\b`)
	creditsTitle = regexp.MustCompile(`(?i)^\s*(credits|end ?credits|ending|ed|créditos|creditos|créditos finales|creditos finales)\b`)
)

// RunChapters lee los capítulos del archivo (solo la cabecera) y marca los de intro y créditos por su título.
func RunChapters(ctx context.Context, f *os.File) ([]Chapter, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_chapters", "-of", "json", "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var raw struct {
		Chapters []struct {
			Start string            `json:"start_time"`
			End   string            `json:"end_time"`
			Tags  map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	chapters := []Chapter{}
	for _, c := range raw.Chapters {
		start, _ := strconv.ParseFloat(c.Start, 64)
		end, _ := strconv.ParseFloat(c.End, 64)
		ch := Chapter{Start: start, End: end, Title: strings.TrimSpace(c.Tags["title"])}
		switch {
		case introTitle.MatchString(ch.Title):
			ch.Kind = "intro"
		case creditsTitle.MatchString(ch.Title):
			ch.Kind = "credits"
		}
		if end > start {
			chapters = append(chapters, ch)
		}
	}
	return chapters, nil
}
