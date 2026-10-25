// Package watchdog is the streaming process's self-health (Story 22.9).
//
// A goroutine beats every Interval: it runs each registered probe with a
// timeout and, when every hard probe answered, advances the heartbeat.
// The liveness check fails once the heartbeat is older than Stale — the
// beat loop itself is stuck, or a hard probe (the live-channel engine
// lock) has not answered for that long — so the supervisor kills and
// restarts a wedged process. Soft probes (the database) only feed the
// readiness check: a database outage makes streaming not ready, it does
// not get it killed, because a restart would not bring the database
// back.
package watchdog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	health "github.com/Hamza-Labs-Core/Maktaba/shared/health/go"
)

// Defaults.
const (
	DefaultInterval     = 5 * time.Second
	DefaultStale        = 45 * time.Second
	DefaultProbeTimeout = 10 * time.Second
)

// ProbeFunc answers nil when the thing it checks is responsive. It must
// honour ctx.
type ProbeFunc func(ctx context.Context) error

type probe struct {
	name string
	fn   ProbeFunc
	hard bool
}

type result struct {
	at  time.Time
	err error
}

// Watchdog tracks the heartbeat. Use New.
type Watchdog struct {
	Interval     time.Duration
	Stale        time.Duration
	ProbeTimeout time.Duration
	// Logger receives each probe's failure and recovery (a transition, not
	// every beat; nil: slog's default): a process the supervisor kills for a
	// stale heartbeat has the reason in the log before.
	Logger *slog.Logger

	now func() time.Time

	mu       sync.Mutex
	probes   []probe
	lastBeat time.Time
	lastErr  error // why the last beat did not advance
	results  map[string]result
}

// New returns a watchdog whose heartbeat starts now, so the process is
// live until a whole Stale window passes without a beat.
func New() *Watchdog {
	w := &Watchdog{
		Interval: DefaultInterval, Stale: DefaultStale, ProbeTimeout: DefaultProbeTimeout,
		now: time.Now, results: map[string]result{},
	}
	w.lastBeat = w.now()
	return w
}

// Watch adds a hard probe: the heartbeat only advances while it answers.
func (w *Watchdog) Watch(name string, fn ProbeFunc) { w.add(probe{name: name, fn: fn, hard: true}) }

// Observe adds a soft probe, reported by ReadyCheck only.
func (w *Watchdog) Observe(name string, fn ProbeFunc) { w.add(probe{name: name, fn: fn}) }

func (w *Watchdog) add(p probe) {
	w.mu.Lock()
	w.probes = append(w.probes, p)
	w.mu.Unlock()
}

// Run beats until ctx ends.
func (w *Watchdog) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		w.Beat(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Beat runs every probe once and advances the heartbeat when all hard
// probes answered.
func (w *Watchdog) Beat(ctx context.Context) {
	w.mu.Lock()
	probes := append([]probe(nil), w.probes...)
	w.mu.Unlock()
	var hardErr error
	for _, p := range probes {
		pctx, cancel := context.WithTimeout(ctx, w.ProbeTimeout)
		err := p.fn(pctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		prev, seen := w.results[p.name]
		w.results[p.name] = result{at: w.now(), err: err}
		w.mu.Unlock()
		w.logTransition(p, prev.err, seen, err)
		if err != nil && p.hard && hardErr == nil {
			hardErr = fmt.Errorf("%s: %w", p.name, err)
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if hardErr == nil {
		w.lastBeat = w.now()
		w.lastErr = nil
	} else {
		w.lastErr = hardErr
	}
}

// logTransition logs a probe that started failing (or fails differently) and
// one that answers again.
func (w *Watchdog) logTransition(p probe, prev error, seen bool, err error) {
	l := w.Logger
	if l == nil {
		l = slog.Default()
	}
	switch {
	case err != nil && (prev == nil || prev.Error() != err.Error()):
		l.Warn("watchdog probe failed", "probe", p.name, "hard", p.hard, "err", err.Error(),
			"event", "watchdog_probe_failed")
	case err == nil && seen && prev != nil:
		l.Info("watchdog probe answers again", "probe", p.name, "event", "watchdog_probe_recovered")
	}
}

// LiveCheck fails when the heartbeat is older than Stale.
func (w *Watchdog) LiveCheck() health.Check {
	return health.CheckFunc{N: "watchdog", F: func(context.Context) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		age := w.now().Sub(w.lastBeat)
		if age <= w.Stale {
			return nil
		}
		if w.lastErr != nil {
			return fmt.Errorf("heartbeat stalled for %s (%v)", age.Round(time.Second), w.lastErr)
		}
		return fmt.Errorf("heartbeat stalled for %s", age.Round(time.Second))
	}}
}

// ReadyCheck reports the last result of a probe (hard or soft) as a
// readiness check; a probe that has not run yet passes.
func (w *Watchdog) ReadyCheck(name string) health.Check {
	return health.CheckFunc{N: name, F: func(context.Context) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.results[name].err
	}}
}
