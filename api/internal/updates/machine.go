package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// MachineJob is what a machine is ordered to install (the supervisor's updater.Job).
type MachineJob struct {
	RunID         string            `json:"run_id"`
	Version       string            `json:"version"`
	Tag           string            `json:"tag"`
	Assets        map[string]string `json:"assets"`
	Home          bool              `json:"home,omitempty"`
	Rehearse      bool              `json:"rehearse,omitempty"`
	RehearseMaxGB int               `json:"rehearse_max_db_gb,omitempty"`
	KeepVersions  int               `json:"keep_versions,omitempty"`
	BackupsKeep   int               `json:"backups_keep,omitempty"`
	WatchSeconds  int               `json:"watch_seconds,omitempty"`
	HealthSeconds int               `json:"health_seconds,omitempty"`
	// AppPostgresMajor is the release's app payloads' PostgreSQL majors (its verified
	// release.json's): a machine refuses another major than its own before downloading
	AppPostgresMajor map[string]int `json:"app_postgres_major,omitempty"`
}

// MachineStep is one step of a machine's run.
type MachineStep struct {
	Step       string     `json:"step"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

// MachineError is why a machine's run failed.
type MachineError struct {
	Step   string `json:"step"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// MachineStatus is a machine's report of its run (the supervisor's updater.Status).
type MachineStatus struct {
	Job       *MachineJob     `json:"job,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	From      string          `json:"from,omitempty"`
	State     string          `json:"state,omitempty"`
	Step      string          `json:"step,omitempty"`
	Steps     []MachineStep   `json:"steps,omitempty"`
	Error     *MachineError   `json:"error,omitempty"`
	Rehearsal json.RawMessage `json:"rehearsal,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Running   string          `json:"running,omitempty"`
	Installer string          `json:"installer,omitempty"`
	// Asks: a desktop app that runs the server, which installs only once someone clicks
	// on it (owner decision D1); App is its answer to the order (ordered, staged, clicked).
	Asks bool   `json:"asks,omitempty"`
	App  string `json:"app,omitempty"`
	// Pending: the release that came up is busy with a start step (the database step, the
	// Python environment): its watch waits, and so does the run's timeout.
	Pending string `json:"pending,omitempty"`
	// BoundSec is how long the machine's own limits let the run go on from its switch (its
	// start steps' cap, from its database's size, and its watch's bound).
	BoundSec int `json:"bound_sec,omitempty"`
	// Attempts are the machine's update attempts (the desktop app's own, its releases'
	// starts), every failure with its reason, newest first.
	Attempts []Attempt `json:"attempts,omitempty"`
	// Backup is the dump the home's new release took before its migrations (at its commit).
	Backup *RunBackup `json:"backup,omitempty"`
}

// RunBackup is a dump of the home's database a run took (update_runs.backup).
type RunBackup struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// RunID is the run the report is of ("" none).
func (s *MachineStatus) RunID() string {
	if s == nil || s.Job == nil {
		return ""
	}
	return s.Job.RunID
}

// Machine states as the supervisor names them (internal/updater).
const (
	MPreparing     = "preparing"
	MPrepared      = "prepared"
	MPrepareFailed = "prepare_failed"
	MAborted       = "aborted"
	MAwaitingClick = "awaiting_click" // ordered from the app: it waits for a click there
	MClicked       = "clicked"        // someone clicked: it waits for the home's go
	MApplying      = "applying"
	MRestarting    = "restarting"
	MWatching      = "watching"
	MCommitted     = "committed"
	MRollingBack   = "rolling_back"
	MRolledBack    = "rolled_back"
	MApplyFailed   = "apply_failed"
	MFailed        = "failed"
)

// Starter hands a planned run to whoever runs it (the Driver).
type Starter interface {
	Start(ctx context.Context, runID string) error
}

// HomeSupervisor is the home's own supervisor's update endpoints.
type HomeSupervisor interface {
	// Do posts op (prepare, apply, abort, rollback) or reads status; the machine's
	// status, and an error when it refused.
	Do(ctx context.Context, op string, body any) (*MachineStatus, error)
	// Log is a run's log.
	Log(ctx context.Context, runID string) ([]byte, error)
}

// ErrOrchestratorUnavailable: the API runs under no supervisor that updates (a build
// without it, or `maktaba-api serve` on its own).
var ErrOrchestratorUnavailable = errors.New("orchestrator_unavailable")

// SupervisorClient is the supervisor's loopback admin (MAKTABA_SUPERVISOR_URL, the
// per-boot token in MAKTABA_SUPERVISOR_TOKEN_FILE).
type SupervisorClient struct {
	BaseURL   string
	TokenFile string
	HTTP      *http.Client
}

func (c *SupervisorClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *SupervisorClient) token() string {
	if c.TokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Do implements HomeSupervisor.
func (c *SupervisorClient) Do(ctx context.Context, op string, body any) (*MachineStatus, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: not running under the maktaba-server supervisor", ErrOrchestratorUnavailable)
	}
	method, rd := http.MethodGet, io.Reader(nil)
	if op != "status" {
		b, _ := json.Marshal(body)
		method, rd = http.MethodPost, bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+"/update/"+op, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("supervisor unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return nil, fmt.Errorf("%w: supervisor answered %d", ErrOrchestratorUnavailable, resp.StatusCode)
	}
	var doc struct {
		Status *MachineStatus `json:"status"`
		Error  string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("supervisor answered %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if doc.Error != "" || resp.StatusCode >= 300 {
		if doc.Error == "" {
			doc.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return doc.Status, errors.New(doc.Error)
	}
	return doc.Status, nil
}

// Log implements HomeSupervisor.
func (c *SupervisorClient) Log(ctx context.Context, runID string) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return nil, ErrOrchestratorUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/update/log?run="+runID, nil)
	if err != nil {
		return nil, err
	}
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("supervisor answered %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Cluster is the cluster channel as the driver uses it (agent.Hub, adapted in the
// router): the machines' agents, their last update.state, the home's own workers, and
// commands.
type Cluster interface {
	// MachineAgents are the connected machine agents' sessions, by node id.
	MachineAgents() map[string]string
	// MachineStates are their last update.state frames, by node id.
	MachineStates() map[string]json.RawMessage
	// HomeProcesses are the home's own worker sessions.
	HomeProcesses() []string
	// Command sends a command to a session and waits for its answer.
	Command(ctx context.Context, sessionID, name string, args any, wait time.Duration) (ok bool, detail json.RawMessage, err error)
}
