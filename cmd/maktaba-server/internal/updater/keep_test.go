package updater

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// writeMigrations puts empty Postgres migrations (and their SQLite siblings) up to slot
// top into dir/migrations.
func writeMigrations(t *testing.T, dir string, slots ...string) {
	t.Helper()
	m := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(m, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range slots {
		for _, n := range []string{s + "_x.sql", s + "_x.sqlite.sql"} {
			if err := os.WriteFile(filepath.Join(m, n), []byte("-- +goose Up\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A release's schema: release.json's schema_rev where a staged release has it, else its
// newest Postgres migration (the SQLite siblings and other files do not count).
func TestReleaseSchema(t *testing.T) {
	dir := t.TempDir()
	if got := releaseSchema(dir); got != 0 {
		t.Fatalf("an empty directory: %d", got)
	}
	writeMigrations(t, dir, "0001", "0140", "0099")
	_ = os.WriteFile(filepath.Join(dir, "migrations", "0150_x.sqlite.sql"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "migrations", "MANIFEST.md"), nil, 0o644)
	if got := releaseSchema(dir); got != 140 {
		t.Fatalf("by the migrations: %d, want 140", got)
	}
	_ = os.WriteFile(filepath.Join(dir, rv.ManifestFile), []byte(`{"schema_rev": 141}`), 0o644)
	if got := releaseSchema(dir); got != 141 {
		t.Fatalf("by release.json: %d, want 141", got)
	}
}

// The plain layout keeps every valid release, each with its signed schema; running one
// again points current at it, with no guard (a restore starts it on purpose).
func TestKeptLayoutReleases(t *testing.T) {
	rs := newReleases(t)
	ctx := context.Background()
	l := layout.New(t.TempDir())
	for _, r := range []struct {
		tag    string
		schema int
	}{{"v1.4.0", 140}, {"v1.5.0", 149}} {
		rel, _ := rs.publish(t, r.tag, releaseverifytest.ReleaseOptions{MinUpgradeFrom: "1.0.0", SchemaRev: r.schema}, nil)
		get := func(_ context.Context, u string, _ int64) ([]byte, error) { return rel.Files[filepath.Base(u)], nil }
		assets := map[string]string{}
		for n := range rel.Files {
			assets[n] = "mem://" + n
		}
		if _, err := install.Stage(ctx, l, get, rv.Input{Tag: r.tag, Identity: rs.source().Identity, TrustedMaterial: rs.tm},
			assets, testPlatform); err != nil {
			t.Fatal(err)
		}
	}
	_ = l.WritePointer(layout.CurrentFile, "1.5.0")
	_ = os.MkdirAll(filepath.Join(l.ReleasesDir(), "1.6.0"), 0o755) // half there: not kept
	li := &LayoutInstaller{Layout: l, Source: rs.source(), Version: "1.5.0"}
	kept := li.Kept()
	if len(kept) != 2 || kept[0] != (KeptRelease{Version: "1.4.0", Schema: 140}) ||
		kept[1] != (KeptRelease{Version: "1.5.0", Schema: 149, Running: true}) {
		t.Fatalf("kept %+v", kept)
	}
	if err := l.Switch("1.5.0", "run-x", time.Now()); err != nil { // a guard a run left
		t.Fatal(err)
	}
	if err := li.RunKept(ctx, kept[0]); err != nil {
		t.Fatal(err)
	}
	if cur, _ := l.Current(); cur != "1.4.0" {
		t.Fatalf("current %q", cur)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatalf("a guard left: %+v", p)
	}
}

// The desktop app keeps its own payload and the copies kept before its updates; running
// a kept copy is the launcher's override, the app's own payload none.
func TestKeptAppPayloads(t *testing.T) {
	bundled := filepath.Join(t.TempDir(), "Maktaba.app", "Contents", "Resources", "server")
	_ = os.MkdirAll(bundled, 0o755)
	_ = os.WriteFile(filepath.Join(bundled, "payload.json"), []byte(`{"schema":1,"version":"1.5.0"}`), 0o644)
	writeMigrations(t, bundled, "0001", "0149")
	lroot := t.TempDir()
	ai := &AppInstaller{Payload: bundled, Bundled: bundled, LauncherRoot: lroot, Version: "1.5.0"}
	copyDir := ai.keptDir("1.4.0")
	writeMigrations(t, copyDir, "0001", "0140")
	_ = os.WriteFile(filepath.Join(copyDir, keptMarker), []byte("1.4.0\n"), 0o644)
	_ = os.MkdirAll(ai.keptDir("1.3.0"), 0o755) // no marker: a copy cut short, not kept
	kept := ai.Kept()
	if len(kept) != 2 || kept[0] != (KeptRelease{Version: "1.5.0", Schema: 149, Running: true}) ||
		kept[1] != (KeptRelease{Version: "1.4.0", Ref: copyDir, Schema: 140}) {
		t.Fatalf("kept %+v", kept)
	}
	ctx := context.Background()
	if err := ai.RunKept(ctx, kept[1]); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != copyDir {
		t.Fatalf("override %q, want the kept copy", o)
	}
	if err := ai.RunKept(ctx, kept[0]); err != nil {
		t.Fatal(err)
	}
	if o := layout.ReadOverride(lroot); o != "" {
		t.Fatalf("override %q, want none (the app's own payload)", o)
	}
}

// The Linux runtime copy keeps every installed runtime directory; running one again
// points current at it.
func TestKeptRuntimes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runtime copy is the Linux app's (symbolic links)")
	}
	ctx := context.Background()
	rt := appruntime.New(filepath.Join(t.TempDir(), "runtime"))
	rt.Sync = func() {}
	first, err := rt.Install(ctx, writePayload(t, "1.4.0"), nil, appruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := rt.Install(ctx, writePayload(t, "1.5.0"), nil, appruntime.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Point(ctx, filepath.Base(second.Dir), appruntime.Options{}); err != nil {
		t.Fatal(err)
	}
	ri := &RuntimeInstaller{Runtime: rt, Version: "1.5.0", LauncherRoot: t.TempDir()}
	byVersion := map[string]KeptRelease{}
	for _, k := range ri.Kept() {
		byVersion[k.Version] = k
	}
	old, cur := byVersion["1.4.0"], byVersion["1.5.0"]
	if old.Ref != filepath.Base(first.Dir) || old.Schema != 1 || old.Running || !cur.Running {
		t.Fatalf("kept %+v", byVersion)
	}
	if err := ri.RunKept(ctx, old); err != nil {
		t.Fatal(err)
	}
	if now, _ := rt.Current(); now != filepath.Base(first.Dir) {
		t.Fatalf("current %q", now)
	}
}
