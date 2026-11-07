package router

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
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

// Story 54.07 §3 (#709): the updates service's list of the plugins a release would pause
// is plugin_installs read through plugins.PausedBy.
func TestPluginsPausedBy(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest)
		VALUES ('dev.example.old', '1.0.0', 'd', '/nowhere', '{"maktaba":"<1.6.0"}')`); err != nil {
		t.Fatal(err)
	}
	paused := pluginsPausedBy(db)
	got, err := paused(context.Background(), "1.6.0")
	if err != nil || len(got) != 1 || got[0].ID != "dev.example.old" || got[0].Range != "<1.6.0" || got[0].Enabled {
		t.Fatalf("1.6.0: %+v %v", got, err)
	}
	if got, err := paused(context.Background(), "1.5.9"); err != nil || len(got) != 0 {
		t.Fatalf("1.5.9: %+v %v", got, err)
	}
}
