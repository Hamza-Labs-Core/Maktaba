package metrics

import (
	"context"
	"log/slog"
	"time"
)

// GaugeSource supplies the live connection gauges each collector tick.
// Backed by relay.Registry.Len() in production.
type GaugeSource func() (servers, tunnels int)

// HourlyPurger deletes hourly rows past the retention window. The privacy
// package provides the implementation (90-day GDPR purge, Story 30.2);
// the Runner calls it on a daily tick. Kept as a func to avoid a metrics
// → privacy import.
type HourlyPurger func(ctx context.Context, now time.Time) (int64, error)

// Runner drives the collector: it samples gauges and flushes per-minute
// raw rows on FlushInterval, rolls raw → hourly and purges raw each hour,
// and purges hourly past retention daily. All errors are logged and the
// loop continues; analytics never takes down the relay.
type Runner struct {
	Collector     *Collector
	Store         *Store
	Gauges        GaugeSource
	PurgeHourly   HourlyPurger
	FlushInterval time.Duration
	Logger        *slog.Logger

	now func() time.Time
}

// NewRunner wires a Runner with sensible defaults (60 s flush).
func NewRunner(c *Collector, s *Store, g GaugeSource, purge HourlyPurger, logger *slog.Logger) *Runner {
	return &Runner{
		Collector:     c,
		Store:         s,
		Gauges:        g,
		PurgeHourly:   purge,
		FlushInterval: 60 * time.Second,
		Logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// Run blocks until ctx is cancelled, then performs a final flush. Intended
// to be started in its own goroutine.
func (r *Runner) Run(ctx context.Context) {
	flush := time.NewTicker(r.FlushInterval)
	defer flush.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	for {
		select {
		case <-ctx.Done():
			r.flushOnce(context.Background())
			return
		case <-flush.C:
			r.flushOnce(ctx)
		case <-hourly.C:
			r.rollupOnce(ctx)
		case <-daily.C:
			r.purgeHourlyOnce(ctx)
		}
	}
}

func (r *Runner) flushOnce(ctx context.Context) {
	if r.Gauges != nil {
		servers, tunnels := r.Gauges()
		r.Collector.ObserveConnections(servers, tunnels)
	}
	rows := r.Collector.Snapshot()
	if err := r.Store.FlushRaw(ctx, rows); err != nil {
		r.log("relay metrics flush failed", err)
	}
}

func (r *Runner) rollupOnce(ctx context.Context) {
	now := r.now()
	if err := r.Store.Rollup(ctx, now); err != nil {
		r.log("relay metrics rollup failed", err)
		return
	}
	if _, err := r.Store.PurgeRaw(ctx, now); err != nil {
		r.log("relay metrics raw purge failed", err)
	}
}

func (r *Runner) purgeHourlyOnce(ctx context.Context) {
	if r.PurgeHourly == nil {
		return
	}
	if _, err := r.PurgeHourly(ctx, r.now()); err != nil {
		r.log("relay metrics hourly purge failed", err)
	}
}

func (r *Runner) log(msg string, err error) {
	if r.Logger != nil {
		r.Logger.Error(msg, "err", err)
	}
}
