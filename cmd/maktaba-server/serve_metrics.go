package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// machineMetrics is this machine's supervisor's metrics: its update metrics (story 35.15,
// internal/updater/metrics.go), from the updates folder Dir, this machine's components
// (story 51.14, internal/supervisor/component_metrics.go; nil: none), and its memory,
// disk and load (story 53.12, Host; nil: none).
type machineMetrics struct {
	Dir   string
	Comps *supervisor.ComponentMetrics
	Host  *hostMetrics
}

// Write writes them in Prometheus's text format.
func (m *machineMetrics) Write(ctx context.Context, w io.Writer) {
	_ = updater.WriteMetrics(w, m.Dir)
	_ = m.Comps.Write(ctx, w)
	machine := store.Home
	if m.Comps != nil && m.Comps.Machine != "" {
		machine = m.Comps.Machine
	}
	m.Host.Write(w, machine)
}

// metricsCommand answers the home's "metrics" on the cluster channel (a joined machine):
// the same text as /metrics, for the home's dashboards to show each machine (story
// 53.12; api/node_metrics.go takes the series labelled with the machine). nil: this
// machine's supervisor has not built its metrics yet.
func metricsCommand(ctx context.Context, m *machineMetrics) (bool, any) {
	if m == nil {
		return false, "metrics not ready"
	}
	var b strings.Builder
	m.Write(ctx, &b)
	return true, b.String()
}

// metricsAdmin adds GET /metrics to the supervisor's loopback admin (machineMetrics). The
// API's /metrics merges them in, so a machine is one scrape target. Read-only and without
// secrets, as /processes beside it.
func metricsAdmin(base http.Handler, m *machineMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		m.Write(r.Context(), w)
	})
	return mux
}

// hostMetrics is this machine's memory, the free space of its data folder's disk and its
// load, for the Overview's "CPU, memory, disk free of the home and each machine" (Epic 53
// story 53.12). A reading the system does not give is left out, never written as 0.
type hostMetrics struct {
	DataDir string
	Total   func() uint64
	Free    func() uint64
	Disk    func(string) uint64
	Load    func() (float64, bool)
	CPUs    int
}

func newHostMetrics(dataDir string) *hostMetrics {
	return &hostMetrics{DataDir: dataDir, Total: hostinfo.TotalMemory, Free: hostinfo.FreeMemory,
		Disk: hostinfo.FreeDisk, Load: hostinfo.LoadAverage, CPUs: runtime.NumCPU()}
}

// Host metric names.
const (
	MetricHostMemory     = "maktaba_host_memory_bytes"
	MetricHostMemoryFree = "maktaba_host_memory_free_bytes"
	MetricHostDiskFree   = "maktaba_host_disk_free_bytes"
	MetricHostLoad       = "maktaba_host_load1"
	MetricHostCPUs       = "maktaba_host_cpus"
)

// Write writes the gauges, labelled with the machine.
func (h *hostMetrics) Write(w io.Writer, machine string) {
	if h == nil {
		return
	}
	gauge := func(name, help string, v float64) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s{machine=%q} %g\n", name, help, name, name, machine, v)
	}
	if h.Total != nil {
		if n := h.Total(); n > 0 {
			gauge(MetricHostMemory, "This machine's memory.", float64(n))
		}
	}
	if h.Free != nil {
		if n := h.Free(); n > 0 {
			gauge(MetricHostMemoryFree, "This machine's memory free or reclaimable.", float64(n))
		}
	}
	if h.Disk != nil && h.DataDir != "" {
		if n := h.Disk(h.DataDir); n > 0 {
			gauge(MetricHostDiskFree, "Free space on the disk that holds the data folder.", float64(n))
		}
	}
	if h.Load != nil {
		if v, ok := h.Load(); ok {
			gauge(MetricHostLoad, "The one-minute load average.", v)
		}
	}
	if h.CPUs > 0 {
		gauge(MetricHostCPUs, "This machine's logical CPUs.", float64(h.CPUs))
	}
}
