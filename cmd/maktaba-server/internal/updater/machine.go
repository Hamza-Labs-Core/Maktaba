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

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/attempts"
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

// Extender is an installer whose launcher's guard has a deadline: the watch pushes it
// on while the new release is busy starting (the home's database step dumps and
// migrates for as long as the database takes), and when it first comes up (an app's
// install may have taken a while since the switch).
type Extender interface {
	Extend(j *Journal, until time.Time) error
}

// guardMargin is how far the watch keeps the launcher's guard ahead of now, every minute
// for as long as it goes on: the guard catches only a supervisor that died or hangs (no
// watch pushes it then); the watch's own limits decide everything else.
const guardMargin = 15 * time.Minute

// pendingCap bounds the start steps of a release that came up (the database step's dump
// and migrations, the pipeline's environment): two hours, and on a home a quarter of an
// hour more per GiB of its database plus three times the rehearsed migrations' time.
func pendingCap(j *Journal) time.Duration {
	d := 2 * time.Hour
	if r := j.Rehearsal; r != nil {
		d += time.Duration(r.DBBytes>>30) * 15 * time.Minute
		d += 3 * time.Duration(r.MigrateSec*float64(time.Second))
	}
	return d
}

// stallLimit is how long a start step may show no progress (a dump that stopped
// growing, a migration that does not finish): an hour, or three times the rehearsed
// migrations' time when that is longer.
func stallLimit(j *Journal) time.Duration {
	d := time.Hour
	if r := j.Rehearsal; r != nil {
		d = max(d, 3*time.Duration(r.MigrateSec*float64(time.Second)))
	}
	return d
}

// extendGuard pushes the launcher's guard to until (best effort).
func (m *Machine) extendGuard(until time.Time) {
	e, ok := m.Installer.(Extender)
	if !ok {
		return
	}
	j := m.Status().Journal
	if j == nil {
		return
	}
	if err := e.Extend(j, until); err != nil {
		m.logf("cannot push the launcher's guard on: %v", err)
	}
}

// Verdict is the watch's view of the release that came up.
type Verdict struct {
	Healthy bool
	// Fatal: waiting will not help (a role crash-loops, another version answers).
	Fatal bool
	// Pending: the release is busy starting (the home's database step dumps and
	// migrates): neither healthy nor unhealthy while it makes progress, within pendingCap.
	Pending bool
	// Progress is what the start step has done so far (it changes while the step works:
	// the dump's size, the migrations done, the environment's install).
	Progress string
	// Unmeasured: the step's progress cannot be seen (one migration's statement, an index
	// built for an hour on a large home; a dump put back): no stall limit, only pendingCap
	// (lock waits are bounded by the migrations' lock timeout).
	Unmeasured bool
	Why        string
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
	// Unmark undoes MarkRestore (the switch back failed: version stays, and its database).
	Unmark(version string) error
	// Migrated reports whether the database holds version's migrations.
	Migrated(version string) bool
}

// Rehearser rehearses migrations on a scratch copy of the home's database.
type Rehearser interface {
	// Check answers whether the database can be dumped here (the program, its version,
	// room for the dump, and for a rehearsal of a database up to maxGB its scratch copy):
	// a home that could not take its pre-update dump fails its preflight, not its start.
	Check(ctx context.Context, rehearse bool, maxGB int) error
	Rehearse(ctx context.Context, api, migrations string, maxGB int, log io.Writer) (Rehearsal, error)
	// DumpAside dumps the database as it is (a failed release's state) beside the
	// backups; best effort.
	DumpAside(ctx context.Context, label string) (string, error)
}

// BackupPruner is a Rehearser that also keeps the home's dumps to
// rules.updates.backups_keep once an update commits (design §5, the commit step).
type BackupPruner interface {
	PruneBackups(keep int) error
}

// BackupFinder is a Rehearser that finds the dump a release's start took before its
// migrations (backups/pre-<from>-to-<to>.dump): nil when there is none.
type BackupFinder interface {
	UpdateDump(from, to string) *Backup
}

// Errors of the machine's requests.
var (
	ErrBusy       = errors.New("update_busy")      // another run is under way here
	ErrNoRun      = errors.New("update_no_run")    // no run of that id here
	ErrNotReady   = errors.New("update_not_ready") // apply before the run is prepared
	ErrPastSwitch = errors.New("past_switch")      // abort after the switch
	ErrAhead      = errors.New("update_ahead")     // this machine runs a newer release
	ErrNoRollback = errors.New("no_rollback")      // nothing to roll back to, or the database moved on
	// ErrTVBusy: the apply's restart would now cut a watched TV, the handover the run
	// counted on being one it cannot make now (story 35.7): nothing changed, and the run's
	// TV gate holds it.
	ErrTVBusy = errors.New("tv_busy")
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
	// TVOnAir reports whether this machine keeps TV streams on air now (its tv-slot
	// processes, or its streaming role, run): a launcher's replacement that cannot hand the
	// TV over then waits for the next restart, which the run's TV gate holds for a quiet
	// TV (nil: none).
	TVOnAir func() bool
	// TVHandover reports whether this machine's TV processes (the front door, the slots)
	// can run on across the restart onto target, the release the next start runs (story
	// 35.7), and why not; arm also makes serve's next exit 75 hand them over. nil: they
	// never do (every process restarts, under the run's TV gate).
	TVHandover func(j *Journal, target string, arm bool) (bool, string)
	// TV moves the home's TV to the new release's streaming binary before the commit
	// (story 35.6, tv.go); nil: no TV slots here (a node, a SQLite home), nothing to move.
	TV TVSwapper

	mu      sync.Mutex
	j       *Journal
	cancel  context.CancelFunc
	running sync.WaitGroup
	base    context.Context
	// saveErr is the last journal write failure logged (one line per distinct one)
	saveErr string
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

func (m *Machine) logf(format string, args ...any) { m.leveledf("", format, args...) }

// errorf and warnf are logf for a failure: the server log's line names its level first
// ("[update] level=error …", what the logs page reads a plain line's level from); the
// run log's line (the Updates page) stays as it was.
func (m *Machine) errorf(format string, args ...any) { m.leveledf("error", format, args...) }
func (m *Machine) warnf(format string, args ...any)  { m.leveledf("warn", format, args...) }

func (m *Machine) leveledf(level, format string, args ...any) {
	line := fmt.Sprintf("[update] "+format+"\n", args...)
	if m.Log != nil {
		_, _ = io.WriteString(m.Log, withLevel(level, line))
	}
	m.mu.Lock()
	j := m.j
	m.mu.Unlock()
	if j != nil {
		m.runLog(j.Job.RunID, line)
	}
}

// withLevel puts "level=<level> " after a line's "[update] " (none: the line as it is).
func withLevel(level, line string) string {
	if level == "" {
		return line
	}
	return strings.Replace(line, "[update] ", "[update] level="+level+" ", 1)
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
	switch {
	case err != nil && err.Error() != m.saveErr:
		// logged here (most callers drop the error), once per distinct failure; not
		// through logf, which takes m.mu
		m.saveErr = err.Error()
		line := fmt.Sprintf("[update] cannot write the journal: %v\n", err)
		if m.Log != nil {
			_, _ = io.WriteString(m.Log, withLevel("error", line))
		}
		m.runLog(m.j.Job.RunID, line)
	case err == nil && m.saveErr != "":
		m.saveErr = ""
		if m.Log != nil {
			_, _ = io.WriteString(m.Log, "[update] the journal is written again\n")
		}
	}
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
	// Asks: this machine installs only once someone clicks on it (Asker).
	Asks bool `json:"asks,omitempty"`
	// BoundSec is how long this machine's own limits let the run go on from its switch
	// (its start steps' cap and its watch's bound): the home's driver waits at least that
	// long before it gives up on the home.
	BoundSec int `json:"bound_sec,omitempty"`
	// Attempts are this machine's update attempts, every failure with its reason (the
	// desktop app's own and its releases' starts: update-attempts.md), newest first.
	Attempts []attempts.Attempt `json:"attempts,omitempty"`
}

// Status reports this machine's run.
func (m *Machine) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Journal: m.j.clone(), Running: m.Installer.Running(), Kind: m.Installer.Kind(), Asks: m.asker() != nil,
		Attempts: attempts.Read(m.Dir)}
	if st.Journal != nil {
		st.BoundSec = int((pendingCap(st.Journal) + st.Journal.Job.WatchBound()).Seconds())
	}
	return st
}

// WatchAttempts calls OnChange when the attempts files change (the desktop app writes its
// own: a node's machine agent then sends them to the home), every interval until ctx ends.
func (m *Machine) WatchAttempts(ctx context.Context, interval time.Duration) {
	last := attempts.Stamp(m.Dir)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if now := attempts.Stamp(m.Dir); now != last {
			last = now
			if m.OnChange != nil {
				m.OnChange()
			}
		}
	}
}

// asker is the installer when it waits for a click.
func (m *Machine) asker() Asker {
	if a, ok := m.Installer.(Asker); ok && a.Asks() {
		return a
	}
	return nil
}

// waitsForClick reports whether state is an order waiting for its click or its go.
func waitsForClick(state string) bool { return state == StateAwaitingClick || state == StateClicked }

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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.j == nil {
		return ErrNoRun
	}
	m.endStepLocked(step, state, detail)
	return m.saveLocked()
}

// endStepLocked is endStep's change (the caller holds m.mu and saves).
func (m *Machine) endStepLocked(step, state, detail string) {
	j, now := m.j, m.now()
	for i := len(j.Steps) - 1; i >= 0; i-- {
		if j.Steps[i].Step == step && j.Steps[i].FinishedAt == nil {
			j.Steps[i].State, j.Steps[i].FinishedAt, j.Steps[i].Detail = state, &now, detail
			return
		}
	}
	j.Steps = append(j.Steps, StepRecord{Step: step, State: state, StartedAt: now, FinishedAt: &now, Detail: detail})
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
	if old := m.j; old != nil {
		switch old.State {
		case StatePrepared, StateAwaitingClick, StateClicked, StateAborted, StatePrepareFailed:
			// what the run it replaces staged goes (an order waiting for its click gives
			// way to the newer run: the app forgets it)
			_ = m.Installer.Discard(context.Background(), old.clone())
		}
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
				return "", m.Rehearser.Check(ctx, j.Job.Rehearse, j.Job.RehearseMaxGB)
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
			m.errorf("cannot write the journal: %v", err)
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
			m.errorf("prepare failed at %s: %s", s.name, f.Error())
			_ = m.withJournal(func(j *Journal) error { return m.Installer.Discard(context.Background(), j) })
			return
		}
		state := "done"
		if strings.HasPrefix(detail, "skipped:") {
			state = "skipped"
		}
		_ = m.endStep(s.name, state, detail)
	}
	ho, hoWhy := m.tvHandover(m.Status().Journal, j.Job.Version, false)
	stopped := false
	_ = m.mutate(func(j *Journal) {
		if j.State != StatePreparing {
			stopped = true // an abort that came after the last step
			return
		}
		j.State, j.Step = StatePrepared, ""
		j.HandsOver, j.HandoverWhy = ho, hoWhy
	})
	if stopped {
		_ = m.withJournal(func(j *Journal) error { return m.Installer.Discard(context.Background(), j) })
		m.logf("prepare aborted")
		return
	}
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
	case StatePrepared, StateAwaitingClick, StateClicked:
		if m.j.State != StatePrepared {
			m.endStepLocked(StepAsk, "failed", "aborted")
		}
		m.j.State = StateAborted
		err := m.saveLocked()
		m.mu.Unlock()
		// the order withdrawn too: the app forgets what it staged
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
// release that comes up finishes the run (Resume). An install a person updates is
// ordered from the app instead (awaiting_click); once someone clicked there, Apply again
// is the home's go.
// ApplyGated is Apply for a run the home ordered while the TV is watched (tvBusy: it
// skipped its TV gate because this machine's prepared report said it hands its TV over):
// when it cannot hand over now, it refuses (ErrTVBusy) and says so in its report, so the
// run's TV gate holds it.
func (m *Machine) ApplyGated(runID string, tvBusy bool) error {
	if j := m.Status().Journal; tvBusy && m.TVHandover != nil && j != nil && j.Job.RunID == runID &&
		(j.State == StatePrepared || j.State == StateClicked) && j.Staged != "running" {
		if ok, why := m.TVHandover(j, j.Job.Version, false); !ok {
			_ = m.mutate(func(j *Journal) { j.HandsOver, j.HandoverWhy = false, why })
			return fmt.Errorf("%w: the TV processes cannot be handed over now (%s) and the TV is watched", ErrTVBusy, why)
		}
	}
	return m.Apply(runID)
}

// Apply switches to the prepared release and restarts on it.
func (m *Machine) Apply(runID string) error {
	m.mu.Lock()
	if m.j == nil || m.j.Job.RunID != runID {
		m.mu.Unlock()
		return ErrNoRun
	}
	switch m.j.State {
	case StateApplying, StateRestarting, StateWatching, StateCommitted, StateRollingBack, StateRolledBack, StateApplyFailed,
		StateAwaitingClick:
		m.mu.Unlock()
		return nil // asked again: under way, done, or waiting for its click
	case StatePrepared, StateClicked:
	default:
		state := m.j.State
		m.mu.Unlock()
		return fmt.Errorf("%w: the run is %s", ErrNotReady, state)
	}
	if m.j.State == StatePrepared && m.j.Staged != "running" && m.asker() != nil {
		now := m.now()
		m.j.State, m.j.Step, m.j.App = StateAwaitingClick, StepAsk, ""
		m.j.Steps = append(m.j.Steps, StepRecord{Step: StepAsk, State: "running", StartedAt: now})
		if err := m.saveLocked(); err != nil {
			m.mu.Unlock()
			return err
		}
		ctx := m.ctxLocked()
		m.running.Add(1)
		m.mu.Unlock()
		go func() {
			defer m.running.Done()
			m.await(ctx, runID)
		}()
		return nil
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
		m.undoSwitch("switch failed")
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateApplyFailed, f })
		m.errorf("switch failed: %s", f.Error())
		return
	}
	_ = m.endStep(StepSwitch, "done", "")
	m.armHandover(j.Job.Version)
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
		m.undoSwitch("restart failed")
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateApplyFailed, f })
		m.errorf("restart failed, nothing was installed: %s", f.Error())
	}
}

// await orders the release from the app and follows its answer until someone clicks
// (clicked: the home's go switches) or it fails (nothing changed).
func (m *Machine) await(ctx context.Context, runID string) {
	a := m.asker()
	j := m.Status().Journal
	if a == nil || j == nil || j.Job.RunID != runID {
		return
	}
	if err := a.Ask(j); err != nil {
		m.askFailed(runID, err)
		return
	}
	m.logf("ordered %s from the app: it installs once someone clicks on this machine", j.Job.Version)
	t := time.NewTicker(m.checkEvery())
	defer t.Stop()
	for {
		cur := m.Status().Journal
		if cur == nil || cur.Job.RunID != runID || cur.State != StateAwaitingClick {
			return // withdrawn, replaced, or answered
		}
		state, err := a.Answer(cur)
		if err != nil {
			m.askFailed(runID, err)
			return
		}
		switch {
		case state == RequestClicked:
			m.mu.Lock()
			if m.j != nil && m.j.Job.RunID == runID && m.j.State == StateAwaitingClick {
				m.j.State, m.j.Step, m.j.App = StateClicked, "", RequestClicked
				m.endStepLocked(StepAsk, "done", "clicked")
				_ = m.saveLocked()
			}
			m.mu.Unlock()
			m.logf("someone chose to install %s here: it waits for the home's go", cur.Job.Version)
			return
		case state != cur.App && (state == RequestOrdered || state == RequestStaged):
			_ = m.mutate(func(j *Journal) {
				if j.Job.RunID == runID {
					j.App = state
				}
			})
			if state == RequestStaged {
				m.logf("the app staged %s and shows it: it waits for a click", cur.Job.Version)
			}
		}
		select {
		case <-ctx.Done():
			return // the supervisor stops: the next start goes on waiting
		case <-t.C:
		}
	}
}

// askFailed ends an order the app could not take (nothing changed).
func (m *Machine) askFailed(runID string, err error) {
	f := failure(StepAsk, err)
	var withdraw *Journal
	m.mu.Lock()
	if m.j != nil && m.j.Job.RunID == runID && waitsForClick(m.j.State) {
		m.endStepLocked(StepAsk, "failed", f.Error())
		m.j.State, m.j.Error = StateApplyFailed, f
		_ = m.saveLocked()
		withdraw = m.j.clone()
	}
	m.mu.Unlock()
	if withdraw != nil {
		_ = m.Installer.Discard(context.Background(), withdraw)
		m.logf("the app could not take the order: %s", f.Error())
	}
}

// ---------- resume, watch, commit, rollback ----------

// Resume takes up the run the last start left (call once, before the roles start
// serving, after Start). It decides from the journal and the release this process
// runs; a watch goes on in the background.
func (m *Machine) Resume() {
	j, err := LoadJournal(m.Dir)
	if errors.Is(err, ErrJournalUnreadable) {
		m.logf("the journal cannot be read (%v): set aside", err)
		_ = setAside(m.Dir, m.now())
		return
	}
	if err != nil {
		// the file's, not the journal's: left as it is for the next start
		m.logf("the journal cannot be opened now (%v): the next start takes the run up", err)
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
	case StateAwaitingClick, StateClicked:
		if newRel {
			// installed while it waited (the app's own way): watched like a switch
			m.beginWatch("installed while the order waited for its click")
			return
		}
		if j.State == StateAwaitingClick {
			ctx := m.ctx()
			m.running.Add(1)
			go func() {
				defer m.running.Done()
				m.await(ctx, j.Job.RunID)
			}()
		}
		// clicked: it waits for the home's go
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
		m.undoSwitch("an interrupted apply")
		_ = m.mutate(func(j *Journal) {
			j.State, j.Error = StateApplyFailed, &Failure{Step: StepRestart, Code: "interrupted",
				Detail: "the machine stopped during the switch; it runs " + orNone(running) + " again"}
		})
		m.warnf("the apply of %s was interrupted: %s runs", j.Job.Version, orNone(running))
	case StateRestarting, StateWatching:
		if newRel {
			if j.State == StateRestarting {
				_ = m.endStep(StepRestart, "done", "")
			}
			m.beginWatch("")
			return
		}
		if running != "" && layout.CompareVersions(running, j.Job.Version) > 0 {
			// a newer release runs (installed by hand meanwhile): the run is over, and
			// nothing is switched back under it (its database is the newer one's)
			_ = m.mutate(func(j *Journal) {
				j.State, j.Step = StateApplyFailed, ""
				j.Error = &Failure{Step: StepWatch, Code: "replaced_by_newer",
					Detail: fmt.Sprintf("%s runs now, newer than %s", running, j.Job.Version)}
			})
			m.logf("%s runs now, newer than %s: the run is over", running, j.Job.Version)
			return
		}
		if f := m.installFailure(j); f != nil && j.State == StateRestarting {
			// the app said its install failed (after it had stopped the server for it):
			// nothing was installed, and that is what the run says
			m.undoSwitch("the app's install failed")
			_ = m.endStep(StepRestart, "failed", f.Error())
			_ = m.mutate(func(j *Journal) { j.State, j.Error = StateApplyFailed, f })
			m.errorf("the install of %s failed: %s", j.Job.Version, f.Error())
			return
		}
		// it came back on the previous release: the launcher's guard, or the app's
		// install did not happen
		why := "did_not_start"
		if j.State == StateWatching {
			why = "did_not_stay_up"
		}
		m.cameBackOld(running, why)
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

// InstallReporter is an installer whose install is someone else's (the desktop app's):
// the failure it reported, if any.
type InstallReporter interface {
	InstallFailure(j *Journal) *Failure
}

func (m *Machine) installFailure(j *Journal) *Failure {
	if r, ok := m.Installer.(InstallReporter); ok {
		return r.InstallFailure(j)
	}
	return nil
}

// undoSwitch reverts the switch after what went wrong (after names it) (the previous release runs
// again): a revert that fails too is logged, as it leaves the new release half set up.
func (m *Machine) undoSwitch(after string) {
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) }); err != nil {
		m.errorf("undoing the switch after %s: %v", after, err)
	}
}

// cameBackOld records a run whose new release did not stay running: the launcher's
// guard (or the app) brought back the previous one, and the record was marked for the
// dump to be put back where the migrations ran (by the launcher, or here when it could
// not: the database step runs after this). The switch is undone too (the guard, the
// order, the override): nothing is left that would take the new release for one being
// tried, should it come later (the app's install finishing after all).
func (m *Machine) cameBackOld(running, why string) {
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) }); err != nil {
		m.errorf("undoing the switch: %v", err)
	}
	j := m.Status().Journal
	if m.Records != nil && j.Job.Home {
		if marked, err := m.Records.MarkRestore(j.Job.Version, j.From, why); err != nil {
			m.errorf("cannot mark the database record for the dump to be put back: %v", err)
		} else if marked {
			m.logf("the database goes back to the dump taken before %s's migrations", j.Job.Version)
		}
	}
	_ = m.mutate(func(j *Journal) {
		j.State, j.Reason, j.Step = StateRolledBack, why, ""
		j.Error = &Failure{Step: StepWatch, Code: why, Detail: fmt.Sprintf("%s did not stay running; %s runs again",
			j.Job.Version, orNone(running))}
	})
	m.warnf("%s did not stay running (%s): %s runs again", j.Job.Version, why, orNone(running))
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
	// the guard's deadline ran from the switch; the watch begins now
	if j := m.Status().Journal; j != nil {
		m.extendGuard(m.now().Add(j.Job.Watch() + j.Job.HealthLimit() + guardMargin))
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
	watch, limit, bound := j.Job.Watch(), j.Job.HealthLimit(), j.Job.WatchBound()
	m.logf("watching %s: healthy for %s commits it", j.Job.Version, watch)
	var healthySince, unhealthySince, extended, progressAt time.Time
	watched := time.Duration(j.WatchedSec) * time.Second
	pending := time.Duration(j.PendingSec) * time.Second
	last, saved, savedPending := m.now(), watched, pending
	pendingSaid, progress := "", ""
	capped, stall := pendingCap(j), stallLimit(j)
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
		if v.Pending {
			pending += now.Sub(last)
		} else {
			watched += now.Sub(last)
			progressAt = time.Time{}
			if pendingSaid != "" {
				pendingSaid = ""
				_ = m.mutate(func(j *Journal) { j.Pending = "" })
			}
		}
		last = now
		if watched-saved >= 30*time.Second || pending-savedPending >= 30*time.Second {
			saved, savedPending = watched, pending
			_ = m.mutate(func(j *Journal) {
				j.WatchedSec, j.PendingSec = int(watched/time.Second), int(pending/time.Second)
			})
		}
		switch {
		case v.Fatal:
			m.warnf("the new release fails: %s", v.Why)
			m.rollback(v.Why)
			return
		case v.Pending:
			// starting (the database step): the watch's clock does not run while the step
			// makes progress, within pendingCap; one that stalls or runs past it rolls back
			healthySince, unhealthySince = time.Time{}, time.Time{}
			if v.Why != pendingSaid {
				pendingSaid = v.Why
				why := v.Why
				_ = m.mutate(func(j *Journal) { j.Pending = why })
				m.logf("starting: %s", v.Why)
			}
			if progressAt.IsZero() || v.Progress != progress || v.Unmeasured {
				progress, progressAt = v.Progress, now
			}
			if pending >= capped {
				m.logf("still starting after %s (at most %s): %s", pending.Round(time.Second), capped, v.Why)
				m.rollback("start_timeout")
				return
			}
			if now.Sub(progressAt) >= stall {
				m.logf("no progress for %s: %s", stall, v.Why)
				m.rollback("start_timeout")
				return
			}
		case watched >= bound && !waitsForTV(v, healthySince, now, watch):
			// neither healthy long enough nor unhealthy long enough: a flapping release
			// (one healthy for its whole watch waits only for its TV step, whose own
			// limits bound it)
			m.logf("no verdict within %s of watching: %s", bound, v.Why)
			m.rollback("watch_timeout")
			return
		case v.Healthy:
			unhealthySince = time.Time{}
			if healthySince.IsZero() {
				healthySince = now
			}
			// the TV moves to the new streaming binary while the release is watched; the
			// release commits once both are done
			tvDone, code, detail := m.tvStep(ctx)
			if code != "" {
				m.failTV(code, detail)
				return
			}
			if now.Sub(healthySince) >= watch && tvDone && m.commit() {
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
		// the watch goes on (within its bound): the launcher's guard is kept ahead of it
		if now.Sub(extended) >= time.Minute {
			extended = now
			m.extendGuard(now.Add(guardMargin))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// commit keeps the new release; false when the installer could not (the launcher's
// guard still stands, and would take the release back at its deadline): the watch goes
// on and commits again at its next check.
func (m *Machine) commit() bool {
	j := m.Status().Journal
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Commit(context.Background(), j) }); err != nil {
		m.logf("cannot keep %s yet (tried again): %v", j.Job.Version, err)
		return false
	}
	// the dump the new release's start took before its migrations is the run's backup
	if f, ok := m.Rehearser.(BackupFinder); ok && j.Job.Home {
		if b := f.UpdateDump(j.From, j.Job.Version); b != nil {
			_ = m.mutate(func(j *Journal) { j.Backup = b })
		}
	}
	// the release is kept: the dumps go to rules.updates.backups_keep before the run says
	// it is committed (whoever waits for that sees them pruned)
	if p, ok := m.Rehearser.(BackupPruner); ok && j.Job.Home && j.Job.BackupsKeep > 0 {
		if err := p.PruneBackups(j.Job.BackupsKeep); err != nil {
			m.logf("the dumps could not be pruned to %d: %v", j.Job.BackupsKeep, err)
		}
	}
	_ = m.endStep(StepWatch, "done", "")
	_ = m.endStep(StepCommit, "done", "")
	_ = m.mutate(func(j *Journal) { j.State, j.Step = StateCommitted, "" })
	m.logf("committed %s", j.Job.Version)
	m.replaceLauncher()
	return true
}

// replaceLauncher hands over to the release's launcher when it needs a newer one (story
// 35.7 AC6): put in place, then a restart (exit 75) the running launcher answers by
// running the new one. A failure leaves the release committed (the launcher it runs
// under speaks the protocol it was installed with; the preflight refused the others).
func (m *Machine) replaceLauncher() {
	r, ok := m.Installer.(LauncherReplacer)
	if !ok {
		return
	}
	j := m.Status().Journal
	if j == nil || !wantsNewLauncher(j) {
		return
	}
	_ = m.beginStep(StepLauncher)
	replaced, err := r.ReplaceLauncher(j)
	if err != nil {
		_ = m.endStep(StepLauncher, "failed", err.Error())
		m.logf("the launcher could not be replaced: %v", err)
		return
	}
	if !replaced {
		_ = m.endStep(StepLauncher, "skipped", "")
		return
	}
	// the TV processes run on across this restart when they can (story 35.7): then it
	// cuts no TV
	handsOver, _ := m.tvHandover(j, j.Job.Version, true)
	if !handsOver && m.TVOnAir != nil && m.TVOnAir() {
		// a restart now would cut the TV this machine keeps on air, outside the gates the
		// run's restarts pass: the new launcher takes over at the next restart instead
		_ = m.endStep(StepLauncher, "done", fmt.Sprintf("launcher protocol %d: takes over at the next restart "+
			"(TV on air here)", j.LauncherProtocol))
		m.logf("the launcher of %s (protocol %d) takes over at the next restart: this machine keeps TV on air",
			j.Job.Version, j.LauncherProtocol)
		return
	}
	_ = m.endStep(StepLauncher, "done", fmt.Sprintf("launcher protocol %d", j.LauncherProtocol))
	if needsNewLauncher(j) {
		m.logf("the launcher of %s (protocol %d) takes over: restarting", j.Job.Version, j.LauncherProtocol)
	} else {
		m.logf("the launcher of %s takes over (it keeps the TV processes running across restarts): restarting",
			j.Job.Version)
	}
	if m.Exit != nil {
		m.Exit(75)
	}
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
		m.errorf("cannot roll back (no previous release): %s", reason)
		return
	}
	if j.Rollbacks >= 2 {
		// switched back twice and the new release came up again both times (an install
		// cut short that the switch back cannot undo): a person decides, no more loops
		_ = m.mutate(func(j *Journal) {
			j.State, j.Reason = StateFailed, reason
			j.Error = &Failure{Step: StepRollback, Code: "rollback_loop",
				Detail: fmt.Sprintf("the switch back to %s did not take twice: %s", orNone(j.From), reason)}
		})
		m.errorf("the switch back to %s did not take twice: a person is needed", orNone(j.From))
		return
	}
	_ = m.endStep(StepWatch, "failed", reason)
	_ = m.mutate(func(j *Journal) { j.State, j.Reason, j.Rollbacks = StateRollingBack, reason, j.Rollbacks+1 })
	_ = m.beginStep(StepRollback)
	m.warnf("rolling back %s to %s: %s", j.Job.Version, orNone(j.From), reason)
	// the TV first (story 35.6 AC8): its change cancelled, or rolled back on the air
	m.withdrawTV(reason)
	migrated := j.Job.Home && m.Records != nil && m.Records.Migrated(j.Job.Version)
	if migrated {
		if m.Rehearser != nil {
			if p, err := m.Rehearser.DumpAside(context.Background(), "failed-"+j.Job.Version); err == nil {
				m.logf("the database as %s left it is in %s", j.Job.Version, p)
			} else {
				m.warnf("could not dump the failed state aside: %v", err)
			}
		}
		if _, err := m.Records.MarkRestore(j.Job.Version, j.From, reason); err != nil {
			m.errorf("cannot mark the database record for the dump to be put back: %v", err)
		}
	}
	if err := m.withJournal(func(j *Journal) error { return m.Installer.Revert(context.Background(), j) }); err != nil {
		if migrated {
			// the new release stays, and so does its database: no start may put the dump
			// back under it
			if uerr := m.Records.Unmark(j.Job.Version); uerr != nil {
				m.errorf("cannot take the restore mark back: %v", uerr)
			}
		}
		f := failure(StepRollback, err)
		_ = m.endStep(StepRollback, "failed", f.Error())
		_ = m.mutate(func(j *Journal) { j.State, j.Error = StateFailed, f })
		m.errorf("the switch back failed: %s", f.Error())
		return
	}
	m.armHandover(j.From)
	if m.Exit != nil {
		m.Exit(75)
	}
}

// tvHandover is TVHandover (false when there is none).
func (m *Machine) tvHandover(j *Journal, target string, arm bool) (bool, string) {
	if m.TVHandover == nil || j == nil {
		return false, ""
	}
	return m.TVHandover(j, target, arm)
}

// armHandover makes the next exit 75 hand the TV processes over to the start on target
// when they can run on across it (story 35.7), and says which it is in the run's log:
// a restart that cannot hand over restarts every process, the TV's too (AC4).
func (m *Machine) armHandover(target string) {
	if m.TVHandover == nil {
		return
	}
	ok, why := m.tvHandover(m.Status().Journal, target, true)
	_ = m.mutate(func(j *Journal) { j.HandsOver, j.HandoverWhy = ok, why })
	if ok {
		m.logf("the TV processes run on across the restart onto %s", target)
	} else if why != "" {
		m.logf("every process restarts onto %s, the TV's too: %s", target, why)
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
