package systemstatus

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tv"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tvcluster"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func newEnv(h *Handler) *env {
	if h.Now == nil {
		h.Now = func() time.Time { return t0 }
	}
	return &env{h: h, now: h.Now(), rules: DefaultRules()}
}

func only(t *testing.T, items []Item) Item {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("want one item, got %d: %+v", len(items), items)
	}
	return items[0]
}

func want(t *testing.T, it Item, status, code string) {
	t.Helper()
	if it.Status != status || it.Code != code {
		t.Fatalf("%s: got %s/%s (%s), want %s/%s", it.ID, it.Status, it.Code, it.Reason, status, code)
	}
	if it.Reason == "" || it.Help == "" || it.Group == "" {
		t.Fatalf("%s: reason, help and group are required: %+v", it.ID, it)
	}
}

// ---------- fakes ----------

type fakeCluster struct {
	resp tvcluster.Response
	err  error
	n    atomic.Int32
}

func (f *fakeCluster) Read(context.Context) (tvcluster.Response, error) {
	f.n.Add(1)
	return f.resp, f.err
}

type fakeTV struct {
	plex    tv.PlexStatus
	plexErr error
	dev     tv.Device
	devErr  error
}

func (f *fakeTV) ReadPlexStatus(context.Context) (tv.PlexStatus, error) { return f.plex, f.plexErr }
func (f *fakeTV) ReadDevice(context.Context) (tv.Device, error)         { return f.dev, f.devErr }

type fakeQueues struct {
	r   queues.HealthRollup
	err error
}

func (f fakeQueues) HealthRollup(context.Context) (queues.HealthRollup, error) { return f.r, f.err }

type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

func dialOK(context.Context, string, string) (net.Conn, error) { return fakeConn{}, nil }
func dialRefused(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("connection refused")
}

// slowDB answers every query with one integer after a delay (a slow or
// stuck database), honouring the context.
type slowDriver struct{}
type slowConn struct{ delay time.Duration }
type slowRows struct{ done bool }

var (
	slowOnce   sync.Once
	slowDelays sync.Map
)

func (slowDriver) Open(name string) (driver.Conn, error) {
	d, _ := slowDelays.Load(name)
	return &slowConn{delay: d.(time.Duration)}, nil
}
func (c *slowConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("no prepare") }
func (c *slowConn) Close() error                        { return nil }
func (c *slowConn) Begin() (driver.Tx, error)           { return nil, errors.New("no tx") }
func (c *slowConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	select {
	case <-time.After(c.delay):
		return &slowRows{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (r *slowRows) Columns() []string { return []string{"n"} }
func (r *slowRows) Close() error      { return nil }
func (r *slowRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	return nil
}

func slowDB(t *testing.T, d time.Duration) *sql.DB {
	slowOnce.Do(func() { sql.Register("systemstatus-slow", slowDriver{}) })
	name := t.Name()
	slowDelays.Store(name, d)
	db, err := sql.Open("systemstatus-slow", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------- services ----------

func TestAPI(t *testing.T) {
	h := &Handler{Version: "1.2.3", StartTime: t0.Add(-90 * time.Minute)}
	it := only(t, checkAPI(context.Background(), newEnv(h)))
	want(t, it, StatusOK, "api_ok")
	if !it.Critical || it.Params["version"] != "1.2.3" || it.Params["uptime_seconds"] != int64(5400) {
		t.Fatalf("api: %+v", it)
	}
}

func TestPostgres(t *testing.T) {
	ctx := context.Background()
	want(t, only(t, checkPostgres(ctx, newEnv(&Handler{}))), StatusDown, "postgres_not_configured")

	db := queuestest.New(
		&queuestest.Rule{Match: "select 1", Cols: []string{"n"}, Rows: [][]driver.Value{{int64(1)}}},
		&queuestest.Rule{Match: "pg_is_in_recovery", Cols: []string{"r"}, Rows: [][]driver.Value{{false}}},
		&queuestest.Rule{Match: "pg_stat_replication", Cols: []string{"n"}, Rows: [][]driver.Value{{int64(2)}}},
	)
	it := only(t, checkPostgres(ctx, newEnv(&Handler{DB: db.DB})))
	want(t, it, StatusOK, "postgres_ok")
	if !it.Critical || it.Details["role"] != "primary" || it.Details["replicas"] != 2 {
		t.Fatalf("postgres details: %+v", it.Details)
	}

	bad := queuestest.New(&queuestest.Rule{Match: "select 1", Err: errors.New("connection refused")})
	it = only(t, checkPostgres(ctx, newEnv(&Handler{DB: bad.DB})))
	want(t, it, StatusDown, "postgres_unreachable")

	e := newEnv(&Handler{DB: slowDB(t, 30*time.Millisecond)})
	e.rules.DBLatencyMs = 5
	want(t, only(t, checkPostgres(ctx, e)), StatusDegraded, "postgres_slow")
}

func TestStreaming(t *testing.T) {
	ctx := context.Background()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer up.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	live := &fakeCluster{resp: tvcluster.Response{
		Hosts: []tvcluster.Host{{HostID: "h1", Name: "home", State: "online"}},
		OnAir: []tvcluster.OnAir{{Name: "Quran", Encoder: "nvenc"}, {Name: "Kids", Encoder: "software"},
			{Name: "Old", Stale: true}},
	}}

	it := only(t, checkStreaming(ctx, newEnv(&Handler{StreamingURL: up.URL, StreamingAddr: "x:1",
		Dial: dialOK, TVCluster: live})))
	want(t, it, StatusOK, "streaming_ok")
	if it.Details["on_air"] != 2 || it.Details["encoder_sessions"] != 2 || !it.Critical {
		t.Fatalf("streaming details: %+v", it)
	}

	// down while channels are on air is critical
	it = only(t, checkStreaming(ctx, newEnv(&Handler{StreamingURL: broken.URL, TVCluster: live})))
	want(t, it, StatusDown, "streaming_unreachable")
	if !it.Critical {
		t.Fatal("streaming down with channels on air must be critical")
	}
	// down with nothing on air is not
	it = only(t, checkStreaming(ctx, newEnv(&Handler{StreamingURL: broken.URL, TVCluster: &fakeCluster{}})))
	want(t, it, StatusDown, "streaming_unreachable")
	if it.Critical {
		t.Fatal("streaming down with nothing on air must not be critical")
	}
	it = only(t, checkStreaming(ctx, newEnv(&Handler{StreamingURL: up.URL, StreamingAddr: "x:1",
		Dial: dialRefused, TVCluster: live})))
	want(t, it, StatusDegraded, "streaming_grpc_unreachable")
	want(t, only(t, checkStreaming(ctx, newEnv(&Handler{}))), StatusDegraded, "streaming_not_configured")
}

func TestPipeline(t *testing.T) {
	ctx := context.Background()
	want(t, only(t, checkPipeline(ctx, newEnv(&Handler{}))), StatusDegraded, "pipeline_not_configured")
	want(t, only(t, checkPipeline(ctx, newEnv(&Handler{PipelineAddr: "p:50051", Dial: dialOK}))),
		StatusOK, "pipeline_ok")
	it := only(t, checkPipeline(ctx, newEnv(&Handler{PipelineAddr: "p:50051", Dial: dialRefused})))
	want(t, it, StatusDown, "pipeline_unreachable")
	if it.Critical {
		t.Fatal("the pipeline is not critical")
	}
}

func TestProcesses(t *testing.T) {
	sup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"processes":[{"role":"api","state":"running"},
			{"role":"streaming","state":"backoff","restarts":3},
			{"role":"pipeline","state":"running","crash_loop":true}]}`))
	}))
	defer sup.Close()
	got := checkProcesses(context.Background(), newEnv(&Handler{SupervisorURL: sup.URL}))
	if len(got) != 3 {
		t.Fatalf("processes: %+v", got)
	}
	want(t, got[0], StatusOK, "process_running")
	want(t, got[1], StatusDegraded, "process_restarting")
	want(t, got[2], StatusDown, "process_crash_loop")

	gone := checkProcesses(context.Background(), newEnv(&Handler{SupervisorURL: "http://127.0.0.1:1"}))
	want(t, only(t, gone), StatusDegraded, "process_supervisor_unreachable")
}

// ---------- workers ----------

func worker(id, host string, seen time.Duration, stages ...string) queues.Worker {
	return queues.Worker{ID: id, Host: host, LastSeenAt: t0.Add(-seen), Stages: stages, Role: "home"}
}

func byID(items []Item) map[string]Item {
	out := map[string]Item{}
	for _, it := range items {
		out[it.ID] = it
	}
	return out
}

func TestWorkerStates(t *testing.T) {
	r := DefaultRules()
	ws := []queues.Worker{
		worker("fresh", "nas", 5*time.Second, "probe", "detect"),
		worker("late", "pc", 2*time.Minute, "probe"),
		worker("gone", "mac", 10*time.Minute, "transcribe"),
	}
	running := map[string]map[string]int{"fresh": {"probe": 3, "detect": 1}}
	got := byID(workerItems(ws, running, t0, r))
	want(t, got["worker:fresh"], StatusOK, "worker_ok")
	want(t, got["worker:late"], StatusDegraded, "worker_late")
	want(t, got["worker:gone"], StatusDown, "worker_gone")
	if got["worker:fresh"].Params["jobs"] != 4 || got["worker:fresh"].Details["role"] != "main" {
		t.Fatalf("fresh worker: %+v", got["worker:fresh"])
	}
	if got["worker:gone"].Details["role"] != "transcription" {
		t.Fatalf("a transcribe-only worker is a transcription worker: %+v", got["worker:gone"].Details)
	}
	sum := got["workers"]
	want(t, sum, StatusDegraded, "workers_some_missing")
	if !sum.Critical || sum.Params["online"] != 1 || sum.Params["total"] != 3 {
		t.Fatalf("summary: %+v", sum)
	}
}

// A worker that restarted under a new id leaves its old row behind: with a
// fresh worker on the same machine, the old row is history, never a failure.
func TestStaleDuplicateIsHistoryNotFailure(t *testing.T) {
	node := "node-1"
	old := worker("old", "MacBook", 3*time.Hour, "transcribe")
	cur := worker("new", "macbook", 3*time.Second, "transcribe")
	oldNode := worker("old-node", "pc-a", time.Hour, "probe")
	oldNode.NodeID = &node
	newNode := worker("new-node", "pc-b", 2*time.Second, "probe")
	newNode.NodeID = &node
	items := workerItems([]queues.Worker{old, cur, oldNode, newNode}, nil, t0, DefaultRules())
	got := byID(items)
	for _, id := range []string{"worker:old", "worker:old-node"} {
		it := got[id]
		want(t, it, StatusOK, "worker_replaced")
		if !it.History {
			t.Fatalf("%s must be history", id)
		}
	}
	want(t, got["workers"], StatusOK, "workers_ok")
	status, _, _, _ := rollup(items)
	if status != StatusOK {
		t.Fatalf("a stale duplicate must not make the page %s", status)
	}
	// history comes after the workers that run
	if !items[len(items)-1].History || items[0].ID != "workers" {
		t.Fatalf("order: %+v", items)
	}
}

func TestWorkerForgottenAndAllGone(t *testing.T) {
	r := DefaultRules()
	got := byID(workerItems([]queues.Worker{worker("ancient", "old-pc", 72*time.Hour)}, nil, t0, r))
	if !got["worker:ancient"].History {
		t.Fatal("a worker silent past worker_forget_hours is history")
	}
	want(t, got["workers"], StatusDown, "workers_none")

	got = byID(workerItems([]queues.Worker{worker("a", "nas", 20*time.Minute)}, nil, t0, r))
	want(t, got["workers"], StatusDown, "workers_all_gone")
	if !got["workers"].Critical {
		t.Fatal("every worker gone is critical")
	}
	got = byID(workerItems(nil, nil, t0, r))
	want(t, got["workers"], StatusDown, "workers_none")
}

func TestCheckWorkersReadsTheRegistry(t *testing.T) {
	db := queuestest.New(
		&queuestest.Rule{Match: "from workers w left join cluster_nodes",
			Cols: []string{"id", "host", "stages", "pools", "started_at", "last_seen_at", "version", "node_id", "node_name", "role"},
			Rows: [][]driver.Value{
				{"w1", "nas", "{probe,detect}", nil, t0.Add(-time.Hour), t0.Add(-5 * time.Second), "1.0", nil, nil, "home"},
			}},
		&queuestest.Rule{Match: "state = 'running' and claimed_by", Cols: []string{"w", "stage", "n"},
			Rows: [][]driver.Value{{"w1", "probe", int64(2)}}},
	)
	got := byID(checkWorkers(context.Background(), newEnv(&Handler{DB: db.DB})))
	want(t, got["worker:w1"], StatusOK, "worker_ok")
	if got["worker:w1"].Params["jobs"] != 2 {
		t.Fatalf("running jobs: %+v", got["worker:w1"])
	}
	bad := queuestest.New(&queuestest.Rule{Match: "from workers w", Err: errors.New("boom")})
	want(t, only(t, checkWorkers(context.Background(), newEnv(&Handler{DB: bad.DB}))),
		StatusDegraded, "workers_unreadable")
}

func TestStreamingHosts(t *testing.T) {
	beat := t0.Add(-2 * time.Second)
	old := t0.Add(-time.Hour)
	why := "no heartbeat for over 15 s"
	cl := &fakeCluster{resp: tvcluster.Response{Hosts: []tvcluster.Host{
		{HostID: "a", Name: "home", State: "online", LastBeatAt: &beat, Used: 2, Capacity: 4},
		{HostID: "b", Name: "Home", State: "down", LastBeatAt: &old, Reason: &why},
		{HostID: "c", Name: "pc", State: "down", LastBeatAt: &old, Reason: &why},
		{HostID: "d", Name: "nas", State: "disabled", LastBeatAt: &beat},
	}}}
	got := byID(checkStreamingHosts(context.Background(), newEnv(&Handler{TVCluster: cl})))
	want(t, got["streaming_host:a"], StatusOK, "streaming_host_ok")
	want(t, got["streaming_host:b"], StatusOK, "streaming_host_replaced")
	if !got["streaming_host:b"].History {
		t.Fatal("a down record of an online machine is history")
	}
	want(t, got["streaming_host:c"], StatusDown, "streaming_host_down")
	want(t, got["streaming_host:d"], StatusOK, "streaming_host_disabled")
}

// ---------- dependencies ----------

func TestDependencies(t *testing.T) {
	db := queuestest.New(&queuestest.Rule{Match: "from dependency_health",
		Cols: []string{"name", "healthy", "message", "checked_at", "since"},
		Rows: [][]driver.Value{
			{"dependency:model:tagcheck", false, "http://ollama:11434 does not answer", t0.Add(-30 * time.Second), t0.Add(-time.Hour)},
			{"dependency:share:/mnt/nas", true, "answers", t0.Add(-time.Minute), t0.Add(-time.Hour)},
			{"resource:disk", true, "enough free space", t0.Add(-time.Hour), t0.Add(-48 * time.Hour)},
			{"resource:shm", true, "", "2026-10-22T11:59:00.000Z", "2026-10-21T11:59:00.000Z"},
		}})
	got := byID(checkDependencies(context.Background(), newEnv(&Handler{DB: db.DB})))
	want(t, got["dependency:dependency:model:tagcheck"], StatusDown, "dependency_down")
	want(t, got["dependency:dependency:share:/mnt/nas"], StatusOK, "dependency_ok")
	want(t, got["dependency:resource:disk"], StatusDegraded, "dependency_stale")
	shm := got["dependency:resource:shm"]
	want(t, shm, StatusOK, "dependency_ok")
	if !shm.LastChecked.Equal(t0.Add(-time.Minute)) {
		t.Fatalf("last_checked is the probe's own time: %v", shm.LastChecked)
	}
	empty := queuestest.New()
	want(t, only(t, checkDependencies(context.Background(), newEnv(&Handler{DB: empty.DB}))),
		StatusOK, "dependencies_none")
}

// ---------- integrations ----------

func plexStatus(status string) tv.PlexStatus {
	checked := "2026-10-22T11:58:00.000Z"
	st := tv.PlexStatus{Plex: tv.PlexExtension{Enabled: true, Configured: true, URL: "http://plex:32400"},
		CheckedAt: &checked, Status: map[string]any{}}
	_ = json.Unmarshal([]byte(status), &st.Status)
	return st
}

func TestPlex(t *testing.T) {
	ctx := context.Background()
	run := func(p tv.PlexStatus) Item {
		return only(t, checkPlex(ctx, newEnv(&Handler{TV: &fakeTV{plex: p}})))
	}
	want(t, run(tv.PlexStatus{}), StatusOK, "plex_off")
	want(t, run(plexStatus(`{"reachable":false,"error":"connection refused"}`)), StatusDown, "plex_unreachable")
	want(t, run(plexStatus(`{"reachable":true,"maktaba":{"registered":false}}`)), StatusDegraded, "plex_not_registered")
	want(t, run(plexStatus(`{"reachable":true,"maktaba":{"registered":true},
		"channels":{"lineup":12,"mapped":10,"in_sync":false}}`)), StatusDegraded, "plex_out_of_sync")
	ok := run(plexStatus(`{"reachable":true,"maktaba":{"registered":true},
		"channels":{"lineup":12,"mapped":12,"in_sync":true}}`))
	want(t, ok, StatusOK, "plex_ok")
	if !ok.LastChecked.Equal(t0.Add(-2 * time.Minute)) {
		t.Fatalf("last_checked is the stage's check: %v", ok.LastChecked)
	}
	withErr := plexStatus(`{"reachable":true,"maktaba":{"registered":true},"channels":{"in_sync":true}}`)
	msg, at := "guide reload failed", "2026-10-22T11:58:00.000Z"
	withErr.LastError, withErr.LastErrorAt = &msg, &at
	want(t, run(withErr), StatusDegraded, "plex_error")
	never := plexStatus(`{}`)
	never.CheckedAt = nil
	want(t, run(never), StatusDegraded, "plex_never_checked")
}

// ---------- queues & TV ----------

func TestQueues(t *testing.T) {
	ctx := context.Background()
	run := func(r queues.HealthRollup, err error) Item {
		return only(t, checkQueues(ctx, newEnv(&Handler{Queues: fakeQueues{r: r, err: err}})))
	}
	it := run(queues.HealthRollup{Stages: 20, Failing: []string{"detect"}, NeedsYou: 3}, nil)
	want(t, it, StatusDegraded, "queues_failing")
	if it.Link != "/queue/failures" {
		t.Fatalf("failing links to the failures page: %s", it.Link)
	}
	want(t, run(queues.HealthRollup{Stages: 20, Waiting: []string{"tag"}}, nil), StatusDegraded, "queues_waiting")
	needs := run(queues.HealthRollup{Stages: 20, NeedsYou: 3}, nil)
	want(t, needs, StatusOK, "queues_needs_you")
	if !needs.Attention {
		t.Fatal("needs-you is ok with attention")
	}
	ok := run(queues.HealthRollup{Stages: 20}, nil)
	want(t, ok, StatusOK, "queues_ok")
	if ok.Link != "/queue" {
		t.Fatalf("link: %s", ok.Link)
	}
	want(t, run(queues.HealthRollup{}, errors.New("boom")), StatusDegraded, "queues_unreadable")
}

func TestTV(t *testing.T) {
	ctx := context.Background()
	cl := &fakeCluster{resp: tvcluster.Response{OnAir: []tvcluster.OnAir{
		{Name: "Quran", Viewers: tvcluster.Viewers{Tuners: 2}}, {Name: "Kids", Viewers: tvcluster.Viewers{Tuners: 1}},
	}}}
	want(t, only(t, checkTVChannels(ctx, newEnv(&Handler{TVCluster: cl}))), StatusOK, "tv_channels_ok")
	stale := &fakeCluster{resp: tvcluster.Response{OnAir: []tvcluster.OnAir{{Name: "Kids", Stale: true}}}}
	want(t, only(t, checkTVChannels(ctx, newEnv(&Handler{TVCluster: stale}))), StatusDegraded, "tv_channels_stale")

	tuner := func(d tv.Device, err error) Item {
		return only(t, checkTVTuner(ctx, newEnv(&Handler{TV: &fakeTV{dev: d, devErr: err}, TVCluster: cl})))
	}
	want(t, tuner(tv.Device{}, sql.ErrNoRows), StatusOK, "tv_tuner_not_set_up")
	want(t, tuner(tv.Device{Enabled: false, TunerCount: 4}, nil), StatusOK, "tv_tuner_off")
	want(t, tuner(tv.Device{Enabled: true, TunerCount: 4}, nil), StatusOK, "tv_tuner_ok")
	want(t, tuner(tv.Device{Enabled: true, TunerCount: 3}, nil), StatusDegraded, "tv_tuner_full")

	changes := queuestest.New(&queuestest.Rule{Match: "from tv_changes",
		Cols: []string{"id", "number", "state", "approved_by"},
		Rows: [][]driver.Value{
			{"c1", int64(7), "ready", []byte(`["u1"]`)}, // approved: on its way
			{"c2", int64(8), "ready", []byte(`[]`)},
		}})
	it := only(t, checkTVChange(ctx, newEnv(&Handler{DB: changes.DB})))
	want(t, it, StatusOK, "tv_change_ready")
	if !it.Attention || it.Link != "/admin/live-tv/changes/c2" || it.Params["number"] != int64(8) {
		t.Fatalf("tv change: %+v", it)
	}
	want(t, only(t, checkTVChange(ctx, newEnv(&Handler{DB: queuestest.New().DB}))), StatusOK, "tv_change_none")

	slots := queuestest.New(&queuestest.Rule{Match: "from tv_slots",
		Cols: []string{"host_id", "slot", "state", "beat_at"},
		Rows: [][]driver.Value{
			{"home", "blue", "healthy", t0.Add(-3 * time.Second)},
			{"pc", "green", "healthy", t0.Add(-time.Minute)},
		}})
	got := byID(checkTVSlots(ctx, newEnv(&Handler{DB: slots.DB})))
	want(t, got["tv_slot:home:blue"], StatusOK, "tv_slot_ok")
	want(t, got["tv_slot:pc:green"], StatusDown, "tv_slot_unhealthy")
	want(t, only(t, checkTVSlots(ctx, newEnv(&Handler{DB: queuestest.New().DB}))), StatusOK, "tv_slot_single")
}

// ---------- rollup ----------

func TestRollup(t *testing.T) {
	ok := Item{Name: "API", Status: StatusOK}
	cases := []struct {
		name  string
		items []Item
		want  string
		code  string
	}{
		{"all ok", []Item{ok}, StatusOK, "overall_ok"},
		{"nothing", nil, StatusOK, "overall_ok"},
		{"critical down", []Item{ok, {Name: "Database", Status: StatusDown, Critical: true}}, StatusDown, "overall_down"},
		{"non-critical down", []Item{ok, {Name: "Pipeline", Status: StatusDown}}, StatusDegraded, "overall_degraded"},
		{"critical degraded", []Item{{Name: "Database", Status: StatusDegraded, Critical: true}}, StatusDegraded, "overall_degraded"},
		{"history ignored", []Item{ok, {Name: "old", Status: StatusDown, Critical: true, History: true}}, StatusOK, "overall_ok"},
	}
	for _, c := range cases {
		st, code, _, reason := rollup(c.items)
		if st != c.want || code != c.code || reason == "" {
			t.Errorf("%s: got %s/%s %q", c.name, st, code, reason)
		}
	}
	_, _, p, reason := rollup([]Item{{Name: "Database", Status: StatusDown, Critical: true},
		{Name: "Pipeline", Status: StatusDegraded}})
	if p["items"] != "Database" || !strings.Contains(reason, "Database") {
		t.Fatalf("down names only the critical items: %v %q", p, reason)
	}
}

// ---------- timeouts and the round ----------

func TestSlowCheckIsCutAtItsDeadline(t *testing.T) {
	e := newEnv(&Handler{})
	block := make(chan struct{})
	defer close(block)
	checks := []check{
		{Name: "fast", Fallback: base("fast", GroupServices, KindAPI, "Fast"), Run: func(context.Context, *env) []Item {
			it := base("fast", GroupServices, KindAPI, "Fast")
			it.set(StatusOK, "api_ok", nil, "fine")
			return one(it)
		}},
		{Name: "stuck", Fallback: crit(base("stuck", GroupServices, KindPostgres, "Stuck")),
			Run: func(context.Context, *env) []Item { <-block; return nil }},
		{Name: "slow", Fallback: base("slow", GroupServices, KindPipeline, "Slow"),
			Run: func(ctx context.Context, _ *env) []Item { <-ctx.Done(); return nil }},
		{Name: "panics", Fallback: base("panics", GroupServices, KindStreaming, "Panics"),
			Run: func(context.Context, *env) []Item { panic("bad") }},
	}
	start := time.Now()
	got := byID(runChecks(context.Background(), e, checks, 50*time.Millisecond))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the round waited %v for a stuck check", d)
	}
	want(t, got["fast"], StatusOK, "api_ok")
	want(t, got["stuck"], StatusDown, "check_timeout") // critical: down
	want(t, got["slow"], StatusDegraded, "check_timeout")
	want(t, got["panics"], StatusDegraded, "check_timeout")
	if got["stuck"].LastChecked.IsZero() {
		t.Fatal("a fallback has its check time")
	}
}

func TestStuckDatabaseIsDownWithinTheTimeout(t *testing.T) {
	r := DefaultRules()
	r.CheckTimeoutMs = 50
	h := &Handler{DB: slowDB(t, 5*time.Second), Rules: &r, Now: func() time.Time { return t0 }}
	start := time.Now()
	rep := h.compute(context.Background())
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the round took %v", d)
	}
	pg := byID(rep.Items)["postgres"]
	if pg.Status != StatusDown || rep.Status != StatusDown {
		t.Fatalf("a stuck database is down and the page is down: %s / %s", pg.Status, rep.Status)
	}
}

func TestReportIsCachedAndShared(t *testing.T) {
	now := t0
	r := DefaultRules()
	cl := &fakeCluster{}
	h := &Handler{Rules: &r, TVCluster: cl, Now: func() time.Time { return now }}
	first := h.Report(context.Background())
	if n := cl.n.Load(); n != 1 {
		t.Fatalf("one round reads the cluster once, got %d", n)
	}
	now = now.Add(2 * time.Second)
	if again := h.Report(context.Background()); !again.CheckedAt.Equal(first.CheckedAt) || cl.n.Load() != 1 {
		t.Fatal("within cache_seconds the answer is reused")
	}
	now = now.Add(10 * time.Second)
	if later := h.Report(context.Background()); later.CheckedAt.Equal(first.CheckedAt) || cl.n.Load() != 2 {
		t.Fatal("after cache_seconds a new round runs")
	}
}

func TestGetIsAdminOnly(t *testing.T) {
	r := DefaultRules()
	h := &Handler{Rules: &r, Version: "9.9.9"}
	mux := chi.NewRouter()
	h.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/system/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/system/status", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u", IsAdmin: false}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/admin/system/status", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{UserID: "u", IsAdmin: true}))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin: %d", rec.Code)
	}
	var rep Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	ids := byID(rep.Items)
	if rep.Status != StatusDown || ids["api"].Status != StatusOK || ids["postgres"].Code != "postgres_not_configured" {
		t.Fatalf("no database: %+v", rep)
	}
	if rep.Items[0].Group != GroupServices || rep.Items[len(rep.Items)-1].Group == GroupServices {
		t.Fatalf("items are in page order: %+v", rep.Items)
	}
}

// ---------- rules ----------

func TestRulesDefaultsMatchTheSchema(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "rules", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Sections []struct {
			Key    string `json:"key"`
			Fields []struct {
				Name    string          `json:"name"`
				Default json.RawMessage `json:"default"`
			} `json:"fields"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	def := map[string]any{}
	raw, _ := json.Marshal(DefaultRules())
	_ = json.Unmarshal(raw, &def)
	found := false
	for _, s := range schema.Sections {
		if s.Key != "system_health" {
			continue
		}
		found = true
		if len(s.Fields) != len(def) {
			t.Fatalf("schema has %d fields, the API %d", len(s.Fields), len(def))
		}
		for _, f := range s.Fields {
			var d float64
			if json.Unmarshal(f.Default, &d) != nil || def[f.Name] != d {
				t.Errorf("%s: schema %s, API %v", f.Name, f.Default, def[f.Name])
			}
		}
	}
	if !found {
		t.Fatal("schema.json has no system_health section")
	}
}

func TestApplyRules(t *testing.T) {
	got := applyRules(DefaultRules(), []byte(`{"heartbeat_late_seconds":120,"heartbeat_gone_seconds":30,
		"check_timeout_ms":5,"cache_seconds":0,"worker_forget_hours":"x"}`))
	if got.HeartbeatLateSeconds != 120 || got.HeartbeatGoneSeconds != 120 {
		t.Fatalf("gone is never before late: %+v", got)
	}
	if got.CheckTimeoutMs != 2000 || got.CacheSeconds != 0 || got.WorkerForgetHours != 24 {
		t.Fatalf("out of range keeps the default: %+v", got)
	}
	if applyRules(DefaultRules(), []byte(`nope`)) != DefaultRules() {
		t.Fatal("a bad value keeps the defaults")
	}
}

// ---------- help ----------

var helpAnchor = regexp.MustCompile(`(?m)^#{1,6}\s.*\{#([a-z0-9-]+)\}\s*$`)

// The page links every item to /help/system-health#item-<kind>; a kind
// added here without its entry in both languages fails.
func TestEveryKindHasHelp(t *testing.T) {
	for _, locale := range []string{"en", "ar"} {
		path := filepath.Join("..", "..", "..", "..", "web", "src", "help", "content", locale, "system-health.md")
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			t.Skipf("web/ is not in this checkout (%s)", path)
		}
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, m := range helpAnchor.FindAllStringSubmatch(string(b), -1) {
			have[m[1]] = true
		}
		for _, k := range Kinds {
			a := "item-" + strings.ReplaceAll(k, "_", "-")
			if !have[a] {
				t.Errorf("%s/system-health.md: no entry {#%s} for item kind %q", locale, a, k)
			}
		}
	}
}
