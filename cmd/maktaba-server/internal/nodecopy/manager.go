package nodecopy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// StatusFile is the copy's status in the node's state directory (the node's API
// shows it on /api/cluster/self).
const StatusFile = "copy.json"

// Manager makes and keeps this machine's copy (see the package doc). It runs for as
// long as the supervisor does.
type Manager struct {
	Copy Copy
	// Programs finds or fetches the Postgres programs: the directory that holds bin/.
	Programs func(ctx context.Context) (string, error)
	// Want starts (true) or stops this machine's database (the supervisor's copy role);
	// Running reports whether it runs.
	Want    func(on bool) error
	Running func() bool
	// StateDir holds StatusFile ("": not written).
	StateDir string
	Log      io.Writer
	Now      func() time.Time

	// Every is the watch's period (0: 15 s), Retry the wait after a failure (0: 1 min).
	Every, Retry time.Duration
	// The home refusing this machine WipeAfter times (0: 3) over WipeOver at least
	// (0: 3 min) means it is not the home's any more: the copy is wiped.
	WipeAfter int
	WipeOver  time.Duration
	// StuckAfter: errors applying the home's changes for this long (0: 10 min) mean the
	// copy cannot follow any more: it subscribes anew.
	StuckAfter time.Duration

	mu     sync.Mutex
	status Status
	// erring: since when the subscription's error count keeps rising (zero: it does not)
	erring   time.Time
	lastErrs int64
	// reporting: a report to the home is under way; pending: the status to report next
	reporting bool
	pending   *Status
	// local is this machine's schema version, once migrated
	local int64
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logf(format string, args ...any) {
	if m.Log != nil {
		fmt.Fprintf(m.Log, "[copy] "+format+"\n", args...)
	}
}

func (m *Manager) every() time.Duration {
	if m.Every > 0 {
		return m.Every
	}
	return 15 * time.Second
}

func (m *Manager) retry() time.Duration {
	if m.Retry > 0 {
		return m.Retry
	}
	return time.Minute
}

func (m *Manager) wipeAfter() int {
	if m.WipeAfter > 0 {
		return m.WipeAfter
	}
	return 3
}

func (m *Manager) wipeOver() time.Duration {
	if m.WipeOver > 0 {
		return m.WipeOver
	}
	return 3 * time.Minute
}

func (m *Manager) stuckAfter() time.Duration {
	if m.StuckAfter > 0 {
		return m.StuckAfter
	}
	return 10 * time.Minute
}

// Status is the copy's last status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// set records and publishes a status: the state file, and the home when it answers.
func (m *Manager) set(ctx context.Context, s Status) {
	if s.Checked.IsZero() {
		s.Checked = m.now().UTC()
	}
	m.mu.Lock()
	if s.LocalSchema == 0 {
		s.LocalSchema = m.local
	}
	prev := m.status
	m.status = s
	m.mu.Unlock()
	if prev.State != s.State || prev.Detail != s.Detail {
		if s.Detail != "" {
			m.logf("%s: %s", s.State, s.Detail)
		} else {
			m.logf("%s", s.State)
		}
	}
	if m.StateDir != "" {
		if b, err := json.Marshal(s); err == nil {
			_ = writeSecret(filepath.Join(m.StateDir, StatusFile), b)
		}
	}
	if s.State != StateRevoked {
		m.report(ctx, s)
	}
}

// report tells the home a status in the background (the latest one, one at a time):
// a home that does not answer never holds the copy up.
func (m *Manager) report(ctx context.Context, s Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reporting {
		m.pending = &s
		return
	}
	m.reporting = true
	go func() {
		for {
			_ = m.Copy.Report(ctx, s)
			m.mu.Lock()
			if m.pending == nil || ctx.Err() != nil {
				m.reporting, m.pending = false, nil
				m.mu.Unlock()
				return
			}
			s, m.pending = *m.pending, nil
			m.mu.Unlock()
		}
	}()
}

// state is a status of a state and detail, with what the copy's marker says.
func (m *Manager) state(state, detail string) Status {
	s := Status{State: state, Detail: detail, Checked: m.now().UTC()}
	if mk, ok := m.Copy.Mark(); ok && !mk.Subscribed.IsZero() {
		t := mk.Subscribed
		s.Subscribed = &t
	}
	return s
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Run keeps the copy until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	local, ok := m.prepare(ctx)
	if !ok {
		return
	}
	m.mu.Lock()
	m.local = local
	m.mu.Unlock()
	refused := 0
	var firstRefused time.Time
	backoff := m.retry()
	for ctx.Err() == nil {
		g, err := m.Copy.Ask(ctx, false)
		if err != nil {
			switch kindOf(err) {
			case kindRefused:
				if refused == 0 {
					firstRefused = m.now()
				}
				refused++
				// refused three times over three minutes at least: no longer the home's
				if refused >= m.wipeAfter() && m.now().Sub(firstRefused) >= m.wipeOver() {
					m.revoked(ctx, err)
					return
				}
				m.set(ctx, m.watchState(ctx, "the home refused this machine: "+err.Error()))
			case kindUnsupported:
				refused = 0
				m.set(ctx, m.state(StateUnavailable, "the home's database keeps no copies: "+err.Error()))
				if !sleep(ctx, 10*m.retry()) {
					return
				}
				continue
			default:
				refused = 0
				m.set(ctx, m.watchState(ctx, "the home's database does not answer"))
			}
			if !m.watch(ctx, m.retry()) {
				return
			}
			continue
		}
		refused = 0
		if g.SchemaVersion > local {
			// the home's changes would not apply until this machine updates
			_ = m.Copy.Pause(ctx)
			m.set(ctx, m.state(StateIncompatible, fmt.Sprintf("Maktaba on this machine is older than the home's "+
				"(its database is at %d, the home's at %d): its copy follows again once it updates", local, g.SchemaVersion)))
			if !sleep(ctx, 10*m.retry()) {
				return
			}
			continue
		}
		if why := m.resubscribeReason(g, local); why != "" {
			if err := m.resubscribe(ctx, local, why); err != nil {
				m.set(ctx, m.state(StateError, err.Error()))
				if !sleep(ctx, backoff) {
					return
				}
				backoff = min(2*backoff, time.Hour)
				continue
			}
			backoff = m.retry()
		} else if kept, err := m.Copy.Follow(ctx, g); err != nil {
			m.set(ctx, m.state(StateError, "keeping the subscription: "+err.Error()))
		} else if !kept {
			if err := m.resubscribe(ctx, local, "the subscription is not the home's any more"); err != nil {
				m.set(ctx, m.state(StateError, err.Error()))
			}
		}
		if !m.watch(ctx, 4*m.retry()) {
			return
		}
	}
}

// prepare makes this machine's database ready for the copy: the programs, the
// database itself (a database of this machine's own from before it joined is moved
// aside), running, its read-only role, its migrations. This machine's schema version.
func (m *Manager) prepare(ctx context.Context) (int64, bool) {
	for {
		m.set(ctx, m.state(StatePreparing, ""))
		err := m.prepareOnce(ctx)
		if err == nil {
			break
		}
		m.set(ctx, m.state(StateError, err.Error()))
		if !sleep(ctx, m.retry()) {
			return 0, false
		}
	}
	for {
		local, err := m.migrate(ctx)
		if err == nil {
			return local, true
		}
		m.set(ctx, m.state(StateError, "this machine's database: "+err.Error()))
		if !sleep(ctx, m.retry()) {
			return 0, false
		}
	}
}

func (m *Manager) prepareOnce(ctx context.Context) error {
	home, err := m.Programs(ctx)
	if err != nil {
		return fmt.Errorf("the Postgres programs: %w", err)
	}
	m.Copy.DB.Home = home
	if _, err := m.Copy.Password(); err != nil {
		return fmt.Errorf("the copy's password: %w", err)
	}
	if _, err := m.Copy.ReaderPassword(); err != nil {
		return fmt.Errorf("the copy's password: %w", err)
	}
	if !m.running() {
		if aside, err := m.Copy.Claim(m.now()); err != nil {
			return err
		} else if aside != "" {
			m.logf("this machine's own database from before it joined is kept, untouched, at %s", aside)
		}
		if err := m.Copy.DB.Prepare(ctx, m.Log); err != nil {
			return fmt.Errorf("this machine's database: %w", err)
		}
		if _, ok := m.Copy.Mark(); !ok {
			if err := m.Copy.writeMark(Marker{}); err != nil {
				return err
			}
		}
	}
	if err := m.Want(true); err != nil {
		return err
	}
	deadline := m.now().Add(5 * time.Minute)
	for {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := pgbundle.Ready(rctx, m.Copy.Addr(), pgbundle.AppRole, pgbundle.AppDatabase)
		cancel()
		if err == nil {
			break
		}
		if m.now().After(deadline) {
			return fmt.Errorf("this machine's database does not start: %w", err)
		}
		if !sleep(ctx, time.Second) {
			return ctx.Err()
		}
	}
	return m.Copy.EnsureReader(ctx)
}

// migrate brings this machine's database to this machine's schema (its own migrations)
// and says how far that is.
func (m *Manager) migrate(ctx context.Context) (int64, error) {
	if m.Copy.Migrate != nil {
		dsn, err := m.Copy.DB.URL()
		if err != nil {
			return 0, err
		}
		if err := m.Copy.Migrate(ctx, dsn); err != nil {
			return 0, fmt.Errorf("its migrations: %w", err)
		}
	}
	return m.Copy.LocalSchema(ctx)
}

func (m *Manager) running() bool { return m.Running != nil && m.Running() }

// resubscribeReason says why the copy must subscribe (anew), or "".
func (m *Manager) resubscribeReason(g Grant, local int64) string {
	mk, ok := m.Copy.Mark()
	switch {
	case !ok || mk.Subscribed.IsZero():
		return "this machine has no copy yet"
	case mk.SystemID != g.SystemID:
		return "the home's database is another one now"
	case mk.LocalSchema != local:
		// its own migrations may have put rows the home's changes would collide with
		return "this machine's Maktaba was updated"
	case !g.SlotExists:
		return "the home gave up on this copy (it fell too far behind)"
	}
	m.mu.Lock()
	stuck := !m.erring.IsZero() && m.now().Sub(m.erring) >= m.stuckAfter()
	m.mu.Unlock()
	if stuck {
		return "the home's changes keep failing to apply"
	}
	return ""
}

// resubscribe subscribes the copy (anew): every row copied again.
func (m *Manager) resubscribe(ctx context.Context, local int64, why string) error {
	state := StateRebuilding
	if mk, ok := m.Copy.Mark(); !ok || mk.Subscribed.IsZero() {
		state = StateCopying
	}
	m.set(ctx, m.state(state, why))
	var g Grant
	var err error
	for i := 0; i < 10; i++ {
		// a slot still in use is the dropped subscription's session ending on the home
		if g, err = m.Copy.Ask(ctx, true); err == nil || kindOf(err) != kindBusy {
			break
		}
		if !sleep(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	if err != nil {
		return fmt.Errorf("asking the home for a fresh copy: %w", err)
	}
	if err := m.Copy.Subscribe(ctx, g); err != nil {
		return err
	}
	m.mu.Lock()
	m.erring, m.lastErrs = time.Time{}, 0
	m.mu.Unlock()
	m.logf("subscribed to the home's publication (%d tables): %s", len(g.Tables), why)
	return m.Copy.writeMark(Marker{SystemID: g.SystemID, LocalSchema: local, Subscribed: m.now().UTC()})
}

// watchState is the state to show while the home cannot be asked: the copy's own,
// with why the home was not asked.
func (m *Manager) watchState(ctx context.Context, detail string) Status {
	s := m.Copy.Observe(ctx, m.now())
	if s.State != StateFollowing && s.State != StateCopying {
		s.Detail = detail
	}
	return s
}

// watch observes the copy every period for d (false: ctx ended), and notes whether
// the home's changes keep failing to apply.
func (m *Manager) watch(ctx context.Context, d time.Duration) bool {
	end := m.now().Add(d)
	for {
		s := m.Copy.Observe(ctx, m.now())
		m.noteErrors(s)
		m.set(ctx, s)
		left := end.Sub(m.now())
		if left <= 0 {
			return true
		}
		if !sleep(ctx, min(left, m.every())) {
			return false
		}
	}
}

func (m *Manager) noteErrors(s Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case s.Errors > m.lastErrs && m.erring.IsZero():
		m.erring = m.now()
	case s.Errors == m.lastErrs && !m.erring.IsZero() && s.State == StateFollowing:
		m.erring = time.Time{}
	}
	m.lastErrs = s.Errors
}

// revoked wipes the copy: this machine is not the home's any more.
func (m *Manager) revoked(ctx context.Context, cause error) {
	if db, err := m.Copy.super(); err == nil {
		_ = m.Copy.unsubscribe(ctx, db)
		_ = db.Close()
	}
	_ = m.Want(false)
	for deadline := m.now().Add(2 * time.Minute); m.running() && m.now().Before(deadline); {
		if !sleep(ctx, 500*time.Millisecond) {
			return
		}
	}
	err := m.Copy.Wipe()
	detail := "the home refused this machine (" + cause.Error() + "): its copy of the home's database is wiped"
	if err != nil {
		detail += " (not all of it: " + err.Error() + ")"
	}
	m.set(ctx, m.state(StateRevoked, detail))
}

// MajorOf is the Postgres major version of the install at home ("" when it does not
// run): `postgres --version`.
func MajorOf(home string) string {
	out, err := exec.Command(filepath.Join(home, "bin", pgbin.Exe("postgres")), "--version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	v := f[len(f)-1]
	if i := strings.IndexByte(v, '.'); i > 0 {
		return v[:i]
	}
	return v
}

// Installed is Programs for an install this machine already has.
func Installed(home string) func(ctx context.Context) (string, error) {
	return func(context.Context) (string, error) { return home, nil }
}

// ProgramsFor is Programs for this platform's pinned build under root.
func ProgramsFor(root string, fetch pgbin.Fetcher, log io.Writer) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		pin, ok := pgbin.Here()
		if !ok {
			return "", fmt.Errorf("no Postgres build for this platform")
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		return pgbin.Ensure(ctx, root, pin, fetch, log)
	}
}
