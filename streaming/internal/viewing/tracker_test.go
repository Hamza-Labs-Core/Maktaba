package viewing

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeStore records every batch; fail makes the next writes fail.
type fakeStore struct {
	mu      sync.Mutex
	begun   int
	batches []Batch
	off     map[uuid.UUID]bool
	asked   [][]uuid.UUID
	fail    error
	prefErr error
}

func (f *fakeStore) Begin(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.begun++
	return nil
}

func (f *fakeStore) Write(_ context.Context, b Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.batches = append(f.batches, b)
	return nil
}

func (f *fakeStore) TrackingOff(_ context.Context, users []uuid.UUID) (map[uuid.UUID]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, users)
	if f.prefErr != nil {
		return nil, f.prefErr
	}
	out := map[uuid.UUID]bool{}
	for _, u := range users {
		if f.off[u] {
			out[u] = true
		}
	}
	return out, nil
}

func (f *fakeStore) last(t *testing.T) Batch {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.batches) == 0 {
		t.Fatal("nothing written")
	}
	return f.batches[len(f.batches)-1]
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time            { return c.t }
func (c *clock) add(d time.Duration)       { c.t = c.t.Add(d) }
func newClock() *clock                     { return &clock{t: time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)} }
func rowsOf(b Batch, ch uuid.UUID) (n int) { return len(rowsFor(b, ch)) }

func rowsFor(b Batch, ch uuid.UUID) []Row {
	var out []Row
	for _, r := range b.Rows {
		if r.Channel == ch {
			out = append(out, r)
		}
	}
	return out
}

func rig() (*Tracker, *fakeStore, *clock) {
	st := &fakeStore{off: map[uuid.UUID]bool{}}
	c := newClock()
	tr := NewTracker(st)
	tr.Now = c.now
	return tr, st, c
}

var web = Client{App: "web", Version: "0.3.25", Platform: "macos", Device: "desktop"}

func flush(t *testing.T, tr *Tracker) {
	t.Helper()
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The first segment opens a viewing; each later one moves its last segment on;
// the seconds run from the start to the last segment.
func TestSegmentOpensAndMovesAViewing(t *testing.T) {
	tr, st, c := rig()
	ch, u := uuid.New(), uuid.New()
	v := Viewer{User: u, Client: web, Addr: "127.0.0.1"}
	tr.Segment(ch, v)
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].User != u || rs[0].Surface != SurfaceApp || rs[0].Client != web ||
		!rs[0].Ended.IsZero() || rs[0].Seconds() != 0 {
		t.Fatalf("opened: %+v", rs)
	}
	id := rs[0].ID
	for i := 0; i < 10; i++ {
		c.add(4 * time.Second)
		tr.Segment(ch, v)
	}
	flush(t, tr)
	rs = rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].ID != id || rs[0].Seconds() != 40 || !rs[0].LastSeen.Equal(c.t) {
		t.Fatalf("moved on: %+v", rs)
	}
	if st.begun != 1 {
		t.Fatalf("Begin ran %d times, want once", st.begun)
	}
}

// 60 s with no segment ends the viewing (idle) at its last segment; the next
// segment opens a new one.
func TestIdleEndsAViewing(t *testing.T) {
	tr, st, c := rig()
	ch, u := uuid.New(), uuid.New()
	v := Viewer{User: u, Client: web}
	tr.Segment(ch, v)
	c.add(10 * time.Second)
	tr.Segment(ch, v)
	last := c.t
	c.add(DefaultIdle - time.Second)
	flush(t, tr)
	if rs := rowsFor(st.last(t), ch); len(rs) != 1 || !rs[0].Ended.IsZero() {
		t.Fatalf("59 s quiet: %+v", rs)
	}
	if n := tr.AppViewers()[ch]; n != 1 {
		t.Fatalf("viewers %d, want 1", n)
	}
	c.add(time.Second)
	if n := tr.AppViewers()[ch]; n != 0 {
		t.Fatalf("viewers after 60 s quiet %d, want 0", n)
	}
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].EndReason != EndIdle || !rs[0].Ended.Equal(last) || rs[0].Seconds() != 10 {
		t.Fatalf("ended: %+v", rs)
	}
	first := rs[0].ID
	flush(t, tr)
	if n := rowsOf(st.last(t), ch); n != 0 {
		t.Fatalf("an ended viewing written again: %d rows", n)
	}
	// a segment after the quiet opens a new viewing (even before a sweep)
	c.add(5 * time.Minute)
	tr.Segment(ch, v)
	c.add(2 * time.Minute)
	tr.Segment(ch, v)
	flush(t, tr)
	rs = rowsFor(st.last(t), ch)
	if len(rs) != 2 {
		t.Fatalf("rows %+v, want the quiet one ended and a new one", rs)
	}
	for _, r := range rs {
		if r.ID == first {
			t.Fatal("the first viewing came back")
		}
	}
}

// One viewer per (channel, person, app): two apps of one person are two viewers,
// two people are two; viewers with no person are told apart by address and
// counted, but never written.
func TestViewerKeys(t *testing.T) {
	tr, st, _ := rig()
	ch, a, b := uuid.New(), uuid.New(), uuid.New()
	tv := Client{App: "tv", Version: "1.0", Platform: "tvos", Device: "tv"}
	tr.Segment(ch, Viewer{User: a, Client: web})
	tr.Segment(ch, Viewer{User: a, Client: web})
	tr.Segment(ch, Viewer{User: a, Client: tv})
	tr.Segment(ch, Viewer{User: b, Client: web})
	tr.Segment(ch, Viewer{Client: web, Addr: "10.0.0.5"})
	tr.Segment(ch, Viewer{Client: web, Addr: "10.0.0.6"})
	tr.Segment(ch, Viewer{Client: web, Addr: "10.0.0.6"})
	if n := tr.AppViewers()[ch]; n != 5 {
		t.Fatalf("viewers %d, want 5", n)
	}
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 3 {
		t.Fatalf("rows %+v, want the three with a person", rs)
	}
	for _, r := range rs {
		if r.User == uuid.Nil {
			t.Fatalf("a viewer with no person written: %+v", r)
		}
	}
}

// A person who turned tracking off is counted but never written; the switch is
// read once per prefsTTL, and turning it on records the next flush.
func TestTrackingOff(t *testing.T) {
	tr, st, c := rig()
	ch, off, on := uuid.New(), uuid.New(), uuid.New()
	st.off[off] = true
	tr.Segment(ch, Viewer{User: off, Client: web})
	tr.Segment(ch, Viewer{User: on, Client: web})
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].User != on {
		t.Fatalf("rows %+v, want only the one tracking", rs)
	}
	if n := tr.AppViewers()[ch]; n != 2 {
		t.Fatalf("viewers %d, want both counted", n)
	}
	// cached: the next flushes within the TTL ask nothing
	c.add(10 * time.Second)
	tr.Segment(ch, Viewer{User: off, Client: web})
	flush(t, tr)
	if len(st.asked) != 1 {
		t.Fatalf("asked %d times within the TTL, want once", len(st.asked))
	}
	// switched on: read again after the TTL, and written from then on
	st.mu.Lock()
	delete(st.off, off)
	st.mu.Unlock()
	for i := 0; i < 2; i++ { // both keep watching
		c.add(prefsTTL / 2)
		tr.Segment(ch, Viewer{User: off, Client: web})
		tr.Segment(ch, Viewer{User: on, Client: web})
	}
	flush(t, tr)
	if len(st.asked) != 2 {
		t.Fatalf("asked %d times after the TTL, want 2", len(st.asked))
	}
	if n := rowsOf(st.last(t), ch); n != 2 {
		t.Fatalf("rows %d after switching on, want 2", n)
	}
	// an ended viewing of a person with tracking off is dropped, not kept
	st.mu.Lock()
	st.off[off] = true
	st.mu.Unlock()
	c.add(prefsTTL)
	tr.Segment(ch, Viewer{User: on, Client: web})
	c.add(DefaultIdle)
	flush(t, tr)
	for _, r := range rowsFor(st.last(t), ch) {
		if r.User == off {
			t.Fatalf("written with tracking off: %+v", r)
		}
	}
	tr.mu.Lock()
	n := len(tr.ended)
	tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d ended viewings kept", n)
	}
}

// The switch not readable: nothing of that person is written or dropped until it is.
func TestTrackingUnknownWaits(t *testing.T) {
	tr, st, c := rig()
	ch, u := uuid.New(), uuid.New()
	st.prefErr = errors.New("db down")
	tr.Segment(ch, Viewer{User: u, Client: web})
	c.add(DefaultIdle + time.Second)
	flush(t, tr)
	if n := rowsOf(st.last(t), ch); n != 0 {
		t.Fatalf("written with the switch unknown: %d", n)
	}
	st.prefErr = nil
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].EndReason != EndIdle {
		t.Fatalf("rows %+v, want the ended viewing", rs)
	}
}

// A tuner viewing opens when the stream starts, moves on with every flush and
// ends (closed) when it ends.
func TestTunerOpenClose(t *testing.T) {
	tr, st, c := rig()
	ch := uuid.New()
	done := tr.OpenTuner(ch, "192.168.1.20")
	c.add(15 * time.Second)
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].Surface != SurfaceTuner || rs[0].User != uuid.Nil || rs[0].Addr != "192.168.1.20" ||
		!rs[0].Ended.IsZero() || rs[0].Seconds() != 15 {
		t.Fatalf("open: %+v", rs)
	}
	c.add(30 * time.Second)
	done()
	done() // idempotent
	c.add(time.Minute)
	flush(t, tr)
	rs = rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].EndReason != EndClosed || rs[0].Seconds() != 45 {
		t.Fatalf("closed: %+v", rs)
	}
	flush(t, tr)
	if n := rowsOf(st.last(t), ch); n != 0 {
		t.Fatalf("a closed tuner viewing written again: %d", n)
	}
	// tuner viewers are not app viewers (the front door counts their TS)
	tr.OpenTuner(ch, "x")
	if n := tr.AppViewers()[ch]; n != 0 {
		t.Fatalf("app viewers %d", n)
	}
	if len(st.asked) != 0 {
		t.Fatalf("a tuner asked for a tracking switch: %v", st.asked)
	}
}

// Nothing reaches the store between flushes; a failed write keeps the rows (and
// the ended ones) for the next flush; channel viewers ride along.
func TestBatchingAndRetry(t *testing.T) {
	tr, st, c := rig()
	ch, u := uuid.New(), uuid.New()
	tr.Viewers = func() map[uuid.UUID]int { return tr.AppViewers() }
	for i := 0; i < 20; i++ {
		tr.Segment(ch, Viewer{User: u, Client: web})
		c.add(2 * time.Second)
	}
	if len(st.batches) != 0 {
		t.Fatal("written before a flush")
	}
	flush(t, tr)
	if len(st.batches) != 1 || len(st.batches[0].Rows) != 1 || st.batches[0].Viewers[ch] != 1 {
		t.Fatalf("one flush, one row: %+v", st.batches)
	}
	st.fail = errors.New("db down")
	c.add(DefaultIdle)
	if err := tr.Flush(context.Background()); err == nil {
		t.Fatal("want the failure")
	}
	st.fail = nil
	flush(t, tr)
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 1 || rs[0].EndReason != EndIdle {
		t.Fatalf("after the retry: %+v", rs)
	}
	if v := st.last(t).Viewers; v == nil || v[ch] != 0 {
		t.Fatalf("viewers %v, want the channel at none", v)
	}
}

// Begin failing (the database down at start) is tried again before the first write.
func TestBeginRetried(t *testing.T) {
	tr, st, _ := rig()
	st.fail = errors.New("down")
	if err := tr.Flush(context.Background()); err == nil {
		t.Fatal("want the failure")
	}
	st.fail = nil
	flush(t, tr)
	flush(t, tr)
	if st.begun != 1 {
		t.Fatalf("begun %d, want 1", st.begun)
	}
}

// Shutdown ends every open viewing (closed) and writes them a last time.
func TestRunFlushesAtShutdown(t *testing.T) {
	tr, st, c := rig()
	tr.Every = time.Hour
	ch, u := uuid.New(), uuid.New()
	tr.Segment(ch, Viewer{User: u, Client: web})
	c.add(8 * time.Second)
	tr.Segment(ch, Viewer{User: u, Client: web})
	tr.OpenTuner(ch, "10.1.1.1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { tr.Run(ctx); close(done) }()
	cancel()
	<-done
	rs := rowsFor(st.last(t), ch)
	if len(rs) != 2 {
		t.Fatalf("rows %+v", rs)
	}
	for _, r := range rs {
		if r.EndReason != EndClosed || r.Ended.IsZero() {
			t.Fatalf("not closed: %+v", r)
		}
		if r.Surface == SurfaceApp && r.Seconds() != 8 {
			t.Fatalf("app seconds %d, want 8", r.Seconds())
		}
	}
}

// No database: nothing is written, the quiet viewers are still forgotten, Run
// stops with its context, and the hooks are harmless.
func TestNoStore(t *testing.T) {
	tr := NewTracker(nil)
	c := newClock()
	tr.Now = c.now
	ch := uuid.New()
	tr.Segment(ch, Viewer{Addr: "10.0.0.9"})
	tr.OpenTuner(ch, "a")()
	if n := tr.AppViewers()[ch]; n != 1 {
		t.Fatalf("viewers %d, want 1", n)
	}
	c.add(DefaultIdle)
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr.mu.Lock()
	left := len(tr.app) + len(tr.ended)
	tr.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d viewings kept with no database", left)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr.Run(ctx)
	var nilT *Tracker
	nilT.Segment(uuid.New(), Viewer{})
	nilT.OpenTuner(uuid.New(), "a")()
	if len(nilT.AppViewers()) != 0 {
		t.Fatal("nil tracker has viewers")
	}
}

func TestParseClient(t *testing.T) {
	for in, want := range map[string]Client{
		"player/1.3.0 (ios; phone)":         {App: "player", Version: "1.3.0", Platform: "ios", Device: "phone"},
		"Web/0.3.25 (macOS; Desktop)":       {App: "web", Version: "0.3.25", Platform: "macos", Device: "desktop"},
		"tv/1.0 (androidtv;tv)":             {App: "tv", Version: "1.0", Platform: "androidtv", Device: "tv"},
		"native":                            {App: "native"},
		"remote/1.0":                        {App: "remote", Version: "1.0"},
		"":                                  {},
		"<script>/1 (x y; z)":               {App: "unknown", Version: "1", Device: "z"},
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {App: "unknown"},
	} {
		if got := ParseClient(in); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}

func TestFromUserAgent(t *testing.T) {
	for in, want := range map[string]Client{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Safari/605.1.15": {App: "web", Platform: "macos", Device: "desktop"},
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148":              {App: "web", Platform: "ios", Device: "phone"},
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) Mobile Safari/537.36":                     {App: "web", Platform: "android", Device: "phone"},
		"AppleCoreMedia/1.0.0.22A3354 (Apple TV; U; CPU OS 18_0 like Mac OS X)":             {App: "unknown", Platform: "tvos", Device: "tv"},
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64)":                                         {App: "web", Platform: "windows", Device: "desktop"},
		"": {},
	} {
		if got := FromUserAgent(in); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}

// The person comes from the gate's header only on a loopback connection.
func TestFromRequest(t *testing.T) {
	u := uuid.New()
	r := httptest.NewRequest("GET", "/stream/channel/x/seg-1.ts", nil)
	r.RemoteAddr = "127.0.0.1:50000"
	r.Header.Set(ViewerHeader, u.String())
	r.Header.Set("X-Forwarded-For", "192.168.1.7, 127.0.0.1")
	r.Header.Set(ClientHeader, "player/2.0 (ipados; tablet)")
	v := FromRequest(r)
	if v.User != u || v.Addr != "192.168.1.7" || v.Client.App != "player" || v.Client.Device != "tablet" {
		t.Fatalf("loopback: %+v", v)
	}
	r.RemoteAddr = "192.168.1.9:50000"
	if v := FromRequest(r); v.User != uuid.Nil {
		t.Fatalf("the header trusted from the LAN: %+v", v)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::1]:1"
	r.Header.Set(ViewerHeader, "not-a-uuid")
	r.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0)")
	if v := FromRequest(r); v.User != uuid.Nil || v.Addr != "::1" || v.Client.App != "web" {
		t.Fatalf("bad header: %+v", v)
	}
}
