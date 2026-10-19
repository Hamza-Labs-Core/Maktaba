package supervisor

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// State is a child's lifecycle state as reported by the supervisor.
type State string

const (
	// StateStarting: started, its liveness probe has not answered yet.
	StateStarting State = "starting"
	// StateRunning: up, and its last probe (if any) answered.
	StateRunning State = "running"
	// StateUnhealthy: up, but its liveness probe is failing; it is killed
	// after probe_failures failures in a row.
	StateUnhealthy State = "unhealthy"
	// StateBackoff: exited, waiting to be restarted.
	StateBackoff State = "backoff"
	// StateFailed: crash-looping (max_restarts inside the window); the
	// others keep running and it is retried every failed_retry.
	StateFailed State = "failed"
	// StateExited: exited and left stopped by the restart policy.
	StateExited State = "exited"
	// StateStopped: stopped by a supervisor shutdown.
	StateStopped State = "stopped"
)

// Exit reasons recorded in ExitInfo.Reason and Event.Reason.
const (
	ReasonCrash       = "crash"        // exited non-zero or by a signal
	ReasonClean       = "clean"        // exited 0 on its own
	ReasonHang        = "hang"         // killed after failing its liveness probe
	ReasonStartFailed = "start_failed" // could not be started at all
	ReasonShutdown    = "shutdown"     // stopped by the supervisor's shutdown
	ReasonStopped     = "stopped"      // stopped on request (an on-demand process)
)

// Event kinds.
const (
	EventStarted    = "started"
	EventExited     = "exited"
	EventRestarting = "restarting"
	EventFailed     = "failed"
	EventHung       = "hung"
)

// ExitInfo describes how a child's last run ended.
type ExitInfo struct {
	At        time.Time `json:"at"`
	PID       int       `json:"pid,omitempty"`
	Code      int       `json:"code"` // -1 when killed by a signal or never started
	Signal    string    `json:"signal,omitempty"`
	Reason    string    `json:"reason"`
	Error     string    `json:"error,omitempty"`
	UptimeSec float64   `json:"uptime_sec"`
}

// ProbeInfo is the most recent liveness probe of a child.
type ProbeInfo struct {
	At       time.Time `json:"at"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Failures int       `json:"consecutive_failures"`
}

// ChildStatus is one child's row in a Snapshot.
type ChildStatus struct {
	Role          Role       `json:"role"`
	Instance      string     `json:"instance,omitempty"`
	// Wanted: an on-demand process asked for (tv-slot amber; a stopped standby).
	Wanted        bool       `json:"wanted"`
	State         State      `json:"state"`
	PID           int        `json:"pid,omitempty"`
	Restarts      int        `json:"restarts"`
	CrashLoop     bool       `json:"crash_loop"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	UptimeSec     float64    `json:"uptime_sec"`
	LastExit      *ExitInfo  `json:"last_exit,omitempty"`
	LastRestartAt *time.Time `json:"last_restart_at,omitempty"`
	NextRestartAt *time.Time `json:"next_restart_at,omitempty"`
	LastProbe     *ProbeInfo `json:"last_probe,omitempty"`
	ProbeURL      string     `json:"probe_url,omitempty"`
}

// Event is one entry of the supervisor's event log (start, exit,
// restart, crash-loop, hang).
type Event struct {
	Seq       int64     `json:"seq"`
	At        time.Time `json:"at"`
	Role      Role      `json:"role"`
	Instance  string    `json:"instance,omitempty"`
	Kind      string    `json:"kind"`
	PID       int       `json:"pid,omitempty"`
	Code      *int      `json:"code,omitempty"`
	Signal    string    `json:"signal,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	UptimeSec float64   `json:"uptime_sec,omitempty"`
	DelaySec  float64   `json:"delay_sec,omitempty"`
	Message   string    `json:"message,omitempty"`
}

// PolicyView is the policy as reported in a Snapshot.
type PolicyView struct {
	Restart           RestartMode `json:"restart"`
	BackoffInitialSec float64     `json:"backoff_initial_sec"`
	BackoffMaxSec     float64     `json:"backoff_max_sec"`
	MaxRestarts       int         `json:"max_restarts"`
	WindowSec         float64     `json:"max_restarts_window_sec"`
	ProbeIntervalSec  float64     `json:"probe_interval_sec"`
	ProbeFailures     int         `json:"probe_failures"`
}

// Snapshot is the supervisor's state, served at GET /processes.
type Snapshot struct {
	Host      string        `json:"host"`
	StartedAt time.Time     `json:"started_at"`
	Policy    PolicyView    `json:"policy"`
	Processes []ChildStatus `json:"processes"`
	Events    []Event       `json:"events"`
}

// snapshotEvents is how many recent events a Snapshot carries.
const snapshotEvents = 50

// Snapshot returns a copy of the current state.
func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	out := Snapshot{
		Host:      s.host,
		StartedAt: s.started,
		Policy: PolicyView{
			Restart:           s.policy.Restart,
			BackoffInitialSec: s.policy.BackoffInitial.Seconds(),
			BackoffMaxSec:     s.policy.BackoffMax.Seconds(),
			MaxRestarts:       s.policy.MaxRestarts,
			WindowSec:         s.policy.Window.Seconds(),
			ProbeIntervalSec:  s.policy.ProbeInterval.Seconds(),
			ProbeFailures:     s.policy.ProbeFailures,
		},
		Processes: make([]ChildStatus, 0, len(s.children)),
	}
	for _, c := range s.children {
		st := c.status
		if st.PID > 0 && st.StartedAt != nil {
			st.UptimeSec = now.Sub(*st.StartedAt).Seconds()
		}
		// A child retried out of a crash loop that has stayed up for
		// healthy_after is no longer looping.
		st.CrashLoop = c.failed && !(st.PID > 0 && st.UptimeSec >= s.policy.HealthyAfter.Seconds())
		st.LastExit = copyPtr(st.LastExit)
		st.LastProbe = copyPtr(st.LastProbe)
		out.Processes = append(out.Processes, st)
	}
	from := 0
	if len(s.events) > snapshotEvents {
		from = len(s.events) - snapshotEvents
	}
	out.Events = append([]Event{}, s.events[from:]...)
	return out
}

func copyPtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Handler serves the supervisor's admin surface: GET /processes (the
// Snapshot as JSON) and GET /healthz. Bind it on loopback only.
func (s *Supervisor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/processes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})
	// POST /slots/{instance}/start|stop: the TV front door asks for the
	// standby or the staging slot (Epic 33 story 33.1).
	mux.HandleFunc("/slots/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/slots/"), "/"), "/")
		if len(parts) != 2 || (parts[1] != "start" && parts[1] != "stop") {
			http.Error(w, "POST /slots/{instance}/start|stop", http.StatusBadRequest)
			return
		}
		if err := s.Want(RoleTVSlot, parts[0], parts[1] == "start"); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Snapshot())
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"supervisor"}`))
	})
	return mux
}
