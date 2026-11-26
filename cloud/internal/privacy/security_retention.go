package privacy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	AuditRetentionDays   = 90
	AbuseRetentionDays   = 30
	securityBatchSize    = 1000
	securitySweepBatches = 10
	securitySweepEvery   = 15 * time.Minute
	securitySweepTimeout = 30 * time.Second
)

type securitySweep struct {
	table   string
	days    int
	deleted atomic.Uint64
	failed  atomic.Uint64
}

// SecurityRetention purges expired security records on the worker leader.
// Each batch commits on its own and skips rows another transaction holds.
type SecurityRetention struct {
	db     execer
	sweeps []*securitySweep
}

func NewSecurityRetention(db execer) *SecurityRetention {
	return &SecurityRetention{db: db, sweeps: []*securitySweep{
		{table: "audit_log", days: AuditRetentionDays},
		{table: "abuse_signals", days: AbuseRetentionDays},
	}}
}

func (r *SecurityRetention) Run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(securitySweepEvery)
	defer ticker.Stop()
	for ctx.Err() == nil {
		r.sweep(ctx, logger, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *SecurityRetention) sweep(ctx context.Context, logger *slog.Logger, now time.Time) {
	for _, sweep := range r.sweeps {
		if ctx.Err() != nil {
			return
		}
		bounded, cancel := context.WithTimeout(ctx, securitySweepTimeout)
		n, err := r.purge(bounded, sweep, now)
		cancel()
		sweep.deleted.Add(uint64(n))
		if n > 0 {
			logger.Info("worker: expired security records purged", "table", sweep.table, "rows", n)
		}
		if err != nil && ctx.Err() == nil {
			sweep.failed.Add(1)
			logger.Warn("worker: security retention sweep", "table", sweep.table, "err", err)
		}
	}
}

func (r *SecurityRetention) purge(ctx context.Context, sweep *securitySweep, now time.Time) (int64, error) {
	// The table name comes only from NewSecurityRetention.
	q := `DELETE FROM ` + sweep.table + ` WHERE id IN (
		SELECT id FROM ` + sweep.table + ` WHERE created_at < $1
		ORDER BY created_at LIMIT $2 FOR UPDATE SKIP LOCKED
	)`
	cutoff := now.UTC().AddDate(0, 0, -sweep.days)
	var total int64
	for range securitySweepBatches {
		res, err := r.db.ExecContext(ctx, q, cutoff, securityBatchSize)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < securityBatchSize {
			break
		}
	}
	return total, nil
}

func (r *SecurityRetention) WriteMetrics(w io.Writer) error {
	for _, metric := range []struct {
		name string
		help string
		load func(*securitySweep) uint64
	}{
		{"maktaba_cloud_security_retention_deleted_total", "Expired security records deleted.", func(s *securitySweep) uint64 { return s.deleted.Load() }},
		{"maktaba_cloud_security_retention_errors_total", "Failed security retention passes.", func(s *securitySweep) uint64 { return s.failed.Load() }},
	} {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", metric.name, metric.help, metric.name); err != nil {
			return err
		}
		for _, sweep := range r.sweeps {
			if _, err := fmt.Fprintf(w, "%s{table=%q} %d\n", metric.name, sweep.table, metric.load(sweep)); err != nil {
				return err
			}
		}
	}
	return nil
}
