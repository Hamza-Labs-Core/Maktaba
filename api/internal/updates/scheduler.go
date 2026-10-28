package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// Blocker reasons (story 35.3 AC5): a due run waits while one holds. A TV change never
// holds an update (the owner's rule of 2026-10-28, "tv changes should not block
// updates"): the TV change controller sets it aside while the update runs and checks it
// again after (tvchange/updatehold.go), and only the restart waits for a swap in progress
// (Driver.tvSwapWait).
const (
	BlockActiveRun             = "run_active"
	BlockRoleNotRunning        = "role_not_running"
	BlockRecentCrash           = "recent_crash"
	BlockSupervisorUnreachable = "supervisor_unreachable"
	BlockSwitchover            = "switchover_open" // a switch of the home (Epic 37) is under way
)

// Waiting reasons (Plan.Waiting): why the target does not install now.
const (
	WaitAutoInstallOff = "auto_install_off"
	WaitNotBuilt       = "automatic_installs_not_built" // phase A (plan 35.3 §4)
	WaitDeferred       = "deferred"
	WaitReleaseAge     = "release_age"
	WaitWindow         = "outside_window"
	// a release whose automatic run failed in this window (story 35.4 AC9: retried, at
	// most one counted attempt a window): tried again at once, then after retryBackoff,
	// then in the next window; one a person must mend waits for the next window (or an
	// admin's Update now)
	WaitRetryBackoff = "retry_backoff"
	WaitRetryWindow  = "retry_next_window"
	WaitNeedsPerson  = "needs_person"
)

// retryBackoff is the pause before a release's third automatic run in one window;
// triesPerWindow how many automatic runs of one release a window takes.
const (
	retryBackoff   = 15 * time.Minute
	triesPerWindow = 3
)

// Blocker answers whether a run may start now ("" = yes, else the reason).
type Blocker interface {
	Blocked(ctx context.Context) (string, error)
}

// BlockerFunc adapts a function.
type BlockerFunc func(ctx context.Context) (string, error)

// Blocked calls f.
func (f BlockerFunc) Blocked(ctx context.Context) (string, error) { return f(ctx) }

// HomeBlockers are the blockers the home's scheduler runs with (router p35): a switch of
// the home under way, and the home's health (supervisor: its loopback URL; "" is not
// supervised). No TV change is among them.
func HomeBlockers(db *sql.DB, supervisor string, hc *http.Client) []Blocker {
	return []Blocker{SwitchoverBlocker{DB: db}, &HomeHealth{BaseURL: supervisor, HTTP: hc}}
}

// SwitchoverBlocker holds runs while a switch of the home (Epic 37) is under way: both
// drain the machines and touch the home's database (a migration during its fence fails,
// a dump put back drops the new home's copy).
type SwitchoverBlocker struct{ DB *sql.DB }

// Blocked implements Blocker. A database without cluster_switchovers is not blocked.
func (b SwitchoverBlocker) Blocked(ctx context.Context) (string, error) {
	var open bool
	if err := b.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cluster_switchovers
	    WHERE state NOT IN ('done', 'failed', 'cancelled', 'handed_over'))`).Scan(&open); err != nil {
		return "", nil
	}
	if open {
		return BlockSwitchover, nil
	}
	return "", nil
}

// Plan is the next planned update as policy sees it (pure; the Updates page shows it).
type Plan struct {
	Choice
	// AutoInstall is auto_install as this build applies it.
	AutoInstall bool `json:"auto_install"`
	// Due: the run should start now (blockers aside).
	Due bool `json:"due"`
	// NextRunAt is when the automatic run will start (nil: it will not by itself).
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
	// EligibleAt is when the target is old enough (min_release_age_hours).
	EligibleAt *time.Time `json:"eligible_at,omitempty"`
	// Waiting is why it is not due now ("" when due or no target).
	Waiting string `json:"waiting,omitempty"`
}

// MakePlan applies the scheduling rules (design §4, story 35.3 AC5) other than the
// blockers: auto_install, a target that is not held, its age, deferred_until and the
// window (the run starts inside it and at least 30 minutes before its end).
func MakePlan(now time.Time, loc *time.Location, p Policy, catalog []Release, current, watermark string) Plan {
	if loc == nil {
		loc = time.Local
	}
	pl := Plan{Choice: ChooseTarget(catalog, p, current, watermark), AutoInstall: p.EffectiveAutoInstall()}
	if pl.Target == nil || pl.Held {
		pl.Waiting = pl.Reason
		return pl
	}
	eligible := pl.Target.Published().Add(time.Duration(p.MinReleaseAgeHours) * time.Hour)
	pl.EligibleAt = &eligible
	switch {
	case !p.AutoInstall:
		pl.Waiting = WaitAutoInstallOff
		return pl
	case !AutoInstallAvailable():
		pl.Waiting = WaitNotBuilt
		return pl
	}
	w, err := ParseWindow(p.Window)
	if err != nil {
		pl.Waiting = WaitWindow
		return pl
	}
	from, why := now, ""
	if p.DeferredUntil != nil && p.DeferredUntil.After(from) {
		from, why = *p.DeferredUntil, WaitDeferred
	}
	if eligible.After(from) {
		from, why = eligible, WaitReleaseAge
	}
	next := w.NextStart(from, loc)
	if next.IsZero() {
		pl.Waiting = WaitWindow
		return pl
	}
	pl.NextRunAt = &next
	if !next.After(now) {
		pl.Due = true
		return pl
	}
	if why == "" || !w.CanStart(now, loc) {
		why = WaitWindow // outside the window anyway; else the deferral or the age holds it
	}
	pl.Waiting = why
	return pl
}

// Service is the home's updater: policy, the catalog, the scheduler and the admin
// actions. One per API process; the scheduler loop runs only where RunScheduler is set.
type Service struct {
	Store      *Store
	Discoverer *Discoverer
	Starter    Starter
	Notifier   notify.Notifier
	Blockers   []Blocker
	Log        *slog.Logger
	// Current is the running version; BaseChannel the build's channel (Epic 28).
	Current     string
	BaseChannel string
	Getenv      func(string) string
	Loc         *time.Location
	Now         func() time.Time

	// lastTick is the scheduler's last decision (the Updates page shows why nothing
	// starts: a blocker, a retry's pause)
	tickMu   sync.Mutex
	lastTick *TickInfo
}

// TickInfo is the scheduler's last decision and when it was made.
type TickInfo struct {
	Decision
	At time.Time `json:"at"`
}

// LastTick is the scheduler's last decision (nil before the first tick, or where the
// scheduler does not run).
func (s *Service) LastTick() *TickInfo {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	if s.lastTick == nil {
		return nil
	}
	c := *s.lastTick
	return &c
}

// noteTick keeps a tick's decision; a new reason a run waits for is logged once.
func (s *Service) noteTick(d Decision) {
	s.tickMu.Lock()
	prev := s.lastTick
	s.lastTick = &TickInfo{Decision: d, At: s.now()}
	s.tickMu.Unlock()
	if (d.Action == "blocked" || (d.Action == "none" && d.Version != "")) &&
		(prev == nil || prev.Action != d.Action || prev.Reason != d.Reason || prev.Version != d.Version) {
		s.log().Info("update run waits", "event", "update_scheduler_waits", "action", d.Action, "reason", d.Reason,
			"version", d.Version)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) loc() *time.Location {
	if s.Loc != nil {
		return s.Loc
	}
	return time.Local
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) notify(ctx context.Context, kind string, p notify.Payload) {
	if s.Notifier == nil {
		return
	}
	if err := s.Notifier.Notify(ctx, kind, p); err != nil {
		s.log().Warn("update notification failed", "event", "update_notify_error", "kind", kind, "err", err.Error())
	}
}

// Policy is the effective rules.updates.
func (s *Service) Policy(ctx context.Context) (Policy, error) {
	return LoadPolicy(ctx, s.Store.DB, s.BaseChannel, s.Getenv)
}

// Plan is the current plan with what it was made from.
func (s *Service) Plan(ctx context.Context) (Plan, Policy, string, error) {
	pol, err := s.Policy(ctx)
	if err != nil {
		return Plan{}, pol, "", err
	}
	cat, err := s.Store.Catalog(ctx)
	if err != nil {
		return Plan{}, pol, "", err
	}
	wm, err := s.Store.Watermark(ctx)
	if err != nil {
		return Plan{}, pol, "", err
	}
	return MakePlan(s.now(), s.loc(), pol, cat, s.Current, wm), pol, wm, nil
}

// blockedFor runs the blockers for a run of version; the first reason wins: an active run that only waits for desktop apps
// and that version supersedes does not block it (start supersedes it, once the release's
// re-check passed).
func (s *Service) blockedFor(ctx context.Context, version string) (string, error) {
	if a, err := s.Store.ActiveRun(ctx); err != nil {
		return "", err
	} else if a != nil {
		sp, ok := s.Starter.(superseder)
		if version == "" || !ok || !sp.Supersedable(ctx, version) {
			return BlockActiveRun, nil
		}
	}
	for _, b := range s.Blockers {
		why, err := b.Blocked(ctx)
		if err != nil {
			return "", err
		}
		if why != "" {
			return why, nil
		}
	}
	return "", nil
}

// Decision is what one scheduler tick did.
type Decision struct {
	Action  string `json:"action"` // none | held | blocked | withdrawn | started | failed
	Reason  string `json:"reason,omitempty"`
	Version string `json:"version,omitempty"`
	RunID   string `json:"run_id,omitempty"`
}

// Tick is one scheduler step (every minute): start the planned run when it is due and
// nothing blocks it.
func (s *Service) Tick(ctx context.Context) (Decision, error) {
	d, err := s.tick(ctx)
	s.noteTick(d)
	return d, err
}

func (s *Service) tick(ctx context.Context) (Decision, error) {
	pl, pol, _, err := s.Plan(ctx)
	if err != nil {
		return Decision{Action: "none"}, err
	}
	if pl.Target != nil && pl.Held {
		s.hold(ctx, *pl.Target)
		return Decision{Action: "held", Reason: ReasonHeld, Version: pl.Target.Version}, nil
	}
	if pl.Target == nil || !pl.Due {
		if pl.Target != nil && pl.NextRunAt != nil {
			s.notify(ctx, NoticeScheduled, notify.Payload{Severity: notify.SeverityInfo,
				Params:  map[string]any{"version": pl.Target.Version, "at": pl.NextRunAt.Format(time.RFC3339)},
				Link:    "/admin/updates",
				Subject: "release:" + pl.Target.Version, Every: notifyOnce})
		}
		return Decision{Action: "none", Reason: pl.Waiting}, nil
	}
	if wait := s.retryWait(ctx, pol, pl.Target.Version); wait != "" {
		return Decision{Action: "none", Reason: wait, Version: pl.Target.Version}, nil
	}
	why, err := s.blockedFor(ctx, pl.Target.Version)
	if err != nil {
		return Decision{Action: "none"}, err
	}
	if why != "" {
		return Decision{Action: "blocked", Reason: why, Version: pl.Target.Version}, nil
	}
	run, err := s.start(ctx, *pl.Target, pol, TriggerAuto, "")
	if err != nil {
		var w *withdrawnError
		if errors.As(err, &w) {
			return Decision{Action: "withdrawn", Reason: w.reason, Version: pl.Target.Version}, nil
		}
		if errors.Is(err, ErrAutoInstallNotBuilt) {
			return Decision{Action: "none", Reason: WaitNotBuilt, Version: pl.Target.Version}, nil
		}
		return Decision{Action: "failed", Reason: err.Error(), Version: pl.Target.Version, RunID: run.ID}, err
	}
	return Decision{Action: "started", Version: run.Version, RunID: run.ID}, nil
}

// retryWait says why a due automatic run of version waits for an earlier one that failed
// in this window ("" when it may start): one that failed for a person to mend (no room,
// an install that cannot update itself, an app built locally) waits for the next window
// or an admin's Update now; one that failed otherwise is tried again at once, then after
// retryBackoff, then in the next window. A run a GitHub call, a notice and an
// update_runs row each, so nothing starts every minute (story 35.4 AC9).
func (s *Service) retryWait(ctx context.Context, pol Policy, version string) string {
	w, err := ParseWindow(pol.Window)
	if err != nil {
		return ""
	}
	start, _, ok := w.Containing(s.now(), s.loc())
	if !ok {
		return ""
	}
	failed, err := s.Store.FailedAutoRuns(ctx, version, start)
	if err != nil || len(failed) == 0 {
		return ""
	}
	if needsPerson(runErrDoc(failed[0])) {
		return WaitNeedsPerson
	}
	switch {
	case len(failed) >= triesPerWindow:
		return WaitRetryWindow
	case len(failed) == triesPerWindow-1 && failed[0].FinishedAt != nil &&
		s.now().Before(failed[0].FinishedAt.Add(retryBackoff)):
		return WaitRetryBackoff
	}
	return ""
}

// runErrDoc is a run's error document (empty when it has none).
func runErrDoc(r Run) map[string]any {
	doc := map[string]any{}
	if len(r.Error) > 0 {
		_ = json.Unmarshal(r.Error, &doc)
	}
	return doc
}

// hold records and notifies a contract release once: it waits for an admin on the
// Changes page (an approval item) and in the notification centre (story 35.3 AC4).
func (s *Service) hold(ctx context.Context, r Release) {
	first, err := s.Store.SetHeld(ctx, r.Version)
	if err != nil || !first {
		return
	}
	s.Store.Audit(ctx, "", "system", "release_held", r.Version, map[string]any{"reason": "breaks_previous_home"})
	s.notify(ctx, NoticeReleaseHeld, notify.Payload{Severity: notify.SeverityWarn,
		Params:  map[string]any{"version": r.Version},
		Link:    "/admin/live-tv/changes#updates",
		Subject: "release:" + r.Version, Every: notifyOnce})
}

type withdrawnError struct{ reason string }

func (e *withdrawnError) Error() string { return "release_withdrawn: " + e.reason }

// ErrNoTarget: Update now with nothing to install.
var ErrNoTarget = errors.New("no_update")

// BlockedError: a run cannot start now.
type BlockedError struct{ Reason string }

func (e *BlockedError) Error() string { return "update_blocked: " + e.Reason }

// ErrAutoInstallNotBuilt: this build has no update orchestrator (story 35.4), so
// nothing may plan a run. Unlike ErrOrchestratorUnavailable (a build that should
// install but whose supervisor cannot), it records no run and sends no notice.
var ErrAutoInstallNotBuilt = errors.New("auto_install_not_built")

// start re-checks the release on GitHub, inserts the run and hands it to the
// supervisor. A supervisor without an orchestrator fails the run (recorded, audited).
// A build without automatic installs refuses before anything is recorded.
func (s *Service) start(ctx context.Context, r Release, pol Policy, trigger, actor string) (Run, error) {
	if !AutoInstallAvailable() {
		return Run{}, ErrAutoInstallNotBuilt
	}
	if s.Discoverer != nil {
		reason, err := s.Discoverer.Recheck(ctx, r, pol.Channel)
		if err != nil {
			return Run{}, fmt.Errorf("re-check the release: %w", err)
		}
		if reason != "" {
			return Run{}, &withdrawnError{reason: reason}
		}
	}
	// the release can start: a run that only waits for desktop apps gives way to it
	s.supersede(ctx, r.Version)
	run, err := s.Store.InsertRun(ctx, r.Version, NormalizeVersion(s.Current), trigger, actor)
	if err != nil {
		return run, err
	}
	src := "system"
	if actor != "" {
		src = "admin"
	}
	s.Store.Audit(ctx, actor, src, "run_planned", r.Version, map[string]any{"run_id": run.ID, "trigger": trigger})
	starter := s.Starter
	if starter == nil {
		starter = noOrchestrator{}
	}
	if err := starter.Start(ctx, run.ID); err != nil {
		code := "start_failed"
		if errors.Is(err, ErrOrchestratorUnavailable) {
			code = ErrOrchestratorUnavailable.Error()
		}
		noteStore(s.log(), "FinishRun", s.Store.FinishRun(ctx, run.ID, "failed", map[string]any{"code": code, "detail": err.Error()}), "run_id", run.ID)
		s.Store.Audit(ctx, actor, src, "run_failed", r.Version, map[string]any{"run_id": run.ID, "code": code})
		s.notify(ctx, NoticeRunFailed, notify.Payload{Severity: notify.SeverityError,
			Params:  map[string]any{"version": r.Version, "code": code},
			Link:    "/admin/updates",
			Subject: "run:" + run.ID, Every: notifyOnce})
		return run, err
	}
	return run, nil
}

// superseder is a Starter that ends a run waiting only for desktop apps to stage their
// orders when a newer release is due (the Driver).
type superseder interface {
	Supersedable(ctx context.Context, version string) bool
	Supersede(ctx context.Context, version string) (bool, error)
}

// supersede lets a newer release take the place of a run that only waits for desktop apps
// to stage their orders (nothing waits for ever on an app that never stages one).
func (s *Service) supersede(ctx context.Context, version string) {
	sp, ok := s.Starter.(superseder)
	if !ok {
		return
	}
	if done, err := sp.Supersede(ctx, version); err != nil {
		s.log().Warn("update run not superseded", "event", "update_supersede_failed", "version", version, "err", err.Error())
	} else if done {
		s.log().Info("update run superseded", "event", "update_superseded", "version", version)
	}
}

// noOrchestrator is the Starter of an API that runs under no supervisor that updates.
type noOrchestrator struct{}

func (noOrchestrator) Start(context.Context, string) error { return ErrOrchestratorUnavailable }

// RunNow is "Update now" (story 35.3 AC7): the same run at once, ignoring only the
// window and the release age, never the checks. version "" is the planned target; a
// held contract release is approved by it.
func (s *Service) RunNow(ctx context.Context, actor, version string) (Run, error) {
	if !AutoInstallAvailable() {
		return Run{}, ErrAutoInstallNotBuilt
	}
	pol, err := s.Policy(ctx)
	if err != nil {
		return Run{}, err
	}
	cat, err := s.Store.Catalog(ctx)
	if err != nil {
		return Run{}, err
	}
	wm, err := s.Store.Watermark(ctx)
	if err != nil {
		return Run{}, err
	}
	var target *Release
	if v := NormalizeVersion(version); v != "" {
		// an explicit version: the admin's choice overrides skip, pin and a block (a
		// retry of a release that failed or rolled back), not the checks: a withdrawn or
		// unverified release stays refused
		if unblocked, err := s.unblock(ctx, actor, v); err != nil {
			return Run{}, err
		} else if unblocked {
			if cat, err = s.Store.Catalog(ctx); err != nil {
				return Run{}, err
			}
		}
		p := pol
		p.SkippedVersions, p.PinnedVersion = nil, ""
		var only []Release
		for _, r := range cat {
			if CompareVersions(r.Version, v) == 0 {
				only = append(only, r)
			}
		}
		target = ChooseTarget(only, p, s.Current, wm).Target
	} else {
		target = ChooseTarget(cat, pol, s.Current, wm).Target
	}
	if target == nil {
		return Run{}, ErrNoTarget
	}
	why, err := s.blockedFor(ctx, target.Version)
	if err != nil {
		return Run{}, err
	}
	// a recent crash is the running release's: it holds an automatic run (the new one
	// would be judged on a home already failing), not an admin's, whose update may be
	// the cure; the new release is still judged on its own health and rolled back
	if why != "" && why != BlockRecentCrash {
		return Run{}, &BlockedError{Reason: why}
	}
	if why == BlockRecentCrash {
		s.Store.Audit(ctx, actor, "admin", "update_past_recent_crash", target.Version, nil)
	}
	if target.BreaksPreviousHome() && target.ApprovedAt == nil {
		if err := s.Store.Approve(ctx, target.Version, actor); err != nil {
			return Run{}, err
		}
		s.Store.Audit(ctx, actor, "admin", "release_approved", target.Version, map[string]any{"reason": "breaks_previous_home"})
	}
	return s.start(ctx, *target, pol, TriggerManual, actor)
}

// Unblock is an admin's retry of a blocked release without installing it now: its block
// and its counted attempts are cleared, and the next window installs it (a withdrawn or
// unverified release is not unblocked). It reports whether there was a block to clear.
func (s *Service) Unblock(ctx context.Context, actor, version string) (bool, error) {
	return s.unblock(ctx, actor, NormalizeVersion(version))
}

func (s *Service) unblock(ctx context.Context, actor, version string) (bool, error) {
	rel, err := s.Store.Get(ctx, version)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if rel.BlockedReason == "" && rel.Attempts == 0 {
		return false, nil
	}
	if rel.WithdrawnAt != nil || rel.VerifyState != VerifyVerified {
		return false, nil
	}
	if err := s.Store.Unblock(ctx, version); err != nil {
		return false, err
	}
	s.Store.Audit(ctx, actor, "admin", "release_unblocked", version, map[string]any{"reason": rel.BlockedReason,
		"attempts": rel.Attempts})
	return true, nil
}

// RunScheduler ticks every minute until ctx ends.
func (s *Service) RunScheduler(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if d, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("update scheduler tick failed", "event", "update_scheduler_error", "err", err.Error(),
				"action", d.Action)
		} else if d.Action == "started" {
			s.log().Info("update run started", "event", "update_run_started", "version", d.Version, "run_id", d.RunID)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
