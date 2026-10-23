package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Installer stages and switches releases for one kind of install (layout.go, runtime.go,
// appbundle.go). Every method is idempotent: a step cut short by a power cut runs again.
type Installer interface {
	// Kind names the install kind ("layout", "runtime", "appbundle").
	Kind() string
	// Running is the release this process runs.
	Running() string
	// Preflight checks the release can be installed here (nothing changes).
	Preflight(ctx context.Context, j *Journal) error
	// Stage fetches and verifies the release and puts it where Switch takes it from,
	// setting j.Staged (and j.FromRef, the running release's handle).
	Stage(ctx context.Context, j *Journal, log io.Writer) error
	// Smoke runs the staged release's programs once (their version).
	Smoke(ctx context.Context, j *Journal) error
	// Tools are the staged release's maktaba-api and migrations directory, for the
	// rehearsal ("" when it cannot be had: the rehearsal is skipped).
	Tools(j *Journal) (api, migrations string)
	// Switch makes the staged release the one the next start runs (under the
	// launcher's guard where there is one).
	Switch(ctx context.Context, j *Journal) error
	// Restart hands the machine over to the switched release: it asks the supervisor to
	// stop and exit 75, or the desktop app to install. It returns once that is under
	// way (this process ends soon after); an error means nothing was installed.
	Restart(ctx context.Context, j *Journal, exit func(code int)) error
	// Revert makes j.From the release the next start runs again.
	Revert(ctx context.Context, j *Journal) error
	// Commit keeps the running (new) release: clears the guard, raises the watermark,
	// prunes old releases to j.Job.KeepVersions.
	Commit(ctx context.Context, j *Journal) error
	// Discard removes what Stage left (a run that ended before its switch).
	Discard(ctx context.Context, j *Journal) error
}

// Verdict is the watch's view of the release that came up.
type Verdict struct {
	Healthy bool
	// Fatal: waiting will not help (a role crash-loops, another version answers).
	Fatal bool
	Why   string
}

// Health judges the running release.
type Health interface {
	Check(ctx context.Context, version string) Verdict
}

// Records is the home's database record (state/database.json, the database step of
// serve_appbundle.go): a rollback after the new release's migrations ran marks the dump
// taken before them to be put back by the next start, before any role runs.
type Records interface {
	// MarkRestore marks the record restoring when it says version's migrations ran over
	// from's database with a dump; it reports whether it did.
	MarkRestore(version, from, reason string) (bool, error)
	// Migrated reports whether the database holds version's migrations.
	Migrated(version string) bool
}

// Rehearser rehearses migrations on a scratch copy of the home's database.
type Rehearser interface {
	// Check answers whether the database can be dumped here (the program, its version):
	// a home that could not take its pre-update dump fails its preflight, not its start.
	Check(ctx context.Context) error
	Rehearse(ctx context.Context, api, migrations string, maxGB int, log io.Writer) (Rehearsal, error)
	// DumpAside dumps the database as it is (a failed release's state) beside the
	// backups; best effort.
	DumpAside(ctx context.Context, label string) (string, error)
}

// Errors of the machine's requests.
var (
	ErrBusy       = errors.New("update_busy")      // another run is under way here
	ErrNoRun      = errors.New("update_no_run")    // no run of that id here
	ErrNotReady   = errors.New("update_not_ready") // apply before the run is prepared
	ErrPastSwitch = errors.New("past_switch")      // abort after the switch
	ErrAhead      = errors.New("update_ahead")     // this machine runs a newer release
	ErrNoRollback = errors.New("no_rollback")      // nothing to roll back to, or the database moved on
)

// Machine runs this machine's part of updates.
type Machine struct {
	// Dir is the updates directory (journal.json, logs/).
	Dir       string
	Installer Installer
	Health    Health
	// Records and Rehearser are the home's (nil on a node).
	Records   Records
	Rehearser Rehearser
	// Exit asks serve to stop every role and exit with code (75: the launcher starts
	// the switched release).
	Exit func(code int)
	Log  io.Writer
	// Now and CheckEvery are test seams (time.Now, 5 s).
	Now        func() time.Time
	CheckEvery time.Duration
	// MaxStarts is how many starts of the new release a watch tolerates (3).
	MaxStarts int
	// OnChange is called after every change of the journal (a node's machine agent
	// sends its update.state again).
	OnChange func()

	mu      sync.Mutex
	j       *Journal
	cancel  context.CancelFunc
	running sync.WaitGroup
	base    context.Context
}

func (m *Machine) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Machine) checkEvery() time.Duration {
	if m.CheckEvery > 0 {
		return m.CheckEvery
	}
	return 5 * time.Second
}

func (m *Machine) maxStarts() int {
	if m.MaxStarts > 0 {
		return m.MaxStarts
	}
	return 3
}

func (m *Machine) logf(format string, args ...any) {
	line := fmt.Sprintf("[update] "+format+"\n", args...)
	if m.Log != nil {
		_, _ = io.WriteString(m.Log, line)
	}
	m.mu.Lock()
	j := m.j
	m.mu.Unlock()
	if j != nil {
		m.runLog(j.Job.RunID, line)
	}
}

// runLog appends a line to logs/<run>.log (the Updates page's run log).
func (m *Machine) runLog(runID, line string) {
	if runID == "" || m.Dir == "" {
		return
	}
	dir := filepath.Join(m.Dir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, safeName(runID)+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = io.WriteString(f, m.now().Format(time.RFC3339)+" "+line)
}

func safeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			out = append(out, r)
		}
	}
	return string(out)
}

// save writes the journal (the caller holds m.mu).
func (m *Machine) saveLocked() error {
	m.j.UpdatedAt = m.now()
	err := SaveJournal(m.Dir, m.j)
	if m.OnChange != nil {
		go m.OnChange()
	}
	return err
}

// mutate changes the journal under the lock and saves it.
func (m *Machine) mutate(fn func(j *Journal)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.j == nil {
		return ErrNoRun
	}
	fn(m.j)
	return m.saveLocked()
}

// Status is a copy of the journal (nil: no run here) with the running release.
type Status struct {
	*Journal
	Running string `json:"running"`
	Kind    string `json:"installer"`
}

// Status reports this machine's run.
func (m *Machine) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{Journal: m.j.clone(), Running: m.Installer.Running(), Kind: m.Installer.Kind()}
}

// Start makes the machine ready: ctx bounds every goroutine it starts (the supervisor's
// life). Resume reads the journal of a run the last start left.
func (m *Machine) Start(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
}

func (m *Machine) ctx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.base == nil {
		return context.Background()
	}
	return m.base
}

// beginStep records a step's start (before its side effects).
func (m *Machine) beginStep(step string) error {
	return m.mutate(func(j *Journal) {
		j.Step = step
		j.Steps = append(j.Steps, StepRecord{Step: step, State: "running", StartedAt: m.now()})
	})
}

// endStep records the step's end (after its side effects); err nil: done.
func (m *Machine) endStep(step, state, detail string) error {
	return m.mutate(func(j *Journal) {
		now := m.now()
		for i := len(j.Steps) - 1; i >= 0; i-- {
			if j.Steps[i].Step == step && j.Steps[i].FinishedAt == nil {
				j.Steps[i].State, j.Steps[i].FinishedAt, j.Steps[i].Detail = state, &now, detail
				return
			}
		}
		j.Steps = append(j.Steps, StepRecord{Step: step, State: state, StartedAt: now, FinishedAt: &now, Detail: detail})
	})
}

// failure is err as a Failure of step.
func failure(step string, err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		c := *f
		if c.Step == "" {
			c.Step = step
		}
		return &c
	}
	return &Failure{Step: step, Code: step + "_failed", Detail: err.Error()}
}

// ---------- prepare ----------

// Prepare starts preparing job (in the background) and returns at once. The same run
// asked again is answered with its state; another run while one is under way is
// ErrBusy. A machine already on the job's release is prepared at once; one on a newer
// release refuses (ErrAhead: never a downgrade).
func (m *Machine) Prepare(job Job) error {
	if err := job.Validate(); err != nil {
		return Fail("bad_job", "%v", err)
	}
	job.Version = layout.NormalizeVersion(job.Version)
	running := m.Installer.Running()
	m.mu.Lock()
	if m.j != nil && m.j.Job.RunID == job.RunID && !Ended(m.j.State) {
		m.mu.Unlock()
		return nil // asked again: under way, prepared, or further
	}
	if m.j != nil && Busy(m.j.State) {
		m.mu.Unlock()
		return fmt.Errorf("%w: run %s is %s", ErrBusy, m.j.Job.RunID, m.j.State)
	}
	if c := layout.CompareVersions(running, job.Version); layout.ValidVersion(layout.NormalizeVersion(running)) && c > 0 {
		m.mu.Unlock()
		return fmt.Errorf("%w: this machine runs %s, newer than %s", ErrAhead, running, job.Version)
	}
	now := m.now()
	m.j = &Journal{Job: job, Kind: m.Installer.Kind(), From: layout.NormalizeVersion(running), State: StatePreparing,
		StartedAt: now, Steps: []StepRecord{}}
	if layout.CompareVersions(running, job.Version) == 0 {
		// already on it (installed by hand, or a run that committed and was asked again)
		m.j.State, m.j.Staged = StatePrepared, "running"
		err := m.saveLocked()
		m.mu.Unlock()
		return err
	}
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(m.ctxLocked())
	m.cancel = cancel
	m.running.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.running.Done()
		m.prepare(ctx)
	}()
	return nil
}

func (m *Machine) ctxLocked() context.Context {
	if m.base == nil {
		return context.Background()
	}
	return m.base
}

// prepare runs the steps of a prepare.
func (m *Machine) prepare(ctx context.Context) {
	j := m.Status().Journal
	m.logf("preparing %s (run %s, %s install, from %s)", j.Job.Version, j.Job.RunID, j.Kind, orNone(j.From))
	steps := []struct {
		name string
		run  func(ctx context.Context) (string, error)
	}{
		{StepPreflight, func(ctx context.Context) (string, error) {
			if err := m.withJournal(func(j *Journal) error { return m.Installer.Preflight(ctx, j) }); err != nil {
				return "", err
			}
			if j.Job.Home && m.Rehearser != nil {
				return "", m.Rehearser.Check(ctx)
			}
			return "", nil
		}},
		{StepStage, func(ctx context.Context) (string, error) {
			return "", m.withJournal(func(j *Journal) error { return m.Installer.Stage(ctx, j, m.logWriter()) })
		}},
		{StepSmoke, func(ctx context.Context) (string, error) {
			return "", m.withJournal(func(j *Journal) error { return m.Installer.Smoke(ctx, j) })
		}},
		{StepRehearse, m.rehearse},
	}
	for _, s := range steps {
		if err := m.beginStep(s.name); err != nil {
			m.logf("cannot write the journal: %v", err)
			return
		}
		detail, err := s.run(ctx)
		if ctx.Err() != nil {
			m.abortCut(s.name)
			return
		}
		if err != nil {
			f := failure(s.name, err)
			_ = m.endStep(s.name, "failed", f.Error())
			_ = m.mutate(func(j *Journal) { j.State, j.Error = StatePrepareFailed, f })
			m.logf("prepare failed at %s: %s", s.name, f.Error())
			_ = m.withJournal(func(j *Journal) error { return m.Installer.Discard(context.Background(), j) })
			return
		}
		state := "done"
		if strings.HasPrefix(detail, "skipped:") {
			state = "skipped"
		}
		_ = m.endStep(s.name, state, detail)
	}
	_ = m.mutate(func(j *Journal) { j.State, j.Step = StatePrepared, "" })
	m.logf("prepared %s", j.Job.Version)
}

// withJournal runs fn on the live journal (the installer sets its handles) and saves it.
func (m *Machine) withJournal(fn func(j *Journal) error) error {
	m.mu.Lock()
	if m.j == nil {
		m.mu.Unlock()
		return ErrNoRun
	}
	work := m.j.clone()
	m.mu.Unlock()
	err := fn(work)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.j == nil || m.j.Job.RunID != work.Job.RunID {
		return ErrNoRun
	}
	m.j.Staged, m.j.FromRef = work.Staged, work.FromRef
	if serr := m.saveLocked(); err == nil {
		err = serr
	}
	return err
}

// rehearse rehearses the release's migrations on the home (skipped elsewhere, or
// without the tools: logged in the step's detail).
func (m *Machine) rehearse(ctx context.Context) (string, error) {
	j := m.Status().Journal
	if !j.Job.Home {
		return "skipped: not the home", nil
	}
	if !j.Job.Rehearse || m.Rehearser == nil {
		_ = m.mutate(func(j *Journal) { j.Rehearsal = &Rehearsal{Skipped: "off"} })
		return "skipped: off", nil
	}
	api, migrations := m.Installer.Tools(j)
	if api == "" || migrations == "" {
		_ = m.mutate(func(j *Journal) { j.Rehearsal = &Rehearsal{Skipped: "no_migrations_archive"} })
		return "skipped: the release carries no migrations to rehearse with", nil
	}
	r, err := m.Rehearser.Rehearse(ctx, api, migrations, j.Job.RehearseMaxGB, m.logWriter())
	_ = m.mutate(func(j *Journal) { rr := r; j.Rehearsal = &rr })
	if err != nil {
		return "", err
	}
	if r.Skipped != "" {
		return "skipped: " + r.Skipped, nil
	}
	return fmt.Sprintf("%d migrations in %.1f s", r.Pending, r.MigrateSec), nil
}

// abortCut ends a prepare that was stopped (Abort, or the supervisor stopping).
func (m *Machine) abortCut(step string) {
	m.mu.Lock()
	aborting := m.j != nil && m.j.State == StateAborted
	m.mu.Unlock()
	if aborting {
		_ = m.endStep(step, "failed", "aborted")
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Discard(context.Background(), j) })
		m.logf("prepare aborted")
		return
	}
	// the supervisor stops: the journal stays preparing, and the next start prepares again
	m.logf("prepare interrupted at %s: it runs again at the next start", step)
}

// logWriter writes lines into the machine's log and the run's log.
func (m *Machine) logWriter() io.Writer { return lineWriter{m} }

type lineWriter struct{ m *Machine }

func (w lineWriter) Write(p []byte) (int, error) {
	if w.m.Log != nil {
		_, _ = w.m.Log.Write(p)
	}
	w.m.mu.Lock()
	j := w.m.j
	w.m.mu.Unlock()
	if j != nil {
		w.m.runLog(j.Job.RunID, string(p))
	}
	return len(p), nil
}

// Abort stops a run before its switch (nothing changed); after it, ErrPastSwitch.
func (m *Machine) Abort(runID string) error {
	m.mu.Lock()
	if m.j == nil || m.j.Job.RunID != runID {
		m.mu.Unlock()
		return ErrNoRun
	}
	switch m.j.State {
	case StatePreparing:
		m.j.State = StateAborted
		_ = m.saveLocked()
		if m.cancel != nil {
			m.cancel()
		}
		m.mu.Unlock()
		return nil
	case StatePrepared:
		m.j.State = StateAborted
		err := m.saveLocked()
		m.mu.Unlock()
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Discard(context.Background(), j) })
		return err
	case StatePrepareFailed, StateAborted:
		m.mu.Unlock()
		return nil
	}
	state := m.j.State
	m.mu.Unlock()
	return fmt.Errorf("%w: the run is %s", ErrPastSwitch, state)
}

// ---------- apply ----------

// Apply switches to the prepared release and restarts on it (in the background); the
// release that comes up finishes the run (Resume).
func (m *Machine) Apply(runID string) error {
	m.mu.Lock()
	if m.j == nil || m.j.Job.RunID != runID {
		m.mu.Unlock()
		return ErrNoRun
	}
	switch m.j.State {
	case StateApplying, StateRestarting, StateWatching, StateCommitted, StateRollingBack, StateRolledBack, StateApplyFailed:
		m.mu.Unlock()
		return nil // asked again: under way or done
	case StatePrepared:
	default:
		state := m.j.State
		m.mu.Unlock()
		return fmt.Errorf("%w: the run is %s", ErrNotReady, state)
	}
	if m.j.Staged == "running" {
		// the machine already runs the release: nothing to switch
		now := m.now()
		m.j.State, m.j.Step = StateCommitted, ""
		m.j.Steps = append(m.j.Steps, StepRecord{Step: StepCommit, State: "done", StartedAt: now, FinishedAt: &now,
			Detail: "already running " + m.j.Job.Version})
		err := m.saveLocked()
		m.mu.Unlock()
		return err
	}
	m.j.State = StateApplying
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return err
	}
	ctx := m.ctxLocked()
	m.running.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.running.Done()
		m.apply(ctx)
	}()
	return nil
}

func (m *Machine) apply(ctx context.Context) {
	j := m.Status().Journal
	m.logf("applying %s (from %s)", j.Job.Version, orNone(j.From))
	_ = m.beginStep(StepSwitch)
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Switch(ctx, j) }); err != nil {
		f := failure(StepSwitch, err)
		_ = m.endStep(StepSwitch, "failed", f.Error())
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) })
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateApplyFailed, f })
		m.logf("switch failed: %s", f.Error())
		return
	}
	_ = m.endStep(StepSwitch, "done", "")
	_ = m.mutate(func(j *Journal) { j.State = StateRestarting })
	_ = m.beginStep(StepRestart)
	m.logf("restarting on %s", j.Job.Version)
	exit := m.Exit
	if exit == nil {
		exit = func(int) {}
	}
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Restart(ctx, j, exit) }); err != nil {
		if ctx.Err() != nil {
			// this process is being stopped (the app's install restarts the server): the
			// journal says restarting, and the release that starts next goes on
			m.logf("stopped while restarting: the next start goes on")
			return
		}
		f := failure(StepRestart, err)
		_ = m.endStep(StepRestart, "failed", f.Error())
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) })
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateApplyFailed, f })
		m.logf("restart failed, nothing was installed: %s", f.Error())
	}
}

// ---------- resume, watch, commit, rollback ----------

// Resume takes up the run the last start left (call once, before the roles start
// serving, after Start). It decides from the journal and the release this process
// runs; a watch goes on in the background.
func (m *Machine) Resume() {
	j, err := LoadJournal(m.Dir)
	if err != nil {
		m.logf("the journal cannot be read (%v): set aside", err)
		_ = setAside(m.Dir, m.now())
		return
	}
	if j == nil {
		return
	}
	running := layout.NormalizeVersion(m.Installer.Running())
	newRel := running != "" && layout.CompareVersions(running, j.Job.Version) == 0
	m.mu.Lock()
	m.j = j
	m.mu.Unlock()
	switch j.State {
	case StatePreparing:
		// cut short: prepared again (every step is idempotent)
		m.mu.Lock()
		ctx, cancel := context.WithCancel(m.ctxLocked())
		m.cancel = cancel
		_ = m.saveLocked()
		m.running.Add(1)
		m.mu.Unlock()
		go func() {
			defer m.running.Done()
			m.prepare(ctx)
		}()
	case StateApplying:
		if newRel {
			m.beginWatch("the switch and restart happened before the journal said so")
			return
		}
		// stopped before its restart: the previous release runs; nothing was installed
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) })
		_ = m.mutate(func(j *Journal) {
			j.State, j.Error = StateApplyFailed, &Failure{Step: StepRestart, Code: "interrupted",
				Detail: "the machine stopped during the switch; it runs " + orNone(running) + " again"}
		})
		m.logf("the apply of %s was interrupted: %s runs", j.Job.Version, orNone(running))
	case StateRestarting:
		if newRel {
			_ = m.endStep(StepRestart, "done", "")
			m.beginWatch("")
			return
		}
		// it came back on the previous release: the launcher's guard, or the app's
		// install did not happen
		m.cameBackOld(running, "did_not_start")
	case StateWatching:
		if newRel {
			m.beginWatch("")
			return
		}
		m.cameBackOld(running, "did_not_stay_up")
	case StateRollingBack:
		if newRel {
			// the switch back did not take: again
			m.rollback(j.Reason)
			return
		}
		_ = m.endStep(StepRollback, "done", "")
		_ = m.mutate(func(j *Journal) { j.State, j.Step = StateRolledBack, "" })
		m.logf("rolled back to %s (%s)", orNone(running), j.Reason)
	}
}

// cameBackOld records a run whose new release did not stay running: the launcher's
// guard (or the app) brought back the previous one, and the record was marked for the
// dump to be put back where the migrations ran (by the launcher, or here when it could
// not: the database step runs after this).
func (m *Machine) cameBackOld(running, why string) {
	j := m.Status().Journal
	if m.Records != nil && j.Job.Home {
		if marked, err := m.Records.MarkRestore(j.Job.Version, j.From, why); err != nil {
			m.logf("cannot mark the database record for the dump to be put back: %v", err)
		} else if marked {
			m.logf("the database goes back to the dump taken before %s's migrations", j.Job.Version)
		}
	}
	_ = m.mutate(func(j *Journal) {
		j.State, j.Reason, j.Step = StateRolledBack, why, ""
		j.Error = &Failure{Step: StepWatch, Code: why, Detail: fmt.Sprintf("%s did not stay running; %s runs again",
			j.Job.Version, orNone(running))}
	})
	m.logf("%s did not stay running (%s): %s runs again", j.Job.Version, why, orNone(running))
}

// beginWatch starts (or goes on with) the watch of the new release.
func (m *Machine) beginWatch(note string) {
	tooMany := false
	_ = m.mutate(func(j *Journal) {
		now := m.now()
		if j.WatchSince == nil {
			j.WatchSince = &now
			j.Steps = append(j.Steps, StepRecord{Step: StepWatch, State: "running", StartedAt: now, Detail: note})
		}
		j.State, j.Step = StateWatching, StepWatch
		j.Starts++
		tooMany = j.Starts > m.maxStarts()
	})
	if tooMany {
		m.rollback("restarts")
		return
	}
	ctx := m.ctx()
	m.running.Add(1)
	go func() {
		defer m.running.Done()
		m.watch(ctx)
	}()
}

// watch checks the new release until it stayed healthy for the job's watch (commit) or
// stayed unhealthy for its health limit, or failed fatally (roll back).
func (m *Machine) watch(ctx context.Context) {
	j := m.Status().Journal
	watch, limit := j.Job.Watch(), j.Job.HealthLimit()
	m.logf("watching %s: healthy for %s commits it", j.Job.Version, watch)
	var healthySince, unhealthySince time.Time
	t := time.NewTicker(m.checkEvery())
	defer t.Stop()
	for {
		v := Verdict{Healthy: true}
		if m.Health != nil {
			v = m.Health.Check(ctx, j.Job.Version)
		}
		if ctx.Err() != nil {
			return // the supervisor stops: the next start goes on watching
		}
		now := m.now()
		switch {
		case v.Fatal:
			m.logf("the new release fails: %s", v.Why)
			m.rollback(v.Why)
			return
		case v.Healthy:
			unhealthySince = time.Time{}
			if healthySince.IsZero() {
				healthySince = now
			}
			if now.Sub(healthySince) >= watch {
				m.commit()
				return
			}
		default:
			healthySince = time.Time{}
			if unhealthySince.IsZero() {
				unhealthySince = now
				m.logf("not healthy yet: %s", v.Why)
			}
			if now.Sub(unhealthySince) >= limit {
				m.logf("unhealthy for %s: %s", limit, v.Why)
				m.rollback(v.Why)
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// commit keeps the new release.
func (m *Machine) commit() {
	_ = m.endStep(StepWatch, "done", "")
	_ = m.beginStep(StepCommit)
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Commit(context.Background(), j) }); err != nil {
		// the release runs and stays; what is left (a prune, the guard file) is retried
		// by the next run
		_ = m.endStep(StepCommit, "done", "kept; "+err.Error())
	} else {
		_ = m.endStep(StepCommit, "done", "")
	}
	_ = m.mutate(func(j *Journal) { j.State, j.Step = StateCommitted, "" })
	j := m.Status().Journal
	m.logf("committed %s", j.Job.Version)
}

// rollback switches back to the release that ran before and restarts on it; where the
// new release's migrations ran, the record is marked for the dump taken before them to
// be put back first (the failed state dumped aside).
func (m *Machine) rollback(reason string) {
	j := m.Status().Journal
	if j.FromRef == "" && j.From == "" {
		_ = m.mutate(func(j *Journal) {
			j.State, j.Reason = StateFailed, reason
			j.Error = &Failure{Step: StepRollback, Code: "no_previous", Detail: "no release to go back to: " + reason}
		})
		m.logf("cannot roll back (no previous release): %s", reason)
		return
	}
	_ = m.endStep(StepWatch, "failed", reason)
	_ = m.mutate(func(j *Journal) { j.State, j.Reason = StateRollingBack, reason })
	_ = m.beginStep(StepRollback)
	m.logf("rolling back %s to %s: %s", j.Job.Version, orNone(j.From), reason)
	if j.Job.Home && m.Records != nil && m.Records.Migrated(j.Job.Version) {
		if m.Rehearser != nil {
			if p, err := m.Rehearser.DumpAside(context.Background(), "failed-"+j.Job.Version); err == nil {
				m.logf("the database as %s left it is in %s", j.Job.Version, p)
			} else {
				m.logf("could not dump the failed state aside: %v", err)
			}
		}
		if _, err := m.Records.MarkRestore(j.Job.Version, j.From, reason); err != nil {
			m.logf("cannot mark the database record for the dump to be put back: %v", err)
		}
	}
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) }); err != nil {
		f := failure(StepRollback, err)
		_ = m.endStep(StepRollback, "failed", f.Error())
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateFailed, f })
		m.logf("the switch back failed: %s", f.Error())
		return
	}
	if m.Exit != nil {
		m.Exit(75)
	}
}

// RollbackCommitted switches a committed run back (an admin's Roll back of a node):
// only to the release that ran before it, and never on a home whose database the new
// release migrated (that needs the dump put back by hand).
func (m *Machine) RollbackCommitted(runID string) error {
	j := m.Status().Journal
	if j == nil || j.Job.RunID != runID {
		return ErrNoRun
	}
	if j.State != StateCommitted || j.From == "" || j.Staged == "running" {
		return fmt.Errorf("%w: the run is %s", ErrNoRollback, j.State)
	}
	if j.Job.Home && m.Records != nil && m.Records.Migrated(j.Job.Version) {
		return fmt.Errorf("%w: %s migrated the database; restore the backup by hand", ErrNoRollback, j.Job.Version)
	}
	go m.rollback("admin")
	return nil
}

// Wait waits for the machine's goroutines (tests).
func (m *Machine) Wait() { m.running.Wait() }

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
