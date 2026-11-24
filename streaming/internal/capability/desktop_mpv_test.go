package capability

import (
	"strings"
	"testing"
)

// Story 13.18 T03: the desktop Player on libmpv plays the home's files as they are. Under
// desktop-player (JavaFX: H.264 and AAC in MP4 or HLS) the same files were remuxed or
// transcoded before their first frame.
func TestDesktopMpv_PlaysTheFilesAsTheyAre(t *testing.T) {
	r := NewRegistry()
	mkv := "matroska,webm"
	film := func(video, audio string, height, channels int) Source {
		return Source{Container: mkv, VideoCodec: video, AudioCodec: audio, Height: height,
			AudioChannels: channels, BitrateKbps: 12000}
	}
	cases := []struct {
		name    string
		profile string
		src     Source
		want    Mode
	}{
		{"JavaFX, H.264 AC-3 5.1 MKV", "desktop-player", film("h264", "ac3", 1080, 6), ModeTranscode},
		{"JavaFX, HEVC 10-bit", "desktop-player", film("hevc", "aac", 2160, 2), ModeTranscode},
		{"mpv, H.264 AC-3 5.1 MKV", "desktop-mpv", film("h264", "ac3", 1080, 6), ModeDirect},
		{"mpv, HEVC 10-bit 2160p", "desktop-mpv", Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160, BitDepth: 10, AudioChannels: 6, BitrateKbps: 40000}, ModeDirect},
		{"mpv, DTS-HD MA 7.1", "desktop-mpv", film("h264", "dts-hd-ma", 1080, 8), ModeDirect},
		{"mpv, AV1 Opus WebM", "desktop-mpv", film("av1", "opus", 1080, 2), ModeDirect},
		{"mpv, MPEG-2 in TS", "desktop-mpv", Source{Container: "mpegts", VideoCodec: "mpeg2video", AudioCodec: "ac3", Height: 576, AudioChannels: 2, BitrateKbps: 6000}, ModeDirect},
		{"mpv, HDR10 (tone-mapped by mpv)", "desktop-mpv", Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "aac", HDR: "hdr10", Height: 2160, BitrateKbps: 30000}, ModeDirect},
		{"mpv, H.264 AAC MP4", "desktop-mpv", Source{Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000}, ModeDirect},
		// what mpv is not asked to play still transcodes: TrueHD, Dolby Vision without a
		// base layer it shows, a bitrate over the profile's
		{"mpv, TrueHD", "desktop-mpv", film("hevc", "truehd", 2160, 8), ModeTranscode},
		{"mpv, 120 Mbit/s", "desktop-mpv", Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "aac", Height: 2160, BitrateKbps: 120000}, ModeTranscode},
	}
	for _, c := range cases {
		p, known := r.Get(c.profile)
		if !known {
			t.Fatalf("%s: no profile %q", c.name, c.profile)
		}
		if got := r.Decide(p, c.src, Override{}); got.Mode != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Mode, got.Reason, c.want)
		}
	}
}

// T05's home side: a file mpv could not open comes back with force_transcode.
func TestDesktopMpv_ForceTranscode(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("desktop-mpv")
	src := Source{Container: "matroska,webm", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000}
	if got := r.Decide(p, src, Override{ForceTranscode: true}); got.Mode != ModeTranscode {
		t.Fatalf("%s, want transcode", got.Mode)
	}
}

func yes() *bool { b := true; return &b }
func no() *bool  { b := false; return &b }

// mpvReport is a desktop Player's report (story 13.19): HEVC 10-bit and VP9 in hardware, AV1
// in software up to avHeight, H.264 in software up to 2160, and the audio device's channels.
func mpvReport(avHeight, channels int) *DeviceCaps {
	return &DeviceCaps{
		Video: []VideoCap{
			{Codec: "h264", MaxHeight: 2160, Hardware: no()},
			{Codec: "hevc", MaxHeight: 2160, BitDepth: 10, Hardware: yes()},
			{Codec: "vp9", MaxHeight: 2160, BitDepth: 10, Hardware: yes()},
			{Codec: "av1", MaxHeight: avHeight, BitDepth: 10, Hardware: no()},
			{Codec: "mpeg2video", MaxHeight: 2160, Hardware: no()},
		},
		MaxAudioChannels: channels,
	}
}

// Story 13.19 T08: desktop-mpv has no ceiling, so a report only narrows it, per codec. A
// 2160p AV1 file on a machine that decodes AV1 in software up to 1080p is transcoded, and the
// verdict says why; a 1080p one plays directly, and HEVC 10-bit in hardware plays at 2160p.
func TestDesktopMpv_TheReportNarrowsPerCodec(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("desktop-mpv")
	caps := mpvReport(1080, 6)
	av1 := func(h int) Source {
		return Source{Container: "matroska,webm", VideoCodec: "av1", AudioCodec: "opus", Height: h, BitDepth: 10,
			AudioChannels: 2, BitrateKbps: 15000}
	}
	v := r.DecideFor(p, caps, av1(2160), Override{})
	if v.Mode != ModeTranscode || v.Reason != "source height exceeds the device's: AV1 decoded in software up to 1080p" {
		t.Fatalf("2160p AV1: %s (%s)", v.Mode, v.Reason)
	}
	// the transcode is H.264 no taller than the report's H.264, in stereo (an Opus stereo source)
	if v.ReportHeightCap != 2160 || v.AudioCodec != "aac" {
		t.Fatalf("2160p AV1's transcode: height cap %d, audio %q", v.ReportHeightCap, v.AudioCodec)
	}
	if v := r.DecideFor(p, caps, av1(1080), Override{}); v.Mode != ModeDirect {
		t.Fatalf("1080p AV1: %s (%s), want direct", v.Mode, v.Reason)
	}
	hevc := Source{Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160, BitDepth: 10,
		AudioChannels: 6, BitrateKbps: 40000}
	if v := r.DecideFor(p, caps, hevc, Override{}); v.Mode != ModeDirect || v.AudioCodec != "" {
		t.Fatalf("2160p HEVC 10-bit in hardware: %s (%s), audio %q; want direct, the audio as it is", v.Mode, v.Reason, v.AudioCodec)
	}
	// direct play is never narrowed by the channels: mpv folds 5.1 down for stereo speakers
	if v := r.DecideFor(p, mpvReport(1080, 2), hevc, Override{}); v.Mode != ModeDirect {
		t.Fatalf("5.1 on a stereo device: %s (%s), want direct", v.Mode, v.Reason)
	}
	// a codec the probe could not decode is left out of the report: transcoded
	noAV1 := &DeviceCaps{Video: []VideoCap{{Codec: "h264", MaxHeight: 1080, Hardware: no()}}}
	if v := r.DecideFor(p, noAV1, av1(720), Override{}); v.Mode != ModeTranscode || v.ReportHeightCap != 1080 {
		t.Fatalf("AV1 not in the report: %s (%s), cap %d", v.Mode, v.Reason, v.ReportHeightCap)
	}
	// a 10-bit file on a machine whose report says 8-bit says how the codec is decoded
	eight := &DeviceCaps{Video: []VideoCap{{Codec: "hevc", MaxHeight: 2160, BitDepth: 8, Hardware: yes()}}}
	if v := r.DecideFor(p, eight, hevc, Override{}); v.Mode != ModeTranscode || !strings.Contains(v.Reason, "(decoded in hardware)") {
		t.Fatalf("HEVC 10-bit on an 8-bit decoder: %s (%s)", v.Mode, v.Reason)
	}
	// a report from before `hardware` (or Android's): heights still narrow, as before
	old := &DeviceCaps{Video: []VideoCap{{Codec: "av1", MaxHeight: 1080}}}
	if v := r.DecideFor(p, old, av1(2160), Override{}); v.Mode != ModeTranscode || v.Reason != "source height exceeds profile" {
		t.Fatalf("a report without hardware: %s (%s)", v.Mode, v.Reason)
	}
}

// Story 13.19 T09: a desktop-mpv transcode keeps up to the report's channels: 6 or more (and a
// source of 6 or more) give E-AC-3 5.1, fewer stereo AAC. No report keeps every transcode's
// audio as it was ("": the encoder's AAC at the source's channels), so a Player from before the
// report, an open before its probe ended and a forced reopen are not folded to stereo; a source
// of unknown channels is never upmixed. Other profiles' transcodes are untouched.
func TestDesktopMpv_TranscodeAudioFollowsTheReportsChannels(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("desktop-mpv")
	surround := Source{Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "truehd", Height: 2160,
		AudioChannels: 8, BitrateKbps: 40000}
	cases := []struct {
		name string
		caps *DeviceCaps
		src  Source
		want string
	}{
		{"6 channels", mpvReport(2160, 6), surround, "eac3"},
		{"8 channels", mpvReport(2160, 8), surround, "eac3"},
		{"2 channels", mpvReport(2160, 2), surround, "aac"},
		{"no report", nil, surround, ""},
		{"a report without channels (no audio device)", mpvReport(2160, 0), surround, ""},
		{"a stereo source on 5.1 speakers", mpvReport(2160, 6), Source{Container: "mp4", VideoCodec: "hevc",
			AudioCodec: "truehd", Height: 2160, AudioChannels: 2, BitrateKbps: 40000}, "aac"},
		{"a source of unknown channels on 5.1 speakers", mpvReport(2160, 6), Source{Container: "mp4", VideoCodec: "hevc",
			AudioCodec: "truehd", Height: 2160, BitrateKbps: 40000}, ""},
		{"a source of unknown channels on stereo speakers", mpvReport(2160, 2), Source{Container: "mp4", VideoCodec: "hevc",
			AudioCodec: "truehd", Height: 2160, BitrateKbps: 40000}, "aac"},
	}
	for _, c := range cases {
		v := r.DecideFor(p, c.caps, c.src, Override{})
		if v.Mode != ModeTranscode || v.AudioCodec != c.want {
			t.Errorf("%s: %s (%s), audio %q; want transcode with %q", c.name, v.Mode, v.Reason, v.AudioCodec, c.want)
		}
	}
	// a forced transcode is sent no report (as on Android): its audio is as it always was
	if v := r.DecideFor(p, nil, surround, Override{ForceTranscode: true}); v.AudioCodec != "" || v.ReportHeightCap != 0 {
		t.Errorf("force_transcode without a report: audio %q, cap %d", v.AudioCodec, v.ReportHeightCap)
	}
	// every other profile's transcode is as it was
	for _, name := range []string{"desktop-player", "generic", "android-native"} {
		q, _ := r.Get(name)
		if v := r.DecideFor(q, &DeviceCaps{MaxAudioChannels: 6}, surround, Override{}); v.AudioCodec != "" || v.ReportHeightCap != 0 {
			t.Errorf("%s: audio %q, cap %d; want neither", name, v.AudioCodec, v.ReportHeightCap)
		}
	}
}

// Story 13.19 T10: `hardware` is read and is part of the report's key; a report differing only
// in it opens its own session.
func TestDeviceCaps_Hardware(t *testing.T) {
	c, err := ParseDeviceCaps([]byte(`{"video":[{"codec":"av1","max_height":1080,"bit_depth":10,"hardware":false},{"codec":"hevc","max_height":2160,"hardware":true}],"max_audio_channels":6}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Video[0].Hardware == nil || *c.Video[0].Hardware || c.Video[1].Hardware == nil || !*c.Video[1].Hardware {
		t.Fatalf("hardware not read: %+v", c.Video)
	}
	other, _ := ParseDeviceCaps([]byte(`{"video":[{"codec":"av1","max_height":1080,"bit_depth":10,"hardware":true},{"codec":"hevc","max_height":2160,"hardware":true}],"max_audio_channels":6}`))
	unsaid, _ := ParseDeviceCaps([]byte(`{"video":[{"codec":"av1","max_height":1080,"bit_depth":10},{"codec":"hevc","max_height":2160,"hardware":true}],"max_audio_channels":6}`))
	if c.Key() == other.Key() || c.Key() == unsaid.Key() || other.Key() == unsaid.Key() {
		t.Fatalf("keys: %s %s %s; want three", c.Key(), other.Key(), unsaid.Key())
	}
	// the containers, audio and HDR left out stay the profile's
	p, _ := NewRegistry().Get("desktop-mpv")
	eff, clamped := Apply(p, c)
	if len(clamped) != 0 || len(eff.Containers) != len(p.Containers) || len(eff.AudioCodecs) != len(p.AudioCodecs) ||
		len(eff.HDR) != len(p.HDR) || eff.MaxAudioChannels != 6 || !eff.VideoHardware["hevc"] || eff.VideoHardware["av1"] {
		t.Fatalf("applied: %+v, clamped %v", eff, clamped)
	}
	// more than the profile's 8 channels is 8
	if eff, _ := Apply(p, &DeviceCaps{MaxAudioChannels: 12}); eff.MaxAudioChannels != 8 {
		t.Fatalf("12 channels: %d", eff.MaxAudioChannels)
	}
}
