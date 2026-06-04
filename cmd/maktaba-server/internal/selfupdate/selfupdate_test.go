package selfupdate

import "testing"

func TestNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"1.2.1", "1.2.0", true},
		{"1.2.0", "1.2.0", false},
		{"1.2.0-rc1", "1.2.0", false}, // suffix stripped -> equal
		{"2.0.0", "1.9.9", true},
		{"dev", "1.0.0", false}, // unparseable never beats a release
		{"1.0.0", "dev", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckNoArtifactForPlatform(t *testing.T) {
	t.Parallel()
	m := Manifest{Version: "9.9.9", Artifacts: map[string]Artifact{
		"plan9/sparc": {URL: "x", SHA256: "y"},
	}}
	d := Check("1.0.0", m)
	if d.Available {
		t.Error("should not be available when no artifact matches platform")
	}
	if d.Latest != "9.9.9" {
		t.Errorf("latest = %q", d.Latest)
	}
}

func TestCheckAvailable(t *testing.T) {
	t.Parallel()
	m := Manifest{Version: "9.9.9", Artifacts: map[string]Artifact{
		platformKey(): {URL: "http://x", SHA256: "abc"},
	}}
	d := Check("1.0.0", m)
	if !d.Available {
		t.Fatal("expected update available")
	}
	if d.Artifact.URL != "http://x" {
		t.Errorf("artifact url = %q", d.Artifact.URL)
	}
}
