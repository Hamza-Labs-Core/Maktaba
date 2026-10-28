package updates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Admin actions' refusals.
var (
	ErrPastSwitch = errors.New("past_switch") // a cancel after the home began switching
	ErrNoSuchRun  = errors.New("no_such_run")
	ErrNotHeld    = errors.New("nothing_held")
	ErrNoMachine  = errors.New("no_such_machine")
	ErrOffline    = errors.New("machine_offline")
)

// Cancel stops a run before the home switches (story 35.4 AC1: before migrate; here
// before the apply, the moment the home's database step may begin; for a home's
// desktop app, before its go), or stops a rollout: the machines not yet applying are
// left as they are (their orders withdrawn), the one applying finishes on its own.
func (d *Driver) Cancel(ctx context.Context, id, actor string) error {
	d.stepMu.Lock()
	defer d.stepMu.Unlock()
	run, err := d.Store.GetRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchRun
	}
	if err != nil {
		return err
	}
	if run.FinishedAt != nil {
		return nil
	}
	_, det, err := d.Store.RunDetailOf(ctx, id)
	if err != nil {
		return err
	}
	switch run.State {
	case RunPlanned, RunHomePreparing, RunHomePrepared, RunDraining:
		if run.State != RunPlanned && !det.NoHome {
			_ = d.abortHome(ctx, run)
		}
	case RunHomeApplying:
		// the home's app stages its order (or waits for its go): the order is withdrawn;
		// once the home switched, the abort is refused
		if err := d.abortHome(ctx, run); err != nil {
			return fmt.Errorf("%w: the run is %s", ErrPastSwitch, run.State)
		}
	case RunHomeCommitted, RunRollingOut:
		if targets, err := d.Store.Targets(ctx, id); err == nil {
			for _, t := range targets {
				if !targetEnded(t.State) && t.State != TargetApplying {
					if t.State == TargetPreparing || t.State == TargetPrepared || targetBeforeGo(t.State) {
						d.withdraw(ctx, run, t) // its app forgets the order
					}
					noteStore(d.log(), "UpdateTarget", d.Store.UpdateTarget(ctx, id, t.NodeID, TargetChange{State: TargetSkipped,
						Detail: map[string]any{"why": "cancelled", "by": actor}}))
				}
			}
		}
	default:
		return fmt.Errorf("%w: the run is %s", ErrPastSwitch, run.State)
	}
	d.Store.Audit(ctx, actor, "admin", "run_cancel", run.Version, map[string]any{"run_id": id})
	return d.finish(ctx, run, det, RunCancelled, map[string]any{"code": "cancelled", "by": actor})
}

// Retry starts a run of an earlier run's release for some of its machines (their
// Retry, or a held rollout's Resume): the home already runs it.
func (d *Driver) Retry(ctx context.Context, id string, nodes []string, actor string) (Run, error) {
	run, err := d.Store.GetRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNoSuchRun
	}
	if err != nil {
		return Run{}, err
	}
	if len(nodes) == 0 {
		return Run{}, ErrNotHeld
	}
	wm, err := d.Store.Watermark(ctx)
	if err != nil {
		return Run{}, err
	}
	if wm == "" || CompareVersions(wm, run.Version) < 0 {
		return Run{}, fmt.Errorf("%w: the home does not run %s", ErrNoTarget, run.Version)
	}
	if rel, err := d.Store.Get(ctx, run.Version); err == nil && rel.WithdrawnAt != nil {
		return Run{}, fmt.Errorf("%w: %s was withdrawn (%s)", ErrNoTarget, run.Version, rel.BlockedReason)
	}
	nr, err := d.Store.InsertRunWith(ctx, run.Version, run.Version, TriggerRetry, actor,
		RunDetail{NoHome: true, Targets: nodes})
	if err != nil {
		return Run{}, err
	}
	d.Store.Audit(ctx, actor, "admin", "run_retry", run.Version, map[string]any{"run_id": nr.ID, "from_run": id,
		"targets": nodes})
	d.Wake()
	return nr, nil
}

// Resume retries a finished run's held machines.
func (d *Driver) Resume(ctx context.Context, id, actor string) (Run, error) {
	targets, err := d.Store.Targets(ctx, id)
	if err != nil {
		return Run{}, err
	}
	var held []string
	for _, t := range targets {
		if t.State == TargetHeld {
			held = append(held, t.NodeID)
		}
	}
	return d.Retry(ctx, id, held, actor)
}

// TargetAction is an admin's Retry, Skip or Roll back of one machine of a run.
func (d *Driver) TargetAction(ctx context.Context, id, node, action, actor string) (any, error) {
	targets, err := d.Store.Targets(ctx, id)
	if err != nil {
		return nil, err
	}
	var t *Target
	for i := range targets {
		if targets[i].NodeID == node {
			t = &targets[i]
		}
	}
	if t == nil {
		return nil, ErrNoMachine
	}
	switch action {
	case "retry":
		return d.Retry(ctx, id, []string{node}, actor)
	case "skip":
		if (!targetEnded(t.State) && t.State != TargetApplying) || t.State == TargetNeedsPerson {
			if targetBeforeGo(t.State) || t.State == TargetPreparing || t.State == TargetPrepared {
				if run, err := d.Store.GetRun(ctx, id); err == nil {
					d.withdraw(ctx, run, *t) // its app forgets the order
				}
			}
			if err := d.Store.UpdateTarget(ctx, id, node, TargetChange{State: TargetSkipped,
				Detail: map[string]any{"why": "skipped", "by": actor}}); err != nil {
				return nil, err
			}
		}
		d.Store.Audit(ctx, actor, "admin", "target_skip", t.ToVersion, map[string]any{"run_id": id, "node_id": node})
		d.Wake()
		return map[string]any{"skipped": node}, nil
	case "rollback":
		if d.Cluster == nil {
			return nil, ErrOffline
		}
		sid, ok := d.Cluster.MachineAgents()[node]
		if !ok {
			return nil, ErrOffline
		}
		okc, raw, err := d.Cluster.Command(ctx, sid, "update.rollback", map[string]any{"run_id": id}, 30*time.Second)
		if err != nil {
			return nil, err
		}
		if !okc {
			return nil, fmt.Errorf("%w: %s", ErrNoTarget, string(raw))
		}
		noteStore(d.log(), "UpdateTarget", d.Store.UpdateTarget(ctx, id, node, TargetChange{State: TargetRolledBack,
			Error: map[string]any{"code": "admin_rollback", "by": actor}}))
		d.Store.Audit(ctx, actor, "admin", "target_rollback", t.ToVersion, map[string]any{"run_id": id, "node_id": node})
		return map[string]any{"rolling_back": node}, nil
	}
	return nil, fmt.Errorf("unknown action %q", action)
}

// Machines are the Updates page's machines table: the home, then every approved machine
// with its version, install kind and update state.
func (d *Driver) Machines(ctx context.Context, home Machine) []Machine {
	home.Attempts = d.homeAttempts(ctx)
	if a, ok := d.Store.lastHomeInstall(ctx); ok {
		home.LastUpdate = lastUpdate(home.Attempts, a)
	} else {
		home.LastUpdate = lastUpdate(home.Attempts)
	}
	out := []Machine{home}
	nodes, err := d.nodes(ctx)
	if err != nil {
		return out
	}
	agents, states := d.agentStates()
	wm, _ := d.Store.Watermark(ctx)
	if home.Version != "" && ValidVersion(home.Version) && (wm == "" || CompareVersions(home.Version, wm) > 0) {
		wm = NormalizeVersion(home.Version) // the home installed by hand: the cluster's release is the home's
	}
	run, _ := d.Store.ActiveRun(ctx)
	var targets map[string]Target
	// inRun: whether the run under way brings a machine (a catch-up or a retry names its
	// machines; any other run, every machine behind its release)
	inRun := func(string) bool { return false }
	if run != nil {
		targets = map[string]Target{}
		if ts, err := d.Store.Targets(ctx, run.ID); err == nil {
			for _, t := range ts {
				targets[t.NodeID] = t
			}
		}
		only := map[string]bool{}
		if _, det, err := d.Store.RunDetailOf(ctx, run.ID); err == nil {
			for _, id := range det.Targets {
				only[id] = true
			}
		}
		inRun = func(id string) bool { return len(only) == 0 || only[id] }
	}
	convert := d.WorkerConvert(ctx)
	// what happens next to a machine behind: the catch-up's own decision (read only)
	var sc *catchUpScope
	scope := func() catchUpScope {
		if sc == nil {
			s := d.scopeFor(ctx, d.policy(ctx), wm)
			sc = &s
		}
		return *sc
	}
	for _, n := range nodes {
		m := Machine{ID: n.id, Name: n.name, Role: "node", Version: NormalizeVersion(n.version), Platform: n.platform,
			Convert: convert[n.id]}
		s, connected := states[n.id]
		if connected {
			if s.Version != "" {
				m.Version = NormalizeVersion(s.Version)
			}
			if s.Platform != "" {
				m.Platform = s.Platform
			}
			m.Kind, m.HomeDB = s.Layout, s.HomeDB.shown()
		}
		_, online := agents[n.id]
		switch {
		case !online && !n.full:
			m.State = "worker_only"
		case !online:
			m.State = "offline"
		case m.Version != "" && wm != "" && CompareVersions(m.Version, wm) > 0:
			m.State = "ahead_of_home"
		case m.Version != "" && wm != "" && CompareVersions(m.Version, wm) < 0:
			m.State = "behind"
		default:
			m.State = "up_to_date"
		}
		if t, ok := targets[n.id]; ok && !targetEnded(t.State) {
			m.State, m.Target = t.State, t.ToVersion
		}
		var runs []Attempt
		if a, ok, err := d.Store.LastTargetAttempt(ctx, n.id); err == nil && ok {
			runs = append(runs, a)
		}
		m.Attempts = mergeAttempts(s.Attempts, runs...)
		if a, ok := d.Store.lastHealthyTarget(ctx, n.id); ok {
			m.LastUpdate = lastUpdate(m.Attempts, a)
		} else {
			m.LastUpdate = lastUpdate(m.Attempts)
		}
		switch {
		case run != nil && m.Target == "" && online && inRun(n.id) && (m.State == "behind" || m.State == "up_to_date") && m.Version != "" &&
			CompareVersions(m.Version, run.Version) < 0:
			// the run under way updates it after the home (its rollout plans every machine
			// behind the release)
			m.Next = &NextStep{Version: NormalizeVersion(run.Version), Reason: NextInRun}
		case run == nil && (m.State == "behind" || m.State == "offline") && m.Version != "" && wm != "" &&
			CompareVersions(m.Version, wm) < 0:
			next := d.catchUpFor(ctx, scope(), n, s, m.Version, online)
			m.Next = &next
		}
		out = append(out, m)
	}
	return out
}

// homeAttempts are the home's last update attempts: its own (its supervisor's status: the
// desktop app's updater, its releases' starts) and its last run.
func (d *Driver) homeAttempts(ctx context.Context) []Attempt {
	var own []Attempt
	if d.Home != nil {
		sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		if st, err := d.Home.Do(sctx, "status", nil); err == nil && st != nil {
			own = st.Attempts
		}
		cancel()
	}
	var runs []Attempt
	if a, ok := d.Store.homeRunAttempt(ctx); ok {
		runs = append(runs, a)
	}
	return mergeAttempts(own, runs...)
}

// ---------- handlers ----------

func (h *Handler) driver(w http.ResponseWriter, r *http.Request) *Driver {
	if h.Driver == nil {
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#orchestrator_unavailable", Title: "service unavailable",
			Status: http.StatusServiceUnavailable, Detail: "this API runs no update driver"}).
			With("code", ErrOrchestratorUnavailable.Error()))
		return nil
	}
	return h.Driver
}

// RunView serves GET /api/cluster/updates/runs/{id}: the run, its steps on the home
// and on each machine, and its machines.
func (h *Handler) RunView(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	v, err := h.Svc.Store.View(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, sql.ErrNoRows) {
		httperror.Write(w, r, httperror.NotFound("update run"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("update run").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, v)
}

// RunLog serves GET /api/cluster/updates/runs/{id}/log: the home's log of the run.
func (h *Handler) RunLog(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	d := h.driver(w, r)
	if d == nil {
		return
	}
	b, err := d.Home.Log(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("update run log"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

// Cancel serves POST /api/cluster/updates/runs/{id}/cancel (before the switch only:
// 409 past_switch).
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	d := h.driver(w, r)
	if d == nil {
		return
	}
	err := d.Cancel(r.Context(), chi.URLParam(r, "id"), p.UserID)
	switch {
	case err == nil:
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"cancelled": true})
	case errors.Is(err, ErrNoSuchRun):
		httperror.Write(w, r, httperror.NotFound("update run"))
	case errors.Is(err, ErrPastSwitch):
		httperror.Write(w, r, httperror.Conflict("about:blank#past_switch", err.Error()).With("code", "past_switch"))
	default:
		httperror.Write(w, r, httperror.Internal("cancel").Wrap(err))
	}
}

// Resume serves POST /api/cluster/updates/runs/{id}/resume (a held rollout's machines
// again).
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	d := h.driver(w, r)
	if d == nil {
		return
	}
	run, err := d.Resume(r.Context(), chi.URLParam(r, "id"), p.UserID)
	h.retryAnswer(w, r, run, err)
}

func (h *Handler) retryAnswer(w http.ResponseWriter, r *http.Request, run Run, err error) {
	switch {
	case err == nil:
		common.WriteJSON(w, r, http.StatusAccepted, map[string]any{"run_id": run.ID, "version": run.Version})
	case errors.Is(err, ErrNoSuchRun), errors.Is(err, ErrNoMachine):
		httperror.Write(w, r, httperror.NotFound("update run"))
	case errors.Is(err, ErrRunActive):
		httperror.Write(w, r, httperror.Conflict("about:blank#run_active", "an update is running").With("code", BlockActiveRun))
	case errors.Is(err, ErrNotHeld):
		httperror.Write(w, r, httperror.Conflict("about:blank#nothing_held", "no machine of this run waits").With("code", "nothing_held"))
	case errors.Is(err, ErrNoTarget):
		httperror.Write(w, r, httperror.Conflict("about:blank#no_update", err.Error()).With("code", "no_update"))
	case errors.Is(err, ErrOffline):
		httperror.Write(w, r, httperror.Conflict("about:blank#machine_offline", "the machine is not connected").With("code", "machine_offline"))
	default:
		httperror.Write(w, r, httperror.Internal("update").Wrap(err))
	}
}

// TargetAction serves POST /api/cluster/updates/runs/{id}/targets/{node}/{action}
// (retry, skip, rollback).
func (h *Handler) TargetAction(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	d := h.driver(w, r)
	if d == nil {
		return
	}
	action := chi.URLParam(r, "action")
	if action != "retry" && action != "skip" && action != "rollback" {
		httperror.Write(w, r, httperror.NotFound("action"))
		return
	}
	out, err := d.TargetAction(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "node"), action, p.UserID)
	if err != nil {
		h.retryAnswer(w, r, Run{}, err)
		return
	}
	if run, ok := out.(Run); ok {
		h.retryAnswer(w, r, run, nil)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}
