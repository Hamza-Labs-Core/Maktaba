package broadcast

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor/relay"
)

// memStore is the published channels as the API leaves them.
type memStore struct {
	mu     sync.Mutex
	jobs   []Job
	states map[uuid.UUID]string
	report map[uuid.UUID]map[string]any
}

func (m *memStore) Jobs(context.Context) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Job(nil), m.jobs...), nil
}

func (m *memStore) SetState(_ context.Context, ch uuid.UUID, state, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[ch] = state + "/" + detail
	for i := range m.jobs {
		if m.jobs[i].Channel == ch {
			m.jobs[i].State = state
		}
	}
	return nil
}

func (m *memStore) Report(_ context.Context, ch uuid.UUID, r map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.report[ch] = r
	return nil
}

func (m *memStore) state(ch uuid.UUID) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[ch]
}

func (m *memStore) set(f func(*memStore)) { m.mu.Lock(); f(m); m.mu.Unlock() }

// fakeSource is the front door: it streams TS bytes and counts holds on the channel.
type fakeSource struct{ held atomic.Int32 }

func (s *fakeSource) Stream(ctx context.Context, _ uuid.UUID, w io.Writer, started func(relay.Origin)) error {
	started(relay.Origin{})
	pkt := make([]byte, 188)
	pkt[0] = 0x47
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
		if _, err := w.Write(pkt); err != nil {
			return err
		}
	}
}

func (s *fakeSource) Attach(uuid.UUID) func() {
	s.held.Add(1)
	var once sync.Once
	return func() { once.Do(func() { s.held.Add(-1) }) }
}

// fakeFFmpeg writes a segment to each HLS output every 20 ms (a ladder) or the slate file.
type fakeFFmpeg struct{ starts atomic.Int32 }

type fakeProc struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (p *fakeProc) Stdin() io.WriteCloser { return nopWC{} }
func (p *fakeProc) Wait() error           { <-p.done; return nil }
func (p *fakeProc) Kill()                 { p.once.Do(func() { close(p.stop) }) }
func (p *fakeProc) PID() int              { return 1 }
func (p *fakeProc) Tail() string          { return "" }

type nopWC struct{}

func (nopWC) Write(b []byte) (int, error) { return len(b), nil }
func (nopWC) Close() error                { return nil }

func (f *fakeFFmpeg) Start(_ string, args []string) (relay.Proc, error) {
	p := &fakeProc{stop: make(chan struct{}), done: make(chan struct{})}
	if args[len(args)-2] == "mpegts" { // the slate
		_ = os.WriteFile(args[len(args)-1], []byte("slate"), 0o600)
		close(p.done)
		return p, nil
	}
	f.starts.Add(1)
	var dirs []string
	start := 0
	for i, a := range args {
		if a == "-hls_segment_filename" {
			dirs = append(dirs, filepath.Dir(args[i+1]))
		}
		if a == "-start_number" {
			_, _ = fmt.Sscan(args[i+1], &start)
		}
	}
	go func() {
		defer close(p.done)
		for n := start; ; n++ {
			select {
			case <-p.stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			for _, d := range dirs {
				_ = os.WriteFile(filepath.Join(d, fmt.Sprintf("%d.ts", n)), []byte(fmt.Sprintf("%s-%d", d, n)), 0o600)
				pl := fmt.Sprintf("#EXTM3U\n#EXTINF:4.000,\n%d.ts\n", n)
				_ = os.WriteFile(filepath.Join(d, "index.m3u8.tmp"), []byte(pl), 0o600)
				_ = os.Rename(filepath.Join(d, "index.m3u8.tmp"), filepath.Join(d, "index.m3u8"))
			}
		}
	}()
	return p, nil
}

// recEdge records what the home sends the edge.
type recEdge struct {
	mu     sync.Mutex
	puts   map[string]int
	states []bool
}

func (e *recEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case r.Method == http.MethodPut:
		_, _ = io.Copy(io.Discard, r.Body)
		parts := strings.Split(r.URL.Path, "/") // /v1/ingest/<ch>/<rung>/<seq>.ts
		e.puts[parts[4]]++
		w.WriteHeader(http.StatusCreated)
	case strings.HasSuffix(r.URL.Path, "/state"):
		var body struct {
			OnAir bool  `json:"on_air"`
			Rungs []any `json:"rungs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		e.states = append(e.states, body.OnAir)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (e *recEdge) count(rung string) int { e.mu.Lock(); defer e.mu.Unlock(); return e.puts[rung] }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestCtl(t *testing.T, store *memStore, src *fakeSource, ff *fakeFFmpeg) *Ctl {
	return &Ctl{Store: store, Source: src, Runner: ff, Dir: t.TempDir(), Poll: 20 * time.Millisecond,
		StateEvery: 50 * time.Millisecond, Grace: 100 * time.Millisecond, RestartDelay: 10 * time.Millisecond,
		Output: func(context.Context) Output {
			return Output{Encoder: EncVideoToolbox, Width: 1920, Height: 1080, FPS: 25, KeyintSeconds: 2, VideoKbps: 6000, AudioKbps: 192}
		}}
}

// T06: a published always-on channel is held (never released by the warm grace) and each
// segment of each rung goes once; unpublished, it stops, posts on_air false and lets go.
func TestCtlAlwaysOn(t *testing.T) {
	edge := &recEdge{puts: map[string]int{}}
	srv := httptest.NewServer(edge)
	defer srv.Close()
	ch := uuid.New()
	store := &memStore{states: map[uuid.UUID]string{}, report: map[uuid.UUID]map[string]any{}, jobs: []Job{{
		Channel: ch, Name: "Seerah", Rungs: []string{"src", "720p", "480p"}, Mode: "always_on", State: "live",
		Creds: Creds{Token: "tok", IngestURL: srv.URL, Channel: "edge1", ExpiresAt: time.Now().Add(time.Hour)}}}}
	src, ff := &fakeSource{}, &fakeFFmpeg{}
	c := newTestCtl(t, store, src, ff)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	waitFor(t, "segments of every rung", func() bool {
		return edge.count("src") >= 3 && edge.count("720p") >= 3 && edge.count("480p") >= 3
	})
	if src.held.Load() != 1 {
		t.Fatalf("the channel is held %d times", src.held.Load())
	}
	waitFor(t, "the slate", func() bool { return edge.count("slate") == 1 })
	waitFor(t, "a report", func() bool { store.mu.Lock(); defer store.mu.Unlock(); return store.report[ch] != nil })

	store.set(func(m *memStore) { m.jobs = nil }) // unpublished
	waitFor(t, "the stop and the channel let go", func() bool { return len(c.Running()) == 0 && src.held.Load() == 0 })
	waitFor(t, "on_air false", func() bool {
		edge.mu.Lock()
		defer edge.mu.Unlock()
		return !edge.states[len(edge.states)-1]
	})
	cancel()
	<-done
}

// T07: an on-demand channel waits for the edge's wake, starts at once, and stops the grace
// after the edge says no one watches; until the edge tells demand at all, it runs.
func TestCtlOnDemand(t *testing.T) {
	edge := &recEdge{puts: map[string]int{}}
	srv := httptest.NewServer(edge)
	defer srv.Close()
	ch := uuid.New()
	store := &memStore{states: map[uuid.UUID]string{}, report: map[uuid.UUID]map[string]any{}, jobs: []Job{{
		Channel: ch, Name: "Seerah", Rungs: []string{"src"}, Mode: "on_demand", State: "live",
		Creds: Creds{Token: "tok", IngestURL: srv.URL, Channel: "edge1", ExpiresAt: time.Now().Add(time.Hour)}}}}
	src, ff := &fakeSource{}, &fakeFFmpeg{}
	c := newTestCtl(t, store, src, ff)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// an edge that has told no demand yet: the channel runs, never dark
	waitFor(t, "the fallback start", func() bool { return len(c.Running()) == 1 })
	// the edge tells demand: no one watches; it stops after the grace and waits for a wake
	c.Event(ctx, ch, Event{Type: "sleep"})
	waitFor(t, "the stop with no one watching", func() bool { return len(c.Running()) == 0 })
	before := edge.count("src")
	time.Sleep(100 * time.Millisecond)
	if len(c.Running()) != 0 {
		t.Fatal("an on-demand channel started before a wake")
	}
	woke := time.Now()
	c.Event(ctx, ch, Event{Type: "wake"})
	waitFor(t, "the first segment", func() bool { return edge.count("src") > before })
	if time.Since(woke) > 15*time.Second {
		t.Fatal("the first segment came after 15 s")
	}
	c.Event(ctx, ch, Event{Type: "sleep"})
	if len(c.Running()) != 1 {
		t.Fatal("stopped before the grace")
	}
	waitFor(t, "the stop after the grace", func() bool { return len(c.Running()) == 0 })
}

// T08 at the broadcaster, and the edge's refusals: an expired token stops the channel with
// tokenExpired; revoked (heard on a PUT or the socket) stops it as revoked.
func TestCtlTokenExpiryAndRevoke(t *testing.T) {
	var revoked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodPut && revoked.Load() {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":"revoked"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	ch := uuid.New()
	store := &memStore{states: map[uuid.UUID]string{}, report: map[uuid.UUID]map[string]any{}, jobs: []Job{{
		Channel: ch, Name: "Seerah", Rungs: []string{"src"}, Mode: "always_on", State: "live",
		Creds: Creds{Token: "tok", IngestURL: srv.URL, Channel: "edge1", ExpiresAt: time.Now().Add(time.Hour)}}}}
	c := newTestCtl(t, store, &fakeSource{}, &fakeFFmpeg{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	waitFor(t, "the start", func() bool { return len(c.Running()) == 1 })

	revoked.Store(true)
	waitFor(t, "revoked", func() bool { return store.state(ch) == "revoked/" })
	waitFor(t, "the stop", func() bool { return len(c.Running()) == 0 })

	// the cloud restores it; then its token runs out
	store.set(func(m *memStore) { m.jobs[0].State = "live"; m.jobs[0].Creds.ExpiresAt = time.Now().Add(-time.Second) })
	revoked.Store(false)
	waitFor(t, "tokenExpired", func() bool { return store.state(ch) == "offline/tokenExpired" })
	if len(c.Running()) != 0 {
		t.Fatal("broadcasting on an expired token")
	}
}

func TestStatesAndSlateArgs(t *testing.T) {
	st := States([]string{"src", "480p"}, Output{Width: 1920, Height: 1080, FPS: 25, VideoKbps: 6000, AudioKbps: 192})
	if st[0].Resolution != "1920x1080" || st[0].Bandwidth != 6192*1100 || st[1].Resolution != "854x480" {
		t.Fatalf("%+v", st)
	}
	dir := t.TempDir()
	texts, err := SlateText(dir, "Seerah's: 100%\n[x];")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(texts[0]); string(b) != "Seerah's: 100%[x];" {
		t.Fatalf("name file %q", b)
	}
	if b, _ := os.ReadFile(texts[2]); string(b) != slateOfflineAR {
		t.Fatalf("arabic file %q", b)
	}
	a := strings.Join(SlateArgs(texts, "/f.ttf", true, false, "/o/0.ts"), " ")
	if strings.Contains(a, "Seerah") || !strings.Contains(a, "textfile=") || !strings.Contains(a, "text_shaping=1") {
		t.Fatalf("slate: %s", a)
	}
	if strings.Contains(strings.Join(SlateArgs(texts, "", true, false, "/o"), " "), "drawtext") {
		t.Fatal("drawtext without a font")
	}
	// no shaping: the Arabic words and an Arabic name are left out, the English kept
	plain := strings.Join(SlateArgs(texts, "/f.ttf", false, true, "/o"), " ")
	if strings.Contains(plain, "text_shaping") || strings.Contains(plain, texts[0]) || strings.Contains(plain, texts[2]) ||
		!strings.Contains(plain, texts[1]) {
		t.Fatalf("unshaped slate: %s", plain)
	}
	if got := Effective([]string{"src", "720p", "480p"}, 5); len(got) != 1 || got[0] != "480p" {
		t.Fatalf("effective %v", got)
	}
}
