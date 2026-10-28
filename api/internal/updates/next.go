package updates

import (
	"context"
	"time"
)

// What happens next to a machine behind the cluster's release (Machine.Next): the same
// decision the catch-up makes (catchUpFor), so the Updates page says what the driver does.
const (
	// NextInRun: the update under way brings it, after the home.
	NextInRun = "in_run"
	// NextCatchUpNow: the next catch-up (within a minute) takes it.
	NextCatchUpNow = "catch_up_now"
	// NextCatchUpWindow: it is caught up in the next quiet window (At its start).
	NextCatchUpWindow = "catch_up_window"
	// NextCatchUpRetry: a catch-up was tried; the next one is At.
	NextCatchUpRetry = "catch_up_retry"
	// NextHeldBack: the release failed or was rolled back there, or an admin skipped it:
	// it waits for a newer release or an admin's Retry.
	NextHeldBack = "held_back"
	// NextBusy: it runs an update of its own.
	NextBusy = "busy"
	// NextOffline: its machine agent is not connected; it is caught up when it is back.
	NextOffline = "offline"
	// NextAutoInstallOff: automatic installs are off; Update now or Retry brings it.
	NextAutoInstallOff = "auto_install_off"
	// NextNotVerified: the home's release is not one the catalog verified (installed by
	// hand from elsewhere) or it was withdrawn: no catch-up brings it.
	NextNotVerified = "not_verified"
)

// NextStep is what happens next to a machine behind the cluster's release.
type NextStep struct {
	// Version is the release it is brought to (the cluster's).
	Version string `json:"version"`
	// Reason is one of the Next* values.
	Reason string `json:"reason"`
	// At is when (the window's start, the retry's), nil when it does not wait for a time.
	At *time.Time `json:"at,omitempty"`
}

// catchUpScope is what a catch-up decides with, read once for every machine.
type catchUpScope struct {
	pol       Policy
	wm        string
	rel       Release
	relOK     bool
	inWindow  bool
	immediate bool
	// nextWindow is the next quiet window's start (zero inside it, or without a window)
	nextWindow time.Time
	streams    map[string]bool
}

// scopeFor reads the catch-up's scope for the cluster's release wm.
func (d *Driver) scopeFor(ctx context.Context, pol Policy, wm string) catchUpScope {
	sc := catchUpScope{pol: pol, wm: wm, inWindow: true, immediate: pol.CatchUp != "window", streams: map[string]bool{}}
	if rel, err := d.Store.Get(ctx, wm); err == nil && rel.VerifyState == VerifyVerified && rel.WithdrawnAt == nil {
		sc.rel, sc.relOK = rel, true
	}
	if w, err := ParseWindow(pol.Window); err == nil {
		loc := time.Local
		if d.Svc != nil {
			loc = d.Svc.loc()
		}
		_, _, sc.inWindow = w.Containing(d.now(), loc)
		if !sc.inWindow {
			if s, _ := w.Occurrence(d.now().In(loc), loc); s.After(d.now()) {
				sc.nextWindow = s
			} else {
				sc.nextWindow, _ = w.Occurrence(d.now().In(loc).AddDate(0, 0, 1), loc)
			}
		}
	}
	if !sc.inWindow {
		sc.streams = d.streamingHosts(ctx)
	}
	return sc
}

// catchUpFor is what happens next to machine n, on ver (behind sc.wm): NextCatchUpNow
// when the catch-up takes it now. It does not change anything (the page asks it too).
func (d *Driver) catchUpFor(ctx context.Context, sc catchUpScope, n nodeRow, s agentState, ver string, online bool) NextStep {
	ns := NextStep{Version: NormalizeVersion(sc.wm)}
	at := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		u := t.UTC()
		return &u
	}
	switch {
	case !online:
		ns.Reason = NextOffline
		return ns
	case !sc.pol.EffectiveAutoInstall():
		ns.Reason = NextAutoInstallOff
		return ns
	case !sc.relOK:
		ns.Reason = NextNotVerified
		return ns
	}
	if s.Run != nil && (s.Run.State == MPreparing || s.Run.State == MApplying || s.Run.State == MRestarting ||
		s.Run.State == MWatching || beforeGo(s.Run.State)) {
		ns.Reason = NextBusy
		return ns
	}
	// it restarts machines: catch_up "immediate" any time, but a machine that keeps TV
	// streams on air (and every machine with catch_up "window") inside the window only; a
	// machine the release's schema pauses (its local routes answer 503 until it runs the
	// release) at once, always (README, rules.updates.catch_up)
	if !sc.inWindow && !d.schemaPaused(ctx, sc.rel, ver) && (!sc.immediate || sc.streams[n.id]) {
		ns.Reason, ns.At = NextCatchUpWindow, at(sc.nextWindow)
		return ns
	}
	// the release failed or was rolled back on it (by itself, or by an admin): it waits
	// for a newer release or an admin's Retry (a part a person mends is tried again once
	// the machine changed, or after catchUpRetry: heldBack)
	if held, until := d.heldBackUntil(ctx, n.id, sc.wm, ver, s.Layout, s.Attempts); held {
		if until.IsZero() {
			ns.Reason = NextHeldBack
		} else {
			ns.Reason, ns.At = NextCatchUpRetry, at(until)
		}
		return ns
	}
	d.mu.Lock()
	last, tried := d.pending[pendingKey(n.id, sc.wm)]
	d.mu.Unlock()
	if tried && d.now().Sub(last) < catchUpRetry {
		ns.Reason, ns.At = NextCatchUpRetry, at(last.Add(catchUpRetry))
		return ns
	}
	ns.Reason = NextCatchUpNow
	return ns
}

// LastUpdate is a machine's last update that completed: the newest installed attempt
// (its own, a release's start, or its part in a run that ended healthy). A rolled-back,
// failed, cancelled or superseded attempt is never its last update.
func lastUpdate(attempts []Attempt, more ...Attempt) *Attempt {
	var best *Attempt
	for _, list := range [][]Attempt{attempts, more} {
		for i := range list {
			a := list[i]
			if a.Outcome != OutcomeInstalled || a.To == "" {
				continue
			}
			if best == nil || a.At.After(best.At) {
				c := a
				best = &c
			}
		}
	}
	return best
}

// lastHealthyTarget is a machine's newest part in a run that ended healthy (ok false: none).
func (s *Store) lastHealthyTarget(ctx context.Context, nodeID string) (Attempt, bool) {
	var runID string
	err := s.DB.QueryRowContext(ctx, `SELECT t.run_id FROM update_targets t JOIN update_runs r ON r.id = t.run_id
		WHERE t.node_id = $1 AND t.state = $2 ORDER BY r.started_at DESC LIMIT 1`, nodeID, TargetHealthy).Scan(&runID)
	if err != nil {
		return Attempt{}, false
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return Attempt{}, false
	}
	targets, err := s.Targets(ctx, runID)
	if err != nil {
		return Attempt{}, false
	}
	for _, t := range targets {
		if t.NodeID == nodeID && t.State == TargetHealthy {
			return targetAttempt(run, t), true
		}
	}
	return Attempt{}, false
}

// lastHomeInstall is the home's newest run that installed its release on the home (a run
// that ended done: not a rolled-back, failed, cancelled or superseded one, nor a catch-up
// of the machines).
func (s *Store) lastHomeInstall(ctx context.Context) (Attempt, bool) {
	runs, err := s.Runs(ctx, 50)
	if err != nil {
		return Attempt{}, false
	}
	for _, r := range runs {
		switch r.State {
		case RunDone, RunDoneFailures, RunDoneNeedsPerson, RunHeld:
		default:
			continue
		}
		if r.FinishedAt == nil {
			continue
		}
		if _, det, err := s.RunDetailOf(ctx, r.ID); err == nil && det.NoHome {
			continue
		}
		return runAttempt(r), true
	}
	return Attempt{}, false
}
