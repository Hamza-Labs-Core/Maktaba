package capability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The source matrix every TV profile is decided against (T01).
var tvSources = map[string]Source{
	"h264-1080-aac-mp4":         {Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000, AudioChannels: 2, BitDepth: 8},
	"h264-1080-ac3-mkv":         {Container: "matroska,webm", VideoCodec: "h264", AudioCodec: "ac3", Height: 1080, BitrateKbps: 10000, AudioChannels: 6, BitDepth: 8},
	"hevc-2160-hdr10-eac3-mkv":  {Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "eac3", HDR: "hdr10", Height: 2160, BitrateKbps: 40000, AudioChannels: 6, BitDepth: 10},
	"hevc-2160-dv81-truehd-mkv": {Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "truehd", HDR: "hdr10", DVProfile: 8, DVCompat: 1, Height: 2160, BitrateKbps: 50000, AudioChannels: 8, BitDepth: 10},
	"hevc-2160-dv5-eac3-mp4":    {Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "hevc", AudioCodec: "eac3", DVProfile: 5, Height: 2160, BitrateKbps: 30000, AudioChannels: 6, BitDepth: 10},
	"av1-2160-hdr10-opus-mkv":   {Container: "matroska,webm", VideoCodec: "av1", AudioCodec: "opus", HDR: "hdr10", Height: 2160, BitrateKbps: 20000, AudioChannels: 2, BitDepth: 10},
	"h264-1080-dts-mkv":         {Container: "matroska,webm", VideoCodec: "h264", AudioCodec: "dts", Height: 1080, BitrateKbps: 12000, AudioChannels: 6, BitDepth: 8},
}

// fullReport is what the platform's newest models report: everything the
// ceiling allows.
func fullReport() *DeviceCaps {
	yes := true
	c := &DeviceCaps{
		Containers:       []string{"mp4", "mkv", "ts"},
		Video:            []VideoCap{{Codec: "h264", MaxHeight: 2160}, {Codec: "hevc", MaxHeight: 2160, BitDepth: 10}, {Codec: "av1", MaxHeight: 2160, BitDepth: 10}},
		HDR:              []string{"hdr10", "hlg", "dv-p5", "dv-p8"},
		Audio:            []string{"aac", "ac3", "eac3"},
		Passthrough:      []string{"eac3", "truehd"},
		MaxAudioChannels: 8,
		HLSSegments:      []string{"ts", "fmp4"},
		DASH:             &yes,
		MaxBitrateKbps:   80000,
	}
	return c
}

type tvRow struct {
	mode     Mode
	segments string
	format   string
	audio    string
	stripDV  bool
}

func (r tvRow) String() string {
	return fmt.Sprintf("%s seg=%q fmt=%q audio=%q strip=%v", r.mode, r.segments, r.format, r.audio, r.stripDV)
}

// TestTVProfiles_VerdictTable is T01: each new profile × the source matrix,
// without a report (the baseline) and with its newest models' full report.
func TestTVProfiles_VerdictTable(t *testing.T) {
	tc := tvRow{mode: ModeTranscode, segments: "ts"}
	want := map[string]map[string]tvRow{}
	// the baseline: H.264 1080p in MP4 direct, everything else an H.264 transcode in TS,
	// or a remux of an H.264 file in another container
	for _, name := range []string{"firetv", "tizen", "webos", "roku", "cast", "vega"} {
		dolby := name == "firetv" || name == "tizen" || name == "webos"
		ac3 := tvRow{mode: ModeRemux, segments: "ts", audio: "aac"}
		dts := tvRow{mode: ModeRemux, segments: "ts", audio: "aac"}
		if dolby {
			ac3 = tvRow{mode: ModeRemux, segments: "ts"}
			dts = tvRow{mode: ModeRemux, segments: "ts", audio: "eac3"}
		}
		want[name+"/baseline"] = map[string]tvRow{
			"h264-1080-aac-mp4":         {mode: ModeDirect},
			"h264-1080-ac3-mkv":         ac3,
			"hevc-2160-hdr10-eac3-mkv":  tc,
			"hevc-2160-dv81-truehd-mkv": tc,
			"hevc-2160-dv5-eac3-mp4":    tc,
			"av1-2160-hdr10-opus-mkv":   tc,
			"h264-1080-dts-mkv":         dts,
		}
	}
	// the full reports, clamped to each ceiling
	want["firetv/full"] = map[string]tvRow{
		"h264-1080-aac-mp4":         {mode: ModeDirect},
		"h264-1080-ac3-mkv":         {mode: ModeDirect},
		"hevc-2160-hdr10-eac3-mkv":  {mode: ModeDirect},
		"hevc-2160-dv81-truehd-mkv": {mode: ModeDirect},
		"hevc-2160-dv5-eac3-mp4":    {mode: ModeDirect},
		"av1-2160-hdr10-opus-mkv":   {mode: ModeRemux, segments: "fmp4", audio: "aac"},
		"h264-1080-dts-mkv":         {mode: ModeRemux, segments: "ts", audio: "eac3"},
	}
	want["tizen/full"] = map[string]tvRow{
		"h264-1080-aac-mp4":         {mode: ModeDirect},
		"h264-1080-ac3-mkv":         {mode: ModeDirect},
		"hevc-2160-hdr10-eac3-mkv":  {mode: ModeDirect},
		"hevc-2160-dv81-truehd-mkv": {mode: ModeRemux, segments: "fmp4", audio: "eac3", stripDV: true},
		"hevc-2160-dv5-eac3-mp4":    tc,
		"av1-2160-hdr10-opus-mkv":   {mode: ModeRemux, segments: "fmp4", audio: "aac"},
		"h264-1080-dts-mkv":         {mode: ModeRemux, segments: "ts", audio: "eac3"},
	}
	want["webos/full"] = want["tizen/full"]
	want["vega/full"] = want["tizen/full"]
	want["roku/full"] = map[string]tvRow{
		"h264-1080-aac-mp4":         {mode: ModeDirect},
		"h264-1080-ac3-mkv":         {mode: ModeDirect},
		"hevc-2160-hdr10-eac3-mkv":  {mode: ModeDirect},
		"hevc-2160-dv81-truehd-mkv": {mode: ModeRemux, segments: "fmp4", audio: "eac3", stripDV: true},
		"hevc-2160-dv5-eac3-mp4":    {mode: ModeDirect},
		"av1-2160-hdr10-opus-mkv":   {mode: ModeRemux, format: "dash", audio: "aac"},
		"h264-1080-dts-mkv":         {mode: ModeRemux, segments: "ts", audio: "eac3"},
	}
	want["cast/full"] = map[string]tvRow{
		"h264-1080-aac-mp4":         {mode: ModeDirect},
		"h264-1080-ac3-mkv":         {mode: ModeRemux, segments: "ts"},
		"hevc-2160-hdr10-eac3-mkv":  {mode: ModeRemux, segments: "fmp4"},
		"hevc-2160-dv81-truehd-mkv": {mode: ModeRemux, segments: "fmp4", audio: "eac3"},
		"hevc-2160-dv5-eac3-mp4":    {mode: ModeDirect},
		"av1-2160-hdr10-opus-mkv":   {mode: ModeRemux, segments: "fmp4", audio: "aac"},
		"h264-1080-dts-mkv":         {mode: ModeRemux, segments: "ts", audio: "eac3"},
	}

	r := NewRegistry()
	for key, rows := range want {
		name, kind, _ := strings.Cut(key, "/")
		p, known := r.Get(name)
		if !known {
			t.Fatalf("profile %s not registered", name)
		}
		var caps *DeviceCaps
		if kind == "full" {
			caps = fullReport()
		}
		for srcName, w := range rows {
			v := r.DecideFor(p, caps, tvSources[srcName], Override{})
			got := tvRow{mode: v.Mode, segments: v.Segments, format: v.Format, audio: v.AudioCodec, stripDV: v.StripDV}
			if got != w {
				t.Errorf("%s × %s: got %s, want %s (%s)", key, srcName, got, w, v.Reason)
			}
		}
	}
}

// T02: a report widening past the ceiling is clamped to it, and Reason says so.
func TestTVProfiles_ReportClampedToCeiling(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("tizen")
	caps := &DeviceCaps{
		Containers:     []string{"mp4", "mkv", "avi"},
		Video:          []VideoCap{{Codec: "h264", MaxHeight: 4320}, {Codec: "hevc", MaxHeight: 4320}},
		HDR:            []string{"hdr10", "dv-p8"},
		Audio:          []string{"aac", "eac3", "truehd"},
		MaxBitrateKbps: 500000,
	}
	eff, clamped := Apply(p, caps)
	if eff.MaxHeight != 2160 || eff.heightFor("hevc") != 2160 || eff.MaxBitrateKbps != 80000 {
		t.Fatalf("not clamped: height %d hevc %d bitrate %d", eff.MaxHeight, eff.heightFor("hevc"), eff.MaxBitrateKbps)
	}
	for _, w := range []string{"container:avi", "height:hevc 4320>2160", "hdr:dv-p8", "audio:truehd", "bitrate:500000>80000"} {
		if !strings.Contains(strings.Join(clamped, ","), w) {
			t.Errorf("clamped %v lacks %q", clamped, w)
		}
	}
	// an 8K HEVC file asked for direct: transcoded within the ceiling, the reason naming the clamp
	src := Source{Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "eac3", Height: 4320, BitrateKbps: 60000, AudioChannels: 6}
	v := r.DecideFor(p, caps, src, Override{})
	if v.Mode != ModeTranscode || v.HeightCap > 2160 {
		t.Fatalf("8K on tizen: got %s cap %d (%s)", v.Mode, v.HeightCap, v.Reason)
	}
	if !strings.Contains(v.Reason, "clamped to the tizen ceiling") || len(v.Clamped) == 0 {
		t.Fatalf("reason does not name the clamp: %q", v.Reason)
	}
	// Samsung never gets Dolby Vision, whatever its report says
	if has(expandHDR(eff.HDR), "dv-p8", canonHDR) {
		t.Fatal("tizen took Dolby Vision from a report")
	}
}

// A report narrows freely: a Fire TV Stick without HEVC gets an H.264 transcode.
func TestTVProfiles_ReportNarrows(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("firetv")
	caps := &DeviceCaps{Video: []VideoCap{{Codec: "h264", MaxHeight: 1080}}, HDR: []string{}}
	src := tvSources["hevc-2160-hdr10-eac3-mkv"]
	if v := r.DecideFor(p, caps, src, Override{}); v.Mode != ModeTranscode || v.HeightCap != 1080 {
		t.Fatalf("got %s cap %d (%s)", v.Mode, v.HeightCap, v.Reason)
	}
	// an older profile is its own ceiling: a report never widens it
	g, _ := r.Get("generic")
	eff, clamped := Apply(g, fullReport())
	if eff.MaxHeight != 720 || len(clamped) == 0 {
		t.Fatalf("generic widened: height %d clamped %v", eff.MaxHeight, clamped)
	}
}

// The story's acceptance cases.
func TestTVProfiles_Acceptance(t *testing.T) {
	r := NewRegistry()
	// a Fire TV listing HEVC 2160p and HDR10 opens a 4K HEVC HDR10 MKV: direct
	ft, _ := r.Get("firetv")
	caps := &DeviceCaps{Containers: []string{"mp4", "mkv"}, Video: []VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{"hdr10"}, Audio: []string{"aac", "eac3"}}
	if v := r.DecideFor(ft, caps, tvSources["hevc-2160-hdr10-eac3-mkv"], Override{}); v.Mode != ModeDirect {
		t.Errorf("firetv 4K HDR10: %s (%s)", v.Mode, v.Reason)
	}
	// a Cast receiver listing fmp4 only: an HEVC remux goes in fMP4
	cast, _ := r.Get("cast")
	caps = &DeviceCaps{Video: []VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{"hdr10"}, Audio: []string{"aac", "eac3"}, HLSSegments: []string{"fmp4"}}
	if v := r.DecideFor(cast, caps, tvSources["hevc-2160-hdr10-eac3-mkv"], Override{}); v.Mode != ModeRemux || v.Segments != "fmp4" {
		t.Errorf("cast HEVC: %s seg %q (%s)", v.Mode, v.Segments, v.Reason)
	}
	// and an H.264 transcode for it too, as the device plays fMP4 alone
	if v := r.DecideFor(cast, caps, tvSources["hevc-2160-dv5-eac3-mp4"], Override{ForceTranscode: true}); v.Segments != "fmp4" {
		t.Errorf("cast transcode segments %q", v.Segments)
	}
	// a Samsung TV (no dv-* in its report) and a DV 8.1 source: remux to HDR10
	tz, _ := r.Get("tizen")
	caps = &DeviceCaps{Containers: []string{"mp4", "mkv"}, Video: []VideoCap{{Codec: "hevc", MaxHeight: 2160}}, HDR: []string{"hdr10", "hlg"}, Audio: []string{"aac", "eac3"}, HLSSegments: []string{"ts", "fmp4"}}
	src := tvSources["hevc-2160-dv81-truehd-mkv"]
	src.AudioCodec, src.AudioChannels = "eac3", 6
	if v := r.DecideFor(tz, caps, src, Override{}); v.Mode != ModeRemux || !v.StripDV {
		t.Errorf("tizen DV 8.1: %s strip %v (%s)", v.Mode, v.StripDV, v.Reason)
	}
	// a DV 8.4 source goes as HLG
	src.DVCompat = 4
	if v := r.DecideFor(tz, caps, src, Override{}); v.Mode != ModeRemux || !strings.Contains(v.Reason, "hlg base") {
		t.Errorf("tizen DV 8.4: %s (%s)", v.Mode, v.Reason)
	}
	// profile 7 to its base layer
	src.DVProfile, src.DVCompat = 7, 6
	if v := r.DecideFor(tz, caps, src, Override{}); v.Mode != ModeRemux || !v.StripDV {
		t.Errorf("tizen DV 7: %s (%s)", v.Mode, v.Reason)
	}
	// a Roku report claiming AV1 inside HLS: AV1 goes by DASH
	rk, _ := r.Get("roku")
	yes := true
	caps = &DeviceCaps{Video: []VideoCap{{Codec: "av1", MaxHeight: 2160}}, HDR: []string{"hdr10"}, HLSSegments: []string{"ts", "fmp4"}, DASH: &yes}
	if v := r.DecideFor(rk, caps, tvSources["av1-2160-hdr10-opus-mkv"], Override{Format: "hls"}); v.Mode != ModeRemux || v.Format != "dash" {
		t.Errorf("roku AV1: %s fmt %q (%s)", v.Mode, v.Format, v.Reason)
	}
	// and without DASH in its report, a transcode
	no := false
	caps.DASH = &no
	if v := r.DecideFor(rk, caps, tvSources["av1-2160-hdr10-opus-mkv"], Override{}); v.Mode != ModeTranscode {
		t.Errorf("roku AV1 without DASH: %s (%s)", v.Mode, v.Reason)
	}
	// Roku plays Dolby Vision only as profile 5: a DV 8 file goes as its base layer
	caps = fullReport()
	if v := r.DecideFor(rk, caps, tvSources["hevc-2160-dv81-truehd-mkv"], Override{}); !v.StripDV {
		t.Errorf("roku DV 8: strip %v (%s)", v.StripDV, v.Reason)
	}
	// a stereo AAC track beside a Dolby one, on Roku only
	if v := r.DecideFor(rk, caps, tvSources["h264-1080-dts-mkv"], Override{}); !v.StereoAAC || v.AudioCodec != "eac3" {
		t.Errorf("roku stereo: %v audio %q", v.StereoAAC, v.AudioCodec)
	}
	if v := r.DecideFor(ft, fullReport(), tvSources["h264-1080-dts-mkv"], Override{}); v.StereoAAC {
		t.Error("firetv got a stereo track beside its Dolby one")
	}
}

// A 10-bit source on a device decoding 8-bit HEVC only is transcoded.
func TestTVProfiles_BitDepthAndFPS(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("firetv")
	caps := &DeviceCaps{Video: []VideoCap{{Codec: "hevc", MaxHeight: 2160, BitDepth: 8, MaxFPS: 30}}, HDR: []string{"hdr10"}}
	src := tvSources["hevc-2160-hdr10-eac3-mkv"]
	if v := r.DecideFor(p, caps, src, Override{}); v.Mode != ModeTranscode {
		t.Fatalf("10-bit on 8-bit: %s", v.Mode)
	}
	src.BitDepth, src.FPS = 8, 59.94
	if v := r.DecideFor(p, caps, src, Override{}); v.Mode != ModeTranscode {
		t.Fatalf("60 fps on 30: %s", v.Mode)
	}
}

// Unknown keys in a report are ignored, known ones kept (T06's parse half).
func TestParseDeviceCaps_UnknownKeysIgnored(t *testing.T) {
	c, err := ParseDeviceCaps([]byte(`{"containers":["mp4"],"future_key":{"x":1},"video":[{"codec":"hevc","max_height":2160,"new":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Containers) != 1 || len(c.Video) != 1 || c.Video[0].MaxHeight != 2160 || c.HDR != nil {
		t.Fatalf("got %+v", c)
	}
	if _, err := ParseDeviceCaps([]byte(`{"containers":"mp4"}`)); err == nil {
		t.Fatal("a wrong type was accepted")
	}
	if c, err := ParseDeviceCaps(nil); c != nil || err != nil {
		t.Fatal("no report should be nil")
	}
	a, _ := json.Marshal(fullReport())
	ca, _ := ParseDeviceCaps(a)
	cb, _ := ParseDeviceCaps(a)
	if ca.Key() != cb.Key() || ca.Key() == "" || (*DeviceCaps)(nil).Key() != "" {
		t.Fatal("key not stable")
	}
}
