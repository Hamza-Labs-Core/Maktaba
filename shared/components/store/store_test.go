package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // the SQLite driver

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// base are the older slots' tables slot 0173 and the store touch (the columns they
// read; the rest of each table left out).
const base = `
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

// upStatements are the goose Up statements of a migration file.
func upStatements(t *testing.T, sqlText string) []string {
	t.Helper()
	up := sqlText
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	var out []string
	for _, block := range strings.Split(up, "-- +goose StatementBegin") {
		if i := strings.Index(block, "-- +goose StatementEnd"); i >= 0 {
			if s := strings.TrimSpace(block[:i]); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func migration(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sqliteDB is a fresh SQLite file with slot 0173's twin applied (twice: it is idempotent).
func sqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, p := range []string{"PRAGMA synchronous = OFF", "PRAGMA journal_mode = MEMORY", "PRAGMA foreign_keys = ON"} {
		if _, err := db.Exec(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range strings.Split(base, ";\n") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
	twin := migration(t, "0173_components.sqlite.sql")
	for range 2 {
		for _, s := range upStatements(t, twin) {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("0173: %v\n%s", err, s)
			}
		}
	}
	return db
}

var ctx = context.Background()

func audit(action string) Audit {
	return Audit{By: "admin", ActorSource: "admin", Action: action}
}

// T02 (SQLite): the twin applies twice; an update moves the generation by one and the
// revision; the stage row is there.
func TestSQLiteTwinRevisionAndGeneration(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	rev0, err := s.Revision(ctx)
	if err != nil || rev0 != 0 {
		t.Fatalf("revision = %d, %v", rev0, err)
	}
	d, err := s.SetDesired(ctx, Desired{ID: "transcription", Want: components.WantEnabled}, audit("component.enable"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Generation != 1 {
		t.Fatalf("first generation %d", d.Generation)
	}
	d, err = s.SetDesired(ctx, Desired{ID: "transcription", Want: components.WantInstalled}, audit("component.disable"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Generation != 2 || d.Want != components.WantInstalled {
		t.Fatalf("after update: %+v", d)
	}
	if rev, _ := s.Revision(ctx); rev != 2 {
		t.Fatalf("revision = %d, want 2 (one per change)", rev)
	}
	if _, err := db.Exec(`DELETE FROM components WHERE id = 'transcription'`); err != nil {
		t.Fatal(err)
	}
	if rev, _ := s.Revision(ctx); rev != 3 {
		t.Fatalf("revision after delete = %d, want 3", rev)
	}
	var stage, pool, placement string
	if err := db.QueryRow(`SELECT stage, pool, placement FROM job_stages WHERE stage = 'component_catchup'`).Scan(&stage, &pool, &placement); err != nil {
		t.Fatal(err)
	}
	if pool != "cpu" || placement != "home" {
		t.Fatalf("catch-up stage: %s %s", pool, placement)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM component_meta`).Scan(&n)
	if n != 1 {
		t.Fatalf("component_meta has %d rows", n)
	}
}

// T13 (SQLite side): a desired write and its audit row go together, before and after.
func TestSetDesiredAudits(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	at := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if _, err := s.SetDesired(ctx, Desired{ID: "llm", Want: components.WantEnabled}, audit("component.enable")); err != nil {
		t.Fatal(err)
	}
	d, err := s.SetDesired(ctx, Desired{ID: "llm", Want: components.WantEnabled, Hold: &Hold{By: "admin", At: at}}, audit("component.hold"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Hold == nil || d.Hold.By != "admin" || !d.Hold.At.Equal(at) {
		t.Fatalf("hold: %+v", d.Hold)
	}
	rows, err := db.Query(`SELECT category, action, target_kind, target_id, payload FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var cat, action, kind, target, payload string
		if err := rows.Scan(&cat, &action, &kind, &target, &payload); err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatal(err)
		}
		if cat != AuditCategory || kind != "component" || target != "llm" {
			t.Fatalf("audit row %s %s %s", cat, kind, target)
		}
		if _, ok := p["before"]; !ok {
			t.Fatalf("payload without before: %s", payload)
		}
		got = append(got, action)
	}
	if strings.Join(got, ",") != "component.enable,component.hold" {
		t.Fatalf("audit actions %v", got)
	}
	if _, err := s.SetDesired(ctx, Desired{ID: "llm", Want: "on"}, audit("component.enable")); err == nil {
		t.Fatal("a bad want was written")
	}
	if _, err := s.SetDesired(ctx, Desired{ID: "llm", Want: "enabled"}, Audit{Action: "component.enable"}); err == nil {
		t.Fatal("a change with no one behind it was written")
	}
}

// T06 (SQLite side): a node's answer is the home's rows with its overrides.
func TestDesiredMergesOverrides(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	const node = "11111111-1111-1111-1111-111111111111"
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id) VALUES ($1)`, node); err != nil {
		t.Fatal(err)
	}
	for _, d := range []Desired{{ID: "transcription", Want: "enabled"}, {ID: "llm", Want: "enabled"}} {
		if _, err := s.SetDesired(ctx, d, audit("component.enable")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetOverride(ctx, Override{Node: node, ID: "transcription", Want: "installed"}, audit("component.disable")); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Desired(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	if got["transcription"].Want != "installed" || got["llm"].Want != "enabled" || len(got) != 2 {
		t.Fatalf("merged: %+v", got)
	}
	home, _, _ := s.Desired(ctx, Home)
	if home["transcription"].Want != "enabled" {
		t.Fatalf("home: %+v", home)
	}
	gen := got["transcription"].Generation
	// clearing the override keeps the row: the home's wish again, the generation still up
	if err := s.SetOverride(ctx, Override{Node: node, ID: "transcription"}, audit("component.follow_home")); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Desired(ctx, node)
	if got["transcription"].Want != "enabled" || got["transcription"].Generation <= gen {
		t.Fatalf("after clearing: %+v (generation was %d)", got["transcription"], gen)
	}
	// a node removed takes its overrides with it
	if _, err := db.Exec(`DELETE FROM cluster_nodes WHERE id = $1`, node); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM component_overrides`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d overrides left after the node went", n)
	}
}

func TestReportStatusPrunesAndKeepsSince(t *testing.T) {
	db := sqliteDB(t)
	s := &Store{DB: db, Version: "0.3.66"}
	rows := []Status{
		{ID: "livetv", Phase: components.PhaseRunning, ObservedGeneration: 3,
			Conditions: []Condition{{Type: components.CondReady, Status: "true"}}},
		{ID: "cluster", Phase: components.PhaseWaiting,
			Conditions: []Condition{{Type: components.CondRequirementsMet, Status: "false", Reason: "dependency_off",
				Params: map[string]any{"component": "llm"}}}},
	}
	if err := s.ReportStatus(ctx, Home, "api", rows); err != nil {
		t.Fatal(err)
	}
	// another part's row is never touched by this part's report
	if err := s.ReportStatus(ctx, Home, "pipeline", []Status{{ID: "livetv", Phase: components.PhaseFailed}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.StatusOf(ctx, "livetv")
	if err != nil || len(first) != 2 {
		t.Fatalf("livetv rows %v, %v", first, err)
	}
	time.Sleep(5 * time.Millisecond)
	// T10's Go twin: the second report has fewer rows; the missing one is pruned
	if err := s.ReportStatus(ctx, Home, "api", rows[:1]); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Statuses(ctx)
	if len(all) != 2 {
		t.Fatalf("after prune %d rows: %+v", len(all), all)
	}
	// story 51.14: one machine's rows only
	if mine, err := s.StatusesOfMachine(ctx, Home); err != nil || len(mine) != 2 {
		t.Fatalf("home's rows %v, %v", mine, err)
	}
	if other, err := s.StatusesOfMachine(ctx, "another-node"); err != nil || len(other) != 0 {
		t.Fatalf("another machine's rows %v, %v", other, err)
	}
	again, _ := s.StatusOf(ctx, "livetv")
	var api Status
	for _, r := range again {
		if r.Part == "api" {
			api = r
		}
	}
	if api.Version != "0.3.66" || api.ObservedGeneration != 3 || len(api.Conditions) != 1 {
		t.Fatalf("api row %+v", api)
	}
	if !api.Since.Equal(first[0].Since) || !api.UpdatedAt.After(first[0].UpdatedAt) {
		t.Fatalf("since moved with no phase change (%v -> %v), or updated_at did not (%v -> %v)",
			first[0].Since, api.Since, first[0].UpdatedAt, api.UpdatedAt)
	}
	if got := Rollup(again); got.Phase != components.PhaseFailed {
		t.Fatalf("rollup %s", got.Phase)
	}
	// a big detail is stored as truncated
	big := map[string]any{"log": strings.Repeat("x", MaxDetailBytes)}
	if err := s.ReportStatus(ctx, Home, "api", []Status{{ID: "livetv", Phase: components.PhaseRunning, Detail: big}}); err != nil {
		t.Fatal(err)
	}
	again, _ = s.StatusOf(ctx, "livetv")
	for _, r := range again {
		if r.Part == "api" && r.Detail["truncated"] != true {
			t.Fatalf("detail not truncated: %v", r.Detail)
		}
	}
	// an empty report clears the part
	if err := s.ReportStatus(ctx, Home, "api", nil); err != nil {
		t.Fatal(err)
	}
	all, _ = s.Statuses(ctx)
	if len(all) != 1 || all[0].Part != "pipeline" {
		t.Fatalf("after an empty report: %+v", all)
	}
}

// T11: a reason or phase the catalog does not have is refused before any write.
func TestReportStatusRefusesUnknownCodes(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	cases := map[string]Status{
		"unknown reason": {ID: "llm", Phase: components.PhaseWaiting,
			Conditions: []Condition{{Type: components.CondReady, Status: "false", Reason: "nonsense"}}},
		"unknown phase": {ID: "llm", Phase: "sleeping"},
		"bad status":    {ID: "llm", Phase: components.PhaseRunning, Conditions: []Condition{{Type: components.CondReady, Status: "yes"}}},
		"no type":       {ID: "llm", Phase: components.PhaseRunning, Conditions: []Condition{{Status: "true"}}},
		"no id":         {Phase: components.PhaseRunning},
	}
	for name, row := range cases {
		err := s.ReportStatus(ctx, Home, "api", []Status{row})
		if err == nil {
			t.Errorf("%s: written", name)
		}
		if name == "unknown reason" && (err == nil || !strings.Contains(err.Error(), `unknown reason "nonsense"`)) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := s.ReportStatus(ctx, Home, "api", []Status{{ID: "a", Phase: "off"}, {ID: "a", Phase: "off"}}); err == nil {
		t.Error("a component reported twice by one part was written")
	}
	all, _ := s.Statuses(ctx)
	if len(all) != 0 {
		t.Fatalf("rows written: %+v", all)
	}
}

// T07: worst phase wins, with its part's reason.
func TestRollup(t *testing.T) {
	rows := []Status{
		{Machine: Home, ID: "livetv", Part: "api", Phase: components.PhaseRunning},
		{Machine: Home, ID: "livetv", Part: "pipeline", Phase: components.PhaseFailed,
			Conditions: []Condition{{Type: components.CondReady, Status: "false", Reason: "crash_loop"}}},
		{Machine: Home, ID: "livetv", Part: "streaming", Phase: components.PhaseDegraded},
	}
	got := Rollup(rows)
	if got.Phase != components.PhaseFailed || got.Reason() != "crash_loop" || got.Part != "" {
		t.Fatalf("Rollup = %+v", got)
	}
	order := []string{"failed", "waiting", "removing", "installing", "draining", "starting", "degraded",
		"stopped", "running", "off", "not_installed"}
	for i := 1; i < len(order); i++ {
		if r := Rollup([]Status{{Phase: order[i]}, {Phase: order[i-1]}}); r.Phase != order[i-1] {
			t.Errorf("%s vs %s: rollup %s", order[i-1], order[i], r.Phase)
		}
	}
	for _, p := range components.KnownPhases {
		if _, ok := rollupRank[p]; !ok {
			t.Errorf("phase %s has no rank", p)
		}
	}
	if r := Rollup([]Status{{Phase: "running"}, {Phase: "from_the_future"}}); r.Phase != "from_the_future" {
		t.Errorf("an unknown phase must rank worst, got %s", r.Phase)
	}
	all := RollupAll(append(rows, Status{Machine: "n1", ID: "livetv", Part: "pipeline", Phase: "running"}))
	if len(all) != 2 || all[Key{"n1", "livetv"}].Phase != "running" || all[Key{Home, "livetv"}].Phase != "failed" {
		t.Fatalf("RollupAll = %+v", all)
	}
	if r := Rollup(nil); r.Phase != "" || r.ID != "" || r.Conditions != nil {
		t.Fatal("Rollup(nil)")
	}
}

func TestParseNotify(t *testing.T) {
	e := ParseNotify(`{"revision":7,"id":"llm","node":null}`)
	if e.Revision != 7 || e.ID != "llm" || e.Node != "" || e.Reload {
		t.Fatalf("%+v", e)
	}
	e = ParseNotify(`{"revision":8,"id":"llm","node":"abc"}`)
	if e.Node != "abc" {
		t.Fatalf("%+v", e)
	}
	if !ParseNotify("garbage").Reload {
		t.Fatal("an unreadable payload must reload")
	}
}

// countingSource counts what a Watcher asks.
type countingSource struct {
	mu        sync.Mutex
	rev       int64
	revisions int
	desireds  int
	err       error
}

func (c *countingSource) Revision(context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revisions++
	return c.rev, c.err
}

func (c *countingSource) Desired(context.Context, string) (map[string]Desired, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desireds++
	if c.err != nil {
		return nil, 0, c.err
	}
	return map[string]Desired{"llm": {ID: "llm", Want: "enabled"}}, c.rev, nil
}

func (c *countingSource) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.revisions, c.desireds
}

type fakeListener struct{ ch chan Event }

func (f *fakeListener) Events() <-chan Event { return f.ch }
func (f *fakeListener) Close() error         { return nil }

// fakeClock hands the Watcher a timer it fires by hand.
type fakeClock struct {
	asked chan chan time.Time
}

func (f *fakeClock) after(time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.asked <- ch
	return ch
}

// T08 (N12): five idle minutes at the 30 s poll are at most 10 revision queries and no
// other query.
func TestWatcherIdleCost(t *testing.T) {
	src := &countingSource{rev: 4}
	clock := &fakeClock{asked: make(chan chan time.Time, 16)}
	lis := &fakeListener{ch: make(chan Event, 4)}
	w := &Watcher{Source: src, Machine: Home, Listener: lis, After: clock.after}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	applied := make(chan int64, 8)
	done := make(chan error, 1)
	go func() { done <- w.Run(cctx, func(_ map[string]Desired, rev int64) { applied <- rev }) }()
	if rev := <-applied; rev != 4 {
		t.Fatalf("first load at %d", rev)
	}
	for range 10 { // 5 minutes of 30 s polls
		(<-clock.asked) <- time.Time{}
	}
	next := <-clock.asked // the 11th timer is armed: the 10th poll is done
	revs, desireds := src.counts()
	if revs > 10 || desireds != 1 {
		t.Fatalf("idle 5 min: %d revision and %d desired queries (want <= 10 and 1)", revs, desireds)
	}
	// a notification at the revision it holds costs nothing; a new revision reloads
	lis.ch <- Event{Revision: 4}
	src.mu.Lock()
	src.rev = 5
	src.mu.Unlock()
	lis.ch <- Event{Revision: 5, ID: "llm"}
	if rev := <-applied; rev != 5 {
		t.Fatalf("reload at %d", rev)
	}
	// a reconnect reloads whatever the revision
	lis.ch <- Event{Reload: true}
	<-applied
	// a poll that sees the revision move reloads (a lost notification)
	src.mu.Lock()
	src.rev = 6
	src.mu.Unlock()
	next <- time.Time{}
	if rev := <-applied; rev != 6 {
		t.Fatalf("poll reload at %d", rev)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
}

func TestWatcherRetriesAfterAFailedLoad(t *testing.T) {
	src := &countingSource{rev: 2, err: errors.New("db down")}
	clock := &fakeClock{asked: make(chan chan time.Time, 16)}
	var errs int
	var mu sync.Mutex
	w := &Watcher{Source: src, Machine: Home, After: clock.after, OnError: func(error) { mu.Lock(); errs++; mu.Unlock() }}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	applied := make(chan int64, 2)
	go func() { _ = w.Run(cctx, func(_ map[string]Desired, rev int64) { applied <- rev }) }()
	tick := <-clock.asked // the first load failed; the poll is armed
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	tick <- time.Time{}
	if rev := <-applied; rev != 2 {
		t.Fatalf("loaded %d", rev)
	}
	mu.Lock()
	defer mu.Unlock()
	if errs != 1 {
		t.Fatalf("%d errors heard", errs)
	}
}

// SetDesiredTx writes inside the caller's transaction: a rollback leaves no row and no
// audit, a commit leaves both.
func TestSetDesiredTxFollowsTheCallersTransaction(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, commit := range []bool{false, true} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetDesiredTx(ctx, tx, Desired{ID: "livetv.hdhomerun", Want: components.WantInstalled},
			audit("component.disable")); err != nil {
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if commit {
			want = 1
		}
		if n := count(`SELECT COUNT(*) FROM components WHERE id = 'livetv.hdhomerun'`); n != want {
			t.Fatalf("commit=%v: %d rows, want %d", commit, n, want)
		}
		if n := count(`SELECT COUNT(*) FROM audit_log WHERE target_id = 'livetv.hdhomerun'`); n != want {
			t.Fatalf("commit=%v: %d audit rows, want %d", commit, n, want)
		}
	}
	tx, _ := db.BeginTx(ctx, nil)
	defer func() { _ = tx.Rollback() }()
	if _, err := s.SetDesiredTx(ctx, tx, Desired{ID: "x", Want: "on"}, audit("component.enable")); err == nil {
		t.Fatal("a bad want was written")
	}
}
