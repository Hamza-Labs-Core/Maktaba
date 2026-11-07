package plugins

// The plugins' job metrics on the home's /metrics (story 54.09 §4). The pipeline has no
// Prometheus endpoint, and every job's outcome is in plugin_health already (the breaker
// records it, 54.09 §2), so the API reads those rows at each scrape:
//
//   - maktaba_plugin_jobs_total{plugin,outcome}  the jobs that ended ok or failed (the
//     row's jobs_done and jobs_failed, counted since the row was made)
//   - maktaba_plugin_job_seconds{plugin,quantile} p50 and p95 of the job time over its
//     last 20 jobs (the row's recent)
//   - maktaba_plugin_memory_peak_bytes{plugin}   the highest memory high-water mark of
//     those jobs
//
// Only the home registers it: a joined machine reads the same rows, and would report them
// twice.

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	healthDB   atomic.Pointer[sql.DB]
	healthOnce sync.Once

	jobsDesc = prometheus.NewDesc("maktaba_plugin_jobs_total",
		"Plugin jobs that ended, by plugin and outcome (ok, failed), from plugin_health (Epic 54).",
		[]string{"plugin", "outcome"}, nil)
	jobSecondsDesc = prometheus.NewDesc("maktaba_plugin_job_seconds",
		"A plugin's job time over its last 20 jobs, by quantile (0.5, 0.95) (Epic 54).",
		[]string{"plugin", "quantile"}, nil)
	memoryDesc = prometheus.NewDesc("maktaba_plugin_memory_peak_bytes",
		"The highest memory high-water mark of a plugin's last 20 jobs, in bytes (Epic 54).",
		[]string{"plugin"}, nil)
)

// RegisterHealthMetrics exports the plugins' job metrics from db's plugin_health (the
// home's; a later call replaces the database, the collector is registered once).
func RegisterHealthMetrics(db *sql.DB) {
	if db == nil {
		return
	}
	healthDB.Store(db)
	healthOnce.Do(func() { _ = prometheus.DefaultRegisterer.Register(healthCollector{}) })
}

type healthCollector struct{}

func (healthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- jobsDesc
	ch <- jobSecondsDesc
	ch <- memoryDesc
}

func (healthCollector) Collect(ch chan<- prometheus.Metric) {
	db := healthDB.Load()
	if db == nil {
		return
	}
	for _, r := range ReadHealthMetrics(db) {
		ch <- prometheus.MustNewConstMetric(jobsDesc, prometheus.CounterValue, float64(r.Done), r.Plugin, "ok")
		ch <- prometheus.MustNewConstMetric(jobsDesc, prometheus.CounterValue, float64(r.Failed), r.Plugin, "failed")
		if r.Recent == 0 {
			continue
		}
		ch <- prometheus.MustNewConstMetric(jobSecondsDesc, prometheus.GaugeValue, r.P50, r.Plugin, "0.5")
		ch <- prometheus.MustNewConstMetric(jobSecondsDesc, prometheus.GaugeValue, r.P95, r.Plugin, "0.95")
		ch <- prometheus.MustNewConstMetric(memoryDesc, prometheus.GaugeValue, float64(r.MemoryPeak), r.Plugin)
	}
}

// HealthMetric is one plugin's numbers for /metrics.
type HealthMetric struct {
	Plugin       string
	Done, Failed int64
	// Recent is how many of its last jobs the times and memory are of.
	Recent     int
	P50, P95   float64 // seconds
	MemoryPeak int64   // bytes
}

// ReadHealthMetrics reads every plugin's numbers (none when the table cannot be read: a
// scrape never fails for it).
func ReadHealthMetrics(db *sql.DB) []HealthMetric {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT plugin_id, jobs_done, jobs_failed, recent FROM plugin_health ORDER BY plugin_id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []HealthMetric
	for rows.Next() {
		var m HealthMetric
		var raw []byte
		if rows.Scan(&m.Plugin, &m.Done, &m.Failed, &raw) != nil {
			continue
		}
		var recent []struct {
			MS     float64 `json:"ms"`
			Memory int64   `json:"memory"`
		}
		_ = json.Unmarshal(raw, &recent)
		secs := make([]float64, 0, len(recent))
		for _, e := range recent {
			secs = append(secs, e.MS/1000)
			m.MemoryPeak = max(m.MemoryPeak, e.Memory)
		}
		m.Recent = len(secs)
		if m.Recent > 0 {
			slices.Sort(secs)
			m.P50, m.P95 = quantile(secs, 0.5), quantile(secs, 0.95)
		}
		out = append(out, m)
	}
	return out
}

// quantile of sorted values, nearest rank.
func quantile(sorted []float64, q float64) float64 {
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}
