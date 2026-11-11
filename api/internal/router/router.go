// Package router constructs the API's HTTP request multiplexer.
// Story 7.1 lays the chassis; later stories mount their handlers via
// Mount.
//
// The middleware order in New is canonical and load-bearing:
//
//  1. ClientIP      — populates RemoteAddr from X-Forwarded-For, believed
//     only from loopback or a trusted proxy (shared/go/clientip),
//     so the rate limiter sees the right key.
//  2. RequestID     — mints / accepts the per-request UUID v7.
//  3. Recoverer     — turns panics into 500 problem+json before they
//     reach the logger; must precede the logger so the
//     panic is logged exactly once.
//  4. Tracing       — attaches a Trace to ctx; reads inbound traceparent.
//  5. SLogLogger    — one structured line per request, with the request
//     id baked in via shared/log/go.
//  6. Client        — reads X-Maktaba-Client into the context (the
//     app; a label, never a right).
//  7. Metrics       — records http_request_duration_seconds, the
//     in-flight gauge and http_requests_by_client_total.
//  8. Usage         — counts the request for the API's use per app
//     (api_usage_daily; Epic 44 story 44.5).
//  9. PerIP rate    — DoS guard, keyed on remote IP.
//  10. PerUser rate — application rate, keyed on user (falls back to IP
//     when no user is on ctx).
//     ComponentOff — 409 component_off on a route whose component is off
//     (Epic 51 story 51.09; from memory, no query).
//  11. SingleWriteGuard
//     — drops second WriteHeader/Write so a buggy handler
//     that double-writes still produces a coherent
//     response.
//
// 12. Idempotency   — replays cached responses for retried mutations.
//
// New does not mount auth (Epic 10) or business handlers (Stories 7.3+).
// Those land via Group + Mount in their respective packages.
package router

import (
	"database/sql"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/usage"
	errrpt "github.com/Hamza-Labs-Core/Maktaba/shared/errrpt/go"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
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

	// StatsDataDir / StatsDB back the /api/system/health stats block:
	// free space on the data volume (syscall.Statfs) and the live
	// processing-job count. Empty/nil leaves the corresponding stat
	// unwired (zero value), so a bare-Deps unit test is unaffected.
	StatsDataDir string
	StatsDB      *sql.DB

	// ErrorReporter is the shared errrpt surface the Recoverer uses to
	// report recovered panics (HLB-300 live path). nil keeps the
	// historical behaviour (plain recover + 500 + slog "panic" line, no
	// errrpt capture) so unit tests that build a bare Deps are
	// unaffected; main.go populates it from MAKTABA_ERROR_WEBHOOK.
	ErrorReporter *errrpt.Reporter

	// License is the entitlement store, published once main.go builds
	// it: the relay gate (Epic 16 story 16.1, free is LAN-only) refuses
	// what the cloud relay forwards unless the license grants
	// cloud_relay. Nil leaves the gate off (bare-Deps unit tests).
	License *subscriptions.StoreRef

	// Usage counts the API's use per app and day (Epic 44 story 44.5);
	// nil counts nothing (a bare-Deps unit test).
	Usage *usage.Counter

	// Capabilities is the home's capabilities snapshot (Epic 51 story 51.09): a route
	// whose component is off answers 409 component_off (ComponentRoutes). MountP51
	// starts it; nil leaves the check off (bare-Deps unit tests).
	Capabilities *components.Capabilities

	// TrustedProxies are the reverse proxies in front of the home ([server]
	// trusted_proxies, Epic 59 story 59.08) whose X-Forwarded-For is believed, besides
	// loopback (this machine's own proxies) which always is. Empty: loopback only.
	TrustedProxies []netip.Prefix
	// TrustedHop trusts addresses learned as the home runs, beside TrustedProxies: its
	// approved nodes, which forward their clients' requests (nil: none).
	TrustedHop func(netip.Addr) bool
}

// New builds the API's chi.Mux with the canonical middleware stack and
// the foundation routes. It does NOT mount business handlers — those
// are added by their packages calling Mount(r) where r is the returned
// router.
func New(d Deps) chi.Router {
	r := chi.NewRouter()

	// Epic 32 story 32.9: only the relay agent on loopback may mark a
	// request as coming through the relay; checked on the real peer
	// address, before the client address is resolved.
	r.Use(mw.StripVia)
	// the relay reaches people's routes only, never the machines' (via.go)
	r.Use(mw.RefuseRelayToMachines)
	if d.License != nil {
		r.Use(subscriptions.RelayGate(d.License))
	}
	// likewise a program on this machine (the desktop app asks /api/cluster/self over
	// loopback), judged on the real peer address
	r.Use(mw.MarkLocalCaller)
	// The client address (Epic 59 story 59.08): X-Forwarded-For (else X-Real-IP) is
	// believed only from loopback (the web port's proxy, the relay agent, direct access)
	// or a proxy in [server] trusted_proxies, walked right to left past the trusted hops;
	// from anyone else it is the client's own text and the peer address stands. The API
	// listens on the LAN, so chi's RealIP (which believed it from every peer) let a LAN
	// client pick its own address.
	r.Use(clientip.MiddlewareWith(d.TrustedProxies, d.TrustedHop))
	r.Use(mw.RequestID)
	// Recoverer carries the central HLB-300 error-report hook. With a
	// nil d.ErrorReporter this is exactly the old mw.Recoverer (no
	// errrpt capture), so the canonical chain order/behaviour is
	// unchanged when no webhook/reporter is configured.
	r.Use(mw.RecovererWithReporter(d.ErrorReporter))
	r.Use(tracing.HTTP)
	r.Use(mw.SLogLogger)
	r.Use(mw.Client)
	r.Use(mw.Metrics)
	r.Use(mw.Usage(d.Usage))

	if d.IPRatePerMin > 0 {
		r.Use(mw.PerIP(d.IPRatePerMin))
	}
	if d.UserRatePerMin > 0 {
		r.Use(mw.PerUser(d.UserRatePerMin))
	}

	// Epic 51 story 51.09: a route whose component is off is 409 component_off
	r.Use(ComponentOff(d.Capabilities))

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
			system.NewAggregatorWithStats(d.AggregatorServices, system.StatsConfig{
				DataDir: d.StatsDataDir,
				DB:      d.StatsDB,
			}))
		r.Method(http.MethodGet, "/api/system/version",
			system.VersionHandler(d.SchemaRev))
	})
}
