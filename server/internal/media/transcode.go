package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const RenderDevice = "/dev/dri/renderD128"

// Transcoder probes actual encoding, not just the presence of a compiled FFmpeg encoder.
// No media or network access is needed; a failed GPU is never required for direct playback.
type Transcoder struct {
	once     sync.Once
	hardware bool
	reason   string
}

func (t *Transcoder) Hardware() (bool, string) {
	t.once.Do(func() {
		if _, e := os.Stat(RenderDevice); e != nil {
			t.reason = "GPU no accesible en el contenedor"
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-init_hw_device", "vaapi=va:" + RenderDevice, "-filter_hw_device", "va", "-f", "lavfi", "-i", "color=s=320x180:r=24", "-vf", "format=nv12,hwupload", "-frames:v", "2", "-c:v", "h264_vaapi", "-f", "null", "-"}
		if out, e := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); e != nil {
			t.reason = "La prueba de codificación Intel no ha funcionado"
			slog.Warn("aceleración de vídeo no disponible", "error", e, "detalle", string(out))
			return
		}
		t.hardware = true
		t.reason = "Intel VAAPI verificado con codificación real"
	})
	return t.hardware, t.reason
}
func (t *Transcoder) Encoder(mode string) string {
	if mode != "software" {
		if ok, _ := t.Hardware(); ok {
			return "h264_vaapi"
		}
	}
	return "libx264"
}

func (t *Transcoder) EncoderFor(mode string, p Plan) string {
	encoder := t.Encoder(mode)
	if p.VideoCodec == "hevc" {
		if strings.HasSuffix(encoder, "_vaapi") {
			return "hevc_vaapi"
		}
		return "libx265"
	}
	return encoder
}

// TranscodeArgs uses fixed codecs/presets and a confined descriptor. No user-supplied FFmpeg switches.
// mode: vaapi (decode+encode), vaapi-upload (CPU decode+GPU encode), software.
func TranscodeArgs(p Plan, start float64, mode string) []string {
	a := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-filter_threads", "1", "-filter_complex_threads", "1"}
	gpu := mode != "software"
	hevc := p.VideoCodec == "hevc"
	format := "nv12"
	if p.PreserveHDR {
		format = "p010le"
	}
	if gpu {
		a = append(a, "-init_hw_device", "vaapi=va:"+RenderDevice, "-filter_hw_device", "va")
	}
	if mode == "vaapi" || mode == "vaapi-normal" {
		a = append(a, "-hwaccel", "vaapi", "-hwaccel_device", "va", "-hwaccel_output_format", "vaapi")
	}
	a = append(a, "-threads", "2", "-readrate", "1.15", "-readrate_initial_burst", "5")
	if start > 0 {
		a = append(a, "-ss", strconv.FormatFloat(start, 'f', 3, 64))
	}
	a = append(a, "-i", "/dev/fd/3", "-map", "0:v:0")
	if p.AudioIndex >= 0 {
		a = append(a, "-map", fmt.Sprintf("0:%d", p.AudioIndex))
	}
	w, h := p.TargetWidth, p.TargetHeight
	if w < 2 {
		w = 1920
	}
	if h < 2 {
		h = 1080
	}
	scale := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease:force_divisible_by=2", w, h)
	filter := ""
	if mode == "vaapi" || mode == "vaapi-normal" {
		if p.Tonemap {
			filter = "tonemap_vaapi=format=nv12:primaries=bt709:transfer=bt709:matrix=bt709,"
		}
		// VAAPI evaluates dimensions against the source; preserve aspect without upscaling.
		filter += fmt.Sprintf("scale_vaapi=w='trunc(iw*min(1,min(%d/iw,%d/ih))/2)*2':h='trunc(ih*min(1,min(%d/iw,%d/ih))/2)*2':format=%s", w, h, w, h, format)
	} else {
		filter = scale
		if p.Tonemap {
			transfer := "smpte2084"
			if p.SourceTransfer == "arib-std-b67" {
				transfer = "arib-std-b67"
			}
			filter += ",zscale=pin=bt2020:tin=" + transfer + ":min=bt2020nc:rin=limited:p=bt2020:t=linear:m=gbr:r=full:npl=100,format=gbrpf32le,tonemap=mobius:desat=0,zscale=pin=bt2020:tin=linear:min=gbr:p=bt709:t=bt709:m=bt709:r=limited"
		}
		if gpu {
			filter += ",format=" + format + ",hwupload"
		} else {
			if p.PreserveHDR {
				filter += ",format=yuv420p10le"
			} else {
				filter += ",format=yuv420p"
			}
		}
	}
	if p.Tonemap {
		filter += ",setparams=range=limited:color_primaries=bt709:color_trc=bt709:colorspace=bt709"
	}
	a = append(a, "-vf", filter)
	bitrate := p.VideoBitrateKbps
	if bitrate <= 0 {
		bitrate = 6000
	}
	if gpu && hevc {
		a = append(a, "-c:v", "hevc_vaapi", "-profile:v", "main10", "-async_depth", "2", "-sei", "hdr", "-tag:v", "hvc1", "-b:v", fmt.Sprintf("%dk", bitrate))
		if !strings.HasSuffix(mode, "-normal") {
			a = append(a, "-low_power", "1")
		}
	} else if gpu {
		a = append(a, "-c:v", "h264_vaapi", "-profile:v", "constrained_baseline", "-async_depth", "1", "-b:v", fmt.Sprintf("%dk", bitrate))
	} else if hevc {
		a = append(a, "-c:v", "libx265", "-preset", "ultrafast", "-profile:v", "main10", "-tag:v", "hvc1", "-x265-params", "pools=1:frame-threads=1:log-level=error", "-threads:v", "2")
	} else {
		a = append(a, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "23", "-profile:v", "baseline", "-threads:v", "2")
	}
	a = append(a, "-maxrate", fmt.Sprintf("%dk", bitrate), "-bufsize", fmt.Sprintf("%dk", bitrate*2), "-g", "48", "-keyint_min", "48", "-bf", "0", "-fpsmax", "30")
	if p.Tonemap {
		a = append(a, "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv")
	}
	if p.PreserveHDR {
		a = append(a, "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", "-color_range", "tv")
	}
	if p.AudioIndex >= 0 {
		if p.ConvertAudio {
			a = append(a, "-c:a", "aac", "-ac", "2", "-b:a", "192k", "-threads:a", "1")
		} else {
			a = append(a, "-c:a", "copy", "-copypriorss:a", "0")
		}
	}
	return append(a, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero", "-movflags", "frag_keyframe+empty_moov+default_base_moof+delay_moov", "-f", "mp4", "pipe:1")
}

// startupGate withholds MP4 headers until a real media fragment is available. A GPU
// failure during initialization can thus fall back without concatenating two MP4 streams.
type startupGate struct {
	w         io.Writer
	data      []byte
	committed bool
	onCommit  func()
}

func (g *startupGate) Write(p []byte) (int, error) {
	if g.committed {
		return g.w.Write(p)
	}
	if len(g.data)+len(p) > 8<<20 {
		return 0, fmt.Errorf("fragmento inicial demasiado grande")
	}
	g.data = append(g.data, p...)
	for offset := 0; offset+8 <= len(g.data); {
		size := int64(binary.BigEndian.Uint32(g.data[offset : offset+4]))
		header := int64(8)
		if size == 1 {
			if offset+16 > len(g.data) {
				break
			}
			size = int64(binary.BigEndian.Uint64(g.data[offset+8 : offset+16]))
			header = 16
		}
		if size < 0 || (size != 0 && size < header) {
			return 0, fmt.Errorf("MP4 inicial no válido")
		}
		if string(g.data[offset+4:offset+8]) == "mdat" && int64(len(g.data)-offset) > header {
			g.committed = true
			if g.onCommit != nil {
				g.onCommit()
			}
			if _, e := g.w.Write(g.data); e != nil {
				return 0, e
			}
			g.data = nil
			return len(p), nil
		}
		if size == 0 || size > int64(len(g.data)-offset) {
			break
		}
		offset += int(size)
	}
	return len(p), nil
}

func TranscodeAt(ctx context.Context, f *os.File, p Plan, video Stream, encoder string, start float64, w io.Writer, onMode ...func(string)) error {
	modes := []string{"software"}
	if strings.HasSuffix(encoder, "_vaapi") {
		modes = []string{"vaapi-upload", "software"}
		if HardwareDecodeCandidate(video) && (!p.Tonemap || video.ColorTransfer == "smpte2084" || HasMasteringDisplay(video)) {
			modes = append([]string{"vaapi"}, modes...)
		}
	}
	if p.VideoCodec == "hevc" && strings.HasSuffix(encoder, "_vaapi") {
		expanded := make([]string, 0, len(modes)*2)
		for _, mode := range modes {
			expanded = append(expanded, mode)
			if mode != "software" {
				expanded = append(expanded, mode+"-normal")
			}
		}
		modes = expanded
	}
	for i, mode := range modes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, e := f.Seek(0, io.SeekStart); e != nil {
			return e
		}
		gate := &startupGate{w: w, onCommit: func() {
			for _, f := range onMode {
				f(mode)
			}
		}}
		err := Remux(ctx, f, TranscodeArgs(p, start, mode), gate)
		if err == nil && gate.committed {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("FFmpeg no produjo un fragmento de vídeo")
		}
		if gate.committed || ctx.Err() != nil || i == len(modes)-1 {
			return err
		}
		slog.Warn("se usa respaldo de transcodificación", "modo", mode, "error", err)
	}
	return fmt.Errorf("no hay transcodificador disponible")
}

// Some mastering metadata is delivered only on decoded frames, not by stream probing.
// HDR10 can try VAAPI VPP; HLG without mastering data goes straight to CPU color + GPU encode.
func HasMasteringDisplay(v Stream) bool {
	for _, data := range v.SideData {
		if kind, ok := data["side_data_type"].(string); ok && strings.Contains(strings.ToLower(kind), "mastering display") {
			return true
		}
	}
	return false
}

// Keep diagnostics bounded even when a damaged stream emits repeated decoder errors.
type TailLog struct{ data []byte }

func (l *TailLog) Write(p []byte) (int, error) {
	n := len(p)
	l.data = append(l.data, p...)
	if len(l.data) > 16384 {
		l.data = append([]byte(nil), l.data[len(l.data)-16384:]...)
	}
	return n, nil
}
func (l *TailLog) String() string { return strings.TrimSpace(string(l.data)) }
