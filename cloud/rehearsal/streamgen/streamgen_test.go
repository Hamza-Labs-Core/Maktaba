package streamgen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// T02 (story 50.06): a server that answers 500 once: counted as one failed
// request; the rest count their bytes. A slow segment (past Late) fails too.
func TestFailedAndLateRequestsCount(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.Host, ".relay.test") {
			t.Errorf("host %q", r.Host)
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		switch n.Add(1) {
		case 2:
			http.Error(w, "boom", 500)
			return
		case 4:
			time.Sleep(300 * time.Millisecond) // late
		}
		_, _ = w.Write(make([]byte, size))
	}))
	defer srv.Close()
	g := &Gen{Edge: srv.URL, Domain: "relay.test", Slugs: []string{"a"}, Clients: 1, Segment: 1000, Every: 50 * time.Millisecond, Late: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	g.Run(ctx)
	s := g.Stats()
	if s.Failed != 2 {
		t.Errorf("failed %d, want 2 (a 500 and a late one): %+v", s.Failed, s.Failures)
	}
	if s.OK == 0 || s.Bytes != s.OK*1000 {
		t.Errorf("ok %d bytes %d", s.OK, s.Bytes)
	}
}

// The probe measures the longest stretch it failed.
func TestProbeDowntime(t *testing.T) {
	p := &Probe{}
	t0 := time.Now()
	p.observe(t0, true)
	p.observe(t0.Add(time.Second), false)
	p.observe(t0.Add(3*time.Second), true)
	p.observe(t0.Add(4*time.Second), false)
	p.observe(t0.Add(4500*time.Millisecond), true)
	if d, n := p.Downtime(); d != 2*time.Second || n != 2 {
		t.Errorf("downtime %s in %d outages", d, n)
	}
	if Percentile([]time.Duration{3, 1, 2, 4}, 50) != 2 || Percentile(nil, 99) != 0 {
		t.Error("percentile")
	}
}

// T02 (story 50.14): a segment that arrives after the next is due is a stall.
func TestAStallIsASegmentLateForTheNext(t *testing.T) {
	start := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	if Stalled(start, start.Add(3*time.Second), 4*time.Second) {
		t.Error("on time counted as a stall")
	}
	if !Stalled(start, start.Add(5*time.Second), 4*time.Second) {
		t.Error("a segment 1 s late is a stall")
	}
	s := Stats{OK: 990, Failed: 2, Stalls: 3}
	if got := s.StallRate(); got < 0.00503 || got > 0.00505 {
		t.Errorf("stall rate %f, want 5/992", got)
	}
}

// A 5 Mbit/s profile with 4 s segments asks for 2.5 MB every 4 s.
func TestAProfileSetsTheSegment(t *testing.T) {
	var g Gen
	g.Apply(Profile{BitsPerSecond: 5_000_000, Segment: 4 * time.Second})
	if g.Segment != 2_500_000 || g.Every != 4*time.Second || g.SegmentMS != 400 {
		t.Errorf("segment %d bytes every %s over %d ms", g.Segment, g.Every, g.SegmentMS)
	}
}
