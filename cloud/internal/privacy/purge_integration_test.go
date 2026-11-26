//go:build integration

package privacy

import (
	"context"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
)

func TestPurgeDueRemovesAccountRecords(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	gone := newPurgeUser(t, pool, "gone@example.test")
	kept := newPurgeUser(t, pool, "kept@example.test")
	var server string
	if err := pool.DB.QueryRow(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash)
		VALUES ($1, 'Test Home', 'purge-home', 'test') RETURNING id`, gone).Scan(&server); err != nil {
		t.Fatal(err)
	}
	execPrivacySQL(t, pool, `UPDATE account_deletions SET cancelled_at = now() WHERE user_id = $1`, kept)
	execPrivacySQL(t, pool, `INSERT INTO audit_log (actor_kind, actor_id, actor_email, action, target_kind, target_id, ip, detail) VALUES
		('user', $1::uuid, 'old-address@example.test', 'test.actor', NULL, NULL, '192.0.2.1', '{"device":"private"}'),
		('user', NULL, 'GONE@example.test', 'test.email', NULL, NULL, '192.0.2.1', '{}'),
		('staff', NULL, 'staff@example.test', 'test.target', 'user', $1::uuid::text, NULL, '{}'),
		('staff', NULL, 'staff@example.test', 'test.home', 'server', $3, NULL, '{}'),
		('staff', NULL, 'gone@example.test', 'test.staff_kept', NULL, NULL, NULL, '{}'),
		('user', $2::uuid, 'gone@example.test', 'test.other_actor_kept', NULL, NULL, '192.0.2.1', '{}'),
		('user', NULL, NULL, 'test.other_target_kept', 'user', $2::uuid::text, '192.0.2.1', '{}')`, gone, kept, server)
	execPrivacySQL(t, pool, `INSERT INTO abuse_signals (subject_kind, subject, kind, detail) VALUES
		('user', $1, 'test', '{"ip":"192.0.2.1"}'),
		('server', $3, 'test', NULL),
		('user', $2, 'test', NULL),
		('ip', '192.0.2.1', 'test', NULL)`, gone, kept, server)

	purger := NewAccountPurger(pool.DB)
	fail := errors.New("the outbox is away")
	purger.ForgetMail = func(_ context.Context, email string) error {
		if email != "gone@example.test" {
			t.Errorf("forgot unexpected address %q", email)
		}
		return fail
	}
	res, err := purger.PurgeDue(ctx)
	if err != nil || res.Purged != 0 || !errors.Is(res.Failed[gone], fail) {
		t.Fatalf("purge during mail failure: %+v %v", res, err)
	}
	countPrivacyRows(t, pool, 7, `SELECT count(*) FROM audit_log`)

	// Make the last SQL write fail. The erasure must roll back along with it.
	fail = nil
	execPrivacySQL(t, pool, `CREATE FUNCTION reject_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'test deletion failure'; END; $$`)
	execPrivacySQL(t, pool, `CREATE TRIGGER reject_delete BEFORE DELETE ON users FOR EACH ROW EXECUTE FUNCTION reject_delete()`)
	res, err = purger.PurgeDue(ctx)
	if err != nil || res.Purged != 0 || res.Failed[gone] == nil {
		t.Fatalf("purge during delete failure: %+v %v", res, err)
	}
	countPrivacyRows(t, pool, 2, `SELECT count(*) FROM users`)
	countPrivacyRows(t, pool, 2, `SELECT count(*) FROM push_dispatch_log`)
	countPrivacyRows(t, pool, 7, `SELECT count(*) FROM audit_log`)
	countPrivacyRows(t, pool, 4, `SELECT count(*) FROM abuse_signals`)
	execPrivacySQL(t, pool, `DROP TRIGGER reject_delete ON users`)

	res, err = purger.PurgeDue(ctx)
	if err != nil || res.Purged != 1 || len(res.Failed) != 0 || res.PushRows != 1 || res.AuditRows != 4 || res.AbuseRows != 2 {
		t.Fatalf("purge: %+v %v", res, err)
	}
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM users WHERE id = $1`, kept)
	countPrivacyRows(t, pool, 0, `SELECT count(*) FROM users WHERE id = $1`, gone)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM push_dispatch_log WHERE user_id = $1`, kept)
	countPrivacyRows(t, pool, 0, `SELECT count(*) FROM push_dispatch_log WHERE user_id = $1`, gone)
	countPrivacyRows(t, pool, 3, `SELECT count(*) FROM audit_log WHERE action LIKE '%_kept'`)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM abuse_signals WHERE subject_kind = 'user' AND subject = $1`, kept)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM abuse_signals WHERE subject_kind = 'ip' AND subject = '192.0.2.1'`)
	res, err = purger.PurgeDue(ctx)
	if err != nil || res.Purged != 0 || len(res.Failed) != 0 {
		t.Fatalf("repeat purge: %+v %v", res, err)
	}
}

func TestPurgeDueRechecksCancellation(t *testing.T) {
	pool := dbtest.Migrated(t)
	id := newPurgeUser(t, pool, "cancel@example.test")
	execPrivacySQL(t, pool, `INSERT INTO audit_log (actor_id, action) VALUES ($1, 'test.keep')`, id)
	purger := NewAccountPurger(pool.DB)
	purger.ForgetMail = func(ctx context.Context, _ string) error {
		_, err := pool.DB.ExecContext(ctx, `UPDATE account_deletions SET cancelled_at = now() WHERE user_id = $1`, id)
		return err
	}
	res, err := purger.PurgeDue(context.Background())
	if err != nil || res.Purged != 0 || len(res.Failed) != 0 {
		t.Fatalf("purge after cancellation: %+v %v", res, err)
	}
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM users WHERE id = $1`, id)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM push_dispatch_log WHERE user_id = $1`, id)
	countPrivacyRows(t, pool, 1, `SELECT count(*) FROM audit_log WHERE actor_id = $1`, id)
}

func newPurgeUser(t *testing.T, pool *db.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.DB.QueryRow(`INSERT INTO users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execPrivacySQL(t, pool, `INSERT INTO account_deletions (user_id, requested_at, purge_after)
		VALUES ($1, now() - INTERVAL '31 days', now() - INTERVAL '1 day')`, id)
	execPrivacySQL(t, pool, `INSERT INTO push_dispatch_log (user_id, platform, status) VALUES ($1, 'ios', 'sent')`, id)
	return id
}

func execPrivacySQL(t *testing.T, pool *db.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.DB.Exec(sql, args...); err != nil {
		t.Fatal(err)
	}
}

func countPrivacyRows(t *testing.T, pool *db.Pool, want int, sql string, args ...any) {
	t.Helper()
	var n int
	if err := pool.DB.QueryRow(sql, args...).Scan(&n); err != nil || n != want {
		t.Fatalf("%s: count %d, want %d (%v)", sql, n, want, err)
	}
}
