package session

import (
	"context"
	"sync/atomic"
	"time"
)

// ReaperConfig governs the reaper loop. IdleAfter is the
// last_segment_at horizon; the reaper closes any session whose last
// touch is older than this. Interval is the loop period.
type ReaperConfig struct {
	IdleAfter time.Duration // 90 s default per Story 8.9 AC-3
	Interval  time.Duration // 30 s default
	OnReap    func(*Row)
}

// Reaper sweeps idle sessions on Interval and closes them.
type Reaper struct {
	store     Store
	cfg       ReaperConfig
	now       func() time.Time
	reapedTot atomic.Uint64
}

// NewReaper builds a reaper. Use Run to start the loop.
func NewReaper(store Store, cfg ReaperConfig) *Reaper {
	if cfg.IdleAfter <= 0 {
		cfg.IdleAfter = 90 * time.Second
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	return &Reaper{store: store, cfg: cfg, now: time.Now}
}

// SetClock replaces the wall clock — for tests.
func (r *Reaper) SetClock(now func() time.Time) { r.now = now }

// Run blocks until ctx is cancelled, sweeping every Interval.
func (r *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(r.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.Sweep(ctx)
		}
	}
}

// Sweep is one pass — exposed so tests can drive the reaper without
// waiting on the ticker.
func (r *Reaper) Sweep(ctx context.Context) error {
	cutoff := r.now().UTC().Add(-r.cfg.IdleAfter)
	idle, err := r.store.ListIdle(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, row := range idle {
		if row.Transcoder != nil {
			_ = row.Transcoder.Stop(ctx)
		}
		_ = r.store.Close(ctx, row.ID, ReasonIdle, r.now().UTC())
		r.reapedTot.Add(1)
		if r.cfg.OnReap != nil {
			r.cfg.OnReap(row)
		}
	}
	return nil
}

// Reaped returns the count of sessions reaped — exposed via
// `sessions_reaped_idle_total` (Story 8.9 AC-3).
func (r *Reaper) Reaped() uint64 { return r.reapedTot.Load() }
