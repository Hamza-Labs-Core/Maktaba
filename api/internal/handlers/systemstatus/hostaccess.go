package systemstatus

// Host access: can this server read the library folders? On the Mac app the
// server runs under macOS's privacy rules, and without Full Disk Access a
// read of a NAS share or a protected folder fails with "operation not
// permitted" while the folder is plainly there. Only when maktaba-server
// supervises the processes (the Mac, Windows and Linux apps, a server set up
// with `maktaba-server setup`): a Docker install lists paths of its host that
// the API's container never mounts.
//
// A read of a share that hangs (a NAS gone to sleep, a dead SMB mount) must
// never pile goroutines up: each path has at most one read in flight, a
// round waits for it no longer than its deadline, and a later round waits on
// the same read instead of starting another.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// hostGOOS is the OS the reasons are worded for (a test sets it).
var hostGOOS = runtime.GOOS

// probe is one read of a folder: done closes when it returned.
type probe struct {
	done    chan struct{}
	started time.Time
	err     error
}

// probes keeps the read in flight (or the last one) of each path.
type probes struct {
	mu sync.Mutex
	m  map[string]*probe
}

// listDir is the read: open the folder and list one name of it.
func listDir(path string) error {
	f, err := os.Open(path) //nolint:gosec // a library root an admin entered
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// probeResult is what a round learnt of one folder: answered with err, or
// not answered after inFlight.
type probeResult struct {
	answered bool
	err      error
	inFlight time.Duration
}

// probe answers whether path can be listed, waiting at most wait. A read
// still running from an earlier round is waited on, not started again.
func (p *probes) probe(ctx context.Context, path string, wait time.Duration, read func(string) error) probeResult {
	p.mu.Lock()
	if p.m == nil {
		p.m = map[string]*probe{}
	}
	pr := p.m[path]
	running := false
	if pr != nil {
		select {
		case <-pr.done:
		default:
			running = true
		}
	}
	if !running {
		pr = &probe{done: make(chan struct{}), started: time.Now()}
		p.m[path] = pr
		go func(pr *probe) {
			defer close(pr.done)
			defer func() {
				if r := recover(); r != nil {
					pr.err = fmt.Errorf("the read failed: %v", r)
				}
			}()
			pr.err = read(path)
		}(pr)
	}
	p.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-pr.done:
		return probeResult{answered: true, err: pr.err}
	case <-t.C:
	case <-ctx.Done():
	}
	return probeResult{inFlight: time.Since(pr.started)}
}

// forget drops the paths no longer a library root.
func (p *probes) forget(keep map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.m {
		if !keep[k] {
			delete(p.m, k)
		}
	}
}

const libraryRootsSQL = `
SELECT lr.id, lr.path, l.name
  FROM library_roots lr
  JOIN libraries l ON l.id = lr.library_id
 WHERE l.deleted_at IS NULL
 ORDER BY l.name, lr.path`

type libraryRoot struct{ id, path, library string }

func loadRoots(ctx context.Context, db *sql.DB) ([]libraryRoot, error) {
	rows, err := db.QueryContext(ctx, libraryRootsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []libraryRoot{}
	for rows.Next() {
		var r libraryRoot
		if err := rows.Scan(&r.id, &r.path, &r.library); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// probeWait is how long one round waits on a folder's read: under the
// check's own deadline, so the folders are answered, not the whole check.
func probeWait(timeout time.Duration) time.Duration {
	w := timeout * 3 / 4
	if w < 100*time.Millisecond {
		w = 100 * time.Millisecond
	}
	return w
}

// volumeOf is the volume a macOS path is on (/Volumes/<name>), or "".
func volumeOf(p string) string {
	if !strings.HasPrefix(p, "/Volumes/") {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(p, "/Volumes/"), "/")
	if name == "" {
		return ""
	}
	return "/Volumes/" + name
}

// rootItem says what the read of one root found.
func rootItem(r libraryRoot, local string, res probeResult, stat func(string) error) Item {
	err := res.err
	it := base("host_access:"+r.id, GroupDependencies, KindHostAccess, r.path)
	it.Details = map[string]any{"library": r.library, "path": r.path, "local_path": local}
	p := map[string]any{"path": r.path, "library": r.library}
	switch {
	case sharepath.IsCanonical(local):
		it.set(StatusDown, "host_access_share_not_mounted", p,
			fmt.Sprintf("The share %s is not mounted on this machine.", r.path))
	case !res.answered:
		p["wait_seconds"] = secs(res.inFlight)
		it.set(StatusDegraded, "host_access_timeout", p,
			fmt.Sprintf("Reading %s did not answer: the share may be asleep or disconnected.", local))
	case err == nil:
		it.set(StatusOK, "host_access_ok", p, "Can be read.")
	case errors.Is(err, fs.ErrPermission):
		p["detail"] = errText(err)
		if hostGOOS == "darwin" {
			it.set(StatusDown, "host_access_denied_mac", p,
				"macOS does not let Maktaba read this folder: turn on Full Disk Access for Maktaba "+
					"(System Settings → Privacy & Security), then restart the server.")
		} else {
			it.set(StatusDown, "host_access_denied", p,
				"The server has no permission to read this folder: "+errText(err))
		}
	case errors.Is(err, fs.ErrNotExist):
		if vol := volumeOf(local); vol != "" && stat(vol) != nil {
			p["volume"] = vol
			it.set(StatusDown, "host_access_volume_missing", p,
				fmt.Sprintf("The volume %s is not mounted: connect the share, then check again.", vol))
		} else {
			it.set(StatusDown, "host_access_missing", p, "The folder does not exist: "+local)
		}
	default:
		p["detail"] = errText(err)
		it.set(StatusDegraded, "host_access_error", p, "The folder cannot be read: "+errText(err))
	}
	return it
}

func statErr(p string) error { _, err := os.Stat(p); return err }

func checkHostAccess(ctx context.Context, e *env) []Item {
	sum := base("host_access", GroupDependencies, KindHostAccess, "Folder access")
	if e.h.DB == nil {
		return nil
	}
	roots, err := loadRoots(ctx, e.h.DB)
	if err != nil {
		sum.set(StatusDegraded, "host_access_unreadable", map[string]any{"detail": errText(err)},
			"The library folders cannot be read from the database: "+errText(err))
		return one(sum)
	}
	read := e.h.ReadDir
	if read == nil {
		read = listDir
	}
	stat := e.h.Stat
	if stat == nil {
		stat = statErr
	}
	wait := probeWait(e.rules.timeout())
	items := make([]Item, len(roots))
	keep := map[string]bool{}
	var wg sync.WaitGroup
	for i, r := range roots {
		// a share with no mount here stays canonical
		local := sharepath.ToLocal(r.path)
		if !sharepath.IsCanonical(local) {
			local = filepath.Clean(local)
		}
		keep[local] = true
		wg.Add(1)
		go func(i int, r libraryRoot, local string) {
			defer wg.Done()
			defer safego.Recover(nil, "systemstatus_root_probe")
			if sharepath.IsCanonical(local) {
				items[i] = rootItem(r, local, probeResult{answered: true}, stat)
				return
			}
			items[i] = rootItem(r, local, e.h.probes.probe(ctx, local, wait, read), stat)
		}(i, r, local)
	}
	wg.Wait()
	e.h.probes.forget(keep)

	total, bad, denied := len(items), 0, 0
	for _, it := range items {
		if it.Status != StatusOK {
			bad++
		}
		if it.Code == "host_access_denied_mac" || it.Code == "host_access_denied" {
			denied++
		}
	}
	p := map[string]any{"total": total, "bad": bad}
	sum.Details = p
	switch {
	case total == 0:
		sum.set(StatusOK, "host_access_none", nil, "No library folder is set up yet.")
	case denied > 0 && hostGOOS == "darwin":
		sum.set(StatusDown, "host_access_full_disk_access", p,
			fmt.Sprintf("%d of %d library folders cannot be read: Maktaba needs Full Disk Access on this Mac.",
				bad, total))
	case bad > 0:
		sum.set(StatusDegraded, "host_access_some", p,
			fmt.Sprintf("%d of %d library folders cannot be read.", bad, total))
	default:
		sum.set(StatusOK, "host_access_all_ok", p, fmt.Sprintf("All %d library folders can be read.", total))
	}
	return append([]Item{sum}, items...)
}
