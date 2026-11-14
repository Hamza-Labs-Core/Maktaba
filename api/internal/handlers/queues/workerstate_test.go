package queues

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

// workersRule scripts workersStateSQL's one row: the newest registry heartbeat, the
// runnable jobs, those a beating worker serves, and when the oldest of each began to wait.
func workersRule(seen any, waiting, served int64, waitingSince, servedSince any) *queuestest.Rule {
	return &queuestest.Rule{Match: "with live as", Cols: []string{"seen", "waiting", "served", "ws", "ss"},
		Rows: [][]driver.Value{{seen, waiting, served, waitingSince, servedSince}}}
}

func tp(t time.Time) *time.Time { return &t }

func TestWorkersState_Rule(t *testing.T) {
	now := t0
	ago := func(d time.Duration) *time.Time { return tp(now.Add(-d)) }
	beating := ago(5 * time.Second)
	cases := []struct {
		name          string
		in            workersInputs
		state, reason string
		since         *time.Time
	}{
		{"nothing waits and no worker ever registered", workersInputs{}, WorkersRunning, "", nil},
		{"nothing waits, the workers long silent", workersInputs{LastSeen: ago(time.Hour)}, WorkersRunning, "", nil},
		{"jobs wait, a worker beats and runs them",
			workersInputs{Waiting: 4, Served: 4, Running: 2, LastSeen: beating, LastActivity: ago(3 * time.Second),
				WaitingSince: ago(time.Hour), ServedSince: ago(time.Hour)}, WorkersRunning, "", nil},
		{"jobs wait and the last worker beat is just inside the bound",
			workersInputs{Waiting: 1, LastSeen: ago(workersSilentAfter), WaitingSince: ago(time.Minute)},
			WorkersRunning, "", nil},
		{"jobs wait and no worker has beaten for the bound",
			workersInputs{Waiting: 3, LastSeen: ago(workersSilentAfter + time.Second), WaitingSince: ago(time.Hour)},
			WorkersNotResponding, WorkersNoHeartbeat, ago(workersSilentAfter + time.Second)},
		{"the workers went silent before the work arrived: since the work waits",
			workersInputs{Waiting: 3, LastSeen: ago(3 * time.Hour), WaitingSince: ago(time.Hour)},
			WorkersNotResponding, WorkersNoHeartbeat, ago(time.Hour)},
		{"jobs wait and no worker ever registered",
			workersInputs{Waiting: 2, WaitingSince: ago(20 * time.Minute)},
			WorkersNotResponding, WorkersNoHeartbeat, ago(20 * time.Minute)},
		{"every queue paused: never stalled",
			workersInputs{PausedAll: true, Waiting: 3, LastSeen: ago(time.Hour), WaitingSince: ago(time.Hour)},
			WorkersRunning, "", nil},
		{"a beating worker serves the waiting stage and nothing moved for hours",
			workersInputs{Waiting: 5, Served: 5, LastSeen: beating, LastActivity: ago(3 * time.Hour),
				WaitingSince: ago(4 * time.Hour), ServedSince: ago(4 * time.Hour)},
			WorkersNotResponding, WorkersNoDispatch, ago(3 * time.Hour)},
		{"the same with nothing ever run: since the work waits",
			workersInputs{Waiting: 5, Served: 5, LastSeen: beating,
				WaitingSince: ago(time.Hour), ServedSince: ago(time.Hour)},
			WorkersNotResponding, WorkersNoDispatch, ago(time.Hour)},
		{"the queues were quiet and the work is new: not yet",
			workersInputs{Waiting: 5, Served: 5, LastSeen: beating, LastActivity: ago(3 * time.Hour),
				WaitingSince: ago(time.Minute), ServedSince: ago(time.Minute)}, WorkersRunning, "", nil},
		{"a job runs: the workers dispatch",
			workersInputs{Waiting: 5, Served: 5, Running: 1, LastSeen: beating, LastActivity: ago(time.Hour),
				WaitingSince: ago(time.Hour), ServedSince: ago(time.Hour)}, WorkersRunning, "", nil},
		{"a job finished a minute ago",
			workersInputs{Waiting: 5, Served: 5, LastSeen: beating, LastActivity: ago(time.Minute),
				WaitingSince: ago(time.Hour), ServedSince: ago(time.Hour)}, WorkersRunning, "", nil},
		{"no beating worker serves the waiting stage (its machine is off): not the workers' stall",
			workersInputs{Waiting: 5, Served: 0, LastSeen: beating, LastActivity: ago(3 * time.Hour),
				WaitingSince: ago(4 * time.Hour)}, WorkersRunning, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workersState(tc.in, now)
			if got.State != tc.state || got.Reason != tc.reason {
				t.Fatalf("state %q reason %q, want %q %q", got.State, got.Reason, tc.state, tc.reason)
			}
			switch {
			case tc.since == nil && got.Since != nil:
				t.Fatalf("since %v, want none", got.Since)
			case tc.since != nil && (got.Since == nil || !got.Since.Equal(*tc.since)):
				t.Fatalf("since %v, want %v", got.Since, tc.since)
			}
			if got.Waiting != tc.in.Waiting {
				t.Errorf("waiting %d", got.Waiting)
			}
		})
	}
}

// workersPulse is the pulse fixture with its workers row replaced.
func workersPulse(running, waiting int64, beat, done any, paused bool, workers *queuestest.Rule) *fixture {
	return newFixture(
		workers,
		&queuestest.Rule{Match: "max(j.last_heartbeat_at)", Cols: []string{"r", "w", "hb"}, Rows: [][]driver.Value{{running, waiting, beat}}},
		&queuestest.Rule{Match: "select max(finished_at)", Cols: []string{"m"}, Rows: [][]driver.Value{{done}}},
		&queuestest.Rule{Match: "from queue_control", Cols: []string{"p"}, Rows: [][]driver.Value{{paused}}},
	)
}

func TestPulse_Workers(t *testing.T) {
	hour := t0.Add(-time.Hour)
	recent := t0.Add(-5 * time.Second)

	t.Run("no worker heartbeat while jobs wait: stalled, since the last beat", func(t *testing.T) {
		f := workersPulse(0, 7, nil, recent, false, workersRule(hour, 7, 0, t0.Add(-2*time.Hour), nil))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		w := out.Workers
		if rec.Code != 200 || out.State != PulseStalled || w.State != WorkersNotResponding ||
			w.Reason != WorkersNoHeartbeat || w.Since == nil || !w.Since.Equal(hour) || w.Waiting != 7 ||
			w.LastSeenAt == nil || !w.LastSeenAt.Equal(hour) || w.CanRestart {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		// the query is given now and the oldest heartbeat that still counts as beating
		call := f.db.Find("with live as")
		if len(call) != 1 || call[0].Args[0] != driver.Value(t0) || call[0].Args[1] != driver.Value(t0.Add(-workerOnlineWindow)) {
			t.Fatalf("workers query %+v", call)
		}
	})

	t.Run("a beating worker dispatches nothing: stalled, since the last activity", func(t *testing.T) {
		f := workersPulse(0, 7, nil, hour, false, workersRule(recent, 7, 7, t0.Add(-2*time.Hour), t0.Add(-2*time.Hour)))
		f.h.RestartWorkers = func(context.Context) (int, error) { return 0, nil }
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		w := out.Workers
		if out.State != PulseStalled || w.State != WorkersNotResponding || w.Reason != WorkersNoDispatch ||
			w.Since == nil || !w.Since.Equal(hour) || !w.CanRestart {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("the workers run the jobs", func(t *testing.T) {
		f := workersPulse(2, 7, recent, recent, false, workersRule(recent, 7, 7, hour, hour))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.State != PulseWorking || out.Workers.State != WorkersRunning || out.Workers.Since != nil ||
			out.Workers.Reason != "" || !strings.Contains(rec.Body.String(), `"since":null`) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("every queue paused: paused, the workers not called stalled", func(t *testing.T) {
		f := workersPulse(0, 7, nil, hour, true, workersRule(hour, 7, 0, hour, nil))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.State != PulsePaused || out.Workers.State != WorkersRunning {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a failed read of the registry fails the pulse", func(t *testing.T) {
		f := workersPulse(0, 0, nil, nil, false, &queuestest.Rule{Match: "with live as", Err: errors.New("boom")})
		if rec := f.do("GET", "/api/queues/pulse", "", admin()); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

// The role rule: only a home admin restarts the workers. Everyone else is refused by
// the API before the supervisor is asked or anything is written.
func TestRestartWorkers_AdminOnly(t *testing.T) {
	callers := []struct {
		name string
		p    *principal.Principal
	}{
		{"anonymous", nil},
		{"viewer", viewer()},
		{"all-libraries viewer", allViewer()},
		{"library viewer", libViewer()},
	}
	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture()
			asked := 0
			f.h.RestartWorkers = func(context.Context) (int, error) { asked++; return 3, nil }
			rec := f.do("POST", "/api/queues/workers/restart", "", c.p)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if asked != 0 || len(f.db.Calls()) != 0 {
				t.Fatalf("a refused caller reached the supervisor (%d) or the database (%d)", asked, len(f.db.Calls()))
			}
		})
	}
}

func TestRestartWorkers_Admin(t *testing.T) {
	t.Run("restarts through the supervisor, audited", func(t *testing.T) {
		f := newFixture()
		asked := 0
		f.h.RestartWorkers = func(context.Context) (int, error) { asked++; return 3, nil }
		rec := f.do("POST", "/api/queues/workers/restart", "", admin())
		var out workersRestarted
		decode(t, rec, &out)
		if rec.Code != http.StatusOK || out.Restarted != 3 || asked != 1 {
			t.Fatalf("%d %s asked %d", rec.Code, rec.Body.String(), asked)
		}
		audit := f.db.Find("insert into audit_log")
		if len(audit) != 1 || audit[0].Args[0] != driver.Value("queue.workers.restart") ||
			!strings.Contains(audit[0].Args[3].(string), `"restarted":3`) {
			t.Fatalf("audit %+v", audit)
		}
	})

	t.Run("no worker runs now: accepted", func(t *testing.T) {
		f := newFixture()
		f.h.RestartWorkers = func(context.Context) (int, error) { return 0, nil }
		if rec := f.do("POST", "/api/queues/workers/restart", "", admin()); rec.Code != http.StatusAccepted {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no supervisor: 503 components_unavailable", func(t *testing.T) {
		f := newFixture()
		rec := f.do("POST", "/api/queues/workers/restart", "", admin())
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), CodeWorkersRestartUnavailable) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("the supervisor does not answer: 503, nothing audited", func(t *testing.T) {
		f := newFixture()
		f.h.RestartWorkers = func(context.Context) (int, error) { return 0, errors.New("supervisor unreachable") }
		rec := f.do("POST", "/api/queues/workers/restart", "", admin())
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), CodeWorkersRestartUnavailable) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if n := len(f.db.Find("insert into audit_log")); n != 0 {
			t.Fatalf("audited %d", n)
		}
	})
}
