package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Ready is the readiness handler. ServeHTTP runs every registered Check
// in parallel under a cumulative 800 ms budget (plan §8) and returns
// 200 only when every check passes *and* the warm-up window has
// elapsed (TC3).
//
// JSON shape (Kubernetes-compatible, AC4 in spirit — kubelet just looks
// at the status code, but the body lets the aggregator surface
// per-check reasons):
//
//	{
//	  "status":  "ok" | "degraded" | "warming",
//	  "service": "api",
//	  "checks": {
//	    "db":       { "status": "ok" },
//	    "pipeline": { "status": "fail", "reason": "dial tcp: ..." }
//	  }
//	}
type Ready struct {
	service      string
	checks       []Check
	warmingUntil time.Time
	// budget caps the *total* time spent running all checks. Per-check
	// timeouts are derived inside ServeHTTP from PerCheckTimeout, but
	// the cumulative bound stops a slow check from dragging the probe
	// past the orchestrator's typical 1–2 s timeout.
	budget time.Duration
	now    func() time.Time
}

// NewReady builds a Ready handler. `warmPeriod` is plan TC3's 30 s
// "may return 503 with reason=warming during cold start" window; pass
// 0 to skip the warm-up gate (useful in unit tests).
func NewReady(service string, checks []Check, warmPeriod time.Duration) *Ready {
	if service == "" {
		service = "unknown"
	}
	r := &Ready{
		service: service,
		checks:  checks,
		budget:  800 * time.Millisecond,
		now:     time.Now,
	}
	if warmPeriod > 0 {
		r.warmingUntil = time.Now().Add(warmPeriod)
	}
	return r
}

// SetBudget overrides the default cumulative budget. Tests use this to
// shorten the upper bound; production callers should not need it.
func (r *Ready) SetBudget(d time.Duration) {
	if d > 0 {
		r.budget = d
	}
}

// checkResult is the per-dependency JSON sub-document. We keep `Reason`
// stringly-typed so tests asserting on it don't have to import a
// wrapper enum that adds nothing for human consumption.
type checkResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// readyResponse is the on-the-wire JSON envelope. Mirrored by the
// aggregator (api/internal/system) so a /api/system/health caller can
// unmarshal whichever shape it gets.
type readyResponse struct {
	Status  string                 `json:"status"`
	Service string                 `json:"service"`
	Checks  map[string]checkResult `json:"checks"`
}

func (r *Ready) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), r.budget)
	defer cancel()

	results := make(map[string]checkResult, len(r.checks))
	resp := readyResponse{Service: r.service, Checks: results}

	bad := false
	if !r.warmingUntil.IsZero() && r.now().Before(r.warmingUntil) {
		// Warm-up window: 503 + reason=warming, but still run the
		// checks so the body shows what's healthy. The probe never
		// blocks on warm-up — it just refuses the green light.
		resp.Status = "warming"
		bad = true
	}

	// Run every check in parallel under a per-check sub-deadline. A
	// dependency stalled for the full per-check budget can't push the
	// total past r.budget because of the parent ctx.
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, c := range r.checks {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, ccancel := context.WithTimeout(ctx, PerCheckTimeout)
			defer ccancel()
			err := c.Run(cctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[c.Name()] = checkResult{Status: "fail", Reason: err.Error()}
				bad = true
			} else {
				results[c.Name()] = checkResult{Status: "ok"}
			}
		}()
	}
	wg.Wait()

	if bad {
		if resp.Status == "" {
			resp.Status = "degraded"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		resp.Status = "ok"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// AdminMux returns a new ServeMux pre-wired with /healthz and /readyz.
// Convenience for the common case where a service exposes nothing else
// on its admin port — Story 21.2 will extend this with /metrics.
func AdminMux(service string, checks []Check, warmPeriod time.Duration) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/healthz", NewLive(service))
	mux.Handle("/readyz", NewReady(service, checks, warmPeriod))
	return mux
}
