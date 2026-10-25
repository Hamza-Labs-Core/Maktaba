package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// The TV handover across a supervisor restart (Epic 35 story 35.7, design §8.1).
//
// A process that Survives (the streaming role: the TV front door; the tv-slot
// instances) is listed in the launcher's updates/handover.json while it runs
// (layout.Handover): its pid, the OS's start time of that pid, and its process group
// (POSIX) or named job (Windows). HandOver makes Run end without stopping those: every
// other process is stopped as at a shutdown, the record is marked for the next
// supervisor, and serve exits 75. The next supervisor adopts each one that is still the
// same process and answers its probe, and supervises it by probe and by polling (it is
// not its parent: it cannot wait for it); one that ends, or must restart, is replaced
// by an ordinary child. One that cannot be adopted is started afresh, and the
// supervisor_adopt_failed event says why.

// Event kinds of the handover.
const (
	EventAdopted     = "adopted"
	EventAdoptFailed = "supervisor_adopt_failed"
)

// adoptPoll is how often an adopted child is checked to still run (a var: the tests).
var adoptPoll = 250 * time.Millisecond

// adoptProbeFor is how long an adoption waits for the child's probe to answer.
var adoptProbeFor = 5 * time.Second

// errAdoptedGone is the "wait" error of an adopted child that ended: the supervisor saw
// it go, it never had its exit status.
var errAdoptedGone = errors.New("the adopted process ended")

// handoverState is the handover's part of a Supervisor (nil: no record kept).
type handoverState struct {
	dir         string // the launcher's updates directory
	launcherPID int

	job    *tvJob // Windows: the boot's named job (set at Run's start)
	jobErr error
	// launcherKeeps: the launcher speaks the handover (layout.EnvLauncherHandover): it
	// leaves what a handover lists running, and on Windows keeps the job it runs the
	// server in across it, so the TV processes need not leave that job
	launcherKeeps bool

	// record writes, one at a time
	recordMu sync.Mutex

	// handingOver: Run ends without stopping the survivors; handedOver: it did (the
	// record is the next supervisor's from then on). Guarded by the supervisor's mu.
	handingOver bool
	handedOver  bool
	// refused: a survivor could not leave the launcher's job (Windows): it ends with
	// this supervisor. Guarded by the supervisor's mu.
	refused string
}

// WithHandover keeps the handover record in dir (the updates directory of the launcher
// that started this server) and adopts, at Run's start, the processes it lists.
// launcherPID is that launcher (Windows: it holds the handle that keeps the TV
// processes' job alive across the restart). dir "" keeps no record (a server no
// launcher started).
func WithHandover(dir string, launcherPID int) Option {
	return func(s *Supervisor) {
		if dir != "" {
			s.ho = &handoverState{dir: dir, launcherPID: launcherPID}
		}
	}
}

// CanHandOver reports whether the processes that may outlive the supervisor can do so
// now: a record is kept, and on Windows the launcher holds their job and every one of
// them runs in it. why says what stands in the way.
func (s *Supervisor) CanHandOver() (bool, string) {
	h := s.ho
	if h == nil {
		return false, "no handover record is kept (no launcher started this server)"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Survives && c.cmd != nil && !c.detached {
			why := c.proc.Name() + " runs where it cannot outlive the supervisor"
			switch {
			case h.refused != "":
				why = h.refused
			case h.jobErr != nil:
				why += ": " + h.jobErr.Error()
			}
			return false, why
		}
	}
	return true, ""
}

// How a survivor is started so it may outlive the supervisor: in the boot's named job,
// out of the launcher's (Windows, under a launcher from before the handover, which
// closes its job when the server exits: the child must leave it, and the launcher holds
// the boot's job); or where it is, under a launcher that keeps its job across a handover
// (and on POSIX always: a child leads its own process group).
const (
	surviveNot  = ""
	surviveBoot = "boot"
	surviveHere = "here"
)

// survivalLocked is how c is started. Caller holds s.mu.
func (s *Supervisor) survivalLocked(c *child) string {
	h := s.ho
	switch {
	case !c.proc.Survives || h == nil:
		return surviveNot
	case h.launcherKeeps:
		return surviveHere
	case h.job.held() && h.refused == "":
		return surviveBoot
	}
	return surviveNot
}

// HandOver makes Run end without stopping the processes that may outlive the supervisor
// (the TV front door and the slots): the others are stopped as at a shutdown and the
// record is written for the next supervisor. The caller then cancels Run's context and
// exits 75. It reports false, and changes nothing, when they cannot outlive the
// supervisor here (CanHandOver): Run then stops every process as usual.
func (s *Supervisor) HandOver() (bool, string) {
	if ok, why := s.CanHandOver(); !ok {
		return false, why
	}
	s.mu.Lock()
	s.ho.handingOver = true
	s.mu.Unlock()
	return true, ""
}

// HandedOver reports whether Run ended on a handover.
func (s *Supervisor) HandedOver() bool {
	if s.ho == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ho.handedOver
}

// SurvivorRelease is the release the running survivor of role (its first instance) runs
// its program from, "" when none runs or its program is from no release directory.
func (s *Supervisor) SurvivorRelease(role Role) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.children {
		if c.proc.Role == role && c.proc.Survives && c.cmd != nil {
			return releaseOfPath(c.cmd.Path)
		}
	}
	return ""
}

// startHandover runs at Run's start: the boot's job (Windows), then the record the
// supervisor before left. Each entry is handed to the process it names (adopted in its
// loop); one this supervisor runs no process for is ended (when it is still that
// process); a record of an earlier boot is removed (story 35.7 EC3).
func (s *Supervisor) startHandover() {
	h := s.ho
	if h == nil {
		return
	}
	keeps := os.Getenv(layout.EnvLauncherHandover) == "1"
	pid := h.launcherPID
	if keeps {
		pid = 0 // the launcher keeps its own job: the boot's is only swept
	}
	job, jobErr := openTVJob(h.dir, pid)
	s.mu.Lock()
	h.job, h.jobErr, h.launcherKeeps = job, jobErr, keeps
	s.mu.Unlock()
	if jobErr != nil {
		s.logf("the TV processes cannot outlive a restart of the supervisor here: %v", jobErr)
	}
	rec, err := layout.ReadHandover(h.dir)
	switch {
	case err != nil:
		s.logf("the handover record cannot be read (%v): removed", err)
		_ = layout.RemoveHandover(h.dir)
		return
	case rec == nil:
		s.sweepJob(nil)
		return
	case !rec.CurrentBoot():
		s.logf("the handover record is of an earlier boot: removed")
		_ = layout.RemoveHandover(h.dir)
		s.sweepJob(nil)
		return
	}
	var candidates []layout.HandoverChild
	s.mu.Lock()
	for _, e := range rec.Children {
		taken := false
		for _, c := range s.children {
			if c.proc.Survives && string(c.proc.Role) == e.Role && c.proc.Instance == e.Instance && c.adopt == nil {
				e := e
				c.adopt = &e
				taken = true
				break
			}
		}
		if taken && layout.ProcessIs(e.PID, e.Start) {
			candidates = append(candidates, e)
		} else if !taken {
			go s.endEntry(e, "this release runs no such process")
		}
	}
	s.mu.Unlock()
	s.sweepJob(candidates)
}

// sweepJob ends what the boot's job holds beyond the processes to adopt and their
// descendants: what a supervisor that ended without a handover left (Windows).
func (s *Supervisor) sweepJob(keep []layout.HandoverChild) {
	h := s.ho
	if h.job == nil {
		return
	}
	roots := make([]int, 0, len(keep))
	for _, c := range keep {
		roots = append(roots, c.PID)
	}
	below := descendants(roots) // what they started (ffmpeg), their jobs gone or not
	h.job.sweep(func(pid int) bool {
		if below[pid] {
			return true
		}
		for _, c := range keep {
			if pid == c.PID || (c.Job != "" && inChildJob(c.Job, pid)) {
				return true
			}
		}
		return false
	})
}

// endEntry ends a process the record lists that is not adopted, while it is still that
// process (never one that reuses its pid).
func (s *Supervisor) endEntry(e layout.HandoverChild, why string) {
	if !layout.ProcessIs(e.PID, e.Start) {
		return
	}
	tree, p, err := adoptTree(e, s.ho.dir, s.ho.launcherPID)
	if err != nil {
		return
	}
	s.logf("%s (pid %d) from the supervisor before is ended: %s", nameOf(e), e.PID, why)
	tree.kill()
	_ = p.Kill()
	tree.release()
}

// adminOf is the admin address a probe URL names (story 35.7 AC1: the record lists it).
func adminOf(probe string) string {
	u, err := url.Parse(probe)
	if err != nil {
		return ""
	}
	return u.Host
}

func nameOf(e layout.HandoverChild) string {
	if e.Instance != "" {
		return e.Role + "/" + e.Instance
	}
	return e.Role
}

// adoptRun adopts c's process from the record (a) and supervises it until it is gone:
// its probe as for any child, its liveness by polling. ok false: it could not be
// adopted (logged, supervisor_adopt_failed), and the loop starts it afresh.
func (s *Supervisor) adoptRun(ctx context.Context, c *child, a layout.HandoverChild) (ExitInfo, bool) {
	var tree *procTree
	var p *os.Process
	why := ""
	if !layout.ProcessIs(a.PID, a.Start) {
		why = "its process is gone (or its pid is another process's now)"
	} else if t, pp, err := adoptTree(a, s.ho.dir, s.ho.launcherPID); err != nil {
		why = err.Error()
	} else if err := s.adoptProbe(ctx, c.proc); err != nil {
		why = "its probe does not answer: " + err.Error()
		// it is this install's and unwell: ended before a fresh one takes its port
		t.kill()
		_ = pp.Kill()
		watchGoneFor(a.PID, a.Start, t, 10*time.Second)
		t.release()
	} else {
		tree, p = t, pp
	}
	if tree == nil {
		s.logf("%s (pid %d) could not be adopted: %s; starting it afresh", c.proc.Name(), a.PID, why)
		s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventAdoptFailed, PID: a.PID, Message: why})
		return ExitInfo{}, false
	}
	cmd := &exec.Cmd{Path: a.Binary, Process: p}
	startedAt := s.now()
	var binding *SlotBinding
	if len(a.Binding) > 0 {
		var b SlotBinding
		if json.Unmarshal(a.Binding, &b) == nil {
			binding = &b
		}
	}
	s.mu.Lock()
	c.cmd, c.tree, c.ident, c.job, c.detached = cmd, tree, a.Start, a.Job, true
	c.killReason = ""
	if binding != nil {
		c.binding = binding
	}
	if a.Wanted {
		c.wanted, c.status.Wanted = true, true
	}
	c.status.PID = a.PID
	c.status.StartedAt = &startedAt
	c.status.NextRestartAt = nil
	c.status.LastProbe = nil
	c.status.State = StateRunning
	c.status.Adopted = true
	s.mu.Unlock()
	s.emit(Event{Role: c.proc.Role, Instance: c.proc.Instance, Kind: EventAdopted, PID: a.PID,
		Message: "adopted from the supervisor before (it kept running across the restart)"})
	s.logf("adopted %s (pid %d): it kept running across the supervisor's restart", c.proc.Name(), a.PID)
	s.saveRecord(false)

	probeCtx, stopProbe := context.WithCancel(ctx)
	if c.proc.Probe != "" || c.proc.Check != nil {
		go s.probe(probeCtx, c, cmd, startedAt)
	}
	watchGone(a.PID, a.Start, tree)
	stopProbe()
	tree.release()
	info := s.exitInfo(c, cmd, errAdoptedGone, startedAt)
	s.mu.Lock()
	c.status.Adopted = false
	s.mu.Unlock()
	return info, true
}

// adoptProbe waits up to adoptProbeFor for a child's liveness check to answer.
func (s *Supervisor) adoptProbe(ctx context.Context, p Process) error {
	if p.Probe == "" && p.Check == nil {
		return nil
	}
	deadline := time.Now().Add(adoptProbeFor)
	for {
		err := s.checkChild(ctx, p)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// watchGoneFor is watchGone bounded by d.
func watchGoneFor(pid int, start uint64, tree *procTree, d time.Duration) {
	done := make(chan struct{})
	go func() { watchGone(pid, start, tree); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// saveRecord writes the record of the survivors that run now (handover: for the next
// supervisor to adopt). With none it removes the record. After a handover the record is
// the next supervisor's: this one writes no more.
func (s *Supervisor) saveRecord(handover bool) {
	h := s.ho
	if h == nil {
		return
	}
	h.recordMu.Lock()
	defer h.recordMu.Unlock()
	rec := layout.Handover{BootID: layout.BootID(), Writer: os.Getpid(), Handover: handover, WrittenAt: s.now().UTC()}
	s.mu.Lock()
	if h.handedOver && !handover {
		s.mu.Unlock()
		return
	}
	for _, c := range s.children {
		if !c.proc.Survives || c.cmd == nil || c.cmd.Process == nil || c.ident == 0 {
			continue
		}
		e := layout.HandoverChild{Role: string(c.proc.Role), Instance: c.proc.Instance, PID: c.cmd.Process.Pid,
			Admin: adminOf(c.proc.Probe),
			Start: c.ident, Job: c.job, Probe: c.proc.Probe, Binary: c.cmd.Path, Release: releaseOfPath(c.cmd.Path),
			Wanted: c.proc.OnDemand && c.wanted}
		if runtime.GOOS != "windows" {
			e.PGID = e.PID // it leads its own process group (prepareTree)
		}
		if held := heldOf(c.tree); held != 0 {
			e.JobHeld, e.JobHolder = held, h.launcherPID
		}
		if c.binding != nil {
			if b, err := json.Marshal(c.binding); err == nil {
				e.Binding = b
			}
		}
		rec.Children = append(rec.Children, e)
	}
	s.mu.Unlock()
	var err error
	if len(rec.Children) == 0 {
		err = layout.RemoveHandover(h.dir)
	} else {
		err = layout.WriteHandover(h.dir, rec)
	}
	if err != nil {
		s.logf("the handover record could not be written: %v", err)
	}
}

// identify records the identity of a survivor that just started (the OS's start time of
// its pid) for the record. Caller holds s.mu.
func identify(c *child, pid int) {
	c.ident = 0
	if st, err := layout.ProcessStart(pid); err == nil {
		c.ident = st
	}
}
