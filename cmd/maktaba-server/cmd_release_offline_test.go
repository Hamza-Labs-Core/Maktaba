package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseOfflineNeedsItsFlags(t *testing.T) {
	out := filepath.Join(t.TempDir(), "o.tar")
	for _, c := range []struct{ dir, ver, plat, out string }{
		{"", "0.4.2", "linux-amd64", out}, {"d", "", "linux-amd64", out}, {"d", "0.4.2", "", out}, {"d", "0.4.2", "linux-amd64", ""},
	} {
		if _, err := releaseOffline(context.Background(), c.dir, c.ver, c.plat, []string{"runtime.ml"}, c.out); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

// A release folder without its signed files makes no file, and leaves no part behind.
func TestReleaseOfflineRefusesAnUnsignedFolder(t *testing.T) {
	out := filepath.Join(t.TempDir(), "maktaba-offline-0.4.2-linux-amd64-full.tar")
	if _, err := releaseOffline(context.Background(), t.TempDir(), "0.4.2", "linux-amd64", []string{"runtime.ml"}, out); err == nil {
		t.Fatal("an offline file from an empty folder")
	}
	for _, p := range []string{out, out + ".part"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was left", p)
		}
	}
}

func TestReleaseOfflineIsHidden(t *testing.T) {
	c := newReleaseOfflineCmd()
	if !c.Hidden || c.Use != "release-offline" {
		t.Fatalf("%s hidden=%v", c.Use, c.Hidden)
	}
}
