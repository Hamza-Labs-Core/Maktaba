package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
)

type fakeImporter struct {
	got  []string
	fail map[string]bool
}

func (f *fakeImporter) Import(_ context.Context, p string) ([]string, error) {
	f.got = append(f.got, filepath.Base(p))
	if f.fail[filepath.Base(p)] {
		return nil, errors.New("verify_failed")
	}
	return []string{"runtime.ml", "tool.yt-dlp"}, nil
}

func systemPacksFixture(t *testing.T, names ...string) (systemPacks, *fakeImporter, *bytes.Buffer) {
	t.Helper()
	dir, state := t.TempDir(), t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("tar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeImporter{fail: map[string]bool{}}
	var log bytes.Buffer
	return systemPacks{dir: dir, stateDir: state, version: "0.4.2", platform: "linux-amd64", m: f, log: &log,
		now: func() time.Time { return time.Unix(0, 0) }}, f, &log
}

const fullTar = "maktaba-offline-0.4.2-linux-amd64-full.tar"

// T10: maktaba-full's offline file is imported at the first start of its version, once.
func TestSystemPacksImportOnce(t *testing.T) {
	s, f, _ := systemPacksFixture(t, fullTar)
	if got := s.run(context.Background()); !reflect.DeepEqual(got, []string{fullTar}) {
		t.Fatalf("first start imported %v", got)
	}
	if got := s.run(context.Background()); got != nil {
		t.Fatalf("second start imported %v again", got)
	}
	if !reflect.DeepEqual(f.got, []string{fullTar}) {
		t.Errorf("Import calls %v", f.got)
	}
	b, err := os.ReadFile(filepath.Join(s.markerDir(), fullTar))
	if err != nil || !strings.Contains(string(b), "runtime.ml,tool.yt-dlp") {
		t.Errorf("marker %q %v", b, err)
	}
}

// The package's form: the offline file unpacked as a folder (full-package.sh).
func TestSystemPacksImportTheFolder(t *testing.T) {
	s, f, _ := systemPacksFixture(t)
	folder := "maktaba-offline-0.4.2-linux-amd64-full"
	if err := os.MkdirAll(filepath.Join(s.dir, folder, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "maktaba-offline-0.4.1-linux-amd64-full"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := s.run(context.Background()); !reflect.DeepEqual(got, []string{folder}) {
		t.Fatalf("imported %v", got)
	}
	if !reflect.DeepEqual(f.got, []string{folder}) {
		t.Errorf("Import calls %v", f.got)
	}
}

// Another version's or platform's file (the app updated before maktaba-full) is left
// alone: no import, no error.
func TestSystemPacksOtherVersionLeftAlone(t *testing.T) {
	s, f, log := systemPacksFixture(t, "maktaba-offline-0.4.1-linux-amd64-full.tar",
		"maktaba-offline-0.4.2-linux-arm64-full.tar", "README")
	if got := s.run(context.Background()); got != nil {
		t.Fatalf("imported %v", got)
	}
	if len(f.got) != 0 {
		t.Errorf("Import calls %v", f.got)
	}
	if !strings.Contains(log.String(), "0.4.1-linux-amd64-full.tar is not for 0.4.2 linux-amd64") {
		t.Errorf("log %q", log.String())
	}
}

// A failed import is tried again at the next start (no marker).
func TestSystemPacksFailedImportRetried(t *testing.T) {
	s, f, log := systemPacksFixture(t, fullTar)
	f.fail[fullTar] = true
	if got := s.run(context.Background()); got != nil {
		t.Fatalf("imported %v", got)
	}
	if !strings.Contains(log.String(), "was not imported: verify_failed") {
		t.Errorf("log %q", log.String())
	}
	f.fail[fullTar] = false
	if got := s.run(context.Background()); !reflect.DeepEqual(got, []string{fullTar}) {
		t.Fatalf("the next start imported %v", got)
	}
}

// A home that chose Core at setup imports nothing; one that chose Full does.
func TestSystemPacksFollowTheChoice(t *testing.T) {
	s, f, log := systemPacksFixture(t, fullTar)
	if err := componentseed.WriteChoice(s.stateDir, componentseed.Choice{Bundle: "core", Enabled: []string{"core"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.run(context.Background()); got != nil || len(f.got) != 0 {
		t.Fatalf("a Core home imported %v (%v)", got, f.got)
	}
	if !strings.Contains(log.String(), "has no runtime.ml") {
		t.Errorf("log %q", log.String())
	}
	if err := componentseed.WriteChoice(s.stateDir, componentseed.Choice{Bundle: "full", Enabled: []string{"core", "runtime.ml"}}); err != nil {
		t.Fatal(err)
	}
	if got := s.run(context.Background()); !reflect.DeepEqual(got, []string{fullTar}) {
		t.Fatalf("a Full home imported %v", got)
	}
}

// No folder (no maktaba-full, or not Linux), or a build of no release: nothing.
func TestSystemPacksNothingToDo(t *testing.T) {
	s, f, _ := systemPacksFixture(t, fullTar)
	s.version = ""
	if got := s.run(context.Background()); got != nil {
		t.Fatalf("a build of no release imported %v", got)
	}
	s.version, s.dir = "0.4.2", filepath.Join(t.TempDir(), "missing")
	if got := s.run(context.Background()); got != nil || len(f.got) != 0 {
		t.Fatalf("no folder imported %v", got)
	}
}

func TestSystemPacksDir(t *testing.T) {
	t.Setenv(systemPacksEnv, "/x/packs")
	if got := systemPacksDir(); got != "/x/packs" {
		t.Errorf("override %q", got)
	}
}

// The kill switch: with [components] reconcile = off the first start imports nothing (and
// leaves no marker, so the import runs once the switch is back on).
func TestSystemPacksReconcileOff(t *testing.T) {
	s, f, log := systemPacksFixture(t, fullTar)
	s.off = true
	if got := s.run(context.Background()); got != nil || len(f.got) != 0 {
		t.Fatalf("imported %v (calls %v) with reconcile off", got, f.got)
	}
	if !strings.Contains(log.String(), "reconcile is off") {
		t.Errorf("log %q", log.String())
	}
	s.off = false
	if got := s.run(context.Background()); !reflect.DeepEqual(got, []string{fullTar}) {
		t.Fatalf("switch back on imported %v", got)
	}
}
