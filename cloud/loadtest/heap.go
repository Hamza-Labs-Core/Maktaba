package loadtest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
)

// LiveHeapSeries is the relay's Go live heap on its /metrics.
const LiveHeapSeries = metrics.LiveHeapSeries

// ErrNoLiveHeap: a relay's /metrics has no live heap (a build from before the series).
var ErrNoLiveHeap = errors.New("the relay serves no " + LiveHeapSeries)

// LiveHeap reads the relays' Go live heap from their /metrics pages, summed.
type LiveHeap struct {
	URLs []string // each relay's /metrics
	Host string   // the Host to ask with (the relay answers /metrics on its bare host only)
}

// Bytes is the relays' live heap together; ErrNoLiveHeap when a page lacks the series.
func (h LiveHeap) Bytes(ctx context.Context) (float64, error) {
	var sum float64
	for _, u := range h.URLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, err
		}
		if h.Host != "" {
			req.Host = h.Host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		m := ParseProm(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("%s: status %d", u, resp.StatusCode)
		}
		v, ok := m[LiveHeapSeries]
		if !ok {
			return 0, fmt.Errorf("%s: %w", u, ErrNoLiveHeap)
		}
		sum += v
	}
	return sum, nil
}

// HeapRounds keeps live heap samples by round, the way load-small keeps the CPU (story 50.14):
// each round's figure is the median of its samples, and the run's is the median of the rounds'.
// A sample lands on whatever the last collection found live, in-flight segments included, so
// one reading swings; the median of a round's dozen does not.
type HeapRounds struct {
	Before  float64 // the live heap before the homes connected: what the tunnels didn't add
	samples []float64
	rounds  []float64
}

// Add keeps one sample of the round under way.
func (r *HeapRounds) Add(bytes float64) { r.samples = append(r.samples, bytes) }

// EndRound closes the round under way (a round with no sample is dropped).
func (r *HeapRounds) EndRound() {
	if len(r.samples) > 0 {
		r.rounds = append(r.rounds, Median(r.samples))
		r.samples = nil
	}
}

// PerTunnel is the median over the rounds of what the load added to the live heap, per tunnel,
// and each round's figure (nil, 0 with no round or no tunnel).
func (r *HeapRounds) PerTunnel(tunnels int) (float64, []float64) {
	if tunnels <= 0 || len(r.rounds) == 0 {
		return 0, nil
	}
	per := make([]float64, len(r.rounds))
	for i, b := range r.rounds {
		per[i] = (b - r.Before) / float64(tunnels)
	}
	return Median(per), per
}
