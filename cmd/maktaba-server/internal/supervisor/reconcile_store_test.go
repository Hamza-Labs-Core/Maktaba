package supervisor

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// T12 (story 51.04): the real store (slot 0175's SQLite twin), a real supervisor and
// fake workers: flipping transcription stops and starts its stt workers, and the other
// processes keep their pids.

// componentsBase are the older slots' tables slot 0175 touches (shared/components/store's
// test has the same).
const componentsBase = `
CREATE TABLE cluster_nodes (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
CREATE TABLE audit_log (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, action TEXT NOT NULL,
	actor_user_id TEXT, target_id TEXT, payload TEXT NOT NULL DEFAULT '{}',
	ts TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), actor_source TEXT, target_kind TEXT);
CREATE TABLE job_stages (stage TEXT PRIMARY KEY, label TEXT NOT NULL, subject_kinds TEXT NOT NULL,
	pool TEXT NOT NULL, lane TEXT NOT NULL, lane_order INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
	debounce_sec INTEGER NOT NULL DEFAULT 0, debounce_max_sec INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 3, edges TEXT NOT NULL DEFAULT '[]', may_request TEXT NOT NULL DEFAULT '[]',
	entry_for TEXT NOT NULL DEFAULT '[]', legacy INTEGER NOT NULL DEFAULT 0, hidden INTEGER NOT NULL DEFAULT 0,
	external_write INTEGER NOT NULL DEFAULT 0, write_mode TEXT NOT NULL DEFAULT 'auto',
	placement TEXT NOT NULL DEFAULT 'any', enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, stage TEXT NOT NULL);
`

// componentsDB is a fresh SQLite file with slot 0175's twin applied.
func componentsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	stmts := strings.Split(componentsBase, ";\n")
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "shared", "db", "migrations", "0175_components.sqlite.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	for _, block := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
			stmts = append(stmts, block[:i])
		}
	}
	for _, s := range stmts {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	return db
}

func TestReconcileStoreFlipsWorkers(t *testing.T) {
	api, _ := fakeProc(t, RoleAPI, "serve")
	stt1, _ := fakeProc(t, RolePipeline, "serve", "FAKE_ON_STOP=exit")
	stt1.Instance = "stt-1"
	stt2, _ := fakeProc(t, RolePipeline, "serve", "FAKE_ON_STOP=exit")
	stt2.Instance = "stt-2"
	s := New([]Process{api, stt1, stt2}, testPolicy(), WithLog(io.Discard))
	st := store.New(componentsDB(t))
	poll := func(time.Duration) <-chan time.Time { return time.After(20 * time.Millisecond) }
	r := NewReconciler(s)
	r.Source, r.Status, r.After, r.Tick = st, st, poll, 20*time.Millisecond
	r.Log, r.CachePath = io.Discard, filepath.Join(t.TempDir(), CompCacheFile)
	r.Prime()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running(t, s)
	go r.Run(ctx)
	pids := func(sn Snapshot) map[string]int {
		m := map[string]int{}
		for _, p := range sn.Processes {
			m[string(p.Role)+"/"+p.Instance] = p.PID
		}
		return m
	}
	before := pids(waitFor(t, s, "all running", func(sn Snapshot) bool {
		for _, p := range sn.Processes {
			if p.State != StateRunning {
				return false
			}
		}
		return true
	}))
	audit := store.Audit{By: "admin", ActorSource: "admin", Action: "component.disable"}
	if _, err := st.SetDesired(ctx, store.Desired{ID: "transcription", Want: components.WantInstalled}, audit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "workers stopped", func(sn Snapshot) bool {
		for _, p := range sn.Processes {
			if p.Role == RolePipeline && p.PID != 0 {
				return false
			}
		}
		return true
	})
	if got := pids(s.Snapshot())["api/"]; got != before["api/"] {
		t.Fatalf("api pid %d, was %d", got, before["api/"])
	}
	waitStatus(t, st, "transcription", components.PhaseOff)
	if row := statusOf(t, st, "core.api"); row.Phase != components.PhaseRunning {
		t.Fatalf("core.api %+v", row)
	}
	audit.Action = "component.enable"
	if _, err := st.SetDesired(ctx, store.Desired{ID: "transcription", Want: components.WantEnabled}, audit); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "workers running", func(sn Snapshot) bool {
		n := 0
		for _, p := range sn.Processes {
			if p.Role == RolePipeline && p.State == StateRunning {
				n++
			}
		}
		return n == 2
	})
	waitStatus(t, st, "transcription", components.PhaseRunning)
	if got := pids(s.Snapshot())["api/"]; got != before["api/"] {
		t.Fatalf("api pid %d, was %d", got, before["api/"])
	}
}

func statusOf(t *testing.T, st *store.Store, id string) store.Status {
	t.Helper()
	rows, err := st.StatusOf(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Part == StatusPart {
			return r
		}
	}
	return store.Status{}
}

func waitStatus(t *testing.T, st *store.Store, id, phase string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		row := statusOf(t, st, id)
		if row.Phase == phase {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is %q, want %q (%+v)", id, row.Phase, phase, row)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
