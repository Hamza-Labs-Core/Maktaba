package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeStarter struct {
	err  error
	runs []string
}

func (f *fakeStarter) Start(_ context.Context, id string) error {
	f.runs = append(f.runs, id)
	return f.err
}

// seed puts verified releases into the catalog.
func seed(t *testing.T, store *Store, rs ...Release) {
	t.Helper()
	ctx := context.Background()
	for _, r := range rs {
		if _, err := store.Seen(ctx, r); err != nil {
			t.Fatal(err)
		}
		if r.VerifyState == VerifyVerified && r.Manifest != nil {
			if err := store.SetVerified(ctx, r.Version, Verified{Manifest: *r.Manifest, ChecksumsSHA256: "c-" + r.Version}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

type fixture struct {
	db      *sql.DB
	store   *Store
	svc     *Service
	starter *fakeStarter
	notes   *recNotifier
	now     time.Time
	loc     *time.Location
}

func newFixture(t *testing.T, db *sql.DB) *fixture {
	t.Helper()
	loc := mustLoc(t, "Europe/London")
	f := &fixture{db: db, store: &Store{DB: db}, starter: &fakeStarter{}, notes: &recNotifier{}, loc: loc,
		now: time.Date(2026, 10, 22, 3, 10, 0, 0, loc)}
	f.store.Now = func() time.Time { return f.now }
	f.svc = &Service{Store: f.store, Starter: f.starter, Notifier: f.notes, Current: "1.4.0", Loc: loc,
		Now: func() time.Time { return f.now }, Blockers: []Blocker{SwitchoverBlocker{DB: db}}}
	return f
}

// The scheduler starts a due run: a planned update_runs row (trigger auto) handed to
// the supervisor (story 35.3 AC5).
func TestSchedulerStartsDueRun(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			f := newFixture(t, be.db)
			seed(t, f.store, rel("1.5.0"))
			d, err := f.svc.Tick(context.Background())
			if err != nil || d.Action != "started" || d.Version != "1.5.0" || len(f.starter.runs) != 1 {
				t.Fatalf("tick: %+v %v starts=%v", d, err, f.starter.runs)
			}
			run, _ := f.store.ActiveRun(context.Background())
			if run == nil || run.Trigger != TriggerAuto || run.State != "planned" || run.FromVersion != "1.4.0" {
				t.Fatalf("run: %+v", run)
			}
			// one at a time
			d, _ = f.svc.Tick(context.Background())
			if d.Action != "blocked" || d.Reason != BlockActiveRun {
				t.Fatalf("second tick: %+v", d)
			}
		})
	}
}

// TC3 (story 35.3): an active run, a crashed role, a deferral - and a TV change in
// flight, which no longer holds a run (the owner's rule of 2026-10-28).
func TestSchedulerBlockers(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	// each case has its own fixture: they run side by side (the unit tier's time cap)

	// a TV change waiting for its approval (ready), or anywhere else in flight, never
	// holds an update: the run starts with the production blockers in place
	for _, state := range []string{"ready", "staged", "verifying", "approved", "swapping"} {
		t.Run("tv change "+state, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, sqliteDB(t))
			f.svc.Blockers = HomeBlockers(f.db, "", nil)
			seed(t, f.store, rel("1.5.0"))
			if _, err := f.db.Exec(`INSERT INTO tv_changes (id, number, state, kinds) VALUES ('c1', 1, $1, '["lineup"]')`, state); err != nil {
				t.Fatal(err)
			}
			if d, _ := f.svc.Tick(ctx); d.Action != "started" {
				t.Fatalf("an update during a %s TV change: %+v", state, d)
			}
		})
	}

	t.Run("crashed role", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("1.5.0"))
		snap := map[string]any{
			"processes": []map[string]any{{"role": "api", "state": "running"}, {"role": "pipeline", "state": "running"}},
			"events":    []map[string]any{{"at": f.now.Add(-5 * time.Minute), "kind": "exit", "reason": "crash"}},
		}
		sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(snap)
		}))
		defer sup.Close()
		f.svc.Blockers = append(f.svc.Blockers, &HomeHealth{BaseURL: sup.URL, Now: func() time.Time { return f.now }})
		if d, _ := f.svc.Tick(ctx); d.Action != "blocked" || d.Reason != BlockRecentCrash {
			t.Fatalf("recent crash: %+v", d)
		}
		// an admin's Update now is not held by the running release's crash
		enableAutoInstall(t)
		if run, err := f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); err != nil || run.Version != "1.5.0" {
			t.Fatalf("update now past a recent crash: %+v %v", run, err)
		}
		f = newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("1.5.0"))
		f.svc.Blockers = append(f.svc.Blockers, &HomeHealth{BaseURL: sup.URL, Now: func() time.Time { return f.now }})
		f.now = f.now.Add(6 * time.Minute) // 11 min after the crash
		if d, _ := f.svc.Tick(ctx); d.Action != "started" {
			t.Fatalf("quiet again: %+v", d)
		}
		if HomeBlocked([]ProcState{{Role: "api", State: "backoff"}}, nil, f.now) != BlockRoleNotRunning {
			t.Fatal("a role in backoff did not block")
		}
		if HomeBlocked([]ProcState{{Role: "tv-slot", Instance: "amber", State: "stopped"}}, nil, f.now) != "" {
			t.Fatal("an on-demand slot nobody asked for blocked")
		}
	})

	t.Run("deferral", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("1.5.0"))
		until := f.now.Add(48 * time.Hour).UTC().Format(time.RFC3339)
		if _, err := f.db.Exec(`INSERT INTO app_settings (key, value) VALUES ('rules.updates', $1)`,
			`{"deferred_until":"`+until+`"}`); err != nil {
			t.Fatal(err)
		}
		if d, _ := f.svc.Tick(ctx); d.Action != "none" || d.Reason != WaitDeferred {
			t.Fatalf("deferred: %+v", d)
		}
		if f.notes.find(NoticeScheduled) == nil {
			t.Fatalf("the planned update was not announced: %v", f.notes.kinds())
		}
	})

	t.Run("outside the window", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("1.5.0"))
		f.now = time.Date(2026, 10, 22, 4, 40, 0, 0, f.loc) // inside the last 30 minutes
		if d, _ := f.svc.Tick(ctx); d.Action != "none" || d.Reason != WaitWindow {
			t.Fatalf("margin: %+v", d)
		}
	})

	t.Run("contract release held and notified once", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("2.0.0", contract))
		for i := 0; i < 2; i++ {
			if d, _ := f.svc.Tick(ctx); d.Action != "held" {
				t.Fatalf("contract: %+v", d)
			}
		}
		n := 0
		for _, k := range f.notes.kinds() {
			if k == NoticeReleaseHeld {
				n++
			}
		}
		if n != 1 || len(f.starter.runs) != 0 {
			t.Fatalf("held notices %d, runs %v", n, f.starter.runs)
		}
		if h := f.notes.find(NoticeReleaseHeld); h.p.Link != "/admin/live-tv/changes#updates" {
			t.Fatalf("approval link: %q", h.p.Link)
		}
	})

	t.Run("supervisor without an orchestrator fails the run", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t, sqliteDB(t))
		seed(t, f.store, rel("1.5.0"))
		f.svc.Starter = &Driver{Store: f.store, Svc: f.svc, Home: &SupervisorClient{BaseURL: ""}}
		d, err := f.svc.Tick(ctx)
		if !errors.Is(err, ErrOrchestratorUnavailable) || d.Action != "failed" {
			t.Fatalf("tick: %+v %v", d, err)
		}
		if a, _ := f.store.ActiveRun(ctx); a != nil {
			t.Fatalf("the failed run is still active: %+v", a)
		}
		runs, _ := f.store.Runs(ctx, 5)
		if len(runs) != 1 || runs[0].State != "failed" || f.notes.find(NoticeRunFailed) == nil {
			t.Fatalf("runs: %+v", runs)
		}
	})
}

// TC6 (story 35.3 AC7): Update now skips only the window and the release age; it
// approves a held contract release; it never skips the checks (blockers, withdrawal).
func TestUpdateNowIgnoresWindowAndAge(t *testing.T) {
	t.Parallel()
	enableAutoInstall(t)
	ctx := context.Background()
	f := newFixture(t, sqliteDB(t))
	f.now = time.Date(2026, 10, 1, 13, 0, 0, 0, f.loc) // midday, an hour after publishing
	young := rel("1.5.0", func(r *Release) { r.Manifest.Published = f.now.Add(-time.Hour) })
	seed(t, f.store, young)
	if d, _ := f.svc.Tick(ctx); d.Action == "started" {
		t.Fatalf("the scheduler started outside the window: %+v", d)
	}
	run, err := f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "")
	if err != nil || run.Trigger != TriggerManual || run.Version != "1.5.0" || len(f.starter.runs) != 1 {
		t.Fatalf("update now: %+v %v", run, err)
	}
	if _, err := f.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ""); !errors.As(err, new(*BlockedError)) {
		t.Fatalf("second update now while a run is active: %v", err)
	}

	// a held contract release: Update now is the approval
	f2 := newFixture(t, sqliteDB(t))
	seed(t, f2.store, rel("2.0.0", contract))
	run, err = f2.svc.RunNow(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "2.0.0")
	if err != nil || run.Version != "2.0.0" {
		t.Fatalf("approve + run: %+v %v", run, err)
	}
	r, _ := f2.store.Get(ctx, "2.0.0")
	if r.ApprovedAt == nil || r.ApprovedBy != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" {
		t.Fatalf("approval not recorded: %+v", r)
	}

	// nothing to install
	f3 := newFixture(t, sqliteDB(t))
	if _, err := f3.svc.RunNow(ctx, "u", ""); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("empty catalog: %v", err)
	}
	// an unverified release is never installed, even by hand
	seed(t, f3.store, rel("1.6.0", func(r *Release) { r.VerifyState = VerifyUnverified }))
	if _, err := f3.svc.RunNow(ctx, "u", "1.6.0"); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("unverified: %v", err)
	}
}
