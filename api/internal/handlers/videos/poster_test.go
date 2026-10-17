package videos

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPosterCandidatesPreferThePosterThenTheCache(t *testing.T) {
	root := t.TempDir()
	hash := "ab12" + "00000000000000000000000000000000000000000000000000000000000000"[:60]
	got := posterCandidates(root, "vid-1", hash, "/media/poster.jpg")
	want := []string{
		"/media/poster.jpg",
		filepath.Join(root, hash, "poster.jpg"),
		filepath.Join(root, hash, "poster.webp"),
		filepath.Join(root, hash, "small.jpg"),
		filepath.Join(root, "imported", "vid-1.jpg"),
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d = %s, want %s", i, got[i], want[i])
		}
	}
	if c := posterCandidates(root, "vid-1", "", ""); len(c) != 1 || c[0] != filepath.Join(root, "imported", "vid-1.jpg") {
		t.Fatalf("no hash, no poster: %v", c)
	}
}

func TestThumbnailRootHonoursTheEnv(t *testing.T) {
	t.Setenv("MAKTABA_THUMBNAIL_ROOT", "/srv/thumbs")
	if got := thumbnailRoot(); got != "/srv/thumbs" {
		t.Fatalf("root = %s", got)
	}
	t.Setenv("MAKTABA_THUMBNAIL_ROOT", "")
	home, _ := os.UserHomeDir()
	if got := thumbnailRoot(); got != filepath.Join(home, ".maktaba", "cache", "thumbnails") {
		t.Fatalf("default root = %s", got)
	}
}
