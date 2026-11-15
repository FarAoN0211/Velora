package media

import (
	"fmt"
	"strings"
)

// TranscodeOptions only limits conversions: a compatible original is never resized or re-encoded.
type TranscodeOptions struct {
	Disabled    bool
	MaxHeight   int
	BitrateKbps int
}

// DecideWithTranscode preserves the existing direct/copy paths before considering video encoding.
func DecideWithTranscode(p Probe, ext string, caps Caps, audio int, opts TranscodeOptions) Plan {
	plan := Decide(p, ext, caps, audio)
	if plan.Method != Unsupported || opts.Disabled {
		return plan
	}
	var video *Stream
	for i := range p.Streams {
		if p.Streams[i].CodecType == "video" && p.Streams[i].CodecName != "mjpeg" && p.Streams[i].CodecName != "png" {
			video = &p.Streams[i]
			break
		}
	}
	if video == nil {
		return plan
	}
	outputCodec := "h264"
	preserveHDR := video.ColorTransfer == "smpte2084" && caps.HDR10 && caps.has(caps.Video, "hevc") && caps.MaxBitDepths["hevc"] >= 10 && (caps.MP4Video == nil || caps.has(caps.MP4Video, "hevc"))
	if preserveHDR {
		outputCodec = "hevc"
	}
	if !caps.has(caps.Video, outputCodec) || !caps.has(caps.Containers, "mp4") || (caps.MP4Video != nil && !caps.has(caps.MP4Video, outputCodec)) {
		return plan
	}
	// Dolby Vision without an HDR10/HLG base needs a dedicated color conversion; never serve incorrect colors.
	if video.HDR() == "dovi" && video.ColorTransfer != "smpte2084" && video.ColorTransfer != "arib-std-b67" {
		plan.Reasons = []string{"Este Dolby Vision no tiene base HDR10 compatible para convertir sus colores."}
		return plan
	}
	height := opts.MaxHeight
	if height <= 0 || height > 2160 {
		height = 1080
	}
	if caps.MaxHeight > 0 && caps.MaxHeight < height {
		height = caps.MaxHeight
	}
	if h := caps.MaxHeights[outputCodec]; h > 0 && h < height {
		height = h
	}
	if height < 144 {
		plan.Reasons = []string{"El límite de resolución declarado por el dispositivo no admite la conversión."}
		return plan
	}
	height = height / 2 * 2
	width := height * 16 / 9 / 2 * 2
	if w := caps.MaxWidths[outputCodec]; w > 0 && w < width {
		width = w / 2 * 2
	}
	if width < 2 || (caps.MaxBitDepths[outputCodec] > 0 && caps.MaxBitDepths[outputCodec] < 8) {
		plan.Reasons = []string{"El dispositivo no admite el perfil de vídeo de compatibilidad."}
		return plan
	}
	bitrate := opts.BitrateKbps
	if bitrate <= 0 || bitrate > 20000 {
		bitrate = 6000
	}
	plan.VideoCodec = outputCodec
	plan.PreserveHDR = preserveHDR
	plan.Method = Transcode
	plan.ConvertVideo = true
	plan.TargetHeight = height
	plan.TargetWidth = width
	plan.VideoBitrateKbps = bitrate
	plan.Tonemap = video.HDR() != "sdr" && !preserveHDR
	if plan.Tonemap || preserveHDR {
		plan.SourceTransfer = video.ColorTransfer
	}
	var audios []Stream
	for _, s := range p.Streams {
		if s.CodecType == "audio" {
			audios = append(audios, s)
		}
	}
	a := pickAudio(audios, audio)
	plan.AudioIndex = -1
	if a != nil {
		plan.AudioIndex = a.Index
		plan.ConvertAudio = !AudioCompatible(caps, *a) || !FMP4Audio(a.CodecName) || (caps.MP4Audio != nil && !caps.has(caps.MP4Audio, a.CodecName))
	}
	if plan.ConvertAudio {
		plan.AudioCodec = "aac"
	}
	if plan.ConvertAudio && !caps.has(caps.Audio, "aac") {
		plan.Method = Unsupported
		plan.Reasons = []string{"El dispositivo no admite el audio AAC de compatibilidad."}
		return plan
	}
	plan.Reasons = append(plan.Reasons, fmt.Sprintf("Se convierte únicamente el vídeo incompatible a H.264, hasta %dp, conservando el audio si es compatible.", height))
	if plan.ConvertAudio {
		plan.Reasons = append(plan.Reasons, "Solo la pista de audio incompatible se convierte a AAC estéreo.")
	}
	if plan.Tonemap {
		plan.Reasons = append(plan.Reasons, "Se adapta HDR a SDR durante la conversión para conservar los colores.")
	}
	plan.Summary = "Compatibilidad: vídeo adaptado a H.264"
	if preserveHDR {
		plan.Summary = "Compatibilidad: vídeo HEVC 10 bits con HDR10 conservado"
		plan.Reasons[0] = fmt.Sprintf("Se convierte el vídeo incompatible a HEVC Main10, hasta %dp, conservando HDR10 y el audio compatible.", height)
	}
	return plan
}

// VideoStream returns the real video rather than an attached cover.
func VideoStream(p Probe) Stream {
	for _, s := range p.Streams {
		if s.CodecType == "video" && s.CodecName != "mjpeg" && s.CodecName != "png" {
			return s
		}
	}
	return Stream{}
}
func HardwareDecodeCandidate(v Stream) bool {
	return (v.CodecName == "h264" || v.CodecName == "hevc" || v.CodecName == "av1" || v.CodecName == "vp9" || v.CodecName == "mpeg2video") && !strings.Contains(v.PixFmt, "422") && !strings.Contains(v.PixFmt, "444") && v.BitDepth() <= 10
}
