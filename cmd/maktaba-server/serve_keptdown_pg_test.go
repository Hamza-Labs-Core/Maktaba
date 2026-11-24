package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appruntime"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// A home on a schema an earlier rollback kept, whose next update fails, on a real Postgres
// with the real maktaba-api and the real database step: release A (slot 1) runs on B's
// schema (slots 2 and 3, kept, its rows in B's table written since), then C's update fails,
// in its migrations or in its watch. Only C's own migrations are undone, whatever the
// install kind; the kept schema, its rows, and its being kept for A stay; A starts on it.
//
// Opt-in: MAKTABA_TEST_PG_BIN (pgtest.Programs).
func TestAFailedUpdateNeverUndoesAKeptSchema(t *testing.T) {
	programs := pgtest.Programs(t)
	ctx := context.Background()
	root, err := os.MkdirTemp("", "mkkept") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b, _ := pgtest.Bundle(ctx, t, programs, root, false)
	dsn, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxIdleConns(0) // the database step ends the other sessions: none is kept
	api := pgtest.APIBinary(t)
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(root, "state")
	stateDir := cfg.StateDir()

	const (
		a  = "-- +goose Up\nCREATE TABLE a_t (id int);\n-- +goose Down\nDROP TABLE a_t;\n"
		b2 = "-- +goose Up\nCREATE TABLE b_t (id int);\n-- +goose Down\nDROP TABLE b_t;\n"
		b3 = "-- +goose Up\nALTER TABLE a_t ADD COLUMN IF NOT EXISTS b_c int;\n-- +goose Down\nALTER TABLE a_t DROP COLUMN b_c;\n"
		// not keepable (a CHECK), but lossless: undone by its Down script
		c4 = "-- +goose Up\nALTER TABLE a_t ADD CONSTRAINT c_check CHECK (id < 100);\n" +
			"-- +goose Down\nALTER TABLE a_t DROP CONSTRAINT IF EXISTS c_check;\n"
		bad5 = "-- +goose Up\nALTER TABLE nowhere ADD COLUMN x int;\n-- +goose Down\nSELECT 1;\n"
		// a NO TRANSACTION Down that stops part way
		badDown4 = "-- +goose NO TRANSACTION\n-- +goose Up\nALTER TABLE a_t ADD CONSTRAINT c_check CHECK (id < 100);\n" +
			"-- +goose Down\nALTER TABLE a_t DROP CONSTRAINT c_check;\nALTER TABLE nowhere DROP COLUMN x;\n"
	)
	// the releases' trees: <releases>/<version>/migrations
	releases := filepath.Join(root, "releases")
	release := func(version string, files ...string) string {
		dir := filepath.Join(releases, version, "migrations")
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := "| Slot | File |\n|---|---|\n"
		for i, body := range files {
			name := fmt.Sprintf("%04d_m.sql", i+1)
			manifest += fmt.Sprintf("| `%04d` | %s |\n", i+1, name)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for name, body := range map[string]string{"MANIFEST.md": manifest, updater.SchemaKeepFile: `{"protocol":1}`} {
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	release("v0.1.0", a)
	release("v0.2.0", a, b2, b3)
	release("v0.3.0", a, b2, b3, c4, bad5)
	release("v0.3.1", a, b2, b3, c4)
	release("v0.3.2", a, b2, b3, badDown4)
	var logs strings.Builder
	apiCmd := func(version string) *exec.Cmd {
		cmd := exec.Command(api)
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn, "MAKTABA_NODE_MODE=",
			"MAKTABA_MIGRATIONS_DIR="+filepath.Join(releases, version, "migrations"))
		return cmd
	}
	gate := func(version string) *migrationGate {
		return &migrationGate{stateDir: stateDir, version: version, pg: &b, api: apiCmd(version), keep: 3, log: &logs,
			updates: filepath.Join(root, "updates"), prev: func(v string) string {
				return filepath.Join(releases, "v"+layout.NormalizeVersion(v), "migrations")
			}}
	}
	start := func(version string) error {
		t.Helper()
		return gate(version).run(ctx, func(supervisor.Detail) {})
	}
	rec := func() apppayload.Database {
		t.Helper()
		r, ok, err := apppayload.ReadDatabase(stateDir)
		if err != nil || !ok {
			t.Fatalf("record: %v %v", ok, err)
		}
		return r
	}
	one := func(q string) (n int64) {
		t.Helper()
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	// the kept schema and what was written since it was kept are there, and nothing of C's is
	kept := func(when string, version string) {
		t.Helper()
		if n := one(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`); n != 3 {
			t.Fatalf("%s: slot %d, want 3\n%s", when, n, logs.String())
		}
		if n := one(`SELECT count(*) FROM b_t`); n != 2 {
			t.Fatalf("%s: %d rows of B's table left, want 2", when, n)
		}
		if n := one(`SELECT count(*) FROM a_t WHERE b_c = 7`); n != 1 {
			t.Fatalf("%s: %d values of B's column left, want 1", when, n)
		}
		if n := one(`SELECT count(*) FROM pg_constraint WHERE conname = 'c_check'`); n != 0 {
			t.Fatalf("%s: C's constraint is left", when)
		}
		if r := rec(); layout.NormalizeVersion(r.Version) != version || r.KeptFor != "0.1.0" {
			t.Fatalf("%s: record %+v, want %s's, kept for 0.1.0", when, r, version)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\n%s", err, logs.String())
		}
	}

	// A, then B, whose watch fails: its schema is kept for A, which writes into it
	must(start("v0.1.0"))
	must(start("v0.2.0"))
	if ok, why := updater.KeepsSchemaFrom(filepath.Join(releases, "v0.1.0", "migrations"), filepath.Join(releases, "v0.2.0", "migrations")); !ok {
		t.Fatalf("B's schema is not one to keep: %s", why)
	}
	if ok, err := layout.MarkRecordKept(apppayload.DatabasePath(stateDir), "v0.2.0", "v0.1.0", "watch_timeout"); !ok || err != nil {
		t.Fatalf("kept: %v %v", ok, err)
	}
	must(start("v0.1.0"))
	for _, q := range []string{`INSERT INTO b_t VALUES (1), (2)`, `INSERT INTO a_t (id, b_c) VALUES (1, 7)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	kept("A on B's schema", "0.2.0")

	// C's migrations fail (0004 ran, 0005 did not), twice in a row
	for _, try := range []string{"first", "second"} {
		if err := start("v0.3.0"); !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_undone" {
			t.Fatalf("the %s failed update: %v\n%s", try, err, logs.String())
		}
		kept("after the "+try+" failure of C's migrations", "0.1.0")
		must(start("v0.1.0")) // A starts on it, its api told the schema may be ahead
	}

	// C's migrations work, and its watch fails: each install kind's rollback
	app := &updater.AppInstaller{Payload: filepath.Join(releases, "v0.3.1"), LauncherRoot: filepath.Join(root, "launcher")}
	rt := &updater.RuntimeInstaller{Runtime: appruntime.Runtime{Root: releases}}
	for name, c := range map[string]struct {
		inst updater.SchemaReverter
		j    updater.Journal
	}{
		"app":     {app, updater.Journal{From: "v0.1.0", FromRef: filepath.Join(releases, "v0.1.0")}},
		"runtime": {rt, updater.Journal{From: "v0.1.0", FromRef: "v0.1.0", Staged: "v0.3.1"}},
	} {
		must(start("v0.3.1"))
		if r := rec(); r.SchemaBefore != 3 || r.DumpKeptFor != "0.1.0" {
			t.Fatalf("%s: C's record %+v", name, r)
		}
		before, onKept, err := recordFile(apppayload.DatabasePath(stateDir)).SchemaBefore("0.3.1")
		must(err)
		slot, ok, why := c.inst.DownTo(&c.j, before, onKept)
		if !ok || slot != 3 {
			t.Fatalf("%s: Down to %d, %v (%s), want 3", name, slot, ok, why)
		}
		u := &updates{api: apiCmd("v0.3.1")}
		must(u.migrateDown(ctx, &c.j, slot))
		if ok, err := recordFile(apppayload.DatabasePath(stateDir)).MarkDown("v0.3.1", "v0.1.0", "watch_timeout"); !ok || err != nil {
			t.Fatalf("%s: MarkDown %v %v", name, ok, err)
		}
		kept(name+"'s rollback of C", "0.1.0")
		must(start("v0.1.0"))
	}

	// a Down script that fails part way (NO TRANSACTION): the kept schema is whole all the
	// same, and the record still names C
	must(start("v0.3.2"))
	u := &updates{api: apiCmd("v0.3.2")}
	if err := u.migrateDown(ctx, nil, 3); err == nil {
		t.Fatal("a Down script that failed is said to have worked")
	}
	if n := one(`SELECT (SELECT count(*) FROM b_t) + (SELECT count(*) FROM a_t WHERE b_c = 7)`); n != 3 {
		t.Fatalf("after a Down script that failed part way: %d of the 3 rows and values written since are left", n)
	}
	if r := rec(); layout.NormalizeVersion(r.Version) != "0.3.2" {
		t.Fatalf("record %+v", r)
	}
}

// A record an older release wrote without schema_before, its migrations cut short: the slot
// is the one the database itself recorded before they began, on a real Postgres.
func TestSchemaVersionAt(t *testing.T) {
	programs := pgtest.Programs(t)
	ctx := context.Background()
	root, err := os.MkdirTemp("", "mkat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b, _ := pgtest.Bundle(ctx, t, programs, root, false)
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE goose_db_version (id serial, version_id bigint NOT NULL, is_applied bool NOT NULL, tstamp timestamp DEFAULT now())`,
		`INSERT INTO goose_db_version (version_id, is_applied, tstamp) VALUES (0, true, now() - interval '9 days'),
		    (230, true, now() - interval '8 days'), (234, true, now() - interval '2 hours'), (235, true, now())`,
		`GRANT SELECT ON goose_db_version TO ` + pgbundle.AppRole,
	} {
		if err := q.Exec(ctx, pgbundle.AppDatabase, s); err != nil {
			t.Fatal(err)
		}
	}
	g := &migrationGate{stateDir: root, pg: &b}
	for ago, want := range map[time.Duration]int64{time.Hour: 234, 3 * time.Hour: 230, 240 * time.Hour: 0, -time.Hour: 235} {
		got, err := schemaVersionAt(ctx, g, time.Now().Add(-ago))
		if err != nil || got != want {
			t.Errorf("%s ago: %d %v, want %d", ago, got, err, want)
		}
	}
	if _, err := schemaVersionAt(ctx, &migrationGate{stateDir: root, pg: &pgbundle.Bundle{Data: root}}, time.Now()); err == nil {
		t.Error("a database that does not answer")
	} else if errors.Is(err, context.Canceled) {
		t.Error(err)
	}
}
