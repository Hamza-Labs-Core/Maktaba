package timelimit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// paced is a registry on a clock that only its own waits move.
func paced(now *time.Time, slept *time.Duration) *Registry {
	g := regAt(now)
	g.Sleep = func(_ context.Context, d time.Duration) error {
		*now = now.Add(d)
		*slept += d
		return nil
	}
	return g
}

// A two-hour file pulled as fast as the client can takes two hours less the
// read-ahead: the wall-clock count holds.
func TestPacedWriterHoldsTheFileToItsLength(t *testing.T) {
	now := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	var slept time.Duration
	g := paced(&now, &slept)
	const rate = 1000.0 // bytes a second
	p := &Pacer{g: g, user: "child"}
	w := p.Writer(context.Background(), httptest.NewRecorder(), "film", rate)
	chunk := make([]byte, 500) // half a second of media
	for i := 0; i < 2*7200; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if want := 2*time.Hour - ReadAhead; slept < want-time.Second || slept > want {
		t.Fatalf("two hours of media took %s, want %s (its length less the read-ahead)", slept, want)
	}
	// a new session of the same video gets no new read-ahead: the bucket is the
	// account's and the video's
	before := slept
	w2 := p.Writer(context.Background(), httptest.NewRecorder(), "film", rate)
	for i := 0; i < 120; i++ {
		_, _ = w2.Write(chunk)
	}
	if d := slept - before; d < 59*time.Second {
		t.Fatalf("a minute of media on a new response took %s: it was given a new read-ahead", d)
	}
	// another video, and another account, have their own
	before = slept
	for _, o := range []http.ResponseWriter{
		p.Writer(context.Background(), httptest.NewRecorder(), "another film", rate),
		(&Pacer{g: g, user: "sibling"}).Writer(context.Background(), httptest.NewRecorder(), "film", rate),
	} {
		for i := 0; i < 100; i++ {
			_, _ = o.Write(chunk)
		}
	}
	if slept != before {
		t.Fatalf("another video or account waited %s inside its read-ahead", slept-before)
	}
	// left alone, the read-ahead comes back, and no further
	now = now.Add(time.Hour)
	before = slept
	for i := 0; i < 2*int(ReadAhead/time.Second)+20; i++ {
		_, _ = w.Write(chunk)
	}
	if d := slept - before; d < 9*time.Second || d > 11*time.Second {
		t.Fatalf("after an hour away, the read-ahead and 10 s more took %s, want about 10 s", d)
	}
}

// A request with no pacer (an account whose time is not kept) is never paced: the
// writer is the response's own, and a segment is served at once.
func TestNoPacerPacesNothing(t *testing.T) {
	var p *Pacer
	rec := httptest.NewRecorder()
	if w := p.Writer(context.Background(), rec, "film", 1); w != http.ResponseWriter(rec) {
		t.Fatal("a nil pacer wrapped the writer")
	}
	if !p.Segment(rec, httptest.NewRequest("GET", "/x", nil), "film", "s", "seg-1.ts", time.Hour) {
		t.Fatal("a nil pacer held a segment")
	}
	if PacerFrom(context.Background()) != nil {
		t.Fatal("a pacer from nowhere")
	}
}

// Segments: a new session of the same video shares the pace (opening sessions one
// after another earns no read-ahead), while its segments, numbered from zero again,
// are paid for again.
func TestSegmentsOfANewSessionShareThePace(t *testing.T) {
	now := time.Date(2026, 11, 10, 17, 0, 0, 0, time.UTC)
	var slept time.Duration
	g := paced(&now, &slept)
	p := &Pacer{g: g, user: "child"}
	get := func(session, name string) {
		t.Helper()
		if !p.Segment(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil), "film", session, name, 6*time.Second) {
			t.Fatalf("%s of %s was refused", name, session)
		}
	}
	for s := 0; s < 10; s++ { // ten sessions, each pulling two minutes and closing
		session := string(rune('a' + s))
		for i := 0; i < 20; i++ {
			get(session, "seg-"+string(rune('0'+i/10))+string(rune('0'+i%10))+".ts")
		}
	}
	// 1200 s of media: all of it but the read-ahead (and the segment in hand) waited
	if want := 1200*time.Second - ReadAhead - 6*time.Second; slept < want {
		t.Fatalf("twenty minutes of media over ten sessions took %s, want at least %s", slept, want)
	}
	// the same segment of one session again, in another rendition, costs nothing
	before := slept
	get("j", "seg-19.ts")
	get("j", "seg-19.m4s")
	if slept != before {
		t.Fatalf("a segment already paid for waited %s", slept-before)
	}
}

func TestSegmentIndexAndByteRate(t *testing.T) {
	for name, want := range map[string]int{"seg-12.ts": 12, "chunk-stream0-00012.m4s": 12, "seg-0.ts": 0, "7.m4s": 7} {
		if n, ok := segmentIndex(name); !ok || n != want {
			t.Errorf("segmentIndex(%q) = %d, %v", name, n, ok)
		}
	}
	for _, name := range []string{"seg.ts", "", "seg-1234567890123.ts", "index.m3u8"} {
		if n, ok := segmentIndex(name); ok {
			t.Errorf("segmentIndex(%q) = %d", name, n)
		}
	}
	if r := ByteRate(7200_000, 7200, 9999); r != 1000 {
		t.Errorf("by length: %v", r)
	}
	if r := ByteRate(7200_000, 0, 4000); r != 500_000 {
		t.Errorf("by bit rate: %v", r)
	}
	if r := ByteRate(0, 0, 0); r != fallbackByteRate {
		t.Errorf("fallback: %v", r)
	}
}
