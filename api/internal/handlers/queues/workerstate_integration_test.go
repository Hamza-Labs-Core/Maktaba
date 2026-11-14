//go:build integration

package queues

// The workers' state of GET /api/queues/pulse on a real, migrated Postgres: the scripted
// driver of workerstate_test.go cannot tell whether workersStateSQL is valid, or which
// jobs it counts.
//
//	MAKTABA_TEST_PG_URL=postgres://u:p@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags integration ./internal/handlers/queues/ -run WorkersState

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

func TestWorkersState_AgainstPostgres(t *testing.T) {
	db := parityDB(t)
	h := &Handler{DB: db, Hub: ws.NewHub(), NowFunc: time.Now}
	r := chi.NewRouter()
	h.Mount(r)
	f := &fixture{h: h, r: r}

	const stage, other = "stall_probe", "stall_other"
	mustExec(t, db, `DELETE FROM processing_jobs; DELETE FROM workers`)
	mustExec(t, db, `UPDATE queue_control SET paused = false WHERE id = 1`)
	for i, s := range []string{stage, other} {
		mustExec(t, db, `INSERT INTO job_stages (stage, label, lane, lane_order, subject_kinds, pool)
			VALUES ($1, $1, 'media', $2, ARRAY['library'], 'cpu') ON CONFLICT (stage) DO UPDATE
			SET enabled = true, paused = false, hidden = false`, s, 900+i)
	}
	const lib = "11111111-1111-4111-8111-111111111111"
	mustExec(t, db, `INSERT INTO libraries (id, name, roots) VALUES ($1, 'stall', ARRAY['/stall'])
		ON CONFLICT (id) DO NOTHING`, lib)
	job := func(st, state, age, extra string) {
		t.Helper()
		mustExec(t, db, `INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage, state,
			created_at, updated_at) VALUES ('library', $1::uuid, $1::uuid, $2, $3,
			now() - $4::interval, now() - $4::interval)`, lib, st, state, age)
		if extra != "" {
			mustExec(t, db, `UPDATE processing_jobs SET `+extra+` WHERE id = (SELECT max(id) FROM processing_jobs)`)
		}
	}
	worker := func(id string, seenAgo string, stages ...string) {
		t.Helper()
		mustExec(t, db, `INSERT INTO workers (id, host, stages, last_seen_at) VALUES ($1, 'home', $2::text[],
			now() - $3::interval) ON CONFLICT (id) DO UPDATE SET stages = EXCLUDED.stages,
			last_seen_at = EXCLUDED.last_seen_at`, id, "{"+join(stages)+"}", seenAgo)
	}
	pulse := func() Pulse {
		t.Helper()
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var p Pulse
		if rec.Code != http.StatusOK {
			t.Fatalf("pulse %d %s", rec.Code, rec.Body.String())
		}
		decode(t, rec, &p)
		return p
	}
	near := func(got *time.Time, ago time.Duration) bool {
		return got != nil && time.Since(*got) > ago-30*time.Second && time.Since(*got) < ago+30*time.Second
	}

	// nothing waits, no worker registered: the workers are not called stalled
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 0 || p.Workers.LastSeenAt != nil {
		t.Fatalf("empty home: %+v", p.Workers)
	}

	// a job waits and no worker ever registered: not_responding, no worker to name
	job(stage, "pending", "45 minutes", "")
	if p := pulse(); p.State != PulseStalled || p.Workers.State != WorkersNotResponding ||
		p.Workers.Reason != WorkersNoHeartbeat || p.Workers.Waiting != 1 || len(p.Workers.Stalled) != 0 ||
		!near(p.Workers.Since, 45*time.Minute) {
		t.Fatalf("no worker: %s %+v", p.State, p.Workers)
	}

	// its worker last beat 20 minutes ago: that worker is silent since then
	worker("home/1", "20 minutes", stage)
	mustExec(t, db, `UPDATE workers SET started_at = now() - interval '1 day'`)
	p := pulse()
	if p.Workers.State != WorkersNotResponding || len(p.Workers.Stalled) != 1 ||
		p.Workers.Stalled[0].Reason != WorkersNoHeartbeat || p.Workers.Stalled[0].WorkerID != "home/1" ||
		!near(&p.Workers.Stalled[0].Since, 20*time.Minute) || !near(p.Workers.LastSeenAt, 20*time.Minute) {
		t.Fatalf("silent worker: %+v", p.Workers)
	}

	// the worker beats and serves the stage, and has started nothing: no_dispatch
	worker("home/1", "5 seconds", stage)
	p = pulse()
	if p.State != PulseStalled || len(p.Workers.Stalled) != 1 || p.Workers.Stalled[0].Reason != WorkersNoDispatch ||
		!near(&p.Workers.Stalled[0].Since, 45*time.Minute) || p.Workers.Stalled[0].Kind != WorkerMain {
		t.Fatalf("no dispatch: %s %+v", p.State, p.Workers)
	}

	// another worker runs a job of another stage and beats: the first is stalled all the
	// same (the home after its restart: the main worker worked, transcription did not)
	worker("home/2", "3 seconds", other)
	mustExec(t, db, `UPDATE workers SET started_at = now() - interval '1 day'`)
	job(other, "running", "2 minutes", `claimed_at = now() - interval '2 minutes', started_at = now() - interval '2 minutes',
		last_heartbeat_at = now() - interval '3 seconds', claimed_by = 'home/2'`)
	p = pulse()
	if p.State != PulseStalled || p.Running != 1 || len(p.Workers.Stalled) != 1 || p.Workers.Stalled[0].WorkerID != "home/1" {
		t.Fatalf("one of two stalled: %s %+v", p.State, p.Workers)
	}

	// the engine's event of a start by that worker within the bound: it is between jobs
	mustExec(t, db, `INSERT INTO job_events (job_id, subject_kind, subject_id, stage, level, kind, message)
		VALUES (1, 'library', $1::uuid, $2, 'debug', 'claimed', 'started by home/1')`, lib, stage)
	if p := pulse(); p.Workers.State != WorkersRunning || len(p.Workers.Stalled) != 0 {
		t.Fatalf("started a job 0 s ago: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE job_events SET at = now() - interval '11 minutes' WHERE kind = 'claimed'`)
	if p := pulse(); len(p.Workers.Stalled) != 1 {
		t.Fatalf("started a job 11 min ago: %+v", p.Workers)
	}

	// a worker restarted a minute ago has the bound to start
	mustExec(t, db, `UPDATE workers SET started_at = now() - interval '1 minute' WHERE id = 'home/1'`)
	if p := pulse(); len(p.Workers.Stalled) != 0 {
		t.Fatalf("just restarted: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE workers SET started_at = now() - interval '1 day' WHERE id = 'home/1'`)

	// the stage's pool: size 0 holds its jobs back, and a shared pool that is full does too
	mustExec(t, db, `INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES ('stall_pool', 'Stall', 'global', 0, 1)
		ON CONFLICT (pool) DO UPDATE SET size = 0, scope = 'global'`)
	mustExec(t, db, `UPDATE job_stages SET pool = 'stall_pool' WHERE stage = $1`, stage)
	if p := pulse(); len(p.Workers.Stalled) != 0 {
		t.Fatalf("pool of size 0: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE worker_pools SET size = 1 WHERE pool = 'stall_pool'`)
	if p := pulse(); len(p.Workers.Stalled) != 1 {
		t.Fatalf("pool of size 1, free: %+v", p.Workers)
	}
	mustExec(t, db, `INSERT INTO compute_leases (pool, plugin_id, expires_at) VALUES ('stall_pool', 'tv.maktaba.x', now() + interval '1 minute')`)
	if p := pulse(); len(p.Workers.Stalled) != 0 {
		t.Fatalf("pool held by a lease: %+v", p.Workers)
	}
	mustExec(t, db, `DELETE FROM compute_leases WHERE pool = 'stall_pool'`)

	// the System health page reads the same state
	if ws, err := h.WorkersNow(context.Background()); err != nil || len(ws.Stalled) != 1 {
		t.Fatalf("WorkersNow: %+v %v", ws, err)
	}

	// jobs that are not runnable never count: parked, not due, a paused stage, a stage off
	mustExec(t, db, `UPDATE processing_jobs SET parked_on = 'disk_space' WHERE state = 'pending'`)
	if p := pulse(); p.Workers.State != WorkersRunning {
		t.Fatalf("parked: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET parked_on = NULL, not_before = now() + interval '1 hour' WHERE state = 'pending'`)
	if p := pulse(); p.Workers.State != WorkersRunning {
		t.Fatalf("not due: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'`)
	mustExec(t, db, `UPDATE job_stages SET paused = true, paused_at = now(), paused_by = 'test' WHERE stage = $1`, stage)
	if p := pulse(); p.Workers.State != WorkersRunning {
		t.Fatalf("paused stage: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE job_stages SET paused = false, paused_at = NULL, paused_by = NULL, enabled = false WHERE stage = $1`, stage)
	if p := pulse(); p.Workers.State != WorkersRunning {
		t.Fatalf("stage off: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE job_stages SET enabled = true WHERE stage = $1`, stage)

	// a retry that came due a minute ago has waited a minute, however old the job is
	mustExec(t, db, `UPDATE processing_jobs SET not_before = now() - interval '1 minute' WHERE state = 'pending'`)
	if p := pulse(); p.Workers.State != WorkersRunning {
		t.Fatalf("just due: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'`)

	// every queue paused: paused, not stalled
	mustExec(t, db, `UPDATE queue_control SET paused = true WHERE id = 1`)
	if p := pulse(); p.State != PulsePaused || p.Workers.State != WorkersRunning {
		t.Fatalf("paused all: %s %+v", p.State, p.Workers)
	}
	mustExec(t, db, `UPDATE queue_control SET paused = false WHERE id = 1`)

	// the admin's restart of the stalled worker's component is audited
	var asked []string
	h.RestartWorkers = func(_ context.Context, ids []string) (int, error) { asked = ids; return 2, nil }
	if rec := f.do("POST", "/api/queues/workers/restart", `{"components":["core.pipeline"]}`, admin()); rec.Code != http.StatusOK ||
		len(asked) != 1 || asked[0] != "core.pipeline" {
		t.Fatalf("restart %d %s %v", rec.Code, rec.Body.String(), asked)
	}
	var payload sql.NullString
	if err := db.QueryRow(`SELECT payload::text FROM audit_log WHERE action = 'queue.workers.restart'
		ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil || !payload.Valid {
		t.Fatalf("audit row: %v", err)
	}
	if rec := f.do("POST", "/api/queues/workers/restart", "", viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer restart %d", rec.Code)
	}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
