package main

// A TV held on its old streaming software stays there across the desktop app's update
// restart and every restart after it (story 35.6, the owner's decision of 2026-10-28; seen
// live on 2026-10-28, 0.3.37 -> 0.3.39: the app relaunched, the supervisor started the live
// slot unbound on the newly installed 0.3.39 binary, and when the TV change's check failed
// the live TV was on the new software already).
//
// End to end on one machine: a scratch Postgres migrated as maktaba-api migrates it
// (MAKTABA_TEST_PG_URL), the server's own TV slots (supervisor.BuildSlots, the supervisor
// and its kept-binary lookup as `maktaba-server serve` wires them: keptStreaming), and a
// fake streaming binary (this test binary) that beats tv_slots with its version as a slot
// does and says which file it runs. Each install kind that restarts its slots with an
// update: the Mac and Windows app (a payload kept under updates/payloads), the Linux
// runtime (its runtime directories).
//
// Opt-in: MAKTABA_TEST_PG_URL (a scratch Postgres whose user may create databases; never
// a live one). It never touches this machine's installs: every path is under t.TempDir(),
// and the slots listen on free ports, not the slots' own.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

const fakeSlotHost = "4c1b8f0e-6a2d-4e3f-9b7c-0d1e2f3a4b5c"

// runFakeSlot is the fake streaming binary's `slot` command (MAKTABA_TEST_FAKE_SLOT=1):
// it beats its tv_slots row with the version written beside it (VERSION: a release's
// binary says its own), writes which file runs to FAKE_SLOT_REPORT/<slot>.json, and serves
// /healthz on its admin address until it is stopped.
func runFakeSlot() (int, bool) {
	if os.Getenv("MAKTABA_TEST_FAKE_SLOT") != "1" {
		return 0, false
	}
	exe, err := os.Executable()
	if err != nil {
		return 90, true
	}
	ver, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "VERSION"))
	v := strings.TrimSpace(string(ver))
	slot := os.Getenv("MAKTABA_TV_SLOT")
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		return 91, true
	}
	beat := func() error {
		_, err := db.Exec(`INSERT INTO tv_slots (host_id, slot, state, version, pid, started_at, beat_at)
			VALUES ($1, $2, 'healthy', $3, $4, now(), now())
			ON CONFLICT (host_id, slot) DO UPDATE SET state = 'healthy', version = $3, pid = $4, beat_at = now()`,
			fakeSlotHost, slot, v, os.Getpid())
		return err
	}
	if err := beat(); err != nil {
		fmt.Fprintln(os.Stderr, "fake slot beat:", err)
		return 92, true
	}
	rep, _ := json.Marshal(map[string]any{"path": exe, "version": v, "pid": os.Getpid()})
	dir := os.Getenv("FAKE_SLOT_REPORT")
	if err := os.WriteFile(filepath.Join(dir, slot+".json.tmp"), rep, 0o600); err != nil ||
		os.Rename(filepath.Join(dir, slot+".json.tmp"), filepath.Join(dir, slot+".json")) != nil {
		return 93, true
	}
	go func() {
		for range time.Tick(time.Second) {
			_ = beat()
		}
	}()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if err := http.ListenAndServe(os.Getenv("MAKTABA_ADMIN_ADDR"), mux); err != nil {
		fmt.Fprintln(os.Stderr, "fake slot:", err)
		return 94, true
	}
	return 0, true
}

// scratchHome is a migrated database of its own on MAKTABA_TEST_PG_URL.
func scratchHome(t *testing.T) string {
	t.Helper()
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: a scratch Postgres whose user may create databases")
	}
	if testing.Short() {
		t.Skip("runs Postgres, builds maktaba-api and starts processes")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "tvheld_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if a, err := sql.Open("postgres", base); err == nil {
			_, _ = a.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = a.Exec(`DROP DATABASE IF EXISTS ` + name)
			_ = a.Close()
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pgtest.Migrator(t, t.TempDir())(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dsn
}

// tvInstall is one install kind's files: where its running payload's streaming binary is
// (the bundle), and how its update keeps the release before.
type tvInstall struct {
	name string
	// setup lays out the install running v (its bundle) and sets its environment.
	setup func(t *testing.T, v string) (bundleBin string)
	// update installs v over the running one, keeping the release before as the install
	// kind keeps it; it returns the kept streaming binary.
	update func(t *testing.T, from, to string) (bundleBin, kept string)
}

// writeStreaming puts the fake streaming binary of version v in dir.
func writeStreaming(t *testing.T, dir, v string) string {
	t.Helper()
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, layout.Exe("maktaba-streaming"))
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// appInstall is the Mac and Windows app: the payload in the app (Contents/Resources/server,
// <install>\server), the one before kept under <launcher root>/updates/payloads/<v> with
// its .kept marker (updater.AppInstaller).
func appInstall(t *testing.T) tvInstall {
	root := t.TempDir()
	payload := filepath.Join(root, "Maktaba.app", "Contents", "Resources", "server")
	lroot := filepath.Join(root, "launcher")
	return tvInstall{name: "app",
		setup: func(t *testing.T, v string) string {
			t.Setenv("MAKTABA_APP_BUNDLE", payload)
			t.Setenv(envLauncherRoot, lroot)
			return writeStreaming(t, filepath.Join(payload, "bin"), v)
		},
		update: func(t *testing.T, from, to string) (string, string) {
			keptDir := filepath.Join(lroot, "updates", "payloads", strings.TrimPrefix(from, "v"))
			kept := writeStreaming(t, filepath.Join(keptDir, "bin"), from)
			if err := os.WriteFile(filepath.Join(keptDir, ".kept"), []byte(strings.TrimPrefix(from, "v")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return writeStreaming(t, filepath.Join(payload, "bin"), to), kept
		},
	}
}

// runtimeInstall is the Linux runtime: one directory per version under the runtime root,
// each with its .installed record (appruntime), the bundle the current one.
func runtimeInstall(t *testing.T) tvInstall {
	rroot := t.TempDir()
	dirOf := func(v string) string { return filepath.Join(rroot, strings.TrimPrefix(v, "v")) }
	install := func(t *testing.T, v string) string {
		bin := writeStreaming(t, filepath.Join(dirOf(v), "bin"), v)
		rec, _ := json.Marshal(map[string]any{"version": strings.TrimPrefix(v, "v"), "installed_at": time.Now().UTC()})
		if err := os.WriteFile(filepath.Join(dirOf(v), ".installed"), rec, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("MAKTABA_APP_BUNDLE", dirOf(v))
		t.Setenv(envLauncherRoot, filepath.Join(rroot, "launcher"))
		return bin
	}
	return tvInstall{name: "runtime",
		setup: install,
		update: func(t *testing.T, from, to string) (string, string) {
			kept := filepath.Join(dirOf(from), "bin", layout.Exe("maktaba-streaming"))
			return install(t, to), kept
		},
	}
}

// slotReport is what a fake slot said it runs.
type slotReport struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// serveSlots runs the TV slots as `maktaba-server serve` does on a home at version v
// (BuildSlots, the install's kept binaries) until both boot slots said what they run, then
// stops them: what each ran, and the supervisor's log.
func serveSlots(t *testing.T, dsn, v, bundleBin string) (map[string]slotReport, string) {
	t.Helper()
	old := version.Version
	version.Version = v
	defer func() { version.Version = old }()
	var cfg config.Config
	cfg.Database.URL = dsn
	cfg.Server.StreamAddr = ":8081"
	cfg.Server.DataDir = t.TempDir()
	procs, err := supervisor.BuildSlots(supervisor.Options{Config: cfg, BinDir: filepath.Dir(bundleBin)})
	if err != nil {
		t.Fatal(err)
	}
	report := t.TempDir()
	for i := range procs {
		// never the slots' own ports: a Maktaba running on this machine listens there
		addr := freeLoopback(t)
		p := &procs[i]
		p.Cmd.Env = append(p.Cmd.Env, "MAKTABA_TEST_FAKE_SLOT=1", "FAKE_SLOT_REPORT="+report, "MAKTABA_ADMIN_ADDR="+addr)
		p.Probe = "http://" + addr + "/healthz"
	}
	var opts []supervisor.Option
	if kept := keptStreaming(cfg); kept != nil {
		opts = append(opts, supervisor.WithKeptStreaming(kept))
	}
	log := &lockedLog{}
	policy := supervisor.DefaultPolicy()
	policy.ProbeInterval, policy.BackoffInitial = 100*time.Millisecond, 100*time.Millisecond
	sup := supervisor.New(procs, policy, append(opts, supervisor.WithLog(log))...)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx, 5*time.Second) }()
	got := map[string]slotReport{}
	deadline := time.Now().Add(30 * time.Second)
	for len(got) < 2 {
		for _, s := range []string{"blue", "green"} {
			b, err := os.ReadFile(filepath.Join(report, s+".json"))
			var r slotReport
			if err == nil && json.Unmarshal(b, &r) == nil {
				got[s] = r
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the slots did not start: %v\n%s", got, log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the supervisor did not stop")
	}
	return got, log.String()
}

func freeLoopback(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestHeldTVStaysOnItsStreamingAcrossTheAppUpdate(t *testing.T) {
	for _, kind := range []func(*testing.T) tvInstall{appInstall, runtimeInstall} {
		in := kind(t)
		t.Run(in.name, func(t *testing.T) { heldAcrossUpdate(t, in) })
	}
}

func heldAcrossUpdate(t *testing.T, in tvInstall) {
	dsn := scratchHome(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the TV on the air: a release made before (from a source checkout it records 0.0.1,
	// as the owner's did), routed live; green live, blue the hot standby
	oldRel, newRel := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version) VALUES ($1, 'd-old', 68, '0.0.1')`, oldRel)
	exec(`INSERT INTO tv_routing (scope, live_release_id, live_gen) VALUES ('cluster', $1, 68)
		ON CONFLICT (scope) DO UPDATE SET live_release_id = $1, live_gen = 68`, oldRel)
	exec(`INSERT INTO tv_slots (host_id, slot, role) VALUES ($1, 'green', 'live'), ($1, 'blue', 'standby')`, fakeSlotHost)

	// 0.3.37 runs: its slots run its streaming binary and say so
	bin37 := in.setup(t, "v0.3.37")
	got, _ := serveSlots(t, dsn, "v0.3.37", bin37)
	if got["green"].Path != bin37 || got["green"].Version != "v0.3.37" {
		t.Fatalf("0.3.37's live slot runs %+v, want %s", got["green"], bin37)
	}

	// the app updates to 0.3.39 (the payload before kept) and relaunches: the update's TV
	// change is checked after this start, so until a change swaps it the live TV stays on
	// 0.3.37's streaming software
	bin39, kept := in.update(t, "v0.3.37", "v0.3.39")
	got, log := serveSlots(t, dsn, "v0.3.39", bin39)
	for _, s := range []string{"green", "blue"} {
		if got[s].Path != kept || got[s].Version != "v0.3.37" {
			t.Fatalf("after the update restart %s runs %+v, want the kept 0.3.37 binary %s\n%s", s, got[s], kept, log)
		}
	}

	// its check failed: the change is held (Retry / Drop on the Updates page) ...
	exec(`INSERT INTO tv_changes (state, title, source, kinds, verify_report) VALUES ('draft',
		'Maktaba 0.3.39: the TV''s streaming software', '{"kind": "auto_update", "version": "v0.3.39"}', '["binary"]',
		'{"checks": [{"check": "ts.conformity", "result": "fail"}]}')`)
	// ... and a plain restart (or the next app start, a Drop) keeps the live TV on 0.3.37
	got, log = serveSlots(t, dsn, "v0.3.39", bin39)
	if got["green"].Path != kept || got["green"].Version != "v0.3.37" {
		t.Fatalf("after a restart with the TV held the live slot runs %+v, want %s\n%s", got["green"], kept, log)
	}

	// TV staging off: no checked change moves the TV, so the restart puts the new software on
	exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.tv_staging', '{"enabled": false}', now())
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	got, _ = serveSlots(t, dsn, "v0.3.39", bin39)
	if got["green"].Path != bin39 {
		t.Fatalf("staging off: the live slot runs %+v, want %s", got["green"], bin39)
	}
	exec(`DELETE FROM app_settings WHERE key = 'rules.tv_staging'`)
	exec(`UPDATE tv_slots SET version = 'v0.3.37' WHERE host_id = $1`, fakeSlotHost)

	// the held release's binary is gone (pruned by hand): said loudly, the slots run 0.3.39's
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	got, log = serveSlots(t, dsn, "v0.3.39", bin39)
	if got["green"].Path != bin39 {
		t.Fatalf("the held binary missing: the live slot runs %+v, want %s", got["green"], bin39)
	}
	if !strings.Contains(log, "level=error the live TV is held on streaming v0.3.37") {
		t.Fatalf("the missing held binary is not said loudly:\n%s", log)
	}

	exec(`UPDATE tv_slots SET version = 'v0.3.37' WHERE host_id = $1`, fakeSlotHost)
	// a Retry that passes swaps the TV to a release recording 0.3.39's streaming: no hold
	exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version) VALUES ($1, 'd-new', 69, 'v0.3.39')`, newRel)
	exec(`UPDATE tv_routing SET live_release_id = $1, live_gen = 69 WHERE scope = 'cluster'`, newRel)
	got, log = serveSlots(t, dsn, "v0.3.39", bin39)
	if got["green"].Path != bin39 || strings.Contains(log, "held on streaming") {
		t.Fatalf("after the swap the live slot runs %+v, want %s\n%s", got["green"], bin39, log)
	}
}

// Which streaming software the TV is held on, on a migrated home: the live slot's own
// report while it is an older release than the one starting, and only while a checked
// change is the TV's way to new software.
func TestHeldStreamingVersionOnAHome(t *testing.T) {
	dsn := scratchHome(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	held := func(current string) string {
		t.Helper()
		v, err := supervisor.HeldStreamingVersion(context.Background(), db, current)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	exec(`INSERT INTO tv_slots (host_id, slot, role, version, beat_at) VALUES ($1, 'green', 'live', 'v0.3.37', now()),
		($1, 'blue', 'standby', 'v0.3.36', now())`, fakeSlotHost)
	if v := held("v0.3.39"); v != "" {
		t.Fatalf("no TV release on the air: held on %q", v)
	}
	rel := uuid.NewString()
	exec(`INSERT INTO tv_releases (id, digest, sched_gen, streaming_version) VALUES ($1, 'd', 1, '0.0.1')`, rel)
	exec(`INSERT INTO tv_routing (scope, live_release_id) VALUES ('cluster', $1)
		ON CONFLICT (scope) DO UPDATE SET live_release_id = $1`, rel)
	for current, want := range map[string]string{
		"v0.3.39": "v0.3.37", // the update: the live slot's (not the standby's, nor the release's 0.0.1)
		"0.3.38":  "v0.3.37",
		"v0.3.37": "", // the held release itself (a launcher's fallback to it)
		"v0.3.36": "", // a fallback to an older release: it runs its own
		"dev":     "", // a source build
	} {
		if v := held(current); v != want {
			t.Errorf("starting %s: held on %q, want %q", current, v, want)
		}
	}
	exec(`UPDATE tv_releases SET streaming_version = 'v0.3.39' WHERE id = $1`, rel)
	if v := held("v0.3.39"); v != "" {
		t.Fatalf("the live release records 0.3.39's streaming: held on %q", v)
	}
	exec(`UPDATE tv_releases SET streaming_version = 'v0.3.37' WHERE id = $1`, rel)
	exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.tv_staging', '{"enabled": false}', now())
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	if v := held("v0.3.39"); v != "" {
		t.Fatalf("staging off: held on %q", v)
	}
	exec(`UPDATE app_settings SET value = '{"enabled": true}' WHERE key = 'rules.tv_staging'`)
	if v := held("v0.3.39"); v != "v0.3.37" {
		t.Fatalf("staging on: held on %q", v)
	}
}
