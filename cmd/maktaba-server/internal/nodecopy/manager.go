package nodecopy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbin"
)

// StatusFile is the copy's status in the node's state directory (the node's API
// shows it on /api/cluster/self).
const StatusFile = "copy.json"

// Manager makes and keeps this machine's copy (see the package doc). It runs for as
// long as the supervisor does.
type Manager struct {
	Copy Copy
	// Programs fetches the Postgres programs (pgbin.Ensure for this platform): the
	// directory that holds bin/.
	Programs func(ctx context.Context) (string, error)
	// Major is the programs' Postgres major version.
	Major string
	// Want starts (true) or stops the copy's supervised process; Running reports
	// whether it runs.
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

	mu     sync.Mutex
	status Status
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
	prev := m.status
	m.status = s
	m.mu.Unlock()
	if prev.State != s.State || prev.Detail != s.Detail {
		m.logf("%s%s", s.State, map[bool]string{true: ": " + s.Detail, false: ""}[s.Detail != ""])
	}
	if m.StateDir != "" {
		if b, err := json.Marshal(s); err == nil {
			_ = writeSecret(filepath.Join(m.StateDir, StatusFile), b)
		}
	}
	if s.State != StateRevoked {
		_ = m.Copy.Report(ctx, s)
	}
}

// state is a status of a state and detail, with what the copy's marker says.
func (m *Manager) state(state, detail string) Status {
	s := Status{State: state, Detail: detail, Checked: m.now().UTC()}
	if mk, ok := m.Copy.Built(); ok {
		b := mk.Built
		s.Built, s.HomeMajor = &b, mk.Major
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
	// the programs (fetched once, then on this machine)
	for {
		m.set(ctx, m.state(StatePreparing, ""))
		bin, err := m.Programs(ctx)
		if err == nil {
			m.Copy.Bin = filepath.Join(bin, "bin")
			break
		}
		m.set(ctx, m.state(StateError, "the Postgres programs: "+err.Error()))
		if !sleep(ctx, 10*m.retry()) {
			return
		}
	}
	if _, err := m.Copy.Password(); err != nil {
		m.set(ctx, m.state(StateError, "the copy's password: "+err.Error()))
		return
	}
	// a copy made before serves at once (the home may be away right now)
	if _, ok := m.Copy.Built(); ok {
		_ = m.Want(true)
	}
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
		if g.Major() != m.Major {
			_ = m.Want(false)
			m.set(ctx, m.state(StateIncompatible, fmt.Sprintf("the home runs Postgres %s, this machine's copy needs %s: "+
				"an update of Maktaba on both brings them together", g.Major(), m.Major)))
			if !sleep(ctx, 10*m.retry()) {
				return
			}
			continue
		}
		mk, built := m.Copy.Built()
		if why := rebuildReason(mk, built, g); why != "" {
			if err := m.rebuild(ctx, built, why); err != nil {
				st := StateError
				if errors.Is(err, ErrIncompatible) {
					st = StateIncompatible
				}
				m.set(ctx, m.state(st, err.Error()))
				if !sleep(ctx, backoff) {
					return
				}
				backoff = min(2*backoff, time.Hour)
				continue
			}
			backoff = m.retry()
		}
		_ = m.Want(true)
		if !m.watch(ctx, 4*m.retry()) {
			return
		}
	}
}

// rebuildReason says why the copy must be made (anew), or "".
func rebuildReason(mk Marker, built bool, g Grant) string {
	switch {
	case !built:
		return "this machine has no copy yet"
	case mk.SystemID != g.SystemID:
		return "the home's database is another one now"
	case mk.Major != g.Major():
		return "the home's Postgres is of another major version now"
	case g.SlotMade:
		return "the home gave up on this copy (it fell too far behind)"
	}
	return ""
}

// rebuild stops the copy and copies the home's database (anew).
func (m *Manager) rebuild(ctx context.Context, built bool, why string) error {
	state := StateCopying
	if built {
		state = StateRebuilding
		m.logf("copying anew: %s", why)
	}
	m.set(ctx, m.state(state, why))
	_ = m.Want(false)
	for deadline := m.now().Add(2 * time.Minute); m.Running != nil && m.Running(); {
		if m.now().After(deadline) {
			return errors.New("nodecopy: the copy did not stop")
		}
		if !sleep(ctx, 500*time.Millisecond) {
			return ctx.Err()
		}
	}
	var g Grant
	var err error
	for i := 0; i < 10; i++ {
		// a slot still in use is the stopped copy's session ending on the home
		if g, err = m.Copy.Ask(ctx, true); err == nil || kindOf(err) != kindBusy {
			break
		}
		if !sleep(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	if err != nil {
		return fmt.Errorf("nodecopy: asking the home for a fresh copy: %w", err)
	}
	started := m.now()
	if err := m.Copy.Build(ctx, g, m.Log); err != nil {
		return err
	}
	m.logf("copied the home's database in %s", m.now().Sub(started).Round(time.Second))
	return nil
}

// watchState is the state to show while the home cannot be asked: the copy's own
// when there is one, else waiting.
func (m *Manager) watchState(ctx context.Context, detail string) Status {
	if _, ok := m.Copy.Built(); !ok {
		return m.state(StateWaiting, detail)
	}
	s := m.Copy.Observe(ctx, m.now())
	if s.State != StateFollowing {
		s.Detail = detail
	}
	return s
}

// watch observes the running copy every period for d (false: ctx ended).
func (m *Manager) watch(ctx context.Context, d time.Duration) bool {
	end := m.now().Add(d)
	for {
		if _, ok := m.Copy.Built(); ok {
			m.set(ctx, m.Copy.Observe(ctx, m.now()))
		}
		left := end.Sub(m.now())
		if left <= 0 {
			return true
		}
		if !sleep(ctx, min(left, m.every())) {
			return false
		}
	}
}

// revoked wipes the copy: this machine is not the home's any more.
func (m *Manager) revoked(ctx context.Context, cause error) {
	_ = m.Want(false)
	for deadline := m.now().Add(2 * time.Minute); m.Running != nil && m.Running() && m.now().Before(deadline); {
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
