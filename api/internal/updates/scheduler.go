package updates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// Blocker reasons (story 35.3 AC5): a due run waits while one holds.
const (
	BlockActiveRun             = "run_active"
	BlockTVChange              = "tv_change_in_flight"
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
)

// Blocker answers whether a run may start now ("" = yes, else the reason).
type Blocker interface {
	Blocked(ctx context.Context) (string, error)
}

// BlockerFunc adapts a function.
type BlockerFunc func(ctx context.Context) (string, error)

// Blocked calls f.
func (f BlockerFunc) Blocked(ctx context.Context) (string, error) { return f(ctx) }

// tvInFlight are the TV change states "staged…swapping" (Epic 33 R9.4): a binary change
// would queue behind them anyway.
var tvInFlight = []string{"staged", "verifying", "ready", "approved", "swapping"}

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

// TVChangeBlocker holds runs while a TV change is in staged…swapping (story 35.3 AC5).
type TVChangeBlocker struct{ DB *sql.DB }

// Blocked implements Blocker. A database without tv_changes is not blocked.
func (b TVChangeBlocker) Blocked(ctx context.Context) (string, error) {
	if b.DB == nil {
		return "", nil
	}
	var n int
	err := b.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tv_changes WHERE state IN ('`+
		strings.Join(tvInFlight, "', '")+`')`).Scan(&n)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "tv_changes") {
			return "", nil
		}
		return "", err
	}
	if n > 0 {
		return BlockTVChange, nil
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

// blocked runs the blockers; the first reason wins.
func (s *Service) blocked(ctx context.Context) (string, error) {
	return s.blockedFor(ctx, "")
}

// blockedFor is blocked for a run of version: an active run that only waits for clicks
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
				Link:    "/settings#updates",
				Subject: "release:" + pl.Target.Version, Every: notifyOnce})
		}
		return Decision{Action: "none", Reason: pl.Waiting}, nil
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
	// the release can start: a run that only waits for clicks gives way to it
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
		_ = s.Store.FinishRun(ctx, run.ID, "failed", map[string]any{"code": code, "detail": err.Error()})
		s.Store.Audit(ctx, actor, src, "run_failed", r.Version, map[string]any{"run_id": run.ID, "code": code})
		s.notify(ctx, NoticeRunFailed, notify.Payload{Severity: notify.SeverityError,
			Params:  map[string]any{"version": r.Version, "code": code},
			Link:    "/settings#updates",
			Subject: "run:" + run.ID, Every: notifyOnce})
		return run, err
	}
	return run, nil
}

// superseder is a Starter that ends a run waiting only for clicks on desktop apps when
// a newer release is due (the Driver).
type superseder interface {
	Supersedable(ctx context.Context, version string) bool
	Supersede(ctx context.Context, version string) (bool, error)
}

// supersede lets a newer release take the place of a run that only waits for clicks
// (owner decision D1: nothing else waits for ever on a machine nobody clicks).
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
		// an explicit version: the admin's choice overrides skip and pin, not the checks
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
	if why != "" {
		return Run{}, &BlockedError{Reason: why}
	}
	if target.BreaksPreviousHome() && target.ApprovedAt == nil {
		if err := s.Store.Approve(ctx, target.Version, actor); err != nil {
			return Run{}, err
		}
		s.Store.Audit(ctx, actor, "admin", "release_approved", target.Version, map[string]any{"reason": "breaks_previous_home"})
	}
	return s.start(ctx, *target, pol, TriggerManual, actor)
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
