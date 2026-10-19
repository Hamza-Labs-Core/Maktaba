package selfmanaged

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// archive writes a pre-layout install: the binaries side by side, as the
// release archive unpacks them into /usr/local/bin.
func archive(t *testing.T, ver string, launcher bool) string {
	t.Helper()
	dir := t.TempDir()
	names := []string{"maktaba-server", "maktaba-api", "maktaba-streaming"}
	if launcher {
		names = append(names, "maktaba-launcher")
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, layout.Exe(n)), []byte(n+" "+ver), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Story 35.2 TC6.
func TestSetupSelfManagedMigratesArchive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "maktaba")
	from := archive(t, "1.4.2", true)
	res, err := Convert(Options{Root: root, From: from, Version: "v1.4.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Staged || !res.Switched || res.Pending || !res.Launcher {
		t.Fatalf("result = %+v", res)
	}
	l := layout.New(root)
	if cur, _ := l.Current(); cur != "1.4.2" {
		t.Fatalf("current = %q", cur)
	}
	if w := l.Watermark(); w != "1.4.2" {
		t.Fatalf("watermark = %q", w)
	}
	if p, _ := l.ReadPending(); p != nil {
		t.Fatal("the running version was put under the guard")
	}
	if err := l.ValidateRelease("1.4.2"); err != nil {
		t.Fatal(err)
	}
	v, err := l.ReadVerified("1.4.2")
	if err != nil || v.Identity != IdentityMigrated || len(v.SHA256) != 4 {
		t.Fatalf(".verified = %+v, %v", v, err)
	}
	if _, err := os.Stat(l.LauncherPath()); err != nil {
		t.Fatal("launcher not installed at the root")
	}
	// the source install is untouched (copied, not moved)
	if _, err := os.Stat(filepath.Join(from, layout.Exe("maktaba-server"))); err != nil {
		t.Fatal(err)
	}
	// idempotent
	res, err = Convert(Options{Root: root, From: from, Version: "1.4.2"})
	if err != nil || res.Staged || res.Switched || res.Launcher || res.Skipped != "already current" {
		t.Fatalf("second run = %+v, %v", res, err)
	}
}

// AC9: a package upgrade switches only when newer than the watermark, and
// under the boot-loop guard; an older package only refreshes the launcher.
func TestPackageUpgradeRespectsWatermark(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	if _, err := Convert(Options{Root: root, From: archive(t, "1.4.2", true), Version: "1.4.2", Identity: IdentityPackage}); err != nil {
		t.Fatal(err)
	}
	res, err := Convert(Options{Root: root, From: archive(t, "1.5.0", true), Version: "1.5.0", Identity: IdentityPackage})
	if err != nil || !res.Switched || !res.Pending {
		t.Fatalf("upgrade = %+v, %v", res, err)
	}
	if cur, _ := l.Current(); cur != "1.5.0" {
		t.Fatalf("current = %q", cur)
	}
	if prev, _ := l.Previous(); prev != "1.4.2" {
		t.Fatalf("previous = %q", prev)
	}
	_ = l.Confirm()
	_ = l.RaiseWatermark("1.6.0") // the updater installed 1.6.0 since
	_ = l.WritePointer(layout.CurrentFile, "1.5.0")
	res, err = Convert(Options{Root: root, From: archive(t, "1.5.1", true), Version: "1.5.1", Identity: IdentityPackage})
	if err != nil || res.Switched || !res.Launcher || res.Skipped == "" {
		t.Fatalf("older package = %+v, %v", res, err)
	}
	if cur, _ := l.Current(); cur != "1.5.0" {
		t.Fatalf("an older package moved current to %q", cur)
	}
}

func TestConvertRefusesDevAndMissing(t *testing.T) {
	if _, err := Convert(Options{Root: t.TempDir(), From: archive(t, "x", true), Version: "dev"}); !errors.Is(err, ErrDevBuild) {
		t.Fatalf("dev: %v", err)
	}
	if _, err := Convert(Options{Root: t.TempDir(), From: t.TempDir(), Version: "1.0.0"}); !errors.Is(err, ErrNoServer) {
		t.Fatalf("no server: %v", err)
	}
	if _, err := Convert(Options{Root: t.TempDir(), From: archive(t, "1.0.0", false), Version: "1.0.0"}); !errors.Is(err, ErrNoLauncher) {
		t.Fatalf("no launcher: %v", err)
	}
}

func TestConvertReplacesBrokenReleaseDir(t *testing.T) {
	root := t.TempDir()
	l := layout.New(root)
	if err := os.MkdirAll(l.ReleaseDir("1.0.0"), 0o755); err != nil { // a half-copied directory
		t.Fatal(err)
	}
	res, err := Convert(Options{Root: root, From: archive(t, "1.0.0", true), Version: "1.0.0", Now: time.Now()})
	if err != nil || !res.Staged {
		t.Fatalf("%+v %v", res, err)
	}
	if err := l.ValidateRelease("1.0.0"); err != nil {
		t.Fatal(err)
	}
}

func TestHomebrewManaged(t *testing.T) {
	for p, want := range map[string]bool{
		"/opt/homebrew/Cellar/maktaba/1.4.2/bin": true,
		"/usr/local/Cellar/maktaba/1.4.2/bin":    true,
		"/usr/local/bin":                         false,
		`C:\Users\u\AppData\Local\Maktaba`:       false,
	} {
		if got := HomebrewManaged(p); got != want {
			t.Errorf("HomebrewManaged(%q) = %v", p, got)
		}
	}
}
