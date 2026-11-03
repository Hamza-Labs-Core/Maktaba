package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// The supervisor's reconciler (Epic 51 story 51.04): it reads this machine's desired
// state, opens and closes the gates of the processes (Allow) and runs the supervisor's
// own loops of switchable components, requirements first and dependents stopped first,
// and reports what it sees as component_status rows of part "supervisor".
//
// Level based: every pass compares desired state with what runs and acts on the
// difference, so a missed notification is caught up by the next one. A pass runs when
// the desired state changes (components_changed, or the store's 30 s revision poll) and
// every Tick on what the supervisor already holds (no query): a process that came up
// lets its dependents start, a crash is reported.
//
// A component with no desired row runs as today's server.toml built it (Phase 1: the
// seed, story 51.03, writes rows equal to it). [components] reconcile = "off" opens and
// closes nothing: every process runs as built, and the status says reconcile_off.

// EnvComponentsReconcile is the kill switch every child is given when [components]
// reconcile = "off": its own runner reports and acts on nothing (stories 51.05, 51.06).
const EnvComponentsReconcile = "MAKTABA_COMPONENTS_RECONCILE"

// StatusPart is the part the supervisor's status rows carry.
const StatusPart = "supervisor"

// CondReconciled is the condition that says why the reconciler does not act on a
// component: reconcile_off, boot_flag or env_override.
const CondReconciled = "Reconciled"

// Reason codes the supervisor reports (the catalog's reasons).
const (
	reasonCrashLoop      = "crash_loop"
	reasonDependencyOff  = "dependency_off"
	reasonHeld           = "held"
	reasonLicence        = "licence_missing"
	reasonPlatform       = "platform_unsupported"
	reasonMemory         = "not_enough_memory"
	reasonReconcileOff   = "reconcile_off"
	ReasonBootFlag       = "boot_flag"
	ReasonEnvOverride    = "env_override"
	condTrue, condFalse  = "true", "false"
	condUnknown          = "unknown"
	reconcileTick        = time.Second
	reconcileRetry       = 2 * time.Second
	statusRetry          = 5 * time.Second
	defaultReconcileMach = store.Home
)

// SupervisorLoop is a loop the supervisor runs for a switchable component
// (supervisor:mdns, supervisor:backups). The reconciler gives it its own context:
// cancelled when the component is turned off.
type SupervisorLoop struct {
	Name string // "supervisor:<name>", as the catalog lists it
	Run  func(ctx context.Context)
	// ConfigOn: it runs when its component has no desired row (today's server.toml).
	ConfigOn bool
	// Forced, when set (boot_flag, env_override), keeps it as ForcedOn for this boot,
	// whatever the desired state, and is reported.
	Forced       string
	ForcedOn     bool
	ForcedParams map[string]any
	component    string
}

// AssetState answers whether an asset component is installed on this machine.
type AssetState interface {
	Installed(id string) bool
}

// gateTable is what the reconciler reads and drives: *Supervisor (tests give a fake).
type gateTable interface {
	procViews() []procView
	Allow(role Role, instance string, on bool) error
}

// StatusReporter writes a part's status rows (*store.Store is one).
type StatusReporter interface {
	ReportStatus(ctx context.Context, machine, part string, rows []store.Status) error
}

// Reconciler is the supervisor's component loop. Set its fields, Prime it before the
// supervisor's Run, then Run it.
type Reconciler struct {
	Catalog *components.Catalog
	// Source is the desired state (nil: none; the gates follow the cache, else today's).
	Source   store.Source
	Listener store.Listener // nil: poll only (or NewListener)
	// NewListener, when set, makes the Listener once the database first answers (a
	// listener made while it is down would wait for it).
	NewListener func(onError func(error)) (store.Listener, error)
	Status      StatusReporter // nil: no status written
	Machine     string         // store.Home or this node's id
	// CachePath is <state>/components.json ("": no cache).
	CachePath string
	Bundle    string
	// Off is [components] reconcile = "off".
	Off   bool
	Loops []*SupervisorLoop
	Log   io.Writer
	// Tick is how often a pass runs on what the supervisor holds (0: 1 s).
	Tick time.Duration
	// After is the store poll's timer (nil: time.After); Retry how often the first read
	// is tried until the database answers (0: 2 s).
	After func(time.Duration) <-chan time.Time
	Retry time.Duration
	// Platform is this machine's (default GOOS-GOARCH); FreeMB its free memory, asked
	// before a dormant process first starts (nil: not checked).
	Platform string
	FreeMB   func() int64
	Now      func() time.Time
	// Assets says which asset components are on this machine (story 51.16): a pack asset
	// a component requires blocks it until installed (nil: assets are not checked).
	// Models never block.
	Assets AssetState
	// OnDesired gets each desired state read from the database (the asset half acts on
	// it: packs.Runner).
	OnDesired func(desired map[string]store.Desired)

	table gateTable

	mu       sync.Mutex
	desired  map[string]store.Desired
	rev      int64
	fromDB   bool
	cached   bool
	loops    map[string]*loopRun
	written  []store.Status
	nextTry  time.Time
	lastErr  string
	updates  chan desiredUpdate
	passes   int
	loopsCtx context.Context
}

type desiredUpdate struct {
	desired map[string]store.Desired
	rev     int64
}

type loopRun struct {
	cancel    context.CancelFunc
	done      chan struct{}
	stopping  bool
	stoppedAt time.Time
	ended     bool // returned on its own while on: not started again until turned off and on
	warned    bool
}

// NewReconciler is a reconciler for sup's processes.
func NewReconciler(sup *Supervisor) *Reconciler {
	return &Reconciler{table: sup, Catalog: sup.catalog, Machine: defaultReconcileMach, Bundle: "full"}
}

func (r *Reconciler) defaults() {
	if r.Catalog == nil {
		r.Catalog = components.Default()
	}
	if r.Log == nil {
		r.Log = os.Stderr
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Tick <= 0 {
		r.Tick = reconcileTick
	}
	if r.Retry <= 0 {
		r.Retry = reconcileRetry
	}
	if r.Platform == "" {
		r.Platform = runtime.GOOS + "-" + runtime.GOARCH
	}
	if r.Machine == "" {
		r.Machine = defaultReconcileMach
	}
	if r.loops == nil {
		r.loops = map[string]*loopRun{}
	}
	for _, l := range r.Loops {
		if l.component == "" {
			host, name := splitLoop(l.Name)
			if comp, ok := r.Catalog.ByLoop(host, name); ok {
				l.component = comp.ID
			}
		}
	}
}

func splitLoop(n string) (string, string) {
	for i := range len(n) {
		if n[i] == ':' {
			return n[:i], n[i+1:]
		}
	}
	return n, ""
}

func (r *Reconciler) logf(format string, args ...any) {
	fmt.Fprintf(r.Log, "[components] "+format+"\n", args...)
}

// Prime sets the gates before the supervisor starts anything: from the cache of the last
// desired state the reconciler saw (a held component never starts during the boot), or
// as server.toml built the processes when there is none.
func (r *Reconciler) Prime() {
	r.defaults()
	if r.Off {
		r.logf("reconcile = \"off\" in server.toml: the processes run as server.toml builds them; status only")
		return
	}
	if r.CachePath != "" {
		d, rev, ok, err := readCompCache(r.CachePath, r.Machine)
		if err != nil {
			r.logf("level=warn the desired state's cache cannot be read (the gates follow server.toml until the database answers): %v", err)
		}
		if ok {
			r.mu.Lock()
			r.desired, r.rev, r.cached = d, rev, true
			r.mu.Unlock()
			r.logf("the gates follow the cached desired state (revision %d) until the database answers", rev)
		}
	}
	r.applyGates(r.table.procViews(), nil)
}

// Run reconciles until ctx ends. Prime first.
func (r *Reconciler) Run(ctx context.Context) {
	r.defaults()
	r.mu.Lock()
	r.updates = make(chan desiredUpdate, 1)
	r.loopsCtx = ctx
	r.mu.Unlock()
	if r.Source != nil {
		go r.watch(ctx)
	}
	t := time.NewTicker(r.Tick)
	defer t.Stop()
	r.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case u := <-r.updates:
			r.mu.Lock()
			changed := !r.fromDB || r.rev != u.rev || !reflect.DeepEqual(r.desired, u.desired)
			first := !r.fromDB
			if r.cached && r.rev > u.rev && first {
				r.logf("the cache's revision %d is newer than the database's %d (a restored database): the database wins",
					r.rev, u.rev)
			}
			r.desired, r.rev, r.fromDB = u.desired, u.rev, true
			r.mu.Unlock()
			n := r.pass(ctx)
			if r.OnDesired != nil {
				r.OnDesired(u.desired)
			}
			if changed {
				if r.CachePath != "" && !r.Off {
					if err := writeCompCache(r.CachePath, r.Machine, r.Bundle, u.desired, u.rev, r.Now()); err != nil {
						r.logf("level=warn the desired state's cache cannot be written: %v", err)
					}
				}
				r.logf("components_reconciled changed=%d revision=%d", n, u.rev)
			}
		case <-t.C:
			r.pass(ctx)
		}
	}
}

// watch reads the desired state: tried every Retry until the database answers (it may
// start after the supervisor), then the store's Watcher.
func (r *Reconciler) watch(ctx context.Context) {
	var first map[string]store.Desired
	var rev int64
	for {
		d, v, err := r.Source.Desired(ctx, r.Machine)
		if err == nil {
			first, rev = d, v
			break
		}
		r.readFailed(ctx, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.Retry):
		}
	}
	r.readOK()
	if r.Listener == nil && r.NewListener != nil {
		l, err := r.NewListener(func(err error) { r.readFailed(ctx, fmt.Errorf("listener: %w", err)) })
		if err != nil {
			r.logf("level=warn no components_changed listener (the desired state is polled every %s): %v",
				store.DefaultPoll, err)
		} else {
			r.Listener = l
			defer func() { _ = l.Close() }()
		}
	}
	w := &store.Watcher{Source: &primedSource{Source: r.Source, d: first, rev: rev}, Machine: r.Machine,
		Listener: r.Listener, After: r.After,
		OnError: func(err error) { r.readFailed(ctx, err) }}
	_ = w.Run(ctx, func(d map[string]store.Desired, v int64) {
		r.readOK()
		select {
		case r.updates <- desiredUpdate{d, v}:
		case <-ctx.Done():
		}
	})
}

// readFailed logs a failed read of the desired state once until it changes.
func (r *Reconciler) readFailed(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	same := r.lastErr == err.Error()
	r.lastErr = err.Error()
	r.mu.Unlock()
	if !same {
		r.logf("level=warn the desired state cannot be read (the gates stay as they are; tried again): %v", err)
	}
}

func (r *Reconciler) readOK() {
	r.mu.Lock()
	was := r.lastErr
	r.lastErr = ""
	r.mu.Unlock()
	if was != "" {
		r.logf("the desired state is read again")
	}
}

// primedSource answers its first Desired with the read already made (the Watcher loads at
// its start: no second query).
type primedSource struct {
	store.Source
	once sync.Once
	d    map[string]store.Desired
	rev  int64
}

func (p *primedSource) Desired(ctx context.Context, machine string) (map[string]store.Desired, int64, error) {
	used := true
	p.once.Do(func() { used = false })
	if !used {
		return p.d, p.rev, nil
	}
	return p.Source.Desired(ctx, machine)
}

// decision is a component's gate and why.
type decision struct {
	open     bool
	phase    string // the phase while closed
	conds    []store.Condition
	gen      int64
	evalReqs bool
}

func cond(typ, status, reason string, params map[string]any) store.Condition {
	return store.Condition{Type: typ, Status: status, Reason: reason, Params: params}
}

// decide is one component's gate from the desired state and what runs (views of every
// process, by component), with open the gate as it is now (for the start-only checks).
func (r *Reconciler) decide(comp *components.Component, procs map[string][]procView, loop *SupervisorLoop,
	desired map[string]store.Desired, wasOpen bool) decision {
	id := comp.ID
	own := procs[id]
	byDefault := loop != nil && loop.ConfigOn
	for _, p := range own {
		if !p.Dormant {
			byDefault = true
		}
	}
	d, has := desired[id]
	dec := decision{gen: d.Generation}
	switch {
	case !comp.Switchable:
		dec.open = true
		return dec
	case r.Off:
		dec.open = byDefault
		if loop != nil && loop.Forced != "" {
			dec.open = loop.ForcedOn
		}
		dec.phase = components.PhaseOff
		dec.conds = append(dec.conds, cond(CondReconciled, condFalse, reasonReconcileOff, nil))
		return dec
	case loop != nil && loop.Forced != "":
		dec.open = loop.ForcedOn
		dec.phase = components.PhaseOff
		dec.conds = append(dec.conds, cond(CondReconciled, condFalse, loop.Forced, loop.ForcedParams))
		return dec
	case !has:
		dec.open = byDefault
		dec.phase = components.PhaseOff
		return dec
	case d.Want == components.WantAbsent:
		dec.phase = components.PhaseNotInstalled
		return dec
	case d.Want != components.WantEnabled:
		dec.phase = components.PhaseOff
		return dec
	case d.Hold != nil:
		dec.phase = components.PhaseStopped
		dec.conds = append(dec.conds, cond(components.CondReady, condFalse, reasonHeld,
			map[string]any{"by": d.Hold.By}))
		return dec
	case comp.Licence != "" && !d.LicensedAt(r.Now()):
		// the licence does not grant it (story 51.19): it waits, the want kept
		dec.phase = components.PhaseWaiting
		dec.conds = append(dec.conds, cond(components.CondLicensed, condFalse, reasonLicence,
			map[string]any{"feature": comp.Licence}))
		return dec
	}
	dec.phase = components.PhaseWaiting
	if len(comp.Platforms) > 0 && !slices.Contains(comp.Platforms, r.Platform) {
		dec.conds = append(dec.conds, cond(components.CondSupported, condFalse, reasonPlatform,
			map[string]any{"platform": r.Platform}))
		return dec
	}
	dec.evalReqs = true
	met, unknown, off := r.requirements(id, procs, desired)
	switch {
	case !met && off != "":
		dec.conds = append(dec.conds, cond(components.CondRequirementsMet, condFalse, reasonDependencyOff,
			map[string]any{"component": off}))
		return dec
	case !met:
		dec.conds = append(dec.conds, cond(components.CondRequirementsMet, condUnknown, "",
			map[string]any{"component": unknown}))
		return dec
	}
	dec.conds = append(dec.conds, cond(components.CondRequirementsMet, condTrue, "", nil))
	// a process today's server.toml leaves out is checked for memory before it first
	// starts, never to stop one that runs
	if !wasOpen && r.FreeMB != nil && comp.Resources.RAMMB > 0 && len(own) > 0 && allDormant(own) {
		if free := r.FreeMB(); free > 0 && free < int64(comp.Resources.RAMMB) {
			dec.conds = append(dec.conds, cond(components.CondSupported, condFalse, reasonMemory,
				map[string]any{"free_mb": free, "ram_mb": comp.Resources.RAMMB}))
			return dec
		}
	}
	dec.open = true
	return dec
}

func allDormant(ps []procView) bool {
	for _, p := range ps {
		if !p.Dormant {
			return false
		}
	}
	return true
}

// requirements reports whether every requirement of id is met on this machine. When one
// is not: unknown names a requirement that is on but not running yet, off one that is
// turned off (dependency_off). A requirement the supervisor runs here must be Running;
// one another runner runs (a stage group, a loop) must be enabled and not held; a pack
// asset with a desired row must be installed (Assets, story 51.16: waiting until it is);
// a model, or any asset when Assets is nil, is not checked.
func (r *Reconciler) requirements(id string, procs map[string][]procView, desired map[string]store.Desired) (met bool, unknown, off string) {
	met = true
	for _, req := range r.Catalog.Requires(id) {
		groupMet, groupUnknown, groupOff := false, "", ""
		for _, alt := range req.AnyOf {
			comp, ok := r.Catalog.Get(alt)
			d, has := desired[alt]
			if ok && comp.Kind == components.KindAsset && r.Assets != nil && has &&
				strings.HasPrefix(comp.SourceFor(r.Platform), "pack:") && !r.Assets.Installed(alt) {
				// a pack asset not on this machine yet: waiting for its install
				if groupUnknown == "" {
					groupUnknown = alt
				}
				continue
			}
			if !ok || comp.Kind == components.KindAsset {
				groupMet = true
				break
			}
			on := !has || (d.Want == components.WantEnabled && d.Hold == nil &&
				(comp.Licence == "" || d.LicensedAt(r.Now())))
			if own := procs[alt]; len(own) > 0 {
				if on && phaseOfProcs(own) == components.PhaseRunning && gateOpen(own) {
					groupMet = true
					break
				}
				if on && groupUnknown == "" {
					groupUnknown = alt
				}
			} else if on {
				groupMet = true
				break
			}
			if !on && groupOff == "" {
				groupOff = alt
			}
		}
		if groupMet {
			continue
		}
		met = false
		if groupUnknown != "" {
			if unknown == "" {
				unknown = groupUnknown
			}
		} else if off == "" {
			off = groupOff
		}
	}
	if off != "" {
		unknown = ""
	}
	return met, unknown, off
}

func gateOpen(ps []procView) bool {
	for _, p := range ps {
		if p.Allowed {
			return true
		}
	}
	return false
}

// phaseOfProcs is the worst phase of a component's processes (store's rollup rank); "off"
// when none counts.
func phaseOfProcs(ps []procView) string {
	rows := []store.Status{}
	for _, p := range ps {
		if ph, _, ok := procPhase(p); ok {
			rows = append(rows, store.Status{Phase: ph})
		}
	}
	if len(rows) == 0 {
		return components.PhaseOff
	}
	return store.Rollup(rows).Phase
}

// reconciled is the components the supervisor has something of here, in Order().
func (r *Reconciler) reconciled(procs map[string][]procView) []*components.Component {
	var out []*components.Component
	for _, id := range r.Catalog.Order() {
		comp, _ := r.Catalog.Get(id)
		if len(procs[id]) > 0 || r.loopOf(id) != nil {
			out = append(out, comp)
		}
	}
	return out
}

func (r *Reconciler) loopOf(id string) *SupervisorLoop {
	for _, l := range r.Loops {
		if l.component == id {
			return l
		}
	}
	return nil
}

func byComponent(views []procView) map[string][]procView {
	m := map[string][]procView{}
	for _, v := range views {
		if v.Component != "" {
			m[v.Component] = append(m[v.Component], v)
		}
	}
	return m
}

// applyGates decides every component and opens and closes the process gates: closes in
// reverse order first (dependents stop first), then opens in order (requirements first).
// It returns the decisions and how many gates moved. loops, when not nil, gets each
// loop's component decision.
func (r *Reconciler) applyGates(views []procView, loops map[string]decision) (map[string]decision, int) {
	r.mu.Lock()
	desired := r.desired
	r.mu.Unlock()
	procs := byComponent(views)
	comps := r.reconciled(procs)
	decs := make(map[string]decision, len(comps))
	for _, comp := range comps {
		decs[comp.ID] = r.decide(comp, procs, r.loopOf(comp.ID), desired, gateOpen(procs[comp.ID]) || r.loopOn(comp.ID))
		if loops != nil {
			loops[comp.ID] = decs[comp.ID]
		}
	}
	if r.Off {
		return decs, 0
	}
	moved := 0
	for i := len(comps) - 1; i >= 0; i-- {
		id := comps[i].ID
		if decs[id].open || !comps[i].Switchable {
			continue
		}
		for _, p := range procs[id] {
			if p.Allowed || (p.Running && !p.Draining) {
				if err := r.table.Allow(p.Role, p.Instance, false); err != nil {
					r.logf("level=warn %s/%s: %v", p.Role, p.Instance, err)
				}
				moved++
			}
		}
	}
	for _, comp := range comps {
		if !decs[comp.ID].open {
			continue
		}
		for _, p := range procs[comp.ID] {
			if !p.Allowed {
				if err := r.table.Allow(p.Role, p.Instance, true); err != nil {
					r.logf("level=warn %s/%s: %v", p.Role, p.Instance, err)
				}
				moved++
			}
		}
	}
	return decs, moved
}

func (r *Reconciler) loopOn(id string) bool {
	l := r.loopOf(id)
	if l == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.loops[l.Name]
	return run != nil && !run.stopping
}

// pass is one reconcile: gates, loops, status. It returns how many things it changed.
func (r *Reconciler) pass(ctx context.Context) int {
	decs, moved := r.applyGates(r.table.procViews(), nil)
	moved += r.applyLoops(ctx, decs)
	r.mu.Lock()
	r.passes++
	r.mu.Unlock()
	r.report(ctx, decs)
	return moved
}

// applyLoops starts and stops the supervisor's loops as their components' gates say.
func (r *Reconciler) applyLoops(ctx context.Context, decs map[string]decision) int {
	moved := 0
	for _, l := range r.Loops {
		dec, ok := decs[l.component]
		open := ok && dec.open
		if l.component == "" {
			open = l.ConfigOn
		}
		r.mu.Lock()
		run := r.loops[l.Name]
		if run != nil {
			select {
			case <-run.done:
				if run.stopping {
					r.loops[l.Name], run = nil, nil
				} else {
					run.ended = true
				}
			default:
			}
		}
		switch {
		case open && run == nil && ctx.Err() == nil:
			lctx, cancel := context.WithCancel(ctx)
			run = &loopRun{cancel: cancel, done: make(chan struct{})}
			r.loops[l.Name] = run
			go r.runLoop(lctx, l, run.done)
			moved++
		case !open && run != nil && run.ended:
			r.loops[l.Name] = nil // turned off after it ended on its own: may start again
		case !open && run != nil && !run.stopping:
			run.stopping, run.stoppedAt = true, r.Now()
			run.cancel()
			r.logf("%s: its component %s is off: its loop is stopped", l.Name, l.component)
			moved++
		case run != nil && run.stopping && !run.warned:
			comp, _ := r.Catalog.Get(l.component)
			grace := time.Duration(components.DefaultStopGraceS) * time.Second
			if comp != nil && comp.StopGraceS > 0 {
				grace = time.Duration(comp.StopGraceS) * time.Second
			}
			if r.Now().Sub(run.stoppedAt) > grace {
				run.warned = true
				r.logf("level=warn %s has not returned %s after it was stopped (a backup dump ends first)", l.Name, grace)
			}
		}
		r.mu.Unlock()
	}
	return moved
}

func (r *Reconciler) runLoop(ctx context.Context, l *SupervisorLoop, done chan struct{}) {
	defer close(done)
	defer func() {
		if p := recover(); p != nil {
			r.logf("level=error %s panicked: %v\n%s", l.Name, p, debug.Stack())
		}
	}()
	l.Run(ctx)
}

// rows is the status of every component the supervisor has something of here.
func (r *Reconciler) rows(decs map[string]decision) []store.Status {
	views := r.table.procViews()
	procs := byComponent(views)
	var out []store.Status
	for _, comp := range r.reconciled(procs) {
		dec := decs[comp.ID]
		row := store.Status{ID: comp.ID, ObservedGeneration: dec.gen}
		own := procs[comp.ID]
		phase, reason := "", ""
		restarts := 0
		rows := []store.Status{}
		for _, p := range own {
			restarts += p.Restarts
			if ph, why, ok := procPhase(p); ok {
				rows = append(rows, store.Status{Phase: ph, Conditions: []store.Condition{{Reason: why}}})
			}
		}
		if l := r.loopOf(comp.ID); l != nil {
			rows = append(rows, store.Status{Phase: r.loopPhase(l)})
		}
		if len(rows) > 0 {
			worst := store.Rollup(rows)
			phase = worst.Phase
			if len(worst.Conditions) > 0 {
				reason = worst.Conditions[0].Reason
			}
		} else {
			phase = components.PhaseOff
		}
		if !dec.open && phase != components.PhaseDraining && comp.Switchable && !r.Off {
			phase = dec.phase
		}
		row.Phase = phase
		row.Conditions = append(row.Conditions, dec.conds...)
		ready := cond(components.CondReady, condFalse, reason, nil)
		if phase == components.PhaseRunning || phase == components.PhaseDegraded {
			ready.Status = condTrue
		}
		if !hasCond(row.Conditions, components.CondReady) {
			row.Conditions = append(row.Conditions, ready)
		}
		if restarts > 0 {
			row.Detail = map[string]any{"restarts": restarts}
		}
		out = append(out, row)
	}
	return out
}

func hasCond(cs []store.Condition, typ string) bool {
	for _, c := range cs {
		if c.Type == typ {
			return true
		}
	}
	return false
}

func (r *Reconciler) loopPhase(l *SupervisorLoop) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.loops[l.Name]
	switch {
	case run == nil:
		return components.PhaseOff
	case run.stopping:
		return components.PhaseDraining
	case run.ended:
		return components.PhaseOff
	}
	return components.PhaseRunning
}

// report writes the status rows when they changed since the last write (an idle machine
// writes nothing: N12). A failed write is tried again after statusRetry.
func (r *Reconciler) report(ctx context.Context, decs map[string]decision) {
	if r.Status == nil {
		return
	}
	rows := r.rows(decs)
	now := r.Now()
	r.mu.Lock()
	prev := r.written
	// a condition keeps the time it last changed
	for i := range rows {
		for j := range rows[i].Conditions {
			c := &rows[i].Conditions[j]
			c.Generation = rows[i].ObservedGeneration
			c.ChangedAt = now
			for _, p := range prev {
				if p.ID != rows[i].ID {
					continue
				}
				for _, pc := range p.Conditions {
					if pc.Type == c.Type && pc.Status == c.Status {
						c.ChangedAt = pc.ChangedAt
					}
				}
			}
		}
	}
	same := prev != nil && reflect.DeepEqual(prev, rows)
	wait := now.Before(r.nextTry)
	r.mu.Unlock()
	if same || wait {
		return
	}
	if err := r.Status.ReportStatus(ctx, r.Machine, StatusPart, rows); err != nil {
		r.mu.Lock()
		r.nextTry = now.Add(statusRetry)
		r.mu.Unlock()
		if ctx.Err() == nil {
			r.readFailed(ctx, fmt.Errorf("status: %w", err))
		}
		return
	}
	r.mu.Lock()
	r.written = rows
	r.nextTry = time.Time{}
	r.mu.Unlock()
}
