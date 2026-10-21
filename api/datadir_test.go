package main

import (
	"path/filepath"
	"testing"
)

// The API's data volume without MAKTABA_DATA_DIR: /var/lib/maktaba for a
// system service, the user's XDG data dir otherwise (a user cannot write
// /var/lib, so the Sigstore root's refresh would fail there).
func TestDefaultDataDir(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "me")
	xdg := t.TempDir() // absolute on every OS
	for _, c := range []struct {
		euid      int
		xdg, home string
		want      string
	}{
		{0, xdg, home, "/var/lib/maktaba"},
		{1000, xdg, home, filepath.Join(xdg, "maktaba")},
		{1000, "relative", home, filepath.Join(home, ".local", "share", "maktaba")},
		{1000, "", home, filepath.Join(home, ".local", "share", "maktaba")},
		{1000, "", "", "/var/lib/maktaba"},
	} {
		if got := defaultDataDir(c.euid, c.xdg, c.home); got != c.want {
			t.Errorf("defaultDataDir(%d, %q, %q) = %q, want %q", c.euid, c.xdg, c.home, got, c.want)
		}
	}
	t.Setenv("MAKTABA_DATA_DIR", " /srv/maktaba ")
	if got := dataDirFromEnv(); got != "/srv/maktaba" {
		t.Errorf("MAKTABA_DATA_DIR: %q", got)
	}
}
