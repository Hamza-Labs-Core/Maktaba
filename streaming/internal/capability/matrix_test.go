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
