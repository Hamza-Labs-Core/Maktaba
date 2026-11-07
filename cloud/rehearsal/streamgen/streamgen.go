// Package streamgen plays the apps in the cloud rehearsal (Epic 50 story
// 50.06): M clients, each playing an HLS-like loop through a home's relay
// address (a segment every few seconds), logging every request that failed
// or came late; and a probe that measures downtime.
package streamgen

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Gen is the stream generator.
type Gen struct {
	Edge       string        // http://127.0.0.1:18080: the relay's edge
	Domain     string        // relay.test: a home is <slug>.<Domain>
	Slugs      []string      // the homes to play from (client i plays Slugs[i % len])
	Clients    int           // M
	Segment    int           // bytes per segment (default 256 KiB)
	Every      time.Duration // a segment this often (default 1 s)
	SegmentMS  int           // how long the home takes to send one (default 200)
	Late       time.Duration // a request slower than this failed (default 10 s)
	HTTPClient *http.Client

	ok, failed, bytes atomic.Int64
	// stalls: segments that arrived after the next one was due (story 50.14)
	stalls   atomic.Int64
	mu       sync.Mutex
	failures []Failure
	ttfb     []time.Duration // a sample of times to first byte (the first 10,000)
}

// Profile is a player's stream (story 50.14): its bitrate and segment length, which set a
// segment's size and how often one is asked for.
type Profile struct {
	BitsPerSecond int           // 2.5, 5 or 8 Mbit/s
	Segment       time.Duration // 4 s
}

// Apply sets the generator's segment size and interval from p; the home sends a segment
// over a tenth of its length (a home faster than real time, as a real one is).
func (g *Gen) Apply(p Profile) {
	g.Every = p.Segment
	g.Segment = int(int64(p.BitsPerSecond) * int64(p.Segment) / int64(time.Second) / 8)
	g.SegmentMS = int(p.Segment / 10 / time.Millisecond)
}

// Stalled says whether a segment asked for at start and finished at end came after the next
// one was due (start + every): the player would have stalled.
func Stalled(start, end time.Time, every time.Duration) bool { return end.Sub(start) > every }

// Failure is one failed or late request.
type Failure struct {
	At    time.Time `json:"at"`
	Slug  string    `json:"slug"`
	Error string    `json:"error"`
	Took  string    `json:"took"`
}

// Stats is what the generator saw.
type Stats struct {
	OK       int64     `json:"ok"`
	Failed   int64     `json:"failed"`
	Bytes    int64     `json:"bytes"`
	Stalls   int64     `json:"stalls"`
	TTFBP95  string    `json:"ttfb_p95"`
	Failures []Failure `json:"failures,omitempty"`
	// TTFB is the sample of times to first byte (not in the JSON).
	TTFB []time.Duration `json:"-"`
}

// StallRate is the share of segments that stalled or failed.
func (s Stats) StallRate() float64 {
	n := s.OK + s.Failed
	if n == 0 {
		return 0
	}
	return float64(s.Stalls+s.Failed) / float64(n)
}

func (g *Gen) defaults() {
	if g.Segment == 0 {
		g.Segment = 256 << 10
	}
	if g.Every == 0 {
		g.Every = time.Second
	}
	if g.SegmentMS == 0 {
		g.SegmentMS = 200
	}
	if g.Late == 0 {
		g.Late = 10 * time.Second
	}
	if g.HTTPClient == nil {
		g.HTTPClient = &http.Client{Transport: &http.Transport{
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 512,
		}}
	}
}

// Run plays until ctx ends.
func (g *Gen) Run(ctx context.Context) {
	g.defaults()
	var wg sync.WaitGroup
	for i := range g.Clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slug := g.Slugs[i%len(g.Slugs)]
			// spread the clients over the first interval
			if !sleep(ctx, time.Duration(i)*g.Every/time.Duration(max(g.Clients, 1))) {
				return
			}
			tk := time.NewTicker(g.Every)
			defer tk.Stop()
			for {
				g.segment(ctx, slug)
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
				}
			}
		}()
	}
	wg.Wait()
}

func (g *Gen) segment(ctx context.Context, slug string) {
	start := time.Now()
	rctx, cancel := context.WithTimeout(ctx, g.Late)
	defer cancel()
	n, first, err := GetTimed(rctx, g.HTTPClient, g.Edge, slug+"."+g.Domain, fmt.Sprintf("/stream?bytes=%d&ms=%d", g.Segment, g.SegmentMS))
	if ctx.Err() != nil {
		return // the run ended mid-request: not a failure
	}
	if first > 0 {
		g.mu.Lock()
		if len(g.ttfb) < 10000 {
			g.ttfb = append(g.ttfb, first)
		}
		g.mu.Unlock()
	}
	if err == nil && n != int64(g.Segment) {
		err = fmt.Errorf("short segment: %d of %d bytes", n, g.Segment)
	}
	if err != nil {
		g.failed.Add(1)
		g.mu.Lock()
		if len(g.failures) < 1000 {
			g.failures = append(g.failures, Failure{At: start, Slug: slug, Error: err.Error(), Took: time.Since(start).String()})
		}
		g.mu.Unlock()
		return
	}
	g.ok.Add(1)
	g.bytes.Add(n)
	if Stalled(start, time.Now(), g.Every) {
		g.stalls.Add(1)
	}
}

// Client is the generator's HTTP client (its default when none was given).
func (g *Gen) Client() *http.Client {
	g.defaults()
	return g.HTTPClient
}

// Stats snapshots the counters.
func (g *Gen) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	ttfb := append([]time.Duration(nil), g.ttfb...)
	return Stats{OK: g.ok.Load(), Failed: g.failed.Load(), Bytes: g.bytes.Load(), Stalls: g.stalls.Load(),
		TTFBP95: Percentile(ttfb, 0.95).String(), TTFB: ttfb, Failures: append([]Failure(nil), g.failures...)}
}

// GetTimed is Get with the time to the first byte of the body (0 when none came).
func GetTimed(ctx context.Context, c *http.Client, edge, host, path string) (int64, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, edge+path, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Host = host
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var first time.Duration
	buf := make([]byte, 32<<10)
	var n int64
	for {
		k, rerr := resp.Body.Read(buf)
		if k > 0 && first == 0 {
			first = time.Since(start)
		}
		n += int64(k)
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return n, first, rerr
		}
	}
	if resp.StatusCode != http.StatusOK {
		return n, first, fmt.Errorf("status %d", resp.StatusCode)
	}
	return n, first, nil
}

// Get requests path on host through the edge and reads the body; a status
// other than 200 is an error.
func Get(ctx context.Context, c *http.Client, edge, host, path string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, edge+path, nil)
	if err != nil {
		return 0, err
	}
	req.Host = host
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return n, err
	}
	if resp.StatusCode != http.StatusOK {
		return n, fmt.Errorf("status %d", resp.StatusCode)
	}
	return n, nil
}

// Probe asks host/path every Every and records the stretches it failed.
type Probe struct {
	Edge, Host, Path string
	Every            time.Duration // default 250 ms
	Client           *http.Client

	mu       sync.Mutex
	downFrom time.Time
	longest  time.Duration
	outages  int
	checks   int
}

// Run probes until ctx ends.
func (p *Probe) Run(ctx context.Context) {
	if p.Every == 0 {
		p.Every = 250 * time.Millisecond
	}
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 2 * time.Second}
	}
	tk := time.NewTicker(p.Every)
	defer tk.Stop()
	for {
		_, err := Get(ctx, p.Client, p.Edge, p.Host, p.Path)
		if ctx.Err() != nil {
			p.end(time.Now())
			return
		}
		p.observe(time.Now(), err == nil)
		select {
		case <-ctx.Done():
			p.end(time.Now())
			return
		case <-tk.C:
		}
	}
}

func (p *Probe) observe(at time.Time, up bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.checks++
	switch {
	case !up && p.downFrom.IsZero():
		p.downFrom = at
		p.outages++
	case up && !p.downFrom.IsZero():
		p.longest = max(p.longest, at.Sub(p.downFrom))
		p.downFrom = time.Time{}
	}
}

func (p *Probe) end(at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.downFrom.IsZero() {
		p.longest = max(p.longest, at.Sub(p.downFrom))
		p.downFrom = time.Time{}
	}
}

// Downtime is the longest stretch the probe failed, and how many outages.
func (p *Probe) Downtime() (time.Duration, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.longest
	if !p.downFrom.IsZero() {
		d = max(d, time.Since(p.downFrom))
	}
	return d, p.outages
}

// Percentiles of durations (p in 0..100).
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(float64(len(s)-1) * p / 100)
	return s[i]
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
