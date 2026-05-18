package idempotency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// PostgresStore is a durable Store backed by the `idempotency_keys`
// table (migration slot 0059). Unlike MemoryStore, entries survive a
// process restart and are shared across API replicas — which is the
// whole point of an idempotency key, defeated by an in-memory map.
//
// Schema and access mirror the established `*sql.DB` store pattern in
// this codebase (see api/internal/auth/sessions and
// api/internal/auth/authz/acl.go): a thin struct wrapping the pool,
// parameterised queries, and ON CONFLICT for race-safe concurrent
// writes.
type PostgresStore struct {
	db dbExecutor
	// log records the swallowed-as-miss breadcrumb on a non-ErrNoRows
	// Lookup failure (see Lookup). Defaults to slog.Default() so the
	// store is usable without explicit plumbing, mirroring the
	// slog.Default() fallback in internal/middleware's guardedWriter.
	log *slog.Logger
}

// dbExecutor is the slice of *sql.DB PostgresStore needs. Pulling it
// behind an interface lets the unit tier exercise the SQL construction
// and the ON CONFLICT race-safety contract without a live Postgres
// (the real-DB assertions live in api/migrate_integration_test.go,
// build tag `integration`). *sql.DB satisfies this directly.
type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) rowScanner
}

// rowScanner is the *sql.Row surface scanSession-style helpers need.
type rowScanner interface {
	Scan(dest ...any) error
}

// sqlDBAdapter adapts a *sql.DB to dbExecutor. QueryRowContext on
// *sql.DB returns *sql.Row, which already has Scan; the adapter just
// widens the static type to the rowScanner interface.
type sqlDBAdapter struct{ *sql.DB }

func (a sqlDBAdapter) QueryRowContext(ctx context.Context, q string, args ...any) rowScanner {
	return a.DB.QueryRowContext(ctx, q, args...)
}

// NewPostgresStore returns a Store backed by db. The caller is
// responsible for periodically calling SweepExpired (see
// api/main.go's idempotency sweep goroutine) so the table doesn't grow
// without bound.
func NewPostgresStore(db dbExecutor) *PostgresStore {
	return &PostgresStore{db: db, log: slog.Default()}
}

// NewPostgresStoreDB is the production constructor: it wraps a real
// *sql.DB. Split from NewPostgresStore so callers in main.go don't
// have to know about the dbExecutor seam. logger receives the
// swallowed-as-miss breadcrumb (see Lookup); nil falls back to
// slog.Default() so callers may omit it.
func NewPostgresStoreDB(db *sql.DB, logger *slog.Logger) *PostgresStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &PostgresStore{db: sqlDBAdapter{db}, log: logger}
}

const (
	insertSQL = `INSERT INTO idempotency_keys
	    (composite_key, user_id, idem_key, request_hash, status, body, headers, created_at)
	    VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	    ON CONFLICT (composite_key) DO NOTHING`

	lookupSQL = `SELECT request_hash, status, body, headers, created_at
	    FROM idempotency_keys WHERE composite_key = $1`

	sweepSQL = `DELETE FROM idempotency_keys WHERE created_at < $1`
)

func isInsert(q string) bool { return strings.Contains(q, "INSERT INTO idempotency_keys") }
func isSweep(q string) bool  { return strings.Contains(q, "DELETE FROM idempotency_keys") }

// Lookup returns a previously cached record, if any. The bool is false
// when no row exists (or on a query error — the caller's contract is
// "no replay available → process from scratch", and re-processing is
// safe-by-design for an idempotent handler). A non-ErrNoRows query
// error is still treated as a miss (the in-memory-equivalent contract)
// but is logged at Warn so a persistently failing replay path — which
// silently re-executes supposedly-idempotent mutations — leaves a
// breadcrumb instead of being invisible.
func (s *PostgresStore) Lookup(ctx context.Context, key, userID string) (Record, bool) {
	row := s.db.QueryRowContext(ctx, lookupSQL, compositeKey(key, userID))
	var (
		rec     Record
		headers []byte
	)
	err := row.Scan(&rec.RequestHash, &rec.Status, &rec.Body, &headers, &rec.CreatedAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			// Real DB failure: still a miss (contract preserved) but
			// no longer silent — this re-executes the mutation.
			s.log.Warn("idempotency: lookup failed; treating as cache miss (mutation will re-execute)",
				"err", err, "event", "idempotency_lookup_failed")
		}
		return Record{}, false
	}
	rec.Key = key
	rec.UserID = userID
	rec.Headers = decodeHeaders(headers)
	return rec, true
}

// Save persists the response so future requests with the same key can
// replay it. CreatedAt is stamped here when zero so callers don't have
// to remember. Concurrent duplicate requests are race-safe: the
// composite_key primary key + ON CONFLICT DO NOTHING means exactly one
// writer wins and the rest are no-ops — the loser's request then sees
// the winner's row on the next Lookup, which is the desired replay.
func (s *PostgresStore) Save(ctx context.Context, r Record) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, insertSQL,
		compositeKey(r.Key, r.UserID),
		r.UserID,
		r.Key,
		r.RequestHash,
		r.Status,
		r.Body,
		encodeHeaders(r.Headers),
		r.CreatedAt.UTC(),
	)
	return err
}

// SweepExpired deletes every row older than ttl and returns the number
// dropped (useful for observability). Backed by the
// `idempotency_keys_reaper` index on created_at.
func (s *PostgresStore) SweepExpired(ctx context.Context, ttl time.Duration) (int, error) {
	cutoff := time.Now().Add(-ttl).UTC()
	res, err := s.db.ExecContext(ctx, sweepSQL, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// encodeHeaders serialises the header map deterministically (sorted
// keys) so the stored JSON is stable. nil → "{}" so the column is
// never NULL.
func encodeHeaders(h map[string]string) []byte {
	if len(h) == 0 {
		return []byte("{}")
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(h))
	for _, k := range keys {
		ordered[k] = h[k]
	}
	b, err := json.Marshal(ordered)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// decodeHeaders is the inverse of encodeHeaders. A malformed or empty
// payload yields an empty (non-nil) map so replay never panics.
func decodeHeaders(b []byte) map[string]string {
	out := map[string]string{}
	if len(b) == 0 {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}
