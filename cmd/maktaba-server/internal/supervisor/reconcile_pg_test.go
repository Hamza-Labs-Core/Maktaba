package supervisor

import (
	"context"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

// Story 51.04 AC1 and AC2 against the home's real Postgres: the migrated schema, the
// store, NOTIFY components_changed heard through LISTEN (no polling: the fallback poll is
// an hour), a real supervisor and fake children. Enabling a component whose process was
// built dormant starts it without a restart of serve; disabling it asks it to stop and it
// finishes its work (a graceful exit, never a kill), the others keeping their pids.
//
// Opt-in: MAKTABA_TEST_PG_URL (a scratch Postgres whose user may create databases).

// scratchComponentsDB is a migrated database of its own on MAKTABA_TEST_PG_URL.
func scratchComponentsDB(t *testing.T) string {
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
	defer func() { _ = admin.Close() }()
	name := "reconcile_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pgtest.Migrator(t, t.TempDir())(ctx, u.String()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return u.String()
}

func TestReconcilePostgresListenEnablesAndDisablesLive(t *testing.T) {
	dsn := scratchComponentsDB(t)
	api, _ := fakeProc(t, RoleAPI, "serve")
	llm, _ := fakeProc(t, RoleOllama, "serve", "FAKE_ON_STOP=exit")
	llm.Dormant = true // server.toml leaves it out: built for the reconciler
	s := New([]Process{api, llm}, testPolicy(), WithLog(io.Discard))
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st := store.New(db)
	r := NewReconciler(s)
	r.Source, r.Status = st, st
	r.NewListener = func(onError func(error)) (store.Listener, error) { return pqlisten.New(dsn, onError) }
	// no poll to fall back on within the test: what changes it is heard through LISTEN
	r.After = func(time.Duration) <-chan time.Time { return time.After(time.Hour) }
	r.Log, r.CachePath = io.Discard, filepath.Join(t.TempDir(), CompCacheFile)
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running(t, s)
	go r.Run(ctx)
	sn := waitFor(t, s, "the api running", func(sn Snapshot) bool { return proc(sn, RoleAPI).State == StateRunning })
	apiPID := proc(sn, RoleAPI).PID
	time.Sleep(300 * time.Millisecond) // the reconciler's first pass and its LISTEN
	if p := proc(s.Snapshot(), RoleOllama); p.PID != 0 {
		t.Fatalf("the dormant model server runs before it is enabled: %+v", p)
	}

	// AC1: enabled, it starts live
	audit := store.Audit{By: "admin", ActorSource: "admin", Action: "component.enable"}
	if _, err := st.SetDesired(ctx, store.Desired{ID: "llm", Want: components.WantEnabled}, audit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "the model server started by its enable", func(sn Snapshot) bool {
		return proc(sn, RoleOllama).State == StateRunning
	})
	waitStatus(t, st, "llm", components.PhaseRunning)

	// AC2: disabled, it is asked to stop and ends its work itself
	audit.Action = "component.disable"
	if _, err := st.SetDesired(ctx, store.Desired{ID: "llm", Want: components.WantInstalled}, audit); err != nil {
		t.Fatal(err)
	}
	sn = waitFor(t, s, "the model server stopped by its disable", func(sn Snapshot) bool {
		return proc(sn, RoleOllama).PID == 0 && proc(sn, RoleOllama).LastExit != nil
	})
	if e := proc(sn, RoleOllama).LastExit; e.Reason != ReasonStopped || e.Code != 0 || e.Signal != "" {
		t.Fatalf("its stop %+v: the work is lost unless it ends on its own", *e)
	}
	waitStatus(t, st, "llm", components.PhaseOff)
	if got := proc(s.Snapshot(), RoleAPI).PID; got != apiPID {
		t.Fatalf("api pid %d, was %d: serve restarted", got, apiPID)
	}
}
