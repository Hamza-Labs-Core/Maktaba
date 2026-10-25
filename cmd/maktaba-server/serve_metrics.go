package main

import (
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/updater"
)

// metricsAdmin adds GET /metrics to the supervisor's loopback admin: its update metrics
// (story 35.15, internal/updater/metrics.go), from the updates folder dir. The API's
// /metrics merges them in, so a machine is one scrape target. Read-only and without
// secrets, as /processes beside it.
func metricsAdmin(base http.Handler, dir string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = updater.WriteMetrics(w, dir)
	})
	return mux
}
