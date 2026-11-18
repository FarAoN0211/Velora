package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscodePlanPreservesCompatibleTracks(t *testing.T) {
	opts := TranscodeOptions{MaxHeight: 1080, BitrateKbps: 6000}
	p := probeOf("mp4", "h264", "yuv420p", "bt709", 2160, "aac")
	if got := DecideWithTranscode(p, ".mp4", chromium, -1, opts); got.Method != DirectPlay || got.ConvertVideo || got.ConvertAudio {
		t.Fatal(got)
	}
	p = probeOf("matroska", "h264", "yuv420p", "bt709", 1080, "ac3")
	if got := DecideWithTranscode(p, ".mkv", chromium, -1, opts); got.Method != DirectStream || got.ConvertVideo || !got.ConvertAudio {
		t.Fatal(got)
	}
	p = probeOf("matroska", "hevc", "yuv420p10le", "bt709", 2160, "aac")
	got := DecideWithTranscode(p, ".mkv", chromium, -1, opts)
	if got.Method != Transcode || !got.ConvertVideo || got.ConvertAudio || got.TargetHeight != 1080 {
		t.Fatal(got)
	}
	if got := DecideWithTranscode(p, ".mkv", chromium, -1, TranscodeOptions{Disabled: true}); got.Method != Unsupported {
		t.Fatal(got)
	}
	limited := chromium
	limited.MaxBitDepths = map[string]int{"h264": 8}
	p = probeOf("mp4", "h264", "yuv420p10le", "bt709", 1080, "aac")
	if got := DecideWithTranscode(p, ".mp4", limited, -1, opts); got.Method != Transcode {
		t.Fatal(got)
	}
	precise := chromium
	precise.SourceVideoUnsupported = true
	if got := DecideWithTranscode(p, ".mp4", precise, -1, opts); got.Method != Transcode {
		t.Fatal(got)
	}
}
func TestWebMRemuxAvoidsVideoConversion(t *testing.T) {
	caps := chromium
	caps.MP4Video = []string{"h264"}
	caps.MP4Audio = []string{"aac"}
	p := probeOf("matroska", "vp9", "yuv420p", "bt709", 1080, "opus")
	got := DecideWithTranscode(p, ".mkv", caps, -1, TranscodeOptions{})
	if got.Method != DirectStream || got.OutputContainer != "webm" || got.ConvertAudio || got.ConvertVideo {
		t.Fatal(got)
	}
	args := strings.Join(RemuxArgs(got, "vp9", 0), " ")
	if !strings.Contains(args, "-c:v copy") || !strings.Contains(args, "-c:a copy") || !strings.Contains(args, "-f webm") {
		t.Fatal(args)
	}
}
func TestTranscodeArgsBoundResources(t *testing.T) {
	p := Plan{ConvertVideo: true, AudioIndex: 1, TargetWidth: 1920, TargetHeight: 1080, VideoBitrateKbps: 6000}
	for _, mode := range []string{"software", "vaapi", "vaapi-upload"} {
		args := strings.Join(TranscodeArgs(p, 20.5, mode), " ")
		for _, want := range []string{"-i /dev/fd/3", "-c:a copy", "-ss 20.500", "-readrate 3", "-readrate_initial_burst 20", "-readrate_catchup 6", "-maxrate 6000k", "pipe:1"} {
			if !strings.Contains(args, want) {
				t.Fatalf("%s: %s", mode, args)
			}
		}
		if strings.Contains(args, "-noaccurate_seek") {
			t.Fatal("el vídeo convertido debe buscar exactamente")
		}
	}
}
func TestStartupGateDoesNotLeakHeadersOnGPUFailure(t *testing.T) {
	var dst bytes.Buffer
	gate := &startupGate{w: &dst}
	box := func(kind string, payload []byte) []byte {
		b := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], kind)
		copy(b[8:], payload)
		return b
	}
	headers := box("ftyp", []byte("isom"))
	gate.Write(headers)
	if dst.Len() != 0 || gate.committed {
		t.Fatal("cabecera expuesta antes del primer fragmento")
	}
	media := box("mdat", []byte{1, 2, 3})
	gate.Write(media[:8])
	if dst.Len() != 0 {
		t.Fatal("mdat vacío")
	}
	gate.Write(media[8:])
	if !gate.committed || dst.Len() != len(headers)+len(media) {
		t.Fatal("fragmento no publicado")
	}
}
func generateTranscodeFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "source.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=3", "-f", "lavfi", "-i", "sine=d=3", "-c:v", "mpeg4", "-threads", "2", "-c:a", "aac", "-shortest", p)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	return p
}
func TestSoftwareTranscodeRealAndPreservesAudio(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip(e)
	}
	source := generateTranscodeFixture(t)
	in, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	for _, encoder := range []string{"libx264", "h264_vaapi"} {
		out, e := os.Create(filepath.Join(t.TempDir(), "out.mp4"))
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		p := Plan{AudioIndex: 1, ConvertVideo: true, TargetWidth: 256, TargetHeight: 144, VideoBitrateKbps: 1000}
		e = TranscodeAt(ctx, in, p, Stream{CodecName: "mpeg4"}, encoder, 0, out)
		cancel()
		out.Close()
		if e != nil {
			t.Fatal(e)
		}
		f, e := os.Open(out.Name())
		if e != nil {
			t.Fatal(e)
		}
		probe, e := RunProbe(context.Background(), f)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		v := VideoStream(probe)
		if v.CodecName != "h264" || v.Width > 256 || v.Height > 144 {
			t.Fatal(probe)
		}
		hashes := func(file string) string {
			b, e := exec.Command("ffprobe", "-v", "error", "-select_streams", "a", "-show_packets", "-show_data_hash", "sha256", "-show_entries", "packet=data_hash", "-of", "csv=p=0", file).Output()
			if e != nil {
				t.Fatal(e)
			}
			return string(b)
		}
		if hashes(source) != hashes(out.Name()) {
			t.Fatal("el audio compatible debe copiarse sin recodificar")
		}
	}
}

// A real encoder must be able to build a reserve, including on FFmpeg 8 where
// default catch-up pacing can silently negate the requested initial burst.
func TestTranscodeBuildsBufferAhead(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip(e)
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "long.mkv")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=64x64:r=24:d=30", "-c:v", "mpeg4", "-threads", "1", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(dir, "ahead.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	plan := Plan{AudioIndex: -1, ConvertVideo: true, TargetWidth: 64, TargetHeight: 64, VideoBitrateKbps: 500}
	err = TranscodeAt(ctx, in, plan, Stream{CodecName: "mpeg4"}, "libx264", 0, out)
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	probe, err := RunProbe(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	// Large tolerance for slow CI: the fixture is deliberately tiny. The old
	// 1.15x/default-catchup policy produces only about 3 seconds here.
	if probe.DurationMS() < 8000 {
		t.Fatalf("no reserve after 3 seconds: %d ms", probe.DurationMS())
	}
}

func TestHardwareTranscodeReal(t *testing.T) {
	if os.Getenv("VELORA_TEST_HW") != "1" {
		t.Skip("GPU solo en prueba explícita")
	}
	tr := &Transcoder{}
	if ok, reason := tr.Hardware(); !ok {
		t.Fatal(reason)
	}
	source := filepath.Join(t.TempDir(), "gpu.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-init_hw_device", "vaapi=va:"+RenderDevice, "-filter_hw_device", "va", "-f", "lavfi", "-i", "testsrc2=s=640x360:r=60:d=2", "-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi", "-y", source)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture GPU: %v %s", e, out)
	}
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var out bytes.Buffer
	p := Plan{AudioIndex: -1, ConvertVideo: true, TargetWidth: 320, TargetHeight: 180, VideoBitrateKbps: 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e := Remux(ctx, f, TranscodeArgs(p, 0, "vaapi"), &out); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out.Bytes(), []byte("mdat")) {
		t.Fatal("GPU no generó vídeo")
	}
}

func TestSoftwareHDRToneMappingReal(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip(e)
	}
	source := filepath.Join(t.TempDir(), "hdr.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=1", "-vf", "format=yuv420p10le", "-c:v", "ffv1", "-threads", "2", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", source)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("HDR fixture: %v %s", e, out)
	}
	in, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.Create(filepath.Join(t.TempDir(), "sdr.mp4"))
	if e != nil {
		t.Fatal(e)
	}
	p := Plan{AudioIndex: -1, ConvertVideo: true, Tonemap: true, TargetWidth: 256, TargetHeight: 144, VideoBitrateKbps: 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if e := TranscodeAt(ctx, in, p, Stream{CodecName: "ffv1"}, "libx264", 0, out); e != nil {
		t.Fatal(e)
	}
	out.Close()
	result, e := os.Open(out.Name())
	if e != nil {
		t.Fatal(e)
	}
	defer result.Close()
	probe, e := RunProbe(ctx, result)
	if e != nil {
		t.Fatal(e)
	}
	v := VideoStream(probe)
	if v.ColorTransfer != "bt709" || v.PixFmt != "yuv420p" {
		t.Fatal(v)
	}
}

func TestNativeRemuxPreservesDTSAndConvertsOnlyFailedAudio(t *testing.T) {
	p := probeOf("avi", "h264", "yuv420p", "bt709", 1080, "dts")
	caps := Caps{Containers: []string{"mp4", "mkv"}, Video: []string{"h264"}, Audio: []string{"dts", "aac"}, SelectAudio: true}
	got := DecideWithTranscode(p, ".avi", caps, -1, TranscodeOptions{})
	if got.Method != DirectStream || got.OutputContainer != "mkv" || got.ConvertAudio || got.ConvertVideo {
		t.Fatal(got)
	}
	args := strings.Join(RemuxArgs(got, "h264", 0), " ")
	if !strings.Contains(args, "-c:v copy") || !strings.Contains(args, "-c:a copy") || !strings.Contains(args, "-f matroska") {
		t.Fatal(args)
	}
	p = probeOf("mp4", "h264", "yuv420p", "bt709", 1080, "aac")
	caps.SourceAudioUnsupported = true
	got = DecideWithTranscode(p, ".mp4", caps, -1, TranscodeOptions{})
	if got.Method != DirectStream || !got.ConvertAudio || got.ConvertVideo {
		t.Fatal(got)
	}
}

func TestHardwareHDRTranscodeReal(t *testing.T) {
	if os.Getenv("VELORA_TEST_HW") != "1" {
		t.Skip("GPU solo en prueba explícita")
	}
	source := filepath.Join(t.TempDir(), "hdr-hevc.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=2", "-vf", "format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400:log-level=error", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc", source)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("HDR GPU fixture: %v %s", e, out)
	}
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	out, e := os.Create(filepath.Join(t.TempDir(), "hdr-to-sdr.mp4"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p := Plan{AudioIndex: -1, ConvertVideo: true, Tonemap: true, SourceTransfer: "smpte2084", TargetWidth: 320, TargetHeight: 180, VideoBitrateKbps: 1000}
	inputProbe, e := RunProbe(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	if VideoStream(inputProbe).ColorTransfer != "smpte2084" {
		t.Fatal("falta HDR10 en la muestra")
	}
	if e := TranscodeAt(ctx, f, p, VideoStream(inputProbe), "h264_vaapi", 0, out); e != nil {
		t.Fatal(e)
	}
	out.Close()
	result, e := os.Open(out.Name())
	if e != nil {
		t.Fatal(e)
	}
	defer result.Close()
	probe, e := RunProbe(ctx, result)
	if e != nil {
		t.Fatal(e)
	}
	v := VideoStream(probe)
	if v.CodecName != "h264" || v.ColorTransfer != "bt709" || v.PixFmt != "yuv420p" {
		t.Fatal(v)
	}
}

func TestAudioProfileFailureDoesNotEncodeCompatibleVideo(t *testing.T) {
	p := probeOf("mp4", "h264", "yuv420p", "bt709", 1080, "aac")
	p.Streams[1].Profile = "HE-AACv2"
	caps := chromium
	caps.AudioProfiles = map[string][]string{"aac": {"LC"}}
	got := DecideWithTranscode(p, ".mp4", caps, -1, TranscodeOptions{})
	if got.Method != DirectStream || !got.ConvertAudio || got.ConvertVideo {
		t.Fatal(got)
	}
	p.Streams[1].Profile = "LC"
	got = DecideWithTranscode(p, ".mp4", caps, -1, TranscodeOptions{})
	if got.Method != DirectPlay || got.ConvertAudio || got.ConvertVideo {
		t.Fatal(got)
	}
}

func TestHDRTranscodeRequiresExplicitDisplayAndMain10Support(t *testing.T) {
	p := probeOf("mkv", "av1", "yuv420p10le", "smpte2084", 2160, "ac3")
	caps := Caps{HDR10: true, Containers: []string{"mp4", "mkv"}, Video: []string{"h264", "hevc"}, Audio: []string{"ac3", "aac"}, MaxBitDepths: map[string]int{"hevc": 10}}
	opts := TranscodeOptions{MaxHeight: 2160, BitrateKbps: 20000}
	got := DecideWithTranscode(p, ".mkv", caps, -1, opts)
	if got.Method != Transcode || got.VideoCodec != "hevc" || !got.PreserveHDR || got.Tonemap || got.ConvertAudio || got.TargetHeight != 2160 {
		t.Fatal(got)
	}
	for _, change := range []func(*Caps){func(c *Caps) { c.HDR10 = false }, func(c *Caps) { c.MaxBitDepths = map[string]int{"hevc": 8} }} {
		fallback := caps
		change(&fallback)
		got = DecideWithTranscode(p, ".mkv", fallback, -1, opts)
		if got.VideoCodec != "h264" || got.PreserveHDR || !got.Tonemap {
			t.Fatal(got)
		}
	}
	caps.Video = append(caps.Video, "av1")
	caps.MaxBitDepths["av1"] = 10
	got = DecideWithTranscode(p, ".mkv", caps, -1, opts)
	if got.Method != DirectPlay || got.ConvertVideo {
		t.Fatal("compatible original changed", got)
	}
}

func TestHardwareHEVCPreservesHDRReal(t *testing.T) {
	if os.Getenv("VELORA_TEST_HW") != "1" {
		t.Skip("GPU solo en prueba explícita")
	}
	source := filepath.Join(t.TempDir(), "hdr.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=2", "-vf", "format=yuv420p10le", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:colorprim=bt2020:transfer=smpte2084:colormatrix=bt2020nc:master-display=G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1):max-cll=1000,400:log-level=error", source)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, b)
	}
	in, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	path := filepath.Join(t.TempDir(), "hevc-hdr.mp4")
	out, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	p := Plan{VideoCodec: "hevc", PreserveHDR: true, SourceTransfer: "smpte2084", ConvertVideo: true, AudioIndex: -1, TargetWidth: 320, TargetHeight: 180, VideoBitrateKbps: 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mode := ""
	e = TranscodeAt(ctx, in, p, Stream{CodecName: "hevc", ColorTransfer: "smpte2084", PixFmt: "yuv420p10le"}, "hevc_vaapi", 0, out, func(m string) { mode = m })
	out.Close()
	if e != nil {
		t.Fatal(e)
	}
	result, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer result.Close()
	probe, e := RunProbe(ctx, result)
	if e != nil {
		t.Fatal(e)
	}
	v := VideoStream(probe)
	if v.CodecName != "hevc" || v.BitDepth() != 10 || v.ColorTransfer != "smpte2084" || !HasMasteringDisplay(v) || mode != "vaapi" {
		t.Fatalf("HDR metadata/fast GPU missing: mode=%s %+v", mode, v)
	}
}
