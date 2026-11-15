package media

import (
	"encoding/json"
	"strings"
	"testing"
)

// Muestras sintéticas con la forma real de «ffprobe -show_format -show_streams -of json» (recortadas a los campos que
// importan). Cada caso comprueba la lista completa de insignias, en orden.

func probeFrom(t *testing.T, js string) Probe {
	t.Helper()
	var p Probe
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		t.Fatalf("muestra no válida: %v", err)
	}
	return p
}

func ids(b []TechBadge) string {
	var s []string
	for _, x := range b {
		s = append(s, x.Category+":"+x.ID)
	}
	return strings.Join(s, " ")
}

func TestTechBadgesSyntheticProbes(t *testing.T) {
	cases := []struct {
		name, file, probe, want string
	}{
		{
			name: "UHD Dolby Vision con TrueHD Atmos 7.1 en MKV",
			file: "Peliculas/Duna (2021)/Duna.2021.2160p.UHD.BluRay.mkv",
			probe: `{"streams":[
				{"index":0,"codec_name":"hevc","profile":"Main 10","codec_type":"video","width":3840,"height":1608,"pix_fmt":"yuv420p10le",
				 "color_transfer":"smpte2084","r_frame_rate":"24000/1001","avg_frame_rate":"24000/1001","disposition":{"default":1},
				 "side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":7}]},
				{"index":1,"codec_name":"truehd","profile":"Dolby TrueHD + Dolby Atmos","codec_type":"audio","channels":8,"channel_layout":"7.1","disposition":{"default":1}},
				{"index":2,"codec_name":"ac3","codec_type":"audio","channels":6,"channel_layout":"5.1(side)","disposition":{"default":0}},
				{"index":3,"codec_name":"subrip","codec_type":"subtitle"}],
				"format":{"format_name":"matroska,webm","duration":"9300.1"}}`,
			want: "resolution:4k hdr:dovi video:hevc audio:atmos channels:7.1 fps:24 bitdepth:10 container:mkv",
		},
		{
			name: "1080p SDR H.264 con AAC estéreo en MP4 y carátula incrustada",
			file: "Series/Serie/Temporada 1/Serie S01E01.mp4",
			probe: `{"streams":[
				{"index":0,"codec_name":"mjpeg","codec_type":"video","width":600,"height":900,"disposition":{"attached_pic":1}},
				{"index":1,"codec_name":"h264","profile":"High","codec_type":"video","width":1920,"height":1080,"pix_fmt":"yuv420p",
				 "color_transfer":"bt709","avg_frame_rate":"25/1","r_frame_rate":"25/1","disposition":{"default":1}},
				{"index":2,"codec_name":"aac","profile":"LC","codec_type":"audio","channels":2,"channel_layout":"stereo","disposition":{"default":1}}],
				"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`,
			want: "resolution:fhd hdr:sdr video:h264 audio:aac channels:2.0 fps:25 container:mp4",
		},
		{
			name: "HDR10+ por el nombre solo si el vídeo es PQ; DTS-HD MA 5.1",
			file: "Pelis/Peli.2019.2160p.HDR10Plus.DTS-HD.MA.5.1.mkv",
			probe: `{"streams":[
				{"index":0,"codec_name":"hevc","codec_type":"video","width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_transfer":"smpte2084","avg_frame_rate":"24/1"},
				{"index":1,"codec_name":"dts","profile":"DTS-HD MA","codec_type":"audio","channels":6,"channel_layout":"5.1(side)"}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:4k hdr:hdr10plus video:hevc audio:dtshd_ma channels:5.1 fps:24 bitdepth:10 container:mkv",
		},
		{
			name: "HDR10+ en el nombre pero vídeo SDR: SDR",
			file: "Peli.HDR10+.1080p.mkv",
			probe: `{"streams":[{"index":0,"codec_name":"h264","codec_type":"video","width":1920,"height":1080,"pix_fmt":"yuv420p"}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:fhd hdr:sdr video:h264 container:mkv",
		},
		{
			name: "DTS:X IMAX es IMAX Enhanced; HLG; AV1 12 bit; 60 FPS",
			file: "Docu/Docu.mkv",
			probe: `{"streams":[
				{"index":0,"codec_name":"av1","codec_type":"video","width":3840,"height":2160,"pix_fmt":"yuv420p12le","color_transfer":"arib-std-b67","avg_frame_rate":"60000/1001"},
				{"index":1,"codec_name":"dts","profile":"DTS-HD MA + DTS:X IMAX","codec_type":"audio","channels":8,"channel_layout":"7.1"}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:4k hdr:hlg video:av1 audio:dtsx channels:7.1 fps:60 bitdepth:12 imax:imax_enhanced container:mkv",
		},
		{
			name: "Dolby Digital Plus con Atmos (JOC) 7.1.4 por número de canales; WebM con VP9",
			file: "Clip.webm",
			probe: `{"streams":[
				{"index":0,"codec_name":"vp9","codec_type":"video","width":2560,"height":1440,"pix_fmt":"yuv420p","avg_frame_rate":"30/1"},
				{"index":1,"codec_name":"eac3","profile":"Dolby Digital Plus + Dolby Atmos","codec_type":"audio","channels":12}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:qhd hdr:sdr video:vp9 audio:atmos channels:7.1.4 fps:30 container:webm",
		},
		{
			name: "DVD: MPEG-2 SD con AC-3 mono y PCM; gana PCM",
			file: "Viejas/Peli.avi",
			probe: `{"streams":[
				{"index":0,"codec_name":"mpeg2video","codec_type":"video","width":720,"height":576,"pix_fmt":"yuv420p","avg_frame_rate":"25/1"},
				{"index":1,"codec_name":"ac3","codec_type":"audio","channels":1,"channel_layout":"mono","disposition":{"default":1}},
				{"index":2,"codec_name":"pcm_s16le","codec_type":"audio","channels":2}],
				"format":{"format_name":"avi"}}`,
			want: "resolution:sd hdr:sdr video:mpeg2 audio:pcm channels:2.0 fps:25 container:avi",
		},
		{
			name: "3D por modo estéreo de Matroska; IMAX por el nombre; FLAC; ffprobe antiguo sin perfil ni fps",
			file: "Avatar.2009.IMAX.3D.mkv",
			probe: `{"streams":[
				{"index":0,"codec_name":"h264","codec_type":"video","width":1920,"height":1080,"pix_fmt":"yuv420p","tags":{"stereo_mode":"left_right"}},
				{"index":1,"codec_name":"flac","codec_type":"audio","channels":6}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:fhd hdr:sdr video:h264 audio:flac channels:5.1 3d:3d imax:imax container:mkv",
		},
		{
			name: "Atmos y DTS:X por el título de la pista si ffprobe no da el perfil; M2TS; VVC",
			file: "BD/00001.m2ts",
			probe: `{"streams":[
				{"index":0,"codec_name":"vvc","codec_type":"video","width":1280,"height":720},
				{"index":1,"codec_name":"dts","codec_type":"audio","channels":8,"tags":{"title":"English DTS:X 7.1"}},
				{"index":2,"codec_name":"eac3","codec_type":"audio","channels":6,"tags":{"title":"Atmos"}}],
				"format":{"format_name":"mpegts"}}`,
			want: "resolution:hd hdr:sdr video:vvc audio:dtsx channels:7.1 container:m2ts",
		},
		{
			name: "Lo desconocido no se muestra: Opus 2.1, códec raro, sin extensión conocida",
			file: "raro.ogv",
			probe: `{"streams":[
				{"index":0,"codec_name":"theora","codec_type":"video","width":0,"height":0,"avg_frame_rate":"0/0","r_frame_rate":"17/1"},
				{"index":1,"codec_name":"opus","codec_type":"audio","channels":3,"channel_layout":"2.1"}],
				"format":{"format_name":"ogg"}}`,
			want: "hdr:sdr",
		},
		{
			name: "Solo audio: sin insignias de vídeo; Dolby Digital 5.1 y DTS normal: gana DTS",
			file: "musica.mkv",
			probe: `{"streams":[
				{"index":0,"codec_name":"ac3","codec_type":"audio","channels":6,"channel_layout":"5.1(side)","disposition":{"default":1}},
				{"index":1,"codec_name":"dts","profile":"DTS","codec_type":"audio","channels":6,"channel_layout":"5.1(side)"}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "audio:dts channels:5.1 container:mkv",
		},
		{
			name: "Panorámica recortada 1920×800 sigue siendo Full HD; 1280×536 es HD; 3D por el nombre H-SBS",
			file: "Peli.3D.HSBS.1080p.mkv",
			probe: `{"streams":[{"index":0,"codec_name":"h264","codec_type":"video","width":1920,"height":800,"bits_per_raw_sample":"8","pix_fmt":"yuv420p"}],
				"format":{"format_name":"matroska,webm"}}`,
			want: "resolution:fhd hdr:sdr video:h264 3d:3d container:mkv",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(TechBadges(probeFrom(t, c.probe), c.file))
			if got != c.want {
				t.Errorf("\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}

func TestTechBadgesResolutionThresholds(t *testing.T) {
	for _, c := range []struct {
		w, h int
		want string
	}{{7680, 4320, "8k"}, {3840, 1600, "4k"}, {2560, 1440, "qhd"}, {1920, 800, "fhd"}, {1440, 1080, "fhd"},
		{1280, 536, "hd"}, {1280, 720, "hd"}, {720, 576, "sd"}, {640, 360, "sd"}, {0, 0, ""}} {
		if got, _ := resolutionBadge(c.w, c.h); got != c.want {
			t.Errorf("%d×%d: %q, quería %q", c.w, c.h, got, c.want)
		}
	}
}

func TestTechBadgesFrameRates(t *testing.T) {
	for rate, want := range map[string]int{"24000/1001": 24, "24/1": 24, "25/1": 25, "30000/1001": 30, "50/1": 50,
		"60000/1001": 60, "120/1": 120, "0/0": 0, "17/1": 0, "": 0, "15/1": 0} {
		if got := frameRate(Stream{AvgFrameRate: rate}); got != want {
			t.Errorf("%q: %d, quería %d", rate, got, want)
		}
	}
}

// Los análisis guardados antes de añadir los campos nuevos siguen funcionando, y uno vacío o roto da una lista vacía.
func TestTechBadgesJSONStoredProbe(t *testing.T) {
	if b := TechBadgesJSON([]byte(`{}`), "x.mkv"); len(b) != 1 || b[0].ID != "mkv" {
		t.Errorf("análisis vacío: %v", b)
	}
	if b := TechBadgesJSON(nil, "x.mkv"); b == nil || len(b) != 0 {
		t.Errorf("sin análisis: %#v", b)
	}
	if b := TechBadgesJSON([]byte(`no es json`), "x.mkv"); b == nil || len(b) != 0 {
		t.Errorf("análisis roto: %#v", b)
	}
	// Un análisis guardado tal como lo serializa el escáner (json.Marshal(Probe)) da lo mismo que el original.
	p := probeFrom(t, `{"streams":[{"index":0,"codec_name":"hevc","codec_type":"video","width":3840,"height":2160,"pix_fmt":"yuv420p10le",
		"color_transfer":"smpte2084","avg_frame_rate":"24000/1001"},{"index":1,"codec_name":"eac3","codec_type":"audio","channels":6,"channel_layout":"5.1(side)"}],
		"format":{"format_name":"matroska,webm"}}`)
	raw, _ := json.Marshal(p)
	if got, want := ids(TechBadgesJSON(raw, "a.mkv")), ids(TechBadges(p, "a.mkv")); got != want || !strings.Contains(got, "fps:24") {
		t.Errorf("guardado: %s, original: %s", got, want)
	}
}
