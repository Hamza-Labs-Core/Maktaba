package capability

import "testing"

func TestRegistry_UnknownProfileFallsBackToGeneric(t *testing.T) {
	r := NewRegistry()
	p, known := r.Get("not-a-real-profile")
	if known {
		t.Fatal("expected fallback")
	}
	if p == nil || p.Name != "generic" {
		t.Fatalf("got %+v", p)
	}
}

func TestDecide_Direct(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("ios-native")
	src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000, AudioChannels: 2}
	v := r.Decide(p, src, Override{})
	if v.Mode != ModeDirect {
		t.Fatalf("want direct, got %s (%s)", v.Mode, v.Reason)
	}
}

// An unknown audio codec (a track row the probe has not filled in) is never played
// direct, on any profile: the transcode encodes the audio to AAC.
func TestDecide_UnknownAudioCodecTranscodes(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"ios-native", "generic", "browser-chrome"} {
		p, _ := r.Get(name)
		src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "", Height: 1080, BitrateKbps: 6000}
		if v := r.Decide(p, src, Override{}); v.Mode != ModeTranscode {
			t.Errorf("%s: want transcode, got %s (%s)", name, v.Mode, v.Reason)
		}
	}
}

func TestDecide_RemuxOnContainerMismatch(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("ios-native")
	src := Source{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000}
	v := r.Decide(p, src, Override{})
	if v.Mode != ModeRemux {
		t.Fatalf("want remux, got %s (%s)", v.Mode, v.Reason)
	}
}

func TestDecide_TranscodeOnCodecMismatch(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("generic")
	src := Source{Container: "mp4", VideoCodec: "av1", AudioCodec: "opus", Height: 720, BitrateKbps: 3000}
	v := r.Decide(p, src, Override{})
	if v.Mode != ModeTranscode {
		t.Fatalf("want transcode, got %s (%s)", v.Mode, v.Reason)
	}
}

func TestDecide_TranscodeOnHeightExceeds(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("generic")
	src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 2160, BitrateKbps: 3000}
	v := r.Decide(p, src, Override{})
	if v.Mode != ModeTranscode {
		t.Fatalf("want transcode, got %s", v.Mode)
	}
	if v.HeightCap != 720 {
		t.Fatalf("height cap=%d want 720", v.HeightCap)
	}
}

func TestDecide_TranscodeOnBitrateExceeds(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("generic")
	src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 720, BitrateKbps: 10000}
	v := r.Decide(p, src, Override{})
	if v.Mode != ModeTranscode {
		t.Fatalf("want transcode, got %s", v.Mode)
	}
	if v.BitrateCapKbps != 4000 {
		t.Fatalf("bitrate cap=%d want 4000", v.BitrateCapKbps)
	}
}

func TestDecide_ForceTranscodeOverride(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("ios-native")
	src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000}
	v := r.Decide(p, src, Override{ForceTranscode: true})
	if v.Mode != ModeTranscode {
		t.Fatalf("force_transcode should win, got %s", v.Mode)
	}
}

func TestDecide_OverrideBitrateBeatsProfile(t *testing.T) {
	r := NewRegistry()
	p, _ := r.Get("ios-native")
	src := Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000}
	v := r.Decide(p, src, Override{MaxBitrateKbps: 1500})
	if v.Mode != ModeTranscode {
		t.Fatalf("override should force transcode, got %s", v.Mode)
	}
	if v.BitrateCapKbps != 1500 {
		t.Fatalf("cap=%d", v.BitrateCapKbps)
	}
}

func TestDecide_TableDriven(t *testing.T) {
	r := NewRegistry()

	type tc struct {
		name    string
		profile string
		src     Source
		want    Mode
	}
	cases := []tc{
		{"chrome direct mp4", "browser-chrome",
			Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000}, ModeDirect},
		{"safari remux mkv h264+aac", "browser-safari",
			Source{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 6000}, ModeTranscode},
		// safari does not list mkv even though codecs match — chrome would remux it; safari falls to transcode by codec list shape.
		{"androidtv direct h265 mkv", "androidtv",
			Source{Container: "mkv", VideoCodec: "h265", AudioCodec: "ac3", Height: 2160, BitrateKbps: 30000}, ModeDirect},
		{"desktop player direct mp4 1080p", "desktop-player",
			Source{Container: "mov,mp4,m4a,3gp,3g2,mj2", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000}, ModeDirect},
		{"desktop player transcodes h265", "desktop-player",
			Source{Container: "mp4", VideoCodec: "h265", AudioCodec: "aac", Height: 1080, BitrateKbps: 8000}, ModeTranscode},
		{"desktop player transcodes 4k", "desktop-player",
			Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 2160, BitrateKbps: 8000}, ModeTranscode},
		{"generic transcode 4k", "generic",
			Source{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Height: 2160, BitrateKbps: 8000}, ModeTranscode},
	}

	// For "safari remux": safari's containers list omits mkv; codecs are h264+aac which are in profile, so the path is "container not in profile" → remux.
	cases[1].want = ModeRemux

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := r.Get(c.profile)
			v := r.Decide(p, c.src, Override{})
			if v.Mode != c.want {
				t.Fatalf("%s: got %s (%s) want %s", c.name, v.Mode, v.Reason, c.want)
			}
		})
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	r.Register(&Profile{Name: "custom", Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 1080, MaxBitrateKbps: 5000, SupportsHLS: true})
	p, ok := r.Get("custom")
	if !ok || p.Name != "custom" {
		t.Fatalf("custom profile not registered: %+v ok=%v", p, ok)
	}
}

// The probe stores ffprobe's format_name: a list of names, matroska for mkv.
func TestDecide_ProbeContainerNames(t *testing.T) {
	r := NewRegistry()
	cases := []struct {
		profile, container string
		want               Mode
	}{
		{"browser-chrome", "mov,mp4,m4a,3gp,3g2,mj2", ModeDirect},
		{"ios-native", "mov,mp4,m4a,3gp,3g2,mj2", ModeDirect},
		{"androidtv", "matroska,webm", ModeDirect},
		{"ios-native", "matroska,webm", ModeRemux},
	}
	for _, c := range cases {
		p, _ := r.Get(c.profile)
		src := Source{Container: c.container, VideoCodec: "h264", AudioCodec: "aac", Height: 720, BitrateKbps: 2000}
		if v := r.Decide(p, src, Override{}); v.Mode != c.want {
			t.Errorf("%s %q: got %s (%s), want %s", c.profile, c.container, v.Mode, v.Reason, c.want)
		}
	}
}

// The older profiles meet ffprobe's codec names ("hevc", "eac3"), not their
// own spellings: an Android player direct-plays an HEVC file it decodes, and
// the profiles whose players cannot take any HEVC file keep transcoding it.
func TestDecide_ProbeCodecNames(t *testing.T) {
	r := NewRegistry()
	mkv := "matroska,webm"
	mp4 := "mov,mp4,m4a,3gp,3g2,mj2"
	cases := []struct {
		profile, container, video, audio, hdr string
		dv                                    int
		want                                  Mode
	}{
		{"androidtv", mkv, "hevc", "eac3", "", 0, ModeDirect},
		{"android-native", mp4, "hevc", "aac", "", 0, ModeDirect},
		{"android-native", mkv, "hevc", "aac", "hdr10", 0, ModeDirect},
		// HDR the profile does not show: a transcode (tone-mapped), not the
		// HDR picture on an SDR screen
		{"android-native", mkv, "hevc", "aac", "", 5, ModeTranscode},
		{"browser-chrome", mp4, "av1", "opus", "hdr10", 0, ModeTranscode},
		{"androidtv", mkv, "hevc", "eac3", "hdr10", 5, ModeDirect},
		// Chrome decodes HEVC only on some hardware, Firefox never; Safari and
		// AVPlayer refuse an MP4 whose HEVC is tagged hev1 (the probe does not
		// record the tag): HEVC still transcodes for them
		{"browser-chrome", mp4, "hevc", "aac", "", 0, ModeTranscode},
		{"browser-safari", mp4, "hevc", "aac", "", 0, ModeTranscode},
		{"ios-native", mp4, "hevc", "aac", "", 0, ModeTranscode},
		{"tvos", mp4, "hevc", "eac3", "", 0, ModeTranscode},
		{"desktop-player", mp4, "hevc", "aac", "", 0, ModeTranscode},
		{"generic", mp4, "hevc", "aac", "", 0, ModeTranscode},
		// a codec's other spellings read as its one name
		{"androidtv", mkv, "h265", "ec-3", "", 0, ModeDirect},
		{"ios-native", mp4, "avc1", "E-AC-3", "", 0, ModeDirect},
	}
	for _, c := range cases {
		p, _ := r.Get(c.profile)
		src := Source{Container: c.container, VideoCodec: c.video, AudioCodec: c.audio, HDR: c.hdr,
			DVProfile: c.dv, Height: 2160, BitrateKbps: 20000}
		if v := r.Decide(p, src, Override{}); v.Mode != c.want {
			t.Errorf("%s %s/%s/%s dv%d: got %s (%s), want %s", c.profile, c.video, c.audio, c.hdr, c.dv, v.Mode, v.Reason, c.want)
		}
	}
}
