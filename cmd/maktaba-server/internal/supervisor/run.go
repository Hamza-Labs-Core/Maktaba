package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Supervisor runs a set of child processes and keeps each one alive on
// its own (Story 22.9): a child that exits is restarted with
// exponential backoff while the others keep running; one that exits
// max_restarts times inside the window is marked failed and retried
// slowly; one whose liveness probe fails probe_failures times in a row
// is killed and restarted (a hang).
type Supervisor struct {
	policy Policy
	sink   EventSink
	log    io.Writer
	now    func() time.Time
	client *http.Client
	host   string

	mu       sync.Mutex
	children []*child
	events   []Event
	seq      int64
	stopping bool
	started  time.Time

	fatal     chan error
	fatalOnce sync.Once
	// onExit, when set, hears every child exit (the TV front door's slot report).
	onExit func(Process, ExitInfo)
	// noConsole: the first child that could not be asked to stop for want of a
	// console has been logged (cannotAsk).
	noConsole sync.Once
	// llm, when set, is the model server's state (WithLLM).
	llm func() llmserver.Status
	// ffmpeg, when set, is the last check of the roles' ffmpeg (WithFFmpeg).
	ffmpeg func() *ffmpeg.Report
	// ho, when set, keeps the handover record and adopts what it lists (WithHandover,
	// handover.go: Epic 35 story 35.7).
	ho *handoverState
}

// child is one supervised role. Everything but proc is guarded by s.mu.
type child struct {
	proc       Process
	status     ChildStatus
	cmd        *exec.Cmd    // the running process, nil between runs
	tree       *procTree    // its process group / job, nil between runs
	exits      []time.Time  // exits that led to a restart, for the crash-loop window
	killReason string       // set before the supervisor kills the child
	failed     bool         // in a crash loop (state failed or retrying from it)
	wanted     bool         // run it (an on-demand process is not until asked for)
	binding    *SlotBinding // tv-slot: the TV release it runs (nil: the current binary)
	basePath   string       // the binary the process was built with
	wake       chan struct{}
	done       chan struct{} // closed when its loop ends (or it never runs one)

	// the handover (Process.Survives): the record's entry to adopt at the start, the
	// running process's start time and named job (Windows), and whether it may outlive
	// this supervisor (Windows: it left the launcher's job for the boot's)
	adopt    *layout.HandoverChild
	ident    uint64
	job      string
	detached bool
}

// Option tweaks a Supervisor (tests inject a clock, log and sink).
type Option func(*Supervisor)

// WithEventSink persists events (e.g. NewFileSink) and reloads the
// recent ones at construction so history survives a supervisor restart.
func WithEventSink(sink EventSink) Option { return func(s *Supervisor) { s.sink = sink } }

// WithLog redirects the supervisor's own log lines (default stderr).
func WithLog(w io.Writer) Option { return func(s *Supervisor) { s.log = w } }

// WithExitHook calls f after every child exit (Epic 33: the front door hears a
// TV slot's end at once).
func WithExitHook(f func(Process, ExitInfo)) Option { return func(s *Supervisor) { s.onExit = f } }

// maxEvents is the in-memory event ring size.
const maxEvents = 200

// New builds a Supervisor for procs; it starts nothing until Run.
func New(procs []Process, policy Policy, opts ...Option) *Supervisor {
	s := &Supervisor{
		policy: policy,
		log:    os.Stderr,
		now:    time.Now,
		fatal:  make(chan error, 1),
	}
	s.host, _ = os.Hostname()
	for _, o := range opts {
		o(s)
	}
	// each check bounds itself (checkChild): a child may take longer than the policy's
	s.client = &http.Client{}
	s.started = s.now()
	for _, p := range procs {
		st := StateStarting
		if p.OnDemand {
			st = StateStopped
		}
		s.children = append(s.children, &child{
			proc:   p,
			wanted: !p.OnDemand,
			basePath: func() string {
				if p.Cmd != nil {
					return p.Cmd.Path
				}
				return ""
			}(),
			wake:   make(chan struct{}, 1),
			done:   make(chan struct{}),
			status: ChildStatus{Role: p.Role, Instance: p.Instance, State: st, ProbeURL: p.Probe, Wanted: !p.OnDemand},
		})
	}
	if s.sink != nil {
		if prev, err := s.sink.Load(maxEvents); err == nil {
			s.events = prev
			for _, e := range prev {
				if e.Seq > s.seq {
					s.seq = e.Seq
				}
			}
		}
	}
	return s
}

// Run starts every process and blocks until ctx is cancelled — or, with
// restart = "never", until any child exits. On return every child has
// been sent the graceful-stop signal and, after grace, killed. With
// restart = "never" the child's exit is returned as the error; a
// shutdown returns nil.
func Run(ctx context.Context, procs []Process, grace time.Duration) error {
	if len(procs) == 0 {
		return errors.New("no processes to run")
	}
	return New(procs, DefaultPolicy()).Run(ctx, grace)
}

// Run is the supervisor's main loop; see the package-level Run.
//
// Processes marked First start alone; the others start once every one of
// them runs (its check answered), and on shutdown they stop before the
// First ones do.
func (s *Supervisor) Run(ctx context.Context, grace time.Duration) error {
	if len(s.children) == 0 {
		return errors.New("no processes to run")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.startHandover()

	var first, rest []*child
	for _, c := range s.children {
		if c.proc.First {
			first = append(first, c)
		} else {
			rest = append(rest, c)
		}
	}
	var firstWG, restWG sync.WaitGroup
	start := func(wg *sync.WaitGroup, cs []*child) {
		for _, c := range cs {
			wg.Add(1)
			go func(c *child) {
				defer wg.Done()
				defer close(c.done)
				s.loop(runCtx, c)
			}(c)
		}
	}

	var result error
	start(&firstWG, first)
	ready := true
	if len(first) > 0 && len(rest) > 0 {
		ready, result = s.awaitRunning(ctx, first)
	}
	if ready {
		start(&restWG, rest)
		select {
		case <-ctx.Done():
		case result = <-s.fatal:
		}
	}

	// Graceful: stop restarting, ask every running child to drain, then
	// hard-kill stragglers after grace; the First processes last.
	s.mu.Lock()
	s.stopping = true
	handover := s.ho != nil && s.ho.handingOver && ready
	cancel()
	s.mu.Unlock()
	if handover {
		// story 35.7: every process but the survivors stops; they run on, and the next
		// supervisor adopts them from the record
		var others []*child
		for _, c := range rest {
			if !c.proc.Survives {
				others = append(others, c)
			}
		}
		s.stopChildren(others, grace)
		s.stopChildren(first, grace)
		s.mu.Lock()
		s.ho.handedOver = true
		s.mu.Unlock()
		s.saveRecord(true)
		s.logf("handed over: the TV processes run on for the next supervisor")
		return result
	}
	s.stopAll(rest, &restWG, grace)
	s.stopAll(first, &firstWG, grace)
	if !ready {
		for _, c := range rest {
			s.setState(c, StateStopped)
		}
	}
	if s.ho != nil && !s.unadopted() {
		_ = layout.RemoveHandover(s.ho.dir) // every process stopped
	}
	return result
}

// unadopted reports whether a process the record lists was never taken up (its loop never
// ran: the supervisor stopped before it was ready). The record then stays, for the
// launcher to stop it, or the next start to adopt it.
func (s *Supervisor) unadopted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.adopt != nil {
			return true
		}
	}
	return false
}

// stopChildren is stopAll for some of the children (their loops' done channels).
func (s *Supervisor) stopChildren(cs []*child, grace time.Duration) {
	s.mu.Lock()
	s.signalLocked(cs, false)
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for _, c := range cs {
			<-c.done
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(grace):
		s.mu.Lock()
		s.signalLocked(cs, true)
		s.mu.Unlock()
		<-done
	}
}

// awaitRunning waits until every child in cs runs. It reports false when the
// supervisor is told to stop first (a signal, or a child's fatal exit).
func (s *Supervisor) awaitRunning(ctx context.Context, cs []*child) (bool, error) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	logged := false
	for {
		s.mu.Lock()
		all := true
		for _, c := range cs {
			all = all && c.status.State == StateRunning
		}
		s.mu.Unlock()
		if all {
			return true, nil
		}
		if !logged {
			s.logf("waiting for %s before starting the others", cs[0].proc.Name())
			logged = true
		}
		select {
		case <-ctx.Done():
			return false, nil
		case err := <-s.fatal:
			return false, err
		case <-t.C:
		}
	}
}

// stopAll sends cs their graceful-stop signal, waits up to grace (a child's own
// StopGrace when longer) for their loops to end and then kills what is left: each
// child at the end of its own grace.
func (s *Supervisor) stopAll(cs []*child, wg *sync.WaitGroup, grace time.Duration) {
	s.mu.Lock()
	s.signalLocked(cs, false)
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	byGrace := map[time.Duration][]*child{}
	for _, c := range cs {
		g := c.proc.graceFor(grace)
		byGrace[g] = append(byGrace[g], c)
	}
	graces := make([]time.Duration, 0, len(byGrace))
	for g := range byGrace {
		graces = append(graces, g)
	}
	sort.Slice(graces, func(i, j int) bool { return graces[i] < graces[j] })
	start := time.Now()
	for _, g := range graces {
		select {
		case <-done:
			return
		case <-time.After(time.Until(start.Add(g))):
			s.mu.Lock()
			s.signalLocked(byGrace[g], true)
			s.mu.Unlock()
		}
	}
	<-done
}

// signalLocked signals every running child of cs. A kill takes the child's
// whole process tree; a graceful ask goes to the child (on Windows, its
// process group), which stops its own children as it drains. A child that
// cannot be asked is killed at once.
func (s *Supervisor) signalLocked(cs []*child, kill bool) {
	for _, c := range cs {
		if c.cmd != nil && c.cmd.Process != nil {
			if !kill {
				err := c.proc.askStop(c.cmd.Process, c.tree)
				if err == nil {
					continue
				}
				s.cannotAsk(c.proc, err)
			}
			c.tree.kill()
			_ = c.cmd.Process.Signal(os.Kill)
		}
	}
}

// cannotAsk logs a child that could not be asked to stop and is killed
// instead. Having no console is this process's state, not the child's: it is
// said once, not at every stop.
func (s *Supervisor) cannotAsk(p Process, err error) {
	if errors.Is(err, errNoConsole) {
		s.noConsole.Do(func() {
			s.logf("%s cannot be asked to stop: %v; it is killed instead, as are the other children CTRL_BREAK would ask (logged once)",
				p.Name(), err)
		})
		return
	}
	s.warnf("%s could not be asked to stop (%v); killing it instead", p.Name(), err)
}

// loop runs one child until shutdown, restarting it per the policy.
func (s *Supervisor) loop(ctx context.Context, c *child) {
	backoff := s.policy.BackoffInitial
	tpl := c.proc.Cmd
	first := true
	s.mu.Lock()
	a := c.adopt
	c.adopt = nil
	s.mu.Unlock()
	if a != nil {
		// story 35.7: the process the supervisor before handed over, when it is still
		// that process and answers; its end is treated as any exit. It runs already: what
		// gates a start (Prepare: the database step) holds only the fresh one after it.
		if info, ok := s.adoptRun(ctx, c, *a); ok && s.afterExit(ctx, c, info, &backoff) {
			return
		}
	}
	if c.proc.Prepare != nil && !s.prepare(ctx, c) {
		s.setState(c, StateStopped)
		return
	}
	for {
		cmd := tpl
		if !first {
			cmd = cloneCmd(tpl)
		}
		first = false
		if c.proc.Args != nil {
			cmd.Args = append([]string{cmd.Args[0]}, c.proc.Args()...)
		}
		if c.proc.BeforeStart != nil {
			c.proc.BeforeStart()
		}

		s.mu.Lock()
		if s.stopping {
			c.status.State = StateStopped
			s.mu.Unlock()
			return
		}
		if !c.wanted {
			// an on-demand process (or one stopped on request): wait to be asked
			c.status.State = StateStopped
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				s.setState(c, StateStopped)
				return
			case <-c.wake:
			}
			backoff = s.policy.BackoffInitial
			continue
		}
		startedAt := s.now()
		var info ExitInfo
		survive := s.survivalLocked(c)
		startErr := applyBinding(cmd, c.binding, c.basePath)
		if startErr == nil {
			prepareTree(cmd)
			orig := cmd.SysProcAttr
			if survive == surviveBoot {
				// on a copy: the template's (and so every later start's) stays as it is
				sa := *orig
				cmd.SysProcAttr = &sa
				prepareSurvivor(cmd)
			}
			startErr = cmd.Start()
			if survive == surviveBoot {
				cmd.SysProcAttr = orig
			}
			if startErr != nil && survive == surviveBoot && errBreakaway(startErr) {
				// the job this supervisor runs in lets no child leave it: this one ends
				// with the supervisor, and an update restarts the TV (story 35.7 AC4)
				s.ho.refused = c.proc.Name() + " cannot leave the job the server runs in: " + startErr.Error()
				s.logf("%s; it is started without leaving it", s.ho.refused)
				cmd = cloneCmd(cmd)
				prepareTree(cmd)
				survive = surviveNot
				startErr = cmd.Start()
			}
		}
		if err := startErr; err != nil {
			info = ExitInfo{At: startedAt, Code: -1, Reason: ReasonStartFailed, Error: err.Error()}
			c.status.LastExit = &info
			c.status.PID = 0
			c.status.StartedAt = nil
			s.mu.Unlock()
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventExited, Reason: ReasonStartFailed, Message: err.Error()})
			s.errorf("%s could not start: %v", c.proc.Name(), err)
		} else {
			pid := cmd.Process.Pid
			var tree *procTree
			var treeErr error
			c.detached = false
			if survive != surviveNot {
				c.job = survivorJob(s.ho.dir, pid)
				boot := s.ho.job
				if survive == surviveHere {
					boot = nil
				}
				tree, c.detached, treeErr = attachSurvivor(cmd, boot, c.job, s.ho.launcherPID)
			} else {
				c.job = ""
				tree, treeErr = attachTree(cmd)
			}
			if c.proc.Survives {
				identify(c, pid)
			}
			c.cmd = cmd
			c.tree = tree
			c.killReason = ""
			c.status.PID = pid
			c.status.StartedAt = &startedAt
			c.status.NextRestartAt = nil
			c.status.LastProbe = nil
			if c.proc.Probe == "" {
				c.status.State = StateRunning
			} else {
				c.status.State = StateStarting
			}
			s.mu.Unlock()
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventStarted, PID: pid})
			s.logf("started %s (pid %d)", c.proc.Name(), pid)
			if treeErr != nil {
				s.logf("%s (pid %d): its child processes are not tied to it: %v", c.proc.Name(), pid, treeErr)
			}
			if c.proc.Survives {
				s.saveRecord(false)
			}

			probeCtx, stopProbe := context.WithCancel(ctx)
			if c.proc.Probe != "" {
				go s.probe(probeCtx, c, cmd, startedAt)
			}
			waitErr := cmd.Wait()
			stopProbe()
			if s.HandedOver() && c.proc.Survives {
				return // the next supervisor's process: nothing of it is this one's now
			}
			// The child is gone: end whatever it left behind (ffmpeg,
			// yt-dlp) so a restart never runs next to orphans.
			tree.release()
			info = s.exitInfo(c, cmd, waitErr, startedAt)
		}
		if s.afterExit(ctx, c, info, &backoff) {
			return
		}
	}
}

// afterExit records a run's end (info) and waits as the policy says before the next
// start. true: the loop ends (a shutdown, a policy that leaves the child stopped).
func (s *Supervisor) afterExit(ctx context.Context, c *child, info ExitInfo, backoff *time.Duration) bool {
	s.mu.Lock()
	stopping := s.stopping
	if stopping {
		info.Reason = ReasonShutdown
	}
	c.status.LastExit = &info
	c.status.PID = 0
	c.status.StartedAt = nil
	c.cmd = nil
	c.tree = nil
	c.ident = 0
	s.mu.Unlock()
	if c.proc.Survives {
		s.saveRecord(false)
	}
	if info.Reason != ReasonStartFailed {
		code := info.Code
		s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventExited, PID: info.PID, Code: &code,
			Signal: info.Signal, Reason: info.Reason, UptimeSec: info.UptimeSec, Message: info.Error})
	}
	if s.onExit != nil && info.Reason != ReasonStartFailed {
		s.onExit(c.proc, info)
	}
	if info.Reason == ReasonStopped && !stopping {
		s.logf("%s stopped on request (pid %d)", c.proc.Name(), info.PID)
		return false // waits until it is wanted again
	}
	if info.Reason == ReasonRestarted && !stopping {
		// asked for: no backoff, and not an exit of a crash loop
		s.logf("%s stopped to start again (pid %d)", c.proc.Name(), info.PID)
		*backoff = s.policy.BackoffInitial
		return false
	}
	if stopping {
		s.logf("%s stopped (pid %d, %s)", c.proc.Name(), info.PID, describeExit(info))
		s.setState(c, StateStopped)
		return true
	}

	// Decide what happens next.
	switch {
	case s.policy.Restart == RestartNever:
		s.errorf("%s exited (pid %d, %s); restart = never, stopping the stack", c.proc.Name(), info.PID, describeExit(info))
		s.setState(c, StateExited)
		s.fatalOnce.Do(func() { s.fatal <- fmt.Errorf("%s exited: %s", c.proc.Name(), describeExit(info)) })
		return true
	case s.policy.Restart == RestartOnFailure && info.Reason == ReasonClean:
		s.logf("%s exited cleanly (pid %d, uptime %s); restart = on-failure, leaving it stopped",
			c.proc.Name(), info.PID, uptime(info))
		s.setState(c, StateExited)
		return true
	}

	now := s.now()
	s.mu.Lock()
	if time.Duration(info.UptimeSec*float64(time.Second)) >= s.policy.HealthyAfter {
		// It ran long enough to count as healthy: forget the past.
		*backoff = s.policy.BackoffInitial
		c.exits = nil
		c.failed = false
	}
	c.exits = append(c.recentExits(now, s.policy.Window), now)
	delay, state := *backoff, StateBackoff
	wasFailed := c.failed
	// for the line below: the restart this wait leads to, and the window's exits so far
	restartN, exitsN := c.status.Restarts+1, len(c.exits)
	if len(c.exits) >= s.policy.MaxRestarts {
		delay, state = s.policy.FailedRetry, StateFailed
		c.failed = true
	} else {
		c.failed = false
		*backoff = nextBackoff(*backoff, s.policy.BackoffMax)
	}
	next := now.Add(delay)
	c.status.State = state
	c.status.NextRestartAt = &next
	s.mu.Unlock()

	if state == StateFailed {
		if !wasFailed {
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventFailed, Reason: info.Reason, DelaySec: delay.Seconds(),
				Message: fmt.Sprintf("%d exits in %s", s.policy.MaxRestarts, s.policy.Window)})
		}
		s.errorf("%s exited (pid %d, %s); crash loop: %d exits in %s, marked failed, retrying in %s",
			c.proc.Name(), info.PID, describeExit(info), s.policy.MaxRestarts, s.policy.Window, delay)
	} else {
		s.warnf("%s exited (pid %d, %s); restarting in %s (restart #%d, %d/%d exits in %s)", c.proc.Name(), info.PID,
			describeExit(info), delay, restartN, exitsN, s.policy.MaxRestarts, s.policy.Window)
	}

	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		s.setState(c, StateStopped)
		return true
	case <-timer.C:
	}
	at := s.now()
	s.mu.Lock()
	c.status.Restarts++
	c.status.LastRestartAt = &at
	n := c.status.Restarts
	s.mu.Unlock()
	s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventRestarting, DelaySec: delay.Seconds(),
		Message: fmt.Sprintf("restart #%d", n)})
	return false
}

// recentExits returns the exits inside the crash-loop window. Caller
// holds s.mu.
func (c *child) recentExits(now time.Time, window time.Duration) []time.Time {
	cut := now.Add(-window)
	out := []time.Time{}
	for _, t := range c.exits {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Supervisor) setState(c *child, st State) {
	s.mu.Lock()
	c.status.State = st
	c.status.NextRestartAt = nil
	s.mu.Unlock()
}

// probe polls the child's liveness URL until ctx ends. After the
// startup grace (or once a probe has succeeded) probe_failures failures
// in a row kill the child; loop then restarts it with reason "hang".
func (s *Supervisor) probe(ctx context.Context, c *child, cmd *exec.Cmd, startedAt time.Time) {
	t := time.NewTicker(s.policy.ProbeInterval)
	defer t.Stop()
	seenOK := false
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := s.checkChild(ctx, c.proc)
		if ctx.Err() != nil {
			return // shutting down or the child already exited
		}
		now := s.now()
		kill := false
		s.mu.Lock()
		if c.cmd != cmd {
			s.mu.Unlock()
			return
		}
		if err == nil {
			failures = 0
			seenOK = true
			c.status.State = StateRunning
		} else if errors.Is(err, ErrStarting) {
			// up, not ready yet (a database recovering): no failure, not running
			failures = 0
			c.status.State = StateStarting
		} else if seenOK || now.Sub(startedAt) >= s.policy.StartupGrace {
			failures++
			if c.status.State == StateRunning {
				c.status.State = StateUnhealthy
			}
		}
		pi := ProbeInfo{At: now, OK: err == nil, Failures: failures}
		if err != nil {
			pi.Error = err.Error()
		}
		c.status.LastProbe = &pi
		if failures >= s.policy.ProbeFailures {
			c.killReason = ReasonHang
			kill = true
		}
		s.mu.Unlock()
		if kill {
			s.warnf("%s (pid %d) failed %d liveness probes in a row (%v); asking it to stop",
				c.proc.Name(), cmd.Process.Pid, failures, err)
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventHung, PID: cmd.Process.Pid, Reason: ReasonHang,
				Message: err.Error()})
			s.mu.Lock()
			tree := c.tree
			s.mu.Unlock()
			// asked first, like a shutdown: a child busy in a long write (a worker in a
			// native call holding its GIL) finishes it and exits, where a kill mid-write
			// can leave its store broken (the Chroma index). Killed after HangGrace.
			if c.proc.askStop(cmd.Process, tree) == nil {
				hang := c.proc.graceFor(HangGrace)
				select {
				case <-ctx.Done():
					return // it stopped
				case <-time.After(hang):
				}
				s.warnf("%s (pid %d) did not stop within %s; killing it", c.proc.Name(), cmd.Process.Pid, hang)
			}
			tree.kill()
			_ = cmd.Process.Kill()
			return
		}
	}
}

// HangGrace is how long a child that failed its liveness probes has to stop once asked,
// before it is killed (a var: the tests shorten it).
var HangGrace = 60 * time.Second

// checkChild runs a child's liveness check: its Check, else a GET of its Probe.
func (s *Supervisor) checkChild(ctx context.Context, p Process) error {
	timeout := s.policy.ProbeTimeout
	if p.ProbeTimeout > timeout {
		timeout = p.ProbeTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if p.Check != nil {
		return p.Check(cctx)
	}
	return s.check(cctx, p.Probe)
}

// check GETs a liveness URL; any 2xx is alive.
func (s *Supervisor) check(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("liveness probe answered %d", resp.StatusCode)
	}
	return nil
}

func (s *Supervisor) exitInfo(c *child, cmd *exec.Cmd, waitErr error, startedAt time.Time) ExitInfo {
	now := s.now()
	info := ExitInfo{At: now, PID: cmd.Process.Pid, Code: -1, UptimeSec: now.Sub(startedAt).Seconds()}
	if ps := cmd.ProcessState; ps != nil {
		info.Code = ps.ExitCode()
		info.Signal = signalName(ps)
	}
	s.mu.Lock()
	killed := c.killReason
	s.mu.Unlock()
	switch {
	case killed != "":
		info.Reason = killed
	case waitErr == nil && info.Code == 0:
		info.Reason = ReasonClean
	default:
		info.Reason = ReasonCrash
	}
	if waitErr != nil {
		info.Error = waitErr.Error()
	}
	return info
}

func (s *Supervisor) emit(e Event) {
	s.mu.Lock()
	s.seq++
	e.Seq = s.seq
	if e.At.IsZero() {
		e.At = s.now()
	}
	s.events = append(s.events, e)
	if len(s.events) > maxEvents {
		s.events = append([]Event(nil), s.events[len(s.events)-maxEvents:]...)
	}
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		if err := sink.Append(e); err != nil {
			s.warnf("event log write failed: %v", err)
		}
	}
}

func (s *Supervisor) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "[supervisor] "+format+"\n", args...)
}

// errorf and warnf are logf for a failure: the leading level=… is what the logs page
// (internal/logfiles) reads a plain line's level from.
func (s *Supervisor) errorf(format string, args ...any) { s.logf("level=error "+format, args...) }
func (s *Supervisor) warnf(format string, args ...any)  { s.logf("level=warn "+format, args...) }

func describeExit(info ExitInfo) string {
	out := fmt.Sprintf("code %d", info.Code)
	if info.Signal != "" {
		out += ", signal " + info.Signal
	}
	out += ", uptime " + uptime(info) + ", reason " + info.Reason
	return out
}

func uptime(info ExitInfo) string {
	return time.Duration(info.UptimeSec * float64(time.Second)).Round(time.Millisecond).String()
}

// restartGrace is how long Restart waits for the instance to stop before it kills it:
// the bundled postgres's fast shutdown ends its sessions and checkpoints first.
const restartGrace = 30 * time.Second

// Restart stops a running instance of a role and starts it again at once, with its
// arguments read afresh (Process.Args). One that does not run now is left as it is: its
// next start reads them anyway.
func (s *Supervisor) Restart(role Role, instance string) error {
	s.mu.Lock()
	var c *child
	for _, ch := range s.children {
		if ch.proc.Role == role && ch.proc.Instance == instance {
			c = ch
		}
	}
	if c == nil {
		s.mu.Unlock()
		return fmt.Errorf("no %s process %q", role, instance)
	}
	cmd, tree := c.cmd, c.tree
	if cmd == nil || cmd.Process == nil || s.stopping {
		s.mu.Unlock()
		return nil
	}
	c.killReason = ReasonRestarted
	s.mu.Unlock()
	s.logf("%s: starting it again (pid %d)", c.proc.Name(), cmd.Process.Pid)
	if err := c.proc.askStop(cmd.Process, tree); err != nil {
		s.cannotAsk(c.proc, err)
		s.killRun(c, cmd, 0)
		return nil
	}
	go func() {
		grace := c.proc.graceFor(restartGrace)
		time.Sleep(grace)
		s.killRun(c, cmd, grace)
	}()
	return nil
}

// Want starts (on) or stops an instance of a role (Epic 33: the TV front door
// asks for the hot standby and the staging slot). A stopped process is not
// restarted until it is wanted again.
func (s *Supervisor) Want(role Role, instance string, on bool) error {
	s.mu.Lock()
	var c *child
	for _, ch := range s.children {
		if ch.proc.Role == role && ch.proc.Instance == instance {
			c = ch
		}
	}
	if c == nil {
		s.mu.Unlock()
		return fmt.Errorf("no %s process %q", role, instance)
	}
	if c.wanted == on {
		s.mu.Unlock()
		return nil
	}
	c.wanted = on
	c.status.Wanted = on
	cmd, tree := c.cmd, c.tree
	if !on && cmd != nil && cmd.Process != nil {
		c.killReason = ReasonStopped
	}
	s.mu.Unlock()
	if on {
		select {
		case c.wake <- struct{}{}:
		default:
		}
		s.logf("%s wanted: starting it", c.proc.Name())
		return nil
	}
	if cmd != nil && cmd.Process != nil {
		s.logf("%s not wanted: stopping it (pid %d)", c.proc.Name(), cmd.Process.Pid)
		if err := c.proc.askStop(cmd.Process, tree); err != nil {
			s.cannotAsk(c.proc, err)
			s.killRun(c, cmd, 0)
			return nil
		}
		go func() {
			time.Sleep(wantStopGrace)
			s.killRun(c, cmd, wantStopGrace)
		}()
	}
	return nil
}

// wantStopGrace is how long Want(off) waits for the instance to stop before it kills it.
const wantStopGrace = 10 * time.Second

// killRun kills c's process tree while cmd is still its running process (not
// a later run). waited is how long it was given to stop once asked (0: it was not
// asked, cannotAsk said so): a child that ignored the ask is logged.
func (s *Supervisor) killRun(c *child, cmd *exec.Cmd, waited time.Duration) {
	s.mu.Lock()
	still := c.cmd == cmd
	tree := c.tree
	s.mu.Unlock()
	if still {
		if waited > 0 {
			s.warnf("%s (pid %d) did not stop within %s; killing it", c.proc.Name(), cmd.Process.Pid, waited)
		}
		tree.kill()
		_ = cmd.Process.Kill()
	}
}
