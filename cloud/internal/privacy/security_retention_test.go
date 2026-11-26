package privacy

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type execFunc func(context.Context, string, ...any) (sql.Result, error)

func (f execFunc) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return f(ctx, query, args...)
}

func TestSecuritySweepBoundsEachTableAndReportsPartialFailures(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.FixedZone("test", -5*60*60))
	audit, abuse := 0, 0
	fail := errors.New("query failed")
	r := NewSecurityRetention(execFunc(func(ctx context.Context, q string, args ...any) (sql.Result, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("purge has no deadline")
		}
		if args[1] != securityBatchSize {
			t.Errorf("batch size = %v", args[1])
		}
		if strings.Contains(q, "DELETE FROM audit_log") {
			audit++
			if want := now.UTC().AddDate(0, 0, -AuditRetentionDays); args[0] != want {
				t.Errorf("audit cutoff = %v, want %v", args[0], want)
			}
			if audit == 2 {
				return nil, fail
			}
		} else if strings.Contains(q, "DELETE FROM abuse_signals") {
			abuse++
			if want := now.UTC().AddDate(0, 0, -AbuseRetentionDays); args[0] != want {
				t.Errorf("abuse cutoff = %v, want %v", args[0], want)
			}
		} else {
			t.Fatalf("unexpected query: %s", q)
		}
		return driver.RowsAffected(securityBatchSize), nil
	}))
	r.sweep(context.Background(), slog.New(slog.DiscardHandler), now)
	if audit != 2 || abuse != securitySweepBatches {
		t.Fatalf("query counts: audit=%d abuse=%d", audit, abuse)
	}
	var metrics strings.Builder
	if err := r.WriteMetrics(&metrics); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"maktaba_cloud_security_retention_deleted_total{table=\"audit_log\"} 1000\n",
		"maktaba_cloud_security_retention_errors_total{table=\"audit_log\"} 1\n",
		"maktaba_cloud_security_retention_deleted_total{table=\"abuse_signals\"} 10000\n",
		"maktaba_cloud_security_retention_errors_total{table=\"abuse_signals\"} 0\n",
	} {
		if !strings.Contains(metrics.String(), want) {
			t.Errorf("missing metric %q in %s", want, metrics.String())
		}
	}
}

func TestSecuritySweepStopsAfterAShortBatch(t *testing.T) {
	calls := 0
	r := NewSecurityRetention(execFunc(func(context.Context, string, ...any) (sql.Result, error) {
		calls++
		return driver.RowsAffected(1), nil
	}))
	r.sweep(context.Background(), slog.New(slog.DiscardHandler), time.Now())
	if calls != 2 {
		t.Fatalf("got %d queries, want one batch per table", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Run(ctx, slog.New(slog.DiscardHandler))
	if calls != 2 {
		t.Fatalf("cancelled worker ran another sweep, got %d queries", calls)
	}
}
