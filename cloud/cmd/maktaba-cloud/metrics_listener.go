package main

import "net/http"

// privateMetrics is the handler of the private series' own listener (server.metrics_listen_addr,
// story 50.28): /metrics with every series, the broadcast ones (viewers, the cost estimate, the
// budget: business figures) included, and the role's own /readyz and /healthz, so the edge's
// health check on this port picks the same process its public site does.
//
// Which series a scrape gets is decided by the listener it reached, which the config confines
// to a loopback or private address (config.ValidateMetricsListener), never by anything in the
// request: a Host, like every header, is the client's to write. The main listener's /metrics
// answers the public series only.
func privateMetrics(metrics http.HandlerFunc, probes http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", metrics)
	mux.Handle("GET /readyz", probes)
	mux.Handle("GET /healthz", probes)
	return mux
}
