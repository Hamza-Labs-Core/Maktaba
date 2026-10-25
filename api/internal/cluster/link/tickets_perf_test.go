//go:build perf

package link

import (
	"testing"
	"time"
)

// TC11, its latency half (design §18.11), in the perf tier (make perf-ci runs it with
// `-tags perf`): under 20 TV pulls and a release download, the control channel's p99 round
// trip, for control frames and for requests each, stays within the design's 50 ms of the
// link's own cost over the runner's own round trip in the same window. The numbers are
// reported every run; the integration tier keeps the functional half
// (TestControlUnderStreams).
func TestControlLatencySLO(t *testing.T) {
	m := measureUnderStreams(t)
	t.Log(m.report)
	bound := 50*time.Millisecond + m.base[98]
	if m.ctl[98] > bound {
		t.Errorf("the control frames' p99 round trip took %v (runner's %v, bound %v) under 20 TV pulls and a release download",
			m.ctl[98], m.base[98], bound)
	}
	if m.rpc[98] > bound {
		t.Errorf("the requests' p99 round trip took %v (runner's %v, bound %v) under 20 TV pulls and a release download",
			m.rpc[98], m.base[98], bound)
	}
}
