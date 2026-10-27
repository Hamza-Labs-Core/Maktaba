package systemstatus

import (
	"context"
	"database/sql/driver"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

func withGOOS(t *testing.T, goos string) {
	t.Helper()
	old := hostGOOS
	hostGOOS = goos
	t.Cleanup(func() { hostGOOS = old })
}

func rootsDB(rows ...[]driver.Value) *queuestest.DB {
	return queuestest.New(&queuestest.Rule{Match: "from library_roots lr",
		Cols: []string{"id", "path", "name"}, Rows: rows})
}

func pathErr(path string, errno syscall.Errno) error {
	return &fs.PathError{Op: "open", Path: path, Err: errno}
}

func TestHostAccessClassifiesEachFolder(t *testing.T) {
	withGOOS(t, "darwin")
	db := rootsDB(
		[]driver.Value{"r1", "/Volumes/Media/Lectures", "Lectures"},
		[]driver.Value{"r2", "/Volumes/Media/Talks", "Talks"},
		[]driver.Value{"r3", "/Volumes/Gone/Old", "Old"},
		[]driver.Value{"r4", "/Users/me/Movies/Extra", "Extra"},
		[]driver.Value{"r5", "smb://nas/Share/Series", "Series"},
		[]driver.Value{"r6", "/Volumes/Media/Odd", "Odd"},
	)
	read := func(p string) error {
		switch p {
		case "/Volumes/Media/Lectures":
			return nil
		case "/Volumes/Media/Talks":
			return pathErr(p, syscall.EPERM) // macOS without Full Disk Access
		case "/Volumes/Gone/Old":
			return pathErr(p, syscall.ENOENT)
		case "/Users/me/Movies/Extra":
			return pathErr(p, syscall.ENOENT)
		}
		return pathErr(p, syscall.EIO)
	}
	stat := func(p string) error {
		if p == "/Volumes/Gone" {
			return pathErr(p, syscall.ENOENT)
		}
		return nil
	}
	h := &Handler{DB: db.DB, SupervisorURL: "http://127.0.0.1:1", ReadDir: read, Stat: stat}
	got := byID(checkHostAccess(context.Background(), newEnv(h)))
	want(t, got["host_access:r1"], StatusOK, "host_access_ok")
	want(t, got["host_access:r2"], StatusDown, "host_access_denied_mac")
	want(t, got["host_access:r3"], StatusDown, "host_access_volume_missing")
	if got["host_access:r3"].Params["volume"] != "/Volumes/Gone" {
		t.Fatalf("names the volume: %+v", got["host_access:r3"])
	}
	want(t, got["host_access:r4"], StatusDown, "host_access_missing")
	want(t, got["host_access:r5"], StatusDown, "host_access_share_not_mounted")
	want(t, got["host_access:r6"], StatusDegraded, "host_access_error")
	sum := got["host_access"]
	want(t, sum, StatusDown, "host_access_full_disk_access")
	if sum.Critical || sum.Params["bad"] != 5 || sum.Params["total"] != 6 {
		t.Fatalf("summary: %+v", sum)
	}
	if got["host_access:r2"].Group != GroupDependencies || got["host_access:r2"].Help != KindHostAccess {
		t.Fatalf("group and help: %+v", got["host_access:r2"])
	}
}

func TestHostAccessWordsOffTheMac(t *testing.T) {
	withGOOS(t, "linux")
	db := rootsDB([]driver.Value{"r1", "/mnt/media", "Media"})
	h := &Handler{DB: db.DB, SupervisorURL: "x",
		ReadDir: func(p string) error { return pathErr(p, syscall.EACCES) }}
	got := byID(checkHostAccess(context.Background(), newEnv(h)))
	want(t, got["host_access:r1"], StatusDown, "host_access_denied")
	want(t, got["host_access"], StatusDegraded, "host_access_some")
}

func TestHostAccessAllOKAndNone(t *testing.T) {
	ok := func(string) error { return nil }
	h := &Handler{DB: rootsDB([]driver.Value{"r1", "/a", "A"}).DB, ReadDir: ok}
	got := byID(checkHostAccess(context.Background(), newEnv(h)))
	want(t, got["host_access"], StatusOK, "host_access_all_ok")
	h = &Handler{DB: rootsDB().DB, ReadDir: ok}
	want(t, only(t, checkHostAccess(context.Background(), newEnv(h))), StatusOK, "host_access_none")
	bad := queuestest.New(&queuestest.Rule{Match: "from library_roots", Err: errors.New("boom")})
	want(t, only(t, checkHostAccess(context.Background(), newEnv(&Handler{DB: bad.DB}))),
		StatusDegraded, "host_access_unreadable")
	if got := checkHostAccess(context.Background(), newEnv(&Handler{})); got != nil {
		t.Fatalf("no database: no item, got %+v", got)
	}
}

// A share that hangs is answered "not answering" at its deadline, and the
// next rounds wait on the same read: never a second one in flight.
func TestHostAccessHungShareKeepsOneReadInFlight(t *testing.T) {
	release := make(chan struct{})
	var started, running, most atomic.Int32
	read := func(string) error {
		started.Add(1)
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		defer running.Add(-1)
		<-release
		return nil
	}
	r := DefaultRules()
	r.CheckTimeoutMs = 200
	h := &Handler{DB: rootsDB([]driver.Value{"r1", "/Volumes/Sleepy/x", "X"}).DB, ReadDir: read, Rules: &r}
	for i := 0; i < 5; i++ {
		e := newEnv(h)
		e.rules = r
		begin := time.Now()
		got := byID(checkHostAccess(context.Background(), e))
		if d := time.Since(begin); d > time.Second {
			t.Fatalf("round %d waited %v on a hung share", i, d)
		}
		want(t, got["host_access:r1"], StatusDegraded, "host_access_timeout")
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("one read of the hung share, got %d", n)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for running.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// it answered: the next round reads again and reports it
	got := byID(checkHostAccess(context.Background(), newEnv(h)))
	want(t, got["host_access:r1"], StatusOK, "host_access_ok")
	if started.Load() != 2 || most.Load() != 1 {
		t.Fatalf("reads: %d started, %d at once", started.Load(), most.Load())
	}
}

func TestHostAccessForgetsRemovedRoots(t *testing.T) {
	ok := func(string) error { return nil }
	h := &Handler{DB: rootsDB([]driver.Value{"r1", "/a", "A"}, []driver.Value{"r2", "/b", "B"}).DB, ReadDir: ok}
	checkHostAccess(context.Background(), newEnv(h))
	h.DB = rootsDB([]driver.Value{"r1", "/a", "A"}).DB
	checkHostAccess(context.Background(), newEnv(h))
	h.probes.mu.Lock()
	defer h.probes.mu.Unlock()
	if len(h.probes.m) != 1 || h.probes.m["/a"] == nil {
		t.Fatalf("kept: %v", h.probes.m)
	}
}

// The real read: a folder lists, an empty one too, a missing one is not
// found and one without permission is denied.
func TestListDir(t *testing.T) {
	dir := t.TempDir()
	if err := listDir(dir); err != nil {
		t.Fatalf("empty folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.mp4"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := listDir(dir); err != nil {
		t.Fatalf("folder: %v", err)
	}
	if err := listDir(filepath.Join(dir, "nope")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		return
	}
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if err := listDir(locked); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("locked: %v", err)
	}
}

// Only maktaba-server's API reads the folders: a Docker install does not.
func TestHostAccessRunsOnlyUnderTheSupervisor(t *testing.T) {
	has := func(h *Handler) bool {
		for _, c := range h.checks() {
			if c.Name == "host_access" {
				return true
			}
		}
		return false
	}
	if has(&Handler{}) {
		t.Fatal("host access without the supervisor")
	}
	if !has(&Handler{SupervisorURL: "http://127.0.0.1:1"}) {
		t.Fatal("no host access under the supervisor")
	}
}

func TestPlexNoRouteToALocalAddressNamesLocalNetwork(t *testing.T) {
	withGOOS(t, "darwin")
	ctx := context.Background()
	run := func(h *Handler, url string) Item {
		p := plexStatus(`{"reachable":false,"error":"dial tcp 192.168.1.20:32400: connect: no route to host"}`)
		p.Plex.URL = url
		h.TV = &fakeTV{plex: p}
		return only(t, checkPlex(ctx, newEnv(h)))
	}
	sup := func() *Handler { return &Handler{SupervisorURL: "http://127.0.0.1:1"} }
	want(t, run(sup(), "http://192.168.1.20:32400"), StatusDown, "plex_unreachable_local_network")
	want(t, run(sup(), "http://plex.local:32400"), StatusDown, "plex_unreachable_local_network")
	want(t, run(sup(), "https://plex.example.com"), StatusDown, "plex_unreachable")
	want(t, run(&Handler{}, "http://192.168.1.20:32400"), StatusDown, "plex_unreachable")
	withGOOS(t, "linux")
	want(t, run(sup(), "http://192.168.1.20:32400"), StatusDown, "plex_unreachable")
	refused := plexStatus(`{"reachable":false,"error":"connection refused"}`)
	refused.Plex.URL = "http://192.168.1.20:32400"
	want(t, only(t, checkPlex(ctx, newEnv(&Handler{SupervisorURL: "x", TV: &fakeTV{plex: refused}}))),
		StatusDown, "plex_unreachable")
}
