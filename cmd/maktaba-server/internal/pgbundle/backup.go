package pgbundle

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"

	_ "github.com/lib/pq" // the dump's snapshot session
)

// KeepBackups is how many nightly dumps stay in backups/ when [backups] keep is not
// set (the pre-update dumps are kept apart: upgrade.go).
const KeepBackups = config.DefaultBackupKeep

// NightlyPolicy is when the scheduled dump is taken and how many stay ([backups]
// nightly, every_days, hour, keep: Epic 36 story 36.16).
type NightlyPolicy struct {
	Off                                        bool
	EveryDays                                  int
	Hour                                       int
	Keep                                       int
	KeepMonthly, KeepYearly, CompressAfterDays int
}

// DefaultNightly is the default weekly schedule and archive policy.
func DefaultNightly() NightlyPolicy {
	return NightlyPolicy{EveryDays: config.DefaultBackupsEveryDays, Hour: backupHour, Keep: KeepBackups,
		KeepMonthly: config.DefaultBackupKeepMonthly, KeepYearly: config.DefaultBackupKeepYearly, CompressAfterDays: config.DefaultBackupCompressAfterDays}
}

func (b Bundle) nightly() NightlyPolicy {
	if b.Nightly == nil {
		return DefaultNightly()
	}
	p := *b.Nightly
	if p.EveryDays < 1 {
		p.EveryDays = DefaultNightly().EveryDays
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
// scheduled retention tiers, after making room for it (MakeRoom). It returns the dump's path.
func (b Bundle) Backup(ctx context.Context, now time.Time) (string, error) {
	if err := MakeRoomWith(b.Backups, b.room()); err != nil {
		return "", err
	}
	path := filepath.Join(b.Backups, backupPrefix+now.Local().Format(backupStamp)+backupSuffix)
	if err := b.dumpTo(ctx, path, DumpOptions{}); err != nil {
		return "", err
	}
	b.recordNightly(now)
	return path, b.tidyRetained()
}

// Tidy keeps the backups folder to its rules: the newest nightly dumps ([backups] keep),
// the newest pre-update dumps (UpdateKeep, or what the last update recorded), dumps of
// failed updates and replaced databases by count and age, and no leftovers of a dump or
// rehearsal cut short (PruneBackups). The backups loop runs it at every check, scheduled
// dumps on or off.
func (b Bundle) Tidy() error { return b.TidyContext(context.Background(), time.Now()) }

func (b Bundle) TidyContext(ctx context.Context, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.tidyRetained(); err != nil {
		return err
	}
	return b.compressBackups(ctx, now)
}

func (b Bundle) tidyRetained() error {
	err := b.prune()
	if perr := PruneBackups(b.Backups, "", BackupsKeep(b.Backups, b.UpdateKeep)); perr != nil && !os.IsNotExist(perr) && err == nil {
		err = perr
	}
	return err
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

// openDB opens the dump's snapshot session (tests with fake programs open none).
var openDB = sql.Open

// dumpTo writes a dump of the app database to path (PgDump: written beside,
// path.partial, and moved into place, so a dump cut short is never taken for one).
func (b Bundle) dumpTo(ctx context.Context, path string, opts DumpOptions) error {
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	if opts.Open == nil {
		opts.Open = func(context.Context) (*sql.DB, error) {
			u, err := b.SuperURL()
			if err != nil {
				return nil, err
			}
			return openDB("postgres", u)
		}
		opts.PSQL = b.Bin("psql")
	}
	return PgDump(ctx, b.Bin("pg_dump"), []string{"-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser,
		"--dbname=" + AppDatabase}, append(os.Environ(), "PGPASSWORD="+superPW), path, opts)
}

// backups lists the finished scheduled dumps, newest first.
func (b Bundle) backups() ([]scheduledDump, error) {
	entries, err := os.ReadDir(b.Backups)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []scheduledDump
	for _, e := range entries {
		if isDump(e) && strings.HasPrefix(e.Name(), backupPrefix) {
			info, err := e.Info()
			if err != nil {
				continue // gone meanwhile
			}
			out = append(out, scheduledDump{e.Name(), scheduledAt(e.Name(), info.ModTime()), info.IsDir()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].at.Equal(out[j].at) {
			return out[i].name > out[j].name
		}
		return out[i].at.After(out[j].at)
	})
	return out, nil
}

func (b Bundle) prune() error {
	if BackupsHeld(b.Backups) {
		return nil // a restore is not done: every dump stays
	}
	names, err := b.backups()
	if err != nil {
		return err
	}
	keep := selectScheduledToKeep(names, b.nightly())
	for _, dump := range names {
		if !keep[dump.name] {
			if err := RemoveDump(filepath.Join(b.Backups, dump.name)); err != nil {
				return err
			}
		}
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
	if names[0].at.After(last) {
		last = names[0].at
	}
	return last
}

// backupHour is the local hour from which the nightly dump is due.
const backupHour = config.DefaultBackupHour

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
	b.RunBackupsLoop(ctx, ctx, interval, log)
}

// RunBackupsLoop is RunBackups whose loop ends with loop (the backups component turned
// off, Epic 51 story 51.04) while a dump under way runs with work (the server's): a dump
// is never cut by the component's stop; the loop returns once it ends.
func (b Bundle) RunBackupsLoop(loop, work context.Context, interval time.Duration, log io.Writer) {
	p := b.nightly()
	due := func() bool {
		// not while a restore is not done: what the database holds is not known to be whole
		return !BackupsHeld(b.Backups) && dueEvery(time.Now(), b.lastBackup(), p)
	}
	if p.Off {
		fmt.Fprintln(log, "[postgres] the scheduled backups are off ([backups] nightly = \"off\"): no dump is taken "+
			"unless asked; the old ones are still pruned")
		due = func() bool { return false }
	}
	runBackups(loop, work, interval, log, due, b.Backup, func() error { return b.TidyContext(loop, time.Now()) })
}

// runBackups is RunBackupsLoop's loop: at every tick of interval, dump when due says so
// (a dump tidies after itself), else tidy.
func runBackups(loop, work context.Context, interval time.Duration, log io.Writer, due func() bool,
	dump func(ctx context.Context, now time.Time) (string, error), tidy func() error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-loop.Done():
			return
		case <-t.C:
		}
		if loop.Err() != nil {
			continue
		}
		if !due() {
			// the dumps a rule no longer keeps go without waiting for the next dump or update
			if err := tidy(); err != nil && loop.Err() == nil {
				fmt.Fprintf(log, "[postgres] backup maintenance failed (trying again later): %v\n", err)
			}
			continue
		}
		if path, err := dump(work, time.Now()); err != nil {
			if work.Err() == nil {
				fmt.Fprintf(log, "[postgres] nightly backup failed (trying again later): %v\n", err)
			}
		} else {
			fmt.Fprintf(log, "[postgres] backup written to %s\n", path)
			if loop.Err() == nil {
				if err := tidy(); err != nil && loop.Err() == nil {
					fmt.Fprintf(log, "[postgres] backup maintenance failed: %v\n", err)
				}
			}
		}
	}
}

type scheduledDump struct {
	name      string
	at        time.Time
	directory bool
}

func scheduledAt(name string, fallback time.Time) time.Time {
	if t, err := time.ParseInLocation(backupStamp, strings.TrimSuffix(strings.TrimPrefix(name, backupPrefix), backupSuffix), time.Local); err == nil {
		return t
	}
	return fallback.In(time.Local)
}

// selectScheduledToKeep takes dumps ordered newest first.
func selectScheduledToKeep(dumps []scheduledDump, p NightlyPolicy) map[string]bool {
	keep := make(map[string]bool)
	months, years := make(map[string]bool), make(map[string]bool)
	for i, dump := range dumps {
		month, year := dump.at.Format("2006-01"), dump.at.Format("2006")
		monthly := len(months) < p.KeepMonthly && !months[month]
		yearly := len(years) < p.KeepYearly && !years[year]
		if monthly {
			months[month] = true
		}
		if yearly {
			years[year] = true
		}
		if i < p.Keep || monthly || yearly {
			keep[dump.name] = true
		}
	}
	return keep
}

// dumpAt uses the timestamp of a scheduled dump even if writing its directory bumped its mtime.
func dumpAt(name string, mod time.Time) time.Time {
	if dumpKind(name) == backupPrefix {
		return scheduledAt(name, mod)
	}
	return mod
}
