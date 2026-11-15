package media

import (
	"fmt"
	"strings"
)

// Caps son las capacidades que declara el cliente (en la web, comprobadas con canPlayType/MediaSource).
type Caps struct {
	HDR10          bool                `json:"hdr10,omitempty"`
	AudioProfiles  map[string][]string `json:"audio_profiles,omitempty"`
	VideoTranscode bool                `json:"video_transcode,omitempty"`
	Containers     []string            `json:"containers"` // mp4, webm, mkv…
	Video          []string            `json:"video"`      // h264, hevc, vp9, av1
	Audio          []string            `json:"audio"`      // aac, mp3, opus, flac, ac3, eac3
	MaxHeight      int                 `json:"max_height"` // 0 = sin límite
	// MaxHeights: altura máxima que decodifica el aparato para cada códec de vídeo (lo que declara su decodificador).
	// Un códec anunciado no garantiza cualquier resolución: un HEVC de 4K en una TV Box que solo llega a 1080 no se
	// arregla reempaquetando, así que se avisa en lugar de intentarlo.
	MaxHeights             map[string]int `json:"max_heights,omitempty"`
	ForceRemux             bool           `json:"force_remux,omitempty"`
	SourceAudioUnsupported bool           `json:"source_audio_unsupported,omitempty"`
	SourceVideoUnsupported bool           `json:"source_video_unsupported,omitempty"`
	MP4Video               []string       `json:"mp4_video,omitempty"`
	MP4Audio               []string       `json:"mp4_audio,omitempty"`
	MaxWidths              map[string]int `json:"max_widths,omitempty"`
	MaxBitDepths           map[string]int `json:"max_bit_depths,omitempty"`
	// SelectAudio: el cliente puede elegir pista de audio dentro del archivo en Directo (p. ej. Safari con audioTracks).
	// La web de Chromium/Firefox no puede: siempre suena la pista por defecto del archivo.
	SelectAudio bool `json:"select_audio"`
}

func (c Caps) has(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// A decoder may accept AAC-LC while rejecting HE-AAC/xHE; absence of profile data is not a rejection.
func AudioCompatible(c Caps, a Stream) bool {
	if !c.has(c.Audio, a.CodecName) || c.SourceAudioUnsupported {
		return false
	}
	if profiles, known := c.AudioProfiles[a.CodecName]; known && a.Profile != "" && !strings.EqualFold(a.Profile, "unknown") {
		return c.has(profiles, a.Profile)
	}
	return true
}

// Method es cómo se reproduce un fichero.
type Method string

const (
	// DirectPlay: el fichero tal cual, con HTTP Range.
	DirectPlay Method = "direct_play"
	// DirectStream: se cambia el contenedor a MP4 fragmentado y, si hace falta, se convierte SOLO el audio (VF-025/026).
	DirectStream Method = "direct_stream"
	// Unsupported: el vídeo necesitaría conversión (transcodificación de vídeo, E2).
	Unsupported Method = "unsupported"
	Transcode   Method = "transcode"
)

// Plan es la decisión, con sus motivos en castellano para mostrarlos al usuario (§5.1).
type Plan struct {
	AudioDynamics    string   `json:"audio_dynamics,omitempty"` // Tratamiento local; no fuerza ninguna conversión en el servidor.
	VideoCodec       string   `json:"video_codec,omitempty"`
	PreserveHDR      bool     `json:"preserve_hdr,omitempty"`
	SourceTransfer   string   `json:"source_transfer,omitempty"`
	ConvertVideo     bool     `json:"convert_video"`
	OutputContainer  string   `json:"output_container,omitempty"`
	AudioCodec       string   `json:"audio_codec,omitempty"`
	TargetHeight     int      `json:"target_height,omitempty"`
	TargetWidth      int      `json:"target_width,omitempty"`
	VideoBitrateKbps int      `json:"video_bitrate_kbps,omitempty"`
	Tonemap          bool     `json:"tonemap,omitempty"`
	Encoder          string   `json:"encoder,omitempty"`
	Method           Method   `json:"method"`
	AudioIndex       int      `json:"audio_index"`   // índice absoluto de la pista de audio elegida (-1 si no hay audio)
	ConvertAudio     bool     `json:"convert_audio"` // DirectStream: true = audio a AAC estéreo; false = copiar
	Reasons          []string `json:"reasons"`
	Summary          string   `json:"summary"` // "En este dispositivo": texto corto para la ficha
}

// Códecs de audio que pueden ir en MP4 fragmentado sin convertir (y que el cliente diga que reproduce).
var fmp4Audio = map[string]bool{"aac": true, "mp3": true, "opus": true, "flac": true, "ac3": true, "eac3": true}

// Códecs de vídeo que pueden ir en MP4 fragmentado sin convertir.
var fmp4Video = map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true, "mpeg4": true, "mpeg2video": true}

// FMP4Audio indica si el códec de audio cabe en MP4 fragmentado sin convertir.
func FMP4Audio(codec string) bool { return fmp4Audio[codec] }

// FMP4Video indica si el códec de vídeo cabe en MP4 fragmentado sin convertir.
func FMP4Video(codec string) bool { return fmp4Video[codec] }

// Decide elige el método más barato posible (architecture.md §5.1): Direct Play → Direct Stream (contenedor y, si hace
// falta, solo audio) → no compatible todavía. preferredAudio < 0 elige la pista por defecto.
func Decide(p Probe, fileExt string, caps Caps, preferredAudio int) Plan {
	plan := Plan{AudioIndex: -1}
	var video *Stream
	var audios []Stream
	for i := range p.Streams {
		s := p.Streams[i]
		switch s.CodecType {
		case "video":
			if video == nil && s.CodecName != "mjpeg" && s.CodecName != "png" {
				video = &p.Streams[i]
			}
		case "audio":
			audios = append(audios, s)
		}
	}
	if video == nil {
		plan.Method = Unsupported
		plan.Reasons = []string{"El archivo no tiene pista de vídeo reconocible."}
		plan.Summary = "No se puede reproducir"
		return plan
	}
	audio := pickAudio(audios, preferredAudio)
	if audio != nil {
		plan.AudioIndex = audio.Index
	}

	container := p.Container()
	if container == "mkv" && strings.EqualFold(fileExt, ".webm") {
		container = "webm"
	}
	videoOK := caps.has(caps.Video, video.CodecName) && !caps.SourceVideoUnsupported
	if caps.MaxHeight > 0 && video.Height > caps.MaxHeight {
		videoOK = false
	}
	if limit := caps.MaxHeights[strings.ToLower(video.CodecName)]; videoOK && limit > 0 && video.Height > limit {
		plan.Method = Unsupported
		plan.Reasons = []string{fmt.Sprintf("Este aparato decodifica vídeo %s hasta %d líneas y este archivo tiene %d; reempaquetarlo no lo cambia.",
			strings.ToUpper(video.CodecName), limit, video.Height)}
		plan.Summary = "Resolución mayor de la que decodifica este aparato"
		return plan
	}
	if limit := caps.MaxWidths[strings.ToLower(video.CodecName)]; limit > 0 && video.Width > limit {
		videoOK = false
	}
	if limit := caps.MaxBitDepths[strings.ToLower(video.CodecName)]; limit > 0 && video.BitDepth() > limit {
		videoOK = false
	}
	audioOK := audio == nil || AudioCompatible(caps, *audio)
	hdr := video.HDR()

	if !videoOK {
		plan.Method = Unsupported
		plan.Reasons = []string{fmt.Sprintf("Este dispositivo no reproduce vídeo %s%s; hace falta convertirlo.",
			describeVideo(video), hdrSuffix(hdr))}
		plan.Summary = "Necesita conversión de vídeo"
		return plan
	}
	// Si se ha elegido una pista distinta de la que sonaría por defecto y el cliente no sabe cambiarla, el archivo tal cual
	// no sirve: se reempaqueta con esa pista (sin tocar la imagen).
	wrongTrack := audio != nil && len(audios) > 1 && !caps.SelectAudio && audio.Index != pickAudio(audios, -1).Index
	mp4VideoOK := fmp4Video[video.CodecName] && (caps.MP4Video == nil || caps.has(caps.MP4Video, video.CodecName))
	mp4AudioOK := audio == nil || (audioOK && fmp4Audio[audio.CodecName] && (caps.MP4Audio == nil || caps.has(caps.MP4Audio, audio.CodecName)))
	directOK := container != "mp4" || (mp4VideoOK && mp4AudioOK)
	if caps.has(caps.Containers, container) && audioOK && directOK && !wrongTrack && !caps.ForceRemux {
		plan.Method = DirectPlay
		plan.Summary = "Directo: el archivo tal cual, sin conversión"
		return plan
	}
	webmVideo := video.CodecName == "vp8" || video.CodecName == "vp9" || video.CodecName == "av1"
	webm := caps.has(caps.Containers, "webm") && webmVideo && (!mp4VideoOK || (!mp4AudioOK && audio != nil && (audio.CodecName == "opus" || audio.CodecName == "vorbis")))
	mkv := caps.has(caps.Containers, "mkv") && (!mp4VideoOK || (audioOK && !mp4AudioOK))
	if !mp4VideoOK && !webm && !mkv {
		plan.Method = Unsupported
		plan.Reasons = []string{fmt.Sprintf("El vídeo %s no se puede reempaquetar para este dispositivo.", describeVideo(video))}
		plan.Summary = "Necesita conversión de vídeo"
		return plan
	}
	plan.Method = DirectStream
	plan.OutputContainer = "mp4"
	if webm {
		plan.OutputContainer = "webm"
	}
	if mkv {
		plan.OutputContainer = "mkv"
	}
	if wrongTrack && caps.has(caps.Containers, container) && audioOK {
		plan.Reasons = append(plan.Reasons, "Para usar la pista de audio elegida se reempaqueta el archivo, sin tocar la imagen.")
	}
	if !caps.has(caps.Containers, container) {
		plan.Reasons = append(plan.Reasons, fmt.Sprintf("El formato %s no se reproduce aquí: se reempaqueta sin tocar la imagen.", strings.ToUpper(container)))
	}
	copyAudio := mp4AudioOK
	if webm {
		copyAudio = audio == nil || (audioOK && (audio.CodecName == "opus" || audio.CodecName == "vorbis"))
	}
	if mkv {
		copyAudio = audio == nil || audioOK
	}
	if audio != nil && !copyAudio {
		plan.ConvertAudio = true
		plan.AudioCodec = "aac"
		if webm && !mkv {
			plan.AudioCodec = "opus"
		}
		if !caps.has(caps.Audio, plan.AudioCodec) {
			plan.Method = Unsupported
			plan.Reasons = []string{"No hay un formato de audio de compatibilidad que el dispositivo admita."}
			return plan
		}
		plan.Reasons = append(plan.Reasons, fmt.Sprintf("El audio %s no se reproduce en el formato de salida: se convierte a %s estéreo.", strings.ToUpper(audio.CodecName), strings.ToUpper(plan.AudioCodec)))
	}
	plan.Summary = "Optimizado: se adapta el formato de transmisión sin convertir el vídeo"
	if plan.ConvertAudio {
		plan.Summary += " · audio adaptado"
	}
	return plan
}

func pickAudio(audios []Stream, preferred int) *Stream {
	if len(audios) == 0 {
		return nil
	}
	if preferred >= 0 {
		for i := range audios {
			if audios[i].Index == preferred {
				return &audios[i]
			}
		}
	}
	for i := range audios {
		if audios[i].Disposition["default"] == 1 {
			return &audios[i]
		}
	}
	return &audios[0]
}

func describeVideo(s *Stream) string {
	name := strings.ToUpper(s.CodecName)
	if s.CodecName == "hevc" && s.BitDepth() >= 10 {
		name += " 10 bits"
	}
	if s.Height > 0 {
		name += fmt.Sprintf(" %dp", s.Height)
	}
	return name
}

func hdrSuffix(hdr string) string {
	switch hdr {
	case "hdr10":
		return " HDR10"
	case "hlg":
		return " HLG"
	case "dovi":
		return " Dolby Vision"
	}
	return ""
}
