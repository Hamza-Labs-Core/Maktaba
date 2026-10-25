package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Story 35.6: the release a process runs from, read off its binary's path (pruning keeps
// it); a TV release records the binary's own "v1.5.0" while its directory is 1.5.0.
func TestReleaseOfPathAndBareVersions(t *testing.T) {
	for p, want := range map[string]string{
		filepath.Join("srv", "maktaba", "bin", "releases", "1.5.0", streamingExe()): "1.5.0",
		filepath.Join("srv", "maktaba", "bin", streamingExe()):                      "",
		"": "",
	} {
		if got := releaseOfPath(p); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
	if bareVersion("v1.5.0") != "1.5.0" || bareVersion("1.5.0") != "1.5.0" || bareVersion("vX") != "vX" {
		t.Fatal("bareVersion")
	}

	root := t.TempDir()
	dir := filepath.Join(root, "releases", "1.5.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, streamingExe())
	if err := os.WriteFile(bin, []byte("streaming 1.5.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	rb, err := ResolveReleaseBinary(root, "v1.5.0", "", "v1.6.0", "")
	if err != nil || rb.Path != bin {
		t.Fatalf("a release recorded as v1.5.0: %+v %v", rb, err)
	}
	sum, _ := fileSHA256(bin)

	// the release's .verified gives the binary's checksum (what the launcher checked)
	b, _ := json.Marshal(map[string]any{"sha256": map[string]string{streamingExe(): sum}, "identity": "test"})
	if err := os.WriteFile(filepath.Join(dir, ".verified"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReleaseStreaming(Options{ReleaseDir: dir}, "v1.5.0")
	if err != nil || got.SHA256 != sum || got.Version != "v1.5.0" || got.Path != bin {
		t.Fatalf("release streaming: %+v %v", got, err)
	}
}
