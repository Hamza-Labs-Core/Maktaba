package client

import (
	"context"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Info
	}{
		{"player/1.3.0 (ios; phone)", Info{"player", "1.3.0", "ios", "phone"}},
		{"remote/1.0 (android; tablet)", Info{"remote", "1.0", "android", "tablet"}},
		{"web/0.3.25 (macos; desktop)", Info{"web", "0.3.25", "macos", "desktop"}},
		{"tv/1.0 (androidtv; tv)", Info{"tv", "1.0", "androidtv", "tv"}},
		{"  TV/1.0(tvOS;TV)  ", Info{"tv", "1.0", "tvos", "tv"}},
		{"native", Info{"native", Unknown, Unknown, Unknown}},
		{"NATIVE", Info{"native", Unknown, Unknown, Unknown}},
		{"", None},
		{"   ", None},
		{"player", Info{"player", Unknown, Unknown, Unknown}},
		{"player/ (ios)", Info{"player", Unknown, "ios", Unknown}},
		{"(ios; phone)", Info{Unknown, Unknown, "ios", "phone"}},
		{"web/1 (mac os!; <script>)", Info{"web", "1", "macos", "script"}},
		{"wéb/1.0 (ios; phone)", Info{"wb", "1.0", "ios", "phone"}},
		{"%%%/$$$ (;;)", Info{Unknown, Unknown, Unknown, Unknown}},
	}
	for _, c := range cases {
		if got := Parse(c.in); got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// Every part is bounded, so a long header cannot grow a label or a row.
func TestParseBounded(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := Parse(long + "/" + long + " (" + long + "; " + long + ")")
	for _, p := range []string{got.App, got.Version, got.Platform, got.Device} {
		if len(p) != maxPart {
			t.Errorf("part %q is %d long, want %d", p, len(p), maxPart)
		}
	}
}

func TestIsNative(t *testing.T) {
	for in, want := range map[string]bool{
		"native":                          true,
		"player/1.0 (ios; phone)":         true,
		"remote/1.0 (android; tablet)":    true,
		"tv/1.0 (tvos; tv)":               true,
		"web/0.3.25 (macos; desktop)":     false,
		"desktop/0.3.25 (macos; desktop)": false,
		"mobile/0.3.25 (ios; phone)":      false,
		"":                                false,
		"!!!":                             false,
	} {
		if got := Parse(in).IsNative(); got != want {
			t.Errorf("Parse(%q).IsNative() = %v, want %v", in, got, want)
		}
	}
}

func TestMetricLabel(t *testing.T) {
	for in, want := range map[string]string{
		"web/1 (web; desktop)":   "web",
		"player/1 (ios; phone)":  "player",
		"native":                 "native",
		"":                       Unknown,
		"someapp/1 (ios; phone)": "other",
	} {
		if got := Parse(in).MetricLabel(); got != want {
			t.Errorf("Parse(%q).MetricLabel() = %q, want %q", in, got, want)
		}
	}
}

func TestContext(t *testing.T) {
	if got := From(context.Background()); got != None {
		t.Errorf("From(empty) = %+v, want None", got)
	}
	info := Parse("tv/2 (tvos; tv)")
	if got := From(With(context.Background(), info)); got != info {
		t.Errorf("From(With(info)) = %+v, want %+v", got, info)
	}
}
