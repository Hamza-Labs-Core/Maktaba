package supervisor

import (
	"fmt"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Epic 51 story 51.04: each process belongs to a catalog component, and runs only while
// that component's gate is open (Allow) and it is wanted (Want: the TV front door, the
// frozen-home rule). The reconciler (reconcile.go) opens and closes the gates.

// WithCatalog is the component catalog the processes belong to (tests give their own;
// default the embedded one).
func WithCatalog(c *components.Catalog) Option { return func(s *Supervisor) { s.catalog = c } }

// drainExtra is added to a component's drain grace before its process is killed: the
// process's own drain (pipeline/shutdown.py: 120 s for transcription) ends first. A var:
// the tests shorten it.
var drainExtra = 10 * time.Second

// componentGrace is how long Allow(false) gives p to stop once asked: its component's
// drain.grace_s plus drainExtra, or p's StopGrace when longer. A component with no drain
// gets Want(false)'s wantStopGrace.
func componentGrace(cat *components.Catalog, p Process) time.Duration {
	grace := wantStopGrace
	if cat != nil {
		if comp, ok := cat.Get(p.Component); ok && comp.Drain.GraceS > 0 {
			grace = time.Duration(comp.Drain.GraceS)*time.Second + drainExtra
		}
	}
	return p.graceFor(grace)
}

// Allow opens (on) or closes the gate of an instance of a role: a process runs only while
// its gate is open and it is wanted. Closing it stops a running process the way Want(false)
// does, with the component's grace (componentGrace), and the process is not started again
// until the gate opens: a stop asked for is not a crash. Closing an already closed gate
// stops the process again if it runs (one adopted across a restart, story 35.7).
func (s *Supervisor) Allow(role Role, instance string, on bool) error {
	s.mu.Lock()
	c := s.childLocked(role, instance)
	if c == nil {
		s.mu.Unlock()
		return fmt.Errorf("no %s process %q", role, instance)
	}
	running := c.cmd != nil && c.cmd.Process != nil
	if c.allowed == on && (on || !running || c.draining) {
		s.mu.Unlock()
		return nil
	}
	c.allowed = on
	c.status.Blocked = !on
	cmd, tree := c.cmd, c.tree
	if !on && running {
		c.killReason = ReasonStopped
		c.draining = true
	}
	s.mu.Unlock()
	if on {
		select {
		case c.wake <- struct{}{}:
		default:
		}
		s.logf("%s: its component %s is on: it may start", c.proc.Name(), c.proc.Component)
		return nil
	}
	if !running {
		s.logf("%s: its component %s is off: it does not start", c.proc.Name(), c.proc.Component)
		return nil
	}
	grace := componentGrace(s.catalog, c.proc)
	s.logf("%s: its component %s is off: stopping it (pid %d, %s to drain)", c.proc.Name(), c.proc.Component,
		cmd.Process.Pid, grace)
	if err := c.proc.askStop(cmd.Process, tree); err != nil {
		s.cannotAsk(c.proc, err)
		s.killRun(c, cmd, 0)
		return nil
	}
	go func() {
		time.Sleep(grace)
		s.killRun(c, cmd, grace)
	}()
	return nil
}

// Gate reports the component of an instance of a role and whether its gate is open.
// ok is false when there is no such process.
func (s *Supervisor) Gate(role Role, instance string) (component string, open, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.childLocked(role, instance)
	if c == nil {
		return "", false, false
	}
	return c.proc.Component, c.allowed, true
}

func (s *Supervisor) childLocked(role Role, instance string) *child {
	var out *child
	for _, ch := range s.children {
		if ch.proc.Role == role && ch.proc.Instance == instance {
			out = ch
		}
	}
	return out
}

// procView is one process as the reconciler sees it.
type procView struct {
	Role      Role
	Instance  string
	Component string
	Dormant   bool
	Allowed   bool
	Wanted    bool
	Draining  bool
	Once      bool
	State     State
	Running   bool // a process runs (pid)
	Restarts  int
	CrashLoop bool
	// Missing: its program is not on this machine (Process.Present)
	Missing bool
	// Restarts24h: its restarts of the last 24 h; PID its running process (0: none)
	Restarts24h int
	PID         int
}

// restartWindow is the health's restart window (restarts_24h).
const restartWindow = 24 * time.Hour

// recentRestarts is ts less the restarts older than restartWindow at now.
func recentRestarts(ts []time.Time, now time.Time) []time.Time {
	i := 0
	for i < len(ts) && now.Sub(ts[i]) > restartWindow {
		i++
	}
	return ts[i:]
}

// restarts24 is the restarts of the last 24 h of a component's processes.
func restarts24(own []procView) int {
	n := 0
	for _, p := range own {
		n += p.Restarts24h
	}
	return n
}

// rssEvery is how often the reconciler reads its processes' memory; rssStep the change
// (MB, or a tenth) that is written: a few MB either way rewrites no row (T09: an idle
// machine writes nothing).
const (
	rssEvery = 5 * time.Minute
	rssStep  = 64
)

// sampleRSS reads the memory of every running process, every rssEvery, into r.rss by
// component (a component with none running has no entry).
func (r *Reconciler) sampleRSS(views []procView) {
	if r.RSS == nil {
		return
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	r.mu.Lock()
	due := r.rssAt.IsZero() || now.Sub(r.rssAt) >= rssEvery
	if due {
		r.rssAt = now
	}
	r.mu.Unlock()
	if !due {
		return
	}
	var pids []int
	for _, p := range views {
		if p.Running && p.PID > 0 {
			pids = append(pids, p.PID)
		}
	}
	got := r.RSS(pids)
	sum := map[string]int64{}
	for _, p := range views {
		if b, ok := got[p.PID]; ok && p.Running && p.PID > 0 {
			sum[p.Component] += int64(b >> 20)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rss == nil {
		r.rss = map[string]int64{}
	}
	for id := range r.rss {
		if _, ok := sum[id]; !ok {
			delete(r.rss, id)
		}
	}
	for id, mb := range sum {
		if prev, ok := r.rss[id]; !ok || rssMoved(prev, mb) {
			r.rss[id] = mb
		}
	}
}

// rssMoved: from prev to mb is a change worth a write (rssStep MB, or a tenth).
func rssMoved(prev, mb int64) bool {
	d := mb - prev
	if d < 0 {
		d = -d
	}
	return d >= rssStep || d*10 >= prev
}

// rssOf is component id's memory as last sampled, while one of its processes runs.
func (r *Reconciler) rssOf(id string, own []procView) (int64, bool) {
	running := false
	for _, p := range own {
		running = running || p.Running
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	mb, ok := r.rss[id]
	return mb, ok && running
}

// procViews is every process, for the reconciler.
func (s *Supervisor) procViews() []procView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]procView, 0, len(s.children))
	for _, c := range s.children {
		missing := c.proc.Present != nil && !c.proc.Present()
		out = append(out, procView{Role: c.proc.Role, Instance: c.proc.Instance, Component: c.proc.Component,
			Dormant: c.proc.Dormant, Allowed: c.allowed, Wanted: c.wanted, Draining: c.draining, Once: c.proc.Once,
			State: c.status.State, Running: c.cmd != nil && c.cmd.Process != nil, Restarts: c.status.Restarts,
			CrashLoop: c.failed, Missing: missing, Restarts24h: len(recentRestarts(c.restartAt, s.now())), PID: c.status.PID})
	}
	return out
}

// procPhase is one process's phase (story 51.04 §6), and the reason with it ("" none).
// A process that is not wanted (a slot the front door has not asked for) or a job that
// ended counts for nothing: ok is false.
func procPhase(p procView) (phase, reason string, ok bool) {
	switch {
	case p.Draining || (!p.Allowed && p.Running):
		return components.PhaseDraining, "", true
	case !p.Allowed:
		return components.PhaseOff, "", true
	case !p.Wanted && !p.Running:
		return "", "", false
	}
	switch p.State {
	case StateRunning:
		return components.PhaseRunning, "", true
	case StateUnhealthy:
		return components.PhaseDegraded, "", true
	case StateStarting, StateBackoff:
		return components.PhaseStarting, "", true
	case StateFailed:
		return components.PhaseFailed, reasonCrashLoop, true
	case StateExited:
		if p.Once {
			return "", "", false
		}
		return components.PhaseStopped, "", true
	}
	return components.PhaseStopped, "", true
}
