package capability

import (
	"slices"
	"testing"
)

// A phone's report, as the Android Player reads it from its own decoders
// (DeviceCapsReader): hardware H.264 and HEVC to 2160p, the platform's audio
// decoders and a phone maker's Dolby ones, no HDR screen.
func phoneReport() *DeviceCaps {
	dash := true
	return &DeviceCaps{
		Containers:       []string{"mp4", "mkv", "ts"},
		Video:            []VideoCap{{Codec: "h264", MaxHeight: 2160, MaxFPS: 60}, {Codec: "hevc", MaxHeight: 2160, MaxFPS: 60, BitDepth: 10}},
		HDR:              []string{},
		Audio:            []string{"aac", "ac3", "eac3", "opus", "flac", "mp3", "vorbis", "truehd"},
		Passthrough:      []string{},
		MaxAudioChannels: 8,
		HLSSegments:      []string{"ts", "fmp4"},
		DASH:             &dash,
	}
}

// The Android Player sent no profile before, so the home used "generic"
// (H.264 and AAC only, MP4, 720p and 4 Mbit/s at most) and transcoded nearly
// every file: a 1080p film waited for a transcode's first segment on every
// rung before its first frame. With its own profile and report, what the phone
// decodes plays as it is.
func TestAndroidNative_PhoneReportPlaysWhatThePhoneDecodes(t *testing.T) {
	r := NewRegistry()
	mkv := "matroska,webm"
	mp4 := "mov,mp4,m4a,3gp,3g2,mj2"
	film := func(container, video, audio string) Source {
		return Source{Container: container, VideoCodec: video, AudioCodec: audio, AudioChannels: 6,
			Height: 1080, BitrateKbps: 8000}
	}
	cases := []struct {
		name    string
		profile string
		report  *DeviceCaps
		src     Source
		want    Mode
	}{
		// before: no profile is "generic"
		{"no profile, H.264 AAC 1080p MKV", "", nil, film(mkv, "h264", "aac"), ModeTranscode},
		{"no profile, H.264 AAC 1080p MP4", "", nil, film(mp4, "h264", "aac"), ModeTranscode},
		// after
		{"phone, H.264 AAC 1080p MKV", "android-native", phoneReport(), film(mkv, "h264", "aac"), ModeDirect},
		{"phone, H.264 AC-3 1080p MKV", "android-native", phoneReport(), film(mkv, "h264", "ac3"), ModeDirect},
		{"phone, HEVC E-AC-3 1080p MKV", "android-native", phoneReport(), film(mkv, "hevc", "eac3"), ModeDirect},
		// what the phone cannot decode still transcodes
		{"phone without DTS, DTS", "android-native", phoneReport(), film(mkv, "h264", "dts"), ModeTranscode},
		{"phone without AV1 hardware, AV1", "android-native", phoneReport(), film(mkv, "av1", "opus"), ModeTranscode},
		{"phone on an SDR screen, HDR10", "android-native", phoneReport(), Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "aac", HDR: "hdr10", Height: 2160, BitrateKbps: 20000}, ModeTranscode},
		// no report (it could not be read): the profile itself, Dolby audio not assumed
		{"android-native alone, AC-3", "android-native", nil, film(mkv, "h264", "ac3"), ModeTranscode},
		{"android-native alone, AAC", "android-native", nil, film(mkv, "h264", "aac"), ModeDirect},
	}
	for _, c := range cases {
		p, _ := r.Get(c.profile)
		got := r.DecideFor(p, c.report, c.src, Override{})
		if got.Mode != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Mode, got.Reason, c.want)
		}
	}
}

// The report widens android-native only up to its ceiling: TrueHD (no Android
// phone decodes it) is clamped, and named.
func TestAndroidNative_ReportClampedToTheCeiling(t *testing.T) {
	p, _ := NewRegistry().Get("android-native")
	eff, clamped := Apply(p, phoneReport())
	if !slices.Contains(clamped, "audio:truehd") {
		t.Fatalf("clamped %v, want audio:truehd", clamped)
	}
	if slices.Contains(eff.AudioCodecs, "truehd") || !slices.Contains(eff.AudioCodecs, "eac3") {
		t.Fatalf("audio %v", eff.AudioCodecs)
	}
}

// A report's per-codec limits hold for android-native too (it is not one of
// the Detailed TV profiles): an 8-bit HEVC decoder, or H.264 only to 1080p,
// gets a transcode of what it cannot decode.
func TestAndroidNative_PerCodecLimitsOfTheReport(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("android-native")
	rep := phoneReport()
	rep.Video = []VideoCap{{Codec: "h264", MaxHeight: 1080, MaxFPS: 30}, {Codec: "hevc", MaxHeight: 2160, BitDepth: 8}}
	mkv := "matroska,webm"
	cases := []struct {
		name string
		src  Source
		want Mode
	}{
		{"10-bit HEVC on an 8-bit decoder", Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "aac", Height: 1080, BitDepth: 10, BitrateKbps: 6000}, ModeTranscode},
		{"8-bit HEVC 2160p", Source{Container: mkv, VideoCodec: "hevc", AudioCodec: "aac", Height: 2160, BitDepth: 8, BitrateKbps: 15000}, ModeDirect},
		{"H.264 2160p on a 1080p decoder", Source{Container: mkv, VideoCodec: "h264", AudioCodec: "aac", Height: 2160, BitrateKbps: 15000}, ModeTranscode},
		{"H.264 60 fps on a 30 fps decoder", Source{Container: mkv, VideoCodec: "h264", AudioCodec: "aac", Height: 1080, FPS: 59.94, BitrateKbps: 8000}, ModeTranscode},
		{"H.264 1080p 24 fps", Source{Container: mkv, VideoCodec: "h264", AudioCodec: "aac", Height: 1080, FPS: 23.976, BitrateKbps: 8000}, ModeDirect},
	}
	for _, c := range cases {
		if got := r.DecideFor(p, rep, c.src, Override{}); got.Mode != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, got.Mode, got.Reason, c.want)
		}
	}
}
