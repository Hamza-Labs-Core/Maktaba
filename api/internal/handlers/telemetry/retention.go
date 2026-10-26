package telemetry

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Retention windows (story 16.7): events 90 days, Web Vitals 30 days, by when
// the server received them.
const (
	EventsRetention = 90 * 24 * time.Hour
	VitalsRetention = 30 * 24 * time.Hour
)

// Sweeper deletes telemetry past its retention window: once at start, then
// every Interval (default a day). It mirrors watch.Reaper: a boot goroutine main.go
// starts on a background context, a failed pass logged and tried again next tick.
type Sweeper struct {
	DB       *sql.DB
	Interval time.Duration
	Logger   *slog.Logger
	now      func() time.Time
}

func (s *Sweeper) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

// RunOnce deletes what is past retention; the rows deleted from each table.
func (s *Sweeper) RunOnce(ctx context.Context) (events, vitals int64, err error) {
	now := s.clock()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM telemetry_events WHERE received_at < $1`, now.Add(-EventsRetention))
	if err != nil {
		return 0, 0, err
	}
	events, _ = res.RowsAffected()
	res, err = s.DB.ExecContext(ctx, `DELETE FROM telemetry_web_vitals WHERE received_at < $1`, now.Add(-VitalsRetention))
	if err != nil {
		return events, 0, err
	}
	vitals, _ = res.RowsAffected()
	return events, vitals, nil
}

// Run sweeps until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Sweeper) pass(ctx context.Context) {
	events, vitals, err := s.RunOnce(ctx)
	if s.Logger == nil {
		return
	}
	if err != nil {
		s.Logger.Warn("telemetry: retention sweep failed; will retry next tick",
			"err", err, "event", "telemetry_sweep_failed")
		return
	}
	if events > 0 || vitals > 0 {
		s.Logger.Info("telemetry: retention sweep",
			"events", events, "web_vitals", vitals, "event", "telemetry_sweep_pass")
	}
}
