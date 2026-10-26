package session

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// ReaperConfig governs the reaper loop. IdleAfter is the
// last_segment_at horizon; the reaper closes any session whose last
// touch is older than this. Interval is the loop period.
type ReaperConfig struct {
	IdleAfter time.Duration // 90 s default per Story 8.9 AC-3
	// ActiveIdleAfter, when longer, is the horizon while the session's
	// transcode runs: a player in a background tab or on a locked phone
	// fetches nothing for a while, and coming back finds its transcode
	// going on.
	ActiveIdleAfter time.Duration
	Interval        time.Duration // 30 s default
	OnReap          func(*Row)
	// Logger receives each reaped session and each failed sweep (nil: slog's default).
	Logger *slog.Logger
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
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
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
			if err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
				r.cfg.Logger.Warn("vod session sweep failed", "err", err.Error(), "event", "vod_reap_failed")
			}
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
		if row.Transcoder != nil && row.Transcoder.Active() && r.cfg.ActiveIdleAfter > r.cfg.IdleAfter &&
			row.LastSegmentAt.After(r.now().UTC().Add(-r.cfg.ActiveIdleAfter)) {
			continue
		}
		active := row.Transcoder != nil && row.Transcoder.Active()
		if row.Transcoder != nil {
			if err := row.Transcoder.Stop(ctx); err != nil {
				r.cfg.Logger.Warn("vod session's transcode not stopped", "session_id", row.ID.String(),
					"err", err.Error(), "event", "vod_reap_stop_failed")
			}
		}
		if err := r.store.Close(ctx, row.ID, ReasonIdle, r.now().UTC()); err != nil {
			r.cfg.Logger.Warn("vod session not closed", "session_id", row.ID.String(),
				"err", err.Error(), "event", "vod_session_close_failed")
		}
		r.cfg.Logger.Info("vod session closed: nothing fetched from it", "session_id", row.ID.String(),
			"video_id", row.VideoID.String(), "reason", string(ReasonIdle),
			"idle_ms", r.now().UTC().Sub(row.LastSegmentAt).Milliseconds(), "transcode_active", active,
			"event", "vod_session_reaped")
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
