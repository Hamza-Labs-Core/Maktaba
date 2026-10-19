package privacy

import (
	"context"
	"database/sql"
	"fmt"
)

// Eraser is the right-to-erasure step the account purge runs before it
// drops a user row (DataSubjectService in production).
type Eraser interface {
	Delete(ctx context.Context, userID string) (DeletionReport, error)
}

// AccountPurger drops accounts whose 30-day deletion hold has elapsed
// (story 25.5). The FK cascades on users cover sessions, OAuth links,
// servers and devices, but push_dispatch_log has no FK to users, so each
// account first goes through the GDPR erasure (story 30.2); otherwise
// its push logs would outlive it.
type AccountPurger struct {
	DB    *sql.DB
	Erase Eraser
}

// NewAccountPurger wires the purge to a pool and the erasure service.
func NewAccountPurger(db *sql.DB) *AccountPurger {
	return &AccountPurger{DB: db, Erase: NewDataSubjectService(db)}
}

// PurgeResult is what one purge pass did.
type PurgeResult struct {
	Purged   int
	PushRows int64
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
	return purgeAccounts(ctx, ids, p.Erase, func(ctx context.Context, id string) error {
		// re-check the hold so a deletion cancelled since the listing survives
		_, err := p.DB.ExecContext(ctx, `
            DELETE FROM users WHERE id = $1 AND id IN (
                SELECT user_id FROM account_deletions
                WHERE cancelled_at IS NULL AND purge_after < now()
            )`, id)
		return err
	}), nil
}

// purgeAccounts runs the erasure, then the user delete, for each id. The
// user row goes only once its push logs are gone, so a failed erasure is
// retried on the next pass instead of being orphaned.
func purgeAccounts(ctx context.Context, ids []string, erase Eraser, deleteUser func(context.Context, string) error) PurgeResult {
	res := PurgeResult{Failed: map[string]error{}}
	for _, id := range ids {
		rep, err := erase.Delete(ctx, id)
		if err != nil {
			res.Failed[id] = fmt.Errorf("erase: %w", err)
			continue
		}
		res.PushRows += rep.PushRows
		if err := deleteUser(ctx, id); err != nil {
			res.Failed[id] = fmt.Errorf("delete user: %w", err)
			continue
		}
		res.Purged++
	}
	return res
}
