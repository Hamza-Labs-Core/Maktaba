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

func testRuntime(t *testing.T) Runtime {
	t.Helper()
	r := New(filepath.Join(t.TempDir(), "runtime"))
	n := 0
	r.Now = func() time.Time { n++; return time.Date(2026, 10, 21, 0, 0, n, 0, time.UTC) }
	r.FreeBytes = func(string) (uint64, error) { return 1 << 40, nil }
	return r
}

func TestInstallCopiesAndVerifies(t *testing.T) {
	r := testRuntime(t)
	from := samplePayload(t, "v0.3.0")
	var last [2]int64
	dir, copied, err := r.Install(context.Background(), from, func(d, total int64) { last = [2]int64{d, total} })
	if err != nil || !copied {
		t.Fatalf("install: %v (copied %v)", err, copied)
	}
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
	// the same payload again: nothing copied
	if again, copied, err := r.Install(context.Background(), from, nil); err != nil || copied || again != dir {
		t.Errorf("second install: %s %v %v", again, copied, err)
	}
	if ents, _ := os.ReadDir(r.Root); len(ents) != 2 { // the runtime and its lock
		t.Errorf("runtime root holds %d entries", len(ents))
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
			_, _, err := r.Install(context.Background(), from, nil)
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
	dir, _, err := r.Install(context.Background(), from, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tgt, err := os.Readlink(filepath.Join(dir, "python", "bin", "python3")); err != nil || tgt != "python3.12" {
		t.Errorf("python3 is not a link again: %q %v", tgt, err)
	}
	// a copy that is not the target's content is refused
	from2 := samplePayload(t, "v0.3.1")
	p := filepath.Join(from2, "python", "bin", "python3")
	_ = os.Remove(p)
	_ = os.WriteFile(p, []byte("something else"), 0o755)
	if _, _, err := r.Install(context.Background(), from2, nil); !errors.Is(err, ErrInvalid) {
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
	if _, _, err := r.Install(context.Background(), from, nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "riscv64") {
		t.Errorf("another platform: %v", err)
	}
	r.FreeBytes = func(string) (uint64, error) { return 3, nil }
	if _, _, err := r.Install(context.Background(), samplePayload(t, "v0.3.0"), nil); !errors.Is(err, ErrDiskFull) {
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

func TestActivateStopsMovesAndStarts(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	a, _, err := r.Install(ctx, samplePayload(t, "v0.2.0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if prev, err := r.Activate(ctx, filepath.Base(a), nil); err != nil || prev != "" {
		t.Fatalf("first activation: %q %v", prev, err)
	}
	b, _, err := r.Install(ctx, samplePayload(t, "v0.3.0"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := &fakeService{active: true}
	prev, err := r.Activate(ctx, filepath.Base(b), svc)
	if err != nil || prev != filepath.Base(a) {
		t.Fatalf("activate: %q %v", prev, err)
	}
	if cur, _ := r.Current(); cur != filepath.Base(b) {
		t.Errorf("current %s", cur)
	}
	if strings.Join(svc.log, ",") != "stop,start" {
		t.Errorf("service %v", svc.log)
	}
	if got, err := os.Readlink(filepath.Join(r.Root, CurrentLink)); err != nil || filepath.IsAbs(got) {
		t.Errorf("current must be a relative link: %q %v", got, err)
	}

	// a start that fails puts the old runtime back and starts it
	svc = &fakeService{active: true, failNext: 1}
	if _, err := r.Activate(ctx, filepath.Base(a), svc); err == nil {
		t.Fatal("a failed start was not reported")
	}
	if cur, _ := r.Current(); cur != filepath.Base(b) {
		t.Errorf("after a failed start current is %s, want %s back", cur, filepath.Base(b))
	}
	if strings.Join(svc.log, ",") != "stop,start,start" || !svc.active {
		t.Errorf("service %v active %v", svc.log, svc.active)
	}

	// a paused service stays paused and runs the new runtime when resumed
	svc = &fakeService{}
	if _, err := r.Activate(ctx, filepath.Base(a), svc); err != nil {
		t.Fatal(err)
	}
	if len(svc.log) != 0 {
		t.Errorf("a stopped service was touched: %v", svc.log)
	}
	if cur, _ := r.Current(); cur != filepath.Base(a) {
		t.Errorf("current %s", cur)
	}
}

func TestPruneKeepsCurrentAndTheOneBefore(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	var names []string
	for _, v := range []string{"v0.1.0", "v0.2.0", "v0.3.0", "v0.4.0"} {
		d, _, err := r.Install(ctx, samplePayload(t, v), nil)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, filepath.Base(d))
	}
	if _, err := r.Activate(ctx, names[2], nil); err != nil { // v0.3.0 runs; v0.4.0 is the newest
		t.Fatal(err)
	}
	_ = os.Mkdir(filepath.Join(r.Root, ".staging-left-behind"), 0o755)
	removed, err := r.Prune(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(removed)
	if strings.Join(removed, ",") != names[0]+","+names[1] {
		t.Errorf("removed %v", removed)
	}
	st, err := r.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if st.Current != names[2] || st.Version != "v0.3.0" || st.Previous != names[3] || len(st.Installed) != 2 {
		t.Errorf("status %+v", st)
	}
	if _, err := os.Stat(filepath.Join(r.Root, ".staging-left-behind")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a staging directory was left")
	}
}

func TestStatusSaysWhenTheBundledPayloadIsNew(t *testing.T) {
	r := testRuntime(t)
	ctx := context.Background()
	old := samplePayload(t, "v0.2.0")
	d, _, _ := r.Install(ctx, old, nil)
	_, _ = r.Activate(ctx, filepath.Base(d), nil)
	if st, _ := r.Status(old); st.UpdatePending {
		t.Errorf("same payload pending: %+v", st)
	}
	st, _ := r.Status(samplePayload(t, "v0.3.0"))
	if !st.UpdatePending || st.FromVersion != "v0.3.0" {
		t.Errorf("new payload not pending: %+v", st)
	}
	if st.Owner != "app" {
		t.Errorf("owner %s", st.Owner)
	}
	if err := r.SetOwner("headless"); err != nil || r.ReadOwner() != "headless" {
		t.Errorf("owner: %v %s", err, r.ReadOwner())
	}
	if err := r.SetOwner("nobody"); err == nil {
		t.Error("a bad owner was recorded")
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
	lockWait = 300 * time.Millisecond
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
