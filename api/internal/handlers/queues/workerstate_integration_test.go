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

	// a job waits and no worker ever registered
	job(stage, "pending", "45 minutes", "")
	if p := pulse(); p.State != PulseStalled || p.Workers.State != WorkersNotResponding ||
		p.Workers.Reason != WorkersNoHeartbeat || p.Workers.Waiting != 1 || !near(p.Workers.Since, 45*time.Minute) {
		t.Fatalf("no worker: %s %+v", p.State, p.Workers)
	}

	// its worker last beat 20 minutes ago: silent since then
	worker("home/1", "20 minutes", stage)
	if p := pulse(); p.Workers.State != WorkersNotResponding || p.Workers.Reason != WorkersNoHeartbeat ||
		!near(p.Workers.Since, 20*time.Minute) || !near(p.Workers.LastSeenAt, 20*time.Minute) {
		t.Fatalf("silent worker: %+v", p.Workers)
	}

	// the worker beats and serves the stage, and nothing was dispatched: no_dispatch
	worker("home/1", "5 seconds", stage)
	if p := pulse(); p.State != PulseStalled || p.Workers.State != WorkersNotResponding ||
		p.Workers.Reason != WorkersNoDispatch || !near(p.Workers.Since, 45*time.Minute) {
		t.Fatalf("no dispatch: %s %+v", p.State, p.Workers)
	}

	// the beating worker serves another stage only: the job waits for a machine, not a stall
	worker("home/1", "5 seconds", other)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 1 {
		t.Fatalf("unserved stage: %+v", p.Workers)
	}
	worker("home/1", "5 seconds", stage)

	// jobs that are not runnable never count: parked, not due, a paused stage, a stage off
	mustExec(t, db, `UPDATE processing_jobs SET parked_on = 'disk_space'`)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 0 {
		t.Fatalf("parked: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET parked_on = NULL, not_before = now() + interval '1 hour'`)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 0 {
		t.Fatalf("not due: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET not_before = NULL`)
	mustExec(t, db, `UPDATE job_stages SET paused = true, paused_at = now(), paused_by = 'test' WHERE stage = $1`, stage)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 0 {
		t.Fatalf("paused stage: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE job_stages SET paused = false, paused_at = NULL, paused_by = NULL, enabled = false WHERE stage = $1`, stage)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 0 {
		t.Fatalf("stage off: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE job_stages SET enabled = true WHERE stage = $1`, stage)

	// a retry that came due a minute ago has waited a minute, however old the job is
	mustExec(t, db, `UPDATE processing_jobs SET not_before = now() - interval '1 minute'`)
	if p := pulse(); p.Workers.State != WorkersRunning || p.Workers.Waiting != 1 {
		t.Fatalf("just due: %+v", p.Workers)
	}
	mustExec(t, db, `UPDATE processing_jobs SET not_before = NULL`)

	// every queue paused: paused, not stalled
	mustExec(t, db, `UPDATE queue_control SET paused = true WHERE id = 1`)
	if p := pulse(); p.State != PulsePaused || p.Workers.State != WorkersRunning {
		t.Fatalf("paused all: %s %+v", p.State, p.Workers)
	}
	mustExec(t, db, `UPDATE queue_control SET paused = false WHERE id = 1`)

	// a job runs and beats: the workers dispatch
	job(other, "running", "2 minutes", `claimed_at = now() - interval '2 minutes', started_at = now() - interval '2 minutes',
		last_heartbeat_at = now() - interval '3 seconds', claimed_by = 'home/1'`)
	if p := pulse(); p.State != PulseWorking || p.Workers.State != WorkersRunning || p.Workers.Waiting != 1 {
		t.Fatalf("running: %s %+v", p.State, p.Workers)
	}

	// the admin's restart is audited (the supervisor is the handler's RestartWorkers)
	h.RestartWorkers = func(context.Context) (int, error) { return 2, nil }
	if rec := f.do("POST", "/api/queues/workers/restart", "", admin()); rec.Code != http.StatusOK {
		t.Fatalf("restart %d %s", rec.Code, rec.Body.String())
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
