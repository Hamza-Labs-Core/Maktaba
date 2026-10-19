package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

func TestBrowserNeedsSignIn(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, f.wsURL("/api/cluster/ws"), nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous browser: %v %v", err, resp)
	}
}

func TestBrowserAdminTopics(t *testing.T) {
	f := newFixture(t)
	v := f.browser("viewer")
	for _, topic := range []string{TopicMetrics, TopicLLM, TopicProcesses, "logs:*:info"} {
		v.send(map[string]any{"op": "subscribe", "topics": []string{topic}})
		m := v.nextType("error")
		if !strings.Contains(string(m.Data), `"forbidden"`) {
			t.Fatalf("%s: %s", topic, m.Data)
		}
	}
	v.send(map[string]any{"op": "subscribe", "topics": []string{"nonsense"}})
	if m := v.nextType("error"); !strings.Contains(string(m.Data), "unknown_topic") {
		t.Fatalf("unknown topic: %s", m.Data)
	}
	v.subscribe(TopicQueues, TopicJobs, TopicWorkers, TopicLibrary)
	v.send(map[string]any{"op": "cmd", "id": 7, "session": "x", "name": "drain"})
	if m := v.nextType("cmd.result"); !strings.Contains(string(m.Data), "forbidden") {
		t.Fatalf("viewer cmd: %s", m.Data)
	}
	v.send(map[string]any{"op": "ping"})
	v.nextType("pong")
}

func TestBrowserProcessesMetricsJobsAndCmd(t *testing.T) {
	f := newFixture(t)
	b := f.browser("admin")
	b.send(map[string]any{"op": "subscribe", "topics": []string{TopicProcesses}})
	snap := b.next(func(m browserMsg) bool { return m.Type == "snapshot" && m.Topic == TopicProcesses })
	var ps struct {
		Processes []Process `json:"processes"`
		Files     Location  `json:"files"`
	}
	_ = json.Unmarshal(snap.Data, &ps)
	if len(ps.Processes) != 0 || ps.Files.Dir != f.dir {
		t.Fatalf("snapshot = %s", snap.Data)
	}
	b.subscribe(TopicMetrics, TopicJobs)

	a, nodeID := f.node("box")
	m := b.nextType("process")
	var ev processEvent
	_ = json.Unmarshal(m.Data, &ev)
	if ev.Event != "connected" || ev.Process == nil || *ev.Process.NodeID != nodeID {
		t.Fatalf("connected = %s", m.Data)
	}

	a.send(map[string]any{"t": "metrics", "ts": "2026-10-19T00:00:00Z", "system": map[string]any{"cpu_pct": 50}})
	m = b.nextType("metrics")
	var mm struct {
		Session string         `json:"session"`
		Metrics map[string]any `json:"metrics"`
	}
	_ = json.Unmarshal(m.Data, &mm)
	if mm.Session != a.Session || mm.Metrics["system"].(map[string]any)["cpu_pct"] != float64(50) {
		t.Fatalf("metrics = %s", m.Data)
	}

	a.send(map[string]any{"t": "job", "event": "claimed", "job_id": 42, "stage": "transcribe"})
	m = b.nextType("job")
	var jm struct {
		Session  string         `json:"session"`
		IP       string         `json:"ip"`
		NodeName *string        `json:"node_name"`
		WorkerID string         `json:"worker_id"`
		Host     string         `json:"host"`
		Job      map[string]any `json:"job"`
	}
	_ = json.Unmarshal(m.Data, &jm)
	if jm.Session != a.Session || jm.IP != "127.0.0.1" || jm.Job["job_id"] != float64(42) || jm.Job["t"] != nil ||
		jm.NodeName == nil || *jm.NodeName != "box" || jm.WorkerID == "" || jm.Host != "test-host" {
		t.Fatalf("job = %s", m.Data)
	}
	// a late subscriber gets the recent jobs
	late := f.browser("viewer")
	late.send(map[string]any{"op": "subscribe", "topics": []string{TopicJobs}})
	s := late.next(func(m browserMsg) bool { return m.Type == "snapshot" })
	var js struct {
		Jobs []json.RawMessage `json:"jobs"`
	}
	if json.Unmarshal(s.Data, &js) != nil || len(js.Jobs) != 1 || !strings.Contains(string(s.Data), `"job_id":42`) {
		t.Fatalf("jobs snapshot = %s", s.Data)
	}

	// cmd from the browser
	b.send(map[string]any{"op": "cmd", "id": "b-1", "session": a.Session, "name": "drain", "args": map[string]any{}})
	cmd := a.recvType("cmd")
	a.send(map[string]any{"t": "cmd.result", "id": cmd["id"], "ok": true, "detail": "draining"})
	m = b.nextType("cmd.result")
	if !strings.Contains(string(m.Data), `"id":"b-1"`) || !strings.Contains(string(m.Data), `"ok":true`) {
		t.Fatalf("cmd.result = %s", m.Data)
	}
	b.send(map[string]any{"op": "cmd", "id": "b-2", "session": "0000000000000000", "name": "drain"})
	if m = b.nextType("cmd.result"); !strings.Contains(string(m.Data), "no_session") {
		t.Fatalf("no session: %s", m.Data)
	}

	_ = a.c.Close(websocket.StatusNormalClosure, "")
	m = b.next(func(m browserMsg) bool { return m.Type == "process" })
	_ = json.Unmarshal(m.Data, &ev)
	if ev.Event != "disconnected" || *ev.Session != a.Session {
		t.Fatalf("disconnected = %s", m.Data)
	}
}

func TestBrowserLogFiltering(t *testing.T) {
	f := newFixture(t)
	a, nodeID := f.node("logger")
	other, _ := f.node("other")
	// before anyone watches: kept for the snapshot
	a.send(map[string]any{"t": "logs", "records": []any{
		map[string]any{"ts": "2026-10-19T00:00:00Z", "level": "info", "event": "early-info"},
		map[string]any{"ts": "2026-10-19T00:00:01Z", "level": "error", "event": "early-error"},
	}})
	waitFor(t, "ring", func() bool {
		s := f.hub.Session(a.Session)
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.logs) == 2
	})

	b := f.browser("admin")
	b.send(map[string]any{"op": "subscribe", "topics": []string{"logs:" + nodeID + ":warning"}})
	snap := b.next(func(m browserMsg) bool { return m.Type == "snapshot" })
	if !strings.Contains(string(snap.Data), "early-error") || strings.Contains(string(snap.Data), "early-info") {
		t.Fatalf("log snapshot = %s", snap.Data)
	}

	other.send(map[string]any{"t": "logs", "records": []any{map[string]any{"level": "error", "event": "not-mine"}}})
	a.send(map[string]any{"t": "logs", "records": []any{
		map[string]any{"level": "debug", "event": "too-low"},
		map[string]any{"level": "warning", "event": "wanted"},
	}})
	m := b.nextType("log")
	var lm struct {
		Session string         `json:"session"`
		IP      string         `json:"ip"`
		Record  map[string]any `json:"record"`
	}
	_ = json.Unmarshal(m.Data, &lm)
	if lm.Session != a.Session || lm.Record["event"] != "wanted" {
		t.Fatalf("log = %s", m.Data)
	}
	b.none(200*time.Millisecond, func(m browserMsg) bool { return m.Type == "log" })

	// by session, by IP (IPv6-safe parsing), unsubscribe
	b.subscribe("logs:" + other.Session + ":debug")
	other.send(map[string]any{"t": "logs", "records": []any{map[string]any{"level": "debug", "event": "other-debug"}}})
	if m := b.nextType("log"); !strings.Contains(string(m.Data), "other-debug") {
		t.Fatalf("by session: %s", m.Data)
	}
	b.send(map[string]any{"op": "unsubscribe", "topics": []string{"logs:" + other.Session + ":debug", "logs:" + nodeID + ":warning"}})
	time.Sleep(50 * time.Millisecond)
	other.send(map[string]any{"t": "logs", "records": []any{map[string]any{"level": "error", "event": "after"}}})
	b.none(200*time.Millisecond, func(m browserMsg) bool { return m.Type == "log" })

	if f, ok := parseLogsTopic("logs:fe80::1:error"); !ok || f.sel != "fe80::1" || f.rank != levelRank("error") {
		t.Fatalf("ipv6 topic = %+v", f)
	}
	if f, ok := parseLogsTopic("logs:10.0.0.2"); !ok || f.sel != "10.0.0.2" || f.rank != levelRank("info") {
		t.Fatalf("no level = %+v", f)
	}
	if _, ok := parseLogsTopic("logs:"); ok {
		t.Fatal("empty selector accepted")
	}
}

func TestQueuesWorkersLibraryTopics(t *testing.T) {
	oldTick, oldGap, oldFile := queuesTick, queuesMinGap, queuesFileEvery
	queuesTick, queuesMinGap, queuesFileEvery = 100*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { queuesTick, queuesMinGap, queuesFileEvery = oldTick, oldGap, oldFile })

	var builds atomic.Int64
	reason := queues.WaitPoolFull
	f := newFixture(t, func(c *Config) {
		c.Queues = func(context.Context) (queues.Snapshot, error) {
			n := builds.Add(1)
			eta := 12.0
			return queues.Snapshot{At: time.Now().UTC(), Queues: []queues.QueueSummary{{
				Stage: "transcribe", Counts: queues.Counts{Pending: int(n), Ready: 2, Running: 1},
				RatePerMin: 1.5, EtaSec: &eta, WaitingReason: &reason,
			}}, Pools: []queues.Pool{{Pool: "gpu", Busy: 1, EffectiveSize: 2}}}, nil
		}
		c.Workers = func(context.Context) ([]queues.Worker, error) {
			return []queues.Worker{{ID: "w1", Host: "mac", Role: "home", Stages: []string{"probe"}}}, nil
		}
		c.Library = func(context.Context) (*LibrarySnapshot, error) {
			return &LibrarySnapshot{TS: time.Now().UTC(), Libraries: []LibraryStats{{LibraryID: "l1", Name: "Lib", Videos: 3}},
				Totals: LibraryStats{Videos: 3}}, nil
		}
	})
	waitFor(t, "first builds", func() bool {
		f.hub.mu.RLock()
		defer f.hub.mu.RUnlock()
		return f.hub.workers != nil && f.hub.library != nil
	})
	b := f.browser("viewer")
	b.subscribeSnap(TopicQueues)
	m := b.nextType("queues") // pushed within a second of subscribing
	var snap queues.Snapshot
	if err := json.Unmarshal(m.Data, &snap); err != nil || len(snap.Queues) != 1 || snap.Queues[0].Stage != "transcribe" {
		t.Fatalf("queues = %s", m.Data)
	}
	if w := b.subscribeSnap(TopicWorkers); !strings.Contains(string(w), `"worker_id":"w1"`) {
		t.Fatalf("workers snapshot = %s", w)
	}
	if l := b.subscribeSnap(TopicLibrary); !strings.Contains(string(l), `"videos":3`) {
		t.Fatalf("library snapshot = %s", l)
	}

	// a job event refreshes the queues at once (debounced)
	a, _ := f.node("worker")
	before := builds.Load()
	a.send(map[string]any{"t": "job", "event": "done", "job_id": 1})
	waitFor(t, "rebuild", func() bool { return builds.Load() > before })

	// the queue and library lines land in the home's folder
	day := time.Now().UTC().Format("2006-01-02")
	waitFor(t, "queue file", func() bool {
		f.hub.files.Flush()
		return countLines(t, f.dir+"/"+testHomeIP+"/queues/"+day+"/hub.jsonl") > 0
	})
	line := readLines(t, f.dir+"/"+testHomeIP+"/queues/"+day+"/hub.jsonl")[0]
	// the web app's charts read snapshot.queues[].counts.pending and
	// rate_per_min; the CLI reads the flat rows
	var ql struct {
		TS       string `json:"ts"`
		Snapshot struct {
			Queues []struct {
				Stage         string        `json:"stage"`
				Counts        queues.Counts `json:"counts"`
				RatePerMin    float64       `json:"rate_per_min"`
				WaitingReason string        `json:"waiting_reason"`
			} `json:"queues"`
			Pools []QueuePoolLite `json:"pools"`
		} `json:"snapshot"`
		Stages []QueueStageLine `json:"stages"`
		Pools  []QueuePoolLine  `json:"pools"`
	}
	if err := json.Unmarshal([]byte(line), &ql); err != nil || ql.TS == "" || len(ql.Snapshot.Queues) != 1 ||
		ql.Snapshot.Queues[0].Counts.Ready != 2 || ql.Snapshot.Queues[0].RatePerMin != 1.5 ||
		ql.Snapshot.Queues[0].WaitingReason != "pool_full" || ql.Snapshot.Pools[0].EffectiveSize != 2 ||
		len(ql.Stages) != 1 || ql.Stages[0].Stage != "transcribe" || ql.Stages[0].Ready != 2 ||
		ql.Pools[0].Pool != "gpu" || ql.Pools[0].Size != 2 {
		t.Fatalf("queue line = %s", line)
	}
	if countLines(t, f.dir+"/"+testHomeIP+"/library/"+day+"/hub.jsonl") != 1 {
		t.Fatal("library line missing")
	}
}

func TestSendQueueDropsOldest(t *testing.T) {
	q := newSendQueue(3)
	for i := 0; i < 5; i++ {
		q.push([]byte{byte('a' + i)})
	}
	got, dropped := q.take()
	if dropped != 2 || len(got) != 3 || string(got[0]) != "c" || string(got[2]) != "e" {
		t.Fatalf("got %q dropped %d", got, dropped)
	}
	if got, dropped := q.take(); len(got) != 0 || dropped != 0 {
		t.Fatal("not emptied")
	}

	// the latest queues snapshot replaces a waiting one and is never the
	// one dropped; metrics and logs go first
	q = newSendQueue(3)
	q.pushKind("queues", []byte("Q1"))
	q.push([]byte("m1"))
	q.pushKind("queues", []byte("Q2"))
	q.push([]byte("m2"))
	q.push([]byte("m3"))
	got, dropped = q.take()
	if dropped != 1 || len(got) != 3 || string(got[0]) != "Q2" || string(got[1]) != "m2" || string(got[2]) != "m3" {
		t.Fatalf("got %q dropped %d", got, dropped)
	}
}

func TestSlowBrowserGetsDroppedCount(t *testing.T) {
	h := New(Config{HomeAddr: testHomeIP, Logger: quiet()})
	b := &browser{admin: true, q: newSendQueue(browserQueueCap), topics: map[string]bool{}}
	for i := 0; i < browserQueueCap+88; i++ {
		b.send("metrics", "", map[string]int{"i": i})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		h.browserWrite(ctx, c, b)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Type string         `json:"type"`
		At   time.Time      `json:"at"`
		Data map[string]int `json:"data"`
	}
	if json.Unmarshal(data, &first) != nil || first.Type != "dropped" || first.Data["count"] != 88 || first.At.IsZero() {
		t.Fatalf("first = %s", data)
	}
	_, data, _ = c.Read(ctx)
	if !strings.Contains(string(data), `"i":88`) {
		t.Fatalf("oldest kept = %s", data)
	}
}
