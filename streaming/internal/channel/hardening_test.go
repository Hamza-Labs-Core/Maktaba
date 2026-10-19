package channel

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPTSTracker_SpanAcrossThe33BitWrap(t *testing.T) {
	var tr ptsTracker
	start := ptsModulus - 2*90000 // 2 s before the wrap (~26.5 h of uptime)
	var b []byte
	for i := int64(0); i < 5*30; i++ { // 5 s of frames at 30 fps
		b = append(b, pesPacket((start+i*3000)%ptsModulus)...)
	}
	if !tr.scan(b) {
		t.Fatal("no PTS found")
	}
	if got := tr.span(); got < 4.9 || got > 5.0 {
		t.Fatalf("span across the wrap = %.3fs, want ~4.97s", got)
	}
	// Slightly out-of-order (B-frame / audio) PTS before the first one
	// must not blow the span up either.
	var tr2 ptsTracker
	tr2.scan(append(pesPacket(10), pesPacket(ptsModulus-3000)...)) // 1 frame "earlier", across the wrap
	if got := tr2.span(); got > 0.1 {
		t.Errorf("out-of-order PTS across the wrap gave span %.3fs", got)
	}
	if ptsDelta(5, ptsModulus-5) != 10 || ptsDelta(ptsModulus-5, 5) != -10 {
		t.Error("ptsDelta not modular")
	}
}

func TestPaceTo_BogusTimestampNeverStalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Station{cfg: StationConfig{Now: time.Now}, epoch: time.Now(), ctx: ctx}
	t0 := time.Now()
	s.paceTo(95_000) // a wrapped/garbage position ~26 h ahead
	if time.Since(t0) > 100*time.Millisecond {
		t.Fatal("paceTo slept on a bogus timestamp")
	}
}

func TestRegistry_StopRunsOutsideTheLock(t *testing.T) {
	clk := newClock(tm("2026-06-14T20:00:00Z"))
	r := newRegistry(1, clk.now)
	a := uuid.New()
	_ = r.admit(a)
	done := make(chan struct{})
	// A stop that needs the registry (e.g. a slow Stop calling back in)
	// would deadlock if evictions ran it under r.mu.
	r.putWith(a, func() { _ = r.size(); close(done) }, 0)
	clk.advance(2 * time.Minute)
	go r.reapIdle(time.Minute)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop ran under the registry lock (deadlock)")
	}
	// Same for an LRU eviction on admit.
	b := uuid.New()
	evicted := make(chan struct{})
	r.putWith(b, func() { _ = r.size(); close(evicted) }, 0)
	go func() { _ = r.admit(uuid.New()) }()
	select {
	case <-evicted:
	case <-time.After(time.Second):
		t.Fatal("admit eviction ran stop under the lock")
	}
}

// slowPipeline takes a while to finish after Stop, like a real station.
type slowPipeline struct {
	*fakePipeline
	finish time.Duration
	once   sync.Once
	done   chan struct{}
}

func (p *slowPipeline) Stop() {
	p.once.Do(func() {
		close(p.stopped)
		go func() {
			time.Sleep(p.finish)
			p.wg.Wait()
			p.hub.Close()
			close(p.done)
		}()
	})
}
func (p *slowPipeline) Done() <-chan struct{} { return p.done }

type countingRepo struct {
	fakeRepo
	mu     sync.Mutex
	events []string
}

func (c *countingRepo) SetRuntime(_ context.Context, _ Runtime) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, "set")
	return nil
}

func (c *countingRepo) ClearRuntime(_ context.Context, _ uuid.UUID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, "clear")
	return nil
}

func TestEngine_RetuneWaitsForTheStoppingStation(t *testing.T) {
	clk := newClock(time.Now())
	repo := &countingRepo{}
	var mu sync.Mutex
	var started []*slowPipeline
	start := func(_ context.Context, _ uuid.UUID, dir string) (Pipeline, error) {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range started {
			select {
			case <-p.done:
			default:
				t.Error("new station started while the old one still owned the dir")
			}
		}
		p := &slowPipeline{fakePipeline: newFakePipeline(dir, 0), finish: 150 * time.Millisecond, done: make(chan struct{})}
		started = append(started, p)
		return p, nil
	}
	e := NewEngine(repo, fakeLayout{root: t.TempDir()}, start, 2, clk.now)
	t.Cleanup(func() {
		e.StopAll()
		mu.Lock()
		defer mu.Unlock()
		for _, p := range started {
			<-p.done
		}
	})
	id := uuid.New()
	if _, err := e.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	clk.advance(2 * time.Minute)
	if len(e.ReapIdle(context.Background())) != 1 {
		t.Fatal("not reaped")
	}
	// Re-tune immediately: must wait for the old station, then start.
	if _, err := e.Ensure(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(started)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("starts = %d", n)
	}
	time.Sleep(50 * time.Millisecond)
	repo.mu.Lock()
	ev := append([]string(nil), repo.events...)
	repo.mu.Unlock()
	if len(ev) == 0 || ev[len(ev)-1] != "set" {
		t.Errorf("the old station's exit cleared the new station's runtime row: %v", ev)
	}
}

func TestLocalInputAndStreamURLAllowList(t *testing.T) {
	ok := map[string]string{
		"Y:/Shows/a.mp4":       "file:Y:/Shows/a.mp4",
		"/srv/media/a b:c.mkv": "file:/srv/media/a b:c.mkv",
		`D:\x\y.mp4`:           `file:D:\x\y.mp4`,
	}
	for in, want := range ok {
		if got, err := LocalInput(in); err != nil || got != want {
			t.Errorf("LocalInput(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "concat:/a.mp4|/b.mp4", "file:/etc/passwd",
		"udp://0.0.0.0:1234", "https://x/y.mp4", "pipe:0", "data:text/plain,x", "a\nb"} {
		if _, err := LocalInput(bad); err == nil {
			t.Errorf("LocalInput(%q) accepted", bad)
		}
	}
	for u, want := range map[string]bool{
		"https://cdn.example/live.m3u8": true, "rtmp://x/live": true, "srt://h:9000": true, "rtsp://cam/1": true,
		"udp://0.0.0.0:1234": false, "file:///etc/passwd": false, "concat:a|b": false, "http:///nohost": false,
	} {
		if IsStreamURL(u) != want {
			t.Errorf("IsStreamURL(%q) != %v", u, want)
		}
	}
	for u, want := range map[string]bool{
		"https://x/live.m3u8": true, "rtmp://x/l": true,
		"udp://0.0.0.0:1234": false, "file:///etc/passwd.ts": false, "rtp://x:1": false, "mms://x/a": false,
	} {
		if IsDirectStream(u) != want {
			t.Errorf("IsDirectStream(%q) != %v", u, want)
		}
	}
}

func TestYtDlp_OnlyHTTPAndEndOfOptions(t *testing.T) {
	y := NewYtDlp("yt")
	var got []string
	y.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		got = args
		return []byte("file:///etc/passwd\n"), nil
	}
	if _, err := y.Resolve(context.Background(), "--exec=evil"); err == nil {
		t.Error("a non-URL source must be refused before yt-dlp runs")
	}
	if got != nil {
		t.Fatal("yt-dlp ran for a non-http source")
	}
	if _, err := y.Resolve(context.Background(), "https://www.youtube.com/watch?v=x"); err == nil {
		t.Error("a non-network URL from yt-dlp must be refused")
	}
	if len(got) < 2 || got[len(got)-2] != "--" {
		t.Errorf("source must follow \"--\": %v", got)
	}
}

func TestStation_LocalAssetRefusesProtocols(t *testing.T) {
	dir := t.TempDir()
	logo := filepath.Join(dir, "logo.png")
	_ = os.WriteFile(logo, []byte("png"), 0o644)
	s := &Station{}
	if got := s.localAsset(context.Background(), AssetKindLogo, logo); got != "file:"+logo {
		t.Errorf("local logo = %q", got)
	}
	for _, bad := range []string{"udp://x:1/a.png", "file:///etc/passwd", "concat:" + logo, filepath.Join(dir, "missing.png")} {
		if got := s.localAsset(context.Background(), AssetKindLogo, bad); got != "" {
			t.Errorf("localAsset(%q) = %q, want refused", bad, got)
		}
	}
	if got := s.localAsset(context.Background(), AssetKindLogo, "https://cdn.example/logo.png"); got != "https://cdn.example/logo.png" {
		t.Errorf("https asset = %q", got)
	}
}
