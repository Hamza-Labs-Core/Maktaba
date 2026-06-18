package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// Reaper is the background loop that keeps watch_sessions honest and
// bounded. It has two cadences in one goroutine (Story 29.1 + 29.6):
//
//   - every minute: mark active sessions with no recent heartbeat as
//     'interrupted' so "currently watching" never lies;
//   - once per ~24h of ticks: purge sessions older than the retention
//     window so the table cannot grow without limit.
//
// It mirrors main.go's runPairingSweep convention (a boot goroutine bound
// to a background context, structured logging, no runtime data in log
// message strings).
type Reaper struct {
	DB *sql.DB

	// StaleTimeout: an active session unheard-from for longer is reaped.
	// Zero ⇒ DefaultStaleTimeout.
	StaleTimeout time.Duration

	// RetentionDays bounds the table; rows started before now-Retention
	// are purged. <=0 disables the purge. When DB-backed app_settings
	// carries `analytics.retention_days`, that overrides this at purge
	// time; this field is the env/default fallback.
	RetentionDays int

	// ReapInterval is the interrupted-session cadence (default 1m).
	ReapInterval time.Duration

	// PurgeEveryTicks runs the retention purge once per this many reap
	// ticks (default 1440 ≈ daily at a 1m cadence).
	PurgeEveryTicks int

	Logger *slog.Logger
	now    func() time.Time
}

func (rp *Reaper) clock() time.Time {
	if rp.now != nil {
		return rp.now()
	}
	return time.Now().UTC()
}

func (rp *Reaper) reapInterval() time.Duration {
	if rp.ReapInterval > 0 {
		return rp.ReapInterval
	}
	return time.Minute
}

func (rp *Reaper) purgeEveryTicks() int {
	if rp.PurgeEveryTicks > 0 {
		return rp.PurgeEveryTicks
	}
	return 1440
}

// RunOnce performs a single reap pass and, when purge is true, a single
// retention purge. Returns the rows touched by each. Exposed for tests.
func (rp *Reaper) RunOnce(ctx context.Context, purge bool) (reaped, purged int64, err error) {
	r := &repo{db: rp.DB}
	now := rp.clock()

	reaped, err = r.reapStale(ctx, now.Add(-staleTimeoutOr(rp.StaleTimeout)))
	if err != nil {
		return reaped, 0, err
	}
	if purge {
		days := rp.retentionDays(ctx)
		if days > 0 {
			cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
			purged, err = r.purgeOlderThan(ctx, cutoff)
			if err != nil {
				return reaped, purged, err
			}
		}
	}
	return reaped, purged, nil
}

// Run drives the loop until ctx is cancelled.
func (rp *Reaper) Run(ctx context.Context) {
	t := time.NewTicker(rp.reapInterval())
	defer t.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			ticks++
			purge := ticks%rp.purgeEveryTicks() == 0
			reaped, purged, err := rp.RunOnce(ctx, purge)
			if err != nil {
				if rp.Logger != nil {
					rp.Logger.Warn("watch: reaper pass failed; will retry next tick",
						"err", err, "event", "watch_reaper_failed")
				}
				continue
			}
			if rp.Logger != nil && (reaped > 0 || purged > 0) {
				rp.Logger.Info("watch: reaper pass",
					"reaped", reaped, "purged", purged, "event", "watch_reaper_pass")
			}
		}
	}
}

// retentionDays resolves the active retention window: the app_settings
// override if present and parseable, else the configured fallback.
func (rp *Reaper) retentionDays(ctx context.Context) int {
	var raw []byte
	err := rp.DB.QueryRowContext(ctx,
		`SELECT value FROM app_settings WHERE key = 'analytics.retention_days'`).Scan(&raw)
	if err == nil {
		var n int
		if json.Unmarshal(raw, &n) == nil {
			return n
		}
	}
	return rp.RetentionDays
}
