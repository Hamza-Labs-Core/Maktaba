//go:build !windows

package appruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// makePayload writes a payload as tools/release/linux-app/payload.sh does: the
// files, files.sha256 (tab-separated, sorted by path) and payload.json.
func makePayload(t *testing.T, version string, files map[string]string, links map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	type line struct{ path, text string }
	var lines []line
	var total int64
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(p, "bin/") {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		lines = append(lines, line{p, fmt.Sprintf("f\t%04o\t%d\t%s\t%s", mode, len(body), hex.EncodeToString(sum[:]), p)})
		total += int64(len(body))
	}
	for p, target := range links {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, full); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line{p, fmt.Sprintf("l\t%s\t%s", target, p)})
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].path < lines[j].path })
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, Manifest), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	msum := sha256.Sum256([]byte(b.String()))
	doc := map[string]any{"schema": 1, "version": version, "platform": Platform(), "min_glibc": "2.35",
		"files": map[string]any{"manifest": Manifest, "count": len(lines), "bytes": total, "sha256": hex.EncodeToString(msum[:])}}
	pj, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, PayloadJSON), pj, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func samplePayload(t *testing.T, version string) string {
	return makePayload(t, version,
		map[string]string{"bin/maktaba-launcher": "#!/bin/sh\necho " + version + "\n", "python/bin/python3.12": "py",
			"postgres/lib/libpq.so.5.16": "pq"},
		map[string]string{"python/bin/python3": "python3.12", "postgres/lib/libpq.so.5": "libpq.so.5.16"})
}

// testRuntime is a runtime in <tmp>/data/runtime: the data directory does not
// exist yet.
func testRuntime(t *testing.T) Runtime {
	t.Helper()
	r := New(filepath.Join(t.TempDir(), "data", "runtime"))
	n := 0
	r.Now = func() time.Time { n++; return time.Date(2026, 10, 21, 0, 0, n, 0, time.UTC) }
	r.FreeBytes = func(string) (uint64, error) { return 1 << 40, nil }
	r.Sync = func() {} // what a whole-system sync flushes is not under test
	return r
}

func mustInstall(t *testing.T, r Runtime, from string) string {
	t.Helper()
	res, err := r.Install(context.Background(), from, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(res.Dir)
}

func TestInstallCopiesAndVerifies(t *testing.T) {
	r := testRuntime(t)
	from := samplePayload(t, "v0.3.0")
	var last [2]int64
	res, err := r.Install(context.Background(), from, func(d, total int64) { last = [2]int64{d, total} }, Options{})
	if err != nil || !res.Copied {
		t.Fatalf("install: %v (%+v)", err, res)
	}
	dir := res.Dir
	if !strings.HasPrefix(filepath.Base(dir), "v0.3.0_") || len(filepath.Base(dir)) != len("v0.3.0_")+8 {
		t.Errorf("dir name %s", filepath.Base(dir))
	}
	if last[0] != last[1] || last[1] == 0 {
		t.Errorf("progress ended at %v", last)
	}
	info, err := os.Stat(filepath.Join(dir, "bin", "maktaba-launcher"))
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("launcher: %v %v", info, err)
	}
	if tgt, err := os.Readlink(filepath.Join(dir, "python", "bin", "python3")); err != nil || tgt != "python3.12" {
		t.Errorf("symlink: %q %v", tgt, err)
	}
	for _, f := range []string{PayloadJSON, Manifest, installedFn} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	// the first runtime here: current names it (nothing could run before)
	if cur, _ := r.Current(); !res.First || cur != filepath.Base(dir) || r.Watermark() != "v0.3.0" {
		t.Errorf("first install: first %v, current %q, watermark %q", res.First, cur, r.Watermark())
	}
	// the same payload again: nothing copied
	if again, err := r.Install(context.Background(), from, nil, Options{}); err != nil || again.Copied || again.First || again.Dir != dir {
		t.Errorf("second install: %+v %v", again, err)
	}
	// the runtime, current, the watermark and the lock
	if ents, _ := os.ReadDir(r.Root); len(ents) != 4 {
		t.Errorf("runtime root holds %d entries", len(ents))
	}
	// the user's only, the data directory too
	for _, d := range []string{r.Root, filepath.Dir(r.Root)} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
}

// With a current, an install leaves it alone: moving it is Activate's.
func TestInstallKeepsCurrent(t *testing.T) {
	r := testRuntime(t)
	a := mustInstall(t, r, samplePayload(t, "v0.2.0"))
	res, err := r.Install(context.Background(), samplePayload(t, "v0.3.0"), nil, Options{})
	if err != nil || res.First {
		t.Fatalf("second runtime: %+v %v", res, err)
	}
	if cur, _ := r.Current(); cur != a {
		t.Errorf("current %s, want %s", cur, a)
	}
}

// What an install cut short left is removed before the room is checked: it
// takes no room from this one.
func TestInstallRemovesLeftoversBeforeCheckingRoom(t *testing.T) {
	r := testRuntime(t)
	left := filepath.Join(r.Root, ".staging-v0.2.0_00000000-dead")
	if err := os.MkdirAll(left, 0o755); err != nil {
		t.Fatal(err)
	}
	r.FreeBytes = func(string) (uint64, error) {
		if _, err := os.Stat(left); err == nil {
			return 1, nil // the leftover fills the disk
		}
		return 1 << 40, nil
	}
	if _, err := r.Install(context.Background(), samplePayload(t, "v0.3.0"), nil, Options{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
		t.Error("the leftover is still there")
	}
}

// payload.json and files.sha256 in the copy are the bytes that were checked,
// even when the source changes while the copy runs.
func TestInstallWritesTheCheckedBytes(t *testing.T) {
	r := testRuntime(t)
	from := samplePayload(t, "v0.3.0")
	pj, _ := os.ReadFile(filepath.Join(from, PayloadJSON))
	mf, _ := os.ReadFile(filepath.Join(from, Manifest))
	changed := false
	res, err := r.Install(context.Background(), from, func(int64, int64) {
		if !changed {
			changed = true
			_ = os.WriteFile(filepath.Join(from, PayloadJSON), []byte(`{"schema":1,"version":"other"}`), 0o644)
			_ = os.WriteFile(filepath.Join(from, Manifest), []byte("changed"), 0o644)
		}
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, PayloadJSON)); string(b) != string(pj) {
		t.Errorf("payload.json %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Dir, Manifest)); string(b) != string(mf) {
		t.Errorf("files.sha256 %q", b)
	}
}

func TestInstallRefusesAChangedPayload(t *testing.T) {
	for name, spoil := range map[string]func(dir string){
		"a changed file": func(d string) {
			_ = os.WriteFile(filepath.Join(d, "python", "bin", "python3.12"), []byte("pY"), 0o644)
		},
		"a missing file": func(d string) { _ = os.Remove(filepath.Join(d, "postgres", "lib", "libpq.so.5.16")) },
		"a manifest that is not payload.json's": func(d string) {
			_ = os.WriteFile(filepath.Join(d, Manifest), []byte("f\t0644\t2\t"+strings.Repeat("0", 64)+"\tx\n"), 0o644)
		},
		"another symlink": func(d string) {
			_ = os.Remove(filepath.Join(d, "python", "bin", "python3"))
			_ = os.Symlink("/usr/bin/python3", filepath.Join(d, "python", "bin", "python3"))
		},
		"no payload.json": func(d string) { _ = os.Remove(filepath.Join(d, PayloadJSON)) },
	} {
		t.Run(name, func(t *testing.T) {
			r := testRuntime(t)
			from := samplePayload(t, "v0.3.0")
			spoil(from)
			_, err := r.Install(context.Background(), from, nil, Options{})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want payload_invalid", err)
			}
			if dirs, _ := r.List(); len(dirs) != 0 {
				t.Errorf("installed %v", dirs)
			}
			ents, _ := os.ReadDir(r.Root)
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), ".staging-") {
					t.Errorf("staging left: %s", e.Name())
				}
			}
		})
	}
}

// tauri's bundlers copy resources with fs::copy, which follows symlinks: the
// app's payload holds copies where the manifest lists links. The runtime makes
// the links again.
func TestInstallMakesLinksTheBundlerCopied(t *testing.T) {
	r := testRuntime(t)
	from := samplePayload(t, "v0.3.0")
	for link, target := range map[string]string{"python/bin/python3": "python/bin/python3.12",
		"postgres/lib/libpq.so.5": "postgres/lib/libpq.so.5.16"} {
		p := filepath.Join(from, filepath.FromSlash(link))
		b, err := os.ReadFile(filepath.Join(from, filepath.FromSlash(target)))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(p)
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.Install(context.Background(), from, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if tgt, err := os.Readlink(filepath.Join(res.Dir, "python", "bin", "python3")); err != nil || tgt != "python3.12" {
		t.Errorf("python3 is not a link again: %q %v", tgt, err)
	}
	// a copy that is not the target's content is refused
	from2 := samplePayload(t, "v0.3.1")
	p := filepath.Join(from2, "python", "bin", "python3")
	_ = os.Remove(p)
	_ = os.WriteFile(p, []byte("something else"), 0o755)
	if _, err := r.Install(context.Background(), from2, nil, Options{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a wrong copy: %v", err)
	}
}

func TestManifestPathsStayInside(t *testing.T) {
	for _, bad := range []string{
		"f\t0644\t1\t" + strings.Repeat("a", 64) + "\t../escape",
		"f\t0644\t1\t" + strings.Repeat("a", 64) + "\t/abs",
		"f\t0644\t1\t" + strings.Repeat("a", 64) + "\tpayload.json",
		"l\t/usr/lib/libssl.so.3\tpostgres/lib/libssl.so.3",
		"l\t../../../etc\tpython/x",
		"f\t17777\t1\t" + strings.Repeat("a", 64) + "\tbin/x",
		"x\twhat",
	} {
		if _, err := parseEntry(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if e, err := parseEntry("l\t../lib/libpq.so.5\tpostgres/bin/libpq"); err != nil || e.Target != "../lib/libpq.so.5" {
		t.Errorf("a relative link inside the payload: %v", err)
	}
}

func TestInstallChecksPlatformAndDisk(t *testing.T) {
	r := testRuntime(t)
	from := samplePayload(t, "v0.3.0")
	pj := filepath.Join(from, PayloadJSON)
	b, _ := os.ReadFile(pj)
	_ = os.WriteFile(pj, []byte(strings.Replace(string(b), Platform(), "linux-riscv64", 1)), 0o644)
	if _, err := r.Install(context.Background(), from, nil, Options{}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "riscv64") {
		t.Errorf("another platform: %v", err)
	}
	r.FreeBytes = func(string) (uint64, error) { return 3, nil }
	if _, err := r.Install(context.Background(), samplePayload(t, "v0.3.0"), nil, Options{}); !errors.Is(err, ErrDiskFull) {
		t.Errorf("no room: %v", err)
	}
}

type fakeService struct {
	active   bool
	failNext int // starts to fail
	log      []string
}

func (s *fakeService) Active(context.Context) bool { return s.active }
func (s *fakeService) Stop(context.Context) error {
	s.log = append(s.log, "stop")
	s.active = false
	return nil
}
func (s *fakeService) Start(context.Context) error {
	s.log = append(s.log, "start")
	if s.failNext > 0 {
		s.failNext--
		return errors.New("start failed")
	}
	s.active = true
	return nil
}

type failingStop struct{ *fakeService }

func (failingStop) Stop(context.Context) error { return errors.New("unit busy") }

func TestActivateStopsMovesAndStarts(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	a := mustInstall(t, r, samplePayload(t, "v0.2.0")) // the first: current
	b := mustInstall(t, r, samplePayload(t, "v0.3.0"))
	svc := &fakeService{active: true}
	prev, err := r.Activate(ctx, b, svc, Options{})
	if err != nil || prev != a {
		t.Fatalf("activate: %q %v", prev, err)
	}
	if cur, _ := r.Current(); cur != b || r.Previous() != a || r.Watermark() != "v0.3.0" {
		t.Errorf("current %s, previous %s, watermark %s", cur, r.Previous(), r.Watermark())
	}
	if strings.Join(svc.log, ",") != "stop,start" {
		t.Errorf("service %v", svc.log)
	}
	for _, l := range []string{CurrentLink, PreviousLink} {
		if got, err := os.Readlink(filepath.Join(r.Root, l)); err != nil || filepath.IsAbs(got) {
			t.Errorf("%s must be a relative link: %q %v", l, got, err)
		}
	}

	// a start that fails puts the old runtime back and starts it
	c := mustInstall(t, r, samplePayload(t, "v0.4.0"))
	svc = &fakeService{active: true, failNext: 1}
	if _, err := r.Activate(ctx, c, svc, Options{}); !errors.Is(err, ErrService) {
		t.Fatalf("a failed start: %v", err)
	}
	if cur, _ := r.Current(); cur != b || r.Previous() != a {
		t.Errorf("after a failed start current is %s (previous %s), want %s back", cur, r.Previous(), b)
	}
	if strings.Join(svc.log, ",") != "stop,start,start" || !svc.active {
		t.Errorf("service %v active %v", svc.log, svc.active)
	}

	// a paused service stays paused and runs the new runtime when resumed
	svc = &fakeService{}
	if _, err := r.Activate(ctx, c, svc, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(svc.log) != 0 {
		t.Errorf("a stopped service was touched: %v", svc.log)
	}
	if cur, _ := r.Current(); cur != c || r.Previous() != b {
		t.Errorf("current %s previous %s", cur, r.Previous())
	}

	// a stop that fails leaves current as it was
	if _, err := r.Activate(ctx, b, failingStop{&fakeService{active: true}}, Options{AllowDowngrade: true}); !errors.Is(err, ErrService) {
		t.Errorf("a failed stop: %v", err)
	}
	if cur, _ := r.Current(); cur != c {
		t.Errorf("current %s after a failed stop", cur)
	}
	if _, err := r.Activate(ctx, "v9.9.9_00000000", nil, Options{}); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
}

// A version older than the newest this machine ran is refused, by install and
// by activate, unless allowed; the watermark never falls.
func TestDowngradeRefusedUnlessAllowed(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	newer := mustInstall(t, r, samplePayload(t, "v0.3.0"))
	older := samplePayload(t, "v0.2.0")
	if _, err := r.Install(ctx, older, nil, Options{}); !errors.Is(err, ErrDowngrade) || !strings.Contains(err.Error(), "v0.3.0") {
		t.Fatalf("install of an older version: %v", err)
	}
	if st, _ := r.Status(older); st.UpdatePending || !st.Downgrade {
		t.Errorf("status of an older payload: %+v", st)
	}
	res, err := r.Install(ctx, older, nil, Options{AllowDowngrade: true})
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}
	old := filepath.Base(res.Dir)
	if _, err := r.Activate(ctx, old, nil, Options{}); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("activate of an older version: %v", err)
	}
	if _, err := r.Activate(ctx, old, nil, Options{AllowDowngrade: true}); err != nil {
		t.Fatalf("allowed: %v", err)
	}
	if r.Watermark() != "v0.3.0" || r.Previous() != newer {
		t.Errorf("watermark %s previous %s", r.Watermark(), r.Previous())
	}
	// the older one runs now: installing it again copies nothing and is no downgrade
	if again, err := r.Install(ctx, older, nil, Options{}); err != nil || again.Copied {
		t.Errorf("the running one again: %+v %v", again, err)
	}
	// back to the one before by its link: the rollback
	if prev, err := r.Activate(ctx, PreviousLink, nil, Options{}); err != nil || prev != old {
		t.Errorf("activate previous: %q %v", prev, err)
	}
	if cur, _ := r.Current(); cur != newer {
		t.Errorf("current %s", cur)
	}
	// the same version, another build: not a downgrade, and pending
	rebuilt := makePayload(t, "v0.3.0", map[string]string{"bin/maktaba-launcher": "rebuilt"}, nil)
	if st, _ := r.Status(rebuilt); !st.UpdatePending || st.Downgrade {
		t.Errorf("status of a rebuild: %+v", st)
	}
	if _, err := r.Install(ctx, rebuilt, nil, Options{}); err != nil {
		t.Errorf("a rebuild of the same version: %v", err)
	}
}

// Prune keeps current and the runtime that ran before it, not the newest
// install (which may never have run).
func TestPruneKeepsCurrentAndTheOneBefore(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	a := mustInstall(t, r, samplePayload(t, "v0.1.0")) // the first: current
	b := mustInstall(t, r, samplePayload(t, "v0.2.0")) // never runs
	c := mustInstall(t, r, samplePayload(t, "v0.3.0"))
	if _, err := r.Activate(ctx, c, nil, Options{}); err != nil { // a ran before c
		t.Fatal(err)
	}
	d := mustInstall(t, r, samplePayload(t, "v0.4.0")) // the newest install, never ran
	_ = os.Mkdir(filepath.Join(r.Root, ".staging-left-behind"), 0o755)
	removed, err := r.Prune(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{b, d}
	sort.Strings(removed)
	sort.Strings(want)
	if strings.Join(removed, ",") != strings.Join(want, ",") {
		t.Errorf("removed %v, want %v", removed, want)
	}
	st, err := r.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if st.Current != c || st.Version != "v0.3.0" || st.Previous != a || len(st.Installed) != 2 || st.Watermark != "v0.3.0" {
		t.Errorf("status %+v", st)
	}
	if _, err := os.Stat(filepath.Join(r.Root, ".staging-left-behind")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a staging directory was left")
	}
	// keep 3: the newest other one too
	mustInstall(t, r, samplePayload(t, "v0.5.0"))
	if removed, err := r.Prune(ctx, 3); err != nil || len(removed) != 0 {
		t.Errorf("keep 3 removed %v (%v)", removed, err)
	}
}

func TestStatusSaysWhenTheBundledPayloadIsNew(t *testing.T) {
	r := testRuntime(t)
	old := samplePayload(t, "v0.2.0")
	if st, _ := r.Status(old); !st.UpdatePending { // no runtime yet
		t.Errorf("empty runtime: %+v", st)
	}
	mustInstall(t, r, old)
	if st, _ := r.Status(old); st.UpdatePending || st.Downgrade {
		t.Errorf("same payload pending: %+v", st)
	}
	st, _ := r.Status(samplePayload(t, "v0.3.0"))
	if !st.UpdatePending || st.FromVersion != "v0.3.0" {
		t.Errorf("new payload not pending: %+v", st)
	}
	if st.Owner != OwnerApp {
		t.Errorf("owner %s", st.Owner)
	}
	if err := r.SetOwner(OwnerHeadless); err != nil || r.ReadOwner() != OwnerHeadless {
		t.Errorf("owner: %v %s", err, r.ReadOwner())
	}
	if err := r.SetOwner("nobody"); err == nil {
		t.Error("a bad owner was recorded")
	}
}

// A download an update left is removed once it is old; a fresh one is kept (an
// update holds no lock while it downloads).
func TestStaleDownloadsAreRemoved(t *testing.T) {
	r := testRuntime(t)
	r.Now = time.Now
	fresh := filepath.Join(r.Root, DownloadPrefix+"fresh")
	stale := filepath.Join(r.Root, DownloadPrefix+"stale")
	for _, d := range []string{fresh, stale} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-7 * time.Hour)
	if err := os.Chtimes(stale, long, long); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Prune(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("a stale download was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a running update's download was removed")
	}
}

func TestLockWaitsThenGivesUp(t *testing.T) {
	r := testRuntime(t)
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	unlock, err := r.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := lockWait
	lockWait = 150 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	if _, err := r.lock(context.Background()); !errors.Is(err, ErrLocked) {
		t.Errorf("second lock: %v", err)
	}
	unlock()
	u2, err := r.lock(context.Background())
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	u2()
}
