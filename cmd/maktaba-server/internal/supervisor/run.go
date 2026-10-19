package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
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
	s.client = &http.Client{Timeout: policy.ProbeTimeout}
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
func (s *Supervisor) Run(ctx context.Context, grace time.Duration) error {
	if len(s.children) == 0 {
		return errors.New("no processes to run")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for _, c := range s.children {
		wg.Add(1)
		go func(c *child) {
			defer wg.Done()
			s.loop(runCtx, c)
		}(c)
	}

	var result error
	select {
	case <-ctx.Done():
	case result = <-s.fatal:
	}

	// Graceful: stop restarting, ask every running child to drain, then
	// hard-kill stragglers after grace.
	s.mu.Lock()
	s.stopping = true
	cancel()
	s.signalLocked(terminationSignal())
	s.mu.Unlock()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		s.mu.Lock()
		s.signalLocked(os.Kill)
		s.mu.Unlock()
		<-done
	}
	return result
}

// signalLocked signals every running child. A kill takes the child's
// whole process tree; a graceful signal goes to the child only, which
// stops its own children as it drains.
func (s *Supervisor) signalLocked(sig os.Signal) {
	for _, c := range s.children {
		if c.cmd != nil && c.cmd.Process != nil {
			if sig == os.Kill {
				c.tree.kill()
			}
			_ = c.cmd.Process.Signal(sig)
		}
	}
}

// loop runs one child until shutdown, restarting it per the policy.
func (s *Supervisor) loop(ctx context.Context, c *child) {
	backoff := s.policy.BackoffInitial
	tpl := c.proc.Cmd
	first := true
	for {
		cmd := tpl
		if !first {
			cmd = cloneCmd(tpl)
		}
		first = false

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
		startErr := applyBinding(cmd, c.binding, c.basePath)
		if startErr == nil {
			prepareTree(cmd)
			startErr = cmd.Start()
		}
		if err := startErr; err != nil {
			info = ExitInfo{At: startedAt, Code: -1, Reason: ReasonStartFailed, Error: err.Error()}
			c.status.LastExit = &info
			c.status.PID = 0
			c.status.StartedAt = nil
			s.mu.Unlock()
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventExited, Reason: ReasonStartFailed, Message: err.Error()})
			s.logf("%s could not start: %v", c.proc.Name(), err)
		} else {
			pid := cmd.Process.Pid
			tree, treeErr := attachTree(cmd)
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

			probeCtx, stopProbe := context.WithCancel(ctx)
			if c.proc.Probe != "" {
				go s.probe(probeCtx, c, cmd, startedAt)
			}
			waitErr := cmd.Wait()
			stopProbe()
			// The child is gone: end whatever it left behind (ffmpeg,
			// yt-dlp) so a restart never runs next to orphans.
			tree.release()
			info = s.exitInfo(c, cmd, waitErr, startedAt)
		}

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
		s.mu.Unlock()
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
			continue // waits until it is wanted again
		}
		if stopping {
			s.logf("%s stopped (pid %d, %s)", c.proc.Name(), info.PID, describeExit(info))
			s.setState(c, StateStopped)
			return
		}

		// Decide what happens next.
		switch {
		case s.policy.Restart == RestartNever:
			s.logf("%s exited (pid %d, %s); restart = never, stopping the stack", c.proc.Name(), info.PID, describeExit(info))
			s.setState(c, StateExited)
			s.fatalOnce.Do(func() { s.fatal <- fmt.Errorf("%s exited: %s", c.proc.Name(), describeExit(info)) })
			return
		case s.policy.Restart == RestartOnFailure && info.Reason == ReasonClean:
			s.logf("%s exited cleanly (pid %d, uptime %s); restart = on-failure, leaving it stopped",
				c.proc.Name(), info.PID, uptime(info))
			s.setState(c, StateExited)
			return
		}

		now := s.now()
		s.mu.Lock()
		if time.Duration(info.UptimeSec*float64(time.Second)) >= s.policy.HealthyAfter {
			// It ran long enough to count as healthy: forget the past.
			backoff = s.policy.BackoffInitial
			c.exits = nil
			c.failed = false
		}
		c.exits = append(c.recentExits(now, s.policy.Window), now)
		delay, state := backoff, StateBackoff
		wasFailed := c.failed
		if len(c.exits) >= s.policy.MaxRestarts {
			delay, state = s.policy.FailedRetry, StateFailed
			c.failed = true
		} else {
			c.failed = false
			backoff = nextBackoff(backoff, s.policy.BackoffMax)
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
			s.logf("%s exited (pid %d, %s); crash loop: %d exits in %s, marked failed, retrying in %s",
				c.proc.Name(), info.PID, describeExit(info), s.policy.MaxRestarts, s.policy.Window, delay)
		} else {
			s.logf("%s exited (pid %d, %s); restarting in %s", c.proc.Name(), info.PID, describeExit(info), delay)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.setState(c, StateStopped)
			return
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
	}
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
		err := s.check(ctx, c.proc.Probe)
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
			s.logf("%s (pid %d) failed %d liveness probes in a row (%v); killing it",
				c.proc.Name(), cmd.Process.Pid, failures, err)
			s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventHung, PID: cmd.Process.Pid, Reason: ReasonHang,
				Message: err.Error()})
			s.mu.Lock()
			tree := c.tree
			s.mu.Unlock()
			tree.kill()
			_ = cmd.Process.Kill()
			return
		}
	}
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
			s.logf("event log write failed: %v", err)
		}
	}
}

func (s *Supervisor) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "[supervisor] "+format+"\n", args...)
}

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
	cmd := c.cmd
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
		_ = cmd.Process.Signal(terminationSignal())
		go func() {
			time.Sleep(10 * time.Second)
			s.mu.Lock()
			still := c.cmd == cmd
			tree := c.tree
			s.mu.Unlock()
			if still {
				tree.kill()
				_ = cmd.Process.Kill()
			}
		}()
	}
	return nil
}
