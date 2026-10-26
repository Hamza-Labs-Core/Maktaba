package metrics

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
)

func TestParseRange(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		q                 string
		wantKey           string
		wantApproxDaysAgo float64
	}{
		{"today", "today", 0},
		{"7d", "7d", 7},
		{"", "7d", 7},
		{"30d", "30d", 30},
		{"90d", "90d", 90},
		{"bogus", "7d", 7},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x?range="+c.q, nil)
		start, key := parseRange(r)
		if key != c.wantKey {
			t.Errorf("parseRange(%q) key = %q, want %q", c.q, key, c.wantKey)
		}
		daysAgo := now.Sub(start).Hours() / 24
		if daysAgo < c.wantApproxDaysAgo-1 || daysAgo > c.wantApproxDaysAgo+1.1 {
			t.Errorf("parseRange(%q) start = %v (%.1f days ago), want ~%.0f", c.q, start, daysAgo, c.wantApproxDaysAgo)
		}
	}
}

type fakeGauges struct {
	servers, tunnels int
	err              error
	gotAge           time.Duration
}

func (f *fakeGauges) LatestGauges(_ context.Context, _ time.Time, maxAge time.Duration) (int, int, bool, error) {
	f.gotAge = maxAge
	return f.servers, f.tunnels, f.err == nil, f.err
}

// Stub audit #21: the api role reads the relay's flushed gauges, so the
// overview shows real counts instead of 0/0.
func TestLiveFromStore(t *testing.T) {
	f := &fakeGauges{servers: 3, tunnels: 1}
	live := LiveFromStore(f, time.Now)
	if s, tu := live(context.Background()); s != 3 || tu != 1 {
		t.Fatalf("live = %d/%d, want 3/1", s, tu)
	}
	if f.gotAge != metricspkg.LiveSampleMaxAge {
		t.Fatalf("max age = %v", f.gotAge)
	}
	f.err = errors.New("db down")
	if s, tu := live(context.Background()); s != 0 || tu != 0 {
		t.Fatalf("on error = %d/%d, want 0/0", s, tu)
	}
	d := &Deps{Live: func(context.Context) (int, int) { return 5, 2 }}
	if s, tu := d.live(httptest.NewRequest("GET", "/", nil)); s != 5 || tu != 2 {
		t.Fatalf("Deps.live = %d/%d", s, tu)
	}
}
