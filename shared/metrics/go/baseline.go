package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Baseline metrics registered at package init for every Go service.
// Story 21.2 AC-1 enumerates the surface; cross-service consumers
// (Grafana dashboards) rely on these names being identical between
// api / streaming / pipeline.
var (
	HTTPRequestDuration = NewLatencyHistogram(
		"http_request_duration",
		"HTTP request handler duration",
		[]string{"method", "route_template", "status_class"},
	)

	// HTTPRequestsByClient counts requests by the app that sent them
	// (X-Maktaba-Client, Epic 44 story 44.5). The app is a bounded label:
	// the known apps, `unknown`, else `other`. A counter of its own so the
	// latency histogram keeps its cardinality.
	HTTPRequestsByClient = func() *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_by_client_total",
			Help: "HTTP requests by the app that sent them and status class.",
		}, []string{"client_app", "status_class"})
		Reg().MustRegister(c)
		return c
	}()

	HTTPInFlight = func() prometheus.Gauge {
		g := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_in_flight_requests",
			Help: "In-flight HTTP requests.",
		})
		Reg().MustRegister(g)
		return g
	}()

	DBQueryDuration = NewLatencyHistogram(
		"db_query_duration",
		"Database query duration",
		[]string{"query_name"},
	)

	CacheHits = func() *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_hits_total",
			Help: "Cache hits per cache.",
		}, []string{"cache"})
		Reg().MustRegister(c)
		return c
	}()

	CacheMisses = func() *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cache_misses_total",
			Help: "Cache misses per cache.",
		}, []string{"cache"})
		Reg().MustRegister(c)
		return c
	}()

	PipelineJobs = func() *prometheus.CounterVec {
		c := prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pipeline_jobs_total",
			Help: "Pipeline jobs by stage and result.",
		}, []string{"stage", "result"})
		Reg().MustRegister(c)
		return c
	}()
)
