package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// The home's status without the API or the database (Epic 53 story 53.03): GET
// /status.json on the web port, answered by the supervisor from memory. It names no host,
// path, version or count: anyone who reaches the web port (the relay too) may read it.

// Status states, as /status.json writes them.
const (
	StatusOK         = "ok"
	StatusStarting   = "starting"
	StatusDegraded   = "degraded"
	StatusRestarting = "restarting"
	StatusDown       = "down"
	StatusOff        = "off"
)

// StatusDoc is the document (docs/api/status.md).
type StatusDoc struct {
	V            int               `json:"v"`
	At           string            `json:"at"`
	Mode         string            `json:"mode"`
	Overall      string            `json:"overall"`
	Components   map[string]string `json:"components"`
	FeaturesDown []string          `json:"features_down"`
	Banner       string            `json:"banner,omitempty"`
}

// StatusCheckInterval is how often the readiness checks run; a state that a check (not a
// crash) changes moves only once two checks in a row agree.
const StatusCheckInterval = 10 * time.Second

// StatusBoard computes the document.
type StatusBoard struct {
	sup *Supervisor
	// Phases is the reconciler's view of each component (nil: none): an off, held or
	// waiting component is off, never down.
	Phases func() map[string]ComponentPhase
	// DBCheck is the database's readiness (a SELECT 1 over its own connection; nil: no
	// check here, the database group follows its process alone).
	DBCheck func(ctx context.Context) error
	// Mode is normal, safe (53.05) or recovery.
	Mode func() string
	Now  func() time.Time
	// Log gets one line per change (status_group_changed); nil: the supervisor's log.
	Log func(group, from, to, reason string)

	mu        sync.Mutex
	published map[string]string
	pending   map[string]string
	dbFails   int
	dbChecked bool
}

// ComponentPhase is a component's phase and reason as the reconciler last reported it.
type ComponentPhase struct {
	Phase  string
	Reason string
}

// NewStatusBoard makes a board for sup; every group reads starting until its first check.
func NewStatusBoard(sup *Supervisor) *StatusBoard {
	b := &StatusBoard{sup: sup, Now: time.Now, published: map[string]string{}, pending: map[string]string{}}
	for _, g := range StatusGroups {
		b.published[g.Name] = StatusStarting
	}
	return b
}

// Run checks every StatusCheckInterval until ctx ends.
func (b *StatusBoard) Run(ctx context.Context) {
	t := time.NewTicker(StatusCheckInterval)
	defer t.Stop()
	b.Check(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.Check(ctx)
		}
	}
}

// Check runs the readiness checks once and moves the states two checks agree on.
func (b *StatusBoard) Check(ctx context.Context) {
	if b.DBCheck != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := b.DBCheck(cctx)
		cancel()
		b.mu.Lock()
		b.dbChecked = true
		if err != nil {
			b.dbFails++
		} else {
			b.dbFails = 0
		}
		b.mu.Unlock()
	}
	b.update(true)
}

// Doc is the document now: a crash shows at once, a failing check after two checks.
func (b *StatusBoard) Doc() StatusDoc {
	groups := b.update(false)
	doc := StatusDoc{V: 1, At: b.Now().UTC().Format(time.RFC3339), Mode: "normal", Components: groups, FeaturesDown: []string{}}
	if b.Mode != nil {
		if m := b.Mode(); m != "" {
			doc.Mode = m
		}
	}
	doc.Overall = StatusOK
	features := map[string]bool{}
	for _, g := range StatusGroups {
		st := groups[g.Name]
		switch st {
		case StatusDown, StatusRestarting:
			for _, f := range g.FeaturesDown {
				features[f] = true
			}
		}
		if (g.Name == "database" || g.Name == "api") && (st == StatusDown || st == StatusRestarting) {
			doc.Overall = StatusDown
		} else if st != StatusOK && st != StatusOff && doc.Overall == StatusOK {
			doc.Overall = StatusDegraded
		}
		if doc.Banner == "" && g.Banner != "" && (st == StatusDegraded || st == StatusRestarting || st == StatusDown) {
			doc.Banner = g.Banner
		}
	}
	for f := range features {
		doc.FeaturesDown = append(doc.FeaturesDown, f)
	}
	sort.Strings(doc.FeaturesDown)
	return doc
}

// update computes each group's state from the processes and the reconciler; scheduled
// (a check round) moves a state the checks change once two rounds agree; a crash
// (restarting, down) and the first state after starting move at once.
func (b *StatusBoard) update(scheduled bool) map[string]string {
	raw, crashed := b.raw()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]string, len(raw))
	for _, g := range StatusGroups {
		now, was := raw[g.Name], b.published[g.Name]
		next := was
		switch {
		case now == was:
			delete(b.pending, g.Name)
		case crashed[g.Name] || now == StatusDown || now == StatusRestarting || now == StatusOff || was == StatusStarting ||
			was == StatusDown || was == StatusRestarting || was == StatusOff:
			next = now
		case scheduled && b.pending[g.Name] == now:
			next = now
		case scheduled:
			b.pending[g.Name] = now
		}
		if next != was {
			b.published[g.Name] = next
			delete(b.pending, g.Name)
			b.logChange(g.Name, was, next)
		}
		out[g.Name] = next
	}
	return out
}

func (b *StatusBoard) logChange(group, from, to string) {
	if b.Log != nil {
		b.Log(group, from, to, "")
		return
	}
	line, _ := json.Marshal(map[string]any{"level": statusLevel(to), "msg": "status_group_changed", "group": group, "from": from, "to": to})
	fmt.Fprintf(b.sup.log, "%s\n", line)
}

func statusLevel(to string) string {
	switch to {
	case StatusDown, StatusRestarting, StatusDegraded:
		return "warn"
	}
	return "info"
}

// procStatus is a process's part of its group's state.
func procStatus(p ChildStatus) string {
	if p.Blocked {
		return StatusOff
	}
	switch p.State {
	case StateRunning:
		return StatusOK
	case StateStarting:
		return StatusStarting
	case StateUnhealthy:
		return StatusDegraded
	case StateBackoff:
		return StatusRestarting
	case StateFailed:
		return StatusDown
	case StateHeld:
		return StatusOff
	case StateExited:
		if !p.Wanted {
			return StatusOff
		}
		return StatusDown
	}
	// stopped: by a shutdown, or an on-demand process nobody asked for
	return StatusOff
}

// offPhase: a component its desired state keeps from running (turned off, not
// installed, held, a requirement off, kept off this boot) is off, never down.
func offPhase(ph ComponentPhase) bool {
	switch ph.Phase {
	case components.PhaseOff, components.PhaseNotInstalled, components.PhaseStopped, components.PhaseWaiting:
		return true
	}
	return false
}

var statusRank = map[string]int{StatusOK: 0, StatusStarting: 1, StatusDegraded: 2, StatusRestarting: 3, StatusDown: 4}

// raw is each group's state from what the supervisor holds now, and the groups a
// process of which crashed (restarting, cut off): those move at once.
func (b *StatusBoard) raw() (map[string]string, map[string]bool) {
	crashed := map[string]bool{}
	snap := b.sup.snapshotStates()
	var phases map[string]ComponentPhase
	if b.Phases != nil {
		phases = b.Phases()
	}
	states := map[string][]string{}
	for _, p := range snap {
		g := groupOf(p.Component)
		if g == "" {
			continue
		}
		st := procStatus(p)
		if ph, ok := phases[p.Component]; ok && offPhase(ph) {
			st = StatusOff
		}
		if st != StatusOff {
			states[g] = append(states[g], st)
		}
		if st == StatusRestarting || st == StatusDown {
			crashed[g] = true
		}
	}
	b.mu.Lock()
	dbFails, dbChecked, hasCheck := b.dbFails, b.dbChecked, b.DBCheck != nil
	b.mu.Unlock()
	out := map[string]string{}
	for _, g := range StatusGroups {
		ss := states[g.Name]
		st := StatusOff
		if len(ss) > 0 {
			worst, ok := StatusOK, 0
			for _, s := range ss {
				if statusRank[s] > statusRank[worst] {
					worst = s
				}
				if s == StatusOK {
					ok++
				}
			}
			st = worst
			// one worker of several down: the group works, less well
			if g.Name == "processing" && ok > 0 && (worst == StatusDown || worst == StatusRestarting) {
				st = StatusDegraded
			}
		}
		if g.Name == "database" && hasCheck {
			switch {
			case !dbChecked && st == StatusOff:
				st = StatusStarting
			case dbFails > 0 && (st == StatusOK || st == StatusOff):
				// it runs (or is not ours: an external server) but refuses connections
				st = StatusDegraded
			case dbFails == 0 && dbChecked && st == StatusOff:
				st = StatusOK
			}
		}
		out[g.Name] = st
	}
	return out, crashed
}

// snapshotStates is the processes' states, without the events and the progress reads.
func (s *Supervisor) snapshotStates() []ChildStatus {
	return s.snapshot(nil, nil).Processes
}

// ServeHTTP answers GET /status.json.
func (b *StatusBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(b.Doc())
}

// StartingStatus is the document while the supervisor itself is starting: every group
// starting.
func StartingStatus(now time.Time) StatusDoc {
	doc := StatusDoc{V: 1, At: now.UTC().Format(time.RFC3339), Mode: "normal", Overall: StatusDegraded,
		Components: map[string]string{}, FeaturesDown: []string{}}
	for _, g := range StatusGroups {
		doc.Components[g.Name] = StatusStarting
	}
	return doc
}
