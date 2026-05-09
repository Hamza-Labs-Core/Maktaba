// Package system implements the cross-service health aggregator
// (Story 21.4 AC3) exposed at GET /api/system/health on the API's main
// port.
//
// The endpoint is a thin fan-out: it probes every configured service's
// /readyz over the internal network with a 1 s per-call timeout and
// derives a roll-up status (ok / degraded / down). This is what the
// web admin panel renders so an operator can see at a glance which
// component is unhappy.
//
// Per-service /readyz handlers live in shared/health/go; this file
// only knows the aggregator endpoint shape and the roll-up math.
package system

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Service is one row in the aggregator's fan-out. Name is the key the
// UI shows; URL is the per-service /readyz endpoint (typically on the
// admin port — http://api:9100/readyz, http://streaming:9101/readyz).
type Service struct {
	Name string
	URL  string
}

// Snapshot is the per-service entry in /api/system/health's response.
// Mirrors the shape returned by the upstream /readyz so the UI doesn't
// need to learn two formats.
type Snapshot struct {
	Status string                 `json:"status"`
	Checks map[string]CheckStatus `json:"checks,omitempty"`
	Reason string                 `json:"reason,omitempty"`
}

// CheckStatus is one dependency entry inside a Snapshot.
type CheckStatus struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// Health is the aggregator's response envelope. Stats fields
// (DiskFreeBytes, QueueDepth, BudgetUSDLeft) are placeholders until the
// stories that own them land — Story 21.7 wires the queue depth, Story
// 19.7 the transcribe budget. Until then we surface zero values so the
// UI can render a stable JSON shape.
type Health struct {
	Status        string              `json:"status"`
	Services      map[string]Snapshot `json:"services"`
	DiskFreeBytes uint64              `json:"disk_free_bytes,omitempty"`
	QueueDepth    int                 `json:"queue_depth,omitempty"`
	BudgetUSDLeft string              `json:"transcribe_budget_usd_left,omitempty"`
}

// Aggregator probes a fixed set of services. It is safe to share
// across concurrent requests; the http.Client is the only stateful
// field and Go's net/http is goroutine-safe.
type Aggregator struct {
	services []Service
	client   *http.Client
	timeout  time.Duration
}

// NewAggregator builds an Aggregator. The default per-service timeout
// is 1 s (plan §0). A nil services slice yields a handler that
// reports the API itself only — Story 21.4 AC3 is satisfied as more
// services are wired in via configuration in later stories.
func NewAggregator(services []Service) *Aggregator {
	return &Aggregator{
		services: services,
		client:   &http.Client{Timeout: time.Second},
		timeout:  time.Second,
	}
}

// ServeHTTP implements http.Handler. It probes every configured
// service in parallel under a per-service timeout and writes the
// aggregated JSON.
//
// Status codes:
//
//	200  every service reports ok.
//	200  but Status == "degraded" — at least one service is unhappy
//	     but at least one is fine. The orchestrator should not restart
//	     the API based on this; the page renders banners instead.
//	200  but Status == "down"     — every probed service is unreachable.
//	     We still return 200 because the *aggregator itself* is alive
//	     and the response body carries the diagnostic. The UI decides
//	     what to render.
func (a *Aggregator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	res := Health{Services: map[string]Snapshot{}}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for _, s := range a.services {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), a.timeout)
			defer cancel()
			snap := a.probe(ctx, s)
			mu.Lock()
			res.Services[s.Name] = snap
			mu.Unlock()
		}()
	}
	wg.Wait()

	res.Status = deriveStatus(res.Services)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

// probe issues a GET against the service's /readyz and translates the
// result into a Snapshot. A network failure or non-2xx, non-503 yields
// status=down with the underlying reason; 503 yields the body's
// per-check breakdown so the UI can surface "pipeline.grpc_unavailable"
// per TC2.
func (a *Aggregator) probe(ctx context.Context, s Service) Snapshot {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return Snapshot{Status: "down", Reason: err.Error()}
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return Snapshot{Status: "down", Reason: err.Error()}
	}
	defer resp.Body.Close()

	// 200 → upstream is fully ready. 503 → upstream is reachable but a
	// dependency is failing; copy through the per-check map. Anything
	// else means the admin port is serving something unexpected, which
	// we treat as down.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusServiceUnavailable:
		var body struct {
			Status string                 `json:"status"`
			Checks map[string]CheckStatus `json:"checks"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return Snapshot{Status: "down", Reason: "invalid /readyz body: " + err.Error()}
		}
		// /readyz uses "ok" / "degraded" / "warming"; map "warming" to
		// "degraded" at the aggregator level so the UI doesn't have to
		// know about the cold-start window.
		st := body.Status
		if st == "warming" {
			st = "degraded"
		}
		return Snapshot{Status: st, Checks: body.Checks}
	default:
		return Snapshot{Status: "down", Reason: http.StatusText(resp.StatusCode)}
	}
}

// deriveStatus reduces the per-service map to a single roll-up.
//
//	0 services configured     → "ok"     (nothing to aggregate yet)
//	all services ok           → "ok"
//	all services down         → "down"   (every dependency unreachable)
//	otherwise                 → "degraded"
func deriveStatus(services map[string]Snapshot) string {
	if len(services) == 0 {
		return "ok"
	}
	bad, down := 0, 0
	for _, v := range services {
		switch v.Status {
		case "ok":
			// healthy
		case "down":
			bad++
			down++
		default:
			bad++
		}
	}
	switch {
	case bad == 0:
		return "ok"
	case down == len(services):
		return "down"
	default:
		return "degraded"
	}
}
