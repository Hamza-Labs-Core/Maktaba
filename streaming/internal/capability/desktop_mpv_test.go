package capability

import "testing"

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
