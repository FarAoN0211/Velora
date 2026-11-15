package media

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Dolby configuration is learned from its first packet. An empty early moov must
// not make compatible AC3/EAC3 audio break either remuxing or video conversion.
func TestFragmentedMP4PreservesDolbyAudio(t *testing.T) {
	for _, codec := range []string{"ac3", "eac3"} {
		t.Run(codec, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source.mkv")
			meta := filepath.Join(t.TempDir(), "chapters.txt")
			if e := os.WriteFile(meta, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=2000\ntitle=Intro\n"), 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=3", "-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-f", "ffmetadata", "-i", meta, "-map_metadata", "2", "-map_chapters", "2", "-c:v", "libx264", "-preset", "ultrafast", "-threads:v", "1", "-c:a", codec, "-ac", "6", "-b:a", "640k", source)
			if b, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("fixture: %v %s", e, b)
			}
			hashes := func(path string) string {
				b, e := exec.Command("ffprobe", "-v", "error", "-select_streams", "a", "-show_packets", "-show_data_hash", "sha256", "-show_entries", "packet=data_hash", "-of", "csv=p=0", path).Output()
				if e != nil {
					t.Fatal(e)
				}
				return string(b)
			}
			original := hashes(source)
			for _, convert := range []bool{false, true} {
				f, e := os.Open(source)
				if e != nil {
					t.Fatal(e)
				}
				target := filepath.Join(t.TempDir(), "output.mp4")
				out, e := os.Create(target)
				if e != nil {
					t.Fatal(e)
				}
				p := Plan{AudioIndex: 1, ConvertVideo: convert, TargetWidth: 320, TargetHeight: 180, VideoBitrateKbps: 1000}
				args := RemuxArgs(p, "h264", 0)
				if convert {
					args = TranscodeArgs(p, 0, "software")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				e = Remux(ctx, f, args, out)
				cancel()
				f.Close()
				out.Close()
				if e != nil {
					t.Fatal(e)
				}
				if original == "" || hashes(target) != original {
					t.Fatal("Dolby packets must remain unchanged")
				}
				result, e := os.Open(target)
				if e != nil {
					t.Fatal(e)
				}
				probe, e := RunProbe(context.Background(), result)
				result.Close()
				if e != nil {
					t.Fatal(e)
				}
				if len(probe.Streams) != 2 {
					t.Fatal("unexpected chapter/data track", probe.Streams)
				}
				if probe.DurationMS() < 2800 || VideoStream(probe).CodecName != "h264" {
					t.Fatal(probe)
				}
				// Ensure there are actual media bytes, rather than accepting only a header.
				check, _ := os.Open(target)
				n, _ := io.Copy(io.Discard, check)
				check.Close()
				if n < 1000 {
					t.Fatal("empty output")
				}
			}
		})
	}
}

func TestVideoTranscodeSeekDiscardsOnlyPriorAudio(t *testing.T) {
	source := filepath.Join(t.TempDir(), "long-gop.mkv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-filter_threads", "1", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24:d=10", "-f", "lavfi", "-i", "sine=frequency=440:duration=10", "-c:v", "libx264", "-preset", "ultrafast", "-threads:v", "1", "-g", "240", "-keyint_min", "240", "-sc_threshold", "0", "-c:a", "ac3", source)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, b)
	}
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	path := filepath.Join(t.TempDir(), "seek.mp4")
	out, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	args := TranscodeArgs(Plan{AudioIndex: 1, ConvertVideo: true, TargetWidth: 320, TargetHeight: 180, VideoBitrateKbps: 1000}, 5, "software")
	args = append(args[:len(args)-1], "-t", "2", "pipe:1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	e = Remux(ctx, f, args, out)
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
	if probe.DurationMS() < 1900 || probe.DurationMS() > 2100 {
		t.Fatalf("audio preroll desynchronizes seek: duration %d", probe.DurationMS())
	}
}
