package channel

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// fakePipeline is a stand-in station: it "produces" a segment after a
// delay and lets the test publish TS bytes.
type fakePipeline struct {
	dir     string
	hub     *Hub
	done    chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newFakePipeline(dir string, readyAfter time.Duration) *fakePipeline {
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\nindex.m3u8\n"), 0o644)
	p := &fakePipeline{dir: dir, hub: NewHub(4), done: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		time.Sleep(readyAfter)
		_ = os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n#EXTINF:4.0,\nseg-1.ts\n"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "seg-1.ts"), bytes.Repeat([]byte{0x47}, tsPacket), 0o644)
	}()
	return p
}

func (p *fakePipeline) Subscribe() (<-chan []byte, func()) { return p.hub.Subscribe() }
func (p *fakePipeline) WaitReady(ctx context.Context) error {
	for {
		if b, err := os.ReadFile(filepath.Join(p.dir, "index.m3u8")); err == nil && bytes.Contains(b, []byte("#EXTINF")) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (p *fakePipeline) Done() <-chan struct{} { return p.done }
func (p *fakePipeline) Alive() bool {
	select {
	case <-p.stopped:
		return false
	default:
		return true
	}
}
func (p *fakePipeline) Stop() {
	p.once.Do(func() {
		close(p.stopped)
		p.hub.Close()
		close(p.done)
	})
}
func (p *fakePipeline) PID() int    { return 42 }
func (p *fakePipeline) Dir() string { return p.dir }

type startCounter struct {
	mu         sync.Mutex
	n          int
	last       *fakePipeline
	readyAfter time.Duration
}

func (s *startCounter) start(_ context.Context, _ uuid.UUID, dir string) (Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	s.last = newFakePipeline(dir, s.readyAfter)
	return s.last, nil
}

func newTestEngine(t *testing.T, clk *fakeClock, readyAfter time.Duration) (*Engine, *fakeRepo, *startCounter) {
	t.Helper()
	repo := &fakeRepo{}
	sc := &startCounter{readyAfter: readyAfter}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, sc.start, 2, clk.now)
	return e, repo, sc
}

func TestEngine_OnePipelinePerChannel(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, repo, sc := newTestEngine(t, clk, 0)
	id := uuid.New()
	for i := 0; i < 5; i++ {
		if _, err := e.Ensure(context.Background(), id); err != nil {
			t.Fatal(err)
		}
	}
	if sc.n != 1 {
		t.Errorf("5 tunes started %d pipelines, want 1", sc.n)
	}
	if repo.setCalls != 1 {
		t.Errorf("runtime row written %d times", repo.setCalls)
	}
	res, err := e.Tune(context.Background(), id)
	if err != nil || res.ManifestURL != "/stream/channel/"+id.String()+"/index.m3u8" {
		t.Errorf("tune: %+v %v", res, err)
	}
}

func TestEngine_UnknownChannel404(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, repo, sc := newTestEngine(t, clk, 0)
	repo.missing = true
	if _, err := e.Ensure(context.Background(), uuid.New()); !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("err = %v", err)
	}
	if sc.n != 0 {
		t.Error("must not start a pipeline for an unknown channel")
	}
}

func TestEngine_ReapAfterGraceAndRestart(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, _, sc := newTestEngine(t, clk, 0)
	id := uuid.New()
	_, _ = e.Ensure(context.Background(), id)
	clk.advance(30 * time.Second)
	if got := e.ReapIdle(context.Background()); len(got) != 0 {
		t.Fatal("reaped inside the grace window")
	}
	clk.advance(61 * time.Second)
	if got := e.ReapIdle(context.Background()); len(got) != 1 {
		t.Fatalf("idle channel not reaped: %v", got)
	}
	if sc.last.Alive() {
		t.Error("reaped pipeline still alive")
	}
	_, _ = e.Ensure(context.Background(), id)
	if sc.n != 2 {
		t.Errorf("re-tune after reap should restart, starts=%d", sc.n)
	}
}

func TestEngine_TSViewerKeepsChannelAlive(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	e, _, sc := newTestEngine(t, clk, 0)
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var got bytes.Buffer
	w := writerFunc(func(b []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return got.Write(b) })
	done := make(chan struct{})
	go func() { _ = e.StreamChannelTS(ctx, id, w); close(done) }()
	waitFor(t, func() bool { sc.mu.Lock(); defer sc.mu.Unlock(); return sc.last != nil && sc.last.hub.Clients() == 1 })

	// A second client shares the same pipeline (fan-out, one encode).
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		_ = e.StreamChannelTS(ctx2, id, writerFunc(func(b []byte) (int, error) { return len(b), nil }))
		close(done2)
	}()
	waitFor(t, func() bool { return sc.last.hub.Clients() == 2 })
	if sc.n != 1 {
		t.Fatalf("second TS client started a second encode")
	}
	sc.last.hub.Publish(bytes.Repeat([]byte{0x47}, tsPacket*2))
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return got.Len() == tsPacket*2 })

	clk.advance(5 * time.Minute)
	if len(e.ReapIdle(context.Background())) != 0 {
		t.Fatal("channel with TS viewers reaped")
	}
	cancel()
	cancel2()
	<-done
	<-done2
	clk.advance(61 * time.Second)
	if len(e.ReapIdle(context.Background())) != 1 {
		t.Error("channel should be reaped after viewers leave + grace")
	}
}

func TestHTTPHandler_LazyTuneWaitsForFirstSegment(t *testing.T) {
	clk := newClock(time.Now())
	e, _, sc := newTestEngine(t, clk, 300*time.Millisecond)
	r := chi.NewRouter()
	(&HTTPHandler{Engine: e}).Mount(r)
	srv := httptest.NewServer(r)
	defer srv.Close()
	id := uuid.New()

	resp, err := http.Get(srv.URL + "/stream/channel/" + id.String() + "/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "#EXTINF") {
		t.Fatalf("index: %d %q", resp.StatusCode, body)
	}
	if sc.n != 1 {
		t.Errorf("index request should tune once, starts=%d", sc.n)
	}
	for _, p := range []string{"/master.m3u8", "/manifest.m3u8", "/seg-1.ts"} {
		resp, err := http.Get(srv.URL + "/stream/channel/" + id.String() + p)
		if err != nil || resp.StatusCode != 200 {
			t.Errorf("%s: %v %v", p, err, resp.StatusCode)
		}
		readAll(t, resp)
	}
	if sc.n != 1 {
		t.Errorf("follow-up requests must reuse the channel, starts=%d", sc.n)
	}
	resp, _ = http.Get(srv.URL + "/stream/channel/" + id.String() + "/..%5Csecret.ts")
	if resp.StatusCode == 200 {
		t.Error("traversal segment served")
	}
	readAll(t, resp)
}

func TestHTTPHandler_ReadyTimeout503(t *testing.T) {
	clk := newClock(time.Now())
	e, _, _ := newTestEngine(t, clk, time.Hour)
	e.ReadyTimeout = 200 * time.Millisecond
	r := chi.NewRouter()
	(&HTTPHandler{Engine: e}).Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/stream/channel/"+uuid.NewString()+"/index.m3u8", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Errorf("code %d", rec.Code)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return b.String()
}
