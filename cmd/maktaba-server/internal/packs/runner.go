package packs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
//     this machine (a Full home's venv counts as on it: Manager.OnDisk); an asset with
//     no desired row is wanted (the catalog default, as the API reads it: enabled);
//   - a model is installed only when an admin asks while the server runs: its want is
//     seen to change to installed or enabled, or its row is written again (a new
//     generation: the install route's retry). A model the pipeline downloads itself on
//     first use still does so;
//   - a removal runs only when the want is seen to change to absent, or to finish one a
//     restart cut short (.removing);
//   - each action runs once per desired generation: a failure is reported, and tried
//     again when an admin asks again (a new generation, or the install route). A busy
//     outcome (another install or removal held the asset) is no failure: it is tried
//     again once that one ends, while the want still asks for it;
//   - an empty desired state is a home not seeded yet (the seed writes rows for every
//     component it decides): nothing acts on it, so an asset the seed leaves absent is
//     never installed in the moment before the seed;
//   - an asset whose want is absent and is not on this machine reads not installed, never
//     failed (a failure an earlier want left is not its state now).
//
// With Off ([components] reconcile = "off") it reports and acts on nothing.
type Runner struct {
	M       *Manager
	Status  StatusReporter
	Machine string
	Off     bool
	// Tick is how often the rows are reported when they changed (0: 1 s).
	Tick time.Duration
	// BusyRetry is how long a busy action waits before it is tried again (0: 2 s).
	BusyRetry time.Duration

	mu      sync.Mutex
	ctx     context.Context
	desired map[string]store.Desired
	diskMB  map[string]int64 // an installed pack folder's size, by its path (measured once)
	seen    bool
	tried   map[string]int64
	written []store.Status
	next    time.Time
}

// Desired takes a desired state the reconciler read from the database and acts on it.
func (r *Runner) Desired(desired map[string]store.Desired) {
	r.mu.Lock()
	if len(desired) == 0 {
		// the home has not seeded: the rows read the catalog defaults, nothing acts, and
		// the first seeded state is the first one seen
		r.desired = desired
		r.mu.Unlock()
		return
	}
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
		if !has {
			// no row: the catalog default (the API's wantOf), generation 0
			d = store.Desired{ID: id, Want: components.WantEnabled}
		}
		pd, phas := prev[id]
		changed := !first && has && (!phas || pd.Want != d.Want)
		// an admin asked again: the row was written while the server runs (the install
		// route writes it again to retry a failed install)
		asked := changed || (!first && has && phas && pd.Generation != d.Generation)
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
		wanted := d.Want == components.WantInstalled || d.Want == components.WantEnabled
		switch {
		case !has && r.M.Layout.Removing(id):
			r.act(id, -1, false, func(ctx context.Context) error { return r.M.Remove(ctx, id) })
		case wanted && !r.M.Installed(id) && !r.M.Disabled && (isPack || asked):
			r.act(id, d.Generation, true, func(ctx context.Context) error { return r.M.Install(ctx, id, nil) })
		case has && d.Want == components.WantAbsent && (changed || r.M.Layout.Removing(id)):
			if by := RequiredBy(r.M.Catalog, id, desired, r.M.Installed); len(by) > 0 {
				r.M.logf("level=warn %s is not removed: %s need it", id, strings.Join(by, ", "))
				r.mu.Lock()
				r.tried[id] = d.Generation
				r.mu.Unlock()
				continue
			}
			if r.M.Installed(id) || r.M.Layout.Removing(id) {
				r.act(id, d.Generation, false, func(ctx context.Context) error { return r.M.Remove(ctx, id) })
			}
		}
	}
}

// act runs f (an install when install, else a removal) for id's generation gen. A busy
// outcome is tried again every BusyRetry, once nothing of id runs in this process, while
// this action is still the one the runner decided and the want still asks for it.
func (r *Runner) act(id string, gen int64, install bool, f func(context.Context) error) {
	r.mu.Lock()
	r.tried[id] = gen
	ctx := r.ctx
	r.mu.Unlock()
	wait := r.BusyRetry
	if wait <= 0 {
		wait = 2 * time.Second
	}
	go func() {
		for {
			err := f(ctx)
			if err == nil || ReasonOf(err) != ReasonBusy || ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if err := r.M.waitIdle(ctx, id); err != nil {
				return
			}
			if !r.stillWants(id, gen, install) {
				return
			}
			r.M.logf("%s: tried again (it was busy)", id)
		}
	}()
}

// stillWants reports whether the runner still decides act's action for id: the same
// generation tried, and the want still an install's (installed, enabled or no row) or a
// removal's (absent, or no row with a removal to finish).
func (r *Runner) stillWants(id string, gen int64, install bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tried[id] != gen {
		return false
	}
	d, has := r.desired[id]
	if install {
		return !has || d.Want == components.WantInstalled || d.Want == components.WantEnabled
	}
	return !has || d.Want == components.WantAbsent
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
		d, has := desired[id]
		if !has {
			d.Want = components.WantEnabled // the catalog default
		}
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
		case ok && p.Phase == components.PhaseFailed && !installed && p.Reason != ReasonBusy &&
			(d.Want != components.WantAbsent || r.M.Layout.Removing(id)):
			// a failure is the state of an asset still wanted, or of a removal not
			// finished; busy (another install or removal held it) is tried again, never
			// a failure
			row.Phase = components.PhaseFailed
			inst.Reason, inst.Params = p.Reason, p.Params
		case installed:
			row.Phase = components.PhaseOff
			if mb, ok := r.packMB(id, comp); ok {
				row.Detail = map[string]any{"disk_mb": mb}
			}
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

// packMB is the size of id's current pack folder on disk (story 51.14: the health's
// disk_mb), measured once per folder; ok is false for an asset that is not a pack here or
// has no folder (one from before packs: the catalog's sizes stand for it).
func (r *Runner) packMB(id string, comp *components.Component) (int64, bool) {
	if !strings.HasPrefix(comp.SourceFor(r.M.Platform), "pack:") {
		return 0, false
	}
	cur := r.M.Layout.Current(id)
	if cur == "" {
		return 0, false
	}
	dir := filepath.Join(r.M.Layout.Dir(id), cur)
	r.mu.Lock()
	mb, ok := r.diskMB[dir]
	r.mu.Unlock()
	if ok {
		return mb, true
	}
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	mb = (n + 1<<20 - 1) >> 20
	r.mu.Lock()
	if r.diskMB == nil {
		r.diskMB = map[string]int64{}
	}
	r.diskMB[dir] = mb
	r.mu.Unlock()
	return mb, true
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
