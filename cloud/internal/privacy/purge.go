package privacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AccountPurger deletes accounts whose 30-day deletion hold has elapsed
// (story 25.5), including records without a foreign key to the account.
type AccountPurger struct {
	DB *sql.DB
	// ForgetMail takes the account's address out of the mail outbox, before the account goes
	// (nil: there is none to take it out of).
	ForgetMail func(ctx context.Context, email string) error
}

func NewAccountPurger(db *sql.DB) *AccountPurger { return &AccountPurger{DB: db} }

// PurgeResult is what one purge pass did.
type PurgeResult struct {
	Purged    int
	PushRows  int64
	AuditRows int64
	AbuseRows int64
	// Failed holds the accounts left for the next pass, keyed by user id.
	Failed map[string]error
}

// DueAccounts lists the users whose deletion hold has elapsed and was not
// cancelled.
func (p *AccountPurger) DueAccounts(ctx context.Context) ([]string, error) {
	rows, err := p.DB.QueryContext(ctx, `
        SELECT user_id FROM account_deletions
        WHERE cancelled_at IS NULL AND purge_after < now()
        ORDER BY purge_after`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PurgeDue erases and deletes every due account. A failure on one
// account leaves it for the next pass and does not stop the others.
func (p *AccountPurger) PurgeDue(ctx context.Context) (PurgeResult, error) {
	ids, err := p.DueAccounts(ctx)
	if err != nil {
		return PurgeResult{}, fmt.Errorf("list due accounts: %w", err)
	}
	res := PurgeResult{Failed: map[string]error{}}
	for _, id := range ids {
		purged, err := p.purgeAccount(ctx, id)
		if err != nil {
			res.Failed[id] = err
			continue
		}
		res.Purged += purged.Purged
		res.PushRows += purged.PushRows
		res.AuditRows += purged.AuditRows
		res.AbuseRows += purged.AbuseRows
	}
	return res, nil
}

const dueAccount = `SELECT u.email FROM users u JOIN account_deletions d ON d.user_id = u.id
	WHERE u.id = $1 AND d.cancelled_at IS NULL AND d.purge_after < now()`

func (p *AccountPurger) purgeAccount(ctx context.Context, id string) (PurgeResult, error) {
	var email string
	// The mail outbox uses its own database operations.
	if p.ForgetMail != nil {
		err := p.DB.QueryRowContext(ctx, dueAccount, id).Scan(&email)
		if errors.Is(err, sql.ErrNoRows) {
			return PurgeResult{}, nil
		}
		if err != nil {
			return PurgeResult{}, fmt.Errorf("read email for the mail outbox: %w", err)
		}
		if err := p.ForgetMail(ctx, email); err != nil {
			return PurgeResult{}, fmt.Errorf("forget its mail: %w", err)
		}
	}

	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return PurgeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// A cancellation after the listing wins unless this transaction has
	// already locked the hold. Keep the user and erasure in the same commit.
	err = tx.QueryRowContext(ctx, dueAccount+" FOR UPDATE OF u, d", id).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return PurgeResult{}, nil
	}
	if err != nil {
		return PurgeResult{}, fmt.Errorf("lock due account: %w", err)
	}

	var res PurgeResult
	res.PushRows, err = deletePushLogs(ctx, tx, id)
	if err != nil {
		return PurgeResult{}, fmt.Errorf("erase push logs: %w", err)
	}
	res.AuditRows, res.AbuseRows, err = eraseSecurityRecords(ctx, tx, id, email)
	if err != nil {
		return PurgeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return PurgeResult{}, fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PurgeResult{}, err
	}
	res.Purged = 1
	return res, nil
}

func eraseSecurityRecords(ctx context.Context, tx *sql.Tx, id, email string) (auditRows, abuseRows int64, err error) {
	// A row's detail can contain arbitrary identifiers, so delete account-
	// linked rows in full. Staff actors and IP-only subjects can be shared
	// across accounts and are governed by the age limits.
	audit, err := tx.ExecContext(ctx, `DELETE FROM audit_log WHERE id IN (
		SELECT id FROM audit_log WHERE actor_kind = 'user' AND actor_id = $1::uuid
		UNION ALL
		SELECT id FROM audit_log WHERE actor_kind = 'user' AND actor_id IS NULL AND lower(actor_email) = lower($2)
		UNION ALL
		SELECT id FROM audit_log WHERE target_kind = 'user' AND target_id = $1::uuid::text
		UNION ALL
		SELECT a.id FROM servers s JOIN audit_log a ON a.target_kind = 'server' AND a.target_id = s.id::text
		WHERE s.owner_user_id = $1::uuid
	)`, id, email)
	if err != nil {
		return 0, 0, fmt.Errorf("erase audit records: %w", err)
	}
	signals, err := tx.ExecContext(ctx, `DELETE FROM abuse_signals WHERE id IN (
		SELECT id FROM abuse_signals WHERE subject_kind = 'user' AND subject = $1::uuid::text
		UNION ALL
		SELECT a.id FROM servers s JOIN abuse_signals a ON a.subject_kind = 'server' AND a.subject = s.id::text
		WHERE s.owner_user_id = $1::uuid
	)`, id)
	if err != nil {
		return 0, 0, fmt.Errorf("erase abuse signals: %w", err)
	}
	auditRows, err = audit.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	abuseRows, err = signals.RowsAffected()
	return auditRows, abuseRows, err
}
