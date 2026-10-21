package config

import (
	"path/filepath"
	"testing"
)

func TestCacheDir(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	data := filepath.Join("/home/me", ".local", "share", "maktaba")
	for _, tc := range []struct {
		name, goos, dataDir string
		inApp               bool
		xdg, want           string
	}{
		{"the Linux app", "linux", data, true, "", filepath.Join("/home/me", ".cache", "maktaba")},
		{"the Linux app, XDG_CACHE_HOME", "linux", data, true, "/var/cache/me", filepath.Join("/var/cache/me", "maktaba")},
		{"a data_dir the owner chose", "linux", "/srv/maktaba", true, "", filepath.Join("/srv/maktaba", "cache")},
		{"a Linux install outside the app", "linux", data, false, "", filepath.Join(data, "cache")},
		{"macOS", "darwin", data, true, "", filepath.Join(data, "cache")},
	} {
		env["XDG_CACHE_HOME"] = tc.xdg
		got := cacheDir(tc.goos, getenv, "/home/me", tc.dataDir, tc.dataDir, data, tc.inApp)
		if got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
