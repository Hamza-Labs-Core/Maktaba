package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrOrchestratorUnavailable: the supervisor has no update orchestrator (story 35.4 not
// in this build, or the API is not running under maktaba-server).
var ErrOrchestratorUnavailable = errors.New("orchestrator_unavailable")

// Starter hands a planned run to the executor.
type Starter interface {
	Start(ctx context.Context, runID string) error
}

// SupervisorClient is the supervisor's loopback admin (MAKTABA_SUPERVISOR_URL):
// POST /update/start {run_id} (design "APIs", supervisor loopback; the server side is
// story 35.4).
type SupervisorClient struct {
	BaseURL string
	// Token is the supervisor's local token (MAKTABA_SUPERVISOR_TOKEN), sent as a bearer
	// when set.
	Token string
	HTTP  *http.Client
}

// Start asks the supervisor to execute runID. A missing supervisor, or one that does not
// know /update/start (404, 405, 501), is ErrOrchestratorUnavailable.
func (c *SupervisorClient) Start(ctx context.Context, runID string) error {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return fmt.Errorf("%w: not running under the maktaba-server supervisor", ErrOrchestratorUnavailable)
	}
	body, _ := json.Marshal(map[string]string{"run_id": runID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/update/start", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("supervisor unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed ||
		resp.StatusCode == http.StatusNotImplemented:
		return fmt.Errorf("%w: supervisor answered %d", ErrOrchestratorUnavailable, resp.StatusCode)
	}
	return fmt.Errorf("supervisor answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

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
