package updates

// Story 35.15 AC2: the update flow's events, each with run_id, version and host_id, in
// the API's structured log and the hub's events file (Epic 31 §7.4). They are read off
// the database rather than raised where each change is made: every driver tick compares
// the runs, their machines' steps and targets and the catalog with what the last tick
// saw, and says what changed. A change made while the API was down (an update restarts
// it) is said once it is back; nothing is said twice in one process, and the first look
// after a start only learns what is there.

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"
)

// Event names (design §16).
const (
	EventPlanned        = "update_planned"
	EventStepStarted    = "update_step_started"
	EventStepFinished   = "update_step_finished"
	EventStepFailed     = "update_step_failed"
	EventHomeCommitted  = "update_home_committed"
	EventRolledBack     = "update_rolled_back"
	EventNodePrepared   = "update_node_prepared"
	EventNodeApplied    = "update_node_applied"
	EventNodeFailed     = "update_node_failed"
	EventNodeHeld       = "update_node_held"
	EventNodeCaughtUp   = "update_node_caught_up"
	EventLauncherRolled = "launcher_rollback"
	EventVerifyFailed   = "release_verify_failed"
	EventWithdrawn      = "release_withdrawn"
)

// HomeHost is host_id of the home's own events (a node's are its node id).
const HomeHost = "home"

// catalogLookEvery bounds how often the catalog is compared (it changes at a check).
const catalogLookEvery = 30 * time.Second

// launcherCodes are the codes of a release that did not stay running, so the launcher's
// guard (or the app) brought back the one before (updater.cameBackOld).
var launcherCodes = map[string]bool{"did_not_start": true, "did_not_stay_up": true}

// observer remembers what the driver's looks said. Each event is said once (by its key):
// a state a target passed through between two looks is still said, from what it left
// (its prepared_at, its steps' records).
type observer struct {
	mu      sync.Mutex
	started bool
	said    map[string]bool
	failed  map[string]bool // releases whose verification failed
	gone    map[string]bool // withdrawn releases
	catAt   time.Time
	first   bool // this look only learns what is there
}

// say emits an event once by key (silently on the first look after a start).
func (d *Driver) say(o *observer, key, name string, fields map[string]any) {
	if o.said[key] {
		return
	}
	o.said[key] = true
	if !o.first {
		d.emit(name, fields)
	}
}

// emit says one event: the structured log, and the hub's events file when there is one.
func (d *Driver) emit(name string, fields map[string]any) {
	args := []any{"event", name}
	for _, k := range []string{"run_id", "version", "host_id", "step", "state", "code", "reason"} {
		if v, ok := fields[k]; ok && v != "" {
			args = append(args, k, v)
		}
	}
	d.log().Info("update event", args...)
	if d.Events != nil {
		rec := map[string]any{"event": name}
		for k, v := range fields {
			rec[k] = v
		}
		d.Events(rec)
	}
}

// observe compares the recent runs and the catalog with what the last looks said.
func (d *Driver) observe(ctx context.Context) {
	o := &d.obs
	o.mu.Lock()
	defer o.mu.Unlock()
	o.first = !o.started
	if o.first {
		o.started = true
		o.said, o.failed, o.gone = map[string]bool{}, map[string]bool{}, map[string]bool{}
	}
	runs, err := d.Store.Runs(ctx, 3)
	if err != nil {
		return
	}
	for _, run := range runs {
		d.observeRun(ctx, o, run)
	}
	if o.first || d.now().Sub(o.catAt) >= catalogLookEvery {
		o.catAt = d.now()
		d.observeCatalog(ctx, o)
	}
}

func (d *Driver) observeRun(ctx context.Context, o *observer, run Run) {
	with := func(extra map[string]any) map[string]any {
		f := map[string]any{"run_id": run.ID, "version": run.Version}
		for k, v := range extra {
			f[k] = v
		}
		return f
	}
	home := func(extra map[string]any) map[string]any {
		f := with(extra)
		f["host_id"] = HomeHost
		return f
	}
	d.say(o, "planned/"+run.ID, EventPlanned, home(map[string]any{"trigger": run.Trigger, "from_version": run.FromVersion}))
	if run.State == RunHomeCommitted {
		d.say(o, "committed/"+run.ID, EventHomeCommitted, home(nil))
	}
	if run.State == RunRolledBack {
		code := errCode(run.Error)
		d.say(o, "rolled_back/"+run.ID, EventRolledBack, home(map[string]any{"code": code}))
		if launcherCodes[code] {
			d.say(o, "launcher/"+run.ID, EventLauncherRolled, home(map[string]any{"code": code}))
		}
	}
	if steps, err := d.Store.Steps(ctx, run.ID); err == nil {
		for _, st := range steps {
			key := run.ID + "/" + st.Host + "/" + st.Step
			f := with(map[string]any{"host_id": st.Host, "step": st.Step, "state": st.State})
			if st.State == "skipped" {
				continue
			}
			d.say(o, "step_started/"+key, EventStepStarted, f)
			switch st.State {
			case "done":
				d.say(o, "step_finished/"+key, EventStepFinished, f)
				if st.Host == HomeHost && st.Step == "commit" {
					d.say(o, "committed/"+run.ID, EventHomeCommitted, home(nil))
				}
			case "failed":
				d.say(o, "step_failed/"+key, EventStepFailed, f)
			}
		}
	}
	targets, err := d.Store.Targets(ctx, run.ID)
	if err != nil {
		return
	}
	for _, t := range targets {
		key := run.ID + "/" + t.NodeID
		f := with(map[string]any{"host_id": t.NodeID, "state": t.State, "name": t.Name})
		code := errCode(t.Error)
		if code != "" {
			f["code"] = code
		}
		if t.PreparedAt != nil {
			d.say(o, "prepared/"+key, EventNodePrepared, f)
		}
		// a machine's failure is said again after a retry (another attempt)
		attempt := key + "/" + strconv.Itoa(t.Attempts)
		switch t.State {
		case TargetHealthy:
			d.say(o, "applied/"+key, EventNodeApplied, f)
			if run.Trigger == TriggerCatchUp {
				d.say(o, "caught_up/"+key, EventNodeCaughtUp, f)
			}
		case TargetFailed, TargetFailedPrepare, TargetRolledBack, TargetNeedsPerson:
			d.say(o, "failed/"+attempt, EventNodeFailed, f)
			if t.State == TargetRolledBack && launcherCodes[code] {
				d.say(o, "launcher/"+attempt, EventLauncherRolled, f)
			}
		case TargetHeld:
			d.say(o, "held/"+attempt, EventNodeHeld, f)
		}
	}
}

func (d *Driver) observeCatalog(ctx context.Context, o *observer) {
	cat, err := d.Store.Catalog(ctx)
	if err != nil {
		return
	}
	for _, r := range cat {
		if r.VerifyError != nil && !o.failed[r.Version] {
			o.failed[r.Version] = true
			if !o.first {
				d.emit(EventVerifyFailed, map[string]any{"version": r.Version, "host_id": HomeHost,
					"reason": r.VerifyError.Kind, "file": r.VerifyError.File})
			}
		} else if r.VerifyError == nil {
			delete(o.failed, r.Version) // verified again: a later failure is said
		}
		if r.WithdrawnAt != nil && !o.gone[r.Version] {
			o.gone[r.Version] = true
			if !o.first {
				d.emit(EventWithdrawn, map[string]any{"version": r.Version, "host_id": HomeHost})
			}
		}
	}
}

// errCode is an error document's code ("" none).
func errCode(raw json.RawMessage) string {
	var doc struct {
		Code string `json:"code"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	return doc.Code
}
