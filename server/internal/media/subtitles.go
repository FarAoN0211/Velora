package media

// Subtítulos de texto (B3 de E1.2, adelantado de E2): pistas internas del archivo y archivos externos junto a la película,
// convertidos a WebVTT con FFmpeg. Los de imagen (PGS, VobSub) necesitan convertir el vídeo y siguen en E2.
//
// Seguridad: los externos solo se buscan en la carpeta de la película, dentro de la biblioteca (os.Root), sin seguir
// enlaces simbólicos, con un tamaño máximo; FFmpeg recibe siempre el archivo como /dev/fd/3 y unos argumentos fijos.
// Limitación documentada: al pasar ASS/SSA a WebVTT se pierden estilos y posiciones.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// MaxExternalSubBytes es el tamaño máximo de un archivo de subtítulos externo.
const MaxExternalSubBytes = 10 << 20

// MaxVTTBytes es el tamaño máximo del WebVTT resultante.
const MaxVTTBytes = 20 << 20

var textSubCodecs = map[string]bool{"subrip": true, "srt": true, "ass": true, "ssa": true, "webvtt": true, "mov_text": true, "text": true}

// externalFormats: extensión → formato de entrada de FFmpeg.
var externalFormats = map[string]string{".srt": "srt", ".ass": "ass", ".ssa": "ass", ".vtt": "webvtt"}

// SubTrack es un subtítulo ofrecido al reproductor. ID: «i<índice>» (interno) o «e<n>» (externo n-ésimo).
type SubTrack struct {
	Label           string `json:"label"`
	Kind            string `json:"type"`
	HearingImpaired bool   `json:"hearing_impaired"`
	Provider        string `json:"provider,omitempty"`
	ID              string `json:"id"`
	Language        string `json:"language,omitempty"`
	Title           string `json:"title,omitempty"`
	Codec           string `json:"codec"`
	Forced          bool   `json:"forced"`
	Default         bool   `json:"default"`
	External        bool   `json:"external"`
	Supported       bool   `json:"supported"` // false: de imagen (PGS/VobSub), aún no se pueden mostrar en la web
}

// ExternalSub es un archivo de subtítulos junto a la película.
type ExternalSub struct {
	Name            string // nombre del archivo, dentro de la carpeta de la película
	Format          string // srt, ass o webvtt
	Language        string
	Forced          bool
	HearingImpaired bool
}

// ExternalSubs busca «Película.es.srt», «Película.spa.forced.ass», «Película.vtt»… en la carpeta de relVideo. Solo
// archivos normales (sin enlaces simbólicos). El orden es estable: el ID «e<n>» se refiere a esta lista.
func ExternalSubs(root *os.Root, relVideo string) []ExternalSub {
	dir := path.Dir(relVideo)
	base := strings.TrimSuffix(path.Base(relVideo), path.Ext(relVideo))
	entries, err := fs.ReadDir(root.FS(), dir)
	if err != nil {
		return nil
	}
	var out []ExternalSub
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		format, ok := externalFormats[ext]
		if !ok {
			continue
		}
		suffix := name[len(base) : len(name)-len(ext)]
		if suffix != "" && !strings.ContainsRune(". _-([", rune(suffix[0])) {
			continue
		}
		middle := strings.Trim(suffix, ". _-")
		sub := ExternalSub{Name: name, Format: format}
		for _, tok := range strings.Split(strings.ToLower(middle), ".") {
			switch {
			case tok == "sdh" || tok == "cc" || (tok == "hi" && sub.Language != "") || tok == "descriptivos":
				sub.HearingImpaired = true
			case tok == "forced" || tok == "forzados" || tok == "forzado":
				sub.Forced = true
			case len(tok) == 2 || len(tok) == 3 || strings.Contains(tok, "-"):
				sub.Language = tok
			}
		}
		out = append(out, sub)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SubtitleTracks lista las pistas internas del análisis y los externos.
func SubtitleTracks(p Probe, external []ExternalSub) []SubTrack {
	tracks := []SubTrack{}
	for _, s := range p.Streams {
		if s.CodecType != "subtitle" {
			continue
		}
		tracks = append(tracks, SubTrack{ID: "i" + strconv.Itoa(s.Index), Language: s.Language(), Title: s.Tags["title"], Codec: s.CodecName,
			HearingImpaired: s.Disposition["hearing_impaired"] == 1, Forced: s.Disposition["forced"] == 1, Default: s.Disposition["default"] == 1, Supported: textSubCodecs[s.CodecName]})
	}
	for i, e := range external {
		tracks = append(tracks, SubTrack{ID: "e" + strconv.Itoa(i), Language: e.Language, Title: e.Name, Codec: e.Format, Forced: e.Forced,
			External: true, Supported: true, HearingImpaired: e.HearingImpaired})
	}
	for i := range tracks {
		LabelSubtitle(&tracks[i])
	}
	ApplySubtitleDefaults(tracks)
	return tracks
}

// ErrSubNotText: la pista es de imagen o no existe.
var ErrSubNotText = errors.New("subtítulo no disponible como texto")

// InternalSubArgs: extraer la pista index del archivo (/dev/fd/3) como WebVTT.
func InternalSubArgs(p Probe, index int) ([]string, error) {
	for _, s := range p.Streams {
		if s.CodecType == "subtitle" && s.Index == index && textSubCodecs[s.CodecName] {
			return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-i", "/dev/fd/3", "-map", "0:" + strconv.Itoa(index),
				"-c:s", "webvtt", "-f", "webvtt", "pipe:1"}, nil
		}
	}
	return nil, ErrSubNotText
}

// ExtractSubtitles extrae en una sola lectura del archivo (/dev/fd/3) las pistas de texto indicadas (índice → ruta de
// salida), cada una como WebVTT. Leer una película grande cuesta lo mismo para una pista que para todas, así que se sacan
// juntas. Solo admite pistas de texto; las rutas las decide el servidor (nunca el cliente).
func ExtractSubtitles(ctx context.Context, f *os.File, p Probe, outs map[int]string) error {
	idx := make([]int, 0, len(outs))
	for i := range outs {
		if _, err := InternalSubArgs(p, i); err != nil {
			return err
		}
		idx = append(idx, i)
	}
	if len(idx) == 0 {
		return nil
	}
	sort.Ints(idx)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", "/dev/fd/3"}
	for _, i := range idx {
		args = append(args, "-map", "0:"+strconv.Itoa(i), "-c:s", "webvtt", "-f", "webvtt", "-flush_packets", "1", outs[i])
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{f}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, remuxNice)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("tiempo agotado al preparar los subtítulos")
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ExternalSubArgs: convertir un archivo externo (/dev/fd/3) de formato conocido a WebVTT.
func ExternalSubArgs(format string) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-f", format, "-i", "/dev/fd/3", "-c:s", "webvtt", "-f", "webvtt", "pipe:1"}
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

var errTooBig = errors.New("el subtítulo convertido supera el tamaño máximo")

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errTooBig
	}
	return b.Buffer.Write(p)
}

// ConvertSubtitle ejecuta FFmpeg (con prioridad baja) y devuelve el WebVTT, como mucho MaxVTTBytes; ctx pone el plazo.
func ConvertSubtitle(ctx context.Context, f *os.File, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{f}
	out := &limitedBuffer{max: MaxVTTBytes}
	var stderr strings.Builder
	cmd.Stdout = out
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, remuxNice)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("tiempo agotado al preparar el subtítulo")
		}
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if !bytes.HasPrefix(bytes.TrimPrefix(out.Bytes(), []byte("\xef\xbb\xbf")), []byte("WEBVTT")) {
		return nil, fmt.Errorf("la conversión no produjo WebVTT")
	}
	return out.Bytes(), nil
}

var _ io.Writer = (*limitedBuffer)(nil)

var subtitleForcedName = regexp.MustCompile(`(?i)(^|[\s._(\[-])(forced|forzados?|foreign.parts.only)([\s._)\]-]|$)`)
var subtitleSDHName = regexp.MustCompile(`(?i)(^|[\s._(\[-])(sdh|cc|hearing.impaired|descriptivos?|sordos)([\s._)\]-]|$)`)

// Metadata labels describe flags/titles; they do not claim to infer the spoken contents.
func LabelSubtitle(s *SubTrack) {
	if !s.External {
		s.Forced = s.Forced || subtitleForcedName.MatchString(s.Title)
		s.HearingImpaired = s.HearingImpaired || subtitleSDHName.MatchString(s.Title)
	}
	name := "Idioma sin identificar"
	if tag, err := language.Parse(s.Language); err == nil && s.Language != "" && s.Language != "und" {
		name = display.Spanish.Languages().Name(tag)
		if name == "" {
			name = s.Language
		}
	}
	s.Kind = "full"
	kind := "Completos"
	if s.Forced {
		s.Kind = "forced"
		kind = "Forzados"
	}
	if s.HearingImpaired {
		if s.Forced {
			kind += " · Descriptivos (SDH/CC)"
		} else {
			s.Kind = "sdh"
			kind = "Descriptivos (SDH/CC)"
		}
	}
	s.Label = name + " · " + kind
}

// ApplySubtitleDefaults selects Spanish forced subtitles, preserving the media files themselves.
func ApplySubtitleDefaults(tracks []SubTrack) {
	best := -1
	for i, t := range tracks {
		tag, err := language.Parse(t.Language)
		if err != nil || !t.Forced {
			continue
		}
		base, _ := tag.Base()
		if base.String() != "es" {
			continue
		}
		if best < 0 || (t.Supported && !tracks[best].Supported) || (t.Supported == tracks[best].Supported && t.Default && !tracks[best].Default) {
			best = i
		}
	}
	for i := range tracks {
		tracks[i].Default = i == best
	}
}
