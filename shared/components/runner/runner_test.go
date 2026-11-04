package runner

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// fakeClock fires its timers only when the test moves it.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, waiter{at: c.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock by d and fires every timer due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
}

func (c *fakeClock) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// eventually waits (real time) for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// testLogger keeps the runner's log in the test's output (shown when a test fails).
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	var buf bytes.Buffer
	var mu sync.Mutex
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			t.Log("runner log:\n" + buf.String())
			mu.Unlock()
		}
	})
	return slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func newRunner(t *testing.T, clk *fakeClock, opt ...func(*Options)) *Runner {
	t.Helper()
	o := Options{Host: "api", Logger: testLogger(t), Clock: clk}
	for _, f := range opt {
		f(&o)
	}
	r := New(o)
	t.Cleanup(func() { r.shutdown() })
	return r
}

func enabled(id string) store.Desired {
	return store.Desired{ID: id, Want: components.WantEnabled, Generation: 1}
}
func installed(id string) store.Desired {
	return store.Desired{ID: id, Want: components.WantInstalled, Generation: 2}
}

func statusOf(r *Runner, id string) store.Status {
	for _, s := range r.Statuses() {
		if s.ID == id {
			return s
		}
	}
	return store.Status{}
}

// polite is a loop that runs until its context ends.
type polite struct {
	starts, stops atomic.Int32
}

func (p *polite) fn(ctx context.Context) error {
	p.starts.Add(1)
	<-ctx.Done()
	p.stops.Add(1)
	return nil
}

// Each stop and each start of a loop is logged once, with its component and why: a
// start from off (enabled), a stop with the gate's reason (off, held).
func TestStopAndStartAreLogged(t *testing.T) {
	clk := newClock()
	var buf bytes.Buffer
	var mu sync.Mutex
	logged := func(event, reason string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.Contains(line, "event="+event) && strings.Contains(line, "component=livetv") &&
				strings.Contains(line, "reason="+reason+" ") {
				n++
			}
		}
		return n
	}
	r := newRunner(t, clk, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil))
	})
	var p polite
	r.Declare("tvchange_controller", p.fn)
	eventually(t, "the loop to start", func() bool { return p.starts.Load() == 1 })
	if n := logged("components_loop_started", "enabled"); n != 1 {
		t.Fatalf("%d start lines", n)
	}
	r.Apply(map[string]store.Desired{"livetv": installed("livetv")})
	eventually(t, "the loop to return", func() bool { return p.stops.Load() == 1 })
	r.Apply(map[string]store.Desired{"livetv": installed("livetv")}) // the same again: nothing new
	if n := logged("components_loop_stopped", components.PhaseOff); n != 1 {
		t.Fatalf("%d stop lines (off)", n)
	}
	r.Apply(map[string]store.Desired{"livetv": enabled("livetv")})
	eventually(t, "the loop to start again", func() bool { return p.starts.Load() == 2 })
	held := enabled("livetv")
	held.Hold = &store.Hold{By: "maya"}
	r.Apply(map[string]store.Desired{"livetv": held})
	eventually(t, "the loop to return again", func() bool { return p.stops.Load() == 2 })
	if logged("components_loop_started", "enabled") != 2 || logged("components_loop_stopped", "held") != 1 {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("log:\n%s", buf.String())
	}
}

// An admin's restart (story 51.08) cancels the running loop and starts it again once it
// returns; it does nothing for a component that is off or has no loop here.
func TestRestart(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("tvchange_controller", p.fn)
	eventually(t, "the loop to start", func() bool { return p.starts.Load() == 1 })
	if n := r.Restart("livetv"); n != 1 {
		t.Fatalf("restarted %d loops", n)
	}
	eventually(t, "the loop to start again", func() bool { return p.stops.Load() == 1 && p.starts.Load() == 2 })
	eventually(t, "Running", func() bool { return statusOf(r, "livetv").Phase == components.PhaseRunning })
	if n := r.Restart("youtube"); n != 0 {
		t.Fatalf("a component with no loop here: %d", n)
	}
	r.Apply(map[string]store.Desired{"livetv": installed("livetv")})
	eventually(t, "the loop to return", func() bool { return p.stops.Load() == 2 })
	if n := r.Restart("livetv"); n != 0 || p.starts.Load() != 2 {
		t.Fatalf("a component that is off restarted %d loops", n)
	}
	hd, _ := components.Default().Get("livetv.hdhomerun")
	if h := Hosts(hd); len(h) != 1 || h[0] != "streaming" || r.Host() != "api" {
		t.Fatalf("hosts %v, runner %q", h, r.Host())
	}
}

// T01: disabling cancels the loop's context; it returns; the component is Off.
func TestDisableStopsTheLoop(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("tvchange_controller", p.fn)
	eventually(t, "the loop to start", func() bool { return p.starts.Load() == 1 })
	if !r.On("livetv") || statusOf(r, "livetv").Phase != components.PhaseRunning {
		t.Fatalf("livetv with no desired row: on and running, got %+v", statusOf(r, "livetv"))
	}

	r.Apply(map[string]store.Desired{"livetv": installed("livetv")})
	eventually(t, "the loop to return", func() bool { return p.stops.Load() == 1 })
	eventually(t, "Off", func() bool { return statusOf(r, "livetv").Phase == components.PhaseOff })
	if r.On("livetv") {
		t.Fatal("livetv installed: On says true")
	}
	s := statusOf(r, "livetv")
	if s.ObservedGeneration != 2 {
		t.Fatalf("observed generation %d, want 2", s.ObservedGeneration)
	}

	// enabled again: the same loop runs again (N2)
	r.Apply(map[string]store.Desired{"livetv": enabled("livetv")})
	eventually(t, "the loop to start again", func() bool { return p.starts.Load() == 2 })
	eventually(t, "Running", func() bool { return statusOf(r, "livetv").Phase == components.PhaseRunning })
}

// T02: a loop that ignores its context is Failed stop_timeout after its grace, and
// enabling again does not start a second copy before it returns.
func TestStuckLoopIsNeverDoubled(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	release := make(chan struct{})
	var running, starts atomic.Int32
	r.Declare("watch_reaper", func(ctx context.Context) error {
		starts.Add(1)
		running.Add(1)
		defer running.Add(-1)
		<-release // ignores ctx
		return nil
	})
	eventually(t, "the loop to start", func() bool { return running.Load() == 1 })

	r.Apply(map[string]store.Desired{"analytics": installed("analytics")})
	eventually(t, "Draining", func() bool { return statusOf(r, "analytics").Phase == components.PhaseDraining })
	eventually(t, "the grace timer", func() bool { return clk.pending() >= 1 })
	clk.Advance(5 * time.Second)
	eventually(t, "stop_timeout", func() bool {
		s := statusOf(r, "analytics")
		return s.Phase == components.PhaseFailed && s.Reason() == "stop_timeout"
	})

	r.Apply(map[string]store.Desired{"analytics": enabled("analytics")})
	time.Sleep(20 * time.Millisecond)
	if n := starts.Load(); n != 1 {
		t.Fatalf("a second copy started while the first still runs (%d starts)", n)
	}
	close(release)
	eventually(t, "a new copy once the first returned", func() bool { return starts.Load() == 2 })
}

// T03: a crashing loop restarts after 1, 2, 4, 8 s; the fifth crash in 10 minutes is
// crash_loop, tried again after 5 minutes.
func TestCrashBackoffAndCrashLoop(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var runs atomic.Int32
	r.Declare("usage_counter", func(ctx context.Context) error {
		n := runs.Add(1)
		if n%2 == 0 {
			panic("boom")
		}
		return errors.New("broken")
	})
	for i, d := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		want := int32(i + 1)
		eventually(t, "a crash", func() bool { return runs.Load() == want && clk.pending() == 1 })
		if s := statusOf(r, "analytics"); s.Phase != components.PhaseStarting {
			t.Fatalf("crash %d: phase %s, want starting", i+1, s.Phase)
		}
		clk.Advance(d - time.Millisecond)
		time.Sleep(5 * time.Millisecond)
		if runs.Load() != want {
			t.Fatalf("crash %d: restarted before %s", i+1, d)
		}
		clk.Advance(time.Millisecond)
	}
	eventually(t, "the fifth crash", func() bool { return runs.Load() == 5 })
	eventually(t, "crash_loop", func() bool {
		s := statusOf(r, "analytics")
		return s.Phase == components.PhaseFailed && s.Reason() == "crash_loop"
	})
	clk.Advance(5*time.Minute - time.Second)
	time.Sleep(5 * time.Millisecond)
	if runs.Load() != 5 {
		t.Fatal("retried before 5 minutes")
	}
	clk.Advance(time.Second)
	eventually(t, "the retry after 5 minutes", func() bool { return runs.Load() == 6 })
	if got := statusOf(r, "analytics").Detail["loops"].(map[string]any)["usage_counter"].(map[string]any)["restarts"]; got != 6 {
		t.Fatalf("restarts %v, want 6", got)
	}
}

// T04: a loop that returns nil while wanted is a one-shot: Off, exited, not restarted.
func TestOneShotIsNotRestarted(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var runs atomic.Int32
	r.Declare("telemetry_sender", func(ctx context.Context) error { runs.Add(1); return nil })
	eventually(t, "Off, exited", func() bool {
		s := statusOf(r, "telemetry")
		return s.Phase == components.PhaseOff && s.Detail["exited"] == true
	})
	clk.Advance(time.Hour)
	time.Sleep(10 * time.Millisecond)
	if runs.Load() != 1 {
		t.Fatalf("a one-shot ran %d times", runs.Load())
	}
	// turned off and on: it runs once more
	r.Apply(map[string]store.Desired{"telemetry": installed("telemetry")})
	r.Apply(map[string]store.Desired{"telemetry": enabled("telemetry")})
	eventually(t, "a run after off and on", func() bool { return runs.Load() == 2 })
}

// T05: a loop that is not in the catalog panics with its host:name.
func TestDeclareUnknownPanics(t *testing.T) {
	r := newRunner(t, newClock())
	defer func() {
		v := recover()
		if v == nil || !strings.Contains(v.(string), "api:nope") {
			t.Fatalf("panic %v, want one naming api:nope", v)
		}
	}()
	r.Declare("nope", func(context.Context) error { return nil })
}

// T06: with the kill switch the loop keeps running and the status says reconcile_off.
func TestReconcileOffRunsEverything(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk, func(o *Options) { o.NoReconcile = true })
	var p polite
	r.Declare("watch_reaper", p.fn)
	r.Apply(map[string]store.Desired{"analytics": installed("analytics")})
	eventually(t, "the loop to start", func() bool { return p.starts.Load() == 1 })
	time.Sleep(10 * time.Millisecond)
	if p.stops.Load() != 0 || !r.On("analytics") {
		t.Fatal("reconcile off: the loop was stopped")
	}
	s := statusOf(r, "analytics")
	if s.Phase != components.PhaseRunning || s.Reason() != "reconcile_off" {
		t.Fatalf("status %s / %q, want running / reconcile_off", s.Phase, s.Reason())
	}
}

// Held and waiting for a requirement are off too, with their reasons.
func TestHoldAndDependency(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk, func(o *Options) { o.Host = "streaming" })
	var p polite
	r.Declare("hdhr_ssdp", p.fn)
	eventually(t, "start", func() bool { return p.starts.Load() == 1 })

	r.Apply(map[string]store.Desired{"livetv": installed("livetv"), "livetv.hdhomerun": enabled("livetv.hdhomerun")})
	eventually(t, "Waiting", func() bool { return statusOf(r, "livetv.hdhomerun").Phase == components.PhaseWaiting })
	if got := statusOf(r, "livetv.hdhomerun").Reason(); got != "dependency_off" {
		t.Fatalf("reason %q, want dependency_off", got)
	}
	held := enabled("livetv.hdhomerun")
	held.Hold = &store.Hold{By: "admin", At: clk.Now()}
	r.Apply(map[string]store.Desired{"livetv": enabled("livetv"), "livetv.hdhomerun": held})
	eventually(t, "Stopped", func() bool { return statusOf(r, "livetv.hdhomerun").Phase == components.PhaseStopped })
	if got := statusOf(r, "livetv.hdhomerun").Reason(); got != "held" {
		t.Fatalf("reason %q, want held", got)
	}
	if p.starts.Load() != 1 {
		t.Fatal("started while held")
	}
	// core components are never stopped
	if !r.On("core.streaming") {
		t.Fatal("core.streaming off")
	}
}

// countingStore counts the queries a runner makes.
type countingStore struct {
	mu                         sync.Mutex
	revision, desired, reports int
	rev                        int64
	want                       map[string]store.Desired
	last                       []store.Status
}

func (s *countingStore) Revision(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	return s.rev, nil
}

func (s *countingStore) Desired(context.Context, string) (map[string]store.Desired, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired++
	return s.want, s.rev, nil
}

func (s *countingStore) ReportStatus(_ context.Context, machine, part string, rows []store.Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports++
	s.last = rows
	return nil
}

func (s *countingStore) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision, s.desired, s.reports
}

// T07 (N12): idle for 5 minutes, a runner asks the revision once per 30 s and writes
// nothing.
func TestIdleCost(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("watch_reaper", p.fn)
	st := &countingStore{rev: 7, want: map[string]store.Desired{"analytics": enabled("analytics")}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, Source{Store: st}); close(done) }()
	// the idle count starts once the load is reported: a first report can go out before it
	// is applied (the catalog's default, generation 0), and the one after it is a real
	// change (an integration run counted it as an idle report)
	eventually(t, "the load reported", func() bool {
		_, d, _ := st.counts()
		st.mu.Lock()
		defer st.mu.Unlock()
		return d == 1 && len(st.last) > 0 && st.last[0].ID == "analytics" && st.last[0].ObservedGeneration == 1
	})
	eventually(t, "the poll timer", func() bool { return clk.pending() == 1 })
	rev0, _, rep0 := st.counts()
	for i := 0; i < 10; i++ {
		clk.Advance(30 * time.Second)
		want := rev0 + i + 1
		eventually(t, "a poll", func() bool { rv, _, _ := st.counts(); return rv == want && clk.pending() == 1 })
	}
	rev, des, rep := st.counts()
	if rev-rev0 > 10 || des != 1 || rep != rep0 {
		t.Fatalf("5 idle minutes: %d revision queries, %d loads, %d reports (from %d); want <= 10, 1, unchanged",
			rev-rev0, des, rep, rep0)
	}
	if st.last[0].ID != "analytics" || st.last[0].Phase != components.PhaseRunning {
		t.Fatalf("reported %+v", st.last)
	}
	cancel()
	<-done
	if p.stops.Load() != 1 {
		t.Fatal("Run returned without stopping its loop")
	}
}

// Run applies a change the watcher sees, and reports it.
func TestRunFollowsTheStore(t *testing.T) {
	clk := newClock()
	r := newRunner(t, clk)
	var p polite
	r.Declare("tvchange_controller", p.fn)
	st := &countingStore{rev: 1, want: map[string]store.Desired{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx, Source{Store: st})
	eventually(t, "the poll timer", func() bool { return clk.pending() == 1 })
	st.mu.Lock()
	st.rev, st.want = 2, map[string]store.Desired{"livetv": installed("livetv")}
	st.mu.Unlock()
	clk.Advance(30 * time.Second)
	eventually(t, "the loop to stop", func() bool { return p.stops.Load() == 1 })
	eventually(t, "Off reported", func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.last) == 1 && st.last[0].Phase == components.PhaseOff
	})
}

// A nil runner runs the loop as a plain goroutine and says every component is on.
func TestNilRunner(t *testing.T) {
	var r *Runner
	done := make(chan struct{})
	r.Declare("anything", func(context.Context) error { close(done); return nil })
	<-done
	if !r.On("livetv") {
		t.Fatal("nil runner: On false")
	}
}

// Bound stops the loop when its old context ends too.
func TestBound(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = Bound(parent, func(ctx context.Context) error { <-ctx.Done(); return nil })(context.Background())
		close(done)
	}()
	cancel()
	<-done
}

// A desired row whose id this release's catalog does not know (story 51.02: a newer
// release wrote it before a rollback) is ignored and logged component_unknown once, not
// on every pass.
func TestUnknownComponentLoggedOnce(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(lockedWriter{&mu, &buf}, nil))
	r := newRunner(t, newClock(), func(o *Options) { o.Logger = logger })
	desired := map[string]store.Desired{"livetv": enabled("livetv"), "from.the.future": enabled("from.the.future")}
	for i := 0; i < 3; i++ {
		r.Apply(desired)
	}
	r.Apply(map[string]store.Desired{"from.the.future": installed("from.the.future"), "also.new": enabled("also.new")})
	mu.Lock()
	defer mu.Unlock()
	out := buf.String()
	if n := strings.Count(out, "component=from.the.future event=component_unknown"); n != 1 {
		t.Fatalf("from.the.future logged %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "also.new") || strings.Contains(out, "component=livetv event=component_unknown") {
		t.Fatalf("log:\n%s", out)
	}
}
