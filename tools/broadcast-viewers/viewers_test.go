package viewers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixture is a small edge: a channel whose segments appear every target duration (1 s here),
// the rungs' playlists listing the newest four, a heartbeat, and a log of who asked for what.
type fixture struct {
	t       *testing.T
	start   time.Time
	target  time.Duration
	offAir  time.Time // from then on: 503 off_air
	slateAt time.Time // from then on: the playlists end in the slate

	mu       sync.Mutex
	bySess   map[string]map[string]int // session -> kind -> count
	segments map[string]map[string]int // session -> segment path -> count
	locs     map[string]string
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, start: time.Now(), target: time.Second, bySess: map[string]map[string]int{},
		segments: map[string]map[string]int{}, locs: map[string]string{}}
}

func (f *fixture) note(sess, kind, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bySess[sess] == nil {
		f.bySess[sess], f.segments[sess] = map[string]int{}, map[string]int{}
	}
	f.bySess[sess][kind]++
	if kind == "segment" {
		f.segments[sess][path]++
	}
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	var claims ViewerClaims
	body, _ := b64.DecodeString(strings.Split(strings.TrimPrefix(parts[0], ViewerPrefix), ".")[0])
	if err := json.Unmarshal(body, &claims); err != nil || claims.Channel != parts[1] {
		f.t.Errorf("token %q for %q", parts[0], parts[1])
	}
	sess := r.Header.Get("X-Rehearsal-Session")
	if sess != claims.Session {
		f.t.Errorf("session header %q, token %q", sess, claims.Session)
	}
	f.mu.Lock()
	f.locs[sess] = r.Header.Get("X-Rehearsal-Location")
	f.mu.Unlock()
	now := time.Now()
	if !f.offAir.IsZero() && now.After(f.offAir) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"off_air","reason":"notice"}`))
		return
	}
	newest := int(now.Sub(f.start)/f.target) + 1
	switch {
	case len(parts) == 3 && parts[2] == "master.m3u8":
		f.note(sess, "master", "")
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nsrc/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=500\n480p/index.m3u8\n")
	case len(parts) == 3 && parts[2] == "beat" && r.Method == http.MethodPost:
		f.note(sess, "beat", "")
		_, _ = fmt.Fprint(w, `{"counted":true}`)
	case len(parts) == 4 && parts[3] == "index.m3u8":
		f.note(sess, "playlist", "")
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", int(f.target/time.Second), max(newest-3, 1))
		for s := max(newest-3, 1); s <= newest; s++ {
			fmt.Fprintf(&b, "#EXTINF:1.000,\n%d-abcdef01.ts\n", s)
		}
		if !f.slateAt.IsZero() && now.After(f.slateAt) {
			b.WriteString("#EXT-X-DISCONTINUITY\n#EXTINF:1.000,\n../slate/0-0badcafe.ts\n")
		}
		_, _ = fmt.Fprint(w, b.String())
	case len(parts) == 4 && strings.HasSuffix(parts[3], ".ts"):
		f.note(sess, "segment", parts[2]+"/"+parts[3])
		_, _ = w.Write(make([]byte, 1000))
	default:
		http.NotFound(w, r)
	}
}

func fleetFor(srv *httptest.Server, rungs []string) *Fleet {
	return &Fleet{Locations: 3, Rungs: rungs, Spread: 200 * time.Millisecond, Template: Config{
		Base: srv.URL, Pub: "pub-1", Channel: "abc", BeatEvery: 2 * time.Second,
		Token: func(s string) (string, error) {
			return ViewerToken(RehearsalViewerKey, ViewerClaims{Pub: "pub-1", Channel: "abc", Session: s, Mode: "full",
				Exp: time.Now().Add(10 * time.Minute).Unix()})
		},
	}}
}

// T01: 10 players against the fixture behave as real players: one master each, a playlist
// every target duration, each new segment once (from the live edge on), a heartbeat every
// period, the token and the location on every request, no error.
func TestT01PlayersAsRealOnes(t *testing.T) {
	f := newFixture(t)
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	fl := fleetFor(srv, []string{"src", "480p"})
	fl.Add(ctx, 10)
	<-ctx.Done()
	fl.Wait()

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bySess) != 10 {
		t.Fatalf("%d sessions, want 10", len(f.bySess))
	}
	for sess, kinds := range f.bySess {
		if kinds["master"] != 1 {
			t.Errorf("%s: %d masters", sess, kinds["master"])
		}
		// ~4.3 s of play at 1 s: 4 or 5 playlists
		if kinds["playlist"] < 3 || kinds["playlist"] > 6 {
			t.Errorf("%s: %d playlists in 4.5 s at a 1 s target", sess, kinds["playlist"])
		}
		// a beat at the start and one 2 s later (and maybe at 4 s)
		if kinds["beat"] < 2 || kinds["beat"] > 3 {
			t.Errorf("%s: %d beats at one per 2 s", sess, kinds["beat"])
		}
		for seg, n := range f.segments[sess] {
			if n != 1 {
				t.Errorf("%s: %s fetched %d times", sess, seg, n)
			}
		}
		// the live edge, then each new segment: one a second
		if n := len(f.segments[sess]); n < 3 || n > 6 {
			t.Errorf("%s: %d segments", sess, n)
		}
		if l, err := strconv.Atoi(f.locs[sess]); err != nil || l < 0 || l > 2 {
			t.Errorf("%s: location %q", sess, f.locs[sess])
		}
	}
	sum := Summarise(fl.Snapshot(), nil)
	if sum.Errors != 0 || sum.States[StateLive] != 10 || sum.Fetch.N != sum.Segments || sum.Requests["master"] != 10 {
		t.Fatalf("summary: %+v", sum)
	}
	// where a fetch's time went: each segment's time to its headers, never past its last byte,
	// and the playlist reloads' delay past their due time (a timer fires after it is due)
	if sum.FirstByte.N != sum.Fetch.N || sum.FirstByte.Max > sum.Fetch.Max || sum.Late.N == 0 {
		t.Fatalf("first byte %+v, fetch %+v, reloads late %+v", sum.FirstByte, sum.Fetch, sum.Late)
	}
	for _, p := range fl.Players() {
		st := p.Stats()
		for i := range st.FirstByte {
			if st.FirstByte[i] > st.Fetch[i] {
				t.Fatalf("segment %d: first byte %v after its last %v", i, st.FirstByte[i], st.Fetch[i])
			}
		}
	}
	// a step's own numbers: what was recorded after the earlier snapshot
	before := fl.Snapshot()
	if s := Summarise(fl.Snapshot(), before); s.FirstByte.N != 0 || s.Late.N != 0 {
		t.Fatalf("nothing new, yet %+v %+v", s.FirstByte, s.Late)
	}
}

// An off-air answer is the off-air card, not an error; the slate is a state of its own.
func TestPlayerOffAirAndSlate(t *testing.T) {
	f := newFixture(t)
	f.slateAt = f.start.Add(1500 * time.Millisecond)
	f.offAir = f.start.Add(3 * time.Second)
	srv := httptest.NewServer(f)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4500*time.Millisecond)
	defer cancel()
	fl := fleetFor(srv, nil)
	fl.Add(ctx, 3)
	<-ctx.Done()
	fl.Wait()
	for _, s := range fl.Snapshot() {
		if len(s.Errors) != 0 {
			t.Errorf("errors: %v", s.Errors)
		}
		if s.FirstSlate.IsZero() || s.FirstOffAir.IsZero() || s.State != StateOffAir {
			t.Errorf("states: slate %v, off air %v, now %s", s.FirstSlate, s.FirstOffAir, s.State)
		}
		if d := s.FirstOffAir.Sub(f.offAir); d < 0 || d > 1500*time.Millisecond {
			t.Errorf("off air seen %s after it began", d)
		}
	}
}

func TestParsers(t *testing.T) {
	pl := ParseMedia("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:4.000,\n7-aaaaaaaa.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:4.000,\n../slate/0-bbbbbbbb.ts\n")
	if pl.Target != 4 || pl.Sequence != 7 || len(pl.Segments) != 2 || !pl.Segments[1].Slate || !pl.Segments[1].Discontinuity || pl.Segments[0].Slate {
		t.Fatalf("%+v", pl)
	}
	if n, ok := SegmentSeq("../x/123-deadbeef.ts"); !ok || n != 123 {
		t.Fatal(n, ok)
	}
	if _, ok := SegmentSeq("index.m3u8"); ok {
		t.Fatal("not a segment")
	}
	if v := Variants("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nsrc/index.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\n480p/index.m3u8\n"); len(v) != 2 || v[1] != "480p" {
		t.Fatal(v)
	}
	if p := PercentilesOf([]time.Duration{time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond}); p.P50 != 2 || p.Max != 100 || p.N != 3 {
		t.Fatalf("%+v", p)
	}
}
