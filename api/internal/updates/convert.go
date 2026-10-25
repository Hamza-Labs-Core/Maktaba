package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Worker-only machines (story 35.8 AC8, design §9.4): a machine that joined with the
// Epic 31 installer runs the pipeline alone, with no machine agent. Its next update makes
// it a full node: on its turn the run drains it and sends its pipeline worker
// `update.convert` (the run's job); the worker downloads the release (the home's mirror
// first), verifies it itself (the pinned signing identity, every file against
// checksums.txt) and runs the release's `maktaba-server setup --node --converting`, which
// installs the layout, registers the launcher's service and removes the old worker units
// once the new service answers. The target is healthy once the machine's new agent comes
// on the release. A worker whose pipeline predates the command is worker_only ("re-run the
// installer once"), until its pipeline changes.

// KindWorker is a worker-only machine's install kind (its target's kind until converted).
const KindWorker = "worker"

// convertTimeout is how long a conversion may take (download, verification, the
// pipeline's environment, the new service's first start) before its target fails.
const convertTimeout = 2 * time.Hour

// Conversion failure codes (the worker reports download_failed, verify_*, release_incomplete
// and install_failed itself).
const (
	CodeConvertRefused = "convert_refused" // the worker refused update.convert (another reason than its age)
	CodeConvertTimeout = "convert_timeout" // no new machine agent on the release within convertTimeout
)

// WhyTooOld is a worker_only target's detail when its pipeline predates update.convert.
const WhyTooOld = "too_old"

// NodeWorker is a machine's pipeline worker on the cluster channel.
type NodeWorker struct {
	Session string
	// Version is the pipeline version the worker reported
	Version string
}

// nodeWorkerer is a Cluster that knows the machines' pipeline workers.
type nodeWorkerer interface {
	NodeWorkers() map[string]NodeWorker
}

// nodeWorkers are the machines' connected pipeline workers, by node id (none when the
// Cluster does not know them).
func (d *Driver) nodeWorkers() map[string]NodeWorker {
	if w, ok := d.Cluster.(nodeWorkerer); ok {
		return w.NodeWorkers()
	}
	return map[string]NodeWorker{}
}

// convertState is a worker's answer to update.convert: how far its conversion got.
type convertState struct {
	State  string `json:"state"` // started | running | done | failed
	Step   string `json:"step,omitempty"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// readConvertState reads a worker's answer (an object; a plain string is its detail).
func readConvertState(raw json.RawMessage) convertState {
	var s convertState
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		s.Detail = text
	}
	return s
}

// unknownCommand reports whether a worker's refusal says it does not know the command (a
// pipeline older than update.convert).
func unknownCommand(raw json.RawMessage) bool {
	return strings.Contains(strings.ToLower(readConvertState(raw).Detail), "unknown command")
}

// tooOldKnown reports whether the machine's last part in a run of version found its
// pipeline too old for update.convert and that pipeline still runs (the same version):
// it is worker_only at once, without asking again. An installer run changes its pipeline.
func (d *Driver) tooOldKnown(ctx context.Context, nodeID, version string, w NodeWorker) bool {
	t, ok, err := d.Store.LastTargetOf(ctx, nodeID, version)
	if err != nil || !ok || t.State != TargetWorkerOnly {
		return false
	}
	var det struct {
		Why     string `json:"why"`
		Version string `json:"pipeline_version"`
	}
	_ = json.Unmarshal(t.Detail, &det)
	return det.Why == WhyTooOld && det.Version == w.Version
}

// convertJob is the run's job for a worker-only machine's conversion, with a download
// token for the home's mirror when the home keeps one (the machine has no delegation key
// yet; without a token it downloads from GitHub).
func (d *Driver) convertJob(ctx context.Context, run Run, t Target) (MachineJob, error) {
	job, err := d.job(ctx, run, false)
	if err != nil {
		return job, err
	}
	if d.Mirror != nil {
		if tok, err := d.Mirror.DownloadToken(t.NodeID, run.Version); err == nil {
			job.DownloadToken = tok
		} else {
			d.log().Warn("update mirror: no download token for a conversion", "event", "update_mirror_token_failed",
				"node_id", t.NodeID, "err", err.Error())
		}
	}
	return job, nil
}

// startConvert sends a worker-only machine's worker update.convert (its turn came, it is
// drained): converting, or worker_only when its pipeline predates the command.
func (d *Driver) startConvert(ctx context.Context, run Run, pol Policy, t Target, w NodeWorker) {
	job, err := d.convertJob(ctx, run, t)
	if err != nil {
		d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": CodeConvertRefused, "detail": err.Error()})
		return
	}
	ok, raw, err := d.Cluster.Command(ctx, w.Session, "update.convert", job, 30*time.Second)
	switch {
	case err == nil && !ok && unknownCommand(raw):
		_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetWorkerOnly,
			Detail: map[string]any{"why": WhyTooOld, "pipeline_version": w.Version}})
		d.audit(ctx, run, "node_worker_only", map[string]any{"node_id": t.NodeID, "pipeline_version": w.Version})
		d.nodeUndrain(ctx, run, t.NodeID)
	case err != nil || !ok:
		st := readConvertState(raw)
		code := firstNonEmpty(st.Code, CodeConvertRefused)
		d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": code, "step": st.Step,
			"detail": strings.TrimSpace(fmt.Sprint(errText(err), " ", st.Detail))})
	default:
		d.audit(ctx, run, "node_convert", map[string]any{"node_id": t.NodeID})
		_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetConverting, Applied: true,
			Attempt: true, Error: "clear", Detail: readConvertState(raw)})
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// followConvert follows a machine converting; done when its part is over. Its new machine
// agent on the release makes it healthy (on another release: a full node now, prepared and
// applied by this run as any other); the worker's failure, or none of that within
// convertTimeout, fails it.
func (d *Driver) followConvert(ctx context.Context, run Run, pol Policy, t Target, agents map[string]string,
	states map[string]agentState, workers map[string]NodeWorker) bool {
	if _, ok := agents[t.NodeID]; ok {
		s := states[t.NodeID]
		if s.Version != "" && CompareVersions(s.Version, run.Version) == 0 {
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetHealthy, Healthy: true,
				Kind: firstNonEmpty(s.Layout, KindWorker), Detail: map[string]any{"converted": true}})
			d.audit(ctx, run, "node_converted", map[string]any{"node_id": t.NodeID, "layout": s.Layout})
			d.nodeUndrain(ctx, run, t.NodeID)
			return true
		}
		if s.Version != "" {
			// converted onto another release: the run takes it from here as a full node
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPending, Kind: firstNonEmpty(s.Layout, "self_managed"),
				From: NormalizeVersion(s.Version), Detail: map[string]any{"converted": true}})
			d.audit(ctx, run, "node_converted", map[string]any{"node_id": t.NodeID, "version": s.Version})
			return true
		}
	}
	if w, ok := workers[t.NodeID]; ok {
		job, err := d.convertJob(ctx, run, t)
		if err == nil {
			// asked again for the same run, the worker says how far it got
			ok, raw, err := d.Cluster.Command(ctx, w.Session, "update.convert", job, 15*time.Second)
			if err == nil {
				st := readConvertState(raw)
				if !ok || st.State == "failed" {
					d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": firstNonEmpty(st.Code, CodeConvertRefused),
						"step": st.Step, "detail": st.Detail})
					return true
				}
				_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{Detail: st})
			}
		}
	}
	if t.AppliedAt != nil && d.now().Sub(*t.AppliedAt) > convertTimeout {
		d.targetFailed(ctx, run, pol, t, TargetFailed, map[string]any{"code": CodeConvertTimeout,
			"detail": fmt.Sprintf("not a full node on %s within %s", NormalizeVersion(run.Version), convertTimeout)})
		return true
	}
	return false
}

// adoptWorkers takes back into a rollout the worker-only machines that were offline when
// it began and whose worker is back.
func (d *Driver) adoptWorkers(ctx context.Context, run Run, targets []Target, workers map[string]NodeWorker) {
	for _, t := range targets {
		if t.State != TargetOffline || t.Kind != KindWorker {
			continue
		}
		if _, ok := workers[t.NodeID]; ok {
			_ = d.Store.UpdateTarget(ctx, run.ID, t.NodeID, TargetChange{State: TargetPending, Error: "clear"})
			d.audit(ctx, run, "node_back", map[string]any{"node_id": t.NodeID})
		}
	}
}

// What becomes of a worker-only machine (WorkerConvert, Machine.Convert).
const (
	ConvertNextUpdate = "next_update" // its next update makes it a full node
	ConvertInstaller  = "installer"   // its pipeline is too old for that: re-run the installer once
)

// WorkerConvert says, for each worker-only machine (no machine agent, not a full node),
// whether its next update converts it or its installer must run once (its pipeline too
// old for update.convert, as its last part in a run of the cluster's release found).
func (d *Driver) WorkerConvert(ctx context.Context) map[string]string {
	out := map[string]string{}
	if d == nil || d.Store == nil || d.Cluster == nil {
		return out
	}
	nodes, err := d.nodes(ctx)
	if err != nil {
		return out
	}
	wm, _ := d.Store.Watermark(ctx)
	agents := d.Cluster.MachineAgents()
	workers := d.nodeWorkers()
	for _, n := range nodes {
		if _, ok := agents[n.id]; ok || n.full {
			continue
		}
		out[n.id] = ConvertNextUpdate
		if wm == "" {
			continue
		}
		w, ok := workers[n.id]
		if !ok {
			// not on the channel now: what its last part found, if anything
			if t, found, err := d.Store.LastTargetOf(ctx, n.id, wm); err == nil && found && t.State == TargetWorkerOnly {
				out[n.id] = ConvertInstaller
			}
			continue
		}
		if d.tooOldKnown(ctx, n.id, wm, w) {
			out[n.id] = ConvertInstaller
		}
	}
	return out
}

// workerCatchUps are the worker-only machines a catch-up converts to the cluster's release
// wm (inside the window): a worker on the channel, not known too old for update.convert,
// not held back by a failed conversion of wm.
func (d *Driver) workerCatchUps(ctx context.Context, nodes []nodeRow, agents map[string]string, wm string) []string {
	workers := d.nodeWorkers()
	var ids []string
	for _, n := range nodes {
		if n.full {
			continue
		}
		if _, ok := agents[n.id]; ok {
			continue // it has a machine agent: a full node, caught up as one
		}
		w, ok := workers[n.id]
		if !ok || d.tooOldKnown(ctx, n.id, wm, w) || d.heldBack(ctx, n.id, wm, "", KindWorker, nil) {
			continue
		}
		ids = append(ids, n.id)
	}
	return ids
}
