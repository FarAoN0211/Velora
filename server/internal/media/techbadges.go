package media

// Insignias técnicas de la ficha (docs/insignias/hoja-referencia-insignias.webp): resolución, HDR, códec de vídeo,
// audio, canales, extras (FPS, bits, 3D, IMAX) y contenedor. Se calculan del análisis de ffprobe que el escáner ya
// guarda en media_files.probe, así que no hace falta volver a abrir el archivo. Lo que no se conoce no se muestra: nunca
// se adivina una insignia por la duración ni por el tamaño.
//
// El nombre del archivo solo se usa para lo que ffprobe no puede ver en las cabeceras: HDR10+ (sus metadatos van en
// cada fotograma) e IMAX. HDR10+ exige además que el vídeo sea PQ (HDR10) según el propio análisis.

import (
	"encoding/json"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// TechBadge es una insignia: la categoría y el id deciden el dibujo en cada cliente; Label es el texto legible
// (accesibilidad y clientes que no conozcan el id).
type TechBadge struct {
	Category string `json:"category"` // resolution, hdr, video, audio, channels, fps, bitdepth, 3d, imax, container
	ID       string `json:"id"`
	Label    string `json:"label"`
}

var (
	hdr10PlusName    = regexp.MustCompile(`(?i)(^|[^a-z0-9])hdr10(\+|plus|p)([^a-z0-9]|$)`)
	imaxName         = regexp.MustCompile(`(?i)(^|[^a-z0-9])imax([^a-z0-9]|$)`)
	imaxEnhancedName = regexp.MustCompile(`(?i)(^|[^a-z0-9])imax[ ._-]?enhanced([^a-z0-9]|$)`)
	stereo3DName     = regexp.MustCompile(`(?i)(^|[^a-z0-9])(h-?sbs|h-?ou|h-?tab|3d[ ._-]?(sbs|ou|tab)|mvc)([^a-z0-9]|$)`)
)

// TechBadgesJSON calcula las insignias a partir del análisis guardado (JSON de Probe) y la ruta del archivo. Un
// análisis vacío o ilegible devuelve una lista vacía (nunca nil, para que la API envíe []).
func TechBadgesJSON(raw []byte, relPath string) []TechBadge {
	var p Probe
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return []TechBadge{}
	}
	return TechBadges(p, relPath)
}

// TechBadges devuelve las insignias en el orden de la ficha (de más a menos relevante).
func TechBadges(p Probe, relPath string) []TechBadge {
	out := []TechBadge{}
	add := func(cat, id, label string) { out = append(out, TechBadge{cat, id, label}) }
	name := path.Base(relPath)

	v := mainVideo(p)
	if v != nil {
		if id, label := resolutionBadge(v.Width, v.Height); id != "" {
			add("resolution", id, label)
		}
		if id, label := hdrBadge(*v, name); id != "" {
			add("hdr", id, label)
		}
		if id, label := videoCodecBadge(v.CodecName); id != "" {
			add("video", id, label)
		}
	}
	a, aid, alabel := bestAudio(p)
	if a != nil {
		add("audio", aid, alabel)
	}
	if a == nil {
		a = defaultAudio(p) // sin códec reconocido (MP3, Opus…), los canales siguen siendo útiles
	}
	if a != nil {
		if id, label := channelsBadge(*a); id != "" {
			add("channels", id, label)
		}
	}
	if v != nil {
		if fps := frameRate(*v); fps > 0 {
			add("fps", strconv.Itoa(fps), strconv.Itoa(fps)+" FPS")
		}
		if bits := bitDepth(*v); bits >= 10 {
			add("bitdepth", strconv.Itoa(bits), strconv.Itoa(bits)+" bit")
		}
		if is3D(*v, name) {
			add("3d", "3d", "3D")
		}
	}
	switch {
	case imaxEnhancedName.MatchString(name) || imaxAudio(p):
		add("imax", "imax_enhanced", "IMAX Enhanced")
	case imaxName.MatchString(name):
		add("imax", "imax", "IMAX")
	}
	if id := containerBadge(p.Format.FormatName, name); id != "" {
		add("container", id, strings.ToUpper(id))
	}
	return out
}

// mainVideo es la primera pista de vídeo que no es una carátula incrustada (attached_pic en MKV y MP4).
func mainVideo(p Probe) *Stream {
	for i := range p.Streams {
		s := &p.Streams[i]
		if s.CodecType == "video" && s.Disposition["attached_pic"] != 1 && s.CodecName != "mjpeg" && s.CodecName != "png" {
			return s
		}
	}
	return nil
}

// resolutionBadge clasifica por la anchura o la altura: así 1920×800 (panorámica recortada) sigue siendo Full HD y
// 1440×1080 (anamórfico) también.
func resolutionBadge(w, h int) (string, string) {
	switch {
	case w <= 0 && h <= 0:
		return "", ""
	case w >= 6000 || h >= 3400:
		return "8k", "8K UHD"
	case w >= 3200 || h >= 1700:
		return "4k", "4K UHD"
	case w >= 2400 || h >= 1300:
		return "qhd", "QHD 1440p"
	case w >= 1700 || h >= 950:
		return "fhd", "Full HD 1080p"
	case w >= 1100 || h >= 650:
		return "hd", "HD 720p"
	}
	return "sd", "SD 480p"
}

func hdrBadge(v Stream, name string) (string, string) {
	switch v.HDR() {
	case "dovi":
		return "dovi", "Dolby Vision"
	case "hdr10":
		if hdr10PlusName.MatchString(name) || hasSideData(v, "hdr10+", "hdr dynamic metadata smpte2094-40") {
			return "hdr10plus", "HDR10+"
		}
		return "hdr10", "HDR10"
	case "hlg":
		return "hlg", "HLG"
	}
	return "sdr", "SDR"
}

func hasSideData(s Stream, types ...string) bool {
	for _, sd := range s.SideData {
		t, _ := sd["side_data_type"].(string)
		t = strings.ToLower(t)
		for _, want := range types {
			if strings.Contains(t, want) {
				return true
			}
		}
	}
	return false
}

func videoCodecBadge(codec string) (string, string) {
	switch codec {
	case "h264":
		return "h264", "H.264 AVC"
	case "hevc", "h265":
		return "hevc", "H.265 HEVC"
	case "av1":
		return "av1", "AV1"
	case "vp9":
		return "vp9", "VP9"
	case "mpeg2video":
		return "mpeg2", "MPEG-2"
	case "vvc", "h266":
		return "vvc", "VVC H.266"
	}
	return "", ""
}

// audioKind reconoce el formato de una pista de audio por su códec, su perfil y, si el ffprobe del servidor es
// antiguo y no informa del perfil, por el título de la pista (metadato del propio archivo). rank ordena por calidad.
func audioKind(s Stream) (id, label string, rank int) {
	profile := strings.ToLower(s.Profile)
	title := strings.ToLower(s.Tags["title"])
	atmos := strings.Contains(profile, "atmos") || strings.Contains(title, "atmos")
	switch {
	case s.CodecName == "truehd" && atmos:
		return "atmos", "Dolby Atmos", 100
	case s.CodecName == "dts" && (strings.Contains(profile, "dts:x") || strings.Contains(title, "dts:x") || strings.Contains(title, "dts-x")):
		return "dtsx", "DTS:X", 95
	case s.CodecName == "truehd":
		return "truehd", "Dolby TrueHD", 90
	case s.CodecName == "dts" && (strings.Contains(profile, "dts-hd ma") || strings.Contains(title, "dts-hd ma") || strings.Contains(title, "master audio")):
		return "dtshd_ma", "DTS-HD Master Audio", 85
	case s.CodecName == "eac3" && atmos:
		return "atmos", "Dolby Atmos", 80
	case strings.HasPrefix(s.CodecName, "pcm_"):
		return "pcm", "PCM", 75
	case s.CodecName == "flac":
		return "flac", "FLAC", 70
	case s.CodecName == "eac3":
		return "eac3", "Dolby Digital Plus", 60
	case s.CodecName == "dts":
		return "dts", "DTS", 55
	case s.CodecName == "ac3":
		return "ac3", "Dolby Digital", 50
	case s.CodecName == "aac":
		return "aac", "AAC", 40
	}
	return "", "", 0
}

// bestAudio es la pista de audio de mayor calidad que el archivo ofrece (como muestran otras plataformas); a igual
// calidad gana la pista por defecto.
func bestAudio(p Probe) (*Stream, string, string) {
	var best *Stream
	var bid, blabel string
	brank := 0
	for i := range p.Streams {
		s := &p.Streams[i]
		if s.CodecType != "audio" {
			continue
		}
		id, label, rank := audioKind(*s)
		if rank == 0 {
			continue
		}
		if rank > brank || (rank == brank && s.Disposition["default"] == 1 && best.Disposition["default"] != 1) {
			best, bid, blabel, brank = s, id, label, rank
		}
	}
	return best, bid, blabel
}

func defaultAudio(p Probe) *Stream {
	var first *Stream
	for i := range p.Streams {
		s := &p.Streams[i]
		if s.CodecType != "audio" {
			continue
		}
		if s.Disposition["default"] == 1 {
			return s
		}
		if first == nil {
			first = s
		}
	}
	return first
}

// imaxAudio: una pista DTS:X IMAX («DTS-HD MA + DTS:X IMAX») es el sonido de IMAX Enhanced.
func imaxAudio(p Probe) bool {
	for _, s := range p.Streams {
		if s.CodecType == "audio" && s.CodecName == "dts" && strings.Contains(strings.ToLower(s.Profile), "imax") {
			return true
		}
	}
	return false
}

// channelsBadge usa la disposición de canales si ffprobe la da y, si no, el número de canales. Las disposiciones que
// no están en la hoja (2.1, 6.1, 5.1.2…) no muestran insignia.
func channelsBadge(s Stream) (string, string) {
	layout := strings.ToLower(s.ChannelLayout)
	switch {
	case layout == "mono":
		return "mono", "Mono"
	case layout == "stereo" || layout == "2.0" || layout == "downmix":
		return "2.0", "Estéreo 2.0"
	case strings.HasPrefix(layout, "5.1") && !strings.HasPrefix(layout, "5.1."):
		return "5.1", "5.1"
	case strings.HasPrefix(layout, "7.1.4"):
		return "7.1.4", "7.1.4"
	case strings.HasPrefix(layout, "7.1") && !strings.HasPrefix(layout, "7.1."):
		return "7.1", "7.1"
	case layout != "":
		return "", ""
	}
	switch s.Channels {
	case 1:
		return "mono", "Mono"
	case 2:
		return "2.0", "Estéreo 2.0"
	case 6:
		return "5.1", "5.1"
	case 8:
		return "7.1", "7.1"
	case 12:
		return "7.1.4", "7.1.4"
	}
	return "", ""
}

// frameRate devuelve los fotogramas por segundo redondeados a un valor de cine o televisión (23,976 → 24,
// 59,94 → 60). Un valor raro (vídeo de frecuencia variable mal etiquetado) no muestra insignia.
func frameRate(v Stream) int {
	f := parseRate(v.AvgFrameRate)
	if f <= 0 {
		f = parseRate(v.RFrameRate)
	}
	for _, std := range []int{24, 25, 30, 48, 50, 60, 120} {
		if math.Abs(f-float64(std)) <= float64(std)*0.004+0.03 {
			return std
		}
	}
	return 0
}

func parseRate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	if !ok {
		return n
	}
	d, err := strconv.ParseFloat(den, 64)
	if err != nil || d == 0 {
		return 0
	}
	return n / d
}

func bitDepth(v Stream) int {
	if b, err := strconv.Atoi(v.BitsPerRawSample); err == nil && (b == 10 || b == 12) {
		return b
	}
	return v.BitDepth()
}

// is3D: modo estéreo de Matroska, datos «Stereo 3D» del análisis o, en su defecto, el formato escrito en el nombre
// (H-SBS, H-OU, MVC…).
func is3D(v Stream, name string) bool {
	if m := strings.ToLower(v.Tags["stereo_mode"]); m != "" && m != "mono" {
		return true
	}
	for _, sd := range v.SideData {
		if t, _ := sd["side_data_type"].(string); strings.EqualFold(t, "Stereo 3D") {
			if typ, _ := sd["type"].(string); typ != "" && strings.EqualFold(typ, "2D") {
				continue
			}
			return true
		}
	}
	return stereo3DName.MatchString(name)
}

// containerBadge decide por la extensión (ffprobe dice «matroska,webm» para MKV y WebM, y «mov,mp4,…» para MOV y MP4)
// y, si no hay extensión conocida, por el formato del análisis. MPEG-TS sin extensión .m2ts no está en la hoja.
func containerBadge(formatName, name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mk3d":
		return "mkv"
	case ".mp4", ".m4v":
		return "mp4"
	case ".avi":
		return "avi"
	case ".mov":
		return "mov"
	case ".webm":
		return "webm"
	case ".m2ts", ".mts":
		return "m2ts"
	}
	switch c := NormalizeContainer(formatName); c {
	case "mkv", "mp4", "avi":
		return c
	}
	return ""
}
