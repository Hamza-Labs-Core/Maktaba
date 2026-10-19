package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// CheckedLive is a liveness handler that also runs liveness checks
// (Story 22.9). Unlike readiness checks these must be cheap and must
// only fail when restarting the process is the cure — a wedged event
// loop or a lock nobody releases — never because a dependency is down.
// It answers 200 like Live when every check passes and 503 otherwise,
// which the process supervisor counts towards killing and restarting
// the process.
type CheckedLive struct {
	service string
	checks  []Check
}

// NewCheckedLive builds a CheckedLive. With no checks it behaves like
// Live.
func NewCheckedLive(service string, checks ...Check) *CheckedLive {
	if service == "" {
		service = "unknown"
	}
	return &CheckedLive{service: service, checks: checks}
}

func (l *CheckedLive) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	resp := readyResponse{Status: "ok", Service: l.service}
	if len(l.checks) > 0 {
		resp.Checks = make(map[string]checkResult, len(l.checks))
	}
	for _, c := range l.checks {
		ctx, cancel := context.WithTimeout(req.Context(), PerCheckTimeout)
		err := c.Run(ctx)
		cancel()
		if err != nil {
			resp.Status = "fail"
			resp.Checks[c.Name()] = checkResult{Status: "fail", Reason: err.Error()}
		} else {
			resp.Checks[c.Name()] = checkResult{Status: "ok"}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if resp.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// AdminMuxWithLiveness is AdminMux with liveness checks on /healthz.
func AdminMuxWithLiveness(service string, live, ready []Check, warmPeriod time.Duration) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/healthz", NewCheckedLive(service, live...))
	mux.Handle("/readyz", NewReady(service, ready, warmPeriod))
	return mux
}
