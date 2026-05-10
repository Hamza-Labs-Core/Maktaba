// Package router constructs the API's HTTP request multiplexer.
// Story 7.1 lays the chassis; later stories mount their handlers via
// Mount.
//
// The middleware order in New is canonical and load-bearing:
//
//  1. RealIP        — populates RemoteAddr from X-Forwarded-For so the
//     rate limiter sees the right key.
//  2. RequestID     — mints / accepts the per-request UUID v7.
//  3. Recoverer     — turns panics into 500 problem+json before they
//     reach the logger; must precede the logger so the
//     panic is logged exactly once.
//  4. Tracing       — attaches a Trace to ctx; reads inbound traceparent.
//  5. SLogLogger    — one structured line per request, with the request
//     id baked in via shared/log/go.
//  6. Metrics       — records http_request_duration_seconds and
//     in-flight gauge.
//  7. PerIP rate    — DoS guard, keyed on remote IP.
//  8. PerUser rate  — application rate, keyed on user (falls back to IP
//     when no user is on ctx).
//  9. SingleWriteGuard
//     — drops second WriteHeader/Write so a buggy handler
//     that double-writes still produces a coherent
//     response.
//
// 10. Idempotency   — replays cached responses for retried mutations.
//
// New does not mount auth (Epic 10) or business handlers (Stories 7.3+).
// Those land via Group + Mount in their respective packages.
package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	tracing "github.com/Hamza-Labs-Core/Maktaba/shared/tracing/go"
)

// Deps bundles the runtime dependencies the router and its handlers
// need. Fields are populated by main.go from environment / config and
// passed in here so the router stays test-friendly (a unit test
// constructs a Deps with the in-memory store and no DB).
type Deps struct {
	IdempotencyStore idempotency.Store

	// IPRatePerMin / UserRatePerMin are the per-IP and per-user
	// rate-limit caps. Pass 0 to skip the corresponding middleware
	// (useful in tests).
	IPRatePerMin   int
	UserRatePerMin int

	// SchemaRev is the binary's expected schema revision; surfaced in
	// /api/system/version (Story 7.20 AC-2).
	SchemaRev int

	// AggregatorServices is the fan-out target list for
	// /api/system/health (Story 21.4).
	AggregatorServices []system.Service
}

// New builds the API's chi.Mux with the canonical middleware stack and
// the foundation routes. It does NOT mount business handlers — those
// are added by their packages calling Mount(r) where r is the returned
// router.
func New(d Deps) chi.Router {
	r := chi.NewRouter()

	r.Use(chimw.RealIP)
	r.Use(mw.RequestID)
	r.Use(mw.Recoverer)
	r.Use(tracing.HTTP)
	r.Use(mw.SLogLogger)
	r.Use(mw.Metrics)

	if d.IPRatePerMin > 0 {
		r.Use(mw.PerIP(d.IPRatePerMin))
	}
	if d.UserRatePerMin > 0 {
		r.Use(mw.PerUser(d.UserRatePerMin))
	}

	r.Use(mw.SingleWriteGuard)
	if d.IdempotencyStore != nil {
		r.Use(mw.Idempotency(d.IdempotencyStore))
	}

	// A 404/405 issued by chi itself bypasses the handler chain; the
	// custom NotFound/MethodNotAllowed render problem+json so clients
	// see the canonical error shape (AC-1).
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httperror.Write(w, req, httperror.NotFound("route not found"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httperror.Write(w, req, &httperror.Error{
			Type:   httperror.TypeMethodNotAllowed,
			Title:  "method not allowed",
			Status: http.StatusMethodNotAllowed,
			Detail: "route does not handle this method",
		})
	})

	mountSystemRoutes(r, d)
	return r
}

// mountSystemRoutes wires the public system endpoints. The aggregator
// /api/system/health is owned by api/internal/system; /api/system/version
// is owned here so build-time vars stay close to the version package.
func mountSystemRoutes(r chi.Router, d Deps) {
	r.Group(func(r chi.Router) {
		// JSON-only routes; the body cap is the global default but
		// individual routes can override via Group.With(BodyLimit).
		r.Use(mw.BodyLimit(mw.DefaultBodyLimit))

		r.Method(http.MethodGet, "/api/system/health",
			system.NewAggregator(d.AggregatorServices))
		r.Method(http.MethodGet, "/api/system/version",
			system.VersionHandler(d.SchemaRev))
	})
}
