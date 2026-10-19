// Package tvmetrics is the streaming service's Prometheus metrics for Maktaba
// TV across the cluster (Epic 32 story 32.11, design §10), served as
// Prometheus text on the admin port's /metrics. The streaming module keeps no
// Prometheus client dependency: these few counters and gauges are written in
// the text exposition format directly.
//
// Labels are bounded and never carry a user name or an IP address: host ids,
// roles, encoders, placement reasons, kinds and error codes only.
package tvmetrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Metric names (design §10).
const (
	HostUp           = "maktaba_tv_host_up"
	HostCapacity     = "maktaba_tv_host_capacity"
	HostUsed         = "maktaba_tv_host_used"
	ChannelsOnAir    = "maktaba_tv_channels_on_air"
	PlacementsTotal  = "maktaba_tv_placements_total"
	FailoversTotal   = "maktaba_tv_failovers_total"
	FrontdoorBytes   = "maktaba_tv_frontdoor_bytes_total"
	FrontdoorErrors  = "maktaba_tv_frontdoor_errors_total"
	RelayPushErrors  = "maktaba_tv_remote_push_errors_total"
	contentTypeValue = "text/plain; version=0.0.4; charset=utf-8"
)

// Vec is a counter or gauge with fixed label names.
type Vec struct {
	name, help, kind string
	labels           []string
	mu               sync.Mutex
	vals             map[string]float64 // joined label values -> value
}

func newVec(kind, name, help string, labels ...string) *Vec {
	v := &Vec{name: name, help: help, kind: kind, labels: labels, vals: map[string]float64{}}
	register(v)
	return v
}

func key(vals []string) string { return strings.Join(vals, "\x00") }

// Add adds d to the series with these label values (counters).
func (v *Vec) Add(d float64, vals ...string) {
	if len(vals) != len(v.labels) {
		return
	}
	v.mu.Lock()
	v.vals[key(vals)] += d
	v.mu.Unlock()
}

// Inc adds one.
func (v *Vec) Inc(vals ...string) { v.Add(1, vals...) }

// Set sets the series with these label values (gauges).
func (v *Vec) Set(x float64, vals ...string) {
	if len(vals) != len(v.labels) {
		return
	}
	v.mu.Lock()
	v.vals[key(vals)] = x
	v.mu.Unlock()
}

// Get reads a series (tests).
func (v *Vec) Get(vals ...string) float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.vals[key(vals)]
}

// Name is the metric name.
func (v *Vec) Name() string { return v.name }

// Labels are the label names.
func (v *Vec) Labels() []string { return append([]string(nil), v.labels...) }

func (v *Vec) write(w io.Writer) {
	v.mu.Lock()
	keys := make([]string, 0, len(v.vals))
	for k := range v.vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", v.name, v.help, v.name, v.kind)
	for _, k := range keys {
		writeSample(w, v.name, v.labels, strings.Split(k, "\x00"), v.vals[k])
	}
	v.mu.Unlock()
}

// Gauge is one scrape-time gauge sample.
type Gauge struct {
	Labels []string
	Value  float64
}

// GaugeFamily is a gauge whose samples are read at scrape time.
type GaugeFamily struct {
	Name, Help string
	LabelNames []string
	Samples    []Gauge
}

// Collector returns scrape-time gauge families (hosts and channels on air).
type Collector func() []GaugeFamily

var (
	regMu      sync.Mutex
	vecs       []*Vec
	collectors []Collector
)

func register(v *Vec) {
	regMu.Lock()
	vecs = append(vecs, v)
	regMu.Unlock()
}

// Register adds a scrape-time collector.
func Register(c Collector) {
	regMu.Lock()
	collectors = append(collectors, c)
	regMu.Unlock()
}

// The counters (incremented where the event happens).
var (
	Placements = newVec("counter", PlacementsTotal, "Channel placements by reason.", "reason")
	Failovers  = newVec("counter", FailoversTotal, "Channels moved off a host that went down.", "from_host")
	Bytes      = newVec("counter", FrontdoorBytes, "Bytes the front door forwarded from streaming nodes.", "kind", "host")
	Errors     = newVec("counter", FrontdoorErrors, "Front-door requests to a node that failed.", "host", "kind")
	PushErrors = newVec("counter", RelayPushErrors, "Push relay errors by code.", "code")
	// Epic 33 stories 33.7-33.9 (design §15): the swap in the front door.
	Swaps         = newVec("counter", "maktaba_tv_swaps_total", "Swap and rollback operations the front door ran, by kind and result.", "kind", "result")
	ChannelSwitch = newVec("counter", "maktaba_tv_channel_switches_total", "Channel switches of a swap or rollback, by result.", "kind", "result")
	TSSplices     = newVec("counter", "maktaba_tv_ts_splices_total", "TS connections spliced onto another slot, by mode (seamless|retimed).", "mode")
	HLSStitches   = newVec("counter", "maktaba_tv_hls_stitches_total", "HLS playlists stitched onto another slot, by reason.", "reason")
	Rollbacks     = newVec("counter", "maktaba_tv_rollbacks_total", "Rollbacks by reason.", "reason")
	SwitchLatency = newVec("gauge", "maktaba_tv_channel_switch_latency_seconds", "The last channel switch's delay after its planned instant.", "kind")
)

func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(s)
}

func writeSample(w io.Writer, name string, labels, vals []string, v float64) {
	var b strings.Builder
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				b.WriteByte(',')
			}
			val := ""
			if i < len(vals) {
				val = vals[i]
			}
			b.WriteString(l + `="` + escape(val) + `"`)
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	b.WriteByte('\n')
	_, _ = io.WriteString(w, b.String())
}

// Write writes every metric in the Prometheus text format.
func Write(w io.Writer) {
	regMu.Lock()
	vs := append([]*Vec(nil), vecs...)
	cs := append([]Collector(nil), collectors...)
	regMu.Unlock()
	for _, c := range cs {
		for _, f := range c() {
			fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", f.Name, f.Help, f.Name)
			for _, s := range f.Samples {
				writeSample(w, f.Name, f.LabelNames, s.Labels, s.Value)
			}
		}
	}
	for _, v := range vs {
		v.write(w)
	}
}

// Handler serves /metrics.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentTypeValue)
		Write(w)
	})
}
