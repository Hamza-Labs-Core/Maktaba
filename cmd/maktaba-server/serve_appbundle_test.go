package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/apppayload"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

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

func gateProcs(pg bool) []supervisor.Process {
	ps := []supervisor.Process{{Role: supervisor.RoleAPI}, {Role: supervisor.RolePipeline},
		{Role: supervisor.RolePipeline, Instance: "stt-1"}, {Role: supervisor.RoleStreaming}}
	if pg {
		ps = append([]supervisor.Process{{Role: supervisor.RolePostgres}}, ps...)
	}
	return ps
}

func prepareOf(ps []supervisor.Process, role supervisor.Role) func(context.Context, func(string)) error {
	for _, p := range ps {
		if p.Role == role {
			return p.Prepare
		}
	}
	return nil
}

func noReport(string) {}

// An update: the api waits for the dump of the database (named after the two
// versions, taken once), the pipeline for the venv; streaming waits on nothing.
func TestAppBundleUpdateDumpsFirst(t *testing.T) {
	cfg, payload := appEnv(t, "v0.3.0", "v0.2.0")
	pg := &pgbundle.Bundle{Backups: filepath.Join(cfg.StateDir(), "backups"), Data: filepath.Join(cfg.StateDir(), "postgres")}
	prev := backupAs
	t.Cleanup(func() { backupAs = prev })
	var dumps []string
	backupAs = func(_ context.Context, b pgbundle.Bundle, name string) (string, error) {
		dumps = append(dumps, name)
		return filepath.Join(b.Backups, name+".dump"), nil
	}
	ps := appBundleGates(cfg, payload, pg, gateProcs(true), &strings.Builder{})
	for _, p := range ps {
		if (p.Role == supervisor.RoleStreaming) != (p.Prepare == nil) {
			t.Fatalf("%s: prepare = %v", p.Name(), p.Prepare != nil)
		}
	}
	var details []string
	api := prepareOf(ps, supervisor.RoleAPI)
	if err := api(context.Background(), func(d string) { details = append(details, d) }); err != nil {
		t.Fatal(err)
	}
	if err := api(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 1 || dumps[0] != "pre-v0.2.0-to-v0.3.0" {
		t.Fatalf("dumps = %v (one, named after the update)", dumps)
	}
	if len(details) != 1 || !strings.Contains(details[0], "v0.3.0") {
		t.Fatalf("details = %v", details)
	}
}

// The same version (or a first start): no dump. A dump already there (a start
// that did not get to run healthy) is not taken again.
func TestAppBundleNoDumpWithoutAnUpdate(t *testing.T) {
	prev := backupAs
	t.Cleanup(func() { backupAs = prev })
	backupAs = func(context.Context, pgbundle.Bundle, string) (string, error) {
		t.Fatal("no dump without an update")
		return "", nil
	}
	for _, confirmed := range []string{"", "v0.3.0"} {
		cfg, payload := appEnv(t, "v0.3.0", confirmed)
		pg := &pgbundle.Bundle{Backups: filepath.Join(cfg.StateDir(), "backups")}
		ps := appBundleGates(cfg, payload, pg, gateProcs(true), &strings.Builder{})
		if err := prepareOf(ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
			t.Fatal(err)
		}
	}
	cfg, payload := appEnv(t, "v0.3.0", "v0.2.0")
	pg := &pgbundle.Bundle{Backups: filepath.Join(cfg.StateDir(), "backups")}
	if err := os.MkdirAll(pg.Backups, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pg.Backups, "pre-v0.2.0-to-v0.3.0.dump"), []byte("d"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := appBundleGates(cfg, payload, pg, gateProcs(true), &strings.Builder{})
	if err := prepareOf(ps, supervisor.RoleAPI)(context.Background(), noReport); err != nil {
		t.Fatal(err)
	}
}

// An older release than the one that ran is refused (its code does not know the
// newer migrations); so is a Postgres of another major than the cluster's.
func TestAppBundleRefusesADowngradeAndAnotherPostgresMajor(t *testing.T) {
	cfg, payload := appEnv(t, "v0.2.0", "v0.3.0")
	pg := &pgbundle.Bundle{Data: filepath.Join(cfg.StateDir(), "postgres")}
	ps := appBundleGates(cfg, payload, pg, gateProcs(true), &strings.Builder{})
	err := prepareOf(ps, supervisor.RoleAPI)(context.Background(), noReport)
	if err == nil || !strings.Contains(err.Error(), "older than the one that last ran here (v0.3.0)") {
		t.Fatalf("downgrade = %v", err)
	}

	pgGate := prepareOf(ps, supervisor.RolePostgres)
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
	ps := appBundleGates(cfg, payload, nil, gateProcs(false), &strings.Builder{})
	err := ps[1].Prepare(context.Background(), noReport)
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
			err := p.Prepare(context.Background(), func(d string) {
				mu.Lock()
				details = append(details, d)
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
	found := false
	for _, d := range details {
		found = found || d == "Installing the transcription tools: Collecting torch (41%)"
	}
	if !found {
		t.Fatalf("details = %v", details)
	}
}

// The payload is saved as the one that ran once every process ran healthy.
func TestConfirmAppPayloadAfterAHealthyRun(t *testing.T) {
	spawnsPrograms(t) // a real child under the supervisor
	t.Setenv("MAKTABA_CONFIRM_AFTER", "1ms")
	t.Setenv("MAKTABA_TEST_FAKE", "1")
	cfg, payload := appEnv(t, "v0.3.0", "v0.2.0")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := supervisor.Process{Role: supervisor.RoleAPI, Cmd: exec.Command(self, "run-until-stopped")}
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
