package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	metrics "github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go"
)

// statusClass maps an HTTP status to its `2xx`/`4xx`/`5xx`-style label.
// Bounded cardinality (Story 21.2 AC-2) — never emit the raw status as
// a label.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return strconv.Itoa(code/100) + "xx"
	}
}

// Metrics records http_request_duration_seconds and increments
// http_in_flight_requests for every request. The route_template label
// is read from chi's RouteContext after the handler runs; for routes
// chi can't classify (404 within a Group, error before Mount), we
// fall back to the literal "unknown" so cardinality stays bounded.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.HTTPInFlight.Inc()
		defer metrics.HTTPInFlight.Dec()

		sc := &statusCapture{ResponseWriter: w}
		t0 := time.Now()
		next.ServeHTTP(sc, r)

		route := "unknown"
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if rp := rc.RoutePattern(); rp != "" {
				route = rp
			}
		}
		metrics.HTTPRequestDuration.
			WithLabelValues(r.Method, route, statusClass(sc.status)).
			Observe(time.Since(t0).Seconds())
	})
}
