package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Notification kinds of a run (story 35.13 AC8; the web says them as notify.<kind>).
const (
	NoticeInstalledHome   = "update.installed_home"
	NoticeRolloutComplete = "update.rollout_complete"
	NoticeRolledBack      = "update.rolled_back"
	NoticeNodeFailed      = "update.node_failed"
	NoticeNeedsPerson     = "update.needs_person"
	// NoticeWaitingClick: a desktop app that runs the server has the release ready and
	// installs it once someone clicks there (owner decision D1).
	NoticeWaitingClick = "update.waiting_click"
)

// Driver runs the cluster's update runs on the home (stories 35.4, 35.12;
// install-kinds.md): the home's own supervisor prepares the release, the gates (the
// window, the TV) and the drain are kept here, the supervisor applies it, the release
// that comes up commits or rolls back; then every joined machine prepares in parallel
// and applies one at a time over the cluster channel, each checked before the next.
// A run's whole state is in update_runs / update_targets: an API that restarts mid-run
// (an update restarts it) takes it up again on its first tick.
type Driver struct {
	Store   *Store
	Svc     *Service
	Home    HomeSupervisor
	Cluster Cluster
	// Viewers is how many watch the TV now (nil: the channel_runtime count).
	Viewers func(ctx context.Context) int
	// QuietMaxViewers is tv_staging.quiet_max_viewers (nil: 0).
	QuietMaxViewers func(ctx context.Context) int
	Log             *slog.Logger
	Now             func() time.Time
	// Tick is how often the run is looked at (3 s).
	Tick time.Duration
	// Getenv reads the watch overrides (tests; nil: os.Getenv).
	Getenv func(string) string
	// Recheck re-reads a release from GitHub and marks it withdrawn when it is (nil: the
	// Service's Discoverer's Recheck, none without one).
	Recheck func(ctx context.Context, r Release, channel string) (string, error)
	// Events receives each update event (story 35.15 AC2: the hub's events file; nil:
	// the structured log only).
	Events func(fields map[string]any)
	// Mirror is the home's copy of the releases its machines install (story 35.12 AC3; nil:
	// the machines download from GitHub and PyPI themselves).
	Mirror *Mirror

	obs       observer // what the last look saw (events.go)
	mu        sync.Mutex
	wake      chan struct{}
	stepMu    sync.Mutex
	pending   map[string]time.Time // catch-ups recently tried, by node
	rechecked map[string]time.Time // releases last re-read from GitHub, by version
	lateLook  time.Time            // when reconcileHome last asked the home

	// prefetched: the runs whose release the mirror was asked to fetch
	prefetched map[string]bool
}

// Withdrawn releases are noticed while a run goes on (review A-W: a release deleted
// upstream while the home waits days for its click): the run's release is re-read every
// recheckEvery, and right before each go and each catch-up (at most every recheckSoon).
const (
	recheckEvery = time.Hour
	recheckSoon  = 5 * time.Minute
)

// withdrawn re-reads version's release when it was not re-read for recheckEvery (soon:
// recheckSoon) and reports whether it is withdrawn. GitHub out of reach is no
// withdrawal: the run goes on, and the next look asks again.
func (d *Driver) withdrawn(ctx context.Context, version string, soon bool) bool {
	rel, err := d.Store.Get(ctx, version)
	if err != nil {
		return false
	}
	if rel.WithdrawnAt != nil {
		return true
	}
	recheck := d.Recheck
	if recheck == nil && d.Svc != nil && d.Svc.Discoverer != nil {
		recheck = d.Svc.Discoverer.Recheck
	}
	if recheck == nil {
		return false
	}
	every := recheckEvery
	if soon {
		every = recheckSoon
	}
	d.mu.Lock()
	if last, ok := d.rechecked[version]; ok && d.now().Sub(last) < every {
		d.mu.Unlock()
		return false
	}
	if d.rechecked == nil {
		d.rechecked = map[string]time.Time{}
	}
	d.rechecked[version] = d.now()
	d.mu.Unlock()
	reason, err := recheck(ctx, rel, d.policy(ctx).Channel)
	return err == nil && reason != ""
}

func (d *Driver) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d *Driver) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.Default()
}

func (d *Driver) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

// Start implements Starter: the run is the driver's from its next tick (at once). A
// build whose supervisor has no update endpoints refuses here, so Update now says so.
func (d *Driver) Start(ctx context.Context, _ string) error {
	if d.Home == nil {
		return ErrOrchestratorUnavailable
	}
	if _, err := d.Home.Do(ctx, "status", nil); errors.Is(err, ErrOrchestratorUnavailable) {
		return err
	}
	d.Wake()
	return nil
}

// Wake makes the driver look at its run now (a run was planned, an admin acted).
func (d *Driver) Wake() {
	d.mu.Lock()
	if d.wake == nil {
		d.wake = make(chan struct{}, 1)
	}
	ch := d.wake
	d.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (d *Driver) wakeCh() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.wake == nil {
		d.wake = make(chan struct{}, 1)
	}
	return d.wake
}

// Run ticks until ctx ends.
func (d *Driver) Run(ctx context.Context) {
	tick := d.Tick
	if tick <= 0 {
		tick = 3 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		d.observe(ctx)
		if err := d.Step(ctx); err != nil && ctx.Err() == nil {
			d.log().Warn("update driver step failed", "event", "update_driver_error", "err", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.wakeCh():
		}
	}
}

// Step moves the active run one step on (or starts a catch-up when none runs).
func (d *Driver) Step(ctx context.Context) error {
	d.stepMu.Lock()
	defer d.stepMu.Unlock()
	run, err := d.Store.ActiveRun(ctx)
	if err != nil {
		return err
	}
	if run == nil {
		d.reconcileHome(ctx)
		return d.catchUp(ctx)
	}
	return d.advance(ctx, *run)
}

// reconcileHome takes up a home that committed after its run gave up on it (home_timeout:
// a watch that ended past homeCeiling): the watermark is raised to its release, so the
// machines catch up to it (review A-N2). It asks the home at most once a minute, while the
// last run is such a run and the watermark is below its release.
func (d *Driver) reconcileHome(ctx context.Context) {
	if d.Home == nil || d.now().Sub(d.lateLook) < time.Minute {
		return
	}
	runs, err := d.Store.Runs(ctx, 1)
	if err != nil || len(runs) == 0 {
		return
	}
	run := runs[0]
	var doc struct {
		Code string `json:"code"`
	}
	if run.State != RunFailed || json.Unmarshal(run.Error, &doc) != nil || doc.Code != "home_timeout" {
		return
	}
	if wm, err := d.Store.Watermark(ctx); err != nil || (wm != "" && CompareVersions(wm, run.Version) >= 0) {
		return
	}
	d.lateLook = d.now()
	st, err := d.Home.Do(ctx, "status", nil)
	if err != nil || st == nil || st.RunID() != run.ID || st.State != MCommitted {
		return
	}
	if err := d.Store.RaiseWatermark(ctx, run.Version); err != nil {
		return
	}
	d.audit(ctx, run, "home_committed_late", nil)
	d.notify(ctx, NoticeInstalledHome, notify.SeverityInfo, run, nil)
	d.log().Info("the home committed after its run gave up: the machines catch up", "event", "update_home_committed_late",
		"run_id", run.ID, "version", run.Version)
}

func (d *Driver) policy(ctx context.Context) Policy {
	p, err := d.Svc.Policy(ctx)
	if err != nil {
		return DefaultPolicy()
	}
	return p
}

func (d *Driver) notify(ctx context.Context, kind, severity string, run Run, params map[string]any) {
	d.notifyAbout(ctx, kind, severity, run, "run:"+run.ID, params)
}

// notifyAbout is notify of one subject (once per subject: a machine of the run).
func (d *Driver) notifyAbout(ctx context.Context, kind, severity string, run Run, subject string, params map[string]any) {
	if params == nil {
		params = map[string]any{}
	}
	params["version"] = run.Version
	d.Svc.notify(ctx, kind, notify.Payload{Severity: severity, Params: params, Link: "/admin/updates",
		Subject: subject, Every: notifyOnce})
}

func (d *Driver) audit(ctx context.Context, run Run, action string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["run_id"] = run.ID
	d.Store.Audit(ctx, run.ActorUserID, "system", action, run.Version, payload)
}

// job is the machine job of a run's release.
func (d *Driver) job(ctx context.Context, run Run, home bool) (MachineJob, error) {
	rel, err := d.Store.Get(ctx, run.Version)
	if err != nil {
		return MachineJob{}, fmt.Errorf("the release %s is not in the catalog: %w", run.Version, err)
	}
	assets := map[string]string{}
	for _, a := range rel.Assets {
		assets[a.Name] = a.URL
	}
	pol := d.policy(ctx)
	j := MachineJob{RunID: run.ID, Version: NormalizeVersion(run.Version), Tag: rel.Tag, Assets: assets, Home: home,
		Rehearse: home && pol.RehearseMigrations, RehearseMaxGB: pol.RehearseMaxDBGB, KeepVersions: pol.KeepVersions,
		BackupsKeep: pol.BackupsKeep}
	if j.Tag == "" {
		j.Tag = "v" + j.Version
	}
	if rel.Manifest != nil {
		j.AppPostgresMajor = rel.Manifest.AppPostgresMajor
	}
	// the watch's length (tests and the end-to-end run shorten it)
	key := "MAKTABA_UPDATE_NODE_WATCH_SECONDS"
	if home {
		key = "MAKTABA_UPDATE_WATCH_SECONDS"
	}
	if n, err := strconv.Atoi(strings.TrimSpace(d.getenv(key))); err == nil && n > 0 {
		j.WatchSeconds = n
	}
	if n, err := strconv.Atoi(strings.TrimSpace(d.getenv("MAKTABA_UPDATE_HEALTH_SECONDS"))); err == nil && n > 0 {
		j.HealthSeconds = n
	}
	return j, nil
}

// prefetch asks the mirror, once per run, to fetch the run's release for the platforms the
// cluster's machines run: they download it from the home as they prepare.
func (d *Driver) prefetch(ctx context.Context, run Run) {
	if d.Mirror == nil {
		return
	}
	d.mu.Lock()
	if d.prefetched == nil {
		d.prefetched = map[string]bool{}
	}
	done := d.prefetched[run.ID]
	d.prefetched[run.ID] = true
	d.mu.Unlock()
	if !done {
		d.Mirror.Prefetch(ctx, run.Version, d.platforms(ctx))
	}
}

// platforms are the platforms the cluster's approved machines run (their rows, and what
// their agents say).
func (d *Driver) platforms(ctx context.Context) []string {
	nodes, _ := d.nodes(ctx)
	_, states := d.agentStates()
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		// an agent says "linux-amd64"; release.json and the mirror name it "linux/amd64"
		if p = strings.TrimSpace(p); !strings.Contains(p, "/") {
			p = strings.Replace(p, "-", "/", 1)
		}
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, n := range nodes {
		add(n.platform)
	}
	for _, s := range states {
		add(s.Platform)
	}
	sort.Strings(out)
	return out
}

// finish ends a run in state with an error document; the machines' drain is put back.
func (d *Driver) finish(ctx context.Context, run Run, det RunDetail, state string, errDoc map[string]any) error {
	d.undrain(ctx, run, &det)
	d.releaseNodeDrains(ctx, run, &det)
	// the machines' orders still waiting for a click are withdrawn (their apps forget them)
	if targets, err := d.Store.Targets(ctx, run.ID); err == nil {
		for _, t := range targets {
			if t.State == TargetWaitingClick || t.State == TargetClicked {
				d.withdraw(ctx, run, t)
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetSkipped,
					Detail: map[string]any{"why": state}})
			}
		}
	}
	if err := d.Store.FinishRun(ctx, run.ID, state, errDoc); err != nil {
		return err
	}
	d.audit(ctx, run, "run_"+state, errDoc)
	d.log().Info("update run ended", "event", "update_run_ended", "run_id", run.ID, "version", run.Version, "state", state)
	if d.Mirror != nil {
		// the mirror keeps the run's release and the newest keep_versions it holds
		d.Mirror.Prune(keepVersions(d.Mirror.Held(), run.Version, d.policy(ctx).KeepVersions))
		d.mu.Lock()
		delete(d.prefetched, run.ID)
		d.mu.Unlock()
	}
	return nil
}

// advance moves a run on from its state.
func (d *Driver) advance(ctx context.Context, run Run) error {
	_, det, err := d.Store.RunDetailOf(ctx, run.ID)
	if err != nil {
		return err
	}
	d.withdrawn(ctx, run.Version, false) // every recheckEvery: stopWithdrawn sees what it found
	if done, err := d.stopWithdrawn(ctx, run, det); done || err != nil {
		return err
	}
	if run.Step == StepWaitingClick && d.switchoverOpen(ctx) {
		// a switch of the home began while the run only waits for clicks (nothing drained,
		// nothing switching; the switch let it through): the orders are withdrawn and the
		// run ends, and the new home's next run orders them again (review A-N1)
		if run.State == RunHomeApplying && !det.NoHome {
			_ = d.abortHome(ctx, run)
		}
		d.audit(ctx, run, "run_ended_for_switchover", nil)
		return d.finish(ctx, run, det, RunCancelled, map[string]any{"code": "switchover"})
	}
	switch run.State {
	case RunPlanned:
		return d.homePrepare(ctx, run, det)
	case RunHomePreparing:
		return d.homePreparing(ctx, run, det)
	case RunHomePrepared:
		return d.homeGates(ctx, run, det)
	case RunDraining:
		return d.draining(ctx, run, det)
	case RunHomeApplying:
		return d.homeApplying(ctx, run, det)
	case RunHomeCommitted:
		return d.planTargets(ctx, run, det)
	case RunRollingOut:
		return d.rollOut(ctx, run, det)
	}
	return nil
}

// homeCeiling is how long the home may take from its switch to its commit (or its
// rollback) before the run gives up on it: the cluster is undrained and a person told. A
// home whose own limits allow longer (a large database's start steps) gets those and an
// hour more (homeCeilingFor).
const homeCeiling = 6 * time.Hour

// homeCeilingFor is homeCeiling, or the home's own bound (MachineStatus.BoundSec) and an
// hour more when that is longer: the driver never gives up on a home still within it.
func homeCeilingFor(st *MachineStatus) time.Duration {
	c := homeCeiling
	if st != nil && st.BoundSec > 0 {
		c = max(c, time.Duration(st.BoundSec)*time.Second+time.Hour)
	}
	return c
}

// stopWithdrawn ends a run whose release was withdrawn meanwhile (the GitHub release
// gone, turned prerelease, re-published with other files), as far as it can: before the
// home switches nothing is installed; a rollout orders no more machines (the home and a
// machine already switching finish on their own). true when it ended the run.
func (d *Driver) stopWithdrawn(ctx context.Context, run Run, det RunDetail) (bool, error) {
	rel, err := d.Store.Get(ctx, run.Version)
	if err != nil || rel.WithdrawnAt == nil {
		return false, nil
	}
	doc := map[string]any{"code": "withdrawn", "reason": rel.BlockedReason}
	switch run.State {
	case RunPlanned, RunHomePreparing, RunHomePrepared, RunDraining:
		if run.State != RunPlanned && !det.NoHome {
			_ = d.abortHome(ctx, run)
		}
	case RunHomeApplying:
		if det.Home == nil || (det.Home.State != MAwaitingClick && det.Home.State != MClicked) ||
			d.abortHome(ctx, run) != nil {
			return false, nil // past the switch: the home finishes on its own, then this stops
		}
	case RunHomeCommitted, RunRollingOut:
		if targets, err := d.Store.Targets(ctx, run.ID); err == nil {
			for _, t := range targets {
				if !targetEnded(t.State) && t.State != TargetApplying {
					d.withdraw(ctx, run, t)
					_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetSkipped,
						Detail: map[string]any{"why": "withdrawn"}})
				}
			}
		}
	default:
		return false, nil
	}
	d.notify(ctx, NoticeRunFailed, notify.SeverityWarn, run, map[string]any{"code": "withdrawn"})
	return true, d.finish(ctx, run, det, RunCancelled, doc)
}

// switchoverOpen reports whether a switch of the home (Epic 37) is under way: no home
// switches release meanwhile.
func (d *Driver) switchoverOpen(ctx context.Context) bool {
	var open bool
	_ = d.Store.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cluster_switchovers
	    WHERE state NOT IN ('done', 'failed', 'cancelled', 'handed_over'))`).Scan(&open)
	return open
}

// ---------- the home ----------

func (d *Driver) homePrepare(ctx context.Context, run Run, det RunDetail) error {
	if det.NoHome {
		// a rollout of a release the home runs already (catch_up, retry)
		return d.Store.SetRunState(ctx, run.ID, RunHomeCommitted, "")
	}
	job, err := d.job(ctx, run, true)
	if err != nil {
		return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "no_release", "detail": err.Error()})
	}
	st, err := d.Home.Do(ctx, "prepare", job)
	if err != nil {
		if errors.Is(err, ErrOrchestratorUnavailable) {
			d.notify(ctx, NoticeRunFailed, notify.SeverityError, run, map[string]any{"code": "orchestrator_unavailable"})
			return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "orchestrator_unavailable", "detail": err.Error()})
		}
		if strings.Contains(err.Error(), "update_ahead") || strings.Contains(err.Error(), "bad_job") {
			return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "refused", "detail": err.Error()})
		}
		return err // busy, or the supervisor restarting: the next tick asks again
	}
	now := d.now()
	det.HomeSince, det.Home = &now, st
	kind := ""
	if st != nil {
		kind = st.Installer
	}
	if err := d.Store.SetRunDetail(ctx, run.ID, kind, det); err != nil {
		return err
	}
	d.audit(ctx, run, "home_prepare", map[string]any{"install_kind": kind})
	return d.Store.SetRunState(ctx, run.ID, RunHomePreparing, "prepare")
}

// homeStatus reads the home supervisor's run, mirrors its steps, and saves its report.
func (d *Driver) homeStatus(ctx context.Context, run Run, det *RunDetail) (*MachineStatus, error) {
	st, err := d.Home.Do(ctx, "status", nil)
	if err != nil || st == nil {
		return nil, err
	}
	if st.RunID() == run.ID {
		_ = d.Store.ReplaceSteps(ctx, run.ID, "home", stepSeqBase(0, true), st.Steps)
		det.Home = st
		_ = d.Store.SetRunDetail(ctx, run.ID, st.Installer, *det)
	}
	return st, nil
}

func machineErr(st *MachineStatus, fallback string) map[string]any {
	doc := map[string]any{"code": fallback}
	if st != nil && st.Error != nil {
		doc["code"], doc["step"], doc["detail"] = st.Error.Code, st.Error.Step, st.Error.Detail
	}
	return doc
}

func (d *Driver) homePreparing(ctx context.Context, run Run, det RunDetail) error {
	st, err := d.homeStatus(ctx, run, &det)
	if err != nil {
		return err
	}
	if st == nil || st.RunID() != run.ID {
		return d.homePrepare(ctx, run, det) // the supervisor lost it (a new journal): ask again
	}
	switch st.State {
	case MPrepared:
		d.audit(ctx, run, "home_prepared", nil)
		return d.Store.SetRunState(ctx, run.ID, RunHomePrepared, "")
	case MPrepareFailed:
		doc := machineErr(st, "prepare_failed")
		d.blockFor(ctx, run, doc)
		d.notifyFailure(ctx, run, doc)
		return d.finish(ctx, run, det, RunFailed, doc)
	case MAborted:
		return d.finish(ctx, run, det, RunCancelled, nil)
	}
	return nil
}

// blockFor blocks the release when a failure says it must not be tried again by itself
// (its signature or identity refused, its migrations failed on a copy of the database),
// and counts the attempt otherwise, at most once a window for an automatic run (story
// 35.4 AC9: three windows, then blocked); a failure of the machine's install, not of the
// release (no room, no Python, no PyPI, an app built locally: needsPerson) counts
// nothing, so the next window goes once a person mended it. A rehearsal that could not
// run (no room for its dump, its scratch database refused, a restore that failed: the
// machine, not the release) is an attempt like any other.
func (d *Driver) blockFor(ctx context.Context, run Run, doc map[string]any) {
	code, _ := doc["code"].(string)
	switch failureClass(doc) {
	case classOlderBuild:
		// an older build of the running release's line (an rc tagged after its release),
		// which the catalog could not tell (its running release unknown to it): never a
		// candidate here, so set aside quietly (blocked "older_build": no attempt counted,
		// no verification alarm, no notice), and passed by from now on
		_ = d.Store.Block(ctx, run.Version, KindOlderBuild)
		d.audit(ctx, run, "older_build_passed", map[string]any{"detail": doc["detail"]})
	case classPerson:
		// a person mends the machine: no attempt counted
	case classVerify:
		_ = d.Store.Block(ctx, run.Version, "verify")
	case classRehearsal:
		_ = d.Store.Block(ctx, run.Version, "rehearsal")
	default:
		if d.countedInWindow(ctx, run) {
			d.audit(ctx, run, "attempt_counted_already", map[string]any{"code": code})
			return
		}
		if blocked, _ := d.Store.CountAttempt(ctx, run.Version); blocked {
			d.audit(ctx, run, "release_blocked", map[string]any{"reason": "attempts"})
		}
	}
}

// What a failed run's error is to its release (blockFor).
const (
	classOlderBuild = "older_build" // an older build of the running release's line: set aside quietly
	classPerson     = "person"      // the machine's install needs a person: nothing counted
	classVerify     = "verify"      // the release's signature or identity refused: blocked
	classRehearsal  = "rehearsal"   // its migrations failed on a copy of the data: blocked
	classAttempt    = "attempt"     // anything else (a download, a rehearsal that could not run): one attempt
)

// failureClass is what a run's error document is to its release.
func failureClass(doc map[string]any) string {
	code, _ := doc["code"].(string)
	switch {
	case code == KindOlderBuild:
		return classOlderBuild
	case needsPerson(doc):
		return classPerson
	case strings.HasPrefix(code, "verify_"):
		return classVerify
	case code == "rehearsal_failed":
		return classRehearsal
	}
	return classAttempt
}

// countedInWindow reports whether an earlier automatic run of the release failed (and
// counted) in the window occurrence run began in: a window counts one attempt.
func (d *Driver) countedInWindow(ctx context.Context, run Run) bool {
	if run.Trigger != TriggerAuto {
		return false
	}
	w, err := ParseWindow(d.policy(ctx).Window)
	if err != nil {
		return false
	}
	start, _, ok := w.Containing(run.StartedAt, d.Svc.loc())
	if !ok {
		return false
	}
	failed, err := d.Store.FailedAutoRuns(ctx, run.Version, start)
	if err != nil {
		return false
	}
	for _, r := range failed {
		if r.ID == run.ID {
			continue
		}
		if failureClass(runErrDoc(r)) == classAttempt {
			return true
		}
	}
	return false
}

// notifyFailure tells of the home's failed run: a person must mend the machine's install
// (needsPerson: once per release and reason, not once per run: the next window tries
// again), or the release failed (at most hourly per release and reason: a failed run is
// tried again within the window).
func (d *Driver) notifyFailure(ctx context.Context, run Run, doc map[string]any) {
	code, _ := doc["code"].(string)
	if code == KindOlderBuild {
		return // passed by quietly (blockFor audits it): no incident
	}
	params := map[string]any{"machine": "home", "code": code, "version": run.Version}
	if needsPerson(doc) {
		d.Svc.notify(ctx, NoticeNeedsPerson, notify.Payload{Severity: notify.SeverityWarn, Params: params,
			Link: "/admin/updates", Subject: "release:" + run.Version + ":home:" + code, Every: notifyOnce})
		return
	}
	delete(params, "machine")
	d.Svc.notify(ctx, NoticeRunFailed, notify.Payload{Severity: notify.SeverityError, Params: params,
		Link: "/admin/updates", Subject: "release:" + run.Version + ":" + code, Every: time.Hour})
}

// windowDeadline is when an automatic run's apply must have begun: the end of the
// window it started in plus window_overrun_minutes (zero: no deadline, an admin's run).
func (d *Driver) windowDeadline(run Run, pol Policy) time.Time {
	if run.Trigger == TriggerManual || run.Trigger == TriggerRetry {
		return time.Time{}
	}
	w, err := ParseWindow(pol.Window)
	if err != nil {
		return time.Time{}
	}
	loc := d.Svc.loc()
	_, end, ok := w.Containing(run.StartedAt, loc)
	if !ok {
		// a catch-up started outside the window: its own window is the next one
		next := w.NextStart(run.StartedAt, loc)
		if _, e, ok := w.Containing(next, loc); ok {
			end = e
		} else {
			return time.Time{}
		}
	}
	return end.Add(time.Duration(pol.WindowOverrunMinutes) * time.Minute)
}

// homeHandsOver reports whether the home's TV processes run on across its restart (story
// 35.7: the TV handover): its restart cuts no TV, so no TV gate holds it.
func homeHandsOver(det RunDetail) bool { return det.Home != nil && det.Home.HandsOver }

// targetHandsOver reports whether a machine's TV processes run on across its restart, as
// its prepared report says.
func targetHandsOver(t Target) bool {
	var st MachineStatus
	return len(t.Detail) > 0 && json.Unmarshal(t.Detail, &st) == nil && st.HandsOver
}

// handsOverNoMore records that a machine's TV cannot be handed over now (its apply
// refused with tv_busy): its prepared report no longer says it does.
func (d *Driver) handsOverNoMore(ctx context.Context, run Run, t Target) {
	var st MachineStatus
	if len(t.Detail) == 0 || json.Unmarshal(t.Detail, &st) != nil {
		return
	}
	st.HandsOver = false
	_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{Detail: st})
}

// tvBusy reports whether more watch the TV than tv_staging.quiet_max_viewers.
func (d *Driver) tvBusy(ctx context.Context) (bool, int) {
	n := 0
	if d.Viewers != nil {
		n = d.Viewers(ctx)
	} else {
		var v sql.NullInt64
		if err := d.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(viewer_count), 0) FROM channel_runtime`).Scan(&v); err == nil {
			n = int(v.Int64)
		}
	}
	quiet := 0
	if d.QuietMaxViewers != nil {
		quiet = d.QuietMaxViewers(ctx)
	}
	return n > quiet, n
}

// homeGates waits for the window and the TV (an automatic run restarts the home only
// inside its window, with nobody watching beyond tv_staging.quiet_max_viewers: the
// spec's fallback while TV processes are not handed over across a restart), then drains
// the cluster's pipeline workers.
func (d *Driver) homeGates(ctx context.Context, run Run, det RunDetail) error {
	if det.Home != nil && det.Home.Asks {
		// a desktop app (owner decision D1): ordered now, installed when someone clicks
		// there (the click is the gate; the drain comes with it)
		return d.orderHome(ctx, run, det)
	}
	pol := d.policy(ctx)
	deadline := d.windowDeadline(run, pol)
	if !deadline.IsZero() && d.now().After(deadline) {
		code := "outside_window"
		if det.Waiting == "tv_busy" {
			code = "tv_busy"
		}
		return d.gateFailed(ctx, run, det, code)
	}
	if run.Trigger != TriggerManual && !homeHandsOver(det) {
		if busy, n := d.tvBusy(ctx); busy {
			if det.Waiting != "tv_busy" {
				det.Waiting = "tv_busy"
				_ = d.Store.SetRunDetail(ctx, run.ID, "", det)
				d.log().Info("update waits for the TV to be quiet", "event", "update_tv_busy", "viewers", n, "run_id", run.ID)
			}
			return d.Store.SetRunState(ctx, run.ID, RunHomePrepared, "waiting_tv")
		}
	}
	det.Waiting = ""
	d.drain(ctx, run, &det)
	if err := d.Store.SetRunDetail(ctx, run.ID, "", det); err != nil {
		return err
	}
	return d.Store.SetRunState(ctx, run.ID, RunDraining, "drain")
}

// drain stops new jobs on every machine and the home's own workers (the writes a
// rollback that puts the database back would lose stay few); undrain puts back what
// was there before.
func (d *Driver) drain(ctx context.Context, run Run, det *RunDetail) {
	if det.PriorDrain == nil {
		det.PriorDrain = map[string]bool{}
		rows, err := d.Store.DB.QueryContext(ctx, `SELECT id, drain FROM cluster_nodes WHERE state = 'approved'`)
		if err == nil {
			for rows.Next() {
				var id string
				var dr boolScan
				if rows.Scan(&id, &dr) == nil {
					det.PriorDrain[id] = dr.v
				}
			}
			_ = rows.Close()
		}
		// what to put back is saved before the flags change (a crash between the two
		// would leave every machine drained for good)
		_ = d.Store.SetRunDetail(ctx, run.ID, "", *det)
		_, _ = d.Store.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = true WHERE state = 'approved'`)
	}
	if d.Cluster != nil && !det.HomeDrained {
		for _, sid := range d.Cluster.HomeProcesses() {
			_, _, _ = d.Cluster.Command(ctx, sid, "drain", map[string]any{}, 10*time.Second)
		}
	}
	det.HomeDrained = true
	now := d.now()
	det.DrainSince = &now
	d.audit(ctx, run, "drain", map[string]any{"machines": len(det.PriorDrain)})
}

func (d *Driver) undrain(ctx context.Context, run Run, det *RunDetail) {
	if det.PriorDrain == nil && !det.HomeDrained {
		return
	}
	for id, was := range det.PriorDrain {
		if !was {
			_, _ = d.Store.DB.ExecContext(ctx, `UPDATE cluster_nodes SET drain = false WHERE id = $1`, id)
		}
	}
	if d.Cluster != nil && det.HomeDrained {
		for _, sid := range d.Cluster.HomeProcesses() {
			_, _, _ = d.Cluster.Command(ctx, sid, "undrain", map[string]any{}, 10*time.Second)
		}
	}
	det.PriorDrain, det.HomeDrained, det.DrainSince = nil, false, nil
	_ = d.Store.SetRunDetail(ctx, run.ID, "", *det)
}

// runningJobs is how many jobs run now anywhere in the cluster.
func (d *Driver) runningJobs(ctx context.Context) int {
	var n int
	if err := d.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM processing_jobs
		WHERE state IN ('claimed', 'running', 'resuming')`).Scan(&n); err != nil {
		return 0
	}
	return n
}

func (d *Driver) draining(ctx context.Context, run Run, det RunDetail) error {
	pol := d.policy(ctx)
	waited := time.Duration(0)
	if det.DrainSince != nil {
		waited = d.now().Sub(*det.DrainSince)
	}
	if n := d.runningJobs(ctx); n > 0 && waited < time.Duration(pol.DrainTimeoutMinutes)*time.Minute {
		return nil // jobs finish (or checkpoint at the restart once the timeout passes)
	}
	// the gates again, right before the restart: viewers may have come during the drain
	if run.Trigger != TriggerManual && !homeHandsOver(det) {
		if busy, n := d.tvBusy(ctx); busy {
			if deadline := d.windowDeadline(run, pol); !deadline.IsZero() && d.now().After(deadline) {
				return d.gateFailed(ctx, run, det, "tv_busy")
			}
			if det.Waiting != "tv_busy" {
				det.Waiting = "tv_busy"
				_ = d.Store.SetRunDetail(ctx, run.ID, "", det)
				d.log().Info("update waits for the TV to be quiet", "event", "update_tv_busy", "viewers", n, "run_id", run.ID)
			}
			return nil
		}
	}
	if d.switchoverOpen(ctx) {
		if det.Waiting != "switchover_open" {
			det.Waiting = "switchover_open"
			_ = d.Store.SetRunDetail(ctx, run.ID, "", det)
		}
		return nil
	}
	if d.withdrawn(ctx, run.Version, true) {
		return nil // the next step ends the run (stopWithdrawn)
	}
	apply := map[string]any{"run_id": run.ID}
	if deadline := d.windowDeadline(run, pol); !deadline.IsZero() {
		// the new release's TV change waits behind another change no longer than the
		// window (story 35.6 AC4: tv_change_pending, tried again in the next window)
		apply["tv_deadline"] = deadline
	}
	// a home that hands its TV over passed the gate above: it is told whether the TV is
	// watched, and refuses (tv_busy) when it cannot hand over now after all (story 35.7)
	if run.Trigger != TriggerManual && homeHandsOver(det) {
		apply["tv_busy"], _ = d.tvBusy(ctx)
	}
	if _, err := d.Home.Do(ctx, "apply", apply); err != nil {
		if strings.Contains(err.Error(), "update_no_run") || strings.Contains(err.Error(), "update_not_ready") {
			return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "home_not_prepared", "detail": err.Error()})
		}
		if strings.Contains(err.Error(), "tv_busy") {
			// its report says it does not hand over now: the gate waits for a quiet TV
			_, _ = d.homeStatus(ctx, run, &det)
			return nil
		}
		return err
	}
	now := d.now()
	det.Waiting, det.ApplySince = "", &now
	_ = d.Store.SetRunDetail(ctx, run.ID, "", det)
	d.audit(ctx, run, "home_apply", nil)
	return d.Store.SetRunState(ctx, run.ID, RunHomeApplying, "apply")
}

// orderHome orders the release from the home's desktop app: it stages it and shows it,
// and the home's supervisor waits for a click there.
func (d *Driver) orderHome(ctx context.Context, run Run, det RunDetail) error {
	if _, err := d.Home.Do(ctx, "apply", map[string]any{"run_id": run.ID}); err != nil {
		if strings.Contains(err.Error(), "update_no_run") || strings.Contains(err.Error(), "update_not_ready") {
			return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "home_not_prepared", "detail": err.Error()})
		}
		return err
	}
	d.audit(ctx, run, "home_order", nil)
	return d.Store.SetRunState(ctx, run.ID, RunHomeApplying, StepWaitingClick)
}

// homeClicked is the home's click: the cluster drains (as before an automatic apply),
// then the home's supervisor is told to go.
func (d *Driver) homeClicked(ctx context.Context, run Run, det RunDetail) error {
	if !det.HomeDrained {
		d.drain(ctx, run, &det)
		if err := d.Store.SetRunDetail(ctx, run.ID, "", det); err != nil {
			return err
		}
		return d.Store.SetRunState(ctx, run.ID, RunHomeApplying, "drain")
	}
	pol := d.policy(ctx)
	waited := time.Duration(0)
	if det.DrainSince != nil {
		waited = d.now().Sub(*det.DrainSince)
	}
	if n := d.runningJobs(ctx); n > 0 && waited < time.Duration(pol.DrainTimeoutMinutes)*time.Minute {
		return nil
	}
	if d.switchoverOpen(ctx) {
		return nil // the home switches release once the switch of the home is over
	}
	if d.withdrawn(ctx, run.Version, true) {
		return nil // the next step ends the run and takes the order back (stopWithdrawn)
	}
	if _, err := d.Home.Do(ctx, "apply", map[string]any{"run_id": run.ID}); err != nil {
		return err // the next tick asks again
	}
	now := d.now()
	det.ApplySince = &now
	_ = d.Store.SetRunDetail(ctx, run.ID, "", det)
	d.audit(ctx, run, "home_go", nil)
	return d.Store.SetRunState(ctx, run.ID, RunHomeApplying, "apply")
}

func (d *Driver) homeApplying(ctx context.Context, run Run, det RunDetail) error {
	if ceiling := homeCeilingFor(det.Home); det.ApplySince != nil && d.now().Sub(*det.ApplySince) > ceiling {
		// neither committed nor rolled back in hours (a supervisor that does not answer, a
		// watch that never ends): the cluster is undrained and a person told
		d.notify(ctx, NoticeNeedsPerson, notify.SeverityError, run, map[string]any{"code": "home_timeout"})
		return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "home_timeout",
			"detail": fmt.Sprintf("the home did not finish the update within %s of its switch", ceiling)})
	}
	st, err := d.homeStatus(ctx, run, &det)
	if err != nil || st == nil {
		return err // restarting: the next tick asks again
	}
	if st.RunID() != run.ID {
		return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "journal_lost",
			"detail": "the home's supervisor no longer knows this run"})
	}
	switch st.State {
	case MAwaitingClick:
		if run.Step != StepWaitingClick {
			_ = d.Store.SetRunState(ctx, run.ID, RunHomeApplying, StepWaitingClick)
		}
		d.notify(ctx, NoticeWaitingClick, notify.SeverityInfo, run, map[string]any{"machine": "home"})
		return nil
	case MClicked:
		return d.homeClicked(ctx, run, det)
	case MApplying, MRestarting, MWatching, MRollingBack:
		_ = d.Store.SetRunState(ctx, run.ID, RunHomeApplying, st.State)
		return nil
	case MCommitted:
		if st.Backup != nil && st.Backup.Path != "" {
			_ = d.Store.SetRunBackup(ctx, run.ID, map[string]any{"path": st.Backup.Path, "bytes": st.Backup.Bytes})
		}
		return d.homeCommitted(ctx, run, det)
	case MRolledBack:
		doc := machineErr(st, "rolled_back")
		if st.Reason != "" {
			doc["reason"] = st.Reason
		}
		if tvCodes[st.Reason] {
			return d.tvRolledBack(ctx, run, det, st.Reason, doc)
		}
		_ = d.Store.Block(ctx, run.Version, "rolled_back")
		d.notify(ctx, NoticeRolledBack, notify.SeverityError, run, map[string]any{"reason": st.Reason})
		return d.finish(ctx, run, det, RunRolledBack, doc)
	case MApplyFailed:
		doc := machineErr(st, "apply_failed")
		d.blockFor(ctx, run, doc)
		d.notifyFailure(ctx, run, doc)
		return d.finish(ctx, run, det, RunFailed, doc)
	case MFailed:
		doc := machineErr(st, "rollback_failed")
		_ = d.Store.Block(ctx, run.Version, "rolled_back")
		d.notify(ctx, NoticeNeedsPerson, notify.SeverityError, run, map[string]any{"code": doc["code"]})
		return d.finish(ctx, run, det, RunFailedRollback, doc)
	}
	return nil
}

// The TV step's outcomes (story 35.6): the home's new release moves the TV to its
// streaming binary through a TV change before it commits, and rolls back when that fails.
const (
	// CodeFailedTV: the change's verification or swap failed (or it was rolled back):
	// the release is blocked, the admin told with the checks that failed.
	CodeFailedTV = "failed_tv"
	// CodeTVChangePending: queued behind a TV change that waits for an approval until
	// the window's end: a gate, tried again in the next window (no attempt counted).
	CodeTVChangePending = "tv_change_pending"
	// CodeTVTimeout: the change did not reach the air within boundary_max_wait + 10
	// minutes of its stage (an attempt, as any failed run).
	CodeTVTimeout = "tv_timeout"
	// CodeTVChangeCancelled: an admin cancelled the update's TV change: the release is
	// blocked until an admin retries it.
	CodeTVChangeCancelled = "tv_change_cancelled"
)

// tvCodes are the reasons a home rolls back for at its TV step.
var tvCodes = map[string]bool{CodeFailedTV: true, CodeTVChangePending: true, CodeTVTimeout: true,
	CodeTVChangeCancelled: true}

// tvRolledBack ends a run whose home went back to the release before at its TV step.
func (d *Driver) tvRolledBack(ctx context.Context, run Run, det RunDetail, code string, doc map[string]any) error {
	doc["code"] = code
	switch code {
	case CodeTVChangePending:
		return d.gateEnded(ctx, run, det, code,
			"the TV change waited behind a change awaiting approval until the window ended; tried again in the next window")
	case CodeTVTimeout:
		d.blockFor(ctx, run, doc)
		d.notifyFailure(ctx, run, doc)
	case CodeTVChangeCancelled:
		_ = d.Store.Block(ctx, run.Version, "tv_cancelled")
		d.notify(ctx, NoticeRolledBack, notify.SeverityError, run, map[string]any{"reason": code})
	default: // failed_tv
		_ = d.Store.Block(ctx, run.Version, CodeFailedTV)
		d.notify(ctx, NoticeRolledBack, notify.SeverityError, run, map[string]any{"reason": code,
			"detail": doc["detail"]})
	}
	return d.finish(ctx, run, det, RunRolledBack, doc)
}

func (d *Driver) homeCommitted(ctx context.Context, run Run, det RunDetail) error {
	if err := d.Store.RaiseWatermark(ctx, run.Version); err != nil {
		return err
	}
	d.undrain(ctx, run, &det)
	d.audit(ctx, run, "home_committed", nil)
	d.notify(ctx, NoticeInstalledHome, notify.SeverityInfo, run, nil)
	return d.Store.SetRunState(ctx, run.ID, RunHomeCommitted, "")
}

// abortHome asks the home's supervisor to stop a run before its switch.
func (d *Driver) abortHome(ctx context.Context, run Run) error {
	_, err := d.Home.Do(ctx, "abort", map[string]any{"run_id": run.ID})
	return err
}

// ---------- the machines ----------

// nodeRow is a joined machine as the rollout sees it.
type nodeRow struct {
	id, name, platform, version string
	full                        bool
}

func (d *Driver) nodes(ctx context.Context) ([]nodeRow, error) {
	rows, err := d.Store.DB.QueryContext(ctx, `SELECT id, name, COALESCE(platform, ''), COALESCE(version, ''), full_node
		FROM cluster_nodes WHERE state = 'approved' ORDER BY name, id`)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cluster_nodes") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var out []nodeRow
	for rows.Next() {
		var n nodeRow
		var full boolScan
		if err := rows.Scan(&n.id, &n.name, &n.platform, &n.version, &full); err != nil {
			return nil, err
		}
		n.full = full.v
		out = append(out, n)
	}
	return out, rows.Err()
}

// agentState is a machine agent's last update.state.
type agentState struct {
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Layout   string `json:"layout"`
	Run      *struct {
		RunID string `json:"run_id"`
		State string `json:"state"`
	} `json:"run"`
	// Attempts are the machine's own update attempts (update-attempts.md)
	Attempts []Attempt `json:"attempts"`
	// HomeDB is the machine's login to the home's database (the home re-makes its
	// access when it cannot)
	HomeDB *HomeDB `json:"home_db"`
}

// HomeDB is a machine's login to the home's database, as its update.state says it.
type HomeDB struct {
	OK      bool       `json:"ok"`
	Code    string     `json:"code,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	At      *time.Time `json:"at,omitempty"`
	Healing bool       `json:"healing,omitempty"`
	// Fails is how many logins in a row failed on the machine
	Fails int `json:"fails,omitempty"`
}

// shown is the machine's word as the page shows it: a failure only once the machine saw
// it twice in a row (one try can meet a home in the middle of a switch), else nil.
func (h *HomeDB) shown() *HomeDB {
	if h == nil || (!h.OK && h.Fails < 2) {
		return nil
	}
	return h
}

func (d *Driver) agentStates() (map[string]string, map[string]agentState) {
	if d.Cluster == nil {
		return map[string]string{}, map[string]agentState{}
	}
	agents := d.Cluster.MachineAgents()
	states := map[string]agentState{}
	for id, raw := range d.Cluster.MachineStates() {
		var s agentState
		if json.Unmarshal(raw, &s) == nil {
			states[id] = s
		}
	}
	return agents, states
}

// planTargets makes the run's targets: every approved machine behind the release (the
// ones a catch-up or retry names), in name order; one with no machine agent is offline
// now (a catch-up takes it when it comes back), a worker-only one is reported, one
// ahead of the home is skipped.
func (d *Driver) planTargets(ctx context.Context, run Run, det RunDetail) error {
	nodes, err := d.nodes(ctx)
	if err != nil {
		return err
	}
	agents, states := d.agentStates()
	workers := d.nodeWorkers()
	only := map[string]bool{}
	for _, id := range det.Targets {
		only[id] = true
	}
	// the machines that keep TV streams on air go last (story 35.6 AC5/AC6)
	streams := d.streamingHosts(ctx)
	sort.SliceStable(nodes, func(i, j int) bool { return !streams[nodes[i].id] && streams[nodes[j].id] })
	ord := 0
	for _, n := range nodes {
		if len(only) > 0 && !only[n.id] {
			continue
		}
		ver, platform, kind := n.version, n.platform, ""
		if s, ok := states[n.id]; ok {
			if s.Version != "" {
				ver = s.Version
			}
			if s.Platform != "" {
				platform = s.Platform
			}
			kind = s.Layout
		}
		_, connected := agents[n.id]
		// a worker-only machine's version is its pipeline's: whatever it is, it converts
		if ver != "" && CompareVersions(ver, run.Version) == 0 && (connected || n.full) {
			continue // already there
		}
		t := Target{RunID: run.ID, NodeID: n.id, Name: n.name, Ord: ord, Platform: platform, Kind: kind,
			FromVersion: NormalizeVersion(ver), ToVersion: NormalizeVersion(run.Version), State: TargetPending}
		later := ver != "" && d.laterBuild(ctx, ver, run.Version)
		w, working := workers[n.id]
		tooOld := false
		switch {
		case !connected && !n.full:
			// a worker-only machine: converted on its turn (update.convert), offline until
			// its worker is back, worker_only while its pipeline is known too old for it
			t.Kind = KindWorker
			switch {
			case !working:
				t.State = TargetOffline
			case d.tooOldKnown(ctx, n.id, run.Version, w):
				t.State, tooOld = TargetWorkerOnly, true
			}
		case ver != "" && CompareVersions(ver, run.Version) > 0, later:
			t.State = TargetSkipped
		case !connected:
			t.State = TargetOffline
		}
		if err := d.Store.InsertTarget(ctx, t); err != nil {
			return err
		}
		if tooOld {
			_ = d.Store.UpdateTarget(ctx, run.ID, n.id, TargetChange{Detail: map[string]any{"why": WhyTooOld,
				"pipeline_version": w.Version}})
		}
		if t.State == TargetSkipped {
			why := "ahead_of_home"
			if later {
				why = KindOlderBuild // a later build of the release's line
			}
			_ = d.Store.UpdateTarget(ctx, run.ID, n.id, TargetChange{Detail: map[string]any{"why": why}})
		}
		ord++
	}
	d.audit(ctx, run, "rollout_planned", map[string]any{"targets": ord})
	if rel, err := d.Store.Get(ctx, run.Version); err == nil {
		d.notePausedMachines(ctx, run, rel)
	}
	return d.Store.SetRunState(ctx, run.ID, RunRollingOut, "")
}

// streamingHosts are the machines that keep TV streams on air now (by node id).
func (d *Driver) streamingHosts(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	rows, err := d.Store.DB.QueryContext(ctx, `SELECT DISTINCT host_id FROM channel_runtime
		WHERE host_id IS NOT NULL AND state <> 'idle'`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// machineTVBusy reports whether more than tv_staging.quiet_max_viewers watch the streams
// a machine keeps on air (its restart would cut them).
func (d *Driver) machineTVBusy(ctx context.Context, nodeID string) bool {
	var n sql.NullInt64
	if err := d.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(viewer_count), 0) FROM channel_runtime
		WHERE host_id = $1`, nodeID).Scan(&n); err != nil {
		return false
	}
	quiet := 0
	if d.QuietMaxViewers != nil {
		quiet = d.QuietMaxViewers(ctx)
	}
	return int(n.Int64) > quiet
}

// mayRestartMachines reports whether the run may restart a machine now: an admin's run
// any time; an automatic one inside its window, or, when the home's desktop app was
// clicked (outside the window maybe), inside any window (the machines then wait for the
// next one rather than being held).
func (d *Driver) mayRestartMachines(run Run, det RunDetail, pol Policy) bool {
	if run.Trigger == TriggerManual || run.Trigger == TriggerRetry {
		return true
	}
	if det.Home != nil && det.Home.Asks {
		w, err := ParseWindow(pol.Window)
		if err != nil {
			return true
		}
		_, _, ok := w.Containing(d.now(), d.Svc.loc())
		return ok
	}
	deadline := d.windowDeadline(run, pol)
	return deadline.IsZero() || !d.now().After(deadline)
}

// adoptBehind takes into a rollout the machines that came back (offline when it began)
// or joined since and run older than its release, as a catch-up would (none while a run
// is open: one that waits for clicks may stay open long).
func (d *Driver) adoptBehind(ctx context.Context, run Run, targets []Target, agents map[string]string, states map[string]agentState) {
	nodes, err := d.nodes(ctx)
	if err != nil {
		return
	}
	have := map[string]Target{}
	for _, t := range targets {
		have[t.NodeID] = t
	}
	ord := len(targets)
	for _, n := range nodes {
		if _, ok := agents[n.id]; !ok || !n.full {
			continue
		}
		ver := n.version
		if s, ok := states[n.id]; ok && s.Version != "" {
			ver = s.Version
		}
		if ver == "" || CompareVersions(ver, run.Version) >= 0 {
			continue
		}
		if t, ok := have[n.id]; ok {
			if t.State == TargetOffline {
				_ = d.Store.UpdateTarget(ctx, run.ID, n.id, TargetChange{State: TargetPending, Error: "clear"})
				d.audit(ctx, run, "node_back", map[string]any{"node_id": n.id})
			}
			continue
		}
		s := states[n.id]
		if d.heldBack(ctx, n.id, run.Version, ver, s.Layout, s.Attempts) {
			continue
		}
		if err := d.Store.InsertTarget(ctx, Target{RunID: run.ID, NodeID: n.id, Name: n.name, Ord: ord,
			Platform: firstNonEmpty(s.Platform, n.platform), Kind: s.Layout, FromVersion: NormalizeVersion(ver),
			ToVersion: NormalizeVersion(run.Version), State: TargetPending}); err == nil {
			ord++
			d.audit(ctx, run, "node_joined_rollout", map[string]any{"node_id": n.id})
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// nodeStatus asks a machine for its run (nil when it does not answer).
func (d *Driver) nodeStatus(ctx context.Context, sid string) *MachineStatus {
	ok, raw, err := d.Cluster.Command(ctx, sid, "update.status", map[string]any{}, 15*time.Second)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var doc struct {
		Status *MachineStatus `json:"status"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	_ = ok
	return doc.Status
}

// prepareConcurrency is how many machines download at once (design §13.3).
const prepareConcurrency = 3

// prepareTimeout is how long a machine may prepare (download, verify, stage) before its
// target fails.
const prepareTimeout = 2 * time.Hour

func (d *Driver) rollOut(ctx context.Context, run Run, det RunDetail) error {
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return err
	}
	d.prefetch(ctx, run)
	pol := d.policy(ctx)
	agents, states := d.agentStates()
	// a run for named machines (a Retry, a Resume, a catch-up) touches only those; the
	// others wait for a catch-up of their own (inside the window)
	workers := d.nodeWorkers()
	if d.mayRestartMachines(run, det, pol) {
		if len(det.Targets) == 0 {
			d.adoptBehind(ctx, run, targets, agents, states)
		}
		d.adoptWorkers(ctx, run, targets, workers)
		targets, _ = d.Store.Targets(ctx, run.ID)
	}
	preparing, applying := 0, -1
	for i, t := range targets {
		switch t.State {
		case TargetPreparing:
			preparing++
		case TargetApplying, TargetConverting:
			applying = i
		}
	}
	// the one applying (or converting) first: one machine at a time (the others prepare
	// meanwhile, and the desktop apps' clicks are noted)
	busy := false
	if applying >= 0 {
		var done bool
		if targets[applying].State == TargetConverting {
			done = d.followConvert(ctx, run, pol, targets[applying], agents, states, workers)
		} else if done, err = d.followApply(ctx, run, pol, targets[applying], agents); err != nil {
			return err
		}
		busy = !done
		if done {
			targets, _ = d.Store.Targets(ctx, run.ID)
		}
	}
	var clicked []Target
	for _, t := range targets {
		// what an earlier target's failure did this tick (a hold) wins over this slice
		if cur := d.Store.TargetState(ctx, run.ID, t.NodeID); cur != "" && cur != t.State {
			continue
		}
		sid, connected := agents[t.NodeID]
		switch t.State {
		case TargetPending:
			if t.Kind == KindWorker {
				// a worker-only machine is converted on its turn (below), never prepared
				if _, ok := workers[t.NodeID]; !ok {
					_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetOffline})
				}
				continue
			}
			if !connected {
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetOffline})
				continue
			}
			if preparing >= prepareConcurrency {
				continue
			}
			if !d.prepareNode(ctx, run, pol, t, sid) {
				continue
			}
			preparing++
		case TargetPreparing:
			if !connected {
				// gone mid-prepare: offline (a catch-up takes it when it is back)
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetOffline})
				continue
			}
			st := d.nodeStatus(ctx, sid)
			if st != nil && st.RunID() != run.ID && st.State != MPreparing {
				// it lost the run (its journal set aside, a start that read none): again
				d.prepareNode(ctx, run, pol, t, sid)
				continue
			}
			if st == nil || st.RunID() != run.ID {
				d.prepareTimedOut(ctx, run, pol, t)
				continue
			}
			_ = d.Store.ReplaceSteps(ctx, run.ID, t.NodeID, stepSeqBase(t.Ord, false), st.Steps)
			switch st.State {
			case MPrepared:
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPrepared, Prepared: true,
					Kind: st.Installer, Detail: st})
				if st.Asks {
					// a desktop app: ordered now; it waits for a click there while the
					// rollout goes on with the others
					d.orderNode(ctx, run, t, sid)
				}
			case MPrepareFailed, MAborted:
				d.targetFailed(ctx, run, pol, t, TargetFailedPrepare, machineErr(st, "prepare_failed"))
			default:
				d.prepareTimedOut(ctx, run, pol, t)
			}
		case TargetPrepared:
			if !connected {
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetOffline})
			}
		case TargetWaitingClick, TargetClicked:
			if !connected {
				// its order waits on its disk; a click it got goes when it is back
				if t.State == TargetClicked {
					_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetWaitingClick})
				}
				continue
			}
			st := d.nodeStatus(ctx, sid)
			if st == nil || st.RunID() != run.ID {
				continue
			}
			_ = d.Store.ReplaceSteps(ctx, run.ID, t.NodeID, stepSeqBase(t.Ord, false), st.Steps)
			switch st.State {
			case MAwaitingClick:
				if t.State != TargetWaitingClick {
					_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetWaitingClick, Detail: st})
				}
			case MClicked:
				if t.State != TargetClicked {
					_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetClicked, Detail: st})
				}
				clicked = append(clicked, t)
			case MApplying, MRestarting, MWatching, MRollingBack:
				// its go was given before this API restarted
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetApplying, Applied: true})
				busy = true
			case MCommitted:
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHealthy, Healthy: true, Detail: st})
				d.nodeUndrain(ctx, run, t.NodeID)
			case MRolledBack:
				d.targetFailed(ctx, run, pol, t, TargetRolledBack, machineErr(st, st.State))
			case MApplyFailed, MAborted, MPrepareFailed, MFailed:
				d.targetFailed(ctx, run, pol, t, TargetFailed, machineErr(st, st.State))
			}
		}
	}
	// no machine goes while one applies, while the home switches, nor for a release found
	// withdrawn (the next step ends the run: stopWithdrawn); the run may still finish
	held := busy || d.switchoverOpen(ctx) || d.withdrawn(ctx, run.Version, true)
	if !held && len(clicked) > 0 && (run.Trigger == TriggerManual || !d.machineTVBusy(ctx, clicked[0].NodeID)) {
		// someone clicked on a machine: its turn comes first (it ignores the window: the
		// person there chose the moment; not the TV it keeps on air for others)
		t := clicked[0]
		if !d.nodeDrained(ctx, run, &det, pol, t) {
			return d.maybeFinishRollout(ctx, run, det) // its jobs finish first; no other machine goes meanwhile
		}
		ok, raw, err := d.Cluster.Command(ctx, agents[t.NodeID], "update.apply", map[string]any{"run_id": run.ID}, 30*time.Second)
		if err != nil || !ok {
			d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": "apply_refused",
				"detail": strings.TrimSpace(fmt.Sprint(err, " ", string(raw)))})
		} else {
			d.audit(ctx, run, "node_go", map[string]any{"node_id": t.NodeID})
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetApplying, Applied: true})
		}
		held = true
	}
	if !held {
		targets, _ = d.Store.Targets(ctx, run.ID)
		for _, t := range targets {
			worker := t.State == TargetPending && t.Kind == KindWorker
			if t.State != TargetPrepared && !worker {
				continue
			}
			if !d.mayRestartMachines(run, det, pol) {
				break // the window is over: the rest wait (held at the end, caught up later)
			}
			sid, connected := agents[t.NodeID]
			if worker {
				var w NodeWorker
				w, connected = workers[t.NodeID]
				sid = w.Session
			}
			if !connected {
				continue
			}
			if run.Trigger != TriggerManual && !targetHandsOver(t) && d.machineTVBusy(ctx, t.NodeID) {
				continue // its TV streams are watched: another machine first, this one later
			}
			if !d.nodeDrained(ctx, run, &det, pol, t) {
				break // its turn: its jobs finish first (or checkpoint once drain_timeout passed)
			}
			if worker {
				d.startConvert(ctx, run, pol, t, workers[t.NodeID])
				break
			}
			busy := run.Trigger != TriggerManual && targetHandsOver(t) && d.machineTVBusy(ctx, t.NodeID)
			ok, raw, err := d.Cluster.Command(ctx, sid, "update.apply", map[string]any{"run_id": run.ID, "tv_busy": busy},
				30*time.Second)
			if (err != nil || !ok) && strings.Contains(fmt.Sprint(err, " ", string(raw)), "tv_busy") {
				// it cannot hand its TV over now after all, and its streams are watched:
				// its report says so, and the gate holds it (another machine first)
				d.handsOverNoMore(ctx, run, t)
				continue
			}
			if err != nil || !ok {
				d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": "apply_refused",
					"detail": strings.TrimSpace(fmt.Sprint(err, " ", string(raw)))})
				continue
			}
			d.audit(ctx, run, "node_apply", map[string]any{"node_id": t.NodeID})
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetApplying, Applied: true})
			break
		}
	}
	return d.maybeFinishRollout(ctx, run, det)
}

// prepareNode orders a machine to prepare the run's release; false when it refused.
func (d *Driver) prepareNode(ctx context.Context, run Run, pol Policy, t Target, sid string) bool {
	job, err := d.job(ctx, run, false)
	if err != nil {
		return false
	}
	ok, raw, err := d.Cluster.Command(ctx, sid, "update.prepare", job, 30*time.Second)
	if err != nil || !ok {
		d.targetFailed(ctx, run, pol, t, TargetFailedPrepare, map[string]any{"code": "prepare_refused",
			"detail": strings.TrimSpace(fmt.Sprint(err, " ", string(raw)))})
		return false
	}
	_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPreparing, Attempt: true, Error: "clear"})
	return true
}

// prepareTimedOut fails a machine that has prepared for longer than prepareTimeout.
func (d *Driver) prepareTimedOut(ctx context.Context, run Run, pol Policy, t Target) {
	if t.UpdatedAt != nil && d.now().Sub(*t.UpdatedAt) > prepareTimeout {
		d.targetFailed(ctx, run, pol, t, TargetFailedPrepare, map[string]any{"code": "prepare_timeout",
			"detail": fmt.Sprintf("not prepared within %s", prepareTimeout)})
	}
}

// orderNode orders the release from a machine's desktop app (it stages it and shows
// it); a machine that does not take the order now is ordered when its turn comes.
func (d *Driver) orderNode(ctx context.Context, run Run, t Target, sid string) {
	ok, _, err := d.Cluster.Command(ctx, sid, "update.apply", map[string]any{"run_id": run.ID}, 30*time.Second)
	if err != nil || !ok {
		return
	}
	d.audit(ctx, run, "node_order", map[string]any{"node_id": t.NodeID})
	_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetWaitingClick})
	d.notifyAbout(ctx, NoticeWaitingClick, notify.SeverityInfo, run, "run:"+run.ID+":"+t.NodeID,
		map[string]any{"machine": t.Name})
}

// withdraw takes back a machine's order still waiting for its click (best effort: a
// machine offline now gives way to the next run's order).
func (d *Driver) withdraw(ctx context.Context, run Run, t Target) {
	if d.Cluster == nil {
		return
	}
	if sid, ok := d.Cluster.MachineAgents()[t.NodeID]; ok {
		_, _, _ = d.Cluster.Command(ctx, sid, "update.abort", map[string]any{"run_id": run.ID}, 15*time.Second)
	}
}

// followApply follows the machine applying; done when its part is over (or it turned out
// to be an order waiting for its click, which frees the turn).
func (d *Driver) followApply(ctx context.Context, run Run, pol Policy, t Target, agents map[string]string) (bool, error) {
	timeout := time.Duration(pol.NodeHealthTimeoutMinutes) * time.Minute
	if n, err := strconv.Atoi(d.getenv("MAKTABA_UPDATE_NODE_TIMEOUT_SECONDS")); err == nil && n > 0 {
		timeout = time.Duration(n) * time.Second
	}
	if sid, ok := agents[t.NodeID]; ok {
		if st := d.nodeStatus(ctx, sid); st != nil && st.RunID() == run.ID {
			_ = d.Store.ReplaceSteps(ctx, run.ID, t.NodeID, stepSeqBase(t.Ord, false), st.Steps)
			if st.Pending != "" && (st.State == MWatching || st.State == MRestarting) {
				// busy with a start step (its Python environment, say): its own watch waits,
				// and so does this timeout
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{Applied: true})
				return false, nil
			}
			switch st.State {
			case MAwaitingClick, MClicked:
				state := TargetWaitingClick
				if st.State == MClicked {
					state = TargetClicked
				}
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: state, Detail: st})
				d.notifyAbout(ctx, NoticeWaitingClick, notify.SeverityInfo, run, "run:"+run.ID+":"+t.NodeID,
					map[string]any{"machine": t.Name})
				return true, nil
			case MCommitted:
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHealthy, Healthy: true, Detail: st})
				d.audit(ctx, run, "node_healthy", map[string]any{"node_id": t.NodeID})
				d.nodeUndrain(ctx, run, t.NodeID)
				return true, nil
			case MRolledBack, MApplyFailed, MFailed:
				state := TargetRolledBack
				if st.State != MRolledBack {
					state = TargetFailed
				}
				d.targetFailed(ctx, run, pol, t, state, machineErr(st, st.State))
				return true, nil
			}
		}
	}
	if t.AppliedAt != nil && d.now().Sub(*t.AppliedAt) > timeout {
		d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": "node_health_timeout",
			"detail": fmt.Sprintf("not healthy within %s", timeout)})
		return true, nil
	}
	return false, nil
}

// targetFailed records a machine's failure and applies node_failure_policy (README
// "Machine failure"): hold_platform holds the others of its platform, halt every other,
// continue none. A held machine's order waiting for its click is withdrawn.
func (d *Driver) targetFailed(ctx context.Context, run Run, pol Policy, t Target, state string, doc map[string]any) {
	if code, _ := doc["code"].(string); code == KindOlderBuild {
		// the machine runs a later build of the release's line (the release is an rc's
		// older build): it is ahead, as one on a higher version is
		_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetSkipped,
			Detail: map[string]any{"why": KindOlderBuild}})
		d.audit(ctx, run, "node_older_build", map[string]any{"node_id": t.NodeID, "error": doc})
		return
	}
	if needsPerson(doc) {
		// nothing another try mends, and nothing about the release: no machine is held
		// for it (node_failure_policy is about releases that fail)
		_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetNeedsPerson, Error: doc})
		d.audit(ctx, run, "node_needs_person", map[string]any{"node_id": t.NodeID, "error": doc})
		code, _ := doc["code"].(string)
		d.notifyAbout(ctx, NoticeNeedsPerson, notify.SeverityWarn, run, "machine:"+t.NodeID+":"+code,
			map[string]any{"machine": t.Name, "code": code})
		return
	}
	_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: state, Error: doc})
	d.audit(ctx, run, "node_"+state, map[string]any{"node_id": t.NodeID, "error": doc})
	d.notify(ctx, NoticeNodeFailed, notify.SeverityWarn, run, map[string]any{"machine": t.Name, "code": doc["code"]})
	if pol.NodeFailurePolicy == FailureContinue {
		return
	}
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return
	}
	for _, o := range targets {
		if o.NodeID == t.NodeID || targetEnded(o.State) || o.State == TargetApplying {
			continue
		}
		if pol.NodeFailurePolicy == FailureHalt || (o.Platform != "" && o.Platform == t.Platform) {
			if o.State == TargetWaitingClick || o.State == TargetClicked {
				d.withdraw(ctx, run, o)
			}
			_ = d.Store.UpdateTarget(ctx, run.ID, o.NodeID, TargetChange{State: TargetHeld,
				Error: map[string]any{"code": "held", "held_by": t.Name, "platform": t.Platform}})
		}
	}
}

// needsPersonCodes are failures of a machine's install, not of the release: an install
// that cannot update itself, an app or launcher too old for it, an app this user cannot
// replace, no room for it, no Python (or not one the release's pins were made for) or no
// PyPI for its pipeline's environment.
// The trust root's codes are this server's (a build too old to check the release), and
// an app built locally (the placeholder updater key: it can take no update) or one macOS
// will not let replace itself (App Management, locked files) waits for a person too.
var needsPersonCodes = map[string]bool{"not_self_managed": true, "old_launcher": true, "no_app": true,
	"no_app_update": true, "launcher_protocol": true, "disk_space": true, "not_writable": true, "no_runtime": true,
	"no_python": true, "venv_network": true, "venv_python_unsupported": true, "needs_admin": true,
	"local_build": true, "app_management": true, "translocated": true, "verify_trust_root_expired": true,
	"no_trust_root": true,
	"trust_root":    true, "python_store_stub": true,
	// macOS refused the server's login item of the new app (not code-signed): its fix is a
	// signed app, or codesign; a release whose bundled Postgres major is not this one's (no
	// upgrade path: its data directory would not open)
	"login_item_unsigned": true, "pg_major_change": true}

func needsPerson(doc map[string]any) bool {
	code, _ := doc["code"].(string)
	if needsPersonCodes[code] {
		return true
	}
	// a machine agent from before automatic updates, or one whose server does not update
	// itself, answers no update.* command ("the machine agent does not run update.prepare",
	// " yet" before agents had command handlers)
	detail, _ := doc["detail"].(string)
	return code == "prepare_refused" && (strings.Contains(detail, "does not run update.") ||
		strings.Contains(detail, "unknown command") || strings.Contains(detail, "not supported") ||
		strings.Contains(detail, "machine_agent_only"))
}

// maybeFinishRollout ends the run once every target's part is over. A run left waiting
// only for clicks on desktop apps stays (step waiting_click): those machines go when
// someone clicks, and a newer release may take the run's place.
func (d *Driver) maybeFinishRollout(ctx context.Context, run Run, det RunDetail) error {
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return err
	}
	pol := d.policy(ctx)
	deadline := d.windowDeadline(run, pol)
	// a run whose home's app was clicked holds nothing at a window's end: its machines
	// wait for the next window
	pastWindow := !deadline.IsZero() && d.now().After(deadline) && (det.Home == nil || !det.Home.Asks)
	failed, held, waiting, busy := 0, 0, 0, false
	for _, t := range targets {
		switch t.State {
		case TargetPrepared:
			if !pastWindow {
				busy = true
				continue
			}
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHeld,
				Error: map[string]any{"code": "window_over"}})
			held++
		case TargetPreparing:
			if !pastWindow {
				busy = true
				continue
			}
			d.withdraw(ctx, run, t)
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHeld,
				Error: map[string]any{"code": "window_over"}})
			held++
		case TargetPending:
			if t.Kind == KindWorker && pastWindow {
				// a worker-only machine whose turn did not come in the window: next window
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHeld,
					Error: map[string]any{"code": "window_over"}})
				held++
				continue
			}
			busy = true
		case TargetApplying, TargetConverting, TargetClicked:
			busy = true
		case TargetWaitingClick:
			waiting++
		case TargetHeld:
			held++
		case TargetFailed, TargetFailedPrepare, TargetRolledBack, TargetNeedsPerson:
			failed++
		}
	}
	step := ""
	if !busy && waiting > 0 {
		step = StepWaitingClick
	}
	if run.Step != step {
		_ = d.Store.SetRunState(ctx, run.ID, RunRollingOut, step)
	}
	if busy || waiting > 0 {
		return nil
	}
	state := RunDone
	switch {
	case held > 0:
		state = RunHeld
	case failed > 0:
		state = RunDoneFailures
	}
	if state == RunDone {
		d.notify(ctx, NoticeRolloutComplete, notify.SeverityInfo, run, map[string]any{"machines": len(targets)})
	}
	return d.finish(ctx, run, det, state, map[string]any{"failed": failed, "held": held})
}

// Supersedable reports whether Supersede would end the active run for version (nothing
// is changed): the scheduler asks first, and supersedes only once the newer release can
// start (its blockers clear, its re-check passed).
func (d *Driver) Supersedable(ctx context.Context, version string) bool {
	run, err := d.Store.ActiveRun(ctx)
	if err != nil || run == nil || run.Step != StepWaitingClick || CompareVersions(version, run.Version) <= 0 {
		return false
	}
	_, det, err := d.Store.RunDetailOf(ctx, run.ID)
	if err != nil {
		return false
	}
	switch run.State {
	case RunHomeApplying:
		return det.Home != nil && det.Home.State == MAwaitingClick
	case RunRollingOut:
		targets, err := d.Store.Targets(ctx, run.ID)
		if err != nil {
			return false
		}
		for _, t := range targets {
			if !targetEnded(t.State) && t.State != TargetWaitingClick {
				return false
			}
		}
		return true
	}
	return false
}

// Supersede ends the active run when all it waits for is clicks on desktop apps (the
// home's not clicked yet, or the machines' still to go) and a newer release is due: the
// orders are withdrawn and the newer release's run orders it in their place. false when
// the run is not such a run (or none is active).
func (d *Driver) Supersede(ctx context.Context, version string) (bool, error) {
	d.stepMu.Lock()
	defer d.stepMu.Unlock()
	run, err := d.Store.ActiveRun(ctx)
	if err != nil || run == nil {
		return false, err
	}
	if run.Step != StepWaitingClick || CompareVersions(version, run.Version) <= 0 {
		return false, nil
	}
	_, det, err := d.Store.RunDetailOf(ctx, run.ID)
	if err != nil {
		return false, err
	}
	switch run.State {
	case RunHomeApplying:
		if det.Home == nil || det.Home.State != MAwaitingClick {
			return false, nil
		}
		if err := d.abortHome(ctx, *run); err != nil {
			return false, nil // clicked (or further) meanwhile: it goes on
		}
	case RunRollingOut:
		targets, err := d.Store.Targets(ctx, run.ID)
		if err != nil {
			return false, err
		}
		for _, t := range targets {
			if !targetEnded(t.State) && t.State != TargetWaitingClick {
				return false, nil
			}
		}
	default:
		return false, nil
	}
	d.audit(ctx, *run, "run_superseded", map[string]any{"by": version})
	return true, d.finish(ctx, *run, det, RunSuperseded, map[string]any{"code": "superseded", "by": version})
}

// ---------- catch-up ----------

// catchUpRetry is how long a machine whose catch-up failed before its switch (a download,
// a refusal) waits before the next one; catchUpPersonRetry how long one that needs a
// person waits, unchanged (its release, its install kind): a day, not a run every few hours
// that can only fail the same way.
const (
	catchUpRetry       = 6 * time.Hour
	catchUpPersonRetry = 24 * time.Hour
)

// catchUp gives a machine behind the watermark (offline during the rollout, or joined
// since) a run of its own: the release the cluster runs, the home's phase skipped
// (story 35.12 AC9).
func (d *Driver) catchUp(ctx context.Context) error {
	if d.Cluster == nil {
		return nil
	}
	pol := d.policy(ctx)
	if !pol.EffectiveAutoInstall() {
		return nil
	}
	wm, err := d.clusterRelease(ctx)
	if err != nil || wm == "" {
		return err
	}
	rel, err := d.Store.Get(ctx, wm)
	if err != nil || rel.VerifyState != VerifyVerified || rel.WithdrawnAt != nil {
		return nil // the home's release is not one the catalog verified (installed by hand), or withdrawn
	}
	// it restarts machines: catch_up "immediate" any time, but a machine that keeps TV
	// streams on air (and every machine with catch_up "window") inside the window only; a
	// machine the release's schema pauses (its local routes answer 503 until it runs the
	// release) at once, always (README, rules.updates.catch_up)
	inWindow := true
	if w, err := ParseWindow(pol.Window); err == nil {
		_, _, inWindow = w.Containing(d.now(), d.Svc.loc())
	}
	immediate := pol.CatchUp != "window"
	streams := map[string]bool{}
	if !inWindow {
		streams = d.streamingHosts(ctx)
	}
	agents, states := d.agentStates()
	nodes, err := d.nodes(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if _, ok := agents[n.id]; !ok {
			continue
		}
		s := states[n.id]
		ver := s.Version
		if ver == "" {
			ver = n.version
		}
		if ver == "" || CompareVersions(ver, wm) >= 0 {
			continue
		}
		if s.Run != nil && (s.Run.State == MPreparing || s.Run.State == MApplying || s.Run.State == MRestarting ||
			s.Run.State == MWatching || s.Run.State == MAwaitingClick || s.Run.State == MClicked) {
			continue // it is busy with a run of its own
		}
		if !inWindow && !d.schemaPaused(ctx, rel, ver) && (!immediate || streams[n.id]) {
			continue // the window, for this one
		}
		// the release failed or was rolled back on it (by itself, or by an admin): it waits
		// for a newer release or an admin's Retry (a part a person mends is tried again once
		// the machine changed, or after catchUpRetry: heldBack)
		if d.heldBack(ctx, n.id, wm, ver, s.Layout, s.Attempts) {
			continue
		}
		d.mu.Lock()
		last, tried := d.pending[n.id]
		d.mu.Unlock()
		if tried && d.now().Sub(last) < catchUpRetry {
			continue
		}
		ids = append(ids, n.id)
	}
	if inWindow {
		// the worker-only machines are made full nodes on the release (story 35.8 AC8):
		// inside the window, as their conversion restarts them
		for _, id := range d.workerCatchUps(ctx, nodes, agents, wm) {
			d.mu.Lock()
			last, tried := d.pending[id]
			d.mu.Unlock()
			if !tried || d.now().Sub(last) >= catchUpRetry {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 || d.withdrawn(ctx, wm, true) {
		return nil
	}
	sort.Strings(ids)
	d.mu.Lock()
	if d.pending == nil {
		d.pending = map[string]time.Time{}
	}
	for _, id := range ids {
		d.pending[id] = d.now()
	}
	d.mu.Unlock()
	run, err := d.Store.InsertRunWith(ctx, wm, wm, TriggerCatchUp, "", RunDetail{NoHome: true, Targets: ids})
	if err != nil {
		if errors.Is(err, ErrRunActive) {
			return nil
		}
		return err
	}
	d.audit(ctx, run, "run_planned", map[string]any{"trigger": TriggerCatchUp, "targets": ids})
	return nil
}

// laterBuild reports whether a machine on installed runs a later build than version, the
// run's release: version is newer but signed before it on its major.minor line
// (releaseverify.OlderBuild), as the catalog knows both.
func (d *Driver) laterBuild(ctx context.Context, installed, version string) bool {
	if CompareVersions(version, installed) <= 0 {
		return false
	}
	rel, err := d.Store.Get(ctx, version)
	if err != nil {
		return false
	}
	cur, err := d.Store.Get(ctx, installed)
	if err != nil {
		return false
	}
	return releaseverify.OlderBuild(rel.Version, rel.signedTime(), cur.Version, cur.signedTime())
}
