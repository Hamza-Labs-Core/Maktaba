package updater

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// world is what a machine's disk holds across its restarts: the release the next start
// runs, what is staged, what the database record says.
type world struct {
	mu        sync.Mutex
	next      string // the release the next start runs
	staged    map[string]bool
	calls     []string
	migrated  bool // the database holds the new release's migrations
	marked    bool // the record says: put the dump back
	committed string
	exits     []int
	// the desktop app's side of an order (an installer that asks)
	order     string // the run ordered ("" withdrawn)
	answer    string // ordered, staged, clicked
	answerErr error
	extends   int // the launcher's guard pushed on (Extender)
}

// app answers the order like the desktop app.
func (w *world) app(answer string) {
	w.mu.Lock()
	w.answer = answer
	w.mu.Unlock()
}

func newWorld(running string) *world { return &world{next: running, staged: map[string]bool{}} }

func (w *world) call(c string) {
	w.mu.Lock()
	w.calls = append(w.calls, c)
	w.mu.Unlock()
}

func (w *world) called(c string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, x := range w.calls {
		if x == c {
			return true
		}
	}
	return false
}

// fakeInst is an installer over a world. dieAt "before:<step>" / "after:<step>" makes the
// process die there: the goroutine ends on the spot (nothing more is written by it).
type fakeInst struct {
	w       *world
	running string
	failAt  string
	// failTimes > 0: failAt fails only its first failTimes calls
	failTimes, failed int
	dieAt             string
	died              chan struct{}
	once              sync.Once
	// asks: a desktop app's install (Asker)
	asks bool
	// replaces: its launcher is replaced when a release needs a newer one
	replaces bool
}

// Extend implements Extender.
func (f *fakeInst) Extend(*Journal, time.Time) error {
	f.w.mu.Lock()
	f.w.extends++
	f.w.mu.Unlock()
	return nil
}

func (f *fakeInst) die(where string) {
	if f.dieAt == where {
		f.once.Do(func() { close(f.died) })
		runtime.Goexit() // the process is gone
	}
}

func (f *fakeInst) step(name string, effect func()) error {
	f.die("before:" + name)
	if f.failAt == name {
		f.w.mu.Lock()
		fail := f.failTimes == 0 || f.failed < f.failTimes
		f.failed++
		f.w.mu.Unlock()
		if fail {
			return Fail(name+"_broken", "the fake fails %s", name)
		}
	}
	f.w.call(name)
	if effect != nil {
		f.w.mu.Lock()
		effect()
		f.w.mu.Unlock()
	}
	f.die("after:" + name)
	return nil
}

func (f *fakeInst) Kind() string    { return "fake" }
func (f *fakeInst) Running() string { return f.running }
func (f *fakeInst) Preflight(context.Context, *Journal) error {
	return f.step(StepPreflight, nil)
}
func (f *fakeInst) Stage(_ context.Context, j *Journal, _ io.Writer) error {
	err := f.step(StepStage, func() { f.w.staged[j.Job.Version] = true })
	if err == nil {
		j.Staged, j.FromRef = "staged:"+j.Job.Version, "ref:"+j.From
	}
	return err
}
func (f *fakeInst) Smoke(context.Context, *Journal) error { return f.step(StepSmoke, nil) }
func (f *fakeInst) Tools(*Journal) (string, string) {
	return "/fake/maktaba-api", "/fake/migrations"
}
func (f *fakeInst) Switch(_ context.Context, j *Journal) error {
	return f.step(StepSwitch, func() { f.w.next = j.Job.Version })
}
func (f *fakeInst) Restart(_ context.Context, _ *Journal, exit func(int)) error {
	err := f.step(StepRestart, nil)
	if err == nil {
		exit(75)
	}
	return err
}
func (f *fakeInst) Revert(_ context.Context, j *Journal) error {
	return f.step("revert", func() { f.w.next = j.From })
}
func (f *fakeInst) Commit(_ context.Context, j *Journal) error {
	return f.step(StepCommit, func() { f.w.committed = j.Job.Version })
}
func (f *fakeInst) Discard(_ context.Context, j *Journal) error {
	return f.step("discard", func() { delete(f.w.staged, j.Job.Version); f.w.order, f.w.answer = "", "" })
}
func (f *fakeInst) Asks() bool { return f.asks }
func (f *fakeInst) ReplaceLauncher(*Journal) (bool, error) {
	if !f.replaces {
		return false, nil
	}
	return true, f.step("launcher", nil)
}
func (f *fakeInst) Ask(j *Journal) error {
	return f.step(StepAsk, func() {
		if f.w.order != j.Job.RunID {
			f.w.order, f.w.answer = j.Job.RunID, RequestOrdered
		}
	})
}
func (f *fakeInst) Answer(j *Journal) (string, error) {
	f.w.mu.Lock()
	defer f.w.mu.Unlock()
	if f.w.answerErr != nil {
		return "", f.w.answerErr
	}
	if f.w.order != j.Job.RunID {
		return "", Fail("app_order_gone", "gone")
	}
	return f.w.answer, nil
}

type fakeRecords struct{ w *world }

func (r fakeRecords) MarkRestore(_, _, _ string) (bool, error) {
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	if !r.w.migrated {
		return false, nil
	}
	r.w.marked = true
	return true, nil
}
func (r fakeRecords) Unmark(string) error {
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	r.w.marked = false
	return nil
}
func (r fakeRecords) Migrated(string) bool {
	r.w.mu.Lock()
	defer r.w.mu.Unlock()
	return r.w.migrated
}

type fakeRehearser struct {
	err error
	n   int
}

func (r *fakeRehearser) Rehearse(context.Context, string, string, int, io.Writer) (Rehearsal, error) {
	r.n++
	return Rehearsal{Pending: 2, MigrateSec: 0.5}, r.err
}
func (r *fakeRehearser) DumpAside(context.Context, string) (string, error) {
	return "/fake/failed.dump", nil
}
func (r *fakeRehearser) Check(context.Context, bool, int) error { return nil }

type fakeHealth struct {
	mu sync.Mutex
	v  Verdict
	// moving: a start step that makes progress (its Progress changes every check)
	moving bool
	n      int
	// turnAt > 0: the verdict becomes turn at that check
	checks, turnAt int
	turn           Verdict
}

func (h *fakeHealth) set(v Verdict) { h.mu.Lock(); h.v = v; h.mu.Unlock() }
func (h *fakeHealth) setMoving()    { h.mu.Lock(); h.moving = true; h.mu.Unlock() }
func (h *fakeHealth) turnAfter(n int, v Verdict) {
	h.mu.Lock()
	h.turnAt, h.turn = h.checks+n, v
	h.mu.Unlock()
}
func (h *fakeHealth) Check(context.Context, string) Verdict {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks++
	if h.turnAt > 0 && h.checks >= h.turnAt {
		h.v, h.turnAt = h.turn, 0
	}
	v := h.v
	if h.moving && v.Pending {
		h.n++
		v.Progress = strconv.Itoa(h.n)
	}
	return v
}

// rig is one machine's process over a world.
type rig struct {
	t      *testing.T
	dir    string
	w      *world
	inst   *fakeInst
	health *fakeHealth
	m      *Machine
	exited chan int
	cancel context.CancelFunc
}

// clock is a fake clock that moves a minute every time it is read (a 10-minute watch
// passes in about ten checks).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Minute)
	return c.t
}

func startRig(t *testing.T, dir string, w *world, health Verdict) *rig {
	t.Helper()
	r := &rig{t: t, dir: dir, w: w, exited: make(chan int, 4)}
	w.mu.Lock()
	running := w.next
	w.mu.Unlock()
	r.inst = &fakeInst{w: w, running: running, died: make(chan struct{})}
	r.health = &fakeHealth{v: health}
	c := &clock{t: time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC)}
	r.m = &Machine{Dir: dir, Installer: r.inst, Health: r.health, Records: fakeRecords{w}, Rehearser: &fakeRehearser{},
		Now: c.now, CheckEvery: time.Millisecond, Exit: func(code int) {
			w.mu.Lock()
			w.exits = append(w.exits, code)
			w.mu.Unlock()
			r.exited <- code
		}}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	t.Cleanup(func() {
		cancel()
		// the goroutines that did not "die" end at once; a dead one never writes again
		done := make(chan struct{})
		go func() { r.m.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	r.m.Start(ctx)
	return r
}

// restart ends this process (abandoning a goroutine that died) and starts the next one
// on the world's next release, resuming the journal.
func (r *rig) restart(health Verdict) *rig { return r.restartDying(health, "") }

// restartDying is restart with the next process dying at dieAt.
func (r *rig) restartDying(health Verdict, dieAt string) *rig {
	r.cancel()
	n := startRig(r.t, r.dir, r.w, health)
	n.inst.dieAt = dieAt
	n.m.Resume()
	return n
}

func (r *rig) waitState(want string) *Journal {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if j := r.m.Status().Journal; j != nil && j.State == want {
			return j
		}
		time.Sleep(2 * time.Millisecond)
	}
	j := r.m.Status().Journal
	if j == nil {
		r.t.Fatalf("no run, want %s", want)
	}
	r.t.Fatalf("state %s (error %+v, steps %+v), want %s", j.State, j.Error, j.Steps, want)
	return nil
}

func (r *rig) waitExit() {
	r.t.Helper()
	select {
	case <-r.exited:
	case <-time.After(5 * time.Second):
		r.t.Fatal("no exit 75")
	}
}

func homeJob() Job {
	return Job{RunID: "run-1", Version: "1.5.0", Tag: "v1.5.0", Home: true, Rehearse: true,
		Assets: map[string]string{"release.json": "https://example/release.json"}}
}

var healthy = Verdict{Healthy: true}

func TestPrepareApplyWatchCommit(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepared)
	if j.From != "1.4.0" || j.Staged != "staged:1.5.0" || j.Rehearsal == nil || j.Rehearsal.Pending != 2 {
		t.Fatalf("prepared journal: %+v", j)
	}
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitExit()
	r.waitState(StateRestarting)
	w.migrated = true // the new release's database step migrated at its start
	n := r.restart(healthy)
	n.waitState(StateCommitted)
	if w.committed != "1.5.0" || w.marked {
		t.Fatalf("committed %q marked %v", w.committed, w.marked)
	}
}

func TestPrepareFailureLeavesNothing(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.inst.failAt = StepSmoke
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepareFailed)
	if j.Error == nil || j.Error.Step != StepSmoke || j.Error.Code != "smoke_broken" {
		t.Fatalf("error %+v", j.Error)
	}
	r.m.Wait()
	if w.staged["1.5.0"] || !w.called("discard") || w.next != "1.4.0" {
		t.Fatalf("staged %v discard %v next %s", w.staged, w.called("discard"), w.next)
	}
}

func TestRehearsalFailureBlocksPrepare(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.m.Rehearser = &fakeRehearser{err: Fail("rehearsal_failed", "0149 failed on the copy")}
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepareFailed)
	if j.Error.Code != "rehearsal_failed" || j.Error.Step != StepRehearse {
		t.Fatalf("error %+v", j.Error)
	}
}

func TestNodeSkipsRehearsal(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	reh := &fakeRehearser{}
	r.m.Rehearser = reh
	job := homeJob()
	job.Home = false
	if err := r.m.Prepare(job); err != nil {
		t.Fatal(err)
	}
	j := r.waitState(StatePrepared)
	if reh.n != 0 || j.Steps[len(j.Steps)-1].State != "skipped" {
		t.Fatalf("rehearsals %d steps %+v", reh.n, j.Steps)
	}
}

func TestWatchUnhealthyRollsBackAndRestoresTheDump(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	w.migrated = true
	n := r.restart(Verdict{Why: "api /readyz 503"})
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Reason != "api /readyz 503" || !w.marked || w.next != "1.4.0" {
		t.Fatalf("reason %q marked %v next %s", j.Reason, w.marked, w.next)
	}
	old := n.restart(healthy)
	j = old.waitState(StateRolledBack)
	if w.committed != "" {
		t.Fatalf("committed %q", w.committed)
	}
	if j.Steps[len(j.Steps)-1].Step != StepRollback {
		t.Fatalf("steps %+v", j.Steps)
	}
}

func TestWatchFatalRollsBackAtOnce(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := r.restart(Verdict{Fatal: true, Why: "api crash loop"})
	n.waitExit()
	n.waitState(StateRollingBack)
	if w.marked { // nothing migrated: nothing to put back
		t.Fatal("marked without migrations")
	}
}

func TestWatchRestartsTooOftenRollsBack(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	unhealthy := Verdict{Why: "starting"}
	n := r.restart(unhealthy)
	n.waitState(StateWatching)
	for i := 0; i < 2; i++ {
		n = n.restart(unhealthy)
		n.waitState(StateWatching)
	}
	n = n.restart(unhealthy) // the fourth start of the new release
	n.waitExit()
	j := n.waitState(StateRollingBack)
	if j.Reason != "restarts" {
		t.Fatalf("reason %q", j.Reason)
	}
}

func TestAbortBeforeSwitchOnly(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	if err := r.m.Abort("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitState(StateAborted)
	if w.staged["1.5.0"] {
		t.Fatal("staged release left")
	}

	w2 := newWorld("1.4.0")
	r2 := startRig(t, t.TempDir(), w2, healthy)
	_ = r2.m.Prepare(homeJob())
	r2.waitState(StatePrepared)
	_ = r2.m.Apply("run-1")
	r2.waitExit()
	if err := r2.m.Abort("run-1"); !errors.Is(err, ErrPastSwitch) {
		t.Fatalf("abort after switch: %v", err)
	}
}

func TestOneRunAtATime(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	r.inst.dieAt = "before:" + StepSmoke // keeps the prepare under way
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	<-r.inst.died
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatalf("same run again: %v", err)
	}
	other := homeJob()
	other.RunID = "run-2"
	if err := r.m.Prepare(other); !errors.Is(err, ErrBusy) {
		t.Fatalf("second run: %v", err)
	}
}

func TestNeverADowngrade(t *testing.T) {
	r := startRig(t, t.TempDir(), newWorld("1.6.0"), healthy)
	if err := r.m.Prepare(homeJob()); !errors.Is(err, ErrAhead) {
		t.Fatalf("prepare older: %v", err)
	}
}

func TestAlreadyRunningCommitsWithoutRestart(t *testing.T) {
	w := newWorld("1.5.0")
	r := startRig(t, t.TempDir(), w, healthy)
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
	r.waitState(StatePrepared)
	if err := r.m.Apply("run-1"); err != nil {
		t.Fatal(err)
	}
	r.waitState(StateCommitted)
	if w.called(StepSwitch) || len(w.exits) != 0 {
		t.Fatalf("calls %v exits %v", w.calls, w.exits)
	}
}

func TestRestartFailureRevertsAndNothingChanged(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	_ = r.m.Prepare(homeJob())
	r.waitState(StatePrepared)
	r.inst.failAt = StepRestart // the app would not install
	_ = r.m.Apply("run-1")
	j := r.waitState(StateApplyFailed)
	if w.next != "1.4.0" || j.Error.Code != "restart_broken" {
		t.Fatalf("next %s error %+v", w.next, j.Error)
	}
}

func TestRollbackCommitted(t *testing.T) {
	w := newWorld("1.4.0")
	r := startRig(t, t.TempDir(), w, healthy)
	job := homeJob()
	job.Home = false
	_ = r.m.Prepare(job)
	r.waitState(StatePrepared)
	_ = r.m.Apply("run-1")
	r.waitExit()
	n := r.restart(healthy)
	n.waitState(StateCommitted)
	if err := n.m.RollbackCommitted("run-1"); err != nil {
		t.Fatal(err)
	}
	n.waitExit()
	n.waitState(StateRollingBack)
	old := n.restart(healthy)
	old.waitState(StateRolledBack)

	// a home whose database the release migrated: refused
	w2 := newWorld("1.4.0")
	r2 := startRig(t, t.TempDir(), w2, healthy)
	_ = r2.m.Prepare(homeJob())
	r2.waitState(StatePrepared)
	_ = r2.m.Apply("run-1")
	r2.waitExit()
	w2.migrated = true
	n2 := r2.restart(healthy)
	n2.waitState(StateCommitted)
	if err := n2.m.RollbackCommitted("run-1"); !errors.Is(err, ErrNoRollback) {
		t.Fatalf("home rollback: %v", err)
	}
}

// TestResumeAfterACrashAtEveryStep cuts the process at every step, before and after its
// side effects, starts the next process on whatever release the disk says, and checks
// the run still ends where it must: prepared again for a cut prepare, nothing changed
// for a cut before the restart was handed over, committed (healthy) after it.
func TestResumeAfterACrashAtEveryStep(t *testing.T) {
	for _, step := range []string{StepPreflight, StepStage, StepSmoke, StepSwitch, StepRestart, StepCommit} {
		for _, when := range []string{"before", "after"} {
			t.Run(when+"_"+step, func(t *testing.T) {
				w := newWorld("1.4.0")
				r := startRig(t, t.TempDir(), w, healthy)
				r.inst.dieAt = when + ":" + step
				if err := r.m.Prepare(homeJob()); err != nil {
					t.Fatal(err)
				}
				prepCut := step == StepPreflight || step == StepStage || step == StepSmoke
				if prepCut {
					<-r.inst.died
					n := r.restart(healthy)
					n.waitState(StatePrepared)
					if err := n.m.Apply("run-1"); err != nil {
						t.Fatal(err)
					}
					n.waitExit()
					n.restart(healthy).waitState(StateCommitted)
					if w.committed != "1.5.0" {
						t.Fatalf("committed %q", w.committed)
					}
					return
				}
				r.waitState(StatePrepared)
				if err := r.m.Apply("run-1"); err != nil {
					t.Fatal(err)
				}
				switch step {
				case StepSwitch:
					<-r.inst.died
					n := r.restart(healthy)
					if when == "before" {
						// the switch never happened: the old release runs, nothing installed
						n.waitState(StateApplyFailed)
						if w.next != "1.4.0" {
							t.Fatalf("next %s", w.next)
						}
						return
					}
					// switched but the journal still says applying: the next start is the
					// new release (the launcher read the pointer), which watches
					n.waitState(StateCommitted)
				case StepRestart:
					<-r.inst.died
					n := r.restart(healthy)
					// the pointer moved before the restart: the new release comes up
					n.waitState(StateCommitted)
				case StepCommit:
					r.waitExit()
					n := r.restartDying(healthy, when+":"+step)
					<-n.inst.died // cut inside the commit
					n2 := n.restart(healthy)
					n2.waitState(StateCommitted)
				}
				if w.next != "1.5.0" {
					t.Fatalf("next %s", w.next)
				}
			})
		}
	}
}

func TestResumeOfEndedRunsChangesNothing(t *testing.T) {
	for _, state := range []string{StateCommitted, StateRolledBack, StateApplyFailed, StatePrepareFailed, StateAborted, StateFailed} {
		dir := t.TempDir()
		j := &Journal{Job: homeJob(), From: "1.4.0", State: state, Steps: []StepRecord{}}
		if err := SaveJournal(dir, j); err != nil {
			t.Fatal(err)
		}
		w := newWorld("1.4.0")
		r := startRig(t, dir, w, healthy)
		r.m.Resume()
		r.m.Wait()
		got, _ := LoadJournal(dir)
		if got.State != state || len(w.calls) != 0 {
			t.Fatalf("%s: became %s, calls %v", state, got.State, w.calls)
		}
	}
}

func TestResumeComesBackOnTheOldRelease(t *testing.T) {
	for _, state := range []string{StateRestarting, StateWatching} {
		dir := t.TempDir()
		now := time.Now().UTC()
		j := &Journal{Job: homeJob(), From: "1.4.0", FromRef: "ref:1.4.0", State: state, WatchSince: &now, Steps: []StepRecord{}}
		_ = SaveJournal(dir, j)
		w := newWorld("1.4.0") // the launcher's guard brought the old release back
		w.migrated = true
		r := startRig(t, dir, w, healthy)
		r.m.Resume()
		got := r.waitState(StateRolledBack)
		if !w.marked || got.Error == nil || !w.called("revert") { // the guard and the order undone too
			t.Fatalf("%s: marked %v error %+v", state, w.marked, got.Error)
		}
	}
}

func TestUnreadableJournalIsSetAside(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/"+JournalFile, "{not json"); err != nil {
		t.Fatal(err)
	}
	r := startRig(t, dir, newWorld("1.4.0"), healthy)
	r.m.Resume()
	if j, err := LoadJournal(dir); j != nil || err != nil {
		t.Fatalf("journal %v err %v", j, err)
	}
	if err := r.m.Prepare(homeJob()); err != nil {
		t.Fatal(err)
	}
}
