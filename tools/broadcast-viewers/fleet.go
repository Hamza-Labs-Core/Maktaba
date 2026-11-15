package viewers

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// Fleet is N players on one channel, spread over locations and rungs, added in steps.
type Fleet struct {
	// Template is every player's config; Session, Location and Rung are set per player.
	Template  Config
	Locations int
	// Rungs are spread over the players in turn (none: each plays the master's first).
	Rungs []string
	// Spread is how long a step's new players take to start (default 4 s, a target duration):
	// real viewers' playlist reloads are not in step.
	Spread time.Duration

	mu      sync.Mutex
	players []*Player
	wg      sync.WaitGroup
}

// Add starts n more players under ctx.
func (f *Fleet) Add(ctx context.Context, n int) {
	spread := f.Spread
	if spread <= 0 {
		spread = 4 * time.Second
	}
	locs := max(f.Locations, 1)
	f.mu.Lock()
	base := len(f.players)
	f.mu.Unlock()
	for i := range n {
		idx := base + i
		c := f.Template
		c.Session = fmt.Sprintf("v%d", idx)
		c.Location = idx % locs
		if len(f.Rungs) > 0 {
			c.Rung = f.Rungs[idx%len(f.Rungs)]
		}
		p := NewPlayer(c)
		f.mu.Lock()
		f.players = append(f.players, p)
		f.mu.Unlock()
		delay := time.Duration(int64(spread) * int64(i) / int64(max(n, 1)))
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			if sleep(ctx, delay) {
				p.Run(ctx)
			}
		}()
	}
}

// Wait waits for every player to stop (after ctx ends).
func (f *Fleet) Wait() { f.wg.Wait() }

// Players is every player so far.
func (f *Fleet) Players() []*Player {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*Player(nil), f.players...)
}

// Summary is a fleet's record at one moment.
type Summary struct {
	Viewers      int            `json:"viewers"`
	Requests     map[string]int `json:"requests"`
	Segments     int            `json:"segments"`
	SegmentBytes int64          `json:"segment_bytes"`
	Errors       int            `json:"errors"`
	// FirstErrors are up to 20 of the errors, for the report.
	FirstErrors []string      `json:"first_errors,omitempty"`
	States      map[State]int `json:"states"`
	Fetch       Percentiles   `json:"fetch_ms"`
	AfterHome   Percentiles   `json:"after_home_ms"`
}

// Percentiles of a set of durations, in milliseconds.
type Percentiles struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// Percentile is the nearest-rank percentile q (0..1) of ds.
func Percentile(ds []time.Duration, q float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(math.Ceil(q*float64(len(s)))) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// PercentilesOf summarises ds.
func PercentilesOf(ds []time.Duration) Percentiles {
	ms := func(d time.Duration) float64 { return math.Round(float64(d)/float64(time.Millisecond)*10) / 10 }
	return Percentiles{N: len(ds), P50: ms(Percentile(ds, 0.5)), P95: ms(Percentile(ds, 0.95)),
		P99: ms(Percentile(ds, 0.99)), Max: ms(Percentile(ds, 1))}
}

// Summarise sums the players' records; from, when set, keeps only what each player recorded
// after its own record at that earlier Summarise (a step's own numbers).
func Summarise(stats []PlayerStats, from []PlayerStats) Summary {
	s := Summary{Viewers: len(stats), Requests: map[string]int{}, States: map[State]int{}}
	var fetch, after []time.Duration
	for i, p := range stats {
		var b PlayerStats
		if i < len(from) {
			b = from[i]
		}
		for k, v := range p.Requests {
			s.Requests[k] += v - b.Requests[k]
		}
		s.Segments += p.Segments - b.Segments
		s.SegmentBytes += p.SegmentBytes - b.SegmentBytes
		errs := p.Errors[min(len(b.Errors), len(p.Errors)):]
		s.Errors += len(errs)
		for _, e := range errs {
			if len(s.FirstErrors) < 20 {
				s.FirstErrors = append(s.FirstErrors, e)
			}
		}
		s.States[p.State]++
		fetch = append(fetch, p.Fetch[min(len(b.Fetch), len(p.Fetch)):]...)
		after = append(after, p.AfterHome[min(len(b.AfterHome), len(p.AfterHome)):]...)
	}
	s.Fetch = PercentilesOf(fetch)
	s.AfterHome = PercentilesOf(after)
	return s
}

// Snapshot is every player's record now.
func (f *Fleet) Snapshot() []PlayerStats {
	ps := f.Players()
	out := make([]PlayerStats, len(ps))
	for i, p := range ps {
		out[i] = p.Stats()
	}
	return out
}
