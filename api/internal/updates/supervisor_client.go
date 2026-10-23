package updates

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// HomeHealth is the "home is healthy" blocker (story 35.3 AC5): every supervised role
// running and no crash in the last 10 minutes, read from the supervisor's GET
// /processes.
type HomeHealth struct {
	BaseURL string
	HTTP    *http.Client
	Now     func() time.Time
}

// CrashQuiet is how long the home must have run without a crash before a run starts.
const CrashQuiet = 10 * time.Minute

// Blocked implements Blocker.
func (h *HomeHealth) Blocked(ctx context.Context) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(h.BaseURL), "/")
	if base == "" {
		return "", nil // not supervised: nothing to ask (the orchestrator check refuses later)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/processes", nil)
	if err != nil {
		return "", err
	}
	hc := h.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return BlockSupervisorUnreachable, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return BlockSupervisorUnreachable, nil
	}
	var snap struct {
		Processes []ProcState `json:"processes"`
		Events    []ProcEvent `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&snap); err != nil {
		return BlockSupervisorUnreachable, nil
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	return HomeBlocked(snap.Processes, snap.Events, now), nil
}

// ProcState is the part of a supervisor child row the health blocker reads.
type ProcState struct {
	Role      string `json:"role"`
	Instance  string `json:"instance"`
	Wanted    bool   `json:"wanted"`
	State     string `json:"state"`
	CrashLoop bool   `json:"crash_loop"`
}

// ProcEvent is the part of a supervisor event the health blocker reads.
type ProcEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"`
}

// HomeBlocked is the health rule on a supervisor snapshot: every role that should run
// is running (an on-demand process nobody asked for is ignored), none is crash
// looping, and no crash or hang in the last CrashQuiet.
func HomeBlocked(procs []ProcState, events []ProcEvent, now time.Time) string {
	for _, p := range procs {
		if p.Instance != "" && !p.Wanted && p.State != "running" {
			continue
		}
		if p.CrashLoop || p.State != "running" {
			return BlockRoleNotRunning
		}
	}
	for _, e := range events {
		if (e.Reason == "crash" || e.Reason == "hang") && now.Sub(e.At) < CrashQuiet {
			return BlockRecentCrash
		}
	}
	return ""
}
