package config

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	t.Parallel()
	cfg, found, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("found should be false for a missing file")
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("default listen = %q", cfg.Server.Listen)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "server.toml")

	in := Defaults()
	in.Media.Roots = []string{"/mnt/videos", "/mnt/movies"}
	in.Database.URL = "postgres://localhost/maktaba"
	in.Transcription.Backend = "faster-whisper"
	if err := in.SaveTo(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, found, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !found {
		t.Fatal("found should be true after save")
	}
	if out.Database.URL != in.Database.URL {
		t.Errorf("url = %q, want %q", out.Database.URL, in.Database.URL)
	}
	if len(out.Media.Roots) != 2 || out.Media.Roots[1] != "/mnt/movies" {
		t.Errorf("roots = %v", out.Media.Roots)
	}
}

func TestPartialFileLayersOverDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "server.toml")
	// Only the database URL is set; everything else should default.
	if err := writeFile(path, "[database]\nurl = \"sqlite:///tmp/x.db\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Database.URL != "sqlite:///tmp/x.db" {
		t.Errorf("url = %q", cfg.Database.URL)
	}
	if cfg.Server.Listen != "0.0.0.0:8080" {
		t.Errorf("listen should keep default, got %q", cfg.Server.Listen)
	}
}
