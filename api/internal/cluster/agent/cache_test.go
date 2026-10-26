package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// fakeCache records what the hub hands it and answers reads.
type fakeCache struct {
	mu        sync.Mutex
	metrics   []fakeMetrics
	logs      []LogRecord
	procs     []Proc
	covers    bool
	coversFn  func(kind string, from, to time.Time) bool
	queries   []CacheQuery
	backfill  string
	ran       bool
	logRecs   []CacheRecord
	metricRec []CacheRecord
}

type fakeMetrics struct {
	frame  json.RawMessage
	health map[string]float64
	final  bool
}

func (c *fakeCache) Metrics(p Proc, frame json.RawMessage, health map[string]float64, final bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.procs = append(c.procs, p)
	c.metrics = append(c.metrics, fakeMetrics{frame, health, final})
}

func (c *fakeCache) Logs(p Proc, recs []LogRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.procs = append(c.procs, p)
	c.logs = append(c.logs, recs...)
}

func (c *fakeCache) Covers(kind string, from, to time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.coversFn != nil {
		return c.coversFn(kind, from, to)
	}
	return c.covers
}

func (c *fakeCache) ReadMetrics(_ context.Context, q CacheQuery) ([]CacheRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	return c.metricRec, nil
}

func (c *fakeCache) ReadLogs(_ context.Context, q CacheQuery) ([]CacheRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	return c.logRecs, nil
}

func (c *fakeCache) Run(ctx context.Context) {
	c.mu.Lock()
	c.ran = true
	c.mu.Unlock()
	<-ctx.Done()
}

func (c *fakeCache) Backfill(_ context.Context, dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.backfill = dir
	return nil
}

func (c *fakeCache) Stats() any { return map[string]any{"rows": 7} }

func TestCacheFedFromFrames(t *testing.T) {
	fc := &fakeCache{}
	f := newFixture(t, func(c *Config) { c.Cache = fc })
	waitFor(t, "cache started", func() bool { fc.mu.Lock(); defer fc.mu.Unlock(); return fc.ran && fc.backfill == f.dir })

	a, nodeID := f.node("cached")
	a.send(map[string]any{"t": "metrics", "ts": "2026-10-19T08:00:02Z", "final": true,
		"health": map[string]any{"nas.open_ms_p50": 4.5, "cpu.pct": 41, "stt.audio_sec:mlx-0": 1830.5}})
	a.send(map[string]any{"t": "logs", "records": []any{
		map[string]any{"ts": "2026-10-19T08:00:01Z", "level": "warning", "event": "slow", "logger": "x", "fields": map[string]any{"f": 1}},
	}})
	waitFor(t, "cache fed", func() bool { fc.mu.Lock(); defer fc.mu.Unlock(); return len(fc.metrics) == 1 && len(fc.logs) == 1 })
	fc.mu.Lock()
	m := fc.metrics[0]
	p := fc.procs[0]
	lr := fc.logs[0]
	fc.mu.Unlock()
	if !m.final || m.health["cpu.pct"] != 41 || m.health["stt.audio_sec:mlx-0"] != 1830.5 || strings.Contains(string(m.frame), `"t"`) {
		t.Fatalf("metrics = %+v %s", m, m.frame)
	}
	if p.SessionID != a.Session || p.NodeID != nodeID || p.NodeName != "cached" || p.Host != "test-host" || p.IP != "127.0.0.1" || p.WorkerID == "" {
		t.Fatalf("proc = %+v", p)
	}
	if lr.Level != "warning" || lr.Event != "slow" || lr.Logger != "x" || string(lr.Fields) != `{"f":1}` || lr.TS != "2026-10-19T08:00:01Z" {
		t.Fatalf("log = %+v", lr)
	}

	// the processes snapshot shows the cache's stats
	var snap map[string]any
	decode(t, f.do("GET", "/api/cluster/processes", "", "admin"), &snap)
	if c, _ := snap["cache"].(map[string]any); c["rows"] != float64(7) {
		t.Fatalf("snapshot = %v", snap)
	}
}

func TestHistoryFromCacheWhenItCovers(t *testing.T) {
	fc := &fakeCache{}
	f := newFixture(t, func(c *Config) { c.Cache = fc })
	seedFiles(t, f.dir)
	ts := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	fc.logRecs = []CacheRecord{
		{WorkerID: "w9", TS: ts("2026-10-19T10:00:05Z"), Line: json.RawMessage(`{"ts":"2026-10-19T10:00:05Z","level":"error","event":"from-cache-2"}`)},
		{WorkerID: "w9", TS: ts("2026-10-19T10:00:04Z"), Line: json.RawMessage(`{"ts":"2026-10-19T10:00:04Z","level":"error","event":"from-cache-1"}`)},
	}
	fc.metricRec = []CacheRecord{{WorkerID: "w9", TS: ts("2026-10-19T10:00:00Z"), Line: json.RawMessage(`{"ts":"2026-10-19T10:00:00Z","system":{"cpu_pct":3}}`)}}

	// not covered: the files answer
	resp := f.do("GET", "/api/cluster/files/192.168.50.31/logs?date="+day, "", "admin")
	if resp.Header.Get("X-Maktaba-Source") != "files" {
		t.Fatalf("source = %q", resp.Header.Get("X-Maktaba-Source"))
	}
	fc.mu.Lock()
	fc.covers = true
	fc.mu.Unlock()
	resp = f.do("GET", "/api/cluster/files/192.168.50.31/logs?date=2026-10-01&level=warn&q=boom&worker=w9&limit=1", "", "admin")
	var rs struct {
		Records    []map[string]any `json:"records"`
		NextBefore *string          `json:"next_before"`
	}
	decode(t, resp, &rs)
	if resp.Header.Get("X-Maktaba-Source") != "cache" || len(rs.Records) != 1 || rs.Records[0]["event"] != "from-cache-2" ||
		rs.Records[0]["worker_id"] != "w9" || rs.NextBefore == nil {
		t.Fatalf("cache logs = %+v", rs)
	}
	fc.mu.Lock()
	q := fc.queries[0]
	fc.mu.Unlock()
	if q.IP != "192.168.50.31" || q.WorkerID != "w9" || q.Level != "warning" || q.Q != "boom" || q.Limit != 2 ||
		!q.From.Equal(ts("2026-10-01T00:00:00Z")) || !q.To.Equal(ts("2026-10-02T00:00:00Z")) {
		t.Fatalf("query = %+v", q)
	}

	resp = f.do("GET", "/api/cluster/files/192.168.50.31/metrics?date=2026-10-01&from=2026-10-01T09:00:00Z", "", "admin")
	var ms struct {
		Samples []struct {
			WorkerID string         `json:"worker_id"`
			Metrics  map[string]any `json:"metrics"`
		} `json:"samples"`
	}
	decode(t, resp, &ms)
	if resp.Header.Get("X-Maktaba-Source") != "cache" || len(ms.Samples) != 1 || ms.Samples[0].WorkerID != "w9" {
		t.Fatalf("cache metrics = %+v", ms)
	}
	// jobs are never in the cache
	if r := f.do("GET", "/api/cluster/files/192.168.50.31/jobs?date="+day, "", "admin"); r.Header.Get("X-Maktaba-Source") != "files" {
		t.Fatal("jobs from the cache")
	}
}

func TestWorkersCarryTheirSession(t *testing.T) {
	f := newFixture(t, func(c *Config) {
		c.Workers = func(context.Context) ([]queues.Worker, error) {
			return []queues.Worker{{ID: "w-live", Host: "a"}, {ID: "w-off", Host: "b"}}, nil
		}
	})
	id, priv := f.approvedNode("n")
	a, err := f.dialAgent(helloOpts{kind: "node", nodeID: id, nodePriv: priv, workerID: "w-live"})
	if err != nil {
		t.Fatal(err)
	}
	b := f.browser("admin")
	var data json.RawMessage
	waitFor(t, "workers with session", func() bool {
		data = b.subscribeSnap(TopicWorkers)
		return strings.Contains(string(data), a.Session)
	})
	var ws struct {
		Workers []map[string]any `json:"workers"`
	}
	_ = json.Unmarshal(data, &ws)
	if len(ws.Workers) != 2 || ws.Workers[0]["session"] != a.Session || ws.Workers[1]["session"] != nil {
		t.Fatalf("workers = %s", data)
	}
}

// Today's history (the default view) is asked of the cache only up to now:
// a cache that holds [now-24h, now] answers it.
func TestCacheAnswersTodayUpToNow(t *testing.T) {
	fc := &fakeCache{}
	fc.coversFn = func(_ string, from, to time.Time) bool {
		now := time.Now().UTC()
		return !from.Before(now.Add(-24*time.Hour)) && !to.After(now)
	}
	f := newFixture(t, func(c *Config) { c.Cache = fc })
	today := time.Now().UTC().Format("2006-01-02")
	for _, kind := range []string{"logs", "metrics"} {
		resp := f.do("GET", "/api/cluster/files/192.168.50.31/"+kind+"?date="+today, "", "admin")
		if resp.StatusCode != 200 || resp.Header.Get("X-Maktaba-Source") != "cache" {
			t.Fatalf("%s today: %d from %q", kind, resp.StatusCode, resp.Header.Get("X-Maktaba-Source"))
		}
	}
	fc.mu.Lock()
	q := fc.queries[0]
	fc.mu.Unlock()
	if q.To.After(time.Now().UTC()) || q.From.Format("2006-01-02") != today {
		t.Fatalf("range = %v .. %v", q.From, q.To)
	}
	// a day the cache does not hold goes to the files
	if r := f.do("GET", "/api/cluster/files/192.168.50.31/logs?date=2020-01-01", "", "admin"); r.Header.Get("X-Maktaba-Source") != "files" {
		t.Fatal("an old day was asked of the cache")
	}
}
