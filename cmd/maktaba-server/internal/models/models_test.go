package models

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookup(t *testing.T) {
	t.Parallel()
	if _, ok := Lookup("mlx-whisper-large-v3"); !ok {
		t.Error("expected mlx-whisper-large-v3 in catalog")
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("did not expect unknown model")
	}
}

func TestInstalledDetection(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	if Installed(data, "mlx-whisper-large-v3") {
		t.Error("nothing should be installed yet")
	}
	// Empty dir does not count as installed.
	dir := filepath.Join(Dir(data), "mlx-whisper-large-v3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if Installed(data, "mlx-whisper-large-v3") {
		t.Error("empty dir should not count as installed")
	}
	// A file inside flips it to installed.
	if err := os.WriteFile(filepath.Join(dir, "weights.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Installed(data, "mlx-whisper-large-v3") {
		t.Error("non-empty dir should count as installed")
	}
	got := ListInstalled(data)
	if len(got) != 1 || got[0] != "mlx-whisper-large-v3" {
		t.Errorf("ListInstalled = %v", got)
	}
}

func TestDownloadUnknownModel(t *testing.T) {
	t.Parallel()
	if err := Download(t.TempDir(), "bogus"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}
