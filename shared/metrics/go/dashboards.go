package metrics

// Story 21.2 / 21.4 — dashboard + alert manifest.
//
// The repo ships the canonical Grafana dashboards and Prometheus alert
// rules as YAML / JSON in `deploy/observability/`. This file exposes
// them as Go strings so the API can serve them under `/api/observability/...`
// for the in-app "view dashboard" link, and so a regression test can
// assert they parse.
//
// Adding a new dashboard: drop the JSON in `deploy/observability/grafana/`
// and add a row to DashboardManifest; CI lints that every row has a
// matching file.

// Dashboard is one Grafana board.
type Dashboard struct {
	ID          string // stable id used by the API to map URL → file
	Title       string
	File        string // relative to deploy/observability/grafana/
	Description string
}

// DashboardManifest is the canonical list. Update this when adding a board.
var DashboardManifest = []Dashboard{
	{
		ID:          "api-latency",
		Title:       "API latency",
		File:        "api-latency.json",
		Description: "Per-endpoint p50/p95/p99, breach counts, in-flight requests.",
	},
	{
		ID:          "pipeline-throughput",
		Title:       "Pipeline throughput",
		File:        "pipeline-throughput.json",
		Description: "Scanner / transcoder / STT per-stage throughput + queue depth.",
	},
	{
		ID:          "streaming",
		Title:       "Streaming hot path",
		File:        "streaming.json",
		Description: "HLS manifest + segment hit rates, fan-out per session.",
	},
	{
		ID:          "errors",
		Title:       "Errors and 5xx",
		File:        "errors.json",
		Description: "Per-endpoint 5xx rate, error class breakdown, recent error_ids.",
	},
	{
		ID:          "scale-readiness",
		Title:       "Scale readiness",
		File:        "scale-readiness.json",
		Description: "Connection pool saturation, cache hit rates, replica count.",
	},
}

// AlertRule is one PrometheusRule block.
type AlertRule struct {
	Name       string
	Severity   string // page | warn | info
	Expression string
	ForSec     int
	Annotation string
}

// AlertManifest is the canonical alert set. Owned by Story 21.4 +
// Story 21.5. Each alert maps to a runbook under docs/runbooks/.
var AlertManifest = []AlertRule{
	{
		Name:       "ApiP95Breach",
		Severity:   "warn",
		Expression: `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket{job="api"}[5m])) > 1.0`,
		ForSec:     300,
		Annotation: "API p95 above 1s for 5m",
	},
	{
		Name:       "ApiErrorRateHigh",
		Severity:   "page",
		Expression: `sum(rate(http_requests_total{job="api",code=~"5.."}[5m])) / sum(rate(http_requests_total{job="api"}[5m])) > 0.05`,
		ForSec:     300,
		Annotation: "API 5xx rate > 5% for 5m",
	},
	{
		Name:       "PipelineQueueBacklog",
		Severity:   "warn",
		Expression: `processing_jobs_queue_depth > 1000`,
		ForSec:     600,
		Annotation: "Pipeline queue > 1000 for 10m",
	},
	{
		Name:       "DBPoolSaturated",
		Severity:   "page",
		Expression: `db_pool_in_use / db_pool_max_open > 0.9`,
		ForSec:     180,
		Annotation: "DB connection pool > 90% utilization for 3m",
	},
	{
		Name:       "DiskAlmostFull",
		Severity:   "warn",
		Expression: `node_filesystem_avail_bytes{mountpoint="/var/lib/maktaba"} / node_filesystem_size_bytes{mountpoint="/var/lib/maktaba"} < 0.10`,
		ForSec:     600,
		Annotation: "Maktaba data volume < 10% free for 10m",
	},
}

// DashboardByID returns the registered dashboard with the matching id,
// or nil.
func DashboardByID(id string) *Dashboard {
	for i, d := range DashboardManifest {
		if d.ID == id {
			return &DashboardManifest[i]
		}
	}
	return nil
}
