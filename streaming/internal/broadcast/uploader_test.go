package broadcast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEdge answers PUT /v1/ingest/<ch>/<rung>/<seq>.ts as the 50.21 edge does, failing
// the first fail[seq] tries of a segment with 503.
type fakeEdge struct {
	mu    sync.Mutex
	got   map[string][]byte
	disc  map[string]bool
	at    map[string]int64 // X-Maktaba-Segment-Time, unix ms
	tries map[string]int
	fail  map[string]int
	code  int // when set, every PUT answers this
	body  string
}

func newFakeEdge() *fakeEdge {
	return &fakeEdge{got: map[string][]byte{}, disc: map[string]bool{}, at: map[string]int64{}, tries: map[string]int{}, fail: map[string]int{}}
}

func (e *fakeEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/v1/ingest/edge1/")
	e.tries[key]++
	if e.code != 0 {
		w.WriteHeader(e.code)
		_, _ = io.WriteString(w, e.body)
		return
	}
	if e.tries[key] <= e.fail[key] {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	b, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(b)
	if r.Header.Get("X-Maktaba-Body-SHA256") != hex.EncodeToString(sum[:]) || r.Header.Get("X-Maktaba-Duration") == "" ||
		r.Header.Get("X-Maktaba-Time") == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, dup := e.got[key]; dup {
		w.WriteHeader(http.StatusOK)
		return
	}
	e.got[key] = b
	e.disc[key] = r.Header.Get("X-Maktaba-Discontinuity") == "1"
	e.at[key], _ = strconv.ParseInt(r.Header.Get("X-Maktaba-Segment-Time"), 10, 64)
	w.WriteHeader(http.StatusCreated)
}

// writeRung writes a rung's segments first..last and a playlist listing them.
func writeRung(t *testing.T, dir string, first, last int) {
	t.Helper()
	var pl strings.Builder
	pl.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n")
	for i := first; i <= last; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.ts", i)), []byte(fmt.Sprintf("seg-%d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&pl, "#EXT-X-PROGRAM-DATE-TIME:2026-11-14T10:00:%02d.000+0000\n#EXTINF:4.000000,\n%d.ts\n", (i*4)%60, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte(pl.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testUploader(edgeURL string, clock *time.Time) *Uploader {
	return &Uploader{
		Creds: func() (Creds, bool) {
			return Creds{Token: "tok", IngestURL: edgeURL, Channel: "edge1", ExpiresAt: clock.Add(time.Hour)}, true
		},
		Now: func() time.Time { return *clock },
		Sleep: func(_ context.Context, d time.Duration) error {
			*clock = clock.Add(d)
			return nil
		},
		Jitter: func() float64 { return 0.5 },
	}
}

// T03: each segment goes once with its hash, duration and time; a segment the edge keeps
// refusing is retried for two target durations, then skipped, and the next one carries a
// discontinuity; at most 8 segments stay on disk, each deleted once handled.
func TestUploaderOncePerSegment(t *testing.T) {
	edge := newFakeEdge()
	srv := httptest.NewServer(edge)
	defer srv.Close()
	clock := time.Unix(1_800_000_000, 0)
	up := testUploader(srv.URL, &clock)
	dir := t.TempDir()
	w := &RungWatcher{Dir: dir, Rung: "720p", Up: up}

	edge.fail["720p/2.ts"] = 2    // two 503s, then it lands: sent once
	edge.fail["720p/3.ts"] = 1000 // never: skipped after 8 s
	writeRung(t, dir, 1, 4)
	w.Step(context.Background())

	if len(edge.got) != 3 {
		t.Fatalf("the edge has %d segments: %v", len(edge.got), edge.got)
	}
	if _, ok := edge.got["720p/3.ts"]; ok {
		t.Fatal("the refused segment landed")
	}
	if !edge.disc["720p/4.ts"] || edge.disc["720p/2.ts"] || edge.disc["720p/1.ts"] {
		t.Fatalf("discontinuities: %v", edge.disc)
	}
	if edge.tries["720p/2.ts"] != 3 {
		t.Fatalf("segment 2 tried %d times", edge.tries["720p/2.ts"])
	}
	// the skip came inside the window (8 s of sleeps at most, never a queue)
	if edge.tries["720p/3.ts"] < 3 || edge.tries["720p/3.ts"] > 40 {
		t.Fatalf("segment 3 tried %d times", edge.tries["720p/3.ts"])
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".ts") {
			t.Fatalf("%s left on disk", e.Name())
		}
	}

	// a second read of the same playlist sends nothing again
	writeRung(t, dir, 1, 4)
	w.Step(context.Background())
	if edge.tries["720p/1.ts"] != 1 {
		t.Fatalf("segment 1 re-sent: %d tries", edge.tries["720p/1.ts"])
	}

	// fallen far behind: only the newest 8 go, the first of them a discontinuity
	writeRung(t, dir, 5, 20)
	w.Step(context.Background())
	if _, ok := edge.got["720p/12.ts"]; ok {
		t.Fatal("a segment past the 8 kept was sent")
	}
	if !edge.disc["720p/13.ts"] || len(edge.got) != 3+8 {
		t.Fatalf("after falling behind: %d sent, disc 13 %v", len(edge.got), edge.disc["720p/13.ts"])
	}
	ents, _ = os.ReadDir(dir)
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".ts") {
			n++
		}
	}
	if n > MaxOnDisk {
		t.Fatalf("%d segments on disk", n)
	}
}

// Refusals: 403 stops the segment and names the edge's code; an expired token is never
// sent; clock skew is reported (and retried).
func TestUploaderRefusals(t *testing.T) {
	edge := newFakeEdge()
	srv := httptest.NewServer(edge)
	defer srv.Close()
	clock := time.Unix(1_800_000_000, 0)
	dir := t.TempDir()
	path := filepath.Join(dir, "1.ts")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	seg := Segment{Rung: "src", Seq: 1, Path: path, Duration: 4}

	var heard []string
	up := testUploader(srv.URL, &clock)
	up.OnRefusal = func(c string) { heard = append(heard, c) }

	edge.code, edge.body = http.StatusForbidden, `{"error":"revoked"}`
	if up.Put(context.Background(), seg) != Skipped || edge.tries["src/1.ts"] != 1 {
		t.Fatalf("403 was retried: %d", edge.tries["src/1.ts"])
	}
	edge.code, edge.body = http.StatusBadRequest, `{"error":"clock_skew"}`
	if up.Put(context.Background(), seg) != Skipped {
		t.Fatal("clock skew never stopped")
	}
	edge.code = 0
	expired := testUploader(srv.URL, &clock)
	expired.Creds = func() (Creds, bool) {
		return Creds{Token: "tok", IngestURL: srv.URL, Channel: "edge1", ExpiresAt: clock.Add(-time.Second)}, true
	}
	expired.OnRefusal = up.OnRefusal
	before := edge.tries["src/1.ts"]
	if expired.Put(context.Background(), seg) != Skipped || edge.tries["src/1.ts"] != before {
		t.Fatal("an expired token was sent")
	}
	want := []string{"revoked", "clockSkew", "tokenExpired"}
	got := strings.Join(dedupe(heard), ",")
	if got != strings.Join(want, ",") {
		t.Fatalf("heard %v, want %v", heard, want)
	}
}

func dedupe(s []string) []string {
	var out []string
	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}

func TestParsePlaylist(t *testing.T) {
	pl := "#EXTM3U\n#EXT-X-PROGRAM-DATE-TIME:2026-11-14T10:00:00.000+0000\n#EXTINF:4.000,\n7.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:3.500,\n8.ts\n"
	segs := ParsePlaylist(strings.NewReader(pl), "/d", "480p")
	if len(segs) != 2 || segs[0].Seq != 7 || segs[0].PDT.IsZero() || segs[0].Discontinuity ||
		!segs[1].Discontinuity || segs[1].Duration != 3.5 || segs[1].Path != "/d/8.ts" {
		t.Fatalf("%+v", segs)
	}
}

// lockedClock is a fake clock the uploader and a fake edge both read (the edge's handler runs
// on the server's goroutine).
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *lockedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *lockedClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func lockedUploader(edgeURL string, c *lockedClock) *Uploader {
	return &Uploader{
		Creds: func() (Creds, bool) {
			return Creds{Token: "tok", IngestURL: edgeURL, Channel: "edge1", ExpiresAt: c.now().Add(time.Hour)}, true
		},
		Now: c.now,
		Sleep: func(_ context.Context, d time.Duration) error {
			c.add(d)
			return nil
		},
		Jitter: func() float64 { return 0.5 },
	}
}

// burstEdge refuses a rung's fifth PUT within a second with 429 and Retry-After: 1, as
// ChannelLive's precheck does (BURST_PER_SECOND), and stores the rest.
type burstEdge struct {
	clock   *lockedClock
	mu      sync.Mutex
	at      map[string][]time.Time
	tries   map[string]int
	limited int
	stored  int
}

func (e *burstEdge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/v1/ingest/edge1/")
	rung, _, _ := strings.Cut(key, "/")
	e.tries[key]++
	t := e.clock.now()
	var recent []time.Time
	for _, x := range e.at[rung] {
		if t.Sub(x) < time.Second {
			recent = append(recent, x)
		}
	}
	e.at[rung] = append(recent, t)
	if len(e.at[rung]) > 4 {
		e.limited++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"rate_limited"}`)
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
	e.stored++
	w.WriteHeader(http.StatusCreated)
}

// A rung catching up after a slow edge (8 segments waiting, the most kept on disk) sends each
// once: its PUTs are paced under the edge's burst limit, so none draws a 429 and a second PUT.
func TestUploaderCatchUpStaysUnderEdgeBurst(t *testing.T) {
	clock := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	edge := &burstEdge{clock: clock, at: map[string][]time.Time{}, tries: map[string]int{}}
	srv := httptest.NewServer(edge)
	defer srv.Close()
	up := lockedUploader(srv.URL, clock)
	dir := t.TempDir()
	w := &RungWatcher{Dir: dir, Rung: "480p", Up: up}

	writeRung(t, dir, 1, MaxOnDisk)
	start := clock.now()
	w.Step(context.Background())

	if edge.limited != 0 || edge.stored != MaxOnDisk {
		t.Fatalf("%d refused with 429, %d stored of %d", edge.limited, edge.stored, MaxOnDisk)
	}
	for i := 1; i <= MaxOnDisk; i++ {
		if n := edge.tries[fmt.Sprintf("480p/%d.ts", i)]; n != 1 {
			t.Fatalf("segment %d PUT %d times", i, n)
		}
	}
	// the backlog drains well inside one target duration's worth of segments
	if took := clock.now().Sub(start); took > time.Duration(MaxOnDisk)*PutGap {
		t.Fatalf("the backlog took %v", took)
	}
	// the live cadence is never paced: a segment a target duration later goes at once
	writeRung(t, dir, MaxOnDisk+1, MaxOnDisk+1)
	clock.add(SegmentSeconds * time.Second)
	before := clock.now()
	w.Step(context.Background())
	if clock.now() != before || edge.tries[fmt.Sprintf("480p/%d.ts", MaxOnDisk+1)] != 1 {
		t.Fatalf("the live segment waited %v", clock.now().Sub(before))
	}
}

// Two rungs are paced apart: one rung's backlog never holds back another's PUT.
func TestUploaderPacesEachRungOnItsOwn(t *testing.T) {
	clock := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	edge := &burstEdge{clock: clock, at: map[string][]time.Time{}, tries: map[string]int{}}
	srv := httptest.NewServer(edge)
	defer srv.Close()
	up := lockedUploader(srv.URL, clock)
	dir := t.TempDir()
	path := filepath.Join(dir, "1.ts")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	if up.Put(context.Background(), Segment{Rung: "src", Seq: 1, Path: path, Duration: 4}) != Sent {
		t.Fatal("src not sent")
	}
	before := clock.now()
	if up.Put(context.Background(), Segment{Rung: "360p", Seq: 1, Path: path, Duration: 4}) != Sent || clock.now() != before {
		t.Fatalf("360p waited %v behind src", clock.now().Sub(before))
	}
}

// A slow edge is waited for, never raced: an answer later than the uploader's backoffs (and than
// its PutGap) is still one PUT, the request's own timeout being the whole retry window.
func TestUploaderSlowEdgeOnePut(t *testing.T) {
	var mu sync.Mutex
	tries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tries++
		mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(1200 * time.Millisecond) // the edge busy (a CPU-starved location)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	clock := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	up := lockedUploader(srv.URL, clock)
	up.Now = time.Now // the request's own timeout runs on real time
	dir := t.TempDir()
	path := filepath.Join(dir, "1.ts")
	_ = os.WriteFile(path, []byte("x"), 0o600)
	if up.Put(context.Background(), Segment{Rung: "src", Seq: 1, Path: path, Duration: 4}) != Sent {
		t.Fatal("the slow edge's segment was skipped")
	}
	mu.Lock()
	defer mu.Unlock()
	if tries != 1 {
		t.Fatalf("%d PUTs to a slow edge", tries)
	}
}

// A real failure still retries: a 5xx and a 429 each draw one more PUT, the 429's after its
// Retry-After (never sooner, so it isn't refused again).
func TestUploaderRetriesRealFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  int
		after string
		least time.Duration
	}{
		{"500", http.StatusInternalServerError, "", 0},
		{"503 storage_unavailable", http.StatusServiceUnavailable, "1", time.Second},
		{"429 rate_limited", http.StatusTooManyRequests, "1", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &lockedClock{t: time.Unix(1_800_000_000, 0)}
			var mu sync.Mutex
			var at []time.Time
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				at = append(at, clock.now())
				_, _ = io.Copy(io.Discard, r.Body)
				if len(at) == 1 {
					if tc.after != "" {
						w.Header().Set("Retry-After", tc.after)
					}
					w.WriteHeader(tc.code)
					return
				}
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()
			up := lockedUploader(srv.URL, clock)
			dir := t.TempDir()
			path := filepath.Join(dir, "1.ts")
			_ = os.WriteFile(path, []byte("x"), 0o600)
			if up.Put(context.Background(), Segment{Rung: "src", Seq: 1, Path: path, Duration: 4}) != Sent {
				t.Fatal("not sent")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(at) != 2 {
				t.Fatalf("%d PUTs, want 2", len(at))
			}
			if gap := at[1].Sub(at[0]); gap < tc.least {
				t.Fatalf("retried after %v, before the edge's Retry-After %v", gap, tc.least)
			}
		})
	}
}

// roundTrips counts the uploader's requests as the rehearsal home's proxy does: around the
// transport, so a replay inside the transport is one round trip.
type roundTrips struct {
	next http.RoundTripper
	mu   sync.Mutex
	n    map[string]int
	errs int
}

func (c *roundTrips) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(req)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n[req.URL.Path]++
	if err != nil {
		c.errs++
	}
	return resp, err
}

// A pooled connection the edge closed before answering (a busy edge resetting idle keep-alive
// connections) is replayed on a fresh connection inside the transport: the uploader makes one
// PUT of the segment and the edge stores it once (the nightly fan-out's duplicates at 1,000
// viewers: each a "connection reset by peer", then a 201).
func TestUploaderResetConnectionIsOnePut(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	stored := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		seen[r.URL.Path]++
		first := seen[r.URL.Path] == 1
		mu.Unlock()
		// segment 2's first try arrives on the connection segment 1 left idle: the edge drops it
		// unanswered, as Miniflare did under 1,000 viewers
		if strings.HasSuffix(r.URL.Path, "/2.ts") && first {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				if tc, ok := conn.(interface{ SetLinger(int) error }); ok {
					_ = tc.SetLinger(0) // a reset, not a clean close
				}
				_ = conn.Close()
			}
			return
		}
		mu.Lock()
		stored[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	rt := &roundTrips{next: &http.Transport{}, n: map[string]int{}}
	clock := &lockedClock{t: time.Unix(1_800_000_000, 0)}
	up := lockedUploader(srv.URL, clock)
	up.HTTP = &http.Client{Transport: rt}
	dir := t.TempDir()
	for i := 1; i <= 2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("%d.ts", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("seg-%d", i)), 0o600)
		if up.Put(context.Background(), Segment{Rung: "src", Seq: int64(i), Path: path, Duration: 4}) != Sent {
			t.Fatalf("segment %d not sent", i)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["/v1/ingest/edge1/src/2.ts"] != 2 {
		t.Fatalf("the reset never happened: the edge saw segment 2 %d times", seen["/v1/ingest/edge1/src/2.ts"])
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.n["/v1/ingest/edge1/src/2.ts"] != 1 || rt.errs != 0 {
		t.Fatalf("the uploader PUT segment 2 %d times (%d errors)", rt.n["/v1/ingest/edge1/src/2.ts"], rt.errs)
	}
	if stored["/v1/ingest/edge1/src/2.ts"] != 1 {
		t.Fatalf("segment 2 stored %d times", stored["/v1/ingest/edge1/src/2.ts"])
	}
}

// ffmpegRung plays ffmpeg's hls muxer on a live input: segment i (from 1) holds the input
// from start+4(i-1) to start+4i and is listed at its close, and its program date time is
// the muxer's open (start+probe, after probing the input) plus the durations before it.
type ffmpegRung struct {
	t     *testing.T
	dir   string
	start time.Time
	probe time.Duration
	first int // the first segment's number
}

func (f ffmpegRung) segStart(i int) time.Time {
	return f.start.Add(time.Duration(i-f.first) * SegmentSeconds * time.Second)
}

// list writes the playlist and files of the segments closed by now (the last 6).
func (f ffmpegRung) list(now time.Time) {
	f.t.Helper()
	var pl strings.Builder
	pl.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n")
	var listed []int
	for i := f.first; !f.segStart(i + 1).After(now); i++ {
		listed = append(listed, i)
	}
	if len(listed) > 6 {
		listed = listed[len(listed)-6:]
	}
	for _, i := range listed {
		p := filepath.Join(f.dir, fmt.Sprintf("%d.ts", i))
		if _, err := os.Stat(p); err != nil {
			if err := os.WriteFile(p, []byte(fmt.Sprintf("seg-%d", i)), 0o600); err != nil {
				f.t.Fatal(err)
			}
		}
		pdt := f.segStart(i).Add(f.probe).UTC().Format("2006-01-02T15:04:05.000-0700")
		fmt.Fprintf(&pl, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000000,\n%d.ts\n", pdt, i)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "index.m3u8"), []byte(pl.String()), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// The segment time sent is the segment's start, not ffmpeg's late program date time: never
// later than the segment's close, and within a poll of its first frame, so the edge's
// glass-to-glass (visible minus this) is about a segment plus the upload, never 0.
func TestUploaderSegmentTimeIsStart(t *testing.T) {
	edge := newFakeEdge()
	srv := httptest.NewServer(edge)
	defer srv.Close()
	start := time.Unix(1_800_000_000, 0)
	clock := start
	up := testUploader(srv.URL, &clock)
	dir := t.TempDir()
	w := &RungWatcher{Dir: dir, Rung: "src", Up: up}
	// ffmpeg 8.1 on a live pipe: the first date is 4.67 s after the input began
	rung := ffmpegRung{t: t, dir: dir, start: start, probe: 4670 * time.Millisecond, first: 1}

	poll := 500 * time.Millisecond
	check := func(rung ffmpegRung, from, to int) {
		t.Helper()
		for i := from; i <= to; i++ {
			key := fmt.Sprintf("src/%d.ts", i)
			at, ok := edge.at[key]
			if !ok || at == 0 {
				t.Fatalf("segment %d sent no segment time", i)
			}
			got := time.UnixMilli(at)
			segStart, closed := rung.segStart(i), rung.segStart(i+1)
			if got.After(closed) {
				t.Fatalf("segment %d: time %v is after its close %v", i, got.Sub(start), closed.Sub(start))
			}
			if d := got.Sub(segStart); d < 0 || d > poll {
				t.Fatalf("segment %d: time is %v from its start", i, d)
			}
		}
	}
	// polls 300 ms off the segment grid, as a real watcher's are
	for clock = start.Add(300 * time.Millisecond); clock.Before(start.Add(41 * time.Second)); clock = clock.Add(poll) {
		rung.list(clock)
		w.Step(context.Background())
	}
	check(rung, 1, 9)

	// a new ffmpeg (a later first number, its own probe): the old correction is dropped
	restart := clock.Add(2 * time.Second)
	rung2 := ffmpegRung{t: t, dir: dir, start: restart, probe: 2 * time.Second, first: 100}
	for clock = restart.Add(100 * time.Millisecond); clock.Before(restart.Add(25 * time.Second)); clock = clock.Add(poll) {
		rung2.list(clock)
		w.Step(context.Background())
	}
	check(rung2, 100, 104)
}

// Without a program date time, a segment's time is when it was listed less its duration.
func TestSegmentClockWithoutPDT(t *testing.T) {
	var c segmentClock
	seen := time.Unix(1_800_000_000, 0)
	if got := c.start(Segment{Duration: 4}, seen); !got.Equal(seen.Add(-4 * time.Second)) {
		t.Fatalf("start %v", got)
	}
}
