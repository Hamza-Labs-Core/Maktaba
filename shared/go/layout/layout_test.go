package layout

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 19, 3, 0, 0, 0, time.UTC)

// mkRelease creates bin/releases/<v>/ with a fake maktaba-server and .verified.
func mkRelease(t *testing.T, l Layout, v string) {
	t.Helper()
	dir := l.ReleaseDir(v)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming"} {
		if err := os.WriteFile(filepath.Join(dir, Exe(n)), []byte(n+" "+v), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteVerified(dir, "test", v, t0); err != nil {
		t.Fatal(err)
	}
}

func readState(t *testing.T, l Layout) (cur, prev string, pend *Pending) {
	t.Helper()
	cur, _ = l.Current()
	prev, _ = l.Previous()
	pend, err := l.ReadPending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	return cur, prev, pend
}

func TestPointerWriteAtomic(t *testing.T) {
	l := New(t.TempDir())
	if err := l.WritePointer(CurrentFile, "1.4.2"); err != nil {
		t.Fatal(err)
	}
	// a crash between the temp write and the rename
	crash := errors.New("power cut")
	beforeRename = func(string) error { return crash }
	t.Cleanup(func() { beforeRename = func(string) error { return nil } })
	if err := l.WritePointer(CurrentFile, "1.5.0"); !errors.Is(err, crash) {
		t.Fatalf("err = %v", err)
	}
	// a real crash leaves the temp file behind: it must not be read
	if err := os.WriteFile(filepath.Join(l.Root, ".current.tmp-123"), []byte("1.5"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := l.Current(); err != nil || v != "1.4.2" {
		t.Fatalf("current = %q, %v; want the old pointer", v, err)
	}
	beforeRename = func(string) error { return nil }
	if err := l.WritePointer(CurrentFile, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if v, _ := l.Current(); v != "1.5.0" {
		t.Fatalf("current = %q", v)
	}
	b, _ := os.ReadFile(filepath.Join(l.Root, CurrentFile))
	if string(b) != "1.5.0\n" {
		t.Fatalf("pointer file = %q", b)
	}
}

func TestPointerRejectsBadVersions(t *testing.T) {
	l := New(t.TempDir())
	for _, v := range []string{"", "..", "../1.0", `1.0\x`, "1.0/2", "abc", strings.Repeat("1", 65)} {
		if err := l.WritePointer(CurrentFile, v); err == nil {
			t.Errorf("WritePointer(%q) accepted", v)
		}
	}
	if err := os.WriteFile(filepath.Join(l.Root, CurrentFile), []byte("../../etc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Current(); !errors.Is(err, ErrBadVersion) {
		t.Fatalf("err = %v", err)
	}
}

func TestSwitchConfirmRollback(t *testing.T) {
	l := New(t.TempDir())
	mkRelease(t, l, "1.4.2")
	mkRelease(t, l, "1.5.0")
	if err := l.Switch("1.4.2", "", t0); err != nil {
		t.Fatal(err)
	}
	if err := l.Confirm(); err != nil {
		t.Fatal(err)
	}
	if err := l.Switch("1.5.0", "run-1", t0); err != nil {
		t.Fatal(err)
	}
	cur, prev, p := readState(t, l)
	if cur != "1.5.0" || prev != "1.4.2" || p == nil || p.Version != "1.5.0" || p.Previous != "1.4.2" || p.RunID != "run-1" {
		t.Fatalf("after switch: %q %q %+v", cur, prev, p)
	}
	got, err := l.Rollback()
	if err != nil || got != "1.4.2" {
		t.Fatalf("rollback = %q, %v", got, err)
	}
	cur, _, p = readState(t, l)
	if cur != "1.4.2" || p != nil {
		t.Fatalf("after rollback: %q %+v", cur, p)
	}
}

func TestSwitchRefusesUnverified(t *testing.T) {
	l := New(t.TempDir())
	mkRelease(t, l, "1.5.0")
	if err := os.WriteFile(l.ServerPath("1.5.0"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := l.Switch("1.5.0", "", t0); !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v", err)
	}
	if err := l.Switch("9.9.9", "", t0); !errors.Is(err, ErrMissingRelease) {
		t.Fatalf("err = %v", err)
	}
	if _, err := l.Current(); !errors.Is(err, ErrNoPointer) {
		t.Fatal("a refused switch wrote current")
	}
}

// TestSwitchPowerLoss cuts the power before each rename of Switch and of
// Rollback; after Reconcile the install is always on one release that
// validates, with a pending marker only when current is the unconfirmed one.
func TestSwitchPowerLoss(t *testing.T) {
	for _, op := range []string{"switch", "rollback"} {
		for cut := 1; cut <= 4; cut++ {
			t.Run(op+"/"+string(rune('0'+cut)), func(t *testing.T) {
				l := New(t.TempDir())
				mkRelease(t, l, "1.4.2")
				mkRelease(t, l, "1.5.0")
				if err := l.Switch("1.4.2", "", t0); err != nil {
					t.Fatal(err)
				}
				_ = l.Confirm()
				if op == "rollback" {
					if err := l.Switch("1.5.0", "r", t0); err != nil {
						t.Fatal(err)
					}
				}
				n := 0
				beforeRename = func(string) error {
					n++
					if n == cut {
						return errors.New("power cut")
					}
					return nil
				}
				if op == "switch" {
					_ = l.Switch("1.5.0", "r", t0)
				} else {
					_, _ = l.Rollback()
				}
				beforeRename = func(string) error { return nil }

				if _, err := l.Reconcile(); err != nil {
					t.Fatal(err)
				}
				cur, _, p := readState(t, l)
				if cur != "1.4.2" && cur != "1.5.0" {
					t.Fatalf("current = %q", cur)
				}
				if err := l.ValidateRelease(cur); err != nil {
					t.Fatal(err)
				}
				if p != nil && p.Version != cur {
					t.Fatalf("pending %q survives with current %q", p.Version, cur)
				}
				if cur == "1.5.0" && p == nil {
					t.Fatal("unconfirmed 1.5.0 is current without a pending marker (no boot-loop guard)")
				}
			})
		}
	}
}

func TestReconcileDropsCorruptPending(t *testing.T) {
	l := New(t.TempDir())
	mkRelease(t, l, "1.4.2")
	_ = l.Switch("1.4.2", "", t0)
	_ = os.WriteFile(filepath.Join(l.Root, PendingFile), []byte("{torn"), 0o644)
	if changed, err := l.Reconcile(); err != nil || !changed {
		t.Fatalf("reconcile = %v, %v", changed, err)
	}
	if p, err := l.ReadPending(); p != nil || err != nil {
		t.Fatalf("pending = %+v, %v", p, err)
	}
}

func TestWatermarkOnlyRises(t *testing.T) {
	l := New(t.TempDir())
	for _, v := range []string{"1.4.2", "1.10.0", "1.9.9", "1.10.0-rc.1"} {
		if err := l.RaiseWatermark(v); err != nil {
			t.Fatal(err)
		}
	}
	if w := l.Watermark(); w != "1.10.0" {
		t.Fatalf("watermark = %q", w)
	}
}

func TestVerifyFiles(t *testing.T) {
	l := New(t.TempDir())
	mkRelease(t, l, "1.5.0")
	if _, err := VerifyFiles(l.ReleaseDir("1.5.0")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(l.ReleaseDir("1.5.0"), Exe("maktaba-api")), []byte("x"), 0o755)
	if _, err := VerifyFiles(l.ReleaseDir("1.5.0")); !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v", err)
	}
	// the server itself is intact, so the launcher would still start it
	if err := l.ValidateRelease("1.5.0"); err != nil {
		t.Fatal(err)
	}
}

func TestPruneKeepsInUse(t *testing.T) {
	l := New(t.TempDir())
	for _, v := range []string{"1.1.0", "1.2.0", "1.3.0", "1.4.0", "1.5.0", "1.6.0"} {
		mkRelease(t, l, v)
	}
	_ = l.Switch("1.5.0", "", t0)
	_ = l.Confirm()
	_ = l.Switch("1.6.0", "", t0)   // previous 1.5.0, pending 1.6.0
	res, err := l.Prune(3, "1.1.0") // a TV slot still runs 1.1.0
	if err != nil {
		t.Fatal(err)
	}
	vs, _ := l.Versions()
	want := "1.1.0 1.4.0 1.5.0 1.6.0"
	if strings.Join(vs, " ") != want {
		t.Fatalf("left %v (removed %v); want %s", vs, res.Removed, want)
	}
	// keep below the floor still keeps current + previous
	res, _ = l.Prune(0)
	vs, _ = l.Versions()
	if strings.Join(vs, " ") != "1.5.0 1.6.0" {
		t.Fatalf("left %v (%+v)", vs, res)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.0", 1},
		{"v1.5.0", "1.5.0", 0},
		{"1.5.0", "1.5.0-rc.2", 1},
		{"1.5.0-rc.10", "1.5.0-rc.2", 1},
		{"1.5.0-beta.1", "1.5.0-rc.1", -1},
		{"1.5", "1.5.0", 0},
		{"1.5.0+build.7", "1.5.0", 0},
		{"2.0.0", "10.0.0", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLockIsExclusive(t *testing.T) {
	l := New(t.TempDir())
	f, err := l.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Lock(); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	Unlock(f)
	g, err := l.Lock()
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	Unlock(g)
}

// LockFile is the same lock on any path, its folder made when missing.
func TestLockFileIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b.lock")
	f, err := LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockFile(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	Unlock(f)
	g, err := LockFile(path)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	Unlock(g)
}

func TestValidateRefusesSharedWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	l := New(t.TempDir())
	mkRelease(t, l, "1.5.0")
	if err := os.Chmod(l.ServerPath("1.5.0"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := l.ValidateRelease("1.5.0"); !errors.Is(err, ErrUnsafePerm) {
		t.Fatalf("err = %v", err)
	}
}
