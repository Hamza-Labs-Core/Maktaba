package tvchange

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange/tvchangetest"
)

// runController runs c.Run until the test ends (or stop is called); done closes when
// Run has returned.
func runController(t *testing.T, c *Controller) (stop func(), done <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		c.Run(ctx)
	}()
	stop = func() {
		cancel()
		<-ch
	}
	t.Cleanup(stop)
	return stop, ch
}

// eventually polls cond every 20 ms until it holds or within passes.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for !cond() {
		if time.Since(start) > within {
			t.Fatalf("%s: not within %s", what, within)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Since(start)
}

// The 0.3.36 update (2026-10-27): its change was staged 4.5 minutes after its stage job
// began, and staged_at said the job's start. The controller looks at a staged change as
// soon as it runs (an API that just started included) and on every tick after: it asks
// for the staging slot within seconds, and the slot's start timeout runs from that ask,
// not from staged_at.
func TestControllerActsOnAStagedChangeWithinSeconds(t *testing.T) {
	for _, be := range tvchangetest.Backends(t) {
		t.Run(be.Name, func(t *testing.T) {
			w := newSVWorld(t, be.DB)
			w.results = passing()
			healthy := w.fs.view
			asks := 0
			w.fs.view = func(p StagingPlan) *StagingView {
				asks++
				if asks == 1 { // just started: not answering yet
					return &StagingView{Active: true, Plan: &p, Slot: "amber"}
				}
				return healthy(p)
			}
			c := w.staged("v2")
			if err := w.ctl.Store.Set(context.Background(), w.db, c.ID,
				map[string]any{"staged_at": w.now.Add(-4*time.Minute - 30*time.Second)}); err != nil {
				t.Fatal(err)
			}
			w.ctl.Interval = 200 * time.Millisecond
			runController(t, w.ctl)
			took := eventually(t, 3*time.Second, "the staging slot asked for", func() bool {
				w.fs.mu.Lock()
				defer w.fs.mu.Unlock()
				return len(w.fs.plans) > 0
			})
			t.Logf("slot asked %s after the controller started", took)
			eventually(t, 3*time.Second, "the change verifying on a healthy slot", func() bool {
				st := w.get(c.ID).State
				if st == StateDraft {
					t.Fatalf("failed on its first look: %v", w.get(c.ID).VerifyReport)
				}
				return st == StateVerifying || st == StateReady
			})
		})
	}
}

// pgOnly is a scratch Postgres database (and its DSN) for the advisory lock tests.
func pgOnly(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" {
		t.Skip("MAKTABA_TEST_PG_URL not set: the controller's lock is a Postgres advisory lock")
	}
	return clustertest.ScratchDB(t)
}

// lockHolders are the backends that hold the controller's advisory lock.
func lockHolders(t *testing.T, db *sql.DB) []int {
	t.Helper()
	rows, err := db.Query(`SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted AND objsubid = 1
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())
		AND classid = ((hashtext($1)::bigint >> 32) & 4294967295)::oid
		AND objid = (hashtext($1)::bigint & 4294967295)::oid`, controllerLock)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pids []int
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

// A controller that stops gives its lock back. Closing the *sql.Conn that held it
// returned the connection, still holding the lock, to the pool: the lock outlived the
// controller for as long as that idle connection lived.
func TestControllerGivesItsLockBackWhenItStops(t *testing.T) {
	db, dsn := pgOnly(t)
	watch, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	c := NewController(db, nil)
	c.Interval = 100 * time.Millisecond
	c.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	stop, _ := runController(t, c)
	eventually(t, 3*time.Second, "the controller taking its lock", func() bool { return len(lockHolders(t, watch)) == 1 })
	stop()
	if got := lockHolders(t, watch); len(got) != 0 {
		t.Fatalf("the stopped controller's lock is still held by backend %v", got)
	}
	var free bool
	if err := watch.QueryRow(`SELECT pg_try_advisory_lock(hashtext($1))`, controllerLock).Scan(&free); err != nil || !free {
		t.Fatalf("another process cannot take the lead: %v %v", free, err)
	}
}

// A controller whose lock connection dies (a restarted or terminated backend) takes the
// lock again instead of acting without it.
func TestControllerTakesItsLockAgainAfterItsConnectionDies(t *testing.T) {
	db, dsn := pgOnly(t)
	watch, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	c := NewController(db, nil)
	c.Interval = 100 * time.Millisecond
	c.Enqueue = func(context.Context, string, map[string]any, string) error { return nil }
	runController(t, c)
	var first int
	eventually(t, 3*time.Second, "the controller taking its lock", func() bool {
		h := lockHolders(t, watch)
		if len(h) == 1 {
			first = h[0]
		}
		return first != 0
	})
	if _, err := watch.Exec(`SELECT pg_terminate_backend($1)`, first); err != nil {
		t.Fatal(err)
	}
	eventually(t, 3*time.Second, "the controller taking its lock again", func() bool {
		h := lockHolders(t, watch)
		return len(h) == 1 && h[0] != first
	})
}

// The 0.3.36 update's tv_stage (2026-10-27) was enqueued at 21:20:00 and dispatched at
// 21:24:48: at the queue's default priority it waited behind the restart's backlog of
// older jobs in the cpu pool. The jobs that move a change go first.
func TestTVJobsRunAheadOfTheBacklog(t *testing.T) {
	db, _ := pgOnly(t)
	ctx := context.Background()
	rows, err := db.Query(`SELECT stage FROM job_stages WHERE pool = 'cpu' AND 'catalog' = ANY(subject_kinds)
		AND enabled AND debounce_sec = 0 AND stage NOT LIKE 'tv\_%' ORDER BY stage`)
	if err != nil {
		t.Fatal(err)
	}
	var backlog []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		backlog = append(backlog, s)
	}
	rows.Close()
	if len(backlog) == 0 {
		t.Fatal("no catalog stage in the cpu pool to queue ahead of the change")
	}
	for _, s := range backlog { // older work at the default priority
		res, err := queues.Enqueue(ctx, db, queues.EnqueueParams{SubjectKind: "catalog", SubjectID: CatalogID, Stage: s})
		if err != nil || !res.Accepted() {
			t.Fatalf("enqueue %s: %v %s", s, err, res.Outcome)
		}
	}
	w := newAUWorld(t, db)
	w.ctl.Enqueue = w.ctl.enqueueQueue // the real queue
	w.exec(`INSERT INTO app_settings (key, value) VALUES ('rules.tv_staging', '{"enabled":true}')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	st := w.begin("run-0.3.36")
	if st.ChangeID == "" {
		t.Fatalf("no change: %+v", st)
	}
	// the claim's order (pipeline engine/claim.py): the eligible pending jobs of the
	// pool by priority, then id
	var next string
	var prio int
	if err := db.QueryRow(`SELECT j.stage, j.priority FROM processing_jobs j JOIN job_stages s ON s.stage = j.stage
		WHERE s.pool = 'cpu' AND j.state IN ('pending', 'paused')
		  AND (j.not_before IS NULL OR j.not_before <= now())
		ORDER BY j.priority, j.id LIMIT 1`).Scan(&next, &prio); err != nil {
		t.Fatal(err)
	}
	if next != StageTVStage {
		t.Fatalf("the worker takes %s (priority %d) before the update's tv_stage; backlog %v", next, prio, backlog)
	}
	// the daily clean-up keeps the default
	if err := w.ctl.Enqueue(ctx, StageTVGC, map[string]any{}, "clean-up"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT priority FROM processing_jobs WHERE stage = 'tv_gc' AND state = 'pending'`).Scan(&prio); err != nil || prio != 100 {
		t.Fatalf("tv_gc priority %d %v", prio, err)
	}
}
