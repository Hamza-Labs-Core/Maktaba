package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	mode := os.Getenv("MAKTABA_TEST_APPBUNDLE_MIGRATE")
	if mode == "" {
		return
	}
	if strings.Join(os.Args[1:], " ") != "migrate --timeout 0 --lock-timeout "+migrateLockTimeout+" up" {
		fmt.Fprintf(os.Stderr, "unexpected %q\n", os.Args[1:])
		os.Exit(2)
	}
	switch mode {
	case "ok":
		fmt.Println("migrate: applying migrations")
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
}

func installFakeDB(t *testing.T, cfg config.Config) *fakeDB {
	t.Helper()
	f := &fakeDB{cfg: cfg}
	prevD, prevR, prevM, prevA := dumpBeforeUpdate, restoreDump, migrateUp, dumpAsideBundled
	t.Cleanup(func() { dumpBeforeUpdate, restoreDump, migrateUp, dumpAsideBundled = prevD, prevR, prevM, prevA })
	dumpAsideBundled = func(_ context.Context, _ pgbundle.Bundle, path string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.asides = append(f.asides, filepath.Base(path))
		return os.WriteFile(path, []byte("aside"), 0o600)
	}
	dumpBeforeUpdate = func(_ context.Context, b pgbundle.Bundle, from, to string, keep int) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.dumps = append(f.dumps, orUnknown(from)+"->"+to)
		f.keep = keep
		return filepath.Join(b.Backups, pgbundle.UpdateDumpName(from, to)), nil
	}
	migrateUp = func(ctx context.Context, _ *exec.Cmd, _ io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		f.migrates = append(f.migrates, rec.State)
		block, err := f.block, f.migrateErr
		f.mu.Unlock()
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return err
	}
	restoreDump = func(_ context.Context, _ pgbundle.Bundle, dump string, _ io.Writer) error {
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		f.mu.Lock()
		defer f.mu.Unlock()
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

// A failed update: the dump is put back (with the record saying so while it
// is), the record then names the release from before, every process halts
// with what happened; the previous release installed again starts, the failed
// one started again tries again.
func TestAppBundleFailedMigrationPutsTheDumpBack(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.migrateErr = errors.New(`ERROR 0144_x.sql: relation "videos" does not exist`)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) {
		t.Fatalf("a failed update = %v, want a halt", err)
	}
	for _, want := range []string{"the update to v0.3.0 could not change the library database", `relation "videos"`,
		"back as it was before the update", "pre-v0.2.0-to-v0.3.0.dump", "install v0.2.0 again", "tries this update again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("detail %q lacks %q", err, want)
		}
	}
	if len(f.restores) != 1 || f.restores[0] != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("restores = %v", f.restores)
	}
	rec := recorded(t, cfg)
	if rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseRestored || rec.Failed != "v0.3.0" {
		t.Fatalf("record = %+v", rec)
	}
	// the other processes halt with it, nothing runs again in this run
	if err := prepareOf(t, ps, supervisor.RoleStreaming)(context.Background(), noReport); !supervisor.Halted(err) {
		t.Fatalf("streaming = %v", err)
	}
	if d, m, r := f.counts(); d != 1 || m != 1 || r != 1 {
		t.Fatalf("%d dumps, %d migrations, %d restores", d, m, r)
	}

	// v0.2.0 installed again: the database is its own, it starts
	newPayload(t, payload, "v0.2.0")
	f.migrateErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the previous release again: %v", err)
	}
	if rec := recorded(t, cfg); rec.Version != "v0.2.0" || rec.State != apppayload.DatabaseMigrated {
		t.Fatalf("record = %+v", rec)
	}
	// v0.3.0 again: the update is tried again, from a fresh dump
	newPayload(t, payload, "v0.3.0")
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if len(f.dumps) != 2 || recorded(t, cfg).Version != "v0.3.0" {
		t.Fatalf("dumps = %v, record %+v", f.dumps, recorded(t, cfg))
	}
}

// Putting the dump back failed: a halt that says so, the record still says
// restoring; the next start puts it back first (and, the failed release
// again, stops there).
func TestAppBundleFailedRestoreIsFinishedNextStart(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "")
	record(t, cfg, apppayload.Database{Version: "v0.2.0", State: apppayload.DatabaseMigrated})
	f := installFakeDB(t, cfg)
	f.migrateErr = errors.New("ERROR 0144_x.sql")
	f.restoreErr = errors.New("pg_restore: disk full")
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || !strings.Contains(err.Error(), "failed too: pg_restore: disk full") ||
		!strings.Contains(err.Error(), "next start tries to put it back again") {
		t.Fatalf("a failed restore = %v", err)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseRestoring || rec.Failed != "v0.3.0" {
		t.Fatalf("record = %+v", rec)
	}
	f.restoreErr = nil
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	err = prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport)
	if !supervisor.Halted(err) || !strings.Contains(err.Error(), "back as it was before the update") {
		t.Fatalf("the next start = %v", err)
	}
	if d, m, r := f.counts(); d != 1 || m != 1 || r != 2 {
		t.Fatalf("%d dumps, %d migrations, %d restores (the restore again, no new try)", d, m, r)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseRestored || rec.Version != "v0.2.0" {
		t.Fatalf("record = %+v", rec)
	}
}

// D2 (review A7): a putting back the launcher's fallback asked for (its deadline, a boot
// loop) sets the failed state aside first, once; one the update's own rollback already
// set aside is not dumped again.
func TestAppBundleRestoreSetsTheFailedStateAside(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "")
	backups := filepath.Join(cfg.StateDir(), "backups")
	_ = os.MkdirAll(backups, 0o700)
	dump := filepath.Join(backups, "pre-v0.2.0-to-v0.3.0.dump")
	_ = os.WriteFile(dump, []byte("dump"), 0o600)
	restoring := apppayload.Database{Version: "v0.3.0", State: apppayload.DatabaseRestoring, From: "v0.2.0",
		Dump: dump, Failed: "v0.3.0", Error: "launcher_deadline"}
	record(t, cfg, restoring)
	f := installFakeDB(t, cfg)
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("= %v", err)
	}
	if len(f.asides) != 1 || !strings.HasPrefix(f.asides[0], "failed-v0.3.0-") || len(f.restores) != 1 {
		t.Fatalf("asides %v restores %v", f.asides, f.restores)
	}
	// set aside already (the update's rollback did it): not again
	record(t, cfg, restoring)
	ps = appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	if err := prepareOf(t, ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("= %v", err)
	}
	if len(f.asides) != 1 || len(f.restores) != 2 {
		t.Fatalf("asides %v restores %v", f.asides, f.restores)
	}
}

// A start cut off while migrating goes on at the next one with the dump from
// before (none taken of the half-migrated database), and puts that dump back
// when the migrations fail.
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
	if d, m, r := f.counts(); d != 0 || m != 1 || r != 1 || f.restores[0] != "pre-v0.2.0-to-v0.3.0.dump" {
		t.Fatalf("%d dumps, %d migrations, restores %v", d, m, f.restores)
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

// The detail codes are a fixed list (the app's screens translate them): a code
// added, gone or with other params must be added here too, and to the list in
// contract-update.md.
func TestDetailCodesAreAFixedList(t *testing.T) {
	want := map[string]string{
		"db_waiting":               "",
		"db_checking":              "release",
		"db_dumping":               "from to",
		"db_migrating":             "to",
		"db_restoring":             "failed dump",
		"db_restored":              "failed error dump release",
		"db_refused_older":         "release needed dump",
		"db_ahead":                 "release error",
		"db_dump_failed":           "to error",
		"db_migrate_failed":        "release error",
		"db_restore_failed":        "failed error dump restore_error",
		"db_restore_unfinished":    "failed dump",
		"db_restoring_backup":      "dump by",
		"db_restore_backup_failed": "dump error",
		"db_no_cluster":            "failed error dump",
		"db_record_failed":         "path error",
		"pg_unreadable":            "path error",
		"pg_major":                 "cluster payload",
		"venv_waiting":             "",
		"venv_building":            "step done total percent message",
		"venv_interrupted":         "log",
		"venv_failed":              "error log",
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
		Dump: "/b/pre-v0.2.0-to-v0.3.0.dump", Failed: "v0.3.0", Error: "ERROR 0144"})
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
	if err := runMigrateUp(context.Background(), api("ok"), &log); err != nil || !strings.Contains(log.String(), "applying") {
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
		// the watch gave up on this start meanwhile (start_timeout): the restore is marked
		rec, _, _ := apppayload.ReadDatabase(cfg.StateDir())
		rec.State, rec.Failed = apppayload.DatabaseRestoring, rec.Version
		return apppayload.WriteDatabase(cfg.StateDir(), rec)
	}
	ps := appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard)
	var mu sync.Mutex
	var said []string
	err := checkedStep(t, prepareOf(t, ps, supervisor.RoleAPI))(context.Background(), func(d supervisor.Detail) {
		mu.Lock()
		said = append(said, d.Text)
		mu.Unlock()
	})
	if d := supervisor.DetailOf(err); err == nil || d.Code != "db_restoring" {
		t.Fatalf("a start given up on: %v", err)
	}
	grew := false
	for _, s := range said {
		if strings.Contains(s, "MB so far") {
			grew = true
		}
	}
	if !grew {
		t.Fatalf("the dump never said its size: %q", said)
	}
	if rec := recorded(t, cfg); rec.State != apppayload.DatabaseRestoring || rec.Failed != "v0.3.0" {
		t.Fatalf("recorded over the restore: %+v", rec)
	}
}

// The owner's request (update-attempts.md): a release's start says what it did with the
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
	if len(got) != 1 || got[0].To != "0.3.0" || got[0].From != "0.2.0" || got[0].Outcome != attempts.PutBack ||
		got[0].Code != "db_migrate_failed" || got[0].Step != "migrate" || !strings.Contains(got[0].Detail, "videos") ||
		got[0].EndedAt == nil || got[0].Source != attempts.SourceServer {
		t.Fatalf("after the failed update: %+v", got)
	}

	// the release before, back: the attempt says what happened to the one that failed
	newPayload(t, payload, "v0.2.0")
	f.migrateErr = nil
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Read(dir); len(got) != 1 || got[0].Outcome != attempts.PutBack {
		t.Fatalf("the release before: %+v", got)
	}

	// tried again, installed: a new attempt, the failed one kept
	newPayload(t, payload, "v0.3.0")
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(ctx, noReport); err != nil {
		t.Fatal(err)
	}
	got = attempts.Read(dir)
	if len(got) != 2 || got[0].Outcome != attempts.Installed || got[0].Code != "" || got[1].Outcome != attempts.PutBack ||
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

// A rollback that marked the dump to be put back (the watch gave up: start_timeout):
// the release before puts it back at its start, and the attempt at the failed release
// reads put back, with the rollback's reason as its code.
func TestAppBundleRollbackRestoreAttempt(t *testing.T) {
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
	installFakeDB(t, cfg)
	if err := prepareOf(t, appBundleGates(cfg, payload, testBundle(cfg), gateProcs(true), io.Discard),
		supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatalf("the release before puts the dump back and starts: %v", err)
	}
	got := attempts.Read(dir)
	if len(got) != 1 || got[0].To != "0.3.0" || got[0].From != "0.2.0" || got[0].Outcome != attempts.PutBack ||
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
