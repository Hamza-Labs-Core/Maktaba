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
	tries map[string]int
	fail  map[string]int
	code  int // when set, every PUT answers this
	body  string
}

func newFakeEdge() *fakeEdge {
	return &fakeEdge{got: map[string][]byte{}, disc: map[string]bool{}, tries: map[string]int{}, fail: map[string]int{}}
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
