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
