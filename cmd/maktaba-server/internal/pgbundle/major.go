package pgbundle

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The major-version upgrade of the bundled cluster (Epic 49, story 49.2): PostgreSQL 16 → 18.
//
// The new major's cluster lives in a directory of its own (DataDirFor: <state>/postgres-18),
// made from the old one by pg_upgrade with the old major's programs, which the payload keeps
// for this step (postgres-16/). The old cluster (<state>/postgres) is never changed: pg_upgrade
// clones its files (APFS, btrfs, XFS: no extra room) or copies them, never links them (a
// linked old cluster cannot start again once the new one has). So whatever switches back to
// the release before (the updater's rollback, the launcher's guard, the desktop app) finds
// the cluster that release left, as it left it, and starts on it with its own code.
//
// The new cluster records what it was made from (upgradeMarker): the old cluster's system
// identifier and its last checkpoint. When the old one has moved on since (the release
// before ran on it again after a rollback), the new one is stale: it is set aside and made
// again, so a second try of the update takes what the release before wrote meanwhile.

// UpgradeMarker is the file in a cluster pg_upgrade made, saying from what.
const UpgradeMarker = "maktaba-upgraded-from.json"

// UpgradedFrom is UpgradeMarker's content.
type UpgradedFrom struct {
	Major      string    `json:"major"`
	Data       string    `json:"data"`
	SystemID   string    `json:"system_identifier"`
	Checkpoint string    `json:"checkpoint"`
	Mode       string    `json:"mode"` // clone or copy
	At         time.Time `json:"at"`
}

// DataDirFor is the cluster directory of a major under root: <root>/postgres for 16 and
// older (every cluster made before Epic 49), <root>/postgres-<major> from 18 on.
func DataDirFor(root, major string) string {
	if n, err := strconv.Atoi(major); err == nil && n >= 18 {
		return filepath.Join(root, "postgres-"+major)
	}
	return filepath.Join(root, "postgres")
}

// OldHomeFor is where a payload keeps the previous major's programs for the upgrade
// (<payload>/postgres-<major>), "" when it has none.
func OldHomeFor(home, major string) string {
	h := filepath.Join(filepath.Dir(home), "postgres-"+major)
	if hasServer(h) {
		return h
	}
	return ""
}

// Control is what pg_controldata says of a cluster that matters here.
type Control struct {
	State, SystemID, Checkpoint string
	Checksums                   bool
}

// ReadControl runs bin's pg_controldata on data.
func ReadControl(ctx context.Context, pgControldata, data string) (Control, error) {
	out, err := exec.CommandContext(ctx, pgControldata, "-D", data).Output()
	if err != nil {
		return Control{}, fmt.Errorf("pg_controldata %s: %w", data, err)
	}
	var c Control
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Database cluster state":
			c.State = v
		case "Database system identifier":
			c.SystemID = v
		case "Latest checkpoint location":
			c.Checkpoint = v
		case "Data page checksum version":
			c.Checksums = v != "0"
		}
	}
	if c.SystemID == "" || c.State == "" {
		return Control{}, fmt.Errorf("pg_controldata %s: unexpected output", data)
	}
	return c, nil
}

// MajorUpgrade brings Old's cluster to New's major in New.Data. Old and New share Keys
// (one superuser password), Port and the machine's settings.
type MajorUpgrade struct {
	Old, New Bundle
	// NoClone forces a copy (tests); Room says whether a copy fits (nil: the disk's own
	// free space, less the room the backups' policy keeps).
	NoClone bool
	Room    func(dir string, need uint64) error
}

// ErrUpgradeNoRoom is a copy that does not fit: nothing was changed.
var ErrUpgradeNoRoom = errors.New("no room for the upgraded copy of the library database")

// Needed reports whether New.Data must be made (again) from Old.Data, and why.
func (u MajorUpgrade) Needed(ctx context.Context) (bool, string, error) {
	if !u.Old.Initialized() {
		return false, "no old cluster", nil
	}
	if !u.New.Initialized() {
		return true, "no " + filepath.Base(u.New.Data) + " yet", nil
	}
	m, ok, err := ReadUpgradedFrom(u.New.Data)
	if err != nil {
		return false, "", err
	}
	if !ok {
		// a cluster of the new major that no upgrade made (a fresh install): it stays
		return false, "made fresh", nil
	}
	c, err := ReadControl(ctx, u.Old.Bin("pg_controldata"), u.Old.Data)
	if err != nil {
		return false, "", err
	}
	if m.Checkpoint == "" {
		return true, "an upgrade that did not finish", nil
	}
	if c.SystemID != m.SystemID {
		// another cluster stands where the old one was (made anew by hand, or by a release
		// before the upgrade started on an empty directory): never taken over the upgraded one
		return false, "the old directory holds another cluster now (" + c.SystemID + ")", nil
	}
	if c.Checkpoint != m.Checkpoint {
		return true, "the old cluster moved on since the upgrade (" + m.Checkpoint + " → " + c.Checkpoint + ")", nil
	}
	return false, "up to date", nil
}

// ReadUpgradedFrom reads data's UpgradeMarker (ok false when there is none).
func ReadUpgradedFrom(data string) (UpgradedFrom, bool, error) {
	raw, err := os.ReadFile(filepath.Join(data, UpgradeMarker))
	if errors.Is(err, fs.ErrNotExist) {
		return UpgradedFrom{}, false, nil
	}
	if err != nil {
		return UpgradedFrom{}, false, err
	}
	var m UpgradedFrom
	if err := json.Unmarshal(raw, &m); err != nil {
		return UpgradedFrom{}, false, fmt.Errorf("%s: %w", UpgradeMarker, err)
	}
	return m, true, nil
}

// Run makes New.Data from Old.Data when Needed says so: the old cluster shut down cleanly
// (its own programs start and stop it once when it was not), a new cluster made like
// Prepare makes one (the same superuser, password, encoding, locale and checksums), pg_upgrade
// into it (clone, else copy), the old cluster's own settings and Maktaba's markers carried
// over, the marker written, and the directory renamed into place. A stale New.Data is moved
// aside (<data>.stale), never deleted. The old cluster is left as it was.
func (u MajorUpgrade) Run(ctx context.Context, log io.Writer) error {
	need, why, err := u.Needed(ctx)
	if err != nil || !need {
		return err
	}
	oldMajor, _ := u.Old.ClusterMajor()
	fmt.Fprintf(log, "[postgres] upgrading the library database from PostgreSQL %s (%s) to %s: %s\n",
		oldMajor, u.Old.Data, filepath.Base(u.New.Data), why)
	c, err := ReadControl(ctx, u.Old.Bin("pg_controldata"), u.Old.Data)
	if err != nil {
		return err
	}
	if c.State != "shut down" && c.State != "shut down in recovery" {
		// pg_upgrade takes only a cluster shut down cleanly: its own server recovers it
		fmt.Fprintf(log, "[postgres] the old cluster was not shut down cleanly (%s): starting and stopping it once\n", c.State)
		old := u.Old
		old.LAN, old.TLS = false, false
		if err := old.withServer(ctx, log, func() error { return nil }); err != nil {
			return fmt.Errorf("the old cluster's recovery: %w", err)
		}
		if c, err = ReadControl(ctx, u.Old.Bin("pg_controldata"), u.Old.Data); err != nil {
			return err
		}
	}
	superPW, err := readPassword(u.New.passwordFile(SuperUser))
	if err != nil {
		return fmt.Errorf("the superuser's password: %w", err)
	}
	// the app role's grants on pg_catalog's replication slot functions (clusterSQL): pg_upgrade
	// restores a catalog function's privileges by its signature, and PostgreSQL 17 changed
	// pg_create_logical_replication_slot's, so they come off the old cluster for the upgrade
	// and back on after it. Its marker goes first: should this stop in between, the release
	// before runs clusterSQL again at its next start (Prepare)
	marker, _ := os.ReadFile(filepath.Join(u.Old.Data, clusterMarker))
	if len(marker) > 0 {
		if err := os.Remove(filepath.Join(u.Old.Data, clusterMarker)); err != nil {
			return err
		}
		if err := u.oldSQL(ctx, log, slotGrantsOffSQL); err != nil {
			return fmt.Errorf("the old cluster's grants, before the upgrade: %w", err)
		}
		defer func() {
			// back on, then the marker (a failed upgrade too: the old cluster is as it was);
			// nothing when the run put them back itself
			if marker == nil {
				return
			}
			if gerr := u.oldSQL(context.WithoutCancel(ctx), log, clusterSQL); gerr != nil {
				fmt.Fprintf(log, "[postgres] level=warn the old cluster's grants are not back (%v): its next start makes them again\n", gerr)
				return
			}
			_ = os.WriteFile(filepath.Join(u.Old.Data, clusterMarker), marker, 0o600)
		}()
	}
	if u.New.Initialized() {
		stale := u.New.Data + ".stale"
		_ = os.RemoveAll(stale)
		if err := os.Rename(u.New.Data, stale); err != nil {
			return err
		}
		fmt.Fprintf(log, "[postgres] the earlier upgraded copy is set aside in %s\n", stale)
	}
	tmp := u.New.Data + ".upgrading"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	nb := u.New
	nb.Data = tmp
	// each try on a cluster initdb just made (a try that failed part way leaves roles in it,
	// which the next refuses)
	try := func(mode string) error {
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
		if err := nb.initdbWith(ctx, superPW, log, checksumFlag(c.Checksums)); err != nil {
			return err
		}
		if err := u.upgrade(ctx, nb, mode, log); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		return nil
	}
	mode := "clone"
	cloneErr := errors.New("a copy was asked for")
	if !u.NoClone {
		cloneErr = try("--clone")
	}
	if cloneErr != nil {
		mode = "copy"
		fmt.Fprintf(log, "[postgres] no clone of the old cluster's files (%v): copying them\n", cloneErr)
		if err := u.room(u.Old.Data, tmp); err != nil {
			return err
		}
		if err := try("--copy"); err != nil {
			return err
		}
	}
	// what ALTER SYSTEM set on the old cluster, and Maktaba's own markers (pg_hba.conf is
	// written again at every start)
	// (not clusterMarker: the new cluster's first start runs clusterSQL on its own signatures)
	for _, f := range []string{"postgresql.auto.conf", bootstrapMarker} {
		if raw, err := os.ReadFile(filepath.Join(u.Old.Data, f)); err == nil {
			if err := os.WriteFile(filepath.Join(tmp, f), raw, 0o600); err != nil {
				return err
			}
		}
	}
	// a marker with no checkpoint first: a run that stops before the real one is written is
	// made again by the next start (Needed), never kept as if it were done
	if err := os.WriteFile(filepath.Join(tmp, UpgradeMarker), []byte(`{"major":"`+oldMajor+`"}`+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, u.New.Data); err != nil {
		return err
	}
	// the marker, last: what the old cluster is once its grants are back (their server's
	// stop moves its checkpoint on)
	if len(marker) > 0 {
		if err := u.oldSQL(ctx, log, clusterSQL); err != nil {
			return fmt.Errorf("the old cluster's grants, after the upgrade: %w", err)
		}
		if err := os.WriteFile(filepath.Join(u.Old.Data, clusterMarker), marker, 0o600); err != nil {
			return err
		}
		marker = nil // the deferred put back is done
	}
	if c, err = ReadControl(ctx, u.Old.Bin("pg_controldata"), u.Old.Data); err != nil {
		return err
	}
	m := UpgradedFrom{Major: oldMajor, Data: u.Old.Data, SystemID: c.SystemID, Checkpoint: c.Checkpoint, Mode: mode,
		At: time.Now().UTC()}
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(u.New.Data, UpgradeMarker), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(log, "[postgres] the library database is upgraded (%s): %s; PostgreSQL %s's cluster is left as it was in %s\n",
		mode, u.New.Data, oldMajor, u.Old.Data)
	return nil
}

// oldSQL runs sql as the superuser in the app's database on the old cluster, with a server
// of its own (never on the LAN).
func (u MajorUpgrade) oldSQL(ctx context.Context, log io.Writer, sql string) error {
	old := u.Old
	old.LAN, old.TLS = false, false
	return old.withServer(ctx, log, func() error {
		q, err := old.NewQuerier()
		if err != nil {
			return err
		}
		return q.Exec(ctx, AppDatabase, sql)
	})
}

// slotGrantsOffSQL puts the replication slot functions clusterSQL grants the app role back
// to the privileges initdb gave them (pg_dump then has nothing of theirs to dump): the app
// role's EXECUTE off, PUBLIC's back unless initdb took it away (pg_init_privs).
const slotGrantsOffSQL = `DO $$
DECLARE f regprocedure;
BEGIN
    FOR f IN SELECT p.oid::regprocedure FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
              WHERE n.nspname = 'pg_catalog' AND p.proname IN (` + slotFunctions + `) LOOP
        EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM ` + AppRole + `', f);
        IF NOT EXISTS (SELECT 1 FROM pg_init_privs WHERE objoid = f::oid AND classoid = 'pg_proc'::regclass) THEN
            EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO PUBLIC', f);
        END IF;
    END LOOP;
END
$$;
`

func checksumFlag(on bool) string {
	if on {
		return "--data-checksums"
	}
	return "--no-data-checksums"
}

// room checks that a copy of old fits beside it.
func (u MajorUpgrade) room(old, into string) error {
	var size uint64
	_ = filepath.WalkDir(old, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				size += uint64(fi.Size())
			}
		}
		return nil
	})
	need := size + size/10 + 256<<20
	if u.Room != nil {
		return u.Room(into, need)
	}
	free, total, err := diskSpaceOf(filepath.Dir(into))
	if err != nil {
		return nil // not known: pg_upgrade says when the disk fills
	}
	if reserve := DefaultRoom().reserveOf(total); free < need+reserve {
		return fmt.Errorf("%w: it needs %d MB and %d MB are free (%d MB kept free)", ErrUpgradeNoRoom,
			need>>20, free>>20, reserve>>20)
	}
	return nil
}

// upgrade runs pg_upgrade from Old into nb.Data with mode (--clone or --copy), in a work
// directory of its own (it writes its scripts into the current one), its servers on a
// short socket directory (a unix socket path has at most 103 bytes) and with the arguments
// the bundled server runs with that a cluster needs to start at all (wal_level=logical:
// the machines' replication slots refuse a server without it).
func (u MajorUpgrade) upgrade(ctx context.Context, nb Bundle, mode string, log io.Writer) error {
	work, err := os.MkdirTemp(filepath.Dir(nb.Data), ".pg_upgrade-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	superPW, err := readPassword(u.New.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	opts := "-c wal_level=logical -c max_replication_slots=40 -c max_wal_senders=40 -c max_worker_processes=24"
	args := []string{"-b", filepath.Dir(u.Old.Bin("postgres")), "-B", filepath.Dir(u.New.Bin("postgres")),
		"-d", u.Old.Data, "-D", nb.Data, "-U", SuperUser, "-p", strconv.Itoa(u.Old.Port + 1),
		"-P", strconv.Itoa(u.Old.Port + 2), "-o", opts, "-O", opts + " -c shared_preload_libraries=" + strings.Join(nb.Preload(), ","),
		mode}
	if runtime.GOOS != "windows" {
		sock, err := os.MkdirTemp("", "pgu")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(sock) }()
		args = append(args, "-s", sock)
	}
	cmd := exec.CommandContext(ctx, u.New.Bin("pg_upgrade"), args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	tail := &tailWriter{max: startTailBytes, next: childLog(log)}
	cmd.Stdout, cmd.Stderr = tail, tail
	if err := restrict(cmd); err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_upgrade %s: %w%s%s", mode, err, tail.said(), upgradeLogTail(nb.Data))
	}
	return nil
}

// upgradeLogTail is the end of the log pg_upgrade wrote last in data (its own logs say why
// a step failed; its output only names them).
func upgradeLogTail(data string) string {
	// a database's restore says why it failed in its dump log; else the newest log
	logs, _ := filepath.Glob(filepath.Join(data, "pg_upgrade_output.d", "*", "log", "pg_upgrade_dump_*.log"))
	if len(logs) == 0 {
		logs, _ = filepath.Glob(filepath.Join(data, "pg_upgrade_output.d", "*", "log", "*.log"))
	}
	var newest string
	var at time.Time
	for _, l := range logs {
		if fi, err := os.Stat(l); err == nil && fi.ModTime().After(at) {
			newest, at = l, fi.ModTime()
		}
	}
	raw, err := os.ReadFile(newest)
	if newest == "" || err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	return "\n" + filepath.Base(newest) + ":\n" + strings.Join(lines, "\n")
}

// AfterUpgrade brings a cluster pg_upgrade just made up to date, with a server of its own:
// every extension to the version its new programs install (pg_upgrade keeps the old ones),
// and the planner's statistics pg_upgrade could not carry (vacuumdb --missing-stats-only).
// It is safe to run again.
func (b Bundle) AfterUpgrade(ctx context.Context, log io.Writer) error {
	superPW, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	return b.withServer(ctx, log, func() error {
		q, err := b.NewQuerier()
		if err != nil {
			return err
		}
		rows, err := q.Query(ctx, "postgres", "SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate")
		if err != nil {
			return err
		}
		for _, db := range rows {
			exts, err := q.Query(ctx, db, "SELECT e.extname FROM pg_extension e JOIN pg_available_extensions a "+
				"ON a.name = e.extname WHERE a.default_version IS DISTINCT FROM e.extversion")
			if err != nil {
				return err
			}
			for _, e := range exts {
				if _, err := q.Query(ctx, db, `ALTER EXTENSION "`+strings.ReplaceAll(e, `"`, `""`)+`" UPDATE`); err != nil {
					return fmt.Errorf("ALTER EXTENSION %s UPDATE in %s: %w", e, db, err)
				}
				fmt.Fprintf(log, "[postgres] %s: extension %s updated\n", db, e)
			}
		}
		cmd := exec.CommandContext(ctx, b.Bin("vacuumdb"), "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port), "-U", SuperUser,
			"--all", "--analyze-in-stages", "--missing-stats-only")
		cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
		cmd.Stdout, cmd.Stderr = childLog(log), childLog(log)
		return cmd.Run()
	})
}
