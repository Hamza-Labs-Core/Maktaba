package packs

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Part is the component_status part the asset rows are reported under: a part of its own,
// so the supervisor's process rows (part "supervisor") and these never prune each other.
const Part = "packs"

// StatusReporter writes a part's rows (*store.Store is one).
type StatusReporter interface {
	ReportStatus(ctx context.Context, machine, part string, rows []store.Status) error
}

// Runner is the asset half of the supervisor's reconciler (story 51.16): it acts on each
// desired state the reconciler reads and reports every asset's status.
//
// The rules keep a Full home as it is today:
//   - a pack asset (runtime.ml, tool.yt-dlp) is installed when it is wanted and not on
//     this machine (a Full home's venv counts as on it: Manager.OnDisk);
//   - a model is installed only when its want is seen to change to installed or enabled
//     while the server runs (a model the pipeline downloads itself on first use still
//     does so);
//   - a removal runs only when the want is seen to change to absent, or to finish one a
//     restart cut short (.removing);
//   - each action runs once per desired generation: a failure is reported, and tried
//     again when an admin asks again (a new generation, or the install route).
//
// With Off ([components] reconcile = "off") it reports and acts on nothing.
type Runner struct {
	M       *Manager
	Status  StatusReporter
	Machine string
	Off     bool
	// Tick is how often the rows are reported when they changed (0: 1 s).
	Tick time.Duration

	mu      sync.Mutex
	ctx     context.Context
	desired map[string]store.Desired
	seen    bool
	tried   map[string]int64
	written []store.Status
	next    time.Time
}

// Desired takes a desired state the reconciler read from the database and acts on it.
func (r *Runner) Desired(desired map[string]store.Desired) {
	r.mu.Lock()
	prev, first := r.desired, !r.seen
	r.desired, r.seen = desired, true
	ctx := r.ctx
	if r.tried == nil {
		r.tried = map[string]int64{}
	}
	r.mu.Unlock()
	if ctx == nil || r.Off {
		return
	}
	for _, id := range r.M.Catalog.Order() {
		comp, _ := r.M.Catalog.Get(id)
		if comp.Kind != components.KindAsset || (len(comp.Platforms) > 0 && !slices.Contains(comp.Platforms, r.M.Platform)) {
			continue
		}
		d, has := desired[id]
		pd, phas := prev[id]
		changed := !first && has && (!phas || pd.Want != d.Want)
		src := comp.SourceFor(r.M.Platform)
		isPack, isModel := strings.HasPrefix(src, "pack:"), components.IsModelSource(src)
		if !isPack && !isModel || r.M.Busy(id) {
			continue
		}
		r.mu.Lock()
		gen, done := r.tried[id]
		r.mu.Unlock()
		if done && gen == d.Generation && !changed {
			continue
		}
		wanted := has && (d.Want == components.WantInstalled || d.Want == components.WantEnabled)
		switch {
		case wanted && !r.M.Installed(id) && !r.M.Disabled && (isPack || changed):
			r.act(id, d.Generation, func(ctx context.Context) error { return r.M.Install(ctx, id, nil) })
		case has && d.Want == components.WantAbsent && (changed || r.M.Layout.Removing(id)):
			if by := RequiredBy(r.M.Catalog, id, desired, r.M.Installed); len(by) > 0 {
				r.M.logf("level=warn %s is not removed: %s need it", id, strings.Join(by, ", "))
				r.mu.Lock()
				r.tried[id] = d.Generation
				r.mu.Unlock()
				continue
			}
			if r.M.Installed(id) || r.M.Layout.Removing(id) {
				r.act(id, d.Generation, func(ctx context.Context) error { return r.M.Remove(ctx, id) })
			}
		case !has && r.M.Layout.Removing(id):
			r.act(id, -1, func(ctx context.Context) error { return r.M.Remove(ctx, id) })
		}
	}
}

func (r *Runner) act(id string, gen int64, f func(context.Context) error) {
	r.mu.Lock()
	r.tried[id] = gen
	ctx := r.ctx
	r.mu.Unlock()
	go func() { _ = f(ctx) }()
}

// Want is the desired state the runner saw last (nil before the first).
func (r *Runner) Want() map[string]store.Desired {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.desired
}

// RequiredBy are the enabled components that need id on this machine: id is in one of
// their requirements and no other component of that requirement is installed.
func RequiredBy(cat *components.Catalog, id string, desired map[string]store.Desired, installed func(string) bool) []string {
	var out []string
	for _, dep := range cat.Dependents(id) {
		d, ok := desired[dep]
		if !ok || d.Want != components.WantEnabled {
			continue
		}
		for _, req := range cat.Requires(dep) {
			if !slices.Contains(req.AnyOf, id) {
				continue
			}
			other := false
			for _, alt := range req.AnyOf {
				if alt != id && installed != nil && installed(alt) {
					other = true
				}
			}
			if !other {
				out = append(out, dep)
				break
			}
		}
	}
	return out
}

// Run reports the asset rows until ctx ends (and gives the actions their context).
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	tick := r.Tick
	if tick <= 0 {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		r.report(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Rows are the status rows of every asset of this platform.
func (r *Runner) Rows() []store.Status {
	r.mu.Lock()
	desired := r.desired
	r.mu.Unlock()
	var out []store.Status
	for _, id := range r.M.Catalog.Order() {
		comp, _ := r.M.Catalog.Get(id)
		if comp.Kind != components.KindAsset || (len(comp.Platforms) > 0 && !slices.Contains(comp.Platforms, r.M.Platform)) {
			continue
		}
		d := desired[id]
		row := store.Status{ID: id, ObservedGeneration: d.Generation}
		installed := r.M.Installed(id)
		inst := store.Condition{Type: components.CondInstalled, Status: "false"}
		if installed {
			inst.Status = "true"
		}
		p, ok := r.M.Progress(id)
		switch {
		case ok && p.Busy:
			row.Phase = p.Phase
			row.Detail = map[string]any{"progress": p.Percent}
			if p.Detail != "" {
				row.Detail["detail_code"] = p.Detail
			}
			if p.Name != "" {
				row.Detail["name"] = p.Name
			}
			if p.Total > 0 {
				row.Detail["done"], row.Detail["total"] = p.Done, p.Total
			}
		case ok && p.Phase == components.PhaseFailed && !installed:
			row.Phase = components.PhaseFailed
			inst.Reason, inst.Params = p.Reason, p.Params
		case installed:
			row.Phase = components.PhaseOff
		case r.M.Version != "" && r.M.Layout.Missing(id) == r.M.Version:
			// a rollback came back to this release and its folder was gone (story 51.18):
			// installing it again clears this
			row.Phase = components.PhaseFailed
			inst.Reason, inst.Params = ReasonPackMissing, map[string]any{"component": id}
		default:
			row.Phase = components.PhaseNotInstalled
			if r.M.Disabled && (d.Want == components.WantInstalled || d.Want == components.WantEnabled) {
				inst.Reason = ReasonPacksDisabled
			}
		}
		row.Conditions = append(row.Conditions, inst)
		if r.Off {
			row.Conditions = append(row.Conditions, store.Condition{Type: "Reconciled", Status: "false", Reason: "reconcile_off"})
		}
		out = append(out, row)
	}
	return out
}

func (r *Runner) report(ctx context.Context) {
	if r.Status == nil {
		return
	}
	rows := r.Rows()
	now := time.Now()
	r.mu.Lock()
	same := r.written != nil && reflect.DeepEqual(r.written, rows)
	wait := now.Before(r.next)
	r.mu.Unlock()
	if same || wait {
		return
	}
	if err := r.Status.ReportStatus(ctx, r.Machine, Part, rows); err != nil {
		r.mu.Lock()
		r.next = now.Add(5 * time.Second)
		r.mu.Unlock()
		if ctx.Err() == nil {
			r.M.logf("level=warn the assets' status was not written: %v", err)
		}
		return
	}
	r.mu.Lock()
	r.written, r.next = rows, time.Time{}
	r.mu.Unlock()
}

// String is for the log.
func (p Progress) String() string {
	return fmt.Sprintf("%s %d%% %s %s", p.Phase, p.Percent, p.Detail, p.Reason)
}
