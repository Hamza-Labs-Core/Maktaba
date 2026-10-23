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
)

// Notification kinds of a run (story 35.13 AC8; the web says them as notify.<kind>).
const (
	NoticeInstalledHome   = "update.installed_home"
	NoticeRolloutComplete = "update.rollout_complete"
	NoticeRolledBack      = "update.rolled_back"
	NoticeNodeFailed      = "update.node_failed"
	NoticeNeedsPerson     = "update.needs_person"
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

	mu      sync.Mutex
	wake    chan struct{}
	stepMu  sync.Mutex
	pending map[string]time.Time // catch-ups recently tried, by node
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
		return d.catchUp(ctx)
	}
	return d.advance(ctx, *run)
}

func (d *Driver) policy(ctx context.Context) Policy {
	p, err := d.Svc.Policy(ctx)
	if err != nil {
		return DefaultPolicy()
	}
	return p
}

func (d *Driver) notify(ctx context.Context, kind, severity string, run Run, params map[string]any) {
	if params == nil {
		params = map[string]any{}
	}
	params["version"] = run.Version
	d.Svc.notify(ctx, kind, notify.Payload{Severity: severity, Params: params, Link: "/admin/updates",
		Subject: "run:" + run.ID, Every: notifyOnce})
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
		Rehearse: home && pol.RehearseMigrations, RehearseMaxGB: pol.RehearseMaxDBGB, KeepVersions: pol.KeepVersions}
	if j.Tag == "" {
		j.Tag = "v" + j.Version
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

// finish ends a run in state with an error document; the machines' drain is put back.
func (d *Driver) finish(ctx context.Context, run Run, det RunDetail, state string, errDoc map[string]any) error {
	d.undrain(ctx, run, &det)
	if err := d.Store.FinishRun(ctx, run.ID, state, errDoc); err != nil {
		return err
	}
	d.audit(ctx, run, "run_"+state, errDoc)
	d.log().Info("update run ended", "event", "update_run_ended", "run_id", run.ID, "version", run.Version, "state", state)
	return nil
}

// advance moves a run on from its state.
func (d *Driver) advance(ctx context.Context, run Run) error {
	_, det, err := d.Store.RunDetailOf(ctx, run.ID)
	if err != nil {
		return err
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
		d.notify(ctx, NoticeRunFailed, notify.SeverityError, run, map[string]any{"code": doc["code"]})
		return d.finish(ctx, run, det, RunFailed, doc)
	case MAborted:
		return d.finish(ctx, run, det, RunCancelled, nil)
	}
	return nil
}

// blockFor blocks the release when a failure says it must not be tried again by itself
// (a verification or a rehearsal failure), and counts the attempt otherwise.
func (d *Driver) blockFor(ctx context.Context, run Run, doc map[string]any) {
	code, _ := doc["code"].(string)
	switch {
	case strings.HasPrefix(code, "verify_"):
		_ = d.Store.Block(ctx, run.Version, "verify")
	case strings.HasPrefix(code, "rehearsal"):
		_ = d.Store.Block(ctx, run.Version, "rehearsal")
	default:
		if blocked, _ := d.Store.CountAttempt(ctx, run.Version); blocked {
			d.audit(ctx, run, "release_blocked", map[string]any{"reason": "attempts"})
		}
	}
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
	max := 0
	if d.QuietMaxViewers != nil {
		max = d.QuietMaxViewers(ctx)
	}
	return n > max, n
}

// homeGates waits for the window and the TV (an automatic run restarts the home only
// inside its window, with nobody watching beyond tv_staging.quiet_max_viewers: the
// spec's fallback while TV processes are not handed over across a restart), then drains
// the cluster's pipeline workers.
func (d *Driver) homeGates(ctx context.Context, run Run, det RunDetail) error {
	pol := d.policy(ctx)
	deadline := d.windowDeadline(run, pol)
	if !deadline.IsZero() && d.now().After(deadline) {
		code := "outside_window"
		if det.Waiting == "tv_busy" {
			code = "tv_busy"
		}
		_ = d.abortHome(ctx, run)
		d.notify(ctx, NoticeRunFailed, notify.SeverityWarn, run, map[string]any{"code": code})
		return d.finish(ctx, run, det, RunFailed, map[string]any{"code": code,
			"detail": "the window ended before the home could restart; tried again in the next window"})
	}
	if run.Trigger != TriggerManual {
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
	if _, err := d.Home.Do(ctx, "apply", map[string]any{"run_id": run.ID}); err != nil {
		if strings.Contains(err.Error(), "update_no_run") || strings.Contains(err.Error(), "update_not_ready") {
			return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "home_not_prepared", "detail": err.Error()})
		}
		return err
	}
	d.audit(ctx, run, "home_apply", nil)
	return d.Store.SetRunState(ctx, run.ID, RunHomeApplying, "apply")
}

func (d *Driver) homeApplying(ctx context.Context, run Run, det RunDetail) error {
	st, err := d.homeStatus(ctx, run, &det)
	if err != nil || st == nil {
		return err // restarting: the next tick asks again
	}
	if st.RunID() != run.ID {
		return d.finish(ctx, run, det, RunFailed, map[string]any{"code": "journal_lost",
			"detail": "the home's supervisor no longer knows this run"})
	}
	switch st.State {
	case MApplying, MRestarting, MWatching, MRollingBack:
		_ = d.Store.SetRunState(ctx, run.ID, RunHomeApplying, st.State)
		return nil
	case MCommitted:
		return d.homeCommitted(ctx, run, det)
	case MRolledBack:
		doc := machineErr(st, "rolled_back")
		if st.Reason != "" {
			doc["reason"] = st.Reason
		}
		_ = d.Store.Block(ctx, run.Version, "rolled_back")
		d.notify(ctx, NoticeRolledBack, notify.SeverityError, run, map[string]any{"reason": st.Reason})
		return d.finish(ctx, run, det, RunRolledBack, doc)
	case MApplyFailed:
		doc := machineErr(st, "apply_failed")
		d.blockFor(ctx, run, doc)
		d.notify(ctx, NoticeRunFailed, notify.SeverityError, run, map[string]any{"code": doc["code"]})
		return d.finish(ctx, run, det, RunFailed, doc)
	case MFailed:
		doc := machineErr(st, "rollback_failed")
		_ = d.Store.Block(ctx, run.Version, "rolled_back")
		d.notify(ctx, NoticeNeedsPerson, notify.SeverityError, run, map[string]any{"code": doc["code"]})
		return d.finish(ctx, run, det, RunFailedRollback, doc)
	}
	return nil
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
	only := map[string]bool{}
	for _, id := range det.Targets {
		only[id] = true
	}
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
		if ver != "" && CompareVersions(ver, run.Version) == 0 {
			continue // already there
		}
		t := Target{RunID: run.ID, NodeID: n.id, Name: n.name, Ord: ord, Platform: platform, Kind: kind,
			FromVersion: NormalizeVersion(ver), ToVersion: NormalizeVersion(run.Version), State: TargetPending}
		_, connected := agents[n.id]
		switch {
		case ver != "" && CompareVersions(ver, run.Version) > 0:
			t.State = TargetSkipped
		case !connected && !n.full:
			t.State = TargetWorkerOnly
		case !connected:
			t.State = TargetOffline
		}
		if err := d.Store.InsertTarget(ctx, t); err != nil {
			return err
		}
		if t.State == TargetSkipped {
			_ = d.Store.UpdateTarget(ctx, run.ID, n.id, TargetChange{Detail: map[string]any{"why": "ahead_of_home"}})
		}
		ord++
	}
	d.audit(ctx, run, "rollout_planned", map[string]any{"targets": ord})
	return d.Store.SetRunState(ctx, run.ID, RunRollingOut, "")
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

func (d *Driver) rollOut(ctx context.Context, run Run, det RunDetail) error {
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return err
	}
	pol := d.policy(ctx)
	agents, _ := d.agentStates()
	preparing, applying := 0, -1
	for i, t := range targets {
		switch t.State {
		case TargetPreparing:
			preparing++
		case TargetApplying:
			applying = i
		}
	}
	// the one applying first: one machine at a time
	if applying >= 0 {
		t := targets[applying]
		done, err := d.followApply(ctx, run, pol, t, agents)
		if err != nil || !done {
			return err
		}
		targets, _ = d.Store.Targets(ctx, run.ID)
	}
	for _, t := range targets {
		sid, connected := agents[t.NodeID]
		switch t.State {
		case TargetPending:
			if !connected {
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetOffline})
				continue
			}
			if preparing >= prepareConcurrency {
				continue
			}
			job, err := d.job(ctx, run, false)
			if err != nil {
				return err
			}
			ok, raw, err := d.Cluster.Command(ctx, sid, "update.prepare", job, 30*time.Second)
			if err != nil || !ok {
				d.targetFailed(ctx, run, pol, t, TargetFailedPrepare, map[string]any{"code": "prepare_refused",
					"detail": strings.TrimSpace(fmt.Sprint(err, " ", string(raw)))})
				continue
			}
			preparing++
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPreparing, Attempt: true, Error: "clear"})
		case TargetPreparing:
			if !connected {
				continue
			}
			st := d.nodeStatus(ctx, sid)
			if st == nil || st.RunID() != run.ID {
				continue
			}
			_ = d.Store.ReplaceSteps(ctx, run.ID, t.NodeID, stepSeqBase(t.Ord, false), st.Steps)
			switch st.State {
			case MPrepared:
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPrepared, Prepared: true,
					Kind: st.Installer, Detail: st})
			case MPrepareFailed, MAborted:
				d.targetFailed(ctx, run, pol, t, TargetFailedPrepare, machineErr(st, "prepare_failed"))
			}
		}
	}
	if applying < 0 {
		targets, _ = d.Store.Targets(ctx, run.ID)
		for _, t := range targets {
			if t.State != TargetPrepared {
				continue
			}
			deadline := d.windowDeadline(run, pol)
			if !deadline.IsZero() && d.now().After(deadline) {
				break // the window is over: the rest wait (held at the end, caught up later)
			}
			sid, connected := agents[t.NodeID]
			if !connected {
				continue
			}
			ok, raw, err := d.Cluster.Command(ctx, sid, "update.apply", map[string]any{"run_id": run.ID}, 30*time.Second)
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

// followApply follows the machine applying; done when its part is over.
func (d *Driver) followApply(ctx context.Context, run Run, pol Policy, t Target, agents map[string]string) (bool, error) {
	timeout := time.Duration(pol.NodeHealthTimeoutMinutes) * time.Minute
	if n, err := strconv.Atoi(d.getenv("MAKTABA_UPDATE_NODE_TIMEOUT_SECONDS")); err == nil && n > 0 {
		timeout = time.Duration(n) * time.Second
	}
	if sid, ok := agents[t.NodeID]; ok {
		if st := d.nodeStatus(ctx, sid); st != nil && st.RunID() == run.ID {
			_ = d.Store.ReplaceSteps(ctx, run.ID, t.NodeID, stepSeqBase(t.Ord, false), st.Steps)
			switch st.State {
			case MCommitted:
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHealthy, Healthy: true, Detail: st})
				d.audit(ctx, run, "node_healthy", map[string]any{"node_id": t.NodeID})
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
// continue none.
func (d *Driver) targetFailed(ctx context.Context, run Run, pol Policy, t Target, state string, doc map[string]any) {
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
			_ = d.Store.UpdateTarget(ctx, run.ID, o.NodeID, TargetChange{State: TargetHeld,
				Error: map[string]any{"code": "held", "held_by": t.Name, "platform": t.Platform}})
		}
	}
}

// maybeFinishRollout ends the run once every target's part is over.
func (d *Driver) maybeFinishRollout(ctx context.Context, run Run, det RunDetail) error {
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return err
	}
	pol := d.policy(ctx)
	deadline := d.windowDeadline(run, pol)
	pastWindow := !deadline.IsZero() && d.now().After(deadline)
	failed, held := 0, 0
	for _, t := range targets {
		switch t.State {
		case TargetPrepared:
			if !pastWindow {
				return nil
			}
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHeld,
				Error: map[string]any{"code": "window_over"}})
			held++
		case TargetPending, TargetPreparing, TargetApplying:
			return nil
		case TargetHeld:
			held++
		case TargetFailed, TargetFailedPrepare, TargetRolledBack:
			failed++
		}
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

// ---------- catch-up ----------

// catchUpRetry is how long a machine whose catch-up failed waits before the next one.
const catchUpRetry = 6 * time.Hour

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
	wm, err := d.Store.Watermark(ctx)
	if err != nil || wm == "" {
		return err
	}
	if rel, err := d.Store.Get(ctx, wm); err != nil || rel.VerifyState != VerifyVerified {
		return nil // the home's release is not one the catalog verified (installed by hand)
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
			s.Run.State == MWatching) {
			continue // it is busy with a run of its own
		}
		d.mu.Lock()
		last, tried := d.pending[n.id]
		d.mu.Unlock()
		if tried && d.now().Sub(last) < catchUpRetry {
			continue
		}
		ids = append(ids, n.id)
	}
	if len(ids) == 0 {
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
