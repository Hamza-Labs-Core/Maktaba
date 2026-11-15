package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// point builds an AE point as the edge writes it (events.ts: blob1 event, blob2 publisher, …).
func point(at time.Time, event, pub, ch, rung string, uploadMs float64) Point {
	blobs := make([]string, 12)
	blobs[broadcast.BlobIndex("event")] = event
	blobs[broadcast.BlobIndex("publisher")] = pub
	blobs[broadcast.BlobIndex("channel")] = ch
	blobs[broadcast.BlobIndex("rung")] = rung
	doubles := make([]float64, 8)
	doubles[broadcast.DoubleIndex("upload_to_visible_ms")] = uploadMs
	return Point{At: at, Blobs: blobs, Doubles: doubles}
}

func TestMinuteRows(t *testing.T) {
	m0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	ps := []Point{
		point(m0.Add(1*time.Second), "ingest", "p", "c", "src", 200),
		point(m0.Add(2*time.Second), "ingest", "p", "c", "480p", 4500),
		point(m0.Add(3*time.Second), "beat", "p", "c", "src", 0),
		point(m0.Add(4*time.Second), "beat", "p", "c", "src", 0),
		point(m0.Add(5*time.Second), "play", "p", "c", "", 0),      // not the minute query's
		point(m0.Add(time.Minute), "ingest", "p", "c", "src", 100), // the next minute
		point(m0.Add(-time.Second), "beat", "p", "c", "src", 0),    // the minute before
	}
	rows := MinuteRows(ps, m0, m0.Add(time.Minute))
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want ingest and beat", rows)
	}
	ing, beat := rows[0], rows[1]
	if ing.Str("event") != "ingest" || ing.Num("n") != 2 || ing.Num("lag_ms") != 4700 {
		t.Errorf("ingest row = %v", ing)
	}
	// 200 ms is under every bound; 4,500 ms only under 5, 10 and 30
	for i, le := range broadcast.LagBuckets {
		want := 1.0
		if le >= 5 {
			want = 2
		}
		if got := ing.Num(broadcast.LagColumn(i)); got != want {
			t.Errorf("le %g = %g, want %g", le, got, want)
		}
	}
	if beat.Str("event") != "beat" || beat.Num("n") != 2 {
		t.Errorf("beat row = %v", beat)
	}
}

// The minute poll's timing: the minute before last, folded once; the series come from the
// cloud's own WriteSeries, and nothing is served before the first fold (as the api role).
func TestFoldAndSeries(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 2, 30, 0, time.UTC)
	m := newMonitor(6.25, "tok")
	m.now = func() time.Time { return now }
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	get := func() string {
		res, err := http.Get(srv.URL + "/metrics")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if s := get(); s != "" {
		t.Fatalf("series before the first fold: %q", s)
	}
	m0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC) // the minute before last at 10:02:30
	m.add([]Point{
		point(m0.Add(time.Second), "ingest", "p", "c", "src", 4500),
		point(m0.Add(time.Second), "ingest", "p", "c", "480p", 100),
		point(m0.Add(2*time.Second), "beat", "p", "c", "src", 0),
		point(now, "beat", "p", "c", "src", 0), // still arriving: not this fold's
	})
	m.fold()
	m.fold() // the same minute again: nothing more
	s := get()
	for _, want := range []string{
		"maktaba_broadcast_channels_live 1",
		"maktaba_broadcast_viewers 1",
		"maktaba_broadcast_viewer_minutes_total 1",
		`maktaba_broadcast_ingest_lag_seconds_bucket{le="3"} 1`,
		`maktaba_broadcast_ingest_lag_seconds_bucket{le="5"} 2`,
		"maktaba_broadcast_ingest_lag_seconds_count 2",
		"maktaba_broadcast_monthly_budget_usd 6.25",
	} {
		if !strings.Contains(s, want+"\n") {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "revocation_list_age") {
		t.Error("a revocation age before the edge ever took a list")
	}
	// the estimate: Workers Paid, one channel-minute of 2 rungs, one viewer-minute
	want := broadcast.CostEstimate(1, map[int]float64{2: 1.0 / 60})
	if !strings.Contains(s, fmt.Sprintf("maktaba_broadcast_edge_cost_estimate_usd %g\n", want)) {
		t.Errorf("cost: want %g in\n%s", want, s)
	}

	res, err := http.Post(srv.URL+fmt.Sprintf("/revocations/took?at_ms=%d", now.Add(-11*time.Minute).UnixMilli()), "", nil)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatal(err, res)
	}
	_ = res.Body.Close()
	if s := get(); !strings.Contains(s, "maktaba_broadcast_revocation_list_age_seconds 660\n") {
		t.Errorf("revocation age: %s", s)
	}
}

func TestNtfyStandIn(t *testing.T) {
	m := newMonitor(0, "tok")
	srv := httptest.NewServer(m.routes())
	defer srv.Close()
	post := func(auth, body string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/ntfy", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if c := post("Bearer tok", `{"status":"firing"}`); c != http.StatusOK {
		t.Fatalf("a page: %d", c)
	}
	if c := post("Bearer other", `{}`); c != http.StatusOK {
		t.Fatalf("a page with the wrong token: %d", c)
	}
	if c := post("", "not json"); c != http.StatusBadRequest {
		t.Fatalf("not JSON: %d", c)
	}
	res, err := http.Get(srv.URL + "/ntfy")
	if err != nil {
		t.Fatal(err)
	}
	var pages []page
	if err := json.NewDecoder(res.Body).Decode(&pages); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if len(pages) != 2 || !pages[0].AuthOK || pages[1].AuthOK {
		t.Errorf("pages = %+v", pages)
	}
}

// The edge source takes each point once, and a Worker's points again from its start after a
// redeploy (a new epoch).
func TestEdgeSource(t *testing.T) {
	var epoch atomic.Value
	epoch.Store("e1")
	var lastSince atomic.Value
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastSince.Store(r.URL.Query().Get("since_ms"))
		pts := `[{"seq":1,"at_ms":1000,"blobs":["beat"],"doubles":[]},{"seq":2,"at_ms":2000,"blobs":["ingest"],"doubles":[]}]`
		if epoch.Load() == "e2" {
			pts = `[{"seq":1,"at_ms":3000,"blobs":["play"],"doubles":[]}]`
		}
		_, _ = fmt.Fprintf(w, `{"workers":[{"worker":"L0","ae_epoch":%q,"ae_points":%s}]}`, epoch.Load(), pts)
	}))
	defer edge.Close()
	src := &edgeSource{base: edge.URL, http: edge.Client()}
	ctx := context.Background()
	ps, err := src.read(ctx)
	if err != nil || len(ps) != 2 {
		t.Fatalf("first read: %v %v", ps, err)
	}
	if lastSince.Load() != "0" {
		t.Errorf("first read since %v, want 0", lastSince.Load())
	}
	if ps, _ = src.read(ctx); len(ps) != 0 {
		t.Errorf("points taken twice: %v", ps)
	}
	if lastSince.Load() == "0" {
		t.Error("the second read asked for every point again")
	}
	epoch.Store("e2")
	if ps, _ = src.read(ctx); len(ps) != 1 || ps[0].Blobs[0] != "play" {
		t.Errorf("after a redeploy: %v", ps)
	}
}
