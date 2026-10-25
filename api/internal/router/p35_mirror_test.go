package router

import (
	"path/filepath"
	"testing"
)

// The home's own copies of a release are in its side-by-side layout, beside the release
// the launcher runs (MAKTABA_RELEASE_DIR); another kind of install has none.
func TestHomeReleaseCopies(t *testing.T) {
	if homeReleaseCopies("") != nil {
		t.Fatal("an install with no release dir has copies")
	}
	root := filepath.Join("srv", "maktaba", "bin", "releases")
	dirs := homeReleaseCopies(filepath.Join(root, "1.4.0") + string(filepath.Separator))("v1.5.0")
	if len(dirs) != 2 || dirs[0] != filepath.Join(root, "1.5.0") || dirs[1] != filepath.Join(root, "1.5.0", "pipeline") {
		t.Fatalf("dirs = %v", dirs)
	}
}
