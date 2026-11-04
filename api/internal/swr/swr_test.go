package swr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// gate is a build that blocks until released and counts its runs.
type gate struct {
	runs    atomic.Int32
	release chan struct{}
	val     atomic.Int32
	err     atomic.Pointer[error]
	sawCtx  chan context.Context
}

func newGate() *gate {
	return &gate{release: make(chan struct{}), sawCtx: make(chan context.Context, 16)}
}

func (g *gate) build(ctx context.Context) (int, error) {
	g.runs.Add(1)
	g.sawCtx <- ctx
	<-g.release
	if p := g.err.Load(); p != nil {
		return 0, *p
	}
	return int(g.val.Load()), nil
}

func wait(t *testing.T, c *Cache[int]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReadersShareOneBuild(t *testing.T) {
	c := &Cache[int]{TTL: 5 * time.Second}
	g := newGate()
	g.val.Store(7)
	var wg sync.WaitGroup
	got := make([]int, 20)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.Get(context.Background(), t0, g.build)
			if err != nil {
				t.Error(err)
			}
			got[i] = v
		}(i)
	}
	// every reader is waiting on the one build
	<-g.sawCtx
	time.Sleep(20 * time.Millisecond)
	close(g.release)
	wg.Wait()
	if n := g.runs.Load(); n != 1 {
		t.Fatalf("builds = %d, want 1", n)
	}
	for i, v := range got {
		if v != 7 {
			t.Fatalf("reader %d got %d", i, v)
		}
	}
}

func TestFreshValueIsServedWithoutABuild(t *testing.T) {
	c := &Cache[int]{TTL: 5 * time.Second}
	n := 0
	build := func(context.Context) (int, error) { n++; return n, nil }
	if v, _ := c.Get(context.Background(), t0, build); v != 1 {
		t.Fatalf("first = %d", v)
	}
	if v, _ := c.Get(context.Background(), t0.Add(4*time.Second), build); v != 1 || n != 1 {
		t.Fatalf("within TTL: v=%d builds=%d", v, n)
	}
}

func TestStaleValueIsServedAtOnceWhileOneRebuildRuns(t *testing.T) {
	c := &Cache[int]{TTL: 5 * time.Second}
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	g := newGate()
	g.val.Store(2)
	later := t0.Add(6 * time.Second)
	for i := 0; i < 10; i++ {
		start := time.Now()
		v, err := c.Get(context.Background(), later, g.build)
		if err != nil || v != 1 {
			t.Fatalf("stale read %d: %d %v", i, v, err)
		}
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("a stale read waited %s", d)
		}
	}
	close(g.release)
	wait(t, c)
	if n := g.runs.Load(); n != 1 {
		t.Fatalf("rebuilds = %d, want 1", n)
	}
	if v, _ := c.Get(context.Background(), later, g.build); v != 2 {
		t.Fatalf("after the rebuild = %d", v)
	}
}

func TestFailedRebuildKeepsTheLastValue(t *testing.T) {
	var reported atomic.Int32
	c := &Cache[int]{TTL: time.Second, OnError: func(error) { reported.Add(1) }}
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 5, nil }); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("canceling statement due to statement timeout")
	fail := func(context.Context) (int, error) { return 0, boom }
	if v, err := c.Get(context.Background(), t0.Add(2*time.Second), fail); err != nil || v != 5 {
		t.Fatalf("stale read during a failing build: %d %v", v, err)
	}
	wait(t, c)
	if v, err := c.Get(context.Background(), t0.Add(3*time.Second), fail); err != nil || v != 5 {
		t.Fatalf("after a failed build: %d %v", v, err)
	}
	wait(t, c)
	if reported.Load() == 0 {
		t.Fatal("a failed build is reported")
	}
}

func TestFirstBuildErrorReachesTheReader(t *testing.T) {
	c := &Cache[int]{TTL: time.Second}
	boom := errors.New("boom")
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// nothing is cached for the error: the next read builds again
	if v, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 3, nil }); err != nil || v != 3 {
		t.Fatalf("retry: %d %v", v, err)
	}
}

func TestReaderGoneDoesNotCancelTheBuild(t *testing.T) {
	c := &Cache[int]{TTL: time.Second}
	g := newGate()
	g.val.Store(9)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.Get(ctx, t0, g.build); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	bctx := <-g.sawCtx
	if bctx.Err() != nil {
		t.Fatal("the build's context ended with its reader's")
	}
	close(g.release)
	wait(t, c)
	if v, err := c.Get(context.Background(), t0, g.build); err != nil || v != 9 {
		t.Fatalf("the next reader gets the build: %d %v", v, err)
	}
	if n := g.runs.Load(); n != 1 {
		t.Fatalf("builds = %d", n)
	}
}

func TestBuildHasItsOwnTimeout(t *testing.T) {
	c := &Cache[int]{TTL: time.Second, Timeout: 20 * time.Millisecond}
	_, err := c.Get(context.Background(), t0, func(ctx context.Context) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestInvalidateWaitsForTheNewValue(t *testing.T) {
	c := &Cache[int]{TTL: time.Hour, FreshWait: 2 * time.Second}
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	v, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 2, nil })
	if err != nil || v != 2 {
		t.Fatalf("after Invalidate: %d %v", v, err)
	}
}

func TestInvalidateFallsBackToTheOldValueWhenTheBuildIsSlow(t *testing.T) {
	c := &Cache[int]{TTL: time.Hour, FreshWait: 30 * time.Millisecond}
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	g := newGate()
	g.val.Store(2)
	v, err := c.Get(context.Background(), t0, g.build)
	if err != nil || v != 1 {
		t.Fatalf("slow rebuild after Invalidate: %d %v", v, err)
	}
	close(g.release)
	wait(t, c)
	if v, _ := c.Get(context.Background(), t0, g.build); v != 2 {
		t.Fatalf("then = %d", v)
	}
}

func TestWriteDuringABuildBuildsAgain(t *testing.T) {
	c := &Cache[int]{TTL: time.Hour, FreshWait: 2 * time.Second}
	if _, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	build := func(context.Context) (int, error) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release // read before the write below
			return 10, nil
		}
		return 20, nil
	}
	done := make(chan int)
	go func() {
		v, _ := c.Get(context.Background(), t0, build)
		done <- v
	}()
	<-started
	c.Invalidate() // a second write while the first rebuild reads
	close(release)
	if v := <-done; v != 20 {
		t.Fatalf("value = %d, want the build after the write", v)
	}
}

func TestPanickingBuildIsAnError(t *testing.T) {
	c := &Cache[int]{TTL: time.Second}
	_, err := c.Get(context.Background(), t0, func(context.Context) (int, error) { panic("x") })
	if err == nil {
		t.Fatal("a panic is an error")
	}
}

func TestPeekNeverWaits(t *testing.T) {
	c := &Cache[int]{TTL: time.Second}
	g := newGate()
	g.val.Store(4)
	if _, ok := c.Peek(context.Background(), t0, g.build); ok {
		t.Fatal("nothing cached yet")
	}
	if _, ok := c.Peek(context.Background(), t0, g.build); ok {
		t.Fatal("still building")
	}
	close(g.release)
	wait(t, c)
	if v, ok := c.Peek(context.Background(), t0, g.build); !ok || v != 4 {
		t.Fatalf("after the build: %d %v", v, ok)
	}
	if n := g.runs.Load(); n != 1 {
		t.Fatalf("builds = %d", n)
	}
}
