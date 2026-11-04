// Package runner starts and stops a Go process's background loops by catalog component
// (Epic 51, story 51.05). The API and streaming each make one Runner. A loop is declared
// by its catalog name (`api:tvchange_controller`) where it used to be started by hand.
// The runner runs it in its own goroutine with its own context, stops it when its
// component is turned off, restarts it with backoff when it crashes, and reports each
// component's phase in component_status (part = the host).
//
// Level based, like every runner of the epic: the desired state comes from a
// store.Watcher (a notification, or a revision poll every 30 s), and each look compares
// it with what runs. A component with no desired row runs as it does today: the seed
// (51.03) writes the rows. With the kill switch (MAKTABA_COMPONENTS_RECONCILE=off) every
// declared loop runs, and the status says reconcile_off.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// EnvReconcile is the kill switch the supervisor sets from server.toml's
// `[components] reconcile` (51.04): "off" runs every declared loop and acts on nothing.
const EnvReconcile = "MAKTABA_COMPONENTS_RECONCILE"

// ReconcileOff reports whether the environment turns reconciling off.
func ReconcileOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(EnvReconcile)), "off")
}

// CondReconciled is the condition a status carries while the kill switch is on (false,
// reason reconcile_off).
const CondReconciled = "Reconciled"

// The restart policy: the supervisor's numbers, so the Components page reads them the
// same way (1 s doubling to 60 s; 5 crashes in 10 minutes is a crash loop, tried again
// after 5 minutes).
const (
	BackoffMin  = time.Second
	BackoffMax  = time.Minute
	CrashWindow = 10 * time.Minute
	CrashLimit  = 5
	CrashRetry  = 5 * time.Minute
)

// LoopFunc is one background loop. It must return soon after ctx ends. nil while it is
// still wanted means it was a one-shot: it is not run again until its component is
// turned off and on. An error (or a panic) is a crash.
type LoopFunc func(ctx context.Context) error

// Func is a loop that returns nothing.
func Func(fn func(ctx context.Context)) LoopFunc {
	return func(ctx context.Context) error {
		fn(ctx)
		return nil
	}
}

// Bound is fn, also stopped when parent ends (a loop that used to run on a context its
// owner cancels at close).
func Bound(parent context.Context, fn LoopFunc) LoopFunc {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(parent, cancel)
		defer stop()
		return fn(ctx)
	}
}

// Store is what a runner reads and writes: *store.Store is one.
type Store interface {
	store.Source
	ReportStatus(ctx context.Context, machine, part string, rows []store.Status) error
}

// Source is where Run reads the desired state and writes the status. A zero Source
// runs the declared loops and reports nothing (no database).
type Source struct {
	Store    Store
	Listener store.Listener // nil: poll only (SQLite); Run closes it when it returns
}

// Clock is time for the runner (tests give a fake one).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Options make a Runner. Host and Catalog decide which loops may be declared.
type Options struct {
	Host    string // api | streaming
	Machine string // store.Home (default) or this machine's cluster_nodes.id
	Catalog *components.Catalog
	Logger  *slog.Logger
	// NoReconcile is the kill switch (ReconcileOff()): every declared loop runs.
	NoReconcile bool
	// Context is the parent of every loop (nil: context.Background()).
	Context context.Context
	Clock   Clock         // nil: the real one
	Poll    time.Duration // 0: store.DefaultPoll
}

// ReasonLicenceMissing is the reason of a component whose licence flag the home's
// licence does not grant (story 51.19): it waits, and its want is kept.
const ReasonLicenceMissing = "licence_missing"

// gate is one component's switch as the desired state decides it.
type gate struct {
	on         bool
	phase      string // when off: off, stopped or waiting
	reason     string // held, dependency_off, licence_missing
	missing    []string
	generation int64
	// licence is the component's licence flag ("" for a free one); licensed whether the
	// published licence grants it now (true while nothing is published).
	licence  string
	licensed bool
	// published: the API published a licence on the row; until is its end (zero: none)
	published bool
	until     time.Time
}

// Loop states, as status detail.loops says them.
const (
	stateRunning  = "running"
	stateStarting = "starting" // waiting out a restart backoff
	stateDraining = "draining"
	stateOff      = "off"
	stateFailed   = "failed"
)

type loop struct {
	name, key, comp string
	grace           time.Duration
	fn              LoopFunc

	alive    bool // fn runs in a goroutine
	cancel   context.CancelFunc
	stopping bool // its context was cancelled by a stop
	stuck    bool // still running past its grace
	waiting  bool // a restart is scheduled
	exited   bool // returned nil while wanted
	// restarting: an admin's restart stopped it, so it starts again once it returns
	restarting bool
	state      string
	reason     string
	restarts   int
	crashes    []time.Time
	epoch      int // moves on every start and every reset: an older timer does nothing
}

// Runner runs one process's declared loops.
type Runner struct {
	host, machine string
	cat           *components.Catalog
	log           *slog.Logger
	noReconcile   bool
	clock         Clock
	poll          time.Duration
	root          context.Context
	cancelRoot    context.CancelFunc

	gates atomic.Pointer[map[string]gate]
	wg    sync.WaitGroup // the loops' goroutines

	mu      sync.Mutex
	loops   []*loop
	desired map[string]store.Desired // nil: not read yet
	closed  bool
	kick    chan struct{}
	phases  map[string]string
	phaseAt map[string]time.Time
	sent    string // the last report that was written
	// licWake is when a published licence next ends (zero: none): the gates are computed
	// again then, so a component stops on time whether or not the API answers.
	licWake time.Time
	// unknown are the desired rows' ids this release's catalog does not know (a newer
	// release wrote them before a rollback), each logged once: they are ignored, and the
	// desired state is read again on every change and every poll.
	unknown map[string]bool
	// served are components this process serves with no loop (Serve): each has a row.
	served []string
}

// New is a runner for o.Host. Its gates start on (the database is not read yet): a loop
// declared before Run starts at once, as it did before the runner.
func New(o Options) *Runner {
	if o.Catalog == nil {
		o.Catalog = components.Default()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Machine == "" {
		o.Machine = store.Home
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Poll <= 0 {
		o.Poll = store.DefaultPoll
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	root, cancel := context.WithCancel(o.Context)
	r := &Runner{host: o.Host, machine: o.Machine, cat: o.Catalog, log: o.Logger, noReconcile: o.NoReconcile,
		clock: o.Clock, poll: o.Poll, root: root, cancelRoot: cancel, kick: make(chan struct{}, 1),
		phases: map[string]string{}, phaseAt: map[string]time.Time{}}
	r.mu.Lock()
	r.computeGates()
	r.mu.Unlock()
	return r
}

// Declare runs fn as the loop name of this host while its component is on. It panics
// when host:name is not in the catalog. On a nil Runner (tests, a process with no
// runner) fn just runs, as a plain goroutine would.
func (r *Runner) Declare(name string, fn LoopFunc) {
	if r == nil {
		go func() {
			defer func() {
				if v := recover(); v != nil {
					logPanic(slog.Default(), name, v)
				}
			}()
			_ = fn(context.Background())
		}()
		return
	}
	comp, ok := r.cat.ByLoop(r.host, name)
	if !ok {
		panic(fmt.Sprintf("componentrunner: loop %s:%s is not in the catalog: add it to a component's loops in shared/components/catalog.toml", r.host, name))
	}
	grace := time.Duration(comp.StopGraceS) * time.Second
	if grace <= 0 {
		grace = components.DefaultStopGraceS * time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	key, n := name, 1
	for slices.ContainsFunc(r.loops, func(l *loop) bool { return l.key == key }) {
		n++
		key = name + "#" + strconv.Itoa(n)
	}
	r.loops = append(r.loops, &loop{name: name, key: key, comp: comp.ID, grace: grace, fn: fn, state: stateOff})
	r.apply()
	r.notify()
}

// On reports whether component id is on here (lock-free). Off: not wanted, held, or a
// requirement is off. An id the catalog does not know is off; a nil Runner says on.
func (r *Runner) On(id string) bool {
	if r == nil {
		return true
	}
	g, ok := (*r.gates.Load())[id]
	return ok && g.on
}

// Restart cancels each running loop of component id and starts it again once it
// returns (an admin's restart, story 51.08); a loop of id that does not run while its
// gate is open (a crash's backoff, a one-shot that ended) starts at once. It answers how
// many loops it restarted or started: 0 when id has no loop here, or it is off (not
// wanted, held, a requirement off).
func (r *Runner) Restart(id string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !(*r.gates.Load())[id].on {
		return 0
	}
	n := 0
	for _, l := range r.loops {
		if l.comp != id {
			continue
		}
		switch {
		case l.alive && !l.stopping:
			l.restarting = true
			r.log.Info("components: a loop stops", "host", r.host, "loop", l.name, "component", l.comp,
				"reason", "restart", "event", "components_loop_stopped")
			r.stop(l)
			n++
		case !l.alive:
			l.epoch++ // a pending backoff timer does nothing now
			l.waiting, l.exited, l.crashes = false, false, nil
			r.log.Info("components: a loop starts", "host", r.host, "loop", l.name, "component", l.comp,
				"reason", "restart", "event", "components_loop_started")
			r.start(l)
			n++
		}
	}
	r.notify()
	return n
}

// Hosts are the hosts that run c's loops, as the catalog names them (api, streaming,
// pipeline, supervisor), in its order.
func Hosts(c *components.Component) []string {
	var out []string
	for _, l := range c.Loops {
		host, _, _ := strings.Cut(l, ":")
		if !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	return out
}

// Host is the host this runner runs loops for (api, streaming, ...).
func (r *Runner) Host() string {
	if r == nil {
		return ""
	}
	return r.host
}

// Serve has this runner report a status row for component id, which this process serves
// in its request handlers with no loop of its own (the API's ext.cloudflare_stream: every
// Cloudflare call is the API's). Its phase follows its gate: running while it is on,
// else off, stopped or waiting as its desired state says.
func (r *Runner) Serve(id string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.served, id) {
		r.served = append(r.served, id)
	}
	r.notify()
}

// Machine is the machine whose desired state r follows (store.Home, or a joined
// machine's cluster_nodes id).
func (r *Runner) Machine() string { return r.machine }

// Declared are the loop names declared so far, sorted.
func (r *Runner) Declared() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, l := range r.loops {
		if !slices.Contains(out, l.name) {
			out = append(out, l.name)
		}
	}
	slices.Sort(out)
	return out
}

// Apply takes a new desired state (what the watcher hands Run; tests call it directly)
// and starts or stops loops to match.
func (r *Runner) Apply(desired map[string]store.Desired) {
	if desired == nil {
		desired = map[string]store.Desired{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range desired {
		if _, ok := r.cat.Get(id); ok || r.unknown[id] {
			continue
		}
		if r.unknown == nil {
			r.unknown = map[string]bool{}
		}
		r.unknown[id] = true
		r.log.Warn("components: a desired row names a component this release does not know; it is ignored",
			"host", r.host, "component", id, "event", "component_unknown")
	}
	r.desired = desired
	r.computeGates()
	r.apply()
	r.notify()
}

// Run reconciles until ctx ends, then stops every loop and waits for them all to
// return. With a zero Source it only waits.
func (r *Runner) Run(ctx context.Context, src Source) {
	defer r.shutdown()
	if src.Listener != nil {
		defer func() { _ = src.Listener.Close() }()
	}
	if src.Store != nil {
		var rep repeat
		w := &store.Watcher{Source: src.Store, Machine: r.machine, Listener: src.Listener, Poll: r.poll,
			After: r.clock.After, OnError: func(err error) {
				r.log.Log(context.Background(), rep.level(err), "components: reading the desired state failed",
					"host", r.host, "err", err.Error(), "event", "components_desired_read_failed")
			}}
		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		defer func() { cancel(); <-done }()
		go func() {
			defer close(done)
			_ = w.Run(wctx, func(d map[string]store.Desired, _ int64) { r.Apply(d) })
		}()
	}
	r.notify()
	var rep repeat
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-retry:
			retry = nil
		}
		if src.Store == nil {
			continue
		}
		err := r.report(ctx, src.Store)
		if err != nil && ctx.Err() == nil {
			r.log.Log(context.Background(), rep.level(err), "components: writing the status failed",
				"host", r.host, "err", err.Error(), "event", "components_status_write_failed")
			if retry == nil {
				retry = r.clock.After(r.poll)
			}
		} else if err == nil {
			rep.level(nil)
		}
	}
}

// shutdown stops every loop and waits for them.
func (r *Runner) shutdown() {
	r.mu.Lock()
	r.closed = true
	for _, l := range r.loops {
		if l.alive && !l.stopping {
			l.stopping = true
			l.cancel()
		}
	}
	r.mu.Unlock()
	r.cancelRoot()
	r.wg.Wait()
}

func (r *Runner) notify() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// computeGates sets every component's gate from the desired state (r.mu held), and
// wakes the runner when a licence's grace ends.
func (r *Runner) computeGates() {
	out := map[string]gate{}
	now := r.clock.Now()
	var wake time.Time
	for id, g := range GatesAt(r.cat, r.desired, r.noReconcile, now) {
		out[id] = gate{on: g.On, phase: g.Phase, reason: g.Reason, missing: g.Missing, generation: g.Generation,
			licence: g.Licence, licensed: g.Licensed, published: g.Published, until: g.Until}
		if g.Licensed && !g.Until.IsZero() && (wake.IsZero() || g.Until.Before(wake)) {
			wake = g.Until
		}
	}
	r.gates.Store(&out)
	if !r.noReconcile && !wake.Equal(r.licWake) {
		r.licWake = wake
		if !wake.IsZero() {
			r.wakeAt(wake, now)
		}
	}
}

// wakeAt computes the gates again at t, when a licence's grace ends (r.mu held). A newer
// wake replaces it.
func (r *Runner) wakeAt(t, now time.Time) {
	ch := r.clock.After(t.Sub(now))
	go func() {
		select {
		case <-ch:
		case <-r.root.Done():
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.licWake.Equal(t) || r.closed {
			return
		}
		r.licWake = time.Time{}
		r.computeGates()
		r.apply()
		r.notify()
	}()
}

// Licensed reports whether the home's licence grants component id now, as its published
// desired row says (lock-free), and whether that is known: not on a nil Runner, with the
// kill switch, before the desired state is read, or while nothing is published.
func (r *Runner) Licensed(id string) (licensed, known bool) {
	if r == nil || r.noReconcile {
		return true, false
	}
	g, ok := (*r.gates.Load())[id]
	if !ok || !g.published {
		return true, false
	}
	return g.licensed && (g.until.IsZero() || r.clock.Now().Before(g.until)), true
}

// Gate is one component's switch as the desired state alone decides it: the rule every
// Go runner applies, and the API's capabilities and component_off answer (story 51.09).
type Gate struct {
	On         bool
	Phase      string   // when off: off, stopped or waiting
	Reason     string   // held, dependency_off, licence_missing
	Missing    []string // dependency_off: the ids of the unmet groups
	Generation int64
	// Licence is the component's licence flag ("" for a free one); Licensed whether the
	// published licence grants it at the time asked (true while nothing is published).
	Licence  string
	Licensed bool
	// Published: the API published a licence on the row; Until is its end (zero: none)
	Published bool
	Until     time.Time
}

// Gates is GatesAt now.
func Gates(cat *components.Catalog, desired map[string]store.Desired, noReconcile bool) map[string]Gate {
	return GatesAt(cat, desired, noReconcile, time.Now())
}

// GatesAt is every catalog component's gate for desired at now (nil desired: nothing read
// yet, all on). A core component, a component with no row, and every component under the
// kill switch (noReconcile) are on. An asset has no run, so it is on unless absent. Off:
// want is not enabled, a hold, a licence the home lacks (story 51.19), or a requirement is
// off. Requirements come first in the catalog's order, so each sees its requirements' gates.
func GatesAt(cat *components.Catalog, desired map[string]store.Desired, noReconcile bool, now time.Time) map[string]Gate {
	out := make(map[string]Gate, len(cat.Order()))
	for _, id := range cat.Order() {
		c, _ := cat.Get(id)
		d, has := desired[id]
		g := Gate{On: true, Generation: d.Generation, Licence: c.Licence, Licensed: true}
		if c.Licence != "" && has {
			g.Licensed = d.LicensedAt(now)
			if l := d.Licence; l != nil {
				g.Published = true
				if l.Until != nil {
					g.Until = *l.Until
				}
			}
		}
		switch {
		case !c.Switchable || noReconcile || !has:
		case c.Kind == components.KindAsset:
			// an asset has no run: on disk (installed or enabled) it is met, absent it is off
			if d.Want == components.WantAbsent {
				g.On, g.Phase = false, components.PhaseOff
			}
		case d.Want != components.WantEnabled:
			g.On, g.Phase = false, components.PhaseOff
		case d.Hold != nil:
			g.On, g.Phase, g.Reason = false, components.PhaseStopped, "held"
		case !g.Licensed:
			g.On, g.Phase, g.Reason = false, components.PhaseWaiting, ReasonLicenceMissing
		default:
			for _, req := range cat.Requires(id) {
				if !slices.ContainsFunc(req.AnyOf, func(x string) bool { return out[x].On }) {
					g.Missing = append(g.Missing, req.AnyOf...)
				}
			}
			if len(g.Missing) > 0 {
				g.On, g.Phase, g.Reason = false, components.PhaseWaiting, "dependency_off"
			}
		}
		out[id] = g
	}
	return out
}

// apply starts what should run and stops what should not (r.mu held).
func (r *Runner) apply() {
	gates := *r.gates.Load()
	for _, l := range r.loops {
		g := gates[l.comp]
		// an ended parent starts nothing: each start would return at once and start again
		want := g.on && !r.closed && r.root.Err() == nil
		switch {
		case want && !l.alive && !l.waiting && !l.exited:
			// a start from off is logged once; a restart after a crash is logged by ended
			if l.state == stateOff {
				reason := "enabled"
				if l.restarting {
					reason = "restart"
				}
				r.log.Info("components: a loop starts", "host", r.host, "loop", l.name, "component", l.comp,
					"reason", reason, "event", "components_loop_started")
			}
			l.restarting = false
			r.start(l)
		case !want && l.alive && !l.stopping:
			reason := stopReason(g)
			if r.closed {
				reason = "shutdown"
			}
			r.log.Info("components: a loop stops", "host", r.host, "loop", l.name, "component", l.comp,
				"reason", reason, "event", "components_loop_stopped")
			r.stop(l)
		case !want && !l.alive:
			// off: forget the crashes, a pending restart and a one-shot's exit
			if l.waiting || l.exited || l.state != stateOff {
				l.epoch++
			}
			l.waiting, l.exited, l.crashes = false, false, nil
			l.state, l.reason = stateOff, ""
		}
	}
}

// stopReason is why a gate is closed: its reason (held, dependency_off,
// licence_missing), or its phase (off) when it has none.
func stopReason(g gate) string {
	if g.reason != "" {
		return g.reason
	}
	if g.phase != "" {
		return g.phase
	}
	return components.PhaseOff
}

func (r *Runner) start(l *loop) {
	ctx, cancel := context.WithCancel(r.root)
	l.epoch++
	l.alive, l.cancel, l.stopping, l.stuck = true, cancel, false, false
	l.state, l.reason = stateRunning, ""
	r.wg.Add(1)
	go r.run(l, ctx)
}

func (r *Runner) run(l *loop, ctx context.Context) {
	defer r.wg.Done()
	var err error
	func() {
		defer func() {
			if v := recover(); v != nil {
				logPanic(r.log, r.host+":"+l.name, v)
				err = fmt.Errorf("panic: %v", v)
			}
		}()
		err = l.fn(ctx)
	}()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended(l, err)
	r.apply()
	r.notify()
}

// ended records how a loop's run ended (r.mu held).
func (r *Runner) ended(l *loop, err error) {
	stopped, stuck := l.stopping, l.stuck
	l.alive, l.stopping, l.stuck = false, false, false
	l.cancel()
	switch {
	case stopped || r.closed || r.root.Err() != nil:
		l.state, l.reason = stateOff, ""
		if stuck {
			r.log.Info("components: a loop returned after its stop grace", "host", r.host, "loop", l.name,
				"component", l.comp, "event", "components_loop_stopped_late")
		}
	case err == nil:
		l.state, l.reason, l.exited = stateOff, "", true
	default:
		now := r.clock.Now()
		l.crashes = slices.DeleteFunc(l.crashes, func(t time.Time) bool { return now.Sub(t) >= CrashWindow })
		l.crashes = append(l.crashes, now)
		l.restarts++
		l.waiting = true
		if len(l.crashes) >= CrashLimit {
			l.state, l.reason = stateFailed, "crash_loop"
			r.log.Warn("components: a loop keeps crashing; trying again later", "host", r.host, "loop", l.name,
				"component", l.comp, "crashes", len(l.crashes), "retry_in", CrashRetry.String(), "err", err.Error(),
				"event", "components_loop_crash_loop")
			r.after(l, CrashRetry, func() { l.crashes = nil })
			return
		}
		d := min(BackoffMin<<(len(l.crashes)-1), BackoffMax)
		l.state, l.reason = stateStarting, ""
		r.log.Warn("components: a loop crashed; restarting", "host", r.host, "loop", l.name, "component", l.comp,
			"in", d.String(), "err", err.Error(), "event", "components_loop_crashed")
		r.after(l, d, nil)
	}
}

// after ends l's wait after d, unless l moved on meanwhile (r.mu held).
func (r *Runner) after(l *loop, d time.Duration, then func()) {
	epoch := l.epoch
	ch := r.clock.After(d)
	go func() {
		select {
		case <-ch:
		case <-r.root.Done():
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if l.epoch != epoch || !l.waiting {
			return
		}
		l.waiting = false
		if then != nil {
			then()
		}
		r.apply()
		r.notify()
	}()
}

// stop cancels l's context; past its grace it is reported failed, stop_timeout, and it
// is never started again before it returns (r.mu held).
func (r *Runner) stop(l *loop) {
	l.stopping = true
	l.cancel()
	l.state, l.reason = stateDraining, ""
	epoch := l.epoch
	ch := r.clock.After(l.grace)
	go func() {
		select {
		case <-ch:
		case <-r.root.Done():
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if l.epoch != epoch || !l.alive || !l.stopping || l.stuck {
			return
		}
		l.stuck = true
		l.state, l.reason = stateFailed, "stop_timeout"
		r.log.Warn("components: a loop did not stop within its grace", "host", r.host, "loop", l.name,
			"component", l.comp, "grace", l.grace.String(), "event", "components_loop_stop_timeout")
		r.notify()
	}()
}

// rank orders the loop states: the worst names the component's phase.
func rank(state string) int {
	switch state {
	case stateFailed:
		return 4
	case stateDraining:
		return 3
	case stateStarting:
		return 2
	case stateRunning:
		return 1
	}
	return 0
}

// Statuses are the status rows this runner reports now: one per component with a
// declared loop or served (Serve), in catalog order.
func (r *Runner) Statuses() []store.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses()
}

func (r *Runner) statuses() []store.Status {
	gates := *r.gates.Load()
	byComp := map[string][]*loop{}
	for _, l := range r.loops {
		byComp[l.comp] = append(byComp[l.comp], l)
	}
	now := r.clock.Now()
	var out []store.Status
	for _, id := range r.cat.Order() {
		ls := byComp[id]
		served := len(ls) == 0 && slices.Contains(r.served, id)
		if len(ls) == 0 && !served {
			continue
		}
		g := gates[id]
		var worst *loop
		exited := !served
		if served {
			// no loop: it runs while its gate is open
			worst = &loop{state: stateOff}
			if g.on {
				worst.state = stateRunning
			}
		} else {
			worst = ls[0]
		}
		detail := map[string]any{}
		for _, l := range ls {
			if rank(l.state) > rank(worst.state) {
				worst = l
			}
			exited = exited && l.exited
			d := map[string]any{"state": l.state}
			if l.reason != "" {
				d["reason"] = l.reason
			}
			if l.restarts > 0 {
				d["restarts"] = l.restarts
			}
			if l.exited {
				d["exited"] = true
			}
			detail[l.key] = d
		}
		phase, reason := components.PhaseOff, ""
		switch worst.state {
		case stateFailed:
			phase, reason = components.PhaseFailed, worst.reason
		case stateDraining:
			phase = components.PhaseDraining
		case stateStarting:
			phase = components.PhaseStarting
		case stateRunning:
			phase = components.PhaseRunning
		default:
			if !g.on {
				phase, reason = g.phase, g.reason
			}
		}
		if r.phases[id] != phase {
			r.phases[id], r.phaseAt[id] = phase, now
		}
		at := r.phaseAt[id]
		var conds []store.Condition
		if r.noReconcile {
			conds = append(conds, store.Condition{Type: CondReconciled, Status: "false", Reason: "reconcile_off",
				ChangedAt: at, Generation: g.generation})
		}
		req := store.Condition{Type: components.CondRequirementsMet, Status: "true", ChangedAt: at, Generation: g.generation}
		if g.reason == "dependency_off" {
			req.Status, req.Reason, req.Params = "false", "dependency_off", map[string]any{"components": g.missing}
		}
		ready := store.Condition{Type: components.CondReady, Status: "true", ChangedAt: at, Generation: g.generation}
		if phase != components.PhaseRunning {
			ready.Status, ready.Reason = "false", reason
		}
		if g.licence != "" && !r.noReconcile {
			lic := store.Condition{Type: components.CondLicensed, Status: "true", ChangedAt: at, Generation: g.generation,
				Params: map[string]any{"feature": g.licence}}
			if !g.licensed {
				lic.Status, lic.Reason = "false", ReasonLicenceMissing
			}
			conds = append(conds, lic)
		}
		conds = append(conds, req, ready)
		det := map[string]any{}
		if !served {
			det["loops"] = detail
		}
		if exited && phase == components.PhaseOff && g.on {
			det["exited"] = true
		}
		out = append(out, store.Status{ID: id, Phase: phase, Conditions: conds, ObservedGeneration: g.generation,
			Detail: det})
	}
	return out
}

// signature is a report without its times: the same signature is not written again.
func signature(rows []store.Status) string {
	type cond struct {
		T, S, R string
		P       map[string]any
		G       int64
	}
	type row struct {
		ID, Phase string
		C         []cond
		G         int64
		D         map[string]any
	}
	out := make([]row, 0, len(rows))
	for _, s := range rows {
		x := row{ID: s.ID, Phase: s.Phase, G: s.ObservedGeneration, D: s.Detail}
		for _, c := range s.Conditions {
			x.C = append(x.C, cond{c.Type, c.Status, c.Reason, c.Params, c.Generation})
		}
		out = append(out, x)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// report writes the status when it changed since the last write.
func (r *Runner) report(ctx context.Context, st Store) error {
	r.mu.Lock()
	rows := r.statuses()
	sig := signature(rows)
	same := sig == r.sent
	r.mu.Unlock()
	if same {
		return nil
	}
	if err := st.ReportStatus(ctx, r.machine, r.host, rows); err != nil {
		return err
	}
	r.mu.Lock()
	r.sent = sig
	r.mu.Unlock()
	return nil
}

// logPanic writes the goroutine_panic line the API's safego writes.
func logPanic(logger *slog.Logger, name string, v any) {
	logger.Error("goroutine panic", "goroutine", name, "panic", fmt.Sprint(v),
		"stack", string(debug.Stack()), "event", "goroutine_panic")
}

// repeat logs an error at Warn the first time it is seen, at Debug while it repeats.
type repeat struct{ last string }

func (p *repeat) level(err error) slog.Level {
	if err == nil {
		p.last = ""
		return slog.LevelDebug
	}
	if s := err.Error(); s != p.last {
		p.last = s
		return slog.LevelWarn
	}
	return slog.LevelDebug
}
