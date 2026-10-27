package pgbundle

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// KeepBackups is how many nightly dumps stay in backups/ when [backups] keep is not
// set (the pre-update dumps are kept apart: upgrade.go).
const KeepBackups = 7

// NightlyPolicy is when the scheduled dump is taken and how many stay ([backups]
// nightly, every_days, hour, keep: Epic 36 story 36.16).
type NightlyPolicy struct {
	Off       bool
	EveryDays int
	Hour      int
	Keep      int
}

// DefaultNightly is a dump every night from 03:00, the newest 7 kept.
func DefaultNightly() NightlyPolicy {
	return NightlyPolicy{EveryDays: 1, Hour: backupHour, Keep: KeepBackups}
}

func (b Bundle) nightly() NightlyPolicy {
	if b.Nightly == nil {
		return DefaultNightly()
	}
	p := *b.Nightly
	if p.EveryDays < 1 {
		p.EveryDays = 1
	}
	if p.Hour < 0 || p.Hour > 23 {
		p.Hour = backupHour
	}
	if p.Keep < 1 {
		p.Keep = KeepBackups
	}
	return p
}

func (b Bundle) room() RoomPolicy {
	if b.Room == nil {
		return DefaultRoom()
	}
	return *b.Room
}

const (
	backupPrefix = "maktaba-"
	backupSuffix = ".dump"
	backupStamp  = "20060102-150405"
)

// Backup writes a dump of the whole app database (dumpfmt.go) into backups/
// (maktaba-<local time>.dump, written beside and moved into place) and keeps the
// newest KeepBackups, after making room for it (MakeRoom). It returns the dump's path.
func (b Bundle) Backup(ctx context.Context, now time.Time) (string, error) {
	if err := MakeRoomWith(b.Backups, b.room()); err != nil {
		return "", err
	}
	path := filepath.Join(b.Backups, backupPrefix+now.Format(backupStamp)+backupSuffix)
	if err := b.dumpTo(ctx, path, DumpOptions{}); err != nil {
		return "", err
	}
	b.recordNightly(now)
	return path, b.prune()
}

// recordNightly keeps when the last scheduled dump was taken (LastNightly), apart
// from the dumps: moving or deleting them never makes the next one due early.
func (b Bundle) recordNightly(now time.Time) {
	if b.LastNightly == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.LastNightly), 0o700); err == nil {
		_ = os.WriteFile(b.LastNightly, []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
}

// DumpTo is a whole dump of the app database to path (a restore by hand sets the
// database it replaces aside so).
func (b Bundle) DumpTo(ctx context.Context, path string) error {
	return b.dumpTo(ctx, path, DumpOptions{})
}

// dumpTo writes a dump of the app database to path (PgDump: written beside,
// path.partial, and moved into place, so a dump cut short is never taken for one).
func (b Bundle) dumpTo(ctx context.Context, path string, opts DumpOptions) error {
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	return PgDump(ctx, b.Bin("pg_dump"), []string{"-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser,
		"--dbname=" + AppDatabase}, append(os.Environ(), "PGPASSWORD="+superPW), path, opts)
}

// backups lists the finished nightly dumps, oldest first.
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
		if isDump(e) && strings.HasPrefix(e.Name(), backupPrefix) {
			out = append(out, e.Name())
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
	for keep := b.nightly().Keep; len(names) > keep; {
		if err := RemoveDump(filepath.Join(b.Backups, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

// lastBackup is when the last scheduled dump was taken: the record (LastNightly) or
// the newest dump in the folder, the later of the two (zero when there is neither).
func (b Bundle) lastBackup() time.Time {
	var last time.Time
	if raw, err := os.ReadFile(b.LastNightly); b.LastNightly != "" && err == nil {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw))); err == nil {
			last = t
		}
	}
	names, _ := b.backups()
	if len(names) == 0 {
		return last
	}
	n := names[len(names)-1]
	t, err := time.ParseInLocation(backupStamp, strings.TrimSuffix(strings.TrimPrefix(n, backupPrefix), backupSuffix), time.Local)
	if err == nil && t.After(last) {
		last = t
	}
	return last
}

// backupHour is the local hour from which the nightly dump is due.
const backupHour = 3

// due reports whether the nightly dump should run now: none yet, or the last
// one is from before today's backupHour (a Mac asleep at that hour catches up
// when it wakes).
func due(now, last time.Time) bool { return dueEvery(now, last, DefaultNightly()) }

// dueEvery reports whether the scheduled dump should run now under p: none yet, or
// the slot (p.Hour, local time) the last one belongs to is p.EveryDays or more
// slots before the current one (a Mac asleep at that hour catches up when it wakes).
func dueEvery(now, last time.Time, p NightlyPolicy) bool {
	if last.IsZero() {
		return true
	}
	slotOf := func(t time.Time) time.Time {
		t = t.In(now.Location())
		s := time.Date(t.Year(), t.Month(), t.Day(), p.Hour, 0, 0, 0, now.Location())
		if t.Before(s) {
			s = s.AddDate(0, 0, -1)
		}
		return s
	}
	every := max(p.EveryDays, 1)
	return !slotOf(last).AddDate(0, 0, every).After(slotOf(now))
}

// RunBackups makes the nightly dump until ctx ends, checking every interval. A
// failed dump (postgres down) is logged and tried again at the next check.
func (b Bundle) RunBackups(ctx context.Context, interval time.Duration, log io.Writer) {
	p := b.nightly()
	if p.Off {
		fmt.Fprintln(log, "[postgres] the scheduled backups are off ([backups] nightly = \"off\"): no dump is taken "+
			"unless asked")
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		if !dueEvery(now, b.lastBackup(), p) {
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
