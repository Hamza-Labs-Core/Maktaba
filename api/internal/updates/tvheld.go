package updates

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/tvchange"
)

// A TV held at the update (the owner's decision, 2026-10-28; install-kinds.md "The TV
// step"): when only the TV change's check (its verification in the staging slot) fails,
// before any swap, the release commits all the same: the new server, web and pipeline stay
// installed, the live TV stays on the streaming software it ran, the change stays a draft
// with its report, the release is not blocked, and the admin is told, with Retry (check it
// again: one that passes swaps as usual) and Drop (withdraw it: the TV stays on the old
// streaming software until a later release) on the Updates page. A swap that fails, or a
// TV that fails on the air after it switched (Epic 33.9), still rolls the release back.
//
//	POST /api/cluster/updates/runs/{id}/tv/retry   check the held change again
//	POST /api/cluster/updates/runs/{id}/tv/drop    withdraw it

// NoticeTVHeld tells the admin the release installed with the TV held.
const NoticeTVHeld = "update.tv_held"

// TV states of a run whose TV was held (RunTV.State), as it is now.
const (
	TVStateHeld     = "held"     // its check failed: Retry or Drop
	TVStateRetrying = "retrying" // checked again (staged, verifying, ready, swapping)
	TVStateLive     = "live"     // a retry passed and swapped: the TV runs the release's software
	TVStateDropped  = "dropped"  // an admin dropped it
	TVStateReplaced = "replaced" // a later release's change replaced it
	TVStateFailed   = "failed"   // a retry's swap failed, or it was rolled back on the air
	TVStateGone     = "gone"     // the change is no longer there (a database put back)
	// TVStatePending: the release committed while its change still waited (an approval,
	// a quiet window, a slow staging); it goes on by itself
	TVStatePending = "pending"
)

// TVChanges is the TV change controller as a run's TV step needs it (tvchange.Controller).
type TVChanges interface {
	AutoUpdate(ctx context.Context, runID string) (tvchange.AutoUpdateStatus, error)
	RetryAutoUpdate(ctx context.Context, runID string, actor *string) (tvchange.AutoUpdateStatus, error)
	// BeginAutoUpdate makes the run's change (a Retry of a TV held with no change behind it)
	BeginAutoUpdate(ctx context.Context, runID, version, sha256 string) (tvchange.AutoUpdateStatus, error)
	DropAutoUpdate(ctx context.Context, runID string, actor *string) (tvchange.AutoUpdateStatus, error)
	ReplaceOlderAutoUpdates(ctx context.Context, runID string) error
}

// RunTV is a run's TV held at its commit (update_runs.detail.tv): what the home said then,
// and (in a view) what it is now.
type RunTV struct {
	// Outcome is the TV step's at the commit: held.
	Outcome  string `json:"outcome"`
	ChangeID string `json:"change_id,omitempty"`
	Number   int64  `json:"number,omitempty"`
	// HeldOn is the streaming software the live TV stayed on.
	HeldOn string `json:"held_on,omitempty"`
	// Failed is the checks that failed.
	Failed []string `json:"failed,omitempty"`
	// State is what the change is now (TVState*; set when the run is read).
	State string `json:"state,omitempty"`
	// Checks are the failed checks with their channel and value (the report's summary;
	// set when the run is read while held).
	Checks []tvchange.Check `json:"checks,omitempty"`
	// StreamingVersion and StreamingSHA256: the binary the change carries, for a TV held
	// with no change behind it (Failed = [no_change]): its Retry makes the change with them.
	StreamingVersion string `json:"streaming_version,omitempty"`
	StreamingSHA256  string `json:"streaming_sha256,omitempty"`
	// Dropped: an admin dropped a TV held with no change behind it (nothing to cancel).
	Dropped bool `json:"dropped,omitempty"`
}

// noChange reports whether the run's TV was held with no change behind it.
func (t *RunTV) noChange() bool { return t.ChangeID == "" && t.Outcome == TVHeld }

// TVHeldNow is the run whose TV is held now (GET /api/cluster/updates: tv_held).
type TVHeldNow struct {
	RunID   string `json:"run_id"`
	Version string `json:"version"`
	RunTV
}

// ErrTVNotHeld: the run's TV is not held (it was not, or it was retried, dropped or
// replaced since).
var ErrTVNotHeld = errors.New("tv_not_held")

// tvState is what a held change is now, from its run's TV change.
func tvState(st tvchange.AutoUpdateStatus) string {
	switch st.Outcome {
	case tvchange.AutoOutcomeHeld:
		return TVStateHeld
	case tvchange.AutoOutcomeQueued, tvchange.AutoOutcomeInProgress:
		return TVStateRetrying
	case tvchange.AutoOutcomeLive:
		return TVStateLive
	case tvchange.AutoOutcomeFailed:
		return TVStateFailed
	case tvchange.AutoOutcomeCancelled:
		if st.EndReason == tvchange.EndReplaced {
			return TVStateReplaced
		}
		return TVStateDropped
	}
	return TVStateGone
}

// fill sets what a held run's TV is now (best effort: a controller that does not answer
// leaves the stored word).
func (t *RunTV) fill(ctx context.Context, tv TVChanges, runID string) {
	if t == nil || tv == nil {
		return
	}
	st, err := tv.AutoUpdate(ctx, runID)
	if err != nil {
		return
	}
	if t.noChange() && st.Outcome == tvchange.AutoOutcomeNone {
		// held with no change behind it: Retry makes it, Drop leaves the TV as it is
		t.State = TVStateHeld
		if t.Dropped {
			t.State = TVStateDropped
		}
		return
	}
	t.State = tvState(st)
	if t.Outcome == TVPending && t.State == TVStateRetrying {
		t.State = TVStatePending // still on its first way to the air, not a retry
	}
	if t.State == TVStateHeld {
		t.Failed, t.Checks = st.Failed, st.FailedChecks
		if st.LiveStreaming != "" {
			t.HeldOn = st.LiveStreaming
		}
	}
}

// tvHeldAtCommit records a TV held at the home's commit: the run's detail, the admin's
// notice (Retry and Drop are on the Updates page), the audit. The release is not blocked.
func (d *Driver) tvHeldAtCommit(ctx context.Context, run Run, det *RunDetail, st *MachineStatus) {
	if st != nil && st.TV != nil && st.TV.Outcome == TVPending && det.TV == nil {
		// committed while its change still waited: the run says so (an admin may drop the
		// change from the Updates page); nothing is held, nobody is warned
		det.TV = &RunTV{Outcome: TVPending, ChangeID: st.TV.ChangeID, Number: st.TV.Number, HeldOn: st.TV.LiveStreaming,
			State: TVStatePending}
		noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, run.ID, "", *det), "run_id", run.ID)
		d.audit(ctx, run, "tv_pending", map[string]any{"change_id": st.TV.ChangeID, "live_streaming": st.TV.LiveStreaming})
		d.log().Info("update installed with its TV change still waiting", "event", "update_tv_pending",
			"run_id", run.ID, "version", run.Version, "change_id", st.TV.ChangeID)
		return
	}
	if st == nil || st.TV == nil || st.TV.Outcome != TVHeld || det.TV != nil {
		return
	}
	det.TV = &RunTV{Outcome: TVHeld, ChangeID: st.TV.ChangeID, Number: st.TV.Number,
		HeldOn: st.TV.LiveStreaming, Failed: st.TV.Failed,
		StreamingVersion: st.TV.StreamingVersion, StreamingSHA256: st.TV.StreamingSHA256}
	noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, run.ID, "", *det), "run_id", run.ID)
	d.audit(ctx, run, "tv_held", map[string]any{"change_id": st.TV.ChangeID, "held_on": st.TV.LiveStreaming,
		"failed": st.TV.Failed})
	held := st.TV.LiveStreaming
	if held == "" {
		held = "-"
	}
	d.notify(ctx, NoticeTVHeld, notify.SeverityWarn, run, map[string]any{"held_on": held,
		"checks": strings.Join(st.TV.Failed, ", "), "change": st.TV.Number})
	d.log().Warn("update installed with the TV held", "event", "update_tv_held", "run_id", run.ID,
		"version", run.Version, "held_on", st.TV.LiveStreaming, "change_id", st.TV.ChangeID)
}

// TVAction retries or drops a run's held TV change (admins only: the handler checks).
func (d *Driver) TVAction(ctx context.Context, runID, action, actor string) (RunTV, error) {
	if d.TV == nil {
		return RunTV{}, ErrOrchestratorUnavailable
	}
	run, err := d.Store.GetRun(ctx, runID)
	if err != nil {
		return RunTV{}, ErrNoSuchRun
	}
	_, det, err := d.Store.RunDetailOf(ctx, runID)
	if err != nil {
		return RunTV{}, err
	}
	if det.TV == nil {
		return RunTV{}, ErrTVNotHeld
	}
	var who *string
	if actor != "" {
		who = &actor
	}
	var st tvchange.AutoUpdateStatus
	switch action {
	case "retry":
		// a run under way moves the TV itself (and replaces this change once it commits)
		if active, err := d.Store.ActiveRun(ctx); err != nil {
			return RunTV{}, err
		} else if active != nil {
			return RunTV{}, ErrRunActive
		}
		if det.TV.noChange() {
			// held with no change behind it (it could not be had at the update): Retry makes
			// it now, with the binary the home said; it is staged and checked as any
			if det.TV.Dropped || det.TV.StreamingVersion == "" || det.TV.StreamingSHA256 == "" {
				return RunTV{}, ErrTVNotHeld
			}
			st, err = d.TV.BeginAutoUpdate(ctx, runID, det.TV.StreamingVersion, det.TV.StreamingSHA256)
			if err == nil && st.ChangeID != "" {
				det.TV.ChangeID, det.TV.Number = st.ChangeID, st.Number
				noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, runID, "", det), "run_id", runID)
			}
			break
		}
		st, err = d.TV.RetryAutoUpdate(ctx, runID, who)
	case "drop":
		if det.TV.noChange() {
			// nothing to cancel: the TV stays on the software it runs until a later release
			if det.TV.Dropped {
				return RunTV{}, ErrTVNotHeld
			}
			det.TV.Dropped = true
			noteStore(d.log(), "SetRunDetail", d.Store.SetRunDetail(ctx, runID, "", det), "run_id", runID)
			st = tvchange.AutoUpdateStatus{Outcome: tvchange.AutoOutcomeCancelled, EndReason: tvchange.EndDropped}
			break
		}
		st, err = d.TV.DropAutoUpdate(ctx, runID, who)
	default:
		return RunTV{}, errors.New("unknown action")
	}
	if errors.Is(err, tvchange.ErrNotHeld) {
		return RunTV{}, ErrTVNotHeld
	}
	if err != nil {
		return RunTV{}, err
	}
	d.Store.Audit(ctx, actor, "admin", "tv_"+action, run.Version, map[string]any{"run_id": runID,
		"change_id": det.TV.ChangeID})
	out := *det.TV
	out.State = tvState(st)
	return out, nil
}

// replaceOlderTV lets a release that committed replace an older run's held TV change
// (best effort: logged).
func (d *Driver) replaceOlderTV(ctx context.Context, run Run) {
	if d.TV == nil {
		return
	}
	if err := d.TV.ReplaceOlderAutoUpdates(ctx, run.ID); err != nil {
		d.log().Warn("older held TV changes not replaced", "run_id", run.ID, "error", err.Error())
	}
}

// TVHeld is the run whose TV is held now: the newest of the last runs with a TV held at
// its commit, while its change is held or checked again (nil: none).
func (d *Driver) TVHeld(ctx context.Context) *TVHeldNow {
	if d == nil || d.TV == nil {
		return nil
	}
	runs, err := d.Store.Runs(ctx, 10)
	if err != nil {
		return nil
	}
	for _, r := range runs {
		_, det, err := d.Store.RunDetailOf(ctx, r.ID)
		if err != nil || det.TV == nil || det.TV.Outcome == TVPending {
			continue // (a TV pending at the commit was never held)
		}
		det.TV.fill(ctx, d.TV, r.ID)
		if det.TV.State != TVStateHeld && det.TV.State != TVStateRetrying {
			return nil
		}
		return &TVHeldNow{RunID: r.ID, Version: r.Version, RunTV: *det.TV}
	}
	return nil
}

// TVAction serves POST /api/cluster/updates/runs/{id}/tv/{action} (retry, drop).
func (h *Handler) TVAction(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	d := h.driver(w, r)
	if d == nil {
		return
	}
	action := chi.URLParam(r, "action")
	if action != "retry" && action != "drop" {
		httperror.Write(w, r, httperror.NotFound("action"))
		return
	}
	out, err := d.TVAction(r.Context(), chi.URLParam(r, "id"), action, p.UserID)
	switch {
	case err == nil:
		common.WriteJSON(w, r, http.StatusOK, out)
	case errors.Is(err, ErrNoSuchRun):
		httperror.Write(w, r, httperror.NotFound("update run"))
	case errors.Is(err, ErrTVNotHeld):
		httperror.Write(w, r, httperror.Conflict("about:blank#tv_not_held",
			"the run's TV change is not held (it was retried, dropped or replaced)").With("code", "tv_not_held"))
	case errors.Is(err, ErrRunActive):
		httperror.Write(w, r, httperror.Conflict("about:blank#run_active", "an update is running").With("code", BlockActiveRun))
	case errors.Is(err, ErrOrchestratorUnavailable):
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#orchestrator_unavailable", Title: "service unavailable",
			Status: http.StatusServiceUnavailable, Detail: "this API has no TV change controller"}).
			With("code", ErrOrchestratorUnavailable.Error()))
	default:
		httperror.Write(w, r, httperror.Internal("tv "+action).Wrap(err))
	}
}
