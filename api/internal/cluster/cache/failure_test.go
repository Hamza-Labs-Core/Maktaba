package cache

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// fakeDB is a database that is down (every statement fails) or hung
// (every statement waits for its deadline).
type fakeDB struct {
	mode  atomic.Value // "fail" | "hang"
	calls atomic.Int64
}

var errDown = errors.New("connection refused")

func (f *fakeDB) do(ctx context.Context) error {
	f.calls.Add(1)
	if f.mode.Load() == "hang" {
		<-ctx.Done()
		return ctx.Err()
	}
	return errDown
}

func (f *fakeDB) Connect(context.Context) (driver.Conn, error) { return fakeConn{f}, nil }
func (f *fakeDB) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return nil, errDown }

type fakeConn struct{ f *fakeDB }

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errDown }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errDown }
func (c fakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	return nil, c.f.do(ctx)
}
func (c fakeConn) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	return nil, c.f.do(ctx)
}
func (c fakeConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return nil, c.f.do(ctx)
}

func fakeDBOf(mode string) (*sql.DB, *fakeDB) {
	f := &fakeDB{}
	f.mode.Store(mode)
	return sql.OpenDB(f), f
}

// lockedBuffer collects log output from several goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// With the database hung, the hub's calls still return at once: the
// queue stops at its size, the rest is dropped and counted, and the batch
// stuck in flight is dropped at its deadline.
func TestNeverBlocksOnAHungDatabase(t *testing.T) {
	db, f := fakeDBOf("hang")
	clk := newClock("2026-10-19T12:00:00Z")
	c := New(db, Options{Window: 24 * time.Hour, QueueSize: 100, Now: clk.Now, Logger: quietLogger()})
	c.timeout = 150 * time.Millisecond
	p := agent.Proc{WorkerID: "w", IP: "10.0.0.1", Host: "h"}
	recs := make([]agent.LogRecord, 10)
	for i := range recs {
		recs[i] = agent.LogRecord{TS: "2026-10-19T11:59:00Z", Level: "info", Event: "e"}
	}
	for i := 0; i < 10; i++ {
		c.Logs(p, recs)
	}
	done := make(chan struct{})
	go func() { defer close(done); c.flushLogs(bg) }()
	for f.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	began := time.Now()
	for i := 0; i < 2000; i++ {
		c.Logs(p, recs)
		at := clk.Add(2 * time.Second)
		c.Metrics(p, frame(at, 1), map[string]float64{"cpu.pct": 1}, i%500 == 0)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("4000 calls took %s with the database hung", took)
	}
	st := c.Snapshot()
	if st.Logs.Queued != 100 || st.Logs.Dropped != 2000*10 || st.Metrics.Queued != 100 || st.Health.Queued != 1 {
		t.Fatalf("while hung: logs %+v metrics %+v health %+v", st.Logs, st.Metrics, st.Health)
	}
	<-done
	if st := c.Snapshot(); st.Logs.Dropped != 2000*10+100 || st.Logs.Queued != 0 || st.LastError == "" {
		t.Fatalf("after the deadline: %+v %q", st.Logs, st.LastError)
	}
}

// With the database down, every batch is dropped and counted, nothing is
// covered, and the error is logged at most once a minute.
func TestDatabaseDownDropsAndLogsOnceAMinute(t *testing.T) {
	db, _ := fakeDBOf("fail")
	clk := newClock("2026-10-19T12:00:00Z")
	var out lockedBuffer
	c := New(db, Options{Window: 24 * time.Hour, Now: clk.Now, Logger: slog.New(slog.NewTextHandler(&out, nil))})
	p := agent.Proc{WorkerID: "w", IP: "10.0.0.1", Host: "h"}
	for i := 0; i < 5; i++ {
		at := clk.Add(10 * time.Second)
		c.Logs(p, []agent.LogRecord{{TS: at.Format(time.RFC3339), Level: "info", Event: "e"}})
		c.Metrics(p, frame(at, 1), map[string]float64{"cpu.pct": 1, "nas.timeouts": 0}, false)
		c.flushLogs(bg)
		c.flushMetrics(bg)
	}
	if n := strings.Count(out.String(), "cluster_cache_failed"); n != 1 {
		t.Fatalf("logged %d times in a minute:\n%s", n, out.String())
	}
	clk.Add(time.Minute)
	c.flushLogs(bg)
	c.Logs(p, []agent.LogRecord{{TS: clk.Now().Format(time.RFC3339), Level: "info", Event: "e"}})
	c.flushLogs(bg)
	if n := strings.Count(out.String(), "cluster_cache_failed"); n != 2 || !strings.Contains(out.String(), "not_logged=") {
		t.Fatalf("after a minute: %d\n%s", n, out.String())
	}
	st := c.Snapshot()
	if st.Logs.Dropped != 6 || st.Metrics.Dropped != 5 || st.Health.Dropped != 10 || st.Logs.Written != 0 {
		t.Fatalf("stats = %+v", st)
	}
	dir, _ := clusterTree(t)
	if err := c.Backfill(bg, dir); err == nil {
		t.Fatal("a pass with the database down must fail")
	}
	if c.Covers(KindLogs, clk.Now(), clk.Now()) {
		t.Fatal("covered with the database down")
	}
	c.prune(bg) // logs, never panics
}

// Run writes on its own cadence, passes over the cluster directory it is
// given, and flushes what is queued when it stops.
func TestRun(t *testing.T) {
	db := sqliteDB(t)
	dir, _ := clusterTree(t)
	var mu sync.Mutex
	clusterDir := ""
	c := New(db, Options{Window: 24 * time.Hour, Driver: "sqlite", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		SweepEvery: 20 * time.Millisecond, ClusterDir: func() string { mu.Lock(); defer mu.Unlock(); return clusterDir }})
	ctx, cancel := context.WithCancel(bg)
	stopped := make(chan struct{})
	go func() { defer close(stopped); c.Run(ctx) }()
	now := time.Now().UTC()
	if c.Covers(KindLogs, now.Add(-time.Hour), now) {
		t.Fatal("covered with no directory")
	}
	mu.Lock()
	clusterDir = dir
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !c.Covers(KindLogs, now.Add(-time.Hour), now) {
		if time.Now().After(deadline) {
			t.Fatalf("no pass: %+v", c.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	p := agent.Proc{WorkerID: "w-run", IP: "10.0.0.9", Host: "h"}
	c.Logs(p, []agent.LogRecord{{TS: now.Format(time.RFC3339Nano), Level: "info", Event: "at stop"}})
	c.Metrics(p, nil, map[string]float64{"cpu.pct": 3}, false)
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if count(t, db, `SELECT count(*) FROM cluster_log_cache WHERE event = 'at stop'`) != 1 ||
		count(t, db, `SELECT count(*) FROM health_samples WHERE host = 'h' AND metric = 'cpu.pct'`) != 1 {
		t.Fatal("not flushed at stop")
	}
	if c.clusterDir() != dir {
		t.Fatal("cluster directory")
	}
	// Backfill remembers its directory for the passes when none is given
	b := New(db, Options{Window: time.Hour, Driver: "sqlite", Logger: quietLogger()})
	must(t, b.Backfill(bg, filepath.Join(dir, "nowhere")))
	if b.clusterDir() != filepath.Join(dir, "nowhere") {
		t.Fatal("Backfill's directory not kept")
	}
}
