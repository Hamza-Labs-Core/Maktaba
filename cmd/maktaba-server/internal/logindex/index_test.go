package logindex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
)

var t0 = time.Date(2026, 11, 6, 10, 15, 0, 0, time.UTC)

// a keeper writing segments into root, on a fake clock
func keeperAt(t *testing.T, root string, now *time.Time) *logkeeper.Keeper {
	t.Helper()
	k := logkeeper.New(logkeeper.Config{Root: root, Node: "home", Now: func() time.Time { return *now },
		Free: func(string) int64 { return -1 }})
	t.Cleanup(k.Close)
	return k
}

func write(t *testing.T, k *logkeeper.Keeper, role, instance string, lines ...string) {
	t.Helper()
	f, err := k.Spool(role, instance)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
	_ = f.Close()
	k.Flush()
}

func open(t *testing.T, root string, now *time.Time) *Index {
	t.Helper()
	ix, err := Open(root, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

func msgs(res Result) []string {
	var out []string
	for _, l := range res.Lines {
		var m map[string]any
		_ = json.Unmarshal(l, &m)
		out = append(out, fmt.Sprint(m["msg"]))
	}
	return out
}

// T01: lines are indexed exactly once, by position, across a restart of the role; a
// line still being written waits.
func TestIngestExactlyOnce(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", `{"level":"info","msg":"one","ts":"2026-11-06T10:00:00Z"}`, `{"level":"warn","msg":"two","ts":"2026-11-06T10:00:01Z"}`)
	ix := open(t, root, &now)
	if n, err := ix.Ingest(context.Background()); err != nil || n != 2 {
		t.Fatalf("ingest: %d %v", n, err)
	}
	if n, _ := ix.Ingest(context.Background()); n != 0 {
		t.Fatalf("a second pass indexed %d again", n)
	}
	_ = ix.Close()
	write(t, k, "api", "", `{"level":"error","msg":"three","ts":"2026-11-06T10:00:02Z"}`)
	ix2 := open(t, root, &now) // the role restarted
	if n, _ := ix2.Ingest(context.Background()); n != 1 {
		t.Fatalf("after a restart: %d, want 1", n)
	}
	res, err := ix2.Search(context.Background(), Query{Service: "api", Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(msgs(res), ","); got != "three,two,one" {
		t.Fatalf("newest first, each once: %s", got)
	}
	// a closed hour (compressed by the keeper) is read where it stopped
	now = now.Add(time.Hour)
	write(t, k, "api", "", `{"level":"info","msg":"four","ts":"2026-11-06T11:15:00Z"}`)
	k.Flush()
	if n, _ := ix2.Ingest(context.Background()); n != 1 {
		t.Fatalf("the next hour: %d, want 1", n)
	}
	res, _ = ix2.Search(context.Background(), Query{Q: "one"})
	if len(res.Lines) != 1 {
		t.Fatalf("a line of a compressed hour is read from it: %v", msgs(res))
	}
}

// T02: words, phrases, prefixes, exclusions and Arabic with or without its marks; column
// filters and bare exclusions are refused; a request id finds its lines in time order.
func TestSearch(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "",
		`{"level":"error","msg":"plex_fetch_failed","ts":"2026-11-06T10:00:00Z","request_id":"01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60","err":{"msg":"connection refused","kind":"unreachable"}}`,
		`{"level":"info","msg":"http_request","ts":"2026-11-06T10:00:01Z","request_id":"01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60","path":"/api/videos"}`,
		`{"level":"info","msg":"library_scanned","ts":"2026-11-06T10:00:02Z","attrs":{"library":"مَكْتَبَة الأفلام"}}`,
		`{"level":"warn","msg":"disk_low","ts":"2026-11-06T10:00:03Z","component":"core.api","detail":"transcoding paused"}`)
	write(t, k, "streaming", "", `{"level":"error","msg":"playback_failed","ts":"2026-11-06T10:00:04Z","request_id":"01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60"}`)
	ix := open(t, root, &now)
	if _, err := ix.Ingest(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for q, want := range map[string]string{
		"refused":              "plex_fetch_failed",
		`"connection refused"`: "plex_fetch_failed",
		"transcod*":            "disk_low",
		"مكتبة":                "library_scanned",
		"مَكْتَبَة":            "library_scanned",
		"paused -disk":         "",
		"http_request":         "http_request",
	} {
		res, err := ix.Search(ctx, Query{Q: q})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if got := strings.Join(msgs(res), ","); got != want {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	for _, bad := range []string{"msg:plex", "-only", `"open`, "a NEAR(b)"} {
		if _, err := ix.Search(ctx, Query{Q: bad}); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	res, _ := ix.Search(ctx, Query{RequestID: "01932f6e"})
	if got := strings.Join(msgs(res), ","); got != "plex_fetch_failed,http_request,playback_failed" {
		t.Errorf("one request, every service, in time order: %s", got)
	}
	res, _ = ix.Search(ctx, Query{Component: "core.api", Level: "warn"})
	if got := strings.Join(msgs(res), ","); got != "disk_low" {
		t.Errorf("by component and level: %s", got)
	}
	// pages with a cursor
	p1, _ := ix.Search(ctx, Query{Limit: 2})
	p2, _ := ix.Search(ctx, Query{Limit: 2, Cursor: p1.Cursor})
	if len(p1.Lines) != 2 || p1.Cursor == "" || len(p2.Lines) != 2 || msgs(p1)[0] == msgs(p2)[0] {
		t.Errorf("pages: %v / %v", msgs(p1), msgs(p2))
	}
	// around a line: the other services' lines then
	around, err := ix.Around(ctx, res.IDs[0], 5, 5)
	if err != nil || len(around.Lines) < 3 {
		t.Errorf("around: %v %v", msgs(around), err)
	}
}

// T04: retention and the segments the keeper removed: their rows go.
func TestSweep(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", `{"level":"info","msg":"old","ts":"2026-11-06T10:00:00Z"}`)
	ix := open(t, root, &now)
	ix.Retention = 7 * 24 * time.Hour
	_, _ = ix.Ingest(context.Background())
	now = now.Add(8 * 24 * time.Hour)
	if n, err := ix.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("retention: %d %v", n, err)
	}
	write(t, k, "api", "", `{"level":"info","msg":"kept","ts":"2026-11-14T10:00:00Z"}`)
	_, _ = ix.Ingest(context.Background())
	_ = os.RemoveAll(filepath.Join(root, "segments"))
	if n, _ := ix.Sweep(context.Background()); n != 1 {
		t.Fatalf("a removed segment's rows: %d, want 1", n)
	}
}

// T06: a corrupt logs.db is moved aside and the index made again from the segments.
func TestCorruptIndexIsRebuilt(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", `{"level":"info","msg":"kept_line","ts":"2026-11-06T10:00:00Z"}`)
	if err := os.MkdirAll(filepath.Dir(Path(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(root), []byte("this is not a database at all, not even close"), 0o600); err != nil {
		t.Fatal(err)
	}
	ix := open(t, root, &now)
	if ix.Rebuilt() == "" {
		t.Fatal("the corrupt file should be reported")
	}
	matches, _ := filepath.Glob(Path(root) + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("the corrupt file should be moved aside")
	}
	if n, _ := ix.Ingest(context.Background()); n != 1 {
		t.Fatalf("rebuilt from the segments: %d", n)
	}
}

// The live tail: a matching line indexed after the tail opened arrives as an event.
func TestTail(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	ix := open(t, root, &now)
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(ix.Handler(&ready))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/tail?level=warn", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	if l, _ := br.ReadString('\n'); !strings.HasPrefix(l, ": tail") {
		t.Fatalf("first line %q", l)
	}
	write(t, k, "api", "", `{"level":"info","msg":"quiet","ts":"2026-11-06T10:00:00Z"}`, `{"level":"warn","msg":"loud","ts":"2026-11-06T10:00:01Z"}`)
	start := time.Now()
	if _, err := ix.Ingest(context.Background()); err != nil {
		t.Fatal(err)
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("no event: %v", err)
		}
		if strings.HasPrefix(l, "data: ") {
			if !strings.Contains(l, `"loud"`) || strings.Contains(l, "quiet") {
				t.Fatalf("event %q", l)
			}
			break
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("the tail took %v (budget 2s)", d)
	}
	for _, p := range []string{"/healthz", "/readyz"} {
		r, _ := http.Get(srv.URL + p)
		if r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
}

// A segment the keeper compressed after the index read it is read from its .zst, at the
// same offsets (readAt also looks again when the .ndjson goes between its look and open).
func TestReadAtFollowsACompressedHour(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", `{"level":"info","msg":"kept_through","ts":"2026-11-06T10:00:00Z"}`)
	ix := open(t, root, &now)
	if _, err := ix.Ingest(context.Background()); err != nil {
		t.Fatal(err)
	}
	var seg string
	var off int64
	if err := ix.db.QueryRow(`SELECT seg, off FROM lines`).Scan(&seg, &off); err != nil {
		t.Fatal(err)
	}
	plain, _ := ix.segFile(seg)
	// the keeper's compression, as it runs: the .zst whole, then the .ndjson gone
	k.Close()
	b, _ := os.ReadFile(plain)
	zf, _ := os.Create(plain + ".zst")
	zw, _ := zstd.NewWriter(zf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	_ = zf.Close()
	_ = os.Remove(plain)
	got := ix.readAt(seg, []int64{off})
	if got[0] == nil || !strings.Contains(string(got[0]), "kept_through") {
		t.Fatalf("read %q", got[0])
	}
}
