//go:build integration

package privacy

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

func TestSecurityRetentionRemovesExpiredUnlockedRows(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	auditCutoff := now.AddDate(0, 0, -AuditRetentionDays)
	abuseCutoff := now.AddDate(0, 0, -AbuseRetentionDays)
	execPrivacySQL(t, pool, `INSERT INTO audit_log (action, created_at) VALUES
		('locked', $1), ('expired', $1), ('boundary', $2), ('recent', $3)`,
		auditCutoff.Add(-time.Second), auditCutoff, abuseCutoff)
	execPrivacySQL(t, pool, `INSERT INTO abuse_signals (subject_kind, subject, kind, created_at)
		SELECT 'ip', '192.0.2.1', 'expired', $1::timestamptz FROM generate_series(1, $2::int)`,
		abuseCutoff.Add(-time.Second), securityBatchSize+1)
	execPrivacySQL(t, pool, `INSERT INTO abuse_signals (subject_kind, subject, kind, created_at) VALUES
		('ip', '192.0.2.1', 'boundary', $1), ('ip', '192.0.2.1', 'recent', $2)`, abuseCutoff, now)
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM audit_log WHERE action = 'locked' FOR UPDATE`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	r := NewSecurityRetention(pool.DB)
	logger := slog.New(slog.DiscardHandler)
	r.sweep(ctx, logger, now)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM audit_log WHERE id = $1`, id)
	countPrivacyRows(t, pool, 2, `SELECT count(*) FROM audit_log WHERE action IN ('boundary', 'recent')`)
	countPrivacyRows(t, pool, 0, `SELECT count(*) FROM audit_log WHERE action = 'expired'`)
	countPrivacyRows(t, pool, 0, `SELECT count(*) FROM abuse_signals WHERE kind = 'expired'`)
	countPrivacyRows(t, pool, 2, `SELECT count(*) FROM abuse_signals WHERE kind IN ('boundary', 'recent')`)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	r.sweep(ctx, logger, now)
	countPrivacyRows(t, pool, 0, `SELECT count(*) FROM audit_log WHERE id = $1`, id)
}
