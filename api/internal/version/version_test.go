package version

import (
	"strings"
	"testing"
)

func TestStringIncludesAllFields(t *testing.T) {
	t.Parallel()

	Version = "v1.2.3"
	Commit = "abc1234"
	BuildDate = "1700000000"
	t.Cleanup(func() {
		Version = "unknown"
		Commit = "unknown"
		BuildDate = "unknown"
	})

	got := String()
	for _, want := range []string{"v1.2.3", "abc1234", "1700000000"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, want to contain %q", got, want)
		}
	}
}

func TestChannelFromVersion(t *testing.T) {
	t.Setenv("MAKTABA_UPDATE_CHANNEL", "") // ensure no operator override
	cases := []struct {
		v    string
		want string
	}{
		{"v1.4.2", "stable"},
		{"1.4.2", "stable"},
		{"v1.5.0-rc.1", "beta"},
		{"v1.5.0-beta.2", "beta"},
		{"v2.0.0-alpha.1", "beta"},
		{"nightly-20260101", "beta"},
		{"unknown", "stable"},
		{"dev", "stable"},
	}
	orig := Version
	t.Cleanup(func() { Version = orig })
	for _, c := range cases {
		Version = c.v
		if got := Channel(); got != c.want {
			t.Errorf("Channel() for %q = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestChannelEnvOverride(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "v1.4.2" // a stable build…
	t.Setenv("MAKTABA_UPDATE_CHANNEL", "beta")
	if got := Channel(); got != "beta" {
		t.Fatalf("env override should win: got %q want beta", got)
	}
}
