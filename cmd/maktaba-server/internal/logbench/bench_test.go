//go:build logbench

package logbench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logindex"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/logkeeper"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

var (
	factor  = 1.0
	resMu   sync.Mutex
	results = map[string]map[string]any{}
)

func TestMain(m *testing.M) {
	factor = Calibrate()
	code := m.Run()
	results["_runner"] = map[string]any{"factor": factor, "scale": Scale()}
	b, _ := json.MarshalIndent(results, "", "  ")
	fmt.Println(string(b))
	if out := os.Getenv("LOGBENCH_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0o644)
	}
	os.Exit(code)
}

// record keeps a bench's numbers for the report (and the job's artifact).
func record(name string, kv map[string]any) {
	resMu.Lock()
	defer resMu.Unlock()
	results[name] = kv
}

// budget fails t when got is over want times the runner's factor.
func budget(t *testing.T, what string, got, want time.Duration) {
	t.Helper()
	limit := time.Duration(float64(want) * factor)
	if got > limit {
		t.Errorf("%s: %v, over its budget of %v (%v at the reference, runner factor %.2f)", what, got, limit, want, factor)
	}
}

func p(ds []time.Duration, q float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(float64(len(s)-1) * q)
	return s[i]
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// countLevels reads every segment's lines (compressed or not) under root: lines by level.
func countLevels(t *testing.T, root string) map[string]int {
	t.Helper()
	out := map[string]int{}
	_ = filepath.Walk(filepath.Join(root, "segments"), func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || strings.Contains(path, string(filepath.Separator)+"warn"+string(filepath.Separator)) {
			return nil
		}
		var r io.Reader
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		switch {
		case strings.HasSuffix(path, ".ndjson.zst"):
			zr, err := zstd.NewReader(f)
			if err != nil {
				return nil
			}
			defer zr.Close()
			r = zr
		case strings.HasSuffix(path, ".ndjson"):
			r = f
		default:
			return nil
		}
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var m struct {
				Level string `json:"level"`
				Msg   string `json:"msg"`
			}
			if json.Unmarshal(sc.Bytes(), &m) == nil && m.Msg != "log_lines_dropped" {
				out[m.Level]++
			}
		}
		return nil
	})
	return out
}

// capture_burst (scaled): four children write the burst's lines as fast as they can, 20 %
// warn and above; the keeper keeps every one, warn and above in their segments, at a cost
// per line that keeps the sustained 500 lines/s under 1 % of a core (20 µs a line).
func TestCaptureBurst(t *testing.T) {
	root := t.TempDir()
	k := logkeeper.New(logkeeper.Config{Root: root, Node: "home", Free: func(string) int64 { return -1 }})
	defer k.Close()
	n := int(50_000 * Scale()) // 10 s of 5,000 lines/s
	lines := Corpus(1, n, time.Now(), time.Minute)
	written := map[string]int{}
	for _, l := range lines {
		var m struct{ Level string }
		_ = json.Unmarshal(l, &m)
		written[m.Level]++
	}
	start, cpu0 := time.Now(), cpuTime()
	var wg sync.WaitGroup
	for c := 0; c < 4; c++ {
		f, err := k.Spool("api", fmt.Sprintf("c%d", c))
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(c int, f *os.File) {
			defer wg.Done()
			w := bufio.NewWriterSize(f, 64<<10)
			for i := c; i < len(lines); i += 4 {
				_, _ = w.Write(lines[i])
				_ = w.WriteByte('\n')
			}
			_ = w.Flush()
			_ = f.Close()
		}(c, f)
	}
	wg.Wait()
	k.Flush()
	k.Flush()
	elapsed := time.Since(start)
	// "1 % of a core at 500 lines/s": the process's CPU per line (the four writers' share is
	// a buffered write each, small beside the keeper's parse, scrub and segment write)
	perLine := (cpuTime() - cpu0) / time.Duration(n)
	got := countLevels(t, root)
	warnPlus := func(m map[string]int) int { return m["warn"] + m["error"] + m["fatal"] }
	record("capture_burst", map[string]any{"lines": n, "elapsed_ms": elapsed.Milliseconds(), "cpu_ns_per_line": perLine.Nanoseconds(),
		"kept": got, "written": written})
	if warnPlus(got) != warnPlus(written) {
		t.Errorf("warn and above: kept %d of %d", warnPlus(got), warnPlus(written))
	}
	if total := got["info"] + got["debug"] + warnPlus(got); total != n {
		t.Errorf("lines kept: %d of %d (no disk pressure here: none may go)", total, n)
	}
	budget(t, "capture CPU per line", perLine, 20*time.Microsecond)
}

// index_lag, index_catchup, search_* (scaled): a corpus over 7 days goes through the keeper
// into segments; the index catches up within its per-line budget (the story's 2 minutes for
// a 5,000/s minute), a second's 500 lines are searchable within 2 s, and the three kinds of
// search answer within their p95.
func TestIndexAndSearch(t *testing.T) {
	root := t.TempDir()
	end := time.Now().UTC().Truncate(time.Hour)
	c := &clock{t: end.Add(-7 * 24 * time.Hour)}
	k := logkeeper.New(logkeeper.Config{Root: root, Node: "home", Now: c.now, Free: func(string) int64 { return -1 }})
	n := int(300_000 * Scale())
	lines := Corpus(2, n, end, 7*24*time.Hour)
	f, err := k.Spool("api", "")
	if err != nil {
		t.Fatal(err)
	}
	hours := 7 * 24
	per := (n + hours - 1) / hours // an hour's lines (the last hour takes what is left)
	for h := 0; h < hours; h++ {
		w := bufio.NewWriterSize(f, 1<<20)
		for i := h * per; i < (h+1)*per && i < n; i++ {
			_, _ = w.Write(lines[i])
			_ = w.WriteByte('\n')
		}
		_ = w.Flush()
		k.Flush()
		c.add(time.Hour)
	}
	k.Flush()
	ix, err := logindex.Open(root, c.now)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	ctx := context.Background()
	start := time.Now()
	indexed, err := ix.Ingest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catchup := time.Since(start)
	perLine := catchup / time.Duration(max(indexed, 1))
	// a second at 500 lines/s, then one pass (the role polls every 200 ms)
	for i := 0; i < 500; i++ {
		_, _ = f.Write(append(append([]byte(nil), lines[i%len(lines)]...), '\n'))
	}
	k.Flush()
	start = time.Now()
	if _, err := ix.Ingest(ctx); err != nil {
		t.Fatal(err)
	}
	lag := 200*time.Millisecond + time.Since(start)
	_ = f.Close()
	k.Close()
	rid := ""
	var m struct {
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(lines[n/2], &m)
	rid = m.RequestID
	run := func(q logindex.Query) []time.Duration {
		var ds []time.Duration
		for i := 0; i < 20; i++ {
			s := time.Now()
			if _, err := ix.Search(ctx, q); err != nil {
				t.Fatal(err)
			}
			ds = append(ds, time.Since(s))
		}
		return ds
	}
	filters := run(logindex.Query{Service: "api", Level: "warn", Since: end.Add(-24 * time.Hour)})
	full := run(logindex.Query{Q: "plex_fetch_failed مكتبة"})
	req := run(logindex.Query{RequestID: rid})
	record("index_and_search", map[string]any{"lines": indexed, "catchup_ms": catchup.Milliseconds(),
		"index_ns_per_line": perLine.Nanoseconds(), "lag_p95_ms": lag.Milliseconds(),
		"search_filters_p95_ms": p(filters, .95).Milliseconds(), "search_fulltext_p95_ms": p(full, .95).Milliseconds(),
		"search_request_p95_ms": p(req, .95).Milliseconds()})
	if indexed < n {
		t.Errorf("indexed %d of %d lines", indexed, n)
	}
	// 2 minutes for the 300,000 lines of a 5,000/s minute: 400 µs a line
	budget(t, "index catch-up per line", perLine, 400*time.Microsecond)
	budget(t, "index lag (p95)", lag, 2*time.Second)
	budget(t, "search with filters (p95)", p(filters, .95), 300*time.Millisecond)
	budget(t, "full-text search (p95)", p(full, .95), 1500*time.Millisecond)
	budget(t, "search by request (p95)", p(req, .95), 300*time.Millisecond)
}

// dashboard_30d: 30 days of hourly rollups for a board's series answer within 500 ms p95.
func TestDashboard30d(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	ix, err := logindex.Open(root, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	ctx := context.Background()
	allowed, _, _ := logindex.AllowedSeries()
	for name, s := range allowed {
		if s.Board != "overview" {
			continue
		}
		for l := 0; l < 5; l++ {
			var pts [][2]float64
			for h := 0; h < 30*24; h++ {
				pts = append(pts, [2]float64{float64(now.Add(-time.Duration(h) * time.Hour).Unix()), float64(h % 17)})
			}
			if err := ix.ImportHours(ctx, name, fmt.Sprintf("id=c%d,machine=home", l), pts); err != nil {
				t.Fatal(err)
			}
		}
	}
	var ds []time.Duration
	for i := 0; i < 20; i++ {
		s := time.Now()
		doc, err := ix.Board(ctx, "overview", "30d")
		if err != nil || len(doc.Series) == 0 {
			t.Fatalf("board: %v %d", err, len(doc.Series))
		}
		ds = append(ds, time.Since(s))
	}
	record("dashboard_30d", map[string]any{"p95_ms": p(ds, .95).Milliseconds()})
	budget(t, "dashboard 30 d (p95)", p(ds, .95), 500*time.Millisecond)
}

// logger_overhead (Go): a log call costs at most 20 µs at the 99th percentile.
func TestLoggerOverhead(t *testing.T) {
	l := mlog.NewLogger(mlog.Options{Service: "bench", Format: "json", Output: io.Discard})
	n := int(200_000 * Scale())
	ds := make([]time.Duration, n)
	ctx := context.Background()
	for i := 0; i < n; i++ {
		s := time.Now()
		l.LogAttrs(ctx, slog.LevelInfo, "http_request", slog.String("path", "/api/videos/123?token=abc"),
			slog.Int("status", 200), slog.String("user_id", "u-1"), slog.Duration("took", time.Millisecond))
		ds[i] = time.Since(s)
	}
	record("logger_overhead_go", map[string]any{"calls": n, "p50_ns": p(ds, .5).Nanoseconds(), "p99_ns": p(ds, .99).Nanoseconds()})
	budget(t, "a log call (p99)", p(ds, .99), 20*time.Microsecond)
}

// disk: a simulated day at the sustained rate, scaled to a smaller cap: at every hour the
// segments stay within the cap (plus the hour still open), and the newest warn lines of each
// service are kept.
func TestDiskCaps(t *testing.T) {
	root := t.TempDir()
	c := &clock{t: time.Now().UTC().Truncate(time.Hour)}
	limit := int64(4 << 20)
	k := logkeeper.New(logkeeper.Config{Root: root, Node: "home", Now: c.now, Free: func(string) int64 { return -1 },
		MaxSegments: limit})
	defer k.Close()
	lines := Corpus(3, 3000, c.now(), time.Hour)
	worst := int64(0)
	for h := 0; h < 30; h++ {
		f, _ := k.Spool("pipeline", "")
		w := bufio.NewWriterSize(f, 1<<20)
		for _, l := range lines {
			_, _ = w.Write(l)
			_ = w.WriteByte('\n')
		}
		_ = w.Flush()
		_ = f.Close()
		k.Flush()
		c.add(time.Hour)
		k.Flush()
		k.Janitor()
		var total int64
		_ = filepath.Walk(filepath.Join(root, "segments"), func(_ string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				total += fi.Size()
			}
			return nil
		})
		worst = max(worst, total)
	}
	record("disk", map[string]any{"cap_bytes": limit, "worst_bytes": worst})
	if worst > limit+limit/2 {
		t.Errorf("segments reached %d bytes, over the cap of %d (and the hour still open)", worst, limit)
	}
	// the corpus's lines carry the day's first hour; the floor keeps the newest files' lines
	got := k.Read(logkeeper.Query{Services: []string{"pipeline"}, MinLevel: "warn", Since: c.now().Add(-72 * time.Hour), Limit: 5})
	if len(got.Lines) == 0 {
		t.Error("the newest warn lines must be kept")
	}
}
