package logindex

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// histogram_quantile's answers: inside a bucket by straight line, the last finite bound past
// it, nothing without requests.
func TestQuantile(t *testing.T) {
	inf := math.Inf(1)
	for _, c := range []struct {
		b    map[float64]float64
		want float64
		ok   bool
	}{
		// 100 requests: 50 under 0.1 s, 90 under 0.5 s, all under 1 s → rank 95 is half-way
		// into (0.5, 1]
		{map[float64]float64{0.1: 50, 0.5: 90, 1: 100, inf: 100}, 0.75, true},
		// rank 95 inside the first bucket: from 0
		{map[float64]float64{0.2: 100, inf: 100}, 0.19, true},
		// past the last finite bound
		{map[float64]float64{0.1: 10, inf: 100}, 0.1, true},
		{map[float64]float64{0.1: 0, inf: 0}, 0, false},
		{map[float64]float64{}, 0, false},
	} {
		got, ok := quantile(0.95, c.b)
		if ok != c.ok || math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%v: %v %v, want %v %v", c.b, got, ok, c.want, c.ok)
		}
	}
}

func histText(route string, b01, b05, b1, inf float64) string {
	return fmt.Sprintf(`http_request_duration_seconds_bucket{route=%q,le="0.1"} %g
http_request_duration_seconds_bucket{route=%q,le="0.5"} %g
http_request_duration_seconds_bucket{route=%q,le="1"} %g
http_request_duration_seconds_bucket{route=%q,le="+Inf"} %g
http_request_duration_seconds_sum{route=%q} 1
http_request_duration_seconds_count{route=%q} %g
`, route, b01, route, b05, route, b1, route, inf, route, route, inf)
}

// Story 53.12: the API's latency histogram reaches the Overview as requests per minute and
// the minute's p95, over every route at once; a restart's buckets count from zero.
func TestLatencyOnTheOverview(t *testing.T) {
	root, now := t.TempDir(), t0.Truncate(time.Minute)
	ix := open(t, root, &now)
	ctx := context.Background()
	rec := func(text string) {
		t.Helper()
		if err := ix.Record(ctx, parseMetrics(strings.NewReader(text)), now); err != nil {
			t.Fatal(err)
		}
	}
	val := func(name string) (float64, bool) {
		var v float64
		err := ix.db.QueryRow(`SELECT value FROM metric_1m WHERE name = ? AND labels = '' AND minute = ?`, name,
			minuteOf(now)).Scan(&v)
		return v, err == nil
	}
	// the first scrape gives no increase
	rec(histText("/a", 10, 10, 10, 10) + histText("/b", 0, 0, 0, 0))
	if _, ok := val("http_request_duration_seconds_count"); ok {
		t.Fatal("a first scrape stored requests")
	}
	// in the next minute, two scrapes: /a's 50 requests fast, /b's 50 between 0.5 and 1 s
	now = now.Add(time.Minute)
	rec(histText("/a", 40, 40, 40, 40) + histText("/b", 0, 0, 20, 20))
	now = now.Add(15 * time.Second)
	rec(histText("/a", 60, 60, 60, 60) + histText("/b", 0, 0, 50, 50))
	if v, _ := val("http_request_duration_seconds_count"); v != 100 {
		t.Errorf("requests in the minute: %v, want 100", v)
	}
	// rank 95 of 100: 50 under 0.1, 50 under 1 → 0.5 + 0.5 × (95-50)/50 = 0.95
	if v, _ := val("http_request_duration_seconds_p95"); math.Abs(v-0.95) > 1e-9 {
		t.Errorf("p95: %v, want 0.95", v)
	}
	// the API restarted: its buckets are new
	now = now.Add(time.Minute)
	rec(histText("/a", 4, 4, 4, 4) + histText("/b", 0, 0, 0, 0))
	if v, _ := val("http_request_duration_seconds_count"); v != 4 {
		t.Errorf("after a restart: %v, want 4", v)
	}
	if v, _ := val("http_request_duration_seconds_p95"); math.Abs(v-0.095) > 1e-9 {
		t.Errorf("p95 after a restart: %v, want 0.095", v)
	}
	var n int
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM metric_1m WHERE name LIKE 'http_request_duration_seconds_bucket%'`).Scan(&n)
	if n != 0 {
		t.Errorf("%d bucket rows stored: only the two series are", n)
	}
	doc, err := ix.Board(ctx, "overview", "1h")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range doc.Series {
		kinds[s.Name] = s.Kind
	}
	if kinds["http_request_duration_seconds_count"] != "counter" || kinds["http_request_duration_seconds_p95"] != "gauge" {
		t.Fatalf("overview: %v", kinds)
	}
}
