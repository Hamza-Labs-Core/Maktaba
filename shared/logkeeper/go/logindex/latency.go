package logindex

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Latency series (Epic 53 story 53.12, the Overview's "requests per minute and p95 latency
// (API)"). A histogram on the list with kind "latency" is kept as two series, over all its
// label sets at once (one route's own buckets would pass the label cap many times over):
//
//   - <name>_count: the requests in the minute (the +Inf bucket's increase), stored as a rate
//     per minute like a counter;
//   - <name>_p95: the minute's 95th percentile, interpolated inside the bucket it falls in,
//     as Prometheus' histogram_quantile does. A minute with no requests has none.
//
// The minute's buckets add up across its scrapes, so the p95 is the whole minute's, not the
// last 15 seconds'.

// LatencyKind is the allow-list kind of a histogram kept as requests and p95.
const LatencyKind = "latency"

// LatencySeries are the names a latency histogram is stored under.
func LatencySeries(name string) (count, p95 string) { return name + "_count", name + "_p95" }

// histState is one latency histogram between scrapes.
type histState struct {
	last   map[float64]float64 // the cumulative buckets at the last scrape, by upper bound
	minute int64
	acc    map[float64]float64 // this minute's increases, by upper bound
}

// latencyRows reads one scrape's buckets of a latency histogram. ok is false on the first
// scrape (no increase yet) or when the scrape has none of its buckets.
func (st *metricsState) latencyRows(name string, samples []sample, minute int64) (count, p95 float64, hasP95, ok bool) {
	cur := map[float64]float64{}
	for _, s := range samples {
		if s.name != name+"_bucket" {
			continue
		}
		if le, found := leOf(s.labels); found {
			cur[le] += s.value
		}
	}
	if len(cur) == 0 {
		return 0, 0, false, false
	}
	h := st.hist[name]
	if h == nil {
		h = &histState{}
		st.hist[name] = h
	}
	prev := h.last
	h.last = cur
	if prev == nil {
		return 0, 0, false, false
	}
	delta := map[float64]float64{}
	restarted := false
	for le, v := range cur {
		d := v - prev[le]
		if d < 0 {
			restarted = true
		}
		delta[le] = d
	}
	if restarted { // the process began again: its buckets count from zero
		delta = cur
	}
	if h.minute != minute || h.acc == nil {
		h.minute, h.acc = minute, map[float64]float64{}
	}
	for le, d := range delta {
		h.acc[le] += d
	}
	count = delta[math.Inf(1)]
	p95, hasP95 = quantile(0.95, h.acc)
	return count, p95, hasP95, true
}

// quantile is histogram_quantile over cumulative buckets (upper bound → count).
func quantile(q float64, buckets map[float64]float64) (float64, bool) {
	les := make([]float64, 0, len(buckets))
	for le := range buckets {
		les = append(les, le)
	}
	sort.Float64s(les)
	if len(les) == 0 {
		return 0, false
	}
	total := buckets[les[len(les)-1]]
	if total <= 0 || !math.IsInf(les[len(les)-1], 1) {
		return 0, false
	}
	rank := q * total
	lower, below := 0.0, 0.0
	for _, le := range les {
		n := buckets[le]
		if n >= rank {
			if math.IsInf(le, 1) {
				return lower, true // past the last finite bound: its bound is the answer
			}
			if n == below {
				return le, true
			}
			return lower + (le-lower)*(rank-below)/(n-below), true
		}
		lower, below = le, n
	}
	return lower, true
}

// leOf finds a bucket's upper bound in a canonical label set ("le=0.25,route=/x").
func leOf(labels string) (float64, bool) {
	for _, p := range strings.Split(labels, ",") {
		if v, found := strings.CutPrefix(p, "le="); found {
			if v == "+Inf" {
				return math.Inf(1), true
			}
			f, err := strconv.ParseFloat(v, 64)
			return f, err == nil
		}
	}
	return 0, false
}
