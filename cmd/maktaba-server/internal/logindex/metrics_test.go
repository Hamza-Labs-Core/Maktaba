package logindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrapeText(restarts int, up float64, extra string) string {
	return fmt.Sprintf(`# HELP maktaba_component_restarts_total restarts
# TYPE maktaba_component_restarts_total counter
maktaba_component_restarts_total{machine="home",id="core.api"} %d
maktaba_component_up{id="core.api",machine="home",phase="running"} %g
go_goroutines 42
some_other_metric{x="1"} 7
%s`, restarts, up, extra)
}

// T01: a counter is a rate per minute (the first scrape gives none, a restart's reset is
// its new value), a gauge its last value; the closed hours roll up into avg, min and max.
func TestMetricsRatesGaugesAndRollups(t *testing.T) {
	root, now := t.TempDir(), t0.Truncate(time.Hour)
	ix := open(t, root, &now)
	ctx := context.Background()
	rec := func(text string) {
		t.Helper()
		if err := ix.Record(ctx, parseMetrics(strings.NewReader(text)), now); err != nil {
			t.Fatal(err)
		}
	}
	rec(scrapeText(10, 1, ""))
	now = now.Add(15 * time.Second)
	rec(scrapeText(12, 1, ""))
	now = now.Add(15 * time.Second)
	rec(scrapeText(13, 0, ""))
	val := func(name, labels string, minute int64) float64 {
		var v float64
		if err := ix.db.QueryRow(`SELECT value FROM metric_1m WHERE name = ? AND labels = ? AND minute = ?`, name, labels,
			minute).Scan(&v); err != nil {
			t.Fatalf("%s{%s}: %v", name, labels, err)
		}
		return v
	}
	m := minuteOf(now)
	if v := val("maktaba_component_restarts_total", "id=core.api,machine=home", m); v != 3 {
		t.Errorf("restarts in the minute: %v, want 3", v)
	}
	if v := val("maktaba_component_up", "id=core.api,machine=home,phase=running", m); v != 0 {
		t.Errorf("up, the last value: %v", v)
	}
	var n int
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM metric_1m WHERE name = 'some_other_metric'`).Scan(&n)
	if n != 0 {
		t.Error("a series not on the list is not stored")
	}
	now = now.Add(time.Minute)
	rec(scrapeText(3, 1, "")) // the process restarted: its counter began again
	if v := val("maktaba_component_restarts_total", "id=core.api,machine=home", minuteOf(now)); v != 3 {
		t.Errorf("after a reset: %v, want 3", v)
	}
	now = now.Add(time.Hour)
	if err := ix.Rollup(ctx); err != nil {
		t.Fatal(err)
	}
	var avg, lo, hi float64
	if err := ix.db.QueryRow(`SELECT value, min, max FROM metric_1h WHERE name = 'maktaba_component_up'`).Scan(&avg, &lo,
		&hi); err != nil {
		t.Fatal(err)
	}
	if lo != 0 || hi != 1 || avg != 0.5 {
		t.Errorf("rollup: avg %v min %v max %v", avg, lo, hi)
	}
}

// T02: a series past its label cap is not stored and is counted as dropped.
func TestMetricsLabelCap(t *testing.T) {
	root, now := t.TempDir(), t0
	ix := open(t, root, &now)
	var b strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "maktaba_tv_channels_on_air{channel=\"c%d\"} 1\n", i)
	}
	if err := ix.Record(context.Background(), parseMetrics(strings.NewReader(b.String())), now); err != nil {
		t.Fatal(err)
	}
	var n int
	var dropped float64
	_ = ix.db.QueryRow(`SELECT COUNT(DISTINCT labels) FROM metric_1m WHERE name = 'maktaba_tv_channels_on_air'`).Scan(&n)
	_ = ix.db.QueryRow(`SELECT value FROM metric_1m WHERE name = ?`, DroppedSeries).Scan(&dropped)
	if n != 50 || dropped != 30 {
		t.Fatalf("kept %d label sets, dropped %v; want 50 and 30", n, dropped)
	}
}

// T03: 7 days of minutes, 30 days of hours.
func TestMetricsRetention(t *testing.T) {
	root, now := t.TempDir(), t0
	ix := open(t, root, &now)
	ctx := context.Background()
	_ = ix.Record(ctx, parseMetrics(strings.NewReader("go_goroutines 3\n")), now)
	now = now.Add(2 * time.Hour)
	_ = ix.Rollup(ctx)
	now = now.Add(8 * 24 * time.Hour)
	if _, err := ix.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var m, h int
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM metric_1m WHERE name = 'go_goroutines'`).Scan(&m)
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM metric_1h WHERE name = 'go_goroutines'`).Scan(&h)
	if m != 0 || h != 1 {
		t.Fatalf("after 8 days: %d minutes, %d hours; want 0 and 1", m, h)
	}
	now = now.Add(25 * 24 * time.Hour)
	_, _ = ix.Sweep(ctx)
	_ = ix.db.QueryRow(`SELECT COUNT(*) FROM metric_1h WHERE name = 'go_goroutines'`).Scan(&h)
	if h != 0 {
		t.Fatalf("after 33 days: %d hours", h)
	}
}

// T04: a board answers its series, at most MaxPoints each, with errors per hour on the
// overview; an unknown board or range is 404; the scraper reads a /metrics endpoint.
func TestBoards(t *testing.T) {
	root, now := t.TempDir(), t0
	k := keeperAt(t, root, &now)
	write(t, k, "api", "", errLine("2026-11-06T10:00:00Z", "boom", "x.y", "bad", "0.3.80", "", ""))
	ix := open(t, root, &now)
	ctx := context.Background()
	_, _ = ix.Ingest(ctx)
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, scrapeText(1, 1, ""))
	}))
	defer src.Close()
	start := now
	ix.Scrape(ctx, []string{src.URL}, src.Client()) // over HTTP once
	samples := parseMetrics(strings.NewReader(scrapeText(1, 1, "")))
	for i := 0; i < 3*60; i++ { // then three hours, a scrape a minute
		now = now.Add(time.Minute)
		if err := ix.Record(ctx, samples, now); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := ix.Board(ctx, "overview", "24h")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, s := range doc.Series {
		names[s.Name] = len(s.Points)
		if len(s.Points) > MaxPoints {
			t.Errorf("%s: %d points", s.Name, len(s.Points))
		}
	}
	if names["go_goroutines"] < 150 || names["maktaba_errors_per_hour"] != 1 || len(doc.Series) > MaxSeries {
		t.Fatalf("overview: %v (from %v)", names, start)
	}
	st, _ := ix.Board(ctx, "storage", "24h")
	found := false
	for _, s := range st.Series {
		found = found || s.Name == "maktaba_logs_segments_bytes"
	}
	if !found {
		t.Error("storage has the log store's size")
	}
	srv := httptest.NewServer(ix.Handler(nil))
	defer srv.Close()
	for path, code := range map[string]int{"/dashboards/overview?range=7d": 200, "/dashboards/nope": 404,
		"/dashboards/overview?range=2y": 404} {
		r, err := http.Get(srv.URL + path)
		if err != nil || r.StatusCode != code {
			t.Errorf("%s: %v %v", path, r.StatusCode, err)
			continue
		}
		if code == 200 {
			var d BoardDoc
			_ = json.NewDecoder(r.Body).Decode(&d)
			if d.Board != "overview" || d.Step != 60*int64((7*24*60+MaxPoints-1)/MaxPoints) {
				t.Errorf("7d: board %q step %d", d.Board, d.Step)
			}
		}
		_ = r.Body.Close()
	}
}

func TestCanonLabels(t *testing.T) {
	if got := canonLabels(`b="2",a="x \"y\"",c="1,2"`); got != `a=x "y",b=2,c=1,2` {
		t.Fatalf("%q", got)
	}
}
