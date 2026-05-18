// SQL-backed PairingStore (Epic 15 Story 15.6).
//
// The in-memory MemoryPairingStore loses every code on restart and
// cannot be shared across API replicas. This store persists tickets in
// the `pairing_tickets` table (migration slot 0055) so a code minted on
// replica A can be redeemed on replica B and survives a process bounce.
//
// Atomicity: Consume is a single `UPDATE ... WHERE consumed_at IS NULL
// RETURNING` so two concurrent redemptions of the same code can never
// both win — the database row lock serialises them and the second sees
// zero rows affected (→ ErrCodeConsumed). This is the one-time-use
// guarantee the spec requires, enforced at the storage layer rather
// than in racy application code.
package discovery

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SQLPairingStore persists pairing tickets in Postgres.
type SQLPairingStore struct {
	DB *sql.DB

	// now is the clock; tests freeze it. Production leaves it nil and
	// the store uses time.Now().UTC() so expiry is evaluated in Go
	// (consistent with MemoryPairingStore) rather than relying on the
	// DB clock.
	now func() time.Time
}

// NewSQLPairingStore returns a store bound to db.
func NewSQLPairingStore(db *sql.DB) *SQLPairingStore {
	return &SQLPairingStore{DB: db}
}

// SetNow overrides the clock (tests only).
func (s *SQLPairingStore) SetNow(fn func() time.Time) { s.now = fn }

func (s *SQLPairingStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// Put inserts (or replaces) a ticket. A re-issued code for the same
// PRIMARY KEY overwrites the prior unconsumed row — matches the
// in-memory store's map-replace semantics.
func (s *SQLPairingStore) Put(ctx context.Context, t PairingTicket) error {
	const q = `
		INSERT INTO pairing_tickets (code, user_id, issued_at, expires_at, consumed_at)
		VALUES ($1, $2, $3, $4, NULL)
		ON CONFLICT (code) DO UPDATE
		SET user_id    = EXCLUDED.user_id,
		    issued_at   = EXCLUDED.issued_at,
		    expires_at  = EXCLUDED.expires_at,
		    consumed_at = NULL`
	_, err := s.DB.ExecContext(ctx, q, t.Code, t.UserID, t.IssuedAt, t.ExpiresAt)
	return err
}

// Get fetches a ticket without consuming it. Expiry is evaluated in Go
// against the store clock so behaviour matches MemoryPairingStore.
func (s *SQLPairingStore) Get(ctx context.Context, code string) (PairingTicket, error) {
	const q = `
		SELECT code, user_id, issued_at, expires_at, consumed_at
		FROM pairing_tickets WHERE code = $1`
	var (
		t   PairingTicket
		con sql.NullTime
	)
	err := s.DB.QueryRowContext(ctx, q, code).
		Scan(&t.Code, &t.UserID, &t.IssuedAt, &t.ExpiresAt, &con)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingTicket{}, ErrCodeNotFound
	}
	if err != nil {
		return PairingTicket{}, err
	}
	if con.Valid {
		v := con.Time
		t.ConsumedAt = &v
	}
	if s.clock().After(t.ExpiresAt) {
		return PairingTicket{}, ErrCodeExpired
	}
	return t, nil
}

// Consume atomically flips consumed_at and returns the prior ticket.
//
// The UPDATE only matches rows that are still unconsumed; a row that
// exists but is already consumed (or expired) yields zero updated rows,
// and a follow-up SELECT disambiguates not-found / expired / consumed
// so the handler can map each to its precise problem+json status.
func (s *SQLPairingStore) Consume(ctx context.Context, code string) (PairingTicket, error) {
	now := s.clock()
	const upd = `
		UPDATE pairing_tickets
		SET consumed_at = $2
		WHERE code = $1 AND consumed_at IS NULL AND expires_at > $3
		RETURNING code, user_id, issued_at, expires_at`
	var t PairingTicket
	err := s.DB.QueryRowContext(ctx, upd, code, now, now).
		Scan(&t.Code, &t.UserID, &t.IssuedAt, &t.ExpiresAt)
	if err == nil {
		v := now
		t.ConsumedAt = &v
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PairingTicket{}, err
	}

	// Zero rows updated — figure out why so the caller gets the right
	// status code (404 vs 409-expired vs 409-consumed).
	const sel = `
		SELECT expires_at, consumed_at FROM pairing_tickets WHERE code = $1`
	var (
		exp time.Time
		con sql.NullTime
	)
	serr := s.DB.QueryRowContext(ctx, sel, code).Scan(&exp, &con)
	if errors.Is(serr, sql.ErrNoRows) {
		return PairingTicket{}, ErrCodeNotFound
	}
	if serr != nil {
		return PairingTicket{}, serr
	}
	if con.Valid {
		return PairingTicket{}, ErrCodeConsumed
	}
	if now.After(exp) {
		return PairingTicket{}, ErrCodeExpired
	}
	// Row is unconsumed and unexpired but the UPDATE still missed it —
	// a concurrent consumer won the race between our UPDATE and SELECT.
	// Treat as already-consumed (the one-time guarantee held).
	return PairingTicket{}, ErrCodeConsumed
}

// Sweep hard-deletes tickets whose expiry is older than `before`.
// Story 15.6 calls for a periodic reaper; this is the query it runs.
// Returns the number of rows removed.
func (s *SQLPairingStore) Sweep(ctx context.Context, before time.Time) (int64, error) {
	const q = `DELETE FROM pairing_tickets WHERE expires_at < $1`
	res, err := s.DB.ExecContext(ctx, q, before)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
