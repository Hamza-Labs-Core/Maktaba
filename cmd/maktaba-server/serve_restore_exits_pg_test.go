package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dbsafe"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// pgExits is a home on a real Postgres with the real maktaba-api, its database step and its
// restorer: on 0.2.0 (slot 2), updated from 0.1.0 (slot 1), whose backup is firstBackup;
// a row written since. 0.2.1 (slot 3) is the newer release.
type pgExits struct {
	t        *testing.T
	ctx      context.Context
	b        pgbundle.Bundle
	ext      *dbsafe.Target // nil: the bundled kind
	dsn      string
	stateDir string
	releases string
	api      string
	inst     *keepInstaller
	r        *restorer
	log      strings.Builder
}

const pgFirst, pgOther = "pre-v0.1.0-to-v0.2.0.dump", "maktaba-20261101-030000.dump"

// newPGExits is the home; kind is bundled, external (a Postgres the home does not bundle),
// or external without psql.
func newPGExits(t *testing.T, kind string) *pgExits {
	t.Helper()
	programs := pgtest.Programs(t)
	root, err := os.MkdirTemp("", "mkexits") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	e := &pgExits{t: t, ctx: context.Background(), releases: filepath.Join(root, "releases"), api: pgtest.APIBinary(t)}
	e.b, _ = pgtest.Bundle(e.ctx, t, programs, root, false)
	// a disk with little room is no failure here: the dumps are small
	e.b.Room = &pgbundle.RoomPolicy{ReserveShare: 0.0001, ReserveMin: 1 << 20, MaxShare: 1}
	if e.dsn, err = e.b.URL(); err != nil {
		t.Fatal(err)
	}
	super, err := e.b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	target := dbsafe.Target{URL: super, Bin: e.b.Bin, Backups: e.b.Backups}
	if kind != "bundled" {
		e.dsn = super // one role does it all (pgvector's extension needs it)
		e.ext = &target
		// a Postgres of the person's own has no pg_textsearch of the home's
		q, err := e.b.NewQuerier()
		if err != nil {
			t.Fatal(err)
		}
		e.must(q.Exec(e.ctx, pgbundle.AppDatabase, "DROP EXTENSION IF EXISTS pg_textsearch CASCADE; DROP SCHEMA IF EXISTS textsearch CASCADE;"))
	}
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(root, "state")
	e.stateDir = cfg.StateDir()
	files := []string{
		"-- +goose Up\nCREATE TABLE a_t (id int);\n-- +goose Down\nDROP TABLE a_t;\n",
		"-- +goose Up\nCREATE TABLE b_t (id int);\n-- +goose Down\nDROP TABLE b_t;\n",
		"-- +goose Up\nCREATE TABLE c_t (id int);\n-- +goose Down\nDROP TABLE c_t;\n"}
	for n, version := range []string{"v0.1.0", "v0.2.0", "v0.2.1"} {
		dir := filepath.Join(e.releases, version, "migrations")
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := "| Slot | File |\n|---|---|\n"
		for i := range n + 1 {
			name := fmt.Sprintf("%04d_m.sql", i+1)
			manifest += fmt.Sprintf("| `%04d` | %s |\n", i+1, name)
			e.must(os.WriteFile(filepath.Join(dir, name), []byte(files[i]), 0o644))
		}
		e.must(os.WriteFile(filepath.Join(dir, "MANIFEST.md"), []byte(manifest), 0o644))
		e.must(os.WriteFile(filepath.Join(dir, updater.SchemaKeepFile), []byte(`{"protocol":1}`), 0o644))
	}
	e.inst = &keepInstaller{running: "0.2.0", kept: []updater.KeptRelease{{Version: "0.1.0", Schema: 1},
		{Version: "0.2.0", Schema: 2}, {Version: "0.2.1", Schema: 3}}}
	e.must(e.start("v0.1.0"))
	e.exec(`INSERT INTO a_t VALUES (1)`)
	e.must(target.Dump(e.ctx, filepath.Join(e.b.Backups, pgOther)))
	e.must(e.start("v0.2.0"))
	e.exec(`INSERT INTO b_t VALUES (1), (2)`)
	if _, err := os.Stat(filepath.Join(e.b.Backups, pgFirst)); err != nil {
		t.Fatal(err)
	}
	if kind == "external without psql" { // since the backups were taken
		e.ext.Bin = func(name string) string {
			if name == "psql" {
				return filepath.Join(root, "no-such-psql")
			}
			return e.b.Bin(name)
		}
	}
	e.r = &restorer{stateDir: e.stateDir, db: &target, inst: e.inst, log: io.Discard, schema: target.DumpSchema, identity: target.Identity,
		run: func() *updater.Journal { return nil }}
	return e
}

func (e *pgExits) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatalf("%v\n%s", err, e.log.String())
	}
}

// start is a start of version: its database step.
func (e *pgExits) start(version string) error {
	cmd := exec.Command(e.api)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+e.dsn, "MAKTABA_NODE_MODE=",
		"MAKTABA_MIGRATIONS_DIR="+filepath.Join(e.releases, version, "migrations"))
	g := &migrationGate{stateDir: e.stateDir, version: version, api: cmd, keep: 3, log: &e.log, room: *e.b.Room,
		prev: func(v string) string { return filepath.Join(e.releases, "v"+layout.NormalizeVersion(v), "migrations") }}
	if e.ext != nil {
		g.ext = e.ext
	} else {
		g.pg = &e.b
	}
	e.inst.running = layout.NormalizeVersion(version)
	return g.run(e.ctx, func(supervisor.Detail) {})
}

func (e *pgExits) db() *sql.DB {
	e.t.Helper()
	db, err := sql.Open("postgres", e.dsn)
	e.must(err)
	e.t.Cleanup(func() { _ = db.Close() })
	db.SetMaxIdleConns(0)
	return db
}

func (e *pgExits) exec(q string) {
	e.t.Helper()
	_, err := e.db().Exec(q)
	e.must(err)
}

func (e *pgExits) one(q string) (n int64) {
	e.t.Helper()
	if err := e.db().QueryRow(q).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v\n%s", q, err, e.log.String())
	}
	return n
}

// slot and rows: the schema, a_t's rows, and b_t's (-1: no such table).
func (e *pgExits) want(when string, slot, a, b int64) {
	e.t.Helper()
	if n := e.one(`SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied`); n != slot {
		e.t.Fatalf("%s: slot %d, want %d\n%s", when, n, slot, e.log.String())
	}
	if n := e.one(`SELECT count(*) FROM a_t`); n != a {
		e.t.Fatalf("%s: %d rows of a_t, want %d", when, n, a)
	}
	n := int64(-1)
	if e.one(`SELECT count(*) FROM pg_class WHERE relname = 'b_t' AND relnamespace = 'public'::regnamespace`) == 1 {
		n = e.one(`SELECT count(*) FROM b_t`)
	}
	if n != b {
		e.t.Fatalf("%s: %d rows of b_t, want %d", when, n, b)
	}
}

func (e *pgExits) rec() apppayload.Database {
	e.t.Helper()
	rec, _, err := apppayload.ReadDatabase(e.stateDir)
	e.must(err)
	return rec
}

// ask is a restore of the backup name asked by a person (the restart after it: the next start).
func (e *pgExits) ask(name string) {
	e.t.Helper()
	if _, err := e.r.restore(e.ctx, name, "admin", true, false); err != nil {
		e.t.Fatalf("the restore of %s: %v", name, err)
	}
}

// damage is a way the first backup's restore fails; it leaves what brings it back.
type damage struct {
	name string
	kind string // newPGExits' kinds it is about ("": all)
	do   func(e *pgExits) (undo func())
	data string
	code string
}

var pgDamages = []damage{
	{"the backup is lost", "", func(e *pgExits) func() {
		p := filepath.Join(e.b.Backups, pgFirst)
		e.must(os.Rename(p, p+".away"))
		return func() { e.must(os.Rename(p+".away", p)) }
	}, apppayload.DataUntouched, "db_restore_backup_failed"},
	{"a table's rows are cut", "bundled|external", cutRows, apppayload.DataUntouched, "db_restore_backup_failed"},
	// the schema is dropped before the dump goes in: the database is empty
	{"a table's rows are cut", "external without psql", cutRows, apppayload.DataGone, "db_restore_backup_gone"},
	// the bundled one is checked before it takes the database's place
	{"it comes out not whole", "bundled", shortRows, apppayload.DataUntouched, "db_restore_backup_failed"},
	// another is committed first
	{"it comes out not whole", "external|external without psql", shortRows, apppayload.DataReplaced, "db_restore_backup_part"},
	{"the database dropped for room, then no room all the same", "bundled", func(e *pgExits) func() {
		prev := restoreDump
		restoreDump = func(ctx context.Context, b pgbundle.Bundle, _ string, before func(string) error, _ io.Writer) error {
			e.must(before(pgbundle.StepGone))
			q, err := b.NewQuerier()
			e.must(err)
			e.must(q.Exec(ctx, "postgres", "DROP DATABASE IF EXISTS "+pgbundle.AppDatabase+" WITH (FORCE);"))
			return errors.New("no space left on device")
		}
		return func() { restoreDump = prev }
	}, apppayload.DataGone, "db_restore_backup_gone"},
}

// cutRows cuts a_t's data in the first backup.
func cutRows(e *pgExits) func() {
	dir := filepath.Join(e.b.Backups, pgFirst)
	ents, err := os.ReadDir(dir)
	e.must(err)
	var path string
	var raw []byte
	for _, ent := range ents {
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		e.must(err)
		if ent.Name() != "toc.dat" && ent.Name() != pgbundle.CountsFile && len(b) > len(raw) {
			path, raw = filepath.Join(dir, ent.Name()), b
		}
	}
	e.must(os.Truncate(path, int64(len(raw)/2)))
	return func() { e.must(os.WriteFile(path, raw, 0o600)) }
}

// shortRows makes the first backup say it has more rows than it does.
func shortRows(e *pgExits) func() {
	p := filepath.Join(e.b.Backups, pgFirst, pgbundle.CountsFile)
	raw, err := os.ReadFile(p)
	e.must(err)
	more := regexp.MustCompile(`("public\.a_t": ?)1\b`).ReplaceAllString(string(raw), "${1}7")
	if more == string(raw) {
		e.t.Fatalf("no count of a_t in %s", raw)
	}
	e.must(os.WriteFile(p, []byte(more), 0o600))
	return func() { e.must(os.WriteFile(p, raw, 0o600)) }
}

// A restore fails at each stage, on each kind of Postgres; then each way out of it is
// taken: the data decides which there are, and none serves anything but the library as it
// was before the restore, or as a backup holds it.
//
// Opt-in: MAKTABA_TEST_PG_BIN (pgtest.Programs).
func TestEveryWayOutOfAFailedRestore(t *testing.T) {
	t.Setenv(envLauncherRoot, "")
	for _, kind := range []string{"bundled", "external", "external without psql"} {
		for _, d := range pgDamages {
			if d.kind != "" && !strings.Contains("|"+d.kind+"|", "|"+kind+"|") {
				continue
			}
			for _, exit := range []string{"try again", "another backup", "give up", "a newer release"} {
				t.Run(kind+"/"+d.name+"/"+exit, func(t *testing.T) {
					wayOut(t, kind, d, exit)
				})
			}
		}
	}
}

func wayOut(t *testing.T, kind string, d damage, exit string) {
	e := newPGExits(t, kind)
	e.ask(pgFirst)
	if rec := e.rec(); rec.Data != apppayload.DataUntouched || rec.DataID == "" || rec.Before == nil || rec.From != "0.1.0" {
		t.Fatalf("asked: %+v", rec)
	}
	undo := d.do(e)
	for i := 1; i <= 2; i++ { // and a plain restart
		err := e.start("v0.1.0")
		if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != d.code {
			t.Fatalf("start %d: %v, want %s\n%s", i, err, d.code, e.log.String())
		}
		if rec := e.rec(); rec.Data != d.data || rec.TriedBy != "v0.1.0" || rec.RestoreError == "" {
			t.Fatalf("start %d: record %+v", i, rec)
		}
		if i == 1 && d.data == apppayload.DataGone && kind == "bundled" {
			// as the next start's Postgres does: an app database, empty
			q, err := e.b.NewQuerier()
			e.must(err)
			e.must(q.Exec(e.ctx, "postgres", "CREATE DATABASE "+pgbundle.AppDatabase+" OWNER "+pgbundle.AppRole+";"))
		}
	}
	asides, err := filepath.Glob(filepath.Join(e.b.Backups, pgbundle.ReplacedDumpPrefix+"*.dump"))
	e.must(err)
	if len(asides) != 1 || e.rec().Aside != asides[0] {
		t.Fatalf("set aside %v, recorded %q", asides, e.rec().Aside)
	}
	untouched := d.data == apppayload.DataUntouched
	if untouched {
		e.want("after the failed tries", 2, 1, 2)
	}
	switch exit {
	case "try again":
		undo()
		e.must(e.start("v0.1.0"))
		e.want("tried again", 1, 1, -1)
	case "another backup":
		undo() // the drop for room fails what it is made to
		e.ask(pgOther)
		e.must(e.start("v0.1.0"))
		e.want("another backup", 1, 1, -1)
		// and what was there before the first: its set aside is whole
		e.ask(filepath.Base(asides[0]))
		e.must(e.start("v0.2.0"))
		e.want("the set aside", 2, 1, 2)
	case "give up":
		err := e.r.abandon(e.ctx, "admin")
		if !untouched {
			if restoreCode(err) != "restore_data_changed" || e.rec().State != apppayload.DatabaseRestoring {
				t.Fatalf("given up on %s data: %v, %+v", d.data, err, e.rec())
			}
			return
		}
		e.must(err)
		if len(e.inst.ran) == 0 || e.inst.ran[len(e.inst.ran)-1] != "0.2.0" {
			t.Fatalf("switched %v, want 0.2.0 last", e.inst.ran)
		}
		e.must(e.start("v0.2.0"))
		e.want("given up", 2, 1, 2)
	case "a newer release":
		err := e.start("v0.2.1")
		if !untouched {
			if !supervisor.Halted(err) || e.rec().State != apppayload.DatabaseRestoring || e.rec().TriedBy != "v0.2.1" {
				t.Fatalf("a newer release on %s data: %v, %+v", d.data, err, e.rec())
			}
			if (d.data != apppayload.DataGone || kind != "bundled") && e.one(`SELECT count(*) FROM pg_class WHERE relname = 'c_t'`) != 0 {
				t.Fatal("it migrated")
			}
			return
		}
		e.must(err)
		e.want("a newer release", 3, 1, 2)
		if rec := e.rec(); rec.Version != "v0.2.1" || rec.State != apppayload.DatabaseMigrated {
			t.Fatalf("record %+v", rec)
		}
	}
	if pgbundle.BackupsHeld(e.b.Backups) {
		t.Fatal("the backups are held though no restore waits")
	}
}

// A try that hangs (its program stuck) is stopped by giving the restore up, on a real
// Postgres: the library is as it was.
func TestAHungRestoreIsGivenUp(t *testing.T) {
	t.Setenv(envLauncherRoot, "")
	for _, kind := range []string{"bundled", "external"} {
		t.Run(kind, func(t *testing.T) {
			e := newPGExits(t, kind)
			e.ask(pgFirst)
			// the bundled one's program hangs; the external one waits for a lock
			started := make(chan struct{})
			prevB, prevE := restoreDump, restoreExternal
			t.Cleanup(func() { restoreDump, restoreExternal = prevB, prevE })
			restoreDump = func(ctx context.Context, b pgbundle.Bundle, _ string, _ func(string) error, _ io.Writer) error {
				q, err := b.NewQuerier()
				if err != nil {
					return err
				}
				close(started)
				return q.Exec(ctx, "postgres", "SELECT pg_sleep(600);")
			}
			hold, unlock := context.WithCancel(e.ctx)
			defer unlock()
			restoreExternal = func(ctx context.Context, x dbsafe.Target, dump string, before func(string) error, w io.Writer) error {
				// a session that comes back whenever the restore ends it
				locker := e.db()
				go func() {
					for hold.Err() == nil {
						_, _ = locker.ExecContext(hold, `BEGIN; LOCK TABLE a_t; SELECT pg_sleep(600)`)
					}
				}()
				for e.one(`SELECT count(*) FROM pg_locks WHERE relation = 'a_t'::regclass AND mode = 'AccessExclusiveLock' AND granted`) == 0 {
					time.Sleep(10 * time.Millisecond)
				}
				close(started)
				return prevE(ctx, x, dump, before, w)
			}
			step := make(chan error, 1)
			go func() { step <- e.start("v0.1.0") }()
			<-started
			if kind == "external" {
				pgtest.WaitFor(t, "the restore to wait for the lock", time.Minute, func() bool {
					return e.one(`SELECT count(*) FROM pg_stat_activity WHERE application_name = '`+pgbundle.RestoreApp+
						`' AND wait_event_type = 'Lock'`) == 1
				})
				if rec := e.rec(); rec.Data != apppayload.DataUntouched {
					t.Fatalf("record %+v", rec)
				}
			}
			e.must(e.r.abandon(e.ctx, "admin"))
			if err := <-step; !supervisor.Halted(err) || !strings.Contains(err.Error(), "stopped by admin") {
				t.Fatalf("the step: %v", err)
			}
			unlock()
			e.must(e.start("v0.2.0"))
			e.want("given up", 2, 1, 2)
		})
	}
}
