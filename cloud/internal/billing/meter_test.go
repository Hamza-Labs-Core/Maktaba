package billing

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// fakeDB records written batches and fails while down.
type fakeDB struct {
	mu      sync.Mutex
	down    bool
	written map[string]int64 // server → bytes
	buckets map[bucketKey]bool
	writes  int
}

func (f *fakeDB) write(_ context.Context, b map[bucketKey]counter) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("db down")
	}
	f.writes++
	for k, c := range b {
		f.written[k.server] += c.BytesIn + c.BytesOut
		f.buckets[k] = true
	}
	return nil
}

func newTestMeter(clock *time.Time) (*Meter, *fakeDB) {
	f := &fakeDB{written: map[string]int64{}, buckets: map[bucketKey]bool{}}
	// a database nobody listens on: every read fails at once
	db, _ := sql.Open("postgres", "postgres://u@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	m := NewMeter(db)
	m.writer = f.write
	m.now = func() time.Time { return *clock }
	return m, f
}

// T03 (story 50.05): flushes failing for 60 s keep every byte; the first
// flush after writes them all, and Allow counts the buffer meanwhile.
func TestMeterKeepsBytesThroughAFailedFlush(t *testing.T) {
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	m, f := newTestMeter(&clock)
	ctx := context.Background()
	a, b := stores.Server{ID: "a", Plan: PlanFree}, stores.Server{ID: "b"}
	f.down = true
	var want int64
	for i := range 12 { // 60 s of 5 s retries
		m.Record(ctx, a, 100, 1_000)
		m.Record(ctx, b, 10, 10)
		want += 1_100
		if err := m.flush(ctx); err == nil {
			t.Fatal("a flush succeeded while the database is down")
		}
		clock = clock.Add(5 * time.Second)
		_ = i
	}
	if in, out := m.Buffered(); in+out != want+12*20 {
		t.Fatalf("buffered %d, want %d", in+out, want+12*20)
	}
	if got := m.unflushedLocked("a", MonthStart(clock)); got != want {
		t.Errorf("Allow's view of a: %d, want %d", got, want)
	}
	f.down = false
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	if f.written["a"] != want || f.written["b"] != 240 {
		t.Errorf("written a=%d b=%d, want %d and 240", f.written["a"], f.written["b"], want)
	}
	if in, out := m.Buffered(); in+out != 0 || m.BufferedEntries() != 0 {
		t.Errorf("left in the buffer: %d bytes, %d entries", in+out, m.BufferedEntries())
	}
	if len(f.buckets) == 0 {
		t.Error("no bucket written")
	}
}

// T04: a buffer bound of 100 entries and 150 buckets: buckets merge per
// server, and each server's total stays exact.
func TestMeterCoarsensPastItsBound(t *testing.T) {
	clock := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	m, f := newTestMeter(&clock)
	m.MaxBuffered = 100
	ctx := context.Background()
	f.down = true
	totals := map[string]int64{}
	for i := range 150 {
		id := []string{"a", "b", "c"}[i%3]
		m.Record(ctx, stores.Server{ID: id}, int64(i), 7)
		totals[id] += int64(i) + 7
		_ = m.flush(ctx)
		clock = clock.Add(5 * time.Minute)
	}
	if n := m.BufferedEntries(); n > 100 {
		t.Errorf("%d entries buffered, bound 100", n)
	}
	f.down = false
	if err := m.flush(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range totals {
		if f.written[id] != want {
			t.Errorf("server %s: %d written, want %d", id, f.written[id], want)
		}
	}
}

// Allow keeps working while the database is away: the month total read
// before is used (here none: the home is allowed), and a home over its cap
// by its buffered bytes is refused.
func TestMeterAllowDuringAnOutage(t *testing.T) {
	clock := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	m, f := newTestMeter(&clock)
	ctx := context.Background()
	f.down = true
	m.months["a"] = monthTotal{month: MonthStart(clock), bytes: Tiers[PlanFree].BandwidthBytesPerMo, at: clock.Add(-time.Hour)}
	sv := stores.Server{ID: "a", Plan: PlanFree}
	// the cached total is stale and the database errors: the cached one is used
	if err := m.Allow(ctx, sv); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	m.Record(ctx, sv, 0, FreeOverageGrace+1)
	if err := m.Allow(ctx, sv); !errors.Is(err, ErrOverLimit) {
		t.Errorf("past the cap and its grace: %v", err)
	}
}

// Allow waits at most the connect timeout (2 s, db.Open) on a database that
// vanished, and for the next 5 s not at all (the rehearsal's db-blip).
func TestMeterAllowDoesNotWaitOnAVanishedDatabase(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	// accepts and never answers: a connect that hangs
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }()
		}
	}()
	pool, err := db.Open("postgres://u@"+ln.Addr().String()+"/x?sslmode=disable", 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMeter(pool.DB)
	ctx := context.Background()
	sv := stores.Server{ID: "a", Plan: PlanFree}
	start := time.Now()
	if err := m.Allow(ctx, sv); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("Allow waited %s", took)
	}
	start = time.Now()
	_ = m.Allow(ctx, sv)
	if took := time.Since(start); took > 50*time.Millisecond {
		t.Errorf("the next Allow waited %s", took)
	}
}
