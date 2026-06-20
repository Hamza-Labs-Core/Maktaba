package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Collector is the in-memory core of relay metrics collection (Story
// 30.1). It keeps two consistent views, updated in the same Record* call
// (README D7):
//
//   - process-cumulative atomics for the Prometheus `/metrics` endpoint
//     (monotonic counters + last-sampled gauges), and
//   - a reset-on-flush delta accumulator keyed (metric, country) that the
//     Runner snapshots into per-minute relay_metrics_raw rows.
//
// It is safe for concurrent use: Record* run on the proxy hot path and
// Snapshot runs on the flush goroutine.
type Collector struct {
	now func() time.Time

	// Prometheus-facing cumulative counters (never reset).
	cBwIn, cBwOut, cReq, cPushOK, cPushErr atomic.Int64
	// Last-sampled gauges.
	gServers, gTunnels atomic.Int64

	mu    sync.Mutex
	delta map[key]*acc
}

type key struct {
	metric  string
	country string
}

type acc struct {
	value   int64
	samples int
}

// RawRow is one per-minute aggregate destined for relay_metrics_raw.
type RawRow struct {
	Bucket  time.Time
	Metric  string
	Country string
	Value   int64
	Samples int
}

// NewCollector returns a ready collector. now defaults to time.Now UTC.
func NewCollector() *Collector {
	return &Collector{
		now:   func() time.Time { return time.Now().UTC() },
		delta: make(map[key]*acc),
	}
}

func (c *Collector) add(metric, country string, value int64, sampleInc int) {
	c.mu.Lock()
	k := key{metric: metric, country: country}
	a, ok := c.delta[k]
	if !ok {
		a = &acc{}
		c.delta[k] = a
	}
	a.value += value
	a.samples += sampleInc
	c.mu.Unlock()
}

// RecordBandwidth accumulates proxied bytes (in = client→server request,
// out = server→client response).
func (c *Collector) RecordBandwidth(in, out int64) {
	if in != 0 {
		c.cBwIn.Add(in)
		c.add(MetricBandwidthIn, "", in, 0)
	}
	if out != 0 {
		c.cBwOut.Add(out)
		c.add(MetricBandwidthOut, "", out, 0)
	}
}

// RecordRequest counts one proxied request, tagged by the edge-derived
// country code ("" when unknown). This is the only country-dimensioned
// metric.
func (c *Collector) RecordRequest(country string) {
	c.cReq.Add(1)
	c.add(MetricRequests, country, 1, 0)
}

// RecordPush counts a push-delivery outcome.
func (c *Collector) RecordPush(ok bool) {
	if ok {
		c.cPushOK.Add(1)
		c.add(MetricPushSent, "", 1, 0)
	} else {
		c.cPushErr.Add(1)
		c.add(MetricPushFailed, "", 1, 0)
	}
}

// ObserveConnections records a gauge sample of the live registry. Each
// sample contributes value+1 observation so the hourly average is
// sum_value/samples.
func (c *Collector) ObserveConnections(servers, tunnels int) {
	c.gServers.Store(int64(servers))
	c.gTunnels.Store(int64(tunnels))
	c.add(MetricConnectedServers, "", int64(servers), 1)
	c.add(MetricActiveTunnels, "", int64(tunnels), 1)
}

// Snapshot copies and resets the delta accumulator, stamping each row
// with the current minute bucket. Returns nil when nothing accumulated.
func (c *Collector) Snapshot() []RawRow {
	bucket := c.now().Truncate(time.Minute)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.delta) == 0 {
		return nil
	}
	rows := make([]RawRow, 0, len(c.delta))
	for k, a := range c.delta {
		rows = append(rows, RawRow{
			Bucket:  bucket,
			Metric:  k.metric,
			Country: k.country,
			Value:   a.value,
			Samples: a.samples,
		})
	}
	c.delta = make(map[key]*acc)
	return rows
}

// PromSnapshot is the live cumulative view for Prometheus exposition.
type PromSnapshot struct {
	ConnectedServers int64
	ActiveTunnels    int64
	BandwidthIn      int64
	BandwidthOut     int64
	Requests         int64
	PushSent         int64
	PushFailed       int64
}

// PromSnapshot reads the cumulative atomics and last-sampled gauges.
func (c *Collector) PromSnapshot() PromSnapshot {
	return PromSnapshot{
		ConnectedServers: c.gServers.Load(),
		ActiveTunnels:    c.gTunnels.Load(),
		BandwidthIn:      c.cBwIn.Load(),
		BandwidthOut:     c.cBwOut.Load(),
		Requests:         c.cReq.Load(),
		PushSent:         c.cPushOK.Load(),
		PushFailed:       c.cPushErr.Load(),
	}
}

// Live returns the last-sampled connection gauges for the dashboard
// overview when the collector is co-located with the reader.
func (c *Collector) Live() (servers, tunnels int) {
	return int(c.gServers.Load()), int(c.gTunnels.Load())
}
