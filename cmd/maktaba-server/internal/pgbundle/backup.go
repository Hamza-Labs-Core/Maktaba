package pgbundle

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KeepBackups is how many nightly dumps stay in backups/ (the pre-update dumps
// are kept apart: [postgres] update_dumps, upgrade.go).
const KeepBackups = 7

const (
	backupPrefix = "maktaba-"
	backupSuffix = ".dump"
	backupStamp  = "20060102-150405"
)

// Backup writes a pg_dump custom-format dump of the app database into
// backups/ (maktaba-<local time>.dump, written beside and renamed into place)
// and keeps the newest KeepBackups, after making room for it (MakeRoom). It
// returns the dump's path.
func (b Bundle) Backup(ctx context.Context, now time.Time) (string, error) {
	if err := MakeRoom(b.Backups); err != nil {
		return "", err
	}
	path := filepath.Join(b.Backups, backupPrefix+now.Format(backupStamp)+backupSuffix)
	if err := b.dumpTo(ctx, path); err != nil {
		return "", err
	}
	return path, b.prune()
}

// DumpTo is dumpTo (the database step's dump of a failed update's state, set aside).
func (b Bundle) DumpTo(ctx context.Context, path string) error { return b.dumpTo(ctx, path) }

// dumpCompress is the dump's gzip level. pg_dump compresses on one core and the search
// vectors (segment_embeddings, tens of GB as text) made the default level the whole
// cost of a dump: level 1 is 5.5x faster for 27% more bytes (the pre-update dump the
// app waits for before it starts: 36 minutes became 7).
const dumpCompress = "1"

// dumpTo writes a pg_dump custom-format dump of the app database to path
// (written beside, path.partial, and renamed into place: a dump cut short is
// never taken for one).
func (b Bundle) dumpTo(ctx context.Context, path string) error {
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	part := path + ".partial"
	cmd := exec.CommandContext(ctx, b.Bin("pg_dump"), "-Fc", "-Z", dumpCompress, "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
		"-U", SuperUser, "-f", part, AppDatabase)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(part, path)
}

// backups lists the finished dumps, oldest first.
func (b Bundle) backups() ([]string, error) {
	entries, err := os.ReadDir(b.Backups)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out) // the stamp sorts by time
	return out, nil
}

func (b Bundle) prune() error {
	names, err := b.backups()
	if err != nil {
		return err
	}
	for len(names) > KeepBackups {
		if err := os.Remove(filepath.Join(b.Backups, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// lastBackup is the time of the newest dump (zero when there is none).
func (b Bundle) lastBackup() time.Time {
	names, _ := b.backups()
	if len(names) == 0 {
		return time.Time{}
	}
	n := names[len(names)-1]
	t, err := time.ParseInLocation(backupStamp, strings.TrimSuffix(strings.TrimPrefix(n, backupPrefix), backupSuffix), time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// backupHour is the local hour from which the nightly dump is due.
const backupHour = 3

// due reports whether the nightly dump should run now: none yet, or the last
// one is from before today's backupHour (a Mac asleep at that hour catches up
// when it wakes).
func due(now, last time.Time) bool {
	if last.IsZero() {
		return true
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), backupHour, 0, 0, 0, now.Location())
	if now.Before(slot) {
		slot = slot.AddDate(0, 0, -1)
	}
	return last.Before(slot)
}

// RunBackups makes the nightly dump until ctx ends, checking every interval. A
// failed dump (postgres down) is logged and tried again at the next check.
func (b Bundle) RunBackups(ctx context.Context, interval time.Duration, log io.Writer) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		if !due(now, b.lastBackup()) {
			continue
		}
		if path, err := b.Backup(ctx, now); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(log, "[postgres] nightly backup failed (trying again later): %v\n", err)
			}
		} else {
			fmt.Fprintf(log, "[postgres] backup written to %s\n", path)
		}
	}
}
