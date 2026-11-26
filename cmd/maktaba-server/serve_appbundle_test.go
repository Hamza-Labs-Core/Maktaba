package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// This test binary is also these tests' stand-ins (run before TestMain, so
// before the tests' flags are read): with MAKTABA_TEST_APPBUNDLE_CHILD set a
// supervised process that runs until it is stopped; with
// MAKTABA_TEST_APPBUNDLE_MIGRATE set the `maktaba-api migrate` of
// TestRunMigrateUp, which says what it is told and exits with its code.
//
// It also lets the setup tests' stand-ins (setup_fake_test.go) be told what to
// do through the venv bootstrap's clean environment.
func init() {
	_ = os.Setenv("MAKTABA_VENV_KEEP_ENV", "MAKTABA_TEST_FAKE,MAKTABA_TEST_FAKE_LOG,MAKTABA_TEST_FAKE_FAIL,MAKTABA_TEST_FAKE_VENV_READY")
	if os.Getenv("MAKTABA_TEST_APPBUNDLE_CHILD") != "" {
		time.Sleep(time.Hour) // a sleep, not a select{}: alone, that is a deadlock the runtime ends
		os.Exit(0)
	}
	if venv := os.Getenv("MAKTABA_TEST_APPBUNDLE_VENV_CHILD"); venv != "" {
		// a child that runs from the venv (TestAppBundleRunnersWaitForTheVenv): each start
		// is logged; without the venv's ready marker it dies as python does on a module the
		// venv does not hold yet
		_, err := os.Stat(venv)
		line := "ready"
		if err != nil {
			line = "early"
		}
		if f, ferr := os.OpenFile(os.Getenv("MAKTABA_TEST_APPBUNDLE_STARTS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); ferr == nil {
			fmt.Fprintf(f, "%s %s\n", os.Getenv("MAKTABA_TEST_APPBUNDLE_NAME"), line)
			_ = f.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "python: No module named maktaba_plugin_runner")
			os.Exit(1)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	mode := os.Getenv("MAKTABA_TEST_APPBUNDLE_MIGRATE")
	if mode == "" {
		return
	}
	if strings.HasPrefix(mode, "down") { // a rollback's Down scripts (TestRunMigrateDownTo)
		if strings.Join(os.Args[1:], " ") != "migrate --timeout 0 --lock-timeout "+migrateLockTimeout+" down-to 191" ||
			os.Getenv("MAKTABA_DISABLE_DOWN") != "" {
			fmt.Fprintf(os.Stderr, "unexpected %q (MAKTABA_DISABLE_DOWN=%q)\n", os.Args[1:], os.Getenv("MAKTABA_DISABLE_DOWN"))
			os.Exit(2)
		}
		if mode == "down-fail" {
			fmt.Fprintln(os.Stderr, "migrate: ERROR 0192_smart_playlists.sql: failed to run SQL migration\nlock timeout")
			os.Exit(1)
		}
		fmt.Println("migrate: OK 0192_smart_playlists.sql (down)")
		os.Exit(0)
	}
	if strings.Join(os.Args[1:], " ") != "migrate --timeout 0 --lock-timeout "+migrateLockTimeout+" up" {
		fmt.Fprintf(os.Stderr, "unexpected %q\n", os.Args[1:])
		os.Exit(2)
	}
	switch mode {
	case "ok":
		// the library is stopped: the index builds run plainly (api/migrate_offline.go)
		fmt.Printf("migrate: applying migrations (offline %s)\n", os.Getenv(envMigrateOffline))
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "migrate: applying migrations\n2026/10/21 OK   0143_queue_health.sql\n"+
			"migrate: ERROR 0144_x.sql: failed to run SQL migration\nrelation \"videos\" does not exist")
		os.Exit(1)
	case "ahead":
		fmt.Fprintln(os.Stderr, "migrate: the database's schema is at 0150 and these migrations end at 0143")
		os.Exit(apiExitSchemaAhead)
	}
	os.Exit(2)
}

func writePayload(t *testing.T, dir, version, pgVersion string) {
	t.Helper()
	body := `{"schema":1,"version":"` + version + `","postgres":{"version":"` + pgVersion + `"},` +
		`"python":{"version":"3.12.15"},"pipeline":{"wheel":"w.whl","sha256":"aa","constraints_sha256":"bb"}}`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// appEnv is an install whose state is temporary, with a payload of version v
// and (when confirmed is set) a payload of that version saved as the one that ran.
func appEnv(t *testing.T, v, confirmed string) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Server.DataDir = filepath.Join(dir, "state")
	cfg.Pipeline.Python = ""
	t.Setenv("MAKTABA_PYTHON", "")
	t.Setenv("MAKTABA_LOG_DIR", filepath.Join(dir, "logs"))
	payload := filepath.Join(dir, "payload")
	writePayload(t, payload, v, "16.15.0")
	if confirmed != "" {
		old := filepath.Join(dir, "old")
		writePayload(t, old, confirmed, "16.15.0")
		if err := apppayload.Confirm(cfg.StateDir(), old); err != nil {
			t.Fatal(err)
		}
	}
	return cfg, payload
}

// newPayload replaces the install's payload (an update, or the previous app
// installed again).
func newPayload(t *testing.T, payload, v string) {
	t.Helper()
	writePayload(t, payload, v, "16.15.0")
}

func record(t *testing.T, cfg config.Config, rec apppayload.Database) {
	t.Helper()
	if err := apppayload.WriteDatabase(cfg.StateDir(), rec); err != nil {
		t.Fatal(err)
	}
}

func recorded(t *testing.T, cfg config.Config) apppayload.Database {
	t.Helper()
	rec, ok, err := apppayload.ReadDatabase(cfg.StateDir())
	if err != nil || !ok {
		t.Fatalf("no database record: %v %v", ok, err)
	}
	return rec
}

func testBundle(cfg config.Config) *pgbundle.Bundle {
	return &pgbundle.Bundle{Backups: filepath.Join(cfg.StateDir(), "backups"), Data: filepath.Join(cfg.StateDir(), "postgres")}
}

func gateProcs(pg bool) []supervisor.Process {
	ps := []supervisor.Process{{Role: supervisor.RoleAPI, Cmd: exec.Command("maktaba-api")}, {Role: supervisor.RolePipeline},
		{Role: supervisor.RolePipeline, Instance: "stt-1"}, {Role: supervisor.RoleStreaming},
		{Role: supervisor.RoleTVSlot, Instance: "blue"}, {Role: supervisor.RoleOllama}}
	if pg {
		ps = append([]supervisor.Process{{Role: supervisor.RolePostgres}}, ps...)
	}
	return ps
}

func prepareOf(t *testing.T, ps []supervisor.Process, role supervisor.Role) step {
	for _, p := range ps {
		if p.Role == role {
			return checkedStep(t, p.Prepare)
		}
	}
	return nil
}

// checkedStep is s, checking that every detail it shows, and its error (but a
// stop's), carries a code of detailCodes with exactly that code's params.
func checkedStep(t *testing.T, s step) step {
	if s == nil {
		return nil
	}
	return func(ctx context.Context, report func(supervisor.Detail)) error {
		err := s(ctx, func(d supervisor.Detail) {
			checkDetail(t, d)
			report(d)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			checkDetail(t, supervisor.DetailOf(err))
		}
		return err
	}
}

func checkDetail(t *testing.T, d supervisor.Detail) {
	t.Helper()
	names, ok := detailCodes[d.Code]
	if !ok {
		t.Errorf("the detail %q has no code of the list (code %q)", d.Text, d.Code)
		return
	}
	if strings.TrimSpace(d.Text) == "" {
		t.Errorf("%s: no English", d.Code)
	}
	if len(d.Params) != len(names) {
		t.Errorf("%s: params %v, want exactly %v", d.Code, d.Params, names)
	}
	for _, n := range names {
		if _, ok := d.Params[n]; !ok {
			t.Errorf("%s: no %q in %v", d.Code, n, d.Params)
		}
	}
}

func noReport(supervisor.Detail) {}

// fakeDB stands in for the database step's programs: the dump, the api's
// migrations and putting a dump back. It notes what the record said as each ran.
type fakeDB struct {
	mu         sync.Mutex
	cfg        config.Config
	dumps      []string // from->to
	keep       int
	migrates   []string // the record's state as each began
	restores   []string // the dumps put back
	asides     []string // the failed states set aside
	migrateErr error
	restoreErr error
	block      chan struct{} // when set, the migrations wait on it (or the context)
	// pending is how many of the payload's migrations the database lacks, when known
	// (not known: the update dumps as it always did)
	pending int
	known   bool
	// ahead: the database's schema is newer than the payload's (the api refuses it unless
	// MAKTABA_SCHEMA_AHEAD_OK)
	ahead bool
	// connects: the record's state each time the workers' CONNECT was checked
	connects []string
	// back is the way back a failed update's migrations take (updater.FailedBack; "":
	// restore), backSchema the Down scripts' slot; applied the database's newest migration
	back       string
	backSchema int64
	before     int64 // the schema's version before any migration ran (0: not readable)
	applied    int64
	backArgs   []string // the release before's folder, applied, on a kept schema, as each was decided
	downs      []string // the record's state and the slot as each run of the Down scripts began
	downErr    error
	undos      []string // the record's state as each undo of the migration that failed part way began
	undoErr    error
	befores    []int64     // the slot before the update, as each was decided
	askedAt    []time.Time // the times the slot the database itself recorded was asked for
	drops      []string    // the record's state as each drop of the invalid leftovers began
	// copies is what was paused and resumed, with the record's state then, in order; paused
	// whether they are now
	copies    []string
	paused    bool
	pauseErr  error
	resumeErr error
	// the database's identity and schema (or that it does not answer), the steps a restore
	// takes before it ends, and what it does then
	id        string
	schema    int64
	idErr     error
	steps     []string
	onRestore func(ctx context.Context) error
	// onMigrate runs as the migrations start (the server stopped then, say)
	onMigrate func()
}

func installFakeDB(t *testing.T, cfg config.Config) *fakeDB {
	t.Helper()
	f := &fakeDB{cfg: cfg}
	prevD, prevR, prevM, prevA, prevP := dumpBeforeUpdate, restoreDump, migrateUp, dumpAsideBundled, pendingMigrations
	prevG := grantWorkersConnect
	prevFB, prevSV, prevDI, prevMD, prevEB := failedBack, schemaVersionOf, dropInvalidIndexes, runMigrateDownTo, endBlockersBundled
	prevCP, prevPC, prevRC := copiesPausedBundled, pauseCopiesBundled, resumeCopiesBundled
	prevUF, prevAt, prevID := runMigrateUndoFailed, schemaVersionAt, dataIdentity
	t.Cleanup(func() {
		dataIdentity = prevID
		schemaVersionAt = prevAt
		copiesPausedBundled, pauseCopiesBundled, resumeCopiesBundled = prevCP, prevPC, prevRC
		dumpBeforeUpdate, restoreDump, migrateUp, dumpAsideBundled, pendingMigrations = prevD, prevR, prevM, prevA, prevP
		grantWorkersConnect = prevG
		failedBack, schemaVersionOf, dropInvalidIndexes, runMigrateDownTo, endBlockersBundled = prevFB, prevSV, prevDI, prevMD, prevEB
		runMigrateUndoFailed = prevUF
	})
	copiesPausedBundled = func(pgbundle.Bundle) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.paused
	}
	pauseCopiesBundled = func(context.Context, pgbundle.Bundle) (int, error) {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.copies = append(f.copies, "pause "+rec.State)
		if f.pauseErr != nil {
			return 0, f.pauseErr
		}
		f.paused = true
		return 2, nil
	}
	resumeCopiesBundled = func(context.Context, pgbundle.Bundle) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.copies = append(f.copies, "resume "+rec.State)
		if f.resumeErr == nil {
			f.paused = false
		}
		return f.resumeErr
	}
	failedBack = func(from, _ string, applied, before int64, onKept bool) (string, int64, string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.backArgs = append(f.backArgs, fmt.Sprintf("%s %d %v", filepath.Base(filepath.Dir(from)), applied, onKept))
		f.befores = append(f.befores, before)
		switch f.back {
		case updater.FailedKeep:
			return f.back, 0, "every migration since it is additive"
		case updater.FailedDown:
			return f.back, f.backSchema, "their Down scripts undo them"
		case updater.FailedRestore:
			return f.back, 0, "the slot the database was at before the update is not known"
		}
		return updater.FailedLossy, 0, "0232_x.sql loses data (ALTER TABLE t DROP COLUMN c)"
	}
	runMigrateUndoFailed = func(context.Context, *exec.Cmd, io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.undos = append(f.undos, rec.State)
		return f.undoErr
	}
	schemaVersionOf = func(context.Context, *migrationGate) (int64, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.migrates) == 0 {
			return f.before, nil
		}
		return f.applied, nil
	}
	schemaVersionAt = func(_ context.Context, _ *migrationGate, at time.Time) (int64, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.askedAt = append(f.askedAt, at)
		return f.before, nil
	}
	dropInvalidIndexes = func(context.Context, *exec.Cmd, io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.drops = append(f.drops, rec.State)
		return nil
	}
	runMigrateDownTo = func(_ context.Context, _ *exec.Cmd, schema int64, _ io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.downs = append(f.downs, fmt.Sprintf("%s %d", rec.State, schema))
		f.copies = append(f.copies, "down")
		return f.downErr
	}
	endBlockersBundled = func(context.Context, pgbundle.Bundle, time.Duration) ([]pgbundle.EndedSession, error) {
		return nil, nil
	}
	grantWorkersConnect = func(context.Context, *migrationGate) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		f.connects = append(f.connects, rec.State)
		return nil
	}
	pendingMigrations = func(context.Context, *migrationGate) (int, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.pending, f.known
	}
	dumpAsideBundled = func(_ context.Context, _ pgbundle.Bundle, path string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asides = append(f.asides, filepath.Base(path))
		return os.WriteFile(path, []byte("PGDMP"), 0o600)
	}
	dumpBeforeUpdate = func(_ context.Context, b pgbundle.Bundle, from, to string, keep int) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.dumps = append(f.dumps, orUnknown(from)+"->"+to)
		f.keep = keep
		return filepath.Join(b.Backups, pgbundle.UpdateDumpName(from, to)), nil
	}
	migrateUp = func(ctx context.Context, api *exec.Cmd, _ io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		f.migrates = append(f.migrates, rec.State)
		block, err, on := f.block, f.migrateErr, f.onMigrate
		if f.ahead && (api == nil || !contains(api.Env, envSchemaAheadOK+"=1")) {
			// the database holds a newer schema than this release's, which its api refuses
			// unless told it is kept for it
			err = errSchemaAhead
		}
		f.mu.Unlock()
		if on != nil {
			on()
			return ctx.Err()
		}
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
	f.id, f.schema = "1/2/3", 149
	dataIdentity = func(context.Context, *migrationGate) (string, int64, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.id, f.schema, f.idErr
	}
	restoreDump = func(ctx context.Context, _ pgbundle.Bundle, dump string, before func(string) error, _ io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, step := range f.steps {
			// told before it is done
			f.mu.Unlock()
			err := before(step)
			f.mu.Lock()
			if err != nil {
				return err
			}
			if step == pgbundle.StepGone {
				f.id, f.schema = "", 0
			} else {
				f.id = "1/4/3"
			}
		}
		if f.onRestore != nil {
			f.mu.Unlock()
			err := f.onRestore(ctx)
			f.mu.Lock()
			if err != nil {
				return err
			}
		}
		if rec.State != apppayload.DatabaseRestoring {
			return fmt.Errorf("the record says %q while the dump is put back", rec.State)
		}
		f.restores = append(f.restores, filepath.Base(dump))
		return f.restoreErr
	}
	return f
}

func (f *fakeDB) counts() (dumps, migrates, restores int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dumps), len(f.migrates), len(f.restores)
}

// Every process that uses the library database waits on the database step;
// postgres on its major check alone, the model server on nothing; the pipeline
// on the database, then the venv.
func TestAppBundleGatesEveryRoleOnTheDatabase(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	for _, p := range ps {
		if (p.Role == supervisor.RoleOllama) != (p.Prepare == nil) {
			t.Errorf("%s: prepare = %v", p.Name(), p.Prepare != nil)
		}
	}
	ps = appBundleGates(cfg, payload, nil, gateProcs(true), io.Discard)
	if prepareOf(t, ps, supervisor.RolePostgres) != nil {
		t.Error("no bundled Postgres, no major check")
	}
}

// An update: one dump named after the two versions, recorded as migrating before
// the migrations run once for every process, then recorded as migrated; every
// waiting process shows the step's progress.
func TestAppBundleUpdateDumpsThenMigrates(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	cfg.Postgres.UpdateDumps = 5
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.block = make(chan struct{})
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)

	var mu sync.Mutex
	details, codes := map[string][]string{}, map[string][]string{}
	var wg sync.WaitGroup
	for _, p := range ps {
		if p.Role == supervisor.RolePostgres || p.Prepare == nil || p.Role == supervisor.RolePipeline {
			continue
		}
		wg.Add(1)
		go func(p supervisor.Process) {
			defer wg.Done()
			err := checkedStep(t, p.Prepare)(context.Background(), func(d supervisor.Detail) {
				mu.Lock()
				details[p.Name()] = append(details[p.Name()], d.Text)
				codes[p.Name()] = append(codes[p.Name()], d.Code)
				mu.Unlock()
			})
			if err != nil {
				t.Error(err)
			}
		}(p)
	}
	waitUntil(t, "the migrations run", func() bool { _, m, _ := f.counts(); return m == 1 })
	time.Sleep(20 * time.Millisecond) // the other processes come to wait
	close(f.block)
	wg.Wait()

	if len(f.dumps) != 1 || f.dumps[0] != "v0.2.0->v0.3.0" || f.keep != 5 {
		t.Fatalf("dumps = %v keep %d (one, named after the update, keeping [postgres] update_dumps)", f.dumps, f.keep)
	}
	if len(f.migrates) != 1 || f.migrates[0] != apppayload.DatabaseMigrating {
		t.Fatalf("migrations = %v (once, after the record said migrating)", f.migrates)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.3.0" || rec.State != apppayload.DatabaseMigrated || rec.From != "v0.2.0" ||
		filepath.Base(rec.Dump) != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("record = %+v", rec)
	}
	for _, name := range []string{"api", "streaming", "tv-slot/blue"} {
		if !contains(details[name], "Updating the library database for v0.3.0") || !contains(codes[name], "db_migrating") {
			t.Errorf("%s showed %v %v", name, details[name], codes[name])
		}
	}
	// done for this run: nothing runs again
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, m, _ := f.counts(); d != 1 || m != 1 {
		t.Fatalf("ran again: %d dumps, %d migrations", d, m)
	}
}

// No record (an install from before it, e.g. an Epic 36 Mac): its first start
// here dumps as pre-unknown-to-<new>; a payload saved as the one that ran
// stands in for the record.
func TestAppBundleNoRecordStillDumps(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if len(f.dumps) != 1 || f.dumps[0] != "unknown->v0.3.0" {
		t.Fatalf("dumps = %v", f.dumps)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.3.0" || rec.State != apppayload.DatabaseMigrated || rec.From != "" {
		t.Fatalf("record = %+v", rec)
	}

	cfg, payload = appEnv(t, "v0.3.0", "v0.2.0")
	f = installFakeDB(t, cfg)
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if len(f.dumps) != 1 || f.dumps[0] != "v0.2.0->v0.3.0" {
		t.Fatalf("dumps = %v (from the payload that ran)", f.dumps)
	}
}

// The same release: no dump; the migrations still run (a check, at once); their
// failure is tried again (nothing to put back), not a halt.
func TestAppBundleSameReleaseNoDump(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.migrateErr = errors.New("ping db: connection refused")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if err == nil || supervisor.Halted(err) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("a failed check = %v (tried again, not a halt)", err)
	}
	f.migrateErr = nil
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, m, r := f.counts(); d != 0 || m != 2 || r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}
}

// The case of the review: v0.3.0 migrated the database but never ran three
// healthy minutes (the payload saved as the one that ran is still v0.2.0);
// v0.2.0 installed again is refused for every process, nothing is dumped or
// migrated.
func TestAppBundleRefusesAReleaseOlderThanTheDatabase(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "v0.2.0")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated, From: "v0.2.0",
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump"})
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	for _, role := range []supervisor.Role{supervisor.RoleAPI, supervisor.RoleStreaming, supervisor.RolePipeline} {
		err := prepareOf(t, ps, role)(context.Background(), noReport)
		if !supervisor.Halted(err) || !strings.Contains(err.Error(), "older than the one whose changes the library database holds (v0.3.0)") ||
			!strings.Contains(err.Error(), "pre-v0.2.0-to-v0.3.0.dump") {
			t.Fatalf("%s: %v", role, err)
		}
	}
	if d, m, r := f.counts(); d+m+r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}
	// a development build's order is not known: it goes on (its migrations would
	// refuse a newer database)
	newPayload(t, payload, "v0.2.0-5-gdeadbee")
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
}

// No automatic restore, ever: an update whose migrations failed, and whose way back
// cannot be told (here: the release before's folder is not kept), stops with nothing put
// back. The record is held (the failed release's version, so every older release refuses
// it), every role halts with the choices, and an older start stops on it too. A person
// chooses: a restore they ask for puts the backup back and the release before runs; or a
// release newer than the one that failed goes on from where it stopped.
func TestAppBundleFailedMigrationStopsAndAsks(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.back = updater.FailedRestore
	f.migrateErr = errors.New(`ERROR 0144_x.sql: relation "videos" does not exist`)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("a failed update = %v, want a db_held halt", err)
	}
	for _, want := range []string{"the update to v0.3.0 stopped", `relation "videos"`, "db_held_unknown",
		"Nothing was put back by itself", "pre-v0.2.0-to-v0.3.0.dump", "/recovery", "maktaba-server restore",
		"brings v0.2.0 back", "newer than v0.3.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("detail %q lacks %q", err, want)
		}
	}
	if len(f.restores) != 0 {
		t.Fatalf("a dump was put back by itself: %v", f.restores)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.3.0" || rec.State != apppayload.DatabaseHeld || rec.Failed != "v0.3.0" ||
		rec.Held != "db_held_unknown" || rec.From != "v0.2.0" || filepath.Base(rec.Dump) != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("record = %+v", rec)
	}
	if got := attempts.Read(updatesDirOf(cfg, "")); len(got) == 0 || got[0].Code != "db_held_unknown" ||
		got[0].Outcome != attempts.Failed {
		t.Fatalf("attempts %+v, want db_held_unknown, failed", got)
	}
	// the other processes halt with it, nothing runs again in this run
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); !supervisor.Halted(err) {
		t.Fatalf("streaming = %v", err)
	}
	if d, m, r := f.counts(); d != 1 || m != 1 || r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}

	// started again, or the release before after its switch back: each stops on it
	for _, v := range []string{"v0.3.0", "v0.2.0"} {
		newPayload(t, payload, v)
		ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
		err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
		if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
			t.Fatalf("%s on a held database = %v, want db_held", v, err)
		}
	}
	if d, m, r := f.counts(); d != 1 || m != 1 || r != 0 {
		t.Fatalf("after starts on the held database: %d dumps, %d migrations, %d restores", d, m, r)
	}

	// a person puts the backup back (the recovery page, `maktaba-server restore`): the
	// release before runs on it
	held := recorded(t, cfg)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: held.Dump, By: "admin"})
	f.migrateErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the release before after the restore asked: %v", err)
	}
	if len(f.restores) != 1 || f.restores[0] != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("restores = %v", f.restores)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v", rec)
	}
}

// The other choice: a release newer than the one that stopped, installed by a person,
// goes on from the held database (its migrations run what is missing), with the backup
// taken before the held update kept as its way back.
func TestAppBundleNewerReleaseGoesOnFromAHeldUpdate(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.1", "")
	dump := filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseHeld, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "boom", Held: "db_down_failed", SchemaBefore: 230})
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("a newer release on a held database: %v", err)
	}
	if d, m, r := f.counts(); d != 0 || m != 1 || r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores: want the held update's backup kept, one migration", d, m, r)
	}
	if len(f.migrates) != 1 || f.migrates[0] != apppayload.DatabaseMigrating {
		t.Fatalf("migrated on a record %v", f.migrates)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.3.1" || rec.State != apppayload.DatabaseMigrated ||
		rec.Dump != dump || rec.From != "v0.2.0" || rec.SchemaBefore != 230 {
		t.Fatalf("record = %+v, want the held update's backup, release before and slot", rec)
	}
}

// Only a release newer than the one that failed goes on: the same one again, an older one, a
// pre-release of it and a build whose order cannot be told stop on the held record, and
// change nothing. A release newer than it that fails too is held with the first backup and
// slot, twice in a row.
func TestAppBundleOnlyANewerReleaseGoesOnFromAHeldUpdate(t *testing.T) {
	heldRec := func(cfg config.Config) apppayload.Database {
		return apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseHeld, From: "v0.2.0",
			Dump:   filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump"),
			Failed: "v0.3.0", Error: "boom", Held: "db_held_lossy", SchemaBefore: 230}
	}
	for _, cur := range []string{"v0.3.0", "v0.2.0", "v0.2.9", "v0.3.0-rc.1", "dev"} {
		cfg, payload := appEnv(t, cur, "")
		record(t, cfg, heldRec(cfg))
		f := installFakeDB(t, cfg)
		ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
		err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
		if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
			t.Errorf("%s on a held record = %v, want db_held", cur, err)
		}
		if d, m, r := f.counts(); d+m+r != 0 {
			t.Errorf("%s: %d dumps, %d migrations, %d restores", cur, d, m, r)
		}
		if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.SchemaBefore != 230 || rec.Dump == "" {
			t.Errorf("%s: record %+v", cur, rec)
		}
	}
	cfg, payload := appEnv(t, "v0.3.1", "")
	record(t, cfg, heldRec(cfg))
	f := installFakeDB(t, cfg)
	f.back, f.migrateErr = updater.FailedLossy, errors.New("migrate: ERROR 0240_x.sql: boom")
	for _, cur := range []string{"v0.3.1", "v0.3.2"} {
		newPayload(t, payload, cur)
		ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
		if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) {
			t.Fatalf("%s: %v", cur, err)
		}
		rec := recorded(t, cfg)
		if rec.State != apppayload.DatabaseHeld || rec.Failed != cur || rec.SchemaBefore != 230 || rec.From != "v0.2.0" ||
			filepath.Base(rec.Dump) != "pre-v0.2.0-to-v0.3.0.dump" {
			t.Fatalf("after %s failed: %+v", cur, rec)
		}
	}
	if d, _, r := f.counts(); d != 0 || r != 0 {
		t.Fatalf("%d dumps, %d restores", d, r)
	}
}

// A restore mark no person asked for (a release from before 2026-11-24, or its launcher's
// guard, marked the record restoring): nothing is put back by itself, the update is held.
func TestAppBundleUnaskedRestoreMarkIsHeld(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "")
	dump := filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "launcher_boot_loop"})
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("= %v, want db_held", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatalf("%d restores", r)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Held != "db_held_unasked" ||
		rec.Failed != "v0.3.0" {
		t.Fatalf("record = %+v", rec)
	}
}

// The outage of 2026-11-23: an update's migration failed part way (a CREATE INDEX
// CONCURRENTLY cancelled by its lock timeout) and the whole dump went back, 2 h 44 min.
// When every migration since the release before is additive and that release runs on a
// newer schema, nothing is put back: the record keeps the database for it (version the
// failed release's, kept_for and release the one before), the invalid indexes the build
// left are dropped first, and the halt says so. The release before starts on it; the
// failed release started again is an update again, from a new dump.
func TestAppBundleFailedMigrationKeepsAnAdditiveSchema(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	updates := updatesDirOf(cfg, "")
	prevDir := filepath.Join(updater.KeptPayloadDir(updates, "v0.2.0"), "migrations")
	if err := os.MkdirAll(prevDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.back, f.applied = updater.FailedKeep, 234
	f.migrateErr = errors.New(`migrate: ERROR 0235_search_terms.sql: pq: canceling statement due to lock timeout (55P03)`)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_kept" {
		t.Fatalf("a failed additive update = %v, want the db_kept halt", err)
	}
	checkDetail(t, supervisor.DetailOf(err))
	for _, want := range []string{"the update to v0.3.0 could not change the library database", "lock timeout",
		"Nothing had to be put back", "v0.2.0 runs on the database as it is", "install v0.2.0 again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("detail %q lacks %q", err, want)
		}
	}
	if len(f.restores) != 0 || len(f.downs) != 0 {
		t.Fatalf("restores %v, Down scripts %v: nothing is put back", f.restores, f.downs)
	}
	if len(f.backArgs) != 1 || f.backArgs[0] != "0.2.0 234 false" {
		t.Fatalf("decided on %v, want the kept payload of v0.2.0 at slot 234", f.backArgs)
	}
	if len(f.drops) != 1 || f.drops[0] != apppayload.DatabaseMigrating {
		t.Fatalf("the invalid leftovers dropped %v, want once while the record says migrating", f.drops)
	}
	// what 0235 made part way is undone by its own Down script, even on a kept schema
	if len(f.undos) != 1 || f.undos[0] != apppayload.DatabaseMigrating {
		t.Fatalf("the failed migration undone %v, want once while the record says migrating", f.undos)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.3.0" || rec.State != apppayload.DatabaseMigrated || rec.KeptFor != "0.2.0" ||
		rec.Release != "v0.2.0" || rec.From != "v0.2.0" || rec.Dump == "" || !strings.Contains(rec.KeptReason, "lock timeout") {
		t.Fatalf("record = %+v", rec)
	}
	got := attempts.Read(updates)
	if len(got) != 1 || got[0].Outcome != attempts.PutBack || got[0].Code != "db_migrate_failed" ||
		!strings.Contains(got[0].Detail, "kept") {
		t.Fatalf("attempts %+v", got)
	}
	// the update's rollback keeps it too (updater.Machine.keepSchema marks it again), and the
	// failed release's halt holds through it: the record is not written again (a change ends
	// the halt, and this release would start updating again under the rollback)
	if ok, err := layout.MarkRecordKept(apppayload.DatabasePath(cfg.StateDir()), "v0.3.0", "v0.2.0", "logs"); err != nil || !ok {
		t.Fatalf("the rollback's mark = %v, %v", ok, err)
	}
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) ||
		supervisor.DetailOf(err).Code != "db_kept" {
		t.Fatalf("after the rollback's mark = %v, want the db_kept halt still", err)
	}
	if len(f.dumps) != 1 || len(f.migrates) != migrateTries { // the lock timeout tried three times
		t.Fatalf("dumps %v, migrations %v after the rollback's mark", f.dumps, f.migrates)
	}
	// the release before starts on it: nothing dumped or put back, its api told
	newPayload(t, payload, "v0.2.0")
	f.migrateErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the release before on the kept schema = %v", err)
	}
	if len(f.dumps) != 1 || len(f.restores) != 0 {
		t.Fatalf("dumps %v, restores %v: the release before dumped or put back", f.dumps, f.restores)
	}
	// the failed release again: an update again, from a new dump
	newPayload(t, payload, "v0.3.0")
	f.pending, f.known = 1, true
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("v0.3.0 again = %v", err)
	}
	if len(f.dumps) != 2 || f.dumps[1] != "v0.2.0->v0.3.0" {
		t.Fatalf("dumps = %v, want a new one from v0.2.0", f.dumps)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.3.0" || rec.State != apppayload.DatabaseMigrated || rec.KeptFor != "" {
		t.Fatalf("record after the update again = %+v", rec)
	}
}

// As above, when the schema cannot be kept but the migrations that ran lose no data: their
// Down scripts undo them (back to the release before's newest slot), the sessions they
// wait on ended as for the migrations; the record is the release before's, down_from the
// failed release; no dump goes back. Their failure puts the dump back after all.
func TestAppBundleFailedMigrationUndoneByItsDownScripts(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	prevDir := filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations")
	if err := os.MkdirAll(prevDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.back, f.backSchema, f.before, f.applied = updater.FailedDown, 230, 230, 234
	f.migrateErr = errors.New(`migrate: ERROR 0235_search_terms.sql: pq: canceling statement due to lock timeout (55P03)`)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	// the slot before the update is recorded, and decides the way back
	if len(f.befores) != 1 || f.befores[0] != 230 {
		t.Fatalf("decided with the slot before %v, want 230", f.befores)
	}
	// 0235, which failed part way, is undone by its own Down script before the others
	if len(f.undos) != 1 || f.undos[0] != apppayload.DatabaseMigrating {
		t.Fatalf("the failed migration undone %v", f.undos)
	}
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_undone" {
		t.Fatalf("a failed lossless update = %v, want the db_undone halt", err)
	}
	checkDetail(t, supervisor.DetailOf(err))
	for _, want := range []string{"undone by its own Down scripts", "nothing had to be put back from the backup",
		"as v0.2.0 knows it", "install v0.2.0 again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("detail %q lacks %q", err, want)
		}
	}
	if len(f.restores) != 0 || len(f.downs) != 1 || f.downs[0] != "migrating 230" {
		t.Fatalf("restores %v, Down scripts %v", f.restores, f.downs)
	}
	if len(f.drops) != 1 {
		t.Fatalf("the invalid leftovers dropped %d times", len(f.drops))
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseMigrated || rec.DownFrom != "v0.3.0" || rec.KeptFor != "" {
		t.Fatalf("record = %+v", rec)
	}
	// the release before starts on its own schema: nothing dumped, nothing put back
	newPayload(t, payload, "v0.2.0")
	f.migrateErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the release before = %v", err)
	}
	if len(f.dumps) != 1 || len(f.restores) != 0 {
		t.Fatalf("dumps %v, restores %v: the release before dumped or put back", f.dumps, f.restores)
	}

	// the Down scripts fail: the update stops and asks, nothing is put back by itself
	newPayload(t, payload, "v0.3.0")
	f.migrateErr = errors.New("migrate: ERROR 0235_search_terms.sql: boom")
	f.downErr = errors.New("migrate: ERROR 0234: cannot drop")
	f.pending, f.known = 1, true
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err = prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("Down scripts that fail = %v, want db_held", err)
	}
	if len(f.restores) != 0 {
		t.Fatalf("restores = %v", f.restores)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Held != "db_down_failed" {
		t.Fatalf("record = %+v", rec)
	}
	// the Updates page says the Down scripts failed
	if got := attempts.Read(updatesDirOf(cfg, "")); len(got) == 0 || got[0].Code != "db_down_failed" {
		t.Fatalf("attempts %+v, want db_down_failed", got)
	}
	// a person put it right (the database is the release before's again); the undo of the
	// one that failed part way fails this time: held too, said the same
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f.downErr, f.undoErr = nil, errors.New("migrate: the Down script of 0235_search_terms.sql: boom")
	downs := len(f.downs)
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err = prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" || len(f.restores) != 0 ||
		len(f.downs) != downs {
		t.Fatalf("an undo that fails = %v, restores %v, downs %v", err, f.restores, f.downs)
	}
	if got := attempts.Read(updatesDirOf(cfg, "")); len(got) == 0 || got[0].Code != "db_down_failed" {
		t.Fatalf("attempts %+v, want db_down_failed", got)
	}
}

// The review of #1538: the update's watch gives up on the start while the Down scripts run
// and marks the record (its rollback: held now; restoring by a release before 2026-11-24):
// the database step saves nothing over that mark, and halts on it.
func TestAppBundleFailedMigrationLeavesAWatchsRestoreMark(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.back, f.backSchema, f.applied = updater.FailedDown, 230, 234
	f.migrateErr = errors.New("migrate: ERROR 0235_x.sql: boom")
	runMigrateDownTo = func(context.Context, *exec.Cmd, int64, io.Writer) error {
		if ok, err := layout.MarkRecordHeld(apppayload.DatabasePath(cfg.StateDir()), "v0.3.0", "start_timeout"); err != nil || !ok {
			t.Errorf("the watch's mark = %v, %v", ok, err)
		}
		return nil
	}
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("= %v, want db_held", err)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Failed != "v0.3.0" || len(f.restores) != 0 {
		t.Fatalf("the watch's mark was saved over, or put back: %+v %v", rec, f.restores)
	}
}

// A failed update whose migrations cannot be kept or undone stops and asks (nothing is put
// back by itself); one whose database was a newer release's kept schema is told so (its
// Down scripts are not this release's).
func TestAppBundleFailedMigrationHeldWhenNeitherIsSafe(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.2.5", State: apppayload.DatabaseMigrated, Release: "v0.2.0",
		KeptFor: "0.2.0"})
	f := installFakeDB(t, cfg)
	f.applied = 231
	f.migrateErr = errors.New("migrate: ERROR 0232_x.sql: boom")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" || len(f.restores) != 0 {
		t.Fatalf("= %v (restores %v), want db_held, nothing put back", err, f.restores)
	}
	if len(f.backArgs) != 1 || f.backArgs[0] != "0.2.0 231 true" {
		t.Fatalf("decided on %v", f.backArgs)
	}
	if len(f.drops) != 0 || len(f.downs) != 0 {
		t.Fatalf("drops %v, downs %v on a held update", f.drops, f.downs)
	}
	// a migration that ran lost data its Down script cannot give back: the Updates page says so
	if got := attempts.Read(updatesDirOf(cfg, "")); len(got) == 0 || got[0].Code != "db_held_lossy" {
		t.Fatalf("attempts %+v, want db_held_lossy", got)
	}
}

// The outages of 2026-11-14 and 2026-11-23: while the migrations run, whatever one of them
// waits on longer than the grace is ended, and the log says whom.
func TestAppBundleMigrationsEndWhatTheyWaitOn(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	prevEvery, prevGrace := migrationBlockerEvery, migrationBlockerGrace
	t.Cleanup(func() { migrationBlockerEvery, migrationBlockerGrace = prevEvery, prevGrace })
	migrationBlockerEvery, migrationBlockerGrace = 5*time.Millisecond, 3*time.Second
	var mu sync.Mutex
	asked := 0
	endBlockersBundled = func(_ context.Context, _ pgbundle.Bundle, grace time.Duration) ([]pgbundle.EndedSession, error) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if grace != 3*time.Second {
			t.Errorf("grace %s", grace)
		}
		if asked == 2 {
			return []pgbundle.EndedSession{{PID: 66198, Kind: "walsender", Role: "maktaba_rep_1e94",
				App: "maktaba_copy_1e94", Client: "192.168.50.94", Query: "COPY public.transcript_segments TO STDOUT"}}, nil
		}
		return nil, nil
	}
	f.block = make(chan struct{})
	go func() {
		for {
			mu.Lock()
			n := asked
			mu.Unlock()
			if n >= 3 {
				close(f.block)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	var log bytes.Buffer
	var logMu sync.Mutex
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), lockedWriter{&logMu, &log})
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	logMu.Lock()
	out := log.String()
	logMu.Unlock()
	if !strings.Contains(out, "a migration waited longer than 3s on walsender 66198 (maktaba_rep_1e94, maktaba_copy_1e94, "+
		"from 192.168.50.94): COPY public.transcript_segments TO STDOUT: ended it") {
		t.Fatalf("the log does not say whom it ended:\n%s", out)
	}
	// stopped with the migrations: no more asking
	mu.Lock()
	n := asked
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if asked != n {
		t.Fatalf("still asked after the migrations ended (%d, then %d)", n, asked)
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Putting back a backup a person asked for failed: a halt that says so, the record still
// says restoring (by); the next start puts it back first.
func TestAppBundleFailedRestoreIsFinishedNextStart(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "")
	dump := filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, By: "admin", Data: apppayload.DataUntouched, DataID: "1/2/3"})
	f := installFakeDB(t, cfg)
	f.restoreErr = errors.New("pg_restore: disk full")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_restore_backup_failed" ||
		!strings.Contains(err.Error(), "pg_restore: disk full") {
		t.Fatalf("a failed restore = %v", err)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseRestoring || rec.By != "admin" {
		t.Fatalf("record = %+v", rec)
	}
	f.restoreErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the next start = %v", err)
	}
	if d, _, r := f.counts(); d != 0 || r != 2 {
		t.Fatalf("%d dumps, %d restores (the restore again)", d, r)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseMigrated || rec.Version != "v0.2.0" {
		t.Fatalf("record = %+v", rec)
	}
}

// The outage of 2026-10-28: the restore failed, every role halted ("cannot start"), and
// the owner put the database back by hand and set the record restored, which nothing
// read until a full restart. The halt watches the record: once it changes, the same
// step (every role waits on it) runs again, and the roles start. A held update (here a
// restore mark no person asked for) ends the same way.
func TestAppBundleHaltEndsWhenTheRecordIsPutRight(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	_ = os.MkdirAll(backups, 0o700)
	dump := filepath.Join(backups, "pre-v0.2.0-to-v0.3.0.dump")
	_ = os.WriteFile(dump, []byte("dump"), 0o600)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "watch_timeout"})
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	api, streaming := prepareOf(t, ps, supervisor.RoleAPI), prepareOf(t, ps, supervisor.RoleStreaming)
	// a restore mark no person asked for is held (nothing put back by itself)
	err := api(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("the unasked restore mark = %v", err)
	}
	if supervisor.HaltCleared(err) {
		t.Fatal("the halt is over with the record unchanged")
	}
	if err := streaming(context.Background(), noReport); !supervisor.Halted(err) {
		t.Fatalf("another role while the record is unchanged = %v", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatalf("%d restores: a held update puts nothing back", r)
	}
	// a restore a person asks for meanwhile (`maktaba-server restore`: restoring, by) is
	// not a way out: that command restarts the server for it
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, By: "admin"})
	if supervisor.HaltCleared(err) {
		t.Fatal("a restore asked by a person ended the halt")
	}
	// put right by hand, as the owner did: the database back, the record restored
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseRestored, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "watch_timeout"})
	if !supervisor.HaltCleared(err) {
		t.Fatal("the halt does not see the record put right")
	}
	if err := streaming(context.Background(), noReport); err != nil {
		t.Fatalf("a role once the record says restored = %v", err)
	}
	if err := api(context.Background(), noReport); err != nil {
		t.Fatalf("the api once the record says restored = %v", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatalf("%d restores: nothing is put back over a database put right", r)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v", rec)
	}
	// the review of #433: a database put right by hand after a restore of a release before
	// 2026-10-28 lacks the machines' CONNECT: a start that finds it restored gives it back
	f.mu.Lock()
	connects := append([]string(nil), f.connects...)
	f.mu.Unlock()
	if len(connects) == 0 || connects[0] != apppayload.DatabaseRestored {
		t.Fatalf("the workers' CONNECT checked at %v, want at the start on the restored record", connects)
	}
}

// The outage's rollback (2026-10-28) put the whole database back for one additive
// migration. A rollback whose migrations are all rollback-safe marks the record kept for
// the release before (layout.MarkRecordKept, the updater's rollback): that release starts
// on the newer schema with nothing put back, its api told it may (MAKTABA_SCHEMA_AHEAD_OK),
// and the record keeps saying whose schema it is. Updating again finds no migration to run.
func TestAppBundleRollbackKeepsAnAdditiveSchema(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	_ = os.MkdirAll(backups, 0o700)
	dump := filepath.Join(backups, "pre-v0.2.0-to-v0.3.0.dump")
	_ = os.WriteFile(dump, []byte("dump"), 0o600)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated, From: "v0.2.0", Dump: dump})
	if ok, err := layout.MarkRecordKept(apppayload.DatabasePath(cfg.StateDir()), "v0.3.0", "v0.2.0", "watch_timeout"); err != nil || !ok {
		t.Fatalf("kept = %v, %v", ok, err)
	}
	f := installFakeDB(t, cfg)
	f.pending, f.known = 0, false // not told: a kept schema needs no count
	newPayload(t, payload, "v0.2.0")
	start := func() *exec.Cmd {
		t.Helper()
		ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
		if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
			t.Fatalf("the release before on the kept schema = %v", err)
		}
		for _, p := range ps {
			if p.Role == supervisor.RoleAPI {
				return p.Cmd
			}
		}
		return nil
	}
	api := start()
	if d, m, r := f.counts(); d != 0 || r != 0 || m != 1 {
		t.Fatalf("%d dumps, %d migrations (the check), %d restores: nothing is put back", d, m, r)
	}
	if !contains(api.Env, "MAKTABA_SCHEMA_AHEAD_OK=1") {
		t.Fatal("the api is not told the newer schema is kept for it")
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.3.0" || rec.Release != "v0.2.0" || rec.KeptFor != "0.2.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v", rec)
	}
	// started again (a restart): the same, still nothing put back
	if api := start(); !contains(api.Env, "MAKTABA_SCHEMA_AHEAD_OK=1") {
		t.Fatal("a restart lost the kept schema")
	}
	if d, _, r := f.counts(); d != 0 || r != 0 {
		t.Fatalf("%d dumps, %d restores on a restart", d, r)
	}
	// the release a person installs again: its migrations are there already
	f.pending, f.known = 0, true
	newPayload(t, payload, "v0.3.0")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("v0.3.0 again = %v", err)
	}
	// (no migration ran: the schema is the same one, which v0.2.0 still runs on)
	if rec := recorded(t, cfg); rec.Version != "v0.3.0" || rec.Release != "" || rec.KeptFor != "0.2.0" {
		t.Fatalf("record after v0.3.0 again = %+v", rec)
	}
	// a release between the one kept for and the database's (an update after the rollback,
	// older than the release that failed): it starts on the kept schema too, nothing put back
	newPayload(t, payload, "v0.2.5")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated, KeptFor: "0.2.0"})
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("a release between the one kept for and the database's = %v", err)
	}
	if d, _, r := f.counts(); d != 0 || r != 0 {
		t.Fatalf("%d dumps, %d restores for a release between", d, r)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.3.0" || rec.Release != "v0.2.5" || rec.KeptFor != "0.2.0" {
		t.Fatalf("record after v0.2.5 = %+v", rec)
	}
	// a release older than the one kept for is still refused
	newPayload(t, payload, "v0.1.0")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated, KeptFor: "0.2.0"})
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) ||
		supervisor.DetailOf(err).Code != "db_refused_older" {
		t.Fatalf("an older release than the one kept for = %v", err)
	}
}

// A rollback after the new release's migrations ran, when neither keeping its schema nor
// its Down scripts take the database back (the update's watch, or the launcher's guard:
// its deadline, a boot loop), marks the record held: the release before stops on it with
// the choices (a release before 2026-11-24 refuses it as older than the database), and
// nothing is dumped or put back by itself.
func TestAppBundleRollbackHeldStopsTheReleaseBefore(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.41", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	_ = os.MkdirAll(backups, 0o700)
	dump := filepath.Join(backups, "pre-v0.3.41-to-v0.3.42.dump")
	_ = os.WriteFile(dump, []byte("dump"), 0o600)
	record(t, cfg, apppayload.Database{Version: "v0.3.42", State: apppayload.DatabaseMigrated, From: "v0.3.41", Dump: dump})
	f := installFakeDB(t, cfg)
	if marked, err := layout.MarkRecordHeld(apppayload.DatabasePath(cfg.StateDir()), "v0.3.42", "launcher_deadline"); err != nil || !marked {
		t.Fatalf("marked %v %v", marked, err)
	}
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" ||
		!strings.Contains(err.Error(), "launcher_deadline") || !strings.Contains(err.Error(), "pre-v0.3.41-to-v0.3.42.dump") {
		t.Fatalf("0.3.41 on the held database = %v", err)
	}
	if d, m, r := f.counts(); len(f.asides) != 0 || d != 0 || m != 0 || r != 0 {
		t.Fatalf("asides %v, %d dumps, %d migrations, %d restores", f.asides, d, m, r)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Version != "v0.3.42" {
		t.Fatalf("record = %+v", rec)
	}
}

// The review of #433: A (v0.2.0) runs on B's (v0.3.0) schema, kept for it; A updates to C
// (v0.4.0), whose migrations fail and are held, and a person puts C's dump (of B's schema)
// back for A. The restored database is B's schema again, kept for A: A starts on it (its
// api told), never halted with db_ahead, and still does on its next start.
func TestAppBundleRestoreKeepsTheKeptSchemaOfItsDump(t *testing.T) {
	cfg, payload := appEnv(t, "v0.4.0", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	_ = os.MkdirAll(backups, 0o700)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", Release: "v0.2.0", State: apppayload.DatabaseMigrated,
		KeptFor: "0.2.0", KeptReason: "watch_timeout"})
	f := installFakeDB(t, cfg)
	f.pending, f.known = 2, true
	f.migrateErr = errors.New("ERROR 0170_x.sql")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) ||
		supervisor.DetailOf(err).Code != "db_held" {
		t.Fatalf("C's failed migrations = %v", err)
	}
	held := recorded(t, cfg)
	if held.State != apppayload.DatabaseHeld || held.DumpKeptFor != "0.2.0" {
		t.Fatalf("held record = %+v", held)
	}
	// a person puts C's dump back for A (restorer.mark carries whom it was kept for)
	record(t, cfg, apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: held.Dump, By: "admin", DumpKeptFor: held.DumpKeptFor})
	// A again, on the database put back (B's schema)
	f.migrateErr, f.ahead = nil, true
	newPayload(t, payload, "v0.2.0")
	for i := 0; i < 2; i++ {
		ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
		if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
			t.Fatalf("A on the restored, kept schema (start %d) = %v", i+1, err)
		}
		if rec := recorded(t, cfg); rec.KeptFor != "0.2.0" || rec.State != apppayload.DatabaseMigrated {
			t.Fatalf("record after A's start %d = %+v", i+1, rec)
		}
	}
	if _, _, r := f.counts(); r != 1 {
		t.Fatalf("%d restores", r)
	}
}

// An update that adds no migration takes no dump: the database is checked (no migration
// runs) and the record keeps the release whose migrations it holds, with the new one as
// the release that runs. Going back to the release before (its rollback, which the
// launcher's fallback does not mark: there is no dump) is a plain start too: nothing
// dumped, nothing put back, no update attempt.
func TestAppBundleUpdateWithoutMigrationsTakesNoDump(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.1", "")
	t.Setenv(envLauncherRoot, "")
	updates := filepath.Join(cfg.StateDir(), "updates")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.pending, f.known = 0, true
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	d, m, r := f.counts()
	if d != 0 || r != 0 || m != 1 || f.migrates[0] != "migrated" {
		t.Fatalf("%d dumps, migrations %v (the check, the record untouched while it ran), %d restores", d, f.migrates, r)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.3.0" || rec.Release != "v0.3.1" || rec.State != apppayload.DatabaseMigrated || rec.Dump != "" ||
		rec.From != "v0.3.0" {
		t.Fatalf("record = %+v", rec)
	}
	if a, ok := attempts.Find(updates, "v0.3.1"); !ok || a.Outcome != attempts.Installed || a.From != "0.3.0" {
		t.Fatalf("attempt = %+v %v", a, ok)
	}
	// the launcher's fallback finds no dump to put back: the record is not marked
	if marked, err := layout.MarkRecordRestoring(apppayload.DatabasePath(cfg.StateDir()), "v0.3.1", "launcher_pending_timeout"); err != nil || marked {
		t.Fatalf("marked = %v, %v", marked, err)
	}
	// the same release again: nothing to do
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	// v0.3.0 again (the rollback): it knows the database; a plain start
	newPayload(t, payload, "v0.3.0")
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the release before again: %v", err)
	}
	if d, _, r := f.counts(); d != 0 || r != 0 || len(f.asides) != 0 {
		t.Fatalf("%d dumps, %d restores, asides %v", d, r, f.asides)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.3.0" || rec.Release != "" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v", rec)
	}
	if _, ok := attempts.Find(updates, "v0.3.0"); ok {
		t.Fatal("going back recorded as an update to v0.3.0")
	}
	// an older release than the database's migrations is still refused
	newPayload(t, payload, "v0.2.9")
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) ||
		supervisor.DetailOf(err).Code != "db_refused_older" {
		t.Fatalf("an older release = %v", err)
	}
}

// An update that adds migrations takes the dump the release before took for it while it
// served (noted in the record, fresh): no dump of its own, it migrates at once; a stale
// one, or one for another release, is not taken (the start dumps as before).
func TestAppBundleUpdateTakesTheDumpTakenWhileServing(t *testing.T) {
	cfg, payload := appEnv(t, "v0.4.0", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	side := filepath.Join(backups, "pre-0.3.0-to-0.4.0.dump")
	if err := os.MkdirAll(side, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(side, "toc.dat"), []byte("toc"), 0o600)
	recPath := apppayload.DatabasePath(cfg.StateDir())
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
	if err := layout.NoteRecordNext(recPath, "0.3.0", "0.4.0", side, time.Now()); err != nil {
		t.Fatal(err)
	}
	f := installFakeDB(t, cfg)
	f.pending, f.known = 3, true
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, m, _ := f.counts(); d != 0 || m != 1 || f.migrates[0] != apppayload.DatabaseMigrating {
		t.Fatalf("%d dumps, migrations %v", d, f.migrates)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.4.0" || rec.State != apppayload.DatabaseMigrated || rec.Dump != side || rec.Next != "" {
		t.Fatalf("record = %+v (the dump taken while serving is the way back)", rec)
	}
	// the guard of an older launcher leaves it alone (its dump withheld until the watch
	// passed), and a release since holds it with its backup named
	if marked, err := layout.MarkRecordRestoring(recPath, "v0.4.0", "launcher_boot_loop"); err != nil || marked {
		t.Fatalf("an older launcher's guard: marked = %v, %v", marked, err)
	}
	if marked, err := layout.MarkRecordHeld(recPath, "v0.4.0", "launcher_boot_loop"); err != nil || !marked {
		t.Fatalf("held = %v, %v", marked, err)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Dump != side {
		t.Fatalf("held record = %+v", rec)
	}

	// a stale note (an update that did not switch then): the start dumps by itself
	cfg2, payload2 := appEnv(t, "v0.4.0", "")
	record(t, cfg2, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
	if err := layout.NoteRecordNext(apppayload.DatabasePath(cfg2.StateDir()), "0.3.0", "0.4.0", side,
		time.Now().Add(-2*nextDumpFresh)); err != nil {
		t.Fatal(err)
	}
	f2 := installFakeDB(t, cfg2)
	f2.pending, f2.known = 3, true
	ps = appBundleGates(cfg2, payload2, testBundle(cfg2), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := f2.counts(); d != 1 || recorded(t, cfg2).Dump == side {
		t.Fatalf("a stale dump taken: %d dumps, record %+v", d, recorded(t, cfg2))
	}
}

// A start cut off while migrating goes on at the next one with the dump from
// before (none taken of the half-migrated database), and when the migrations fail
// the update is held with that dump as the way back (nothing put back by itself).
func TestAppBundleUnfinishedMigrationGoesOn(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrating, From: "v0.2.0",
		Dump: filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")})
	f := installFakeDB(t, cfg)
	f.migrateErr = errors.New("ERROR 0144_x.sql")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); !supervisor.Halted(err) {
		t.Fatalf("= %v", err)
	}
	if d, m, r := f.counts(); d != 0 || m != 1 || r != 0 {
		t.Fatalf("%d dumps, %d migrations, restores %v", d, m, f.restores)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || filepath.Base(rec.Dump) != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("record = %+v", rec)
	}
}

// A stop during the migrations is no failure: nothing is put back, the record
// still says migrating (the next start goes on).
func TestAppBundleStopDuringTheMigrations(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.block = make(chan struct{})
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- prepareOf(t, ps, supervisor.RoleAPI)(ctx, noReport) }()
	waitUntil(t, "the migrations run", func() bool { _, m, _ := f.counts(); return m == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("= %v", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatal("a stop put the dump back")
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseMigrating {
		t.Fatalf("record = %+v", rec)
	}
}

// A database newer than this payload's migrations (a development build after a
// newer release): a halt, nothing put back, the record as it was.
func TestAppBundleSchemaAheadHalts(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0-2-gabc", "")
	record(t, cfg, apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.migrateErr = fmt.Errorf("%w: the database's schema is at 0150", errSchemaAhead)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || !strings.Contains(err.Error(), "changed by a newer Maktaba") {
		t.Fatalf("= %v", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatal("put a dump back")
	}
	if rec := recorded(t, cfg); rec.Version != "v0.4.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v (as it was)", rec)
	}
}

// joinNode makes this machine a joined full node of a home (its node.json),
// as the supervisor reads it; role is [server] role.
func joinNode(t *testing.T, cfg *config.Config, role string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.json")
	body := `{"node_id":"0123456789abcdef0123456789abcdef","name":"desk","private_key":"k",` +
		`"home":{"url":"http://192.168.1.20:8088"},"database_url":"postgres://node:pw@192.168.1.20:5432/maktaba"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_NODE_CONFIG", path)
	t.Setenv("MAKTABA_ROLE", "")
	cfg.Server.Role = role
}

func hasRecord(t *testing.T, cfg config.Config) bool {
	t.Helper()
	_, ok, err := apppayload.ReadDatabase(cfg.StateDir())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// A joined full node: its api runs in node mode against the home's database and
// never migrates, so no process waits on the database step (the pipeline on the
// venv alone), nothing is migrated or dumped, no record is written; an older app
// installed there later is not refused, nor is one after the machine leaves the
// cluster (it has no record of its own).
func TestAppBundleFullNodeHasNoDatabaseStep(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	joinNode(t, &cfg, "")
	f := installFakeDB(t, cfg)
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	var venvRuns atomic.Int32
	venvRun = func(context.Context, appvenv.Spec, func(appvenv.Progress)) (appvenv.Result, error) {
		venvRuns.Add(1)
		return appvenv.Result{Pipeline: "1.4.0"}, nil
	}
	var log strings.Builder
	ps := appBundleGates(cfg, payload, nil, gateProcs(false), &log)
	for _, p := range ps {
		if (p.Role == supervisor.RolePipeline) != (p.Prepare != nil) {
			t.Fatalf("%s: prepare = %v (only the pipeline waits, on its venv)", p.Name(), p.Prepare != nil)
		}
		if p.Prepare != nil {
			if err := checkedStep(t, p.Prepare)(context.Background(), noReport); err != nil {
				t.Fatalf("%s: %v", p.Name(), err)
			}
		}
	}
	if !strings.Contains(log.String(), "full node of http://192.168.1.20:8088") {
		t.Errorf("log = %q", log.String())
	}
	if venvRuns.Load() != 1 {
		t.Fatalf("the venv ran %d times", venvRuns.Load())
	}
	if d, m, r := f.counts(); d+m+r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores on a node", d, m, r)
	}
	if hasRecord(t, cfg) {
		t.Fatal("a node wrote the database's record")
	}
	// an older app on the node: not refused
	newPayload(t, payload, "v0.2.0")
	for _, p := range appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard) {
		if p.Role != supervisor.RolePipeline && p.Prepare != nil {
			t.Fatalf("%s waits on something on a node", p.Name())
		}
	}
	// the machine leaves the cluster: a home with no record, whose older app is
	// dumped and migrated as an install from before the record, not refused
	t.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(t.TempDir(), "node.json"))
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("after leaving the cluster: %v", err)
	}
	if len(f.dumps) != 1 || f.dumps[0] != "unknown->v0.2.0" {
		t.Fatalf("dumps = %v", f.dumps)
	}
}

// [server] role = "home" overrides a joined node config (the api is then a home's,
// supervisor.NodeMode): the database step runs as on any home.
func TestAppBundleForcedHomeKeepsTheDatabaseStep(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	joinNode(t, &cfg, "home")
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, m, _ := f.counts(); d != 1 || m != 1 || !hasRecord(t, cfg) {
		t.Fatalf("%d dumps, %d migrations, record %v", d, m, hasRecord(t, cfg))
	}
}

// A run without the api (serve --role streaming, ...): nothing migrates here,
// so nothing is dumped and the record is left alone; an older release than the
// record, or a database still being put back, is still refused.
func TestAppBundleRoleRunWithoutTheAPI(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	pg := testBundle(cfg)
	f := installFakeDB(t, cfg)
	streaming := func() []supervisor.Process {
		return []supervisor.Process{{Role: supervisor.RoleStreaming}, {Role: supervisor.RoleTVSlot, Instance: "blue"}}
	}
	// no record: nothing written
	ps := appBundleGates(cfg, payload, pg, streaming(), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if hasRecord(t, cfg) {
		t.Fatal("a run without the api wrote a record")
	}
	// an update's record: left as it was
	was := apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated}
	record(t, cfg, was)
	ps = appBundleGates(cfg, payload, pg, streaming(), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if rec := recorded(t, cfg); rec.Version != was.Version || rec.State != was.State {
		t.Fatalf("record = %+v", rec)
	}
	if d, m, r := f.counts(); d+m+r != 0 {
		t.Fatalf("%d dumps, %d migrations, %d restores without the api", d, m, r)
	}
	// still refused: an older release, a database being put back
	record(t, cfg, apppayload.Database{Version: "v0.4.0", State: apppayload.DatabaseMigrated})
	ps = appBundleGates(cfg, payload, pg, streaming(), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); !supervisor.Halted(err) {
		t.Fatalf("an older release = %v", err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump", Failed: "v0.3.0"})
	ps = appBundleGates(cfg, payload, pg, streaming(), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport)
	if !supervisor.Halted(err) || !strings.Contains(err.Error(), "start the whole server") {
		t.Fatalf("a database being put back = %v", err)
	}
	if _, _, r := f.counts(); r != 0 {
		t.Fatal("a run without the api put a dump back")
	}
}

// A Postgres of another major than the cluster's is refused.
func TestAppBundleRefusesAnotherPostgresMajor(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	pg := testBundle(cfg)
	pgGate := prepareOf(t, appBundleGates(cfg, payload, pg, gateProcs(true), io.Discard), supervisor.RolePostgres)
	if err := pgGate(context.Background(), noReport); err != nil {
		t.Fatalf("no cluster yet: %v", err)
	}
	if err := os.MkdirAll(pg.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pg.Data, "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pgGate(context.Background(), noReport); err != nil {
		t.Fatalf("the same major: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pg.Data, "PG_VERSION"), []byte("15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pgGate(context.Background(), noReport); err == nil || !strings.Contains(err.Error(), "PostgreSQL 15") {
		t.Fatalf("another major = %v", err)
	}
}

// Every pipeline process waits for one venv run; its progress is the running
// one's detail; a failure names the log.
func TestAppBundleVenvOnceForEveryWorker(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	installFakeDB(t, cfg)
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	var runs atomic.Int32
	var fail atomic.Bool
	venvRun = func(_ context.Context, spec appvenv.Spec, progress func(appvenv.Progress)) (appvenv.Result, error) {
		runs.Add(1)
		if spec.Payload != payload || spec.Venv != filepath.Join(cfg.StateDir(), "venv") || spec.Log == nil {
			t.Errorf("spec = %+v", spec)
		}
		if fail.Load() {
			return appvenv.Result{}, &appvenv.Error{Message: "pip: no torch"}
		}
		progress(appvenv.Progress{Fraction: 0.41, Message: "Collecting torch"})
		time.Sleep(50 * time.Millisecond)
		return appvenv.Result{Pipeline: "1.4.0"}, nil
	}
	fail.Store(true)
	ps := appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard)
	err := checkedStep(t, ps[1].Prepare)(context.Background(), noReport)
	if err == nil || !strings.Contains(err.Error(), "pip: no torch") || !strings.Contains(err.Error(), "venv.log") {
		t.Fatalf("failure = %v", err)
	}
	fail.Store(false)
	runs.Store(0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var details []string
	for _, i := range []int{1, 2} {
		wg.Add(1)
		go func(p supervisor.Process) {
			defer wg.Done()
			err := checkedStep(t, p.Prepare)(context.Background(), func(d supervisor.Detail) {
				mu.Lock()
				details = append(details, d.Text)
				mu.Unlock()
			})
			if err != nil {
				t.Error(err)
			}
		}(ps[i])
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("the venv bootstrap ran %d times for two workers", runs.Load())
	}
	if !contains(details, "Installing the transcription tools: Collecting torch (41%)") {
		t.Fatalf("details = %v", details)
	}
}

// A pipeline whose Python is named (MAKTABA_PYTHON, [pipeline] python) does not
// use the app's venv: it waits for the database alone.
func TestAppBundleNoVenvForANamedPython(t *testing.T) {
	for _, named := range []string{"env", "config"} {
		cfg, payload := appEnv(t, "v0.3.0", "")
		if named == "env" {
			t.Setenv("MAKTABA_PYTHON", "/opt/py/bin/python3")
		} else {
			cfg.Pipeline.Python = "/opt/py/bin/python3"
		}
		installFakeDB(t, cfg)
		prev := venvRun
		venvRun = func(context.Context, appvenv.Spec, func(appvenv.Progress)) (appvenv.Result, error) {
			t.Errorf("%s: the venv was built", named)
			return appvenv.Result{}, nil
		}
		ps := appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard)
		if err := prepareOf(t, ps, supervisor.RolePipeline)(context.Background(), noReport); err != nil {
			t.Fatal(err)
		}
		venvRun = prev
	}
}

// An update's start (the 0.3.96 one, 2026-11-08): the plugin runners run from the
// venv, so under the supervisor they start only once the venv step has installed this
// release's packages, as the pipeline's workers do. Meanwhile none is started (each
// start before would die on "No module named maktaba_plugin_runner") and none is
// marked failed by the crash loop; once the venv is ready every one runs.
func TestAppBundleRunnersWaitForTheVenv(t *testing.T) {
	spawnsPrograms(t) // real children under the supervisor
	cfg, payload := appEnv(t, "v0.3.96", "v0.3.95")
	installFakeDB(t, cfg)
	dir := t.TempDir()
	marker, starts := filepath.Join(dir, "venv-ready"), filepath.Join(dir, "starts.log")
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	release, building := make(chan struct{}), make(chan struct{})
	var once sync.Once
	venvRun = func(ctx context.Context, _ appvenv.Spec, progress func(appvenv.Progress)) (appvenv.Result, error) {
		once.Do(func() { close(building) })
		progress(appvenv.Progress{Fraction: 0.5, Message: "Installing maktaba_pipeline"})
		select {
		case <-ctx.Done():
			return appvenv.Result{}, ctx.Err()
		case <-release:
		}
		if err := os.WriteFile(marker, []byte("ok"), 0o600); err != nil {
			return appvenv.Result{}, err
		}
		return appvenv.Result{Pipeline: "0.3.96"}, nil
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := func(role supervisor.Role, instance string) supervisor.Process {
		p := supervisor.Process{Role: role, Instance: instance}
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_VENV_CHILD="+marker,
			"MAKTABA_TEST_APPBUNDLE_STARTS="+starts, "MAKTABA_TEST_APPBUNDLE_NAME="+p.Name())
		p.Cmd = cmd
		return p
	}
	api := exec.Command(self)
	api.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_CHILD=1")
	procs := []supervisor.Process{{Role: supervisor.RoleAPI, Cmd: api},
		child(supervisor.RolePipeline, ""),
		child(supervisor.RolePluginRunner, "ml-1"), child(supervisor.RolePluginRunner, "ml-2"),
		child(supervisor.RolePluginRunner, "index")}
	procs = appBundleGates(cfg, payload, nil, procs, io.Discard)

	// a crash loop after two quick exits: had a runner started before the venv was
	// ready, it would be marked failed within milliseconds
	policy := supervisor.DefaultPolicy()
	policy.BackoffInitial, policy.BackoffMax, policy.MaxRestarts = 5*time.Millisecond, 10*time.Millisecond, 2
	policy.HealthyAfter, policy.StartupGrace = 50*time.Millisecond, 50*time.Millisecond
	sup := supervisor.New(procs, policy, supervisor.WithLog(io.Discard))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, 5*time.Second) }()
	defer func() {
		cancel()
		<-done
	}()

	venvChild := func(st supervisor.ChildStatus) bool {
		return st.Role == supervisor.RolePipeline || st.Role == supervisor.RolePluginRunner
	}
	select {
	case <-building:
	case <-time.After(10 * time.Second):
		t.Fatal("the venv step never ran")
	}
	// while the venv installs: no child that runs from it starts, none is failed, each
	// shows what it waits on
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, st := range sup.Snapshot().Processes {
			if !venvChild(st) {
				continue
			}
			if st.State == supervisor.StateFailed || st.CrashLoop || st.PID != 0 || st.Restarts != 0 {
				t.Fatalf("%s/%s before the venv is ready: state %s, pid %d, %d restarts, crash loop %v",
					st.Role, st.Instance, st.State, st.PID, st.Restarts, st.CrashLoop)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, st := range sup.Snapshot().Processes {
		if venvChild(st) && st.DetailCode != "venv_building" {
			t.Errorf("%s/%s waits showing %q (%s)", st.Role, st.Instance, st.DetailCode, st.Detail)
		}
	}
	if b, _ := os.ReadFile(starts); len(b) != 0 {
		t.Fatalf("started before the venv was ready:\n%s", b)
	}

	close(release)
	waitFor := time.Now().Add(20 * time.Second)
	for {
		running := 0
		for _, st := range sup.Snapshot().Processes {
			if !venvChild(st) {
				continue
			}
			if st.State == supervisor.StateFailed || st.CrashLoop {
				t.Fatalf("%s/%s marked failed: %s", st.Role, st.Instance, st.Detail)
			}
			if st.State == supervisor.StateRunning {
				running++
			}
		}
		if running == 4 {
			break
		}
		if time.Now().After(waitFor) {
			t.Fatalf("%d of 4 running after the venv was ready: %+v", running, sup.Snapshot().Processes)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// running is the process started: its own line may still be on its way
	var lines []string
	waitUntil(t, "every child's start logged", func() bool {
		b, _ := os.ReadFile(starts)
		lines = strings.Split(strings.TrimSpace(string(b)), "\n")
		return len(lines) >= 4
	})
	sort.Strings(lines)
	want := []string{"pipeline ready", "plugin-runner/index ready", "plugin-runner/ml-1 ready", "plugin-runner/ml-2 ready"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("starts = %q, want each once, after the venv: %q", lines, want)
	}
}

// The detail codes are a fixed list (the app's screens translate them): a code
// added, gone or with other params must be added here too, and to the list in
// contract-update.md.
func TestDetailCodesAreAFixedList(t *testing.T) {
	want := map[string]string{
		"db_waiting":               "",
		"db_checking":              "release",
		"db_dumping":               "from to mb expected_mb",
		"db_migrating":             "to",
		"db_restoring":             "failed dump",
		"db_restored":              "failed error dump release",
		"db_kept":                  "failed error release",
		"db_undone":                "failed error release",
		"db_refused_older":         "release needed dump",
		"db_ahead":                 "release error",
		"db_dump_failed":           "to error",
		"db_migrate_failed":        "release error",
		"db_held":                  "failed error dump release",
		"db_restore_failed":        "failed error dump restore_error",
		"db_restore_unfinished":    "failed dump",
		"db_restoring_backup":      "dump by",
		"db_restore_backup_failed": "dump error",
		"db_restore_backup_part":   "dump error",
		"db_restore_backup_gone":   "dump error",
		"db_restore_no_copy":       "dump error",
		"db_restore_for_release":   "dump release",
		"db_no_cluster":            "failed error dump",
		"db_record_failed":         "path error",
		"pg_unreadable":            "path error",
		"pg_major":                 "cluster payload",
		"venv_waiting":             "",
		"venv_building":            "step done total percent message",
		"venv_interrupted":         "log",
		"venv_failed":              "error log",
		"storage_waiting":          "path",
		"storage_moving":           "to done total percent",
		"storage_move_failed":      "to error",
		"port_taken":               "port addr holder_pid holder_command holder_role error",
	}
	if len(detailCodes) != len(want) {
		t.Errorf("%d codes, the list has %d", len(detailCodes), len(want))
	}
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	for code, names := range detailCodes {
		if !snake.MatchString(code) {
			t.Errorf("%q is not snake_case", code)
		}
		if w, ok := want[code]; !ok || strings.Join(names, " ") != w {
			t.Errorf("%s: params %v, the list says %q (in it: %v)", code, names, w, ok)
		}
	}
}

// The details no other test meets carry their codes too: a dump that fails, a
// record that cannot be written, a failed update with no database of its own to
// put back into, a cluster that cannot be read, a venv build stopped from
// elsewhere.
func TestAppBundleRareDetailsAreCoded(t *testing.T) {
	codeOf := func(err error) string { return supervisor.DetailOf(err).Code }

	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	installFakeDB(t, cfg)
	dumpBeforeUpdate = func(context.Context, pgbundle.Bundle, string, string, int) (string, error) {
		return "", errors.New("pg_dump: disk full")
	}
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); codeOf(err) != "db_dump_failed" ||
		supervisor.Halted(err) {
		t.Errorf("a failed dump = %v (%s)", err, codeOf(err))
	}

	cfg, payload = appEnv(t, "v0.3.0", "")
	installFakeDB(t, cfg)
	if err := os.MkdirAll(apppayload.DatabasePath(cfg.StateDir()), 0o755); err != nil { // a folder where the file goes
		t.Fatal(err)
	}
	ps = appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); codeOf(err) != "db_record_failed" {
		t.Errorf("a record that cannot be read = %v (%s)", err, codeOf(err))
	}

	cfg, payload = appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump", By: "admin", Error: "restore asked by admin"})
	installFakeDB(t, cfg)
	ps = appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); codeOf(err) != "db_no_cluster" ||
		!supervisor.Halted(err) {
		t.Errorf("no database of its own to put back into = %v (%s)", err, codeOf(err))
	}

	pg := testBundle(cfg)
	if err := os.MkdirAll(filepath.Join(pg.Data, "PG_VERSION"), 0o700); err != nil { // unreadable as a file
		t.Fatal(err)
	}
	gate := prepareOf(t, appBundleGates(cfg, payload, pg, gateProcs(true), io.Discard), supervisor.RolePostgres)
	if err := gate(context.Background(), noReport); codeOf(err) != "pg_unreadable" {
		t.Errorf("a cluster that cannot be read = %v (%s)", err, codeOf(err))
	}

	cfg, payload = appEnv(t, "v0.3.0", "")
	installFakeDB(t, cfg)
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	venvRun = func(context.Context, appvenv.Spec, func(appvenv.Progress)) (appvenv.Result, error) {
		return appvenv.Result{}, appvenv.ErrInterrupted
	}
	ps = appBundleGates(cfg, payload, nil, gateProcs(false), io.Discard)
	if err := prepareOf(t, ps, supervisor.RolePipeline)(context.Background(), noReport); codeOf(err) != "venv_interrupted" {
		t.Errorf("a venv build stopped from elsewhere = %v (%s)", err, codeOf(err))
	}
}

// A process waiting on a shared step ends at its own stop while the step goes on.
func TestSharedStepWaiterStops(t *testing.T) {
	release := make(chan struct{})
	s := &sharedStep{wait: supervisor.Detail{Text: "waiting"}, run: func(_ context.Context, report func(supervisor.Detail)) error {
		report(supervisor.Detail{Code: "db_checking", Text: "working"})
		<-release
		return nil
	}}
	go func() { _ = s.prepare(context.Background(), noReport) }()
	waitUntil(t, "the step runs", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running != nil })
	ctx, cancel := context.WithCancel(context.Background())
	var shown []string
	done := make(chan error, 1)
	go func() { done <- s.prepare(ctx, func(d supervisor.Detail) { shown = append(shown, d.Text) }) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the waiter = %v", err)
	}
	close(release)
	if len(shown) == 0 || shown[0] != "working" {
		t.Fatalf("the waiter showed %v (the step's report)", shown)
	}
}

// A process that starts waiting on a shared step while it runs shows the step's newest
// report, never the waiting text over it: its first report is made before the step can
// broadcast to it, so a broadcast that lands in between is not overwritten. The waiter's
// first report is slow here so that, were it made outside the lock, the step's broadcast
// would land first and the waiting text would end on top.
func TestSharedStepWaiterKeepsTheNewestReport(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var s *sharedStep
	s = &sharedStep{wait: supervisor.Detail{Code: "venv_waiting", Text: "waiting"}, run: func(_ context.Context, report func(supervisor.Detail)) error {
		report(supervisor.Detail{Code: "venv_building", Text: "starting"})
		for { // the waiter is registered: the step reports again at once
			s.mu.Lock()
			n := len(s.watchers)
			s.mu.Unlock()
			if n == 2 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		report(supervisor.Detail{Code: "venv_building", Text: "installing"})
		<-release
		return nil
	}}
	go func() { _ = s.prepare(context.Background(), noReport) }()
	waitUntil(t, "the step runs", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running != nil })
	var (
		mu    sync.Mutex
		shown []string
		first = true
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = s.prepare(ctx, func(d supervisor.Detail) {
			mu.Lock()
			slow := first
			first = false
			mu.Unlock()
			if slow {
				time.Sleep(50 * time.Millisecond)
			}
			mu.Lock()
			shown = append(shown, d.Text)
			mu.Unlock()
		})
	}()
	waitUntil(t, "the waiter shows the step's second report", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, x := range shown {
			if x == "installing" {
				return true
			}
		}
		return false
	})
	time.Sleep(80 * time.Millisecond) // a late first report would land by now
	mu.Lock()
	defer mu.Unlock()
	if shown[len(shown)-1] != "installing" {
		t.Fatalf("the waiter showed %v: the newest report must stay on top", shown)
	}
}

// runMigrateUp runs `migrate --timeout 0 up` with the api's binary and
// environment; a failure carries the last lines, the api's exit 3 is a database
// newer than its migrations.
func TestRunMigrateUp(t *testing.T) {
	spawnsPrograms(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	api := func(mode string) *exec.Cmd {
		c := exec.Command(self)
		c.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_MIGRATE="+mode)
		return c
	}
	var log strings.Builder
	if err := runMigrateUp(context.Background(), api("ok"), &log); err != nil || !strings.Contains(log.String(), "applying") ||
		!strings.Contains(log.String(), "(offline 1)") {
		t.Fatalf("ok = %v, log %q", err, log.String())
	}
	err = runMigrateUp(context.Background(), api("fail"), io.Discard)
	if err == nil || errors.Is(err, errSchemaAhead) || err.Error() != "2026/10/21 OK   0143_queue_health.sql; "+
		"migrate: ERROR 0144_x.sql: failed to run SQL migration; relation \"videos\" does not exist" {
		t.Fatalf("fail = %v", err)
	}
	err = runMigrateUp(context.Background(), api("ahead"), io.Discard)
	if !errors.Is(err, errSchemaAhead) || !strings.Contains(err.Error(), "schema is at 0150") {
		t.Fatalf("ahead = %v", err)
	}
}

// A rollback's Down scripts run as `migrate down-to <schema>` with the api's binary and
// environment, without an operator's MAKTABA_DISABLE_DOWN (the rollback of 0.3.90, 2026-11-07).
func TestRunMigrateDownTo(t *testing.T) {
	spawnsPrograms(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	api := func(mode string) *exec.Cmd {
		c := exec.Command(self)
		c.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_MIGRATE="+mode, "MAKTABA_DISABLE_DOWN=1")
		return c
	}
	var log strings.Builder
	if err := runMigrateDownTo(context.Background(), api("down-ok"), 191, &log); err != nil ||
		!strings.Contains(log.String(), "0192_smart_playlists.sql (down)") {
		t.Fatalf("ok = %v, log %q", err, log.String())
	}
	if err := runMigrateDownTo(context.Background(), api("down-fail"), 191, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("fail = %v", err)
	}
}

// After a rollback whose Down scripts undid the new release's migrations (the record the
// release before's own: layout.MarkRecordDown), the release before starts as on its own
// database: nothing dumped, nothing put back, nothing refused.
func TestAppBundleStartsOnTheSchemaItsDownScriptsLeft(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated, From: "v0.2.0",
		Release: "v0.3.0", Dump: "/b/pre-0.2.0-to-0.3.0.dump"})
	if ok, err := layout.MarkRecordDown(apppayload.DatabasePath(cfg.StateDir()), "v0.3.0", "0.2.0", "watch_timeout"); !ok || err != nil {
		t.Fatalf("MarkRecordDown: %v %v", ok, err)
	}
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if d, _, r := f.counts(); d != 0 || r != 0 {
		t.Fatalf("%d dumps, %d restores", d, r)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record %+v", rec)
	}
}

// A database a rollback kept on a newer schema (v0.2.5's) for v0.2.0: v0.3.0's migrations
// fail and their Down scripts undo them, back to the kept slot. It is still kept for
// v0.2.0, which starts on it (its own code refuses a schema ahead that is not: db_ahead).
func TestAppBundleDownScriptsLeaveAKeptSchemaKept(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.2.5", State: apppayload.DatabaseMigrated, Release: "v0.2.0",
		KeptFor: "0.2.0", KeptReason: "watch_timeout"})
	f := installFakeDB(t, cfg)
	f.pending, f.known = 2, true
	f.back, f.backSchema, f.before, f.applied = updater.FailedDown, 234, 234, 235
	f.migrateErr = errors.New("migrate: ERROR 0236_x.sql: boom")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_undone" {
		t.Fatalf("the failed update = %v", err)
	}
	if len(f.downs) != 1 || f.downs[0] != "migrating 234" || len(f.befores) != 1 || f.befores[0] != 234 {
		t.Fatalf("Down scripts %v, decided with the slot before %v", f.downs, f.befores)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.2.0" || rec.KeptFor != "0.2.0" || rec.SchemaRelease != "v0.2.5" || rec.KeptReason == "" {
		t.Fatalf("record %+v", rec)
	}
	// #1666: an intermediate release M (v0.2.3) between A (0.2.0) and B (v0.2.5) runs on B's
	// kept schema after C's Down scripts, while a release older than A (v0.1.9) is refused.
	if !keptFor(recorded(t, cfg), "v0.2.3") {
		t.Fatalf("keptFor rejected intermediate release v0.2.3 on %+v", recorded(t, cfg))
	}
	if keptFor(recorded(t, cfg), "v0.1.9") {
		t.Fatalf("keptFor accepted release older than kept_for on %+v", recorded(t, cfg))
	}
	// Minor 5 (#1666): downgrade() names the schema's own release (SchemaOwner), not reset Version.
	if derr := downgrade("v0.1.9", recorded(t, cfg)); !strings.Contains(derr.Error(), "v0.2.5") {
		t.Fatalf("downgrade message should name schema owner v0.2.5, got %v", derr)
	}
	// Backwards-compatible: an older record without SchemaRelease uses Version.
	if !keptFor(apppayload.Database{Version: "v0.2.5", State: apppayload.DatabaseMigrated, KeptFor: "0.2.0"}, "v0.2.3") {
		t.Fatal("keptFor should stay backwards-compatible when SchemaRelease is empty")
	}
	newPayload(t, payload, "v0.2.3")
	f.migrateErr, f.ahead = nil, true // the database is at B's schema, ahead of M's own migrations
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("intermediate v0.2.3 on the kept schema: %v", err)
	}
	if rec := recorded(t, cfg); rec.KeptFor != "0.2.0" || rec.SchemaRelease != "v0.2.5" || rec.Release != "v0.2.3" {
		t.Fatalf("record after intermediate start: %+v", rec)
	}
	newPayload(t, payload, "v0.2.0")
	f.migrateErr, f.ahead = nil, true // the database is ahead of v0.2.0's own migrations
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("v0.2.0 on the kept schema: %v", err)
	}
}

// #1666: restoreAsked of a dump taken from a kept schema preserves schema_release so an
// intermediate release M between kept_for and schema_release is accepted after the restore.
func TestAppBundleRestoreAskedPreservesSchemaRelease(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.3", "")
	dump := filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")
	if err := os.MkdirAll(filepath.Dir(dump), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump, []byte("PGDMP"), 0o600); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{
		Version:           "v0.3.0",
		State:             apppayload.DatabaseRestoring,
		From:              "v0.2.0",
		Dump:              dump,
		By:                "admin",
		DumpKeptFor:       "0.2.0",
		DumpSchemaRelease: "v0.2.5",
	})
	f := installFakeDB(t, cfg)
	f.ahead = true // restored dump holds B's (v0.2.5) schema, ahead of M (v0.2.3)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("restoreAsked + start of intermediate v0.2.3: %v", err)
	}
	rec := recorded(t, cfg)
	if rec.State != apppayload.DatabaseMigrated || rec.KeptFor != "0.2.0" || rec.SchemaRelease != "v0.2.5" {
		t.Fatalf("record after restoreAsked: %+v", rec)
	}
}

// An update a release from before schema_before began (its record says migrating, with no
// slot): the database itself says the slot it was at when the migrations started, and a
// failure goes back to it, not to the release before's newest.
func TestAppBundleOlderRecordTakesTheSlotTheDatabaseRecorded(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	began := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrating, From: "v0.2.0",
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump", DumpKeptFor: "0.2.0", At: began})
	f := installFakeDB(t, cfg)
	f.back, f.backSchema, f.before, f.applied = updater.FailedDown, 234, 234, 235
	f.migrateErr = errors.New("migrate: ERROR 0236_x.sql: boom")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	_ = prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if len(f.befores) != 1 || f.befores[0] != 234 {
		t.Fatalf("decided with the slot before %v, want 234", f.befores)
	}
	if len(f.askedAt) != 1 {
		t.Fatalf("asked %v", f.askedAt)
	}
	if rec := recorded(t, cfg); rec.KeptFor != "0.2.0" {
		t.Fatalf("record %+v", rec)
	}

	// a held record (an unasked mark of an older launcher, with no slot) was saved when the
	// update failed, after its migrations: its time says nothing of the slot before them
	cfg, payload = appEnv(t, "v0.3.1", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0", Failed: "v0.3.0",
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump", Error: "launcher_boot_loop"})
	f = installFakeDB(t, cfg)
	f.back, f.backSchema, f.before, f.applied = updater.FailedDown, 234, 236, 237
	f.migrateErr = errors.New("migrate: ERROR 0237_x.sql: boom")
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	_ = prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if len(f.askedAt) != 0 || len(f.befores) != 1 || f.befores[0] != 0 {
		t.Fatalf("asked %v, decided with the slot before %v: want none", f.askedAt, f.befores)
	}
}

// The payload is saved as the one that ran once every process ran healthy.
func TestConfirmAppPayloadAfterAHealthyRun(t *testing.T) {
	spawnsPrograms(t) // a real child under the supervisor
	t.Setenv("MAKTABA_CONFIRM_AFTER", "1ms")
	cfg, payload := appEnv(t, "v0.3.0", "v0.2.0")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self)
	child.Env = append(os.Environ(), "MAKTABA_TEST_APPBUNDLE_CHILD=1")
	p := supervisor.Process{Role: supervisor.RoleAPI, Cmd: child}
	sup := supervisor.New([]supervisor.Process{p}, supervisor.DefaultPolicy(), supervisor.WithLog(&strings.Builder{}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, 5*time.Second) }()
	defer func() {
		cancel()
		<-done
	}()
	go confirmAppPayload(ctx, sup, cfg.StateDir(), payload)
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, ok, err := apppayload.Confirmed(cfg.StateDir())
		if err == nil && ok && got.Version == "v0.3.0" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not confirmed: %+v %v %v", got, ok, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Review round 2 (A-P): an update's migrations never wait on another session's lock for
// ever: the other sessions are ended before each try, and a migration that waited past
// the lock timeout is tried again (at most migrateTries). Each migration goose says it
// applied moves the step on for the update's watch.
func TestAppBundleMigrationsEndSessionsAndRetryALockTimeout(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	cfg.Postgres.UpdateDumps = 5
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	installFakeDB(t, cfg)
	prevE, prevM := endSessionsBundled, migrateUp
	t.Cleanup(func() { endSessionsBundled, migrateUp = prevE, prevM })
	var mu sync.Mutex
	ended, tries := 0, 0
	endSessionsBundled = func(context.Context, pgbundle.Bundle) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		ended++
		return 2, nil
	}
	migrateUp = func(_ context.Context, _ *exec.Cmd, log io.Writer) error {
		mu.Lock()
		tries++
		n := tries
		mu.Unlock()
		if n == 1 {
			_, _ = io.WriteString(log, "OK   0150_a.sql (1.2ms)")
			return errors.New("migrate: ERROR 0151_b.sql: pq: canceling statement due to lock timeout")
		}
		_, _ = io.WriteString(log, "OK   0151_b.sql (3ms)OK   0152_c.sql (2ms)goose: successfully migrated")
		return nil
	}
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	var said []string
	err := checkedStep(t, prepareOf(t, ps, supervisor.RoleAPI))(context.Background(), func(d supervisor.Detail) {
		mu.Lock()
		said = append(said, d.Text)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if ended != 2 || tries != 2 {
		t.Fatalf("sessions ended %d times, migrations tried %d times", ended, tries)
	}
	if !contains(said, "Updating the library database for v0.3.0 (3 migrations done)") {
		t.Fatalf("details %q", said)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseMigrated || rec.Version != "v0.3.0" {
		t.Fatalf("record %+v", rec)
	}
	// the same release starting again: nothing to migrate, no session ended
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if ended != 2 {
		t.Fatalf("a start of the same release ended sessions (%d)", ended)
	}
}

// Review round 2 (A-P): the dump says how much it has written while it is taken (the
// update's watch sees it grow), and a start the watch gave up on meanwhile records
// nothing over the restore it marked.
func TestAppBundleDumpProgressAndAGivenUpStart(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	cfg.Postgres.UpdateDumps = 5
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	installFakeDB(t, cfg)
	prevD, prevM, prevEvery, prevE := dumpBeforeUpdate, migrateUp, dumpReportEvery, endSessionsBundled
	t.Cleanup(func() {
		dumpBeforeUpdate, migrateUp, dumpReportEvery, endSessionsBundled = prevD, prevM, prevEvery, prevE
	})
	endSessionsBundled = func(context.Context, pgbundle.Bundle) (int, error) { return 0, nil }
	dumpReportEvery = 2 * time.Millisecond
	dumpBeforeUpdate = func(_ context.Context, b pgbundle.Bundle, from, to string, _ int) (string, error) {
		path := filepath.Join(b.Backups, pgbundle.UpdateDumpName(from, to))
		_ = os.MkdirAll(b.Backups, 0o700)
		for i := 1; i <= 5; i++ {
			_ = os.WriteFile(path+".partial", make([]byte, i<<20), 0o600)
			time.Sleep(10 * time.Millisecond)
		}
		return path, os.Rename(path+".partial", path)
	}
	migrateUp = func(context.Context, *exec.Cmd, io.Writer) error {
		// the watch gave up on this start meanwhile (start_timeout): the record is held
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		rec.State, rec.Failed, rec.Error = apppayload.DatabaseHeld, rec.Version, "start_timeout"
		return apppayload.WriteDatabase(cfg.StateDir(), rec)
	}
	// the newest finished dump (8 MB) is what this one is expected to come to
	b := testBundle(cfg)
	_ = os.MkdirAll(b.Backups, 0o700)
	_ = os.WriteFile(filepath.Join(b.Backups, "maktaba-20260101-030000.dump"), make([]byte, 3<<20), 0o600)
	newer := filepath.Join(b.Backups, "pre-v0.1.0-to-v0.2.0.dump")
	_ = os.WriteFile(newer, make([]byte, 8<<20), 0o600)
	_ = os.Chtimes(newer, time.Now(), time.Now().Add(time.Hour))
	ps := appBundleGates(cfg, payload, b, gateProcs(true), io.Discard)
	var mu sync.Mutex
	var said []supervisor.Detail
	err := checkedStep(t, prepareOf(t, ps, supervisor.RoleAPI))(context.Background(), func(d supervisor.Detail) {
		mu.Lock()
		said = append(said, d)
		mu.Unlock()
	})
	if d := supervisor.DetailOf(err); !supervisor.Halted(err) || d.Code != "db_held" {
		t.Fatalf("a start given up on: %v", err)
	}
	grew := false
	for _, d := range said {
		if d.Code != "db_dumping" {
			continue
		}
		if d.Params["expected_mb"] != int64(8) {
			t.Fatalf("expected_mb: %+v", d.Params)
		}
		if mb, _ := d.Params["mb"].(int64); strings.Contains(d.Text, "MB so far") && mb > 0 {
			grew = true
		}
	}
	if !grew {
		t.Fatalf("the dump never said its size: %+v", said)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseHeld || rec.Failed != "v0.3.0" {
		t.Fatalf("recorded over the watch's mark: %+v", rec)
	}
}

// Requirement (update-attempts.md): a release's start says what it did with the
// update where the Updates page reads it. A failed migration reads put back, with the
// migration's error; the release before, back, keeps it; the update tried again and
// installed is a new attempt; a dump that cannot be taken reads failed at the dump.
func TestAppBundleStartAttempts(t *testing.T) {
	t.Setenv(envLauncherRoot, "")
	cfg, payload := appEnv(t, "v0.3.0", "")
	dir := filepath.Join(cfg.StateDir(), "updates")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.migrateErr = errors.New(`ERROR 0149_x.sql: relation "videos" does not exist`)
	ctx := context.Background()
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); !supervisor.Halted(err) {
		t.Fatalf("a failed update = %v", err)
	}
	got := attempts.Read(dir)
	if len(got) != 1 || got[0].To != "0.3.0" || got[0].From != "0.2.0" || got[0].Outcome != attempts.Failed ||
		got[0].Code != "db_held_lossy" || got[0].Step != "migrate" || !strings.Contains(got[0].Detail, "videos") ||
		got[0].EndedAt == nil || got[0].Source != attempts.SourceServer {
		t.Fatalf("after the failed update: %+v", got)
	}

	// a person put the backup back; the release before runs: the attempt still says what
	// happened to the one that failed
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseRestored, From: "v0.2.0"})
	newPayload(t, payload, "v0.2.0")
	f.migrateErr = nil
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Read(dir); len(got) != 1 || got[0].Outcome != attempts.Failed || got[0].Code != "db_held_lossy" {
		t.Fatalf("the release before: %+v", got)
	}

	// tried again later (a failed try within retryWithin is the same one), installed: a
	// new attempt, the failed one kept
	prevNow := attempts.Now
	t.Cleanup(func() { attempts.Now = prevNow })
	attempts.Now = func() time.Time { return prevNow().Add(retryWithin + time.Minute) }
	newPayload(t, payload, "v0.3.0")
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); err != nil {
		t.Fatal(err)
	}
	got = attempts.Read(dir)
	if len(got) != 2 || got[0].Outcome != attempts.Installed || got[0].Code != "" || got[1].Outcome != attempts.Failed ||
		got[0].ID == got[1].ID {
		t.Fatalf("installed: %+v", got)
	}

	// a dump that cannot be taken: failed at the dump, the database untouched
	prev := dumpBeforeUpdate
	t.Cleanup(func() { dumpBeforeUpdate = prev })
	dumpBeforeUpdate = func(context.Context, pgbundle.Bundle, string, string, int) (string, error) {
		return "", errors.New("pg_dump: No space left on device")
	}
	newPayload(t, payload, "v0.4.0")
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); err == nil {
		t.Fatal("a dump that failed went on")
	}
	got = attempts.Read(dir)
	if got[0].To != "0.4.0" || got[0].Outcome != attempts.Failed || got[0].Code != "db_dump_failed" ||
		got[0].Step != "dump" || !strings.Contains(got[0].Detail, "No space left") {
		t.Fatalf("a failed dump: %+v", got[0])
	}
}

// A rollback that marked the record (the watch gave up: start_timeout; restoring, as a
// release before 2026-11-24 marks it): the release before puts nothing back and stops,
// and the attempt at the failed release reads failed, with the rollback's reason as its
// code.
func TestAppBundleRollbackHeldAttempt(t *testing.T) {
	t.Setenv(envLauncherRoot, "")
	cfg, payload := appEnv(t, "v0.2.0", "")
	dir := filepath.Join(cfg.StateDir(), "updates")
	dump := filepath.Join(cfg.StateDir(), "backups", "pre-v0.2.0-to-v0.3.0.dump")
	if err := os.MkdirAll(filepath.Dir(dump), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(dump, []byte("dump"), 0o600)
	record(t, cfg, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "start_timeout"})
	f := installFakeDB(t, cfg)
	err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || supervisor.DetailOf(err).Code != "db_held" || len(f.restores) != 0 {
		t.Fatalf("the release before on the marked record: %v (restores %v)", err, f.restores)
	}
	got := attempts.Read(dir)
	if len(got) != 1 || got[0].To != "0.3.0" || got[0].From != "0.2.0" || got[0].Outcome != attempts.Failed ||
		got[0].Code != "start_timeout" {
		t.Fatalf("attempts %+v", got)
	}
}

// A first install is no update (nothing recorded); a start of another release that never
// finished (stopped during its dump, the launcher back on the release before) ends
// failed; and a start of the same release that began hours ago and never ended is not
// gone on with: it ends failed and the new start is a new attempt.
func TestAppBundleStaleAndFirstInstallAttempts(t *testing.T) {
	t.Setenv(envLauncherRoot, "")
	cfg, payload := appEnv(t, "v0.3.0", "")
	dir := filepath.Join(cfg.StateDir(), "updates")
	installFakeDB(t, cfg)
	ctx := context.Background()
	start := func() {
		t.Helper()
		if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
			supervisor.RoleAPI)(ctx, noReport); err != nil {
			t.Fatal(err)
		}
	}
	start()
	if got := attempts.Read(dir); len(got) != 0 {
		t.Fatalf("a first install recorded an update: %+v", got)
	}

	// 0.4.0's start was stopped during its dump; the launcher runs 0.3.0 again
	if err := attempts.Record(dir, attempts.Attempt{ID: "start-0.4.0-1", At: time.Now().UTC(), Source: attempts.SourceServer,
		From: "0.3.0", To: "0.4.0", Outcome: attempts.Running, Step: "dump"}); err != nil {
		t.Fatal(err)
	}
	start()
	got := attempts.Read(dir)
	if len(got) != 1 || got[0].Outcome != attempts.Failed || got[0].Code != attempts.Interrupted || got[0].Step != "dump" ||
		got[0].EndedAt == nil || !strings.Contains(got[0].Detail, "0.3.0 runs now") {
		t.Fatalf("a start that never finished: %+v", got)
	}

	// 0.5.0's start began hours ago and never ended; this start of 0.5.0 is a new try
	if err := attempts.Record(dir, attempts.Attempt{ID: "start-0.5.0-1", At: time.Now().UTC().Add(-3 * time.Hour),
		Source: attempts.SourceServer, From: "0.3.0", To: "0.5.0", Outcome: attempts.Running, Step: "migrate"}); err != nil {
		t.Fatal(err)
	}
	newPayload(t, payload, "v0.5.0")
	start()
	got = attempts.Read(dir)
	old := got[len(got)-1] // the oldest: it began three hours ago
	if len(got) != 3 || got[0].To != "0.5.0" || got[0].Outcome != attempts.Installed || got[0].ID == "start-0.5.0-1" ||
		old.ID != "start-0.5.0-1" || old.Outcome != attempts.Failed || old.Code != attempts.Interrupted {
		t.Fatalf("an old start of the same release: %+v", got)
	}
}

// The home's wheelhouse a node's update staged beside the release goes once the app's venv
// is ready (its packages are in it); a build that failed keeps it for the next try.
func TestAppBundleVenvRemovesTheWheelhouse(t *testing.T) {
	wh := filepath.Join(t.TempDir(), "wheelhouse", "1.5.0")
	if err := os.MkdirAll(wh, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wh, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := venvRun
	t.Cleanup(func() { venvRun = prev })
	fail := true
	venvRun = func(_ context.Context, spec appvenv.Spec, _ func(appvenv.Progress)) (appvenv.Result, error) {
		if spec.Wheelhouse != wh {
			t.Errorf("the build was given %q", spec.Wheelhouse)
		}
		if fail {
			return appvenv.Result{}, &appvenv.Error{Message: "pip: no torch"}
		}
		return appvenv.Result{Pipeline: "1.5.0"}, nil
	}
	g := &venvGate{spec: appvenv.Spec{Wheelhouse: wh}}
	if err := g.run(context.Background(), noReport); err == nil {
		t.Fatal("a failed build passed")
	}
	if _, err := os.Stat(wh); err != nil {
		t.Fatal("a failed build took the wheelhouse")
	}
	fail = false
	if err := g.run(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wh); err == nil {
		t.Fatal("the wheelhouse was kept after the venv was built")
	}
}

// pauseHome is a home whose update's migrations pause the machines' copies.
type pauseHome struct {
	cfg     config.Config
	payload string
	f       *fakeDB
	log     *strings.Builder
}

func newPauseHome(t *testing.T, rec apppayload.Database) pauseHome {
	t.Helper()
	cfg, payload := appEnv(t, "v0.3.0", "")
	if err := os.MkdirAll(filepath.Join(updater.KeptPayloadDir(updatesDirOf(cfg, ""), "v0.2.0"), "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	record(t, cfg, rec)
	return pauseHome{cfg, payload, installFakeDB(t, cfg), &strings.Builder{}}
}

// start is one start of its database step.
func (h pauseHome) start(t *testing.T) error {
	t.Helper()
	ps := appBundleGates(h.cfg, h.payload, testBundle(h.cfg), gateProcs(true), lockedWriter{mu: &sync.Mutex{}, w: h.log})
	return prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
}

// check is what was paused and resumed, in order, and whether the copies are out now.
func (h pauseHome) check(t *testing.T, want string, paused bool) {
	t.Helper()
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	if got := strings.Join(h.f.copies, "|"); got != want || h.f.paused != paused {
		t.Fatalf("copies %q paused %v, want %q paused %v\n%s", got, h.f.paused, want, paused, h.log)
	}
}

// pausePrev is the record of a home about to update, errPauseRun a migration that fails.
var (
	pausePrev   = apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated}
	errPauseRun = errors.New("migrate: ERROR 0144_x.sql: boom")
)

// Research item 5 (E4, E8): an update's migrations pause the machines' copies while they run,
// and let them in again once the schema is settled. They wait for no table sync: the restart
// that began the release ended each (the home's driver waits before it, api/internal/updates).
func TestAppBundleMigrationsPauseTheMachinesCopies(t *testing.T) {
	t.Run("an update", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrating|resume migrated", false)
		for _, want := range []string{"the machines' copies are paused until the schema is settled (2 of their sessions ended)",
			"the machines' copies may log in again"} {
			if !strings.Contains(h.log.String(), want) {
				t.Errorf("the log lacks %q:\n%s", want, h.log)
			}
		}
	})
	t.Run("no update", func(t *testing.T) {
		h := newPauseHome(t, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "", false)
	})
	t.Run("undone by the Down scripts", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		h.f.back, h.f.backSchema, h.f.before, h.f.applied, h.f.migrateErr = updater.FailedDown, 230, 230, 234, errPauseRun
		_ = h.start(t)
		h.check(t, "pause migrating|down|resume migrated", false)
	})
	t.Run("kept", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		h.f.back, h.f.migrateErr = updater.FailedKeep, errPauseRun
		_ = h.start(t)
		h.check(t, "pause migrating|resume migrated", false)
	})
}

// The copies come back only on a settled schema: a held update, migrations a kill or a stop cut
// short and a Down script that failed leave them out, until a person's choice is done or the
// next start ends what was begun.
func TestAppBundleCopiesStayOutUntilTheSchemaIsSettled(t *testing.T) {
	t.Run("held, until a person's choice is done", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		h.f.back, h.f.migrateErr = updater.FailedLossy, errPauseRun
		if err := h.start(t); !supervisor.Halted(err) {
			t.Fatalf("the failed update: %v", err)
		}
		h.check(t, "pause migrating", true)
		// each start stops on it, with the copies out (one the switch back made too)
		h.f.paused = false // a release before the pause ran meanwhile
		if err := h.start(t); !supervisor.Halted(err) {
			t.Fatalf("a start on a held update: %v", err)
		}
		h.check(t, "pause migrating|pause held", true)
		// the person puts the backup back: they come back once it is done
		rec := recorded(t, h.cfg)
		rec.State, rec.By = apppayload.DatabaseRestoring, "admin"
		record(t, h.cfg, rec)
		newPayload(t, h.payload, "v0.2.0")
		h.f.migrateErr = nil
		if err := h.start(t); err != nil {
			t.Fatalf("the restore: %v", err)
		}
		h.check(t, "pause migrating|pause held|pause restoring|resume migrated", false)
	})
	t.Run("held, then a newer release", func(t *testing.T) {
		h := newPauseHome(t, apppayload.Database{Version: "v0.2.5", State: apppayload.DatabaseHeld, From: "v0.2.0",
			Failed: "v0.2.5", Error: "boom", Held: "db_down_failed", Dump: "/b/pre-v0.2.0-to-v0.2.5.dump"})
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause held|pause migrating|resume migrated", false)
	})
	for name, state := range map[string]string{"killed before or while it migrates": apppayload.DatabaseMigrating,
		"killed after it": apppayload.DatabaseMigrated} {
		t.Run(name, func(t *testing.T) {
			h := newPauseHome(t, apppayload.Database{Version: "v0.3.0", State: state, From: "v0.2.0", Dump: "/b/d.dump", SchemaBefore: 230})
			h.f.paused = true // the marker the kill left
			if err := h.start(t); err != nil {
				t.Fatal(err)
			}
			want := "resume migrated"
			if state == apppayload.DatabaseMigrating {
				want = "pause migrating|pause migrating|resume migrated" // the step's start, then the migrations'
			}
			h.check(t, want, false)
		})
	}
	t.Run("stopped while it migrates", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.f.onMigrate = cancel
		ps := appBundleGates(h.cfg, h.payload, testBundle(h.cfg), gateProcs(true), h.log)
		_ = prepareOf(t, ps, supervisor.RoleAPI)(ctx, noReport)
		h.check(t, "pause migrating", true)
	})
	t.Run("a rollback's Down scripts", func(t *testing.T) {
		h := newPauseHome(t, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
		g := &migrationGate{stateDir: h.cfg.StateDir(), version: "v0.2.0", pg: testBundle(h.cfg), log: h.log}
		u := &updates{api: exec.Command("maktaba-api")}
		if err := u.migrateDown(context.Background(), nil, 230, g); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrated|down|resume migrated", false)
		// a Down script that fails leaves the schema part way: they stay out
		h.f.downErr = errors.New("boom")
		if err := u.migrateDown(context.Background(), nil, 230, g); err == nil {
			t.Fatal("a Down script that failed is said to have worked")
		}
		h.check(t, "pause migrated|down|resume migrated|pause migrated|down", true)
		// an update with no backup cannot be held: its record stays migrated, and the copies
		// stay out all the same, for the minute's look and at the release before's start
		if ok, err := layout.MarkRecordHeld(apppayload.DatabasePath(h.cfg.StateDir()), "v0.3.0", "watch_timeout"); ok || err != nil {
			t.Fatalf("held with no backup: %v %v", ok, err)
		}
		if schemaSettled(h.cfg.StateDir()) {
			t.Fatal("a schema part way is settled")
		}
		newPayload(t, h.payload, "v0.2.0")
		if err := h.start(t); !supervisor.Halted(err) {
			t.Fatalf("the release before: %v", err)
		}
		h.check(t, "pause migrated|down|resume migrated|pause migrated|down", true)
		// the failed release started again runs its migrations to their end: settled
		newPayload(t, h.payload, "v0.3.0")
		h.f.downErr = nil
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrated|down|resume migrated|pause migrated|down|resume migrated", false)
	})
	t.Run("Down scripts that work after some that failed", func(t *testing.T) {
		h := newPauseHome(t, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
		g := &migrationGate{stateDir: h.cfg.StateDir(), version: "v0.2.0", pg: testBundle(h.cfg), log: h.log}
		u := &updates{api: exec.Command("maktaba-api")}
		h.f.downErr = errors.New("boom")
		if err := u.migrateDown(context.Background(), nil, 230, g); err == nil {
			t.Fatal("a Down script that failed is said to have worked")
		}
		h.f.downErr = nil
		if err := u.migrateDown(context.Background(), nil, 230, g); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrated|down|pause migrated|down|resume migrated", false)
	})
	t.Run("another release's Down scripts failed", func(t *testing.T) {
		// what failed on v0.3.0's database says nothing of the one v0.4.0's record is of
		h := newPauseHome(t, apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseMigrated})
		if err := noteDownFailed(h.cfg.StateDir()); err != nil {
			t.Fatal(err)
		}
		for version, settled := range map[string]bool{"v0.3.0": false, "0.3.0": false, "v0.4.0": true} {
			if err := apppayload.WriteDatabase(h.cfg.StateDir(), apppayload.Database{Version: version, State: apppayload.DatabaseMigrated}); err != nil {
				t.Fatal(err)
			}
			if schemaSettled(h.cfg.StateDir()) != settled {
				t.Errorf("a Down script failed on v0.3.0's, the record is %s's: settled %v", version, !settled)
			}
		}
	})
}

// Nothing is done as if a pause or a resume that failed had worked, and a server that does not
// answer bounds the wait.
func TestAppBundleCopiesPauseThatFails(t *testing.T) {
	t.Run("the pause fails", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		h.f.pauseErr = errors.New("no space left on device")
		if err := h.start(t); err != nil {
			t.Fatalf("the update: %v", err)
		}
		h.check(t, "pause migrating", false)
		if !strings.Contains(h.log.String(), "level=error the machines' copies could not be paused") ||
			strings.Contains(h.log.String(), "copies are paused") {
			t.Fatalf("not said:\n%s", h.log)
		}
	})
	t.Run("the resume fails", func(t *testing.T) {
		h := newPauseHome(t, pausePrev)
		h.f.resumeErr = errors.New("server closed the connection")
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrating|resume migrated", true)
		h.f.resumeErr = nil
		if err := h.start(t); err != nil {
			t.Fatal(err)
		}
		h.check(t, "pause migrating|resume migrated|resume migrated", false)
	})
}
