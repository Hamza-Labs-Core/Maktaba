//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func mkRuntime(t *testing.T, platform string) (data, copyDir string) {
	t.Helper()
	data = t.TempDir()
	copyDir = filepath.Join(data, "runtime", "v0.3.0_1a2b3c4d")
	if err := os.MkdirAll(filepath.Join(copyDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pj := `{"schema": 1, "version": "v0.3.0", "platform": "` + platform + `"}`
	if err := os.WriteFile(filepath.Join(copyDir, "payload.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	return data, copyDir
}

func TestRuntimePayload(t *testing.T) {
	data, copyDir := mkRuntime(t, payloadPlatform())
	bin := filepath.Join(copyDir, "bin")

	// no current link yet: the copy itself
	if got, ok := runtimePayload(bin); !ok || got != copyDir {
		t.Errorf("without current: %q %v", got, ok)
	}
	// current points at it: the stable path, so the venv's Python path survives updates
	if err := os.Symlink("v0.3.0_1a2b3c4d", filepath.Join(data, "runtime", "current")); err != nil {
		t.Fatal(err)
	}
	if got, ok := runtimePayload(bin); !ok || got != filepath.Join(data, "runtime", "current") {
		t.Errorf("with current: %q %v", got, ok)
	}
	if got := runtimeStateRoot("", filepath.Join(data, "runtime", "current")); got != data {
		t.Errorf("state root %q, want %q", got, data)
	}
	if got := runtimeStateRoot("/flag", copyDir); got != "/flag" {
		t.Errorf("--root ignored: %q", got)
	}

	// not a runtime: no payload.json, another platform, not a bin directory
	if _, ok := runtimePayload(filepath.Join(t.TempDir(), "bin")); ok {
		t.Error("a directory without payload.json")
	}
	_, other := mkRuntime(t, "linux-riscv64")
	if _, ok := runtimePayload(filepath.Join(other, "bin")); ok {
		t.Error("a payload for another platform")
	}
	if _, ok := runtimePayload(copyDir); ok {
		t.Error("the payload directory itself is not its bin/")
	}
}
