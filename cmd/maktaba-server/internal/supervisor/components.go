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
	// FailReason: it exited with one of its FatalExits and waits, not restarted
	// (maktaba-plugind's 78: sandbox_unavailable)
	FailReason string
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
	if r.RSS == nil && r.CPU == nil {
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
	if r.CPU != nil {
		r.sampleCPU(views, r.CPU(pids), now)
	}
	if r.RSS == nil {
		return
	}
	got := r.RSS(pids)
	sum := map[string]int64{}
	parts := map[string]map[string]int64{}
	for _, p := range views {
		if b, ok := got[p.PID]; ok && p.Running && p.PID > 0 {
			sum[p.Component] += int64(b >> 20)
			if parts[p.Component] == nil {
				parts[p.Component] = map[string]int64{}
			}
			parts[p.Component][string(p.Role)+"/"+p.Instance] += int64(b >> 20)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rssParts = moveParts(r.rssParts, parts)
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

// moveParts is each component's memory by process ("<role>/<instance>", MB) as last
// written: a process's number moves only by rssMoved, as the component's does, so an idle
// machine rewrites no row; a process that is gone has no entry.
func moveParts(prev, now map[string]map[string]int64) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for id, procs := range now {
		out[id] = map[string]int64{}
		for name, mb := range procs {
			if was, ok := prev[id][name]; ok && !rssMoved(was, mb) {
				mb = was
			}
			out[id][name] = mb
		}
	}
	return out
}

// rssPartsOf is component id's memory by process as last sampled, for a component with
// more than one process running (one process: rss_mb already is its number).
func (r *Reconciler) rssPartsOf(id string) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	procs := r.rssParts[id]
	if len(procs) < 2 {
		return nil, false
	}
	out := make(map[string]any, len(procs))
	for name, mb := range procs {
		out[name] = mb
	}
	return out, true
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
			CrashLoop: c.failed, Missing: missing, Restarts24h: len(recentRestarts(c.restartAt, s.now())), PID: c.status.PID,
			FailReason: c.fatal})
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
		if p.FailReason != "" {
			return components.PhaseFailed, p.FailReason, true
		}
		return components.PhaseFailed, reasonCrashLoop, true
	case StateExited:
		if p.Once {
			return "", "", false
		}
		return components.PhaseStopped, "", true
	}
	return components.PhaseStopped, "", true
}

// cpuStep is the change in cpu_pct (points of one core) that is written, or a tenth of a
// busy process's number: a point or two either way rewrites no row, so an idle machine
// writes nothing (story 54.52, the same rule as rss_mb).
const cpuStep = 5

// cpuSample is a process's processor time as last read, and when.
type cpuSample struct {
	used time.Duration
	at   time.Time
}

// sampleCPU turns each running process's processor time into the share of one core it used
// since the last sample (cpu_pct, 100 is one whole core), by component and by process
// ("<role>/<instance>"), into r.cpu and r.cpuParts. A process first seen, or whose time
// went back (a pid used again), has no number until the next sample.
func (r *Reconciler) sampleCPU(views []procView, used map[int]time.Duration, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.cpuPrev
	r.cpuPrev = map[int]cpuSample{}
	sum := map[string]int64{}
	parts := map[string]map[string]int64{}
	for _, p := range views {
		u, ok := used[p.PID]
		if !ok || !p.Running || p.PID <= 0 {
			continue
		}
		r.cpuPrev[p.PID] = cpuSample{used: u, at: now}
		was, ok := prev[p.PID]
		wall := now.Sub(was.at)
		if !ok || u < was.used || wall <= 0 {
			continue
		}
		pct := int64((u - was.used) * 100 / wall)
		sum[p.Component] += pct
		if parts[p.Component] == nil {
			parts[p.Component] = map[string]int64{}
		}
		parts[p.Component][string(p.Role)+"/"+p.Instance] += pct
	}
	// each process's number moves only by cpuMoved, as the component's does
	out := map[string]map[string]int64{}
	for id, procs := range parts {
		out[id] = map[string]int64{}
		for name, pct := range procs {
			if was, ok := r.cpuParts[id][name]; ok && !cpuMoved(was, pct) {
				pct = was
			}
			out[id][name] = pct
		}
	}
	r.cpuParts = out
	if r.cpu == nil {
		r.cpu = map[string]int64{}
	}
	for id := range r.cpu {
		if _, ok := sum[id]; !ok {
			delete(r.cpu, id)
		}
	}
	for id, pct := range sum {
		if was, ok := r.cpu[id]; !ok || cpuMoved(was, pct) {
			r.cpu[id] = pct
		}
	}
}

// cpuMoved: from prev to pct is a change worth a write (cpuStep points, or a tenth).
func cpuMoved(prev, pct int64) bool {
	d := pct - prev
	if d < 0 {
		d = -d
	}
	return d >= cpuStep && d*10 >= prev
}

// cpuOf is component id's processor use (cpu_pct) as last sampled, while one of its
// processes runs, and by process when more than one runs (cpu_parts).
func (r *Reconciler) cpuOf(id string, own []procView) (int64, map[string]any, bool) {
	running := false
	for _, p := range own {
		running = running || p.Running
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pct, ok := r.cpu[id]
	if !ok || !running {
		return 0, nil, false
	}
	procs := r.cpuParts[id]
	if len(procs) < 2 {
		return pct, nil, true
	}
	out := make(map[string]any, len(procs))
	for name, v := range procs {
		out[name] = v
	}
	return pct, out, true
}
