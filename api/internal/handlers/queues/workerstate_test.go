package queues

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/hostload"
)

func intp(n int) *int { return &n }

// The registry of the home after its database came back: the main worker, two
// transcription workers and the search index's, all up for a day.
func homeWorkers(now time.Time) []Worker {
	up := now.Add(-24 * time.Hour)
	beat := now.Add(-5 * time.Second)
	return []Worker{
		{ID: "home/1/main", Host: "home", Stages: []string{"scan", "probe", "detect", "tag"}, StartedAt: up, LastSeenAt: beat},
		{ID: "home/2/stt", Host: "home", Stages: []string{"transcribe", "second_listen"}, StartedAt: up, LastSeenAt: beat},
		{ID: "home/3/stt", Host: "home", Stages: []string{"transcribe", "second_listen"}, StartedAt: up, LastSeenAt: beat},
		{ID: "home/4/index", Host: "home", Stages: []string{"index"}, StartedAt: up, LastSeenAt: beat},
	}
}

func TestStalledWorkers_Rule(t *testing.T) {
	now := t0
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	transcribe := func(waiting int, since time.Time) stageWait {
		return stageWait{Stage: "transcribe", Pool: "gpu", PerWorker: true, Size: intp(1), Waiting: waiting, Since: since}
	}
	probe := func(waiting int, since time.Time) stageWait {
		return stageWait{Stage: "probe", Pool: "nas_read", Size: intp(2), Waiting: waiting, Since: since}
	}
	base := func() workersInputs {
		return workersInputs{Workers: homeWorkers(now), Held: map[string]map[string]int{}, HeldAll: map[string]int{},
			Started: map[string]time.Time{}}
	}
	ids := func(ws []StalledWorker) string {
		var out []string
		for _, w := range ws {
			out = append(out, w.WorkerID+":"+w.Reason)
		}
		slices.Sort(out)
		return strings.Join(out, " ")
	}
	cases := []struct {
		name string
		mut  func(*workersInputs)
		want string
	}{
		{"nothing waits", func(*workersInputs) {}, ""},
		{"the main worker runs jobs while both transcription workers start nothing for hours",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(19*time.Hour)), probe(3, ago(time.Minute))}
				in.Held["home/1/main"] = map[string]int{"nas_read": 2}
				in.HeldAll["nas_read"] = 2
				in.Started["home/1/main"] = ago(4 * time.Second)
			}, "home/2/stt:no_dispatch home/3/stt:no_dispatch"},
		{"one transcription worker works, its sibling starts nothing",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(3*time.Hour))}
				in.Held["home/2/stt"] = map[string]int{"gpu": 1}
				in.HeldAll["gpu"] = 1
				in.Started["home/2/stt"] = ago(2 * time.Minute)
			}, "home/3/stt:no_dispatch"},
		{"a worker that started a job a minute ago and holds none now is between jobs",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(3*time.Hour))}
				in.Started["home/2/stt"] = ago(time.Minute)
				in.Started["home/3/stt"] = ago(30 * time.Second)
			}, ""},
		{"a worker just restarted has the bound to start",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(19*time.Hour))}
				in.Workers[1].StartedAt, in.Workers[2].StartedAt = ago(time.Minute), ago(workersIdleAfter-time.Second)
			}, ""},
		{"and is stalled once it has been up for the bound with nothing started",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(19*time.Hour))}
				in.Workers[1].StartedAt, in.Workers[2].StartedAt = ago(time.Minute), ago(workersIdleAfter)
			}, "home/3/stt:no_dispatch"},
		{"new work on idle workers: not yet",
			func(in *workersInputs) { in.Stages = []stageWait{transcribe(2, ago(time.Minute))} }, ""},
		{"a pool of size 0 holds its jobs back: no worker is stalled",
			func(in *workersInputs) {
				s := transcribe(40, ago(19*time.Hour))
				s.Size = intp(0)
				p := probe(9, ago(5*time.Hour))
				p.Size = intp(0)
				in.Stages = []stageWait{s, p}
			}, ""},
		{"a shared pool that is full holds its jobs back",
			func(in *workersInputs) {
				in.Stages = []stageWait{probe(9, ago(5*time.Hour))}
				in.HeldAll["nas_read"] = 2 // held by another machine's worker, or the model's leases
			}, ""},
		{"a shared pool with a free slot and a main worker that starts nothing",
			func(in *workersInputs) {
				in.Stages = []stageWait{probe(9, ago(5*time.Hour))}
				in.HeldAll["nas_read"] = 1
			}, "home/1/main:no_dispatch"},
		{"a per-worker pool with no size in the database: free while the worker holds none",
			func(in *workersInputs) {
				in.Stages = []stageWait{{Stage: "detect", Pool: "cpu", PerWorker: true, Waiting: 4, Since: ago(time.Hour)}}
			}, "home/1/main:no_dispatch"},
		{"the same worker holding a job of that pool is busy",
			func(in *workersInputs) {
				in.Stages = []stageWait{{Stage: "detect", Pool: "cpu", PerWorker: true, Waiting: 4, Since: ago(time.Hour)}}
				in.Held["home/1/main"] = map[string]int{"cpu": 1}
				in.Started["home/1/main"] = ago(time.Hour)
			}, ""},
		{"a stage no worker serves stalls no worker",
			func(in *workersInputs) {
				in.Stages = []stageWait{{Stage: "plan_channels", Pool: "cpu", PerWorker: true, Waiting: 4, Since: ago(time.Hour)}}
			}, ""},
		{"a transcription worker that stopped beating, its jobs waiting",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(3*time.Hour))}
				in.Workers[1].LastSeenAt = ago(workersSilentAfter + time.Second)
				in.Started["home/3/stt"] = ago(time.Minute)
			}, "home/2/stt:no_heartbeat"},
		{"a silent worker's stale claim does not hide it",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(3*time.Hour))}
				in.Workers[1].LastSeenAt = ago(time.Hour)
				in.Held["home/2/stt"] = map[string]int{"gpu": 1}
				in.Started["home/3/stt"] = ago(time.Minute)
			}, "home/2/stt:no_heartbeat"},
		{"a beat just inside the bound is not silent",
			func(in *workersInputs) {
				in.Stages = []stageWait{transcribe(40, ago(3*time.Minute))}
				in.Workers[1].LastSeenAt, in.Workers[2].LastSeenAt = ago(workersSilentAfter), ago(workersSilentAfter)
			}, ""},
		{"every queue paused: nothing is stalled",
			func(in *workersInputs) {
				in.PausedAll = true
				in.Stages = []stageWait{transcribe(40, ago(19*time.Hour))}
			}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mut(&in)
			if got := ids(stalledWorkers(in, now)); got != tc.want {
				t.Fatalf("stalled %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStalledWorkers_NamesEachWorker(t *testing.T) {
	now := t0
	node, name := "9a9a9a9a-0000-4000-8000-000000000001", "Studio PC"
	in := workersInputs{Workers: homeWorkers(now), Held: map[string]map[string]int{}, HeldAll: map[string]int{},
		Started: map[string]time.Time{}}
	in.Workers = append(in.Workers, Worker{ID: "pc/9/stt", Host: "pc", Stages: []string{"transcribe"},
		StartedAt: now.Add(-48 * time.Hour), LastSeenAt: now.Add(-time.Second), NodeID: &node, NodeName: &name})
	in.Stages = []stageWait{
		{Stage: "transcribe", Pool: "gpu", PerWorker: true, Size: intp(1), Waiting: 40, Since: now.Add(-19 * time.Hour)},
		{Stage: "index", Pool: "cpu", PerWorker: true, Waiting: 7, Since: now.Add(-2 * time.Hour)},
		{Stage: "probe", Pool: "cpu", PerWorker: true, Waiting: 5, Since: now.Add(-time.Hour)},
	}
	st := workersState(in, now)
	if st.State != WorkersNotResponding || len(st.Stalled) != 5 || st.Reason != WorkersNoDispatch ||
		st.Waiting != 52 || st.Since == nil || !st.Since.Equal(now.Add(-19*time.Hour)) {
		t.Fatalf("%+v", st)
	}
	by := map[string]StalledWorker{}
	for _, w := range st.Stalled {
		by[w.WorkerID] = w
	}
	for id, want := range map[string][2]string{
		"home/1/main":  {WorkerMain, "core.pipeline"},
		"home/2/stt":   {WorkerTranscription, "transcription"},
		"home/4/index": {WorkerSearch, "search.semantic"},
		"pc/9/stt":     {WorkerTranscription, "transcription"},
	} {
		if w := by[id]; w.Kind != want[0] || w.Component != want[1] {
			t.Errorf("%s: kind %q component %q, want %v", id, w.Kind, w.Component, want)
		}
	}
	// a worker has been up since it started: stalled since the later of that and its work
	if w := by["home/2/stt"]; !w.Since.Equal(now.Add(-19*time.Hour)) || w.Waiting != 40 ||
		!slices.Equal(w.Stages, []string{"transcribe"}) || !w.Restartable || w.NodeName != nil {
		t.Errorf("home transcription worker %+v", w)
	}
	// a joined machine's worker is named, and is not this supervisor's to restart
	if w := by["pc/9/stt"]; w.Restartable || w.NodeName == nil || *w.NodeName != name {
		t.Errorf("node worker %+v", w)
	}
}

func TestWorkersState_NoWorkerRegistered(t *testing.T) {
	now := t0
	in := workersInputs{Stages: []stageWait{{Stage: "probe", Pool: "nas_read", Size: intp(2), Waiting: 6,
		Since: now.Add(-45 * time.Minute)}}}
	st := workersState(in, now)
	if st.State != WorkersNotResponding || st.Reason != WorkersNoHeartbeat || len(st.Stalled) != 0 ||
		st.Waiting != 6 || st.Since == nil || !st.Since.Equal(now.Add(-45*time.Minute)) || st.LastSeenAt != nil {
		t.Fatalf("%+v", st)
	}
	in.Stages[0].Since = now.Add(-time.Minute)
	if st := workersState(in, now); st.State != WorkersRunning || st.Waiting != 0 {
		t.Fatalf("new work, no worker yet: %+v", st)
	}
	in.PausedAll = true
	in.Stages[0].Since = now.Add(-time.Hour)
	if st := workersState(in, now); st.State != WorkersRunning {
		t.Fatalf("paused: %+v", st)
	}
}

// ---------- the pulse ----------

var workerCols = []string{"id", "host", "stages", "pools", "started", "seen", "version", "node", "node_name", "role"}

func workerRow(id string, stages string, started, seen time.Time) []driver.Value {
	return []driver.Value{id, "home", stages, `{}`, started, seen, nil, nil, nil, "home"}
}

// workersRules scripts what loadWorkersInputs reads: the registry, the runnable jobs by
// stage, the jobs held, the leases and the starts.
func workersRules(workers [][]driver.Value, stages [][]driver.Value, held [][]driver.Value,
	started [][]driver.Value) []*queuestest.Rule {
	return []*queuestest.Rule{
		{Match: "from workers w left join cluster_nodes", Cols: workerCols, Rows: workers},
		{Match: "left join worker_pools p", Cols: []string{"stage", "pool", "scope", "size", "n", "since"}, Rows: stages},
		{Match: "max(j.claimed_at)", Cols: []string{"by", "pool", "n", "claimed"}, Rows: held},
		{Match: "from compute_leases", Cols: []string{"pool", "n"}},
		{Match: "kind = 'claimed'", Cols: []string{"message", "at"}, Rows: started},
	}
}

// workersRule is the pulse of a home whose workers beat and nothing waits.
func workersRule() []*queuestest.Rule {
	return workersRules([][]driver.Value{workerRow("home/1/main", "{scan,probe}", t0.Add(-time.Hour), t0.Add(-5*time.Second))},
		nil, nil, nil)
}

// workersPulse is the pulse fixture over the given workers rules.
func workersPulse(running, waiting int64, beat, done any, paused bool, workers []*queuestest.Rule) *fixture {
	rules := append([]*queuestest.Rule{}, workers...)
	rules = append(rules,
		&queuestest.Rule{Match: "max(j.last_heartbeat_at)", Cols: []string{"r", "w", "hb"}, Rows: [][]driver.Value{{running, waiting, beat}}},
		&queuestest.Rule{Match: "select max(finished_at)", Cols: []string{"m"}, Rows: [][]driver.Value{{done}}},
		&queuestest.Rule{Match: "from queue_control", Cols: []string{"p"}, Rows: [][]driver.Value{{paused}}},
	)
	return newFixture(rules...)
}

func TestPulse_Workers(t *testing.T) {
	day := t0.Add(-24 * time.Hour)
	recent := t0.Add(-5 * time.Second)
	registry := [][]driver.Value{
		workerRow("home/1/main", "{scan,probe}", day, recent),
		workerRow("home/2/stt", "{transcribe,second_listen}", day, recent),
	}
	waitingTranscribe := [][]driver.Value{{"transcribe", "gpu", "per_worker", int64(1), int64(40), t0.Add(-19 * time.Hour)}}

	t.Run("jobs run on the main worker while the transcription worker starts nothing: stalled, that worker named", func(t *testing.T) {
		f := workersPulse(2, 40, recent, recent, false, workersRules(registry, waitingTranscribe,
			[][]driver.Value{{"home/1/main", "nas_read", int64(2), recent}}, nil))
		f.h.RestartWorkers = func(context.Context, []string) (int, error) { return 0, nil }
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		w := out.Workers
		if rec.Code != 200 || out.State != PulseStalled || out.Running != 2 || w.State != WorkersNotResponding ||
			w.Reason != WorkersNoDispatch || len(w.Stalled) != 1 || w.Waiting != 40 || !w.CanRestart {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		s := w.Stalled[0]
		if s.WorkerID != "home/2/stt" || s.Kind != WorkerTranscription || s.Component != "transcription" ||
			!s.Since.Equal(t0.Add(-19*time.Hour)) || !s.Restartable || s.Waiting != 40 {
			t.Fatalf("stalled %+v", s)
		}
		// the starts are read as far back as the bound, the runnable jobs as of now
		if c := f.db.Find("kind = 'claimed'"); len(c) != 1 || c[0].Args[0] != driver.Value(t0.Add(-workersIdleAfter)) {
			t.Fatalf("starts query %+v", c)
		}
		if c := f.db.Find("left join worker_pools p"); len(c) != 1 || c[0].Args[0] != driver.Value(t0) {
			t.Fatalf("stages query %+v", c)
		}
	})

	t.Run("the engine's event of a start within the bound clears the worker", func(t *testing.T) {
		f := workersPulse(2, 40, recent, recent, false, workersRules(registry, waitingTranscribe, nil,
			[][]driver.Value{{"started by home/2/stt", t0.Add(-3 * time.Minute)}, {"started by home/1/main", recent}}))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.State != PulseWorking || out.Workers.State != WorkersRunning || len(out.Workers.Stalled) != 0 ||
			!strings.Contains(rec.Body.String(), `"stalled":[]`) || !strings.Contains(rec.Body.String(), `"since":null`) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("with no supervisor nothing is restartable", func(t *testing.T) {
		f := workersPulse(0, 40, nil, recent, false, workersRules(registry, waitingTranscribe, nil, nil))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.Workers.CanRestart || len(out.Workers.Stalled) != 1 || out.Workers.Stalled[0].Restartable {
			t.Fatalf("%s", rec.Body.String())
		}
	})

	t.Run("nothing waits: the jobs held and the starts are not read", func(t *testing.T) {
		f := workersPulse(0, 0, nil, nil, false, workersRule())
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.State != PulseIdle || out.Workers.State != WorkersRunning || out.Workers.LastSeenAt == nil ||
			len(f.db.Find("kind = 'claimed'")) != 0 || len(f.db.Find("max(j.claimed_at)")) != 0 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("every queue paused: paused, no worker called stalled", func(t *testing.T) {
		f := workersPulse(0, 40, nil, day, true, workersRules(registry, waitingTranscribe, nil, nil))
		rec := f.do("GET", "/api/queues/pulse", "", admin())
		var out Pulse
		decode(t, rec, &out)
		if out.State != PulsePaused || out.Workers.State != WorkersRunning || len(out.Workers.Stalled) != 0 {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a failed read of the runnable jobs fails the pulse", func(t *testing.T) {
		rules := workersRule()
		rules[1] = &queuestest.Rule{Match: "left join worker_pools p", Err: errors.New("boom")}
		f := workersPulse(0, 0, nil, nil, false, rules)
		if rec := f.do("GET", "/api/queues/pulse", "", admin()); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	})
}

func TestPulse_Host(t *testing.T) {
	f := workersPulse(0, 0, nil, nil, false, workersRule())
	rec := f.do("GET", "/api/queues/pulse", "", admin())
	if strings.Contains(rec.Body.String(), `"host"`) {
		t.Fatalf("a host block with no reader: %s", rec.Body.String())
	}
	f.h.HostLoad = func(context.Context) (hostload.State, bool) {
		return hostload.State{Overloaded: true, Load1: 512, Load5: 498, Cores: 28, Bound: 56,
			Top: []hostload.Proc{{PID: 9, Name: "runaway", CPUPct: 795}}}, true
	}
	rec = f.do("GET", "/api/queues/pulse", "", admin())
	var out Pulse
	decode(t, rec, &out)
	if out.Host == nil || !out.Host.Overloaded || out.Host.Cores != 28 || len(out.Host.Top) != 1 ||
		out.Host.Top[0].Name != "runaway" {
		t.Fatalf("%s", rec.Body.String())
	}
	f.h.HostLoad = func(context.Context) (hostload.State, bool) { return hostload.State{}, false }
	if rec = f.do("GET", "/api/queues/pulse", "", admin()); strings.Contains(rec.Body.String(), `"host"`) {
		t.Fatalf("a host block where the load cannot be read: %s", rec.Body.String())
	}
}

// ---------- the restart ----------

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
		for _, body := range []string{"", `{"components":["transcription"]}`} {
			t.Run(c.name+" "+body, func(t *testing.T) {
				f := newFixture()
				asked := 0
				f.h.RestartWorkers = func(context.Context, []string) (int, error) { asked++; return 3, nil }
				rec := f.do("POST", "/api/queues/workers/restart", body, c.p)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%d %s", rec.Code, rec.Body.String())
				}
				if asked != 0 || len(f.db.Calls()) != 0 {
					t.Fatalf("a refused caller reached the supervisor (%d) or the database (%d)", asked, len(f.db.Calls()))
				}
			})
		}
	}
}

func TestRestartWorkers_Admin(t *testing.T) {
	restarter := func(f *fixture, n int, err error) *[][]string {
		var asked [][]string
		f.h.RestartWorkers = func(_ context.Context, ids []string) (int, error) {
			asked = append(asked, ids)
			return n, err
		}
		return &asked
	}

	t.Run("no body: every worker, audited", func(t *testing.T) {
		f := newFixture()
		asked := restarter(f, 3, nil)
		rec := f.do("POST", "/api/queues/workers/restart", "", admin())
		var out workersRestarted
		decode(t, rec, &out)
		if rec.Code != http.StatusOK || out.Restarted != 3 || len(*asked) != 1 || len((*asked)[0]) != 0 {
			t.Fatalf("%d %s asked %v", rec.Code, rec.Body.String(), *asked)
		}
		audit := f.db.Find("insert into audit_log")
		if len(audit) != 1 || audit[0].Args[0] != driver.Value("queue.workers.restart") ||
			!strings.Contains(audit[0].Args[3].(string), `"restarted":3`) {
			t.Fatalf("audit %+v", audit)
		}
	})

	t.Run("only the components named, each once", func(t *testing.T) {
		f := newFixture()
		asked := restarter(f, 2, nil)
		rec := f.do("POST", "/api/queues/workers/restart", `{"components":["transcription","transcription"]}`, admin())
		var out workersRestarted
		decode(t, rec, &out)
		if rec.Code != http.StatusOK || !slices.Equal((*asked)[0], []string{"transcription"}) ||
			!slices.Equal(out.Components, []string{"transcription"}) {
			t.Fatalf("%d %s asked %v", rec.Code, rec.Body.String(), *asked)
		}
		if a := f.db.Find("insert into audit_log"); len(a) != 1 || !strings.Contains(a[0].Args[3].(string), `"components":["transcription"]`) {
			t.Fatalf("audit %+v", a)
		}
	})

	t.Run("a component with no worker, or a body that is not JSON: 400, nothing asked", func(t *testing.T) {
		for _, body := range []string{`{"components":["youtube"]}`, `{"components":["../x"]}`, `not json`} {
			f := newFixture()
			asked := restarter(f, 1, nil)
			if rec := f.do("POST", "/api/queues/workers/restart", body, admin()); rec.Code != http.StatusBadRequest || len(*asked) != 0 {
				t.Fatalf("%s: %d %s asked %v", body, rec.Code, rec.Body.String(), *asked)
			}
		}
	})

	t.Run("no worker runs now: accepted", func(t *testing.T) {
		f := newFixture()
		restarter(f, 0, nil)
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
		restarter(f, 0, errors.New("supervisor unreachable"))
		rec := f.do("POST", "/api/queues/workers/restart", "", admin())
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), CodeWorkersRestartUnavailable) {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if n := len(f.db.Find("insert into audit_log")); n != 0 {
			t.Fatalf("audited %d", n)
		}
	})
}
