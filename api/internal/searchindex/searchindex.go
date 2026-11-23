// Package searchindex makes keyword search's BM25 indexes (Epic 49, stories 49.5 and 49.6)
// where the database can hold them. Slot 0232's maktaba_search_index_statements() says
// which are missing (none without pg_textsearch), and Ensure runs its statements one by one,
// outside a transaction, as CREATE INDEX CONCURRENTLY needs.
//
// The build is never part of a start's migrations: on a large library it takes longer than
// any bound a migration has, and a bound reached part way leaves an invalid index that the
// next try drops and builds again from the start. The home's API runs Run as a background
// loop: it builds what is missing after the start, with no time limit, one API at a time
// (an advisory lock), and checks again every hour (an index left invalid, the extension
// made later). Until both indexes are valid (Ready), keyword search keeps its line path.
package searchindex

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Names are the two BM25 indexes (slot 0234): exact (search_text, maktaba_search) and
// stemmed (stem_text, maktaba_stem).
var Names = []string{"video_search_docs_bm25_text", "video_search_docs_bm25_stem_text"}

// lockKey is the advisory lock one builder holds while it builds ("mkt49idx").
const lockKey int64 = 0x6d6b7434396964

// Querier is what Ensure needs: a *sql.DB, or the one connection Run holds its lock on.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Ensure makes the BM25 indexes missing here and returns the statements it ran. A database
// whose schema has no slot 0232 yet (an older release's migrations) needs none.
func Ensure(ctx context.Context, db Querier) ([]string, error) {
	var have bool
	if err := db.QueryRowContext(ctx,
		`SELECT to_regprocedure('public.maktaba_search_index_statements()') IS NOT NULL`).Scan(&have); err != nil {
		return nil, fmt.Errorf("search indexes: %w", err)
	}
	if !have {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT s FROM public.maktaba_search_index_statements() AS s`)
	if err != nil {
		return nil, fmt.Errorf("search indexes: %w", err)
	}
	var stmts []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("search indexes: %w", err)
		}
		stmts = append(stmts, s)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("search indexes: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search indexes: %w", err)
	}
	for i, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return stmts[:i], fmt.Errorf("search indexes: %s: %w", s, err)
		}
	}
	return stmts, nil
}

// Ready reports whether both BM25 indexes are there and valid: keyword search ranks by them
// only then (story 49.7), and by its lines before.
func Ready(ctx context.Context, db Querier) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_index x
		WHERE x.indexrelid IN (to_regclass('public.video_search_docs_bm25_text'), to_regclass('public.video_search_docs_bm25_stem_text'))
		  AND x.indisvalid AND x.indisready`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("search indexes: %w", err)
	}
	return n == len(Names), nil
}

// BuildOnce builds what is missing on one connection of db, holding the builders' advisory
// lock (another API building now: nothing, and locked is false) with no statement or lock
// timeout of the session's own (an operator's DATABASE_URL may set them).
func BuildOnce(ctx context.Context, db *sql.DB) (ran []string, locked bool, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("search indexes: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&locked); err != nil {
		return nil, false, fmt.Errorf("search indexes: %w", err)
	}
	if !locked {
		return nil, false, nil
	}
	defer func() {
		bg := context.WithoutCancel(ctx)
		_, _ = conn.ExecContext(bg, `SELECT pg_advisory_unlock($1)`, lockKey)
		_, _ = conn.ExecContext(bg, `RESET statement_timeout`)
		_, _ = conn.ExecContext(bg, `RESET lock_timeout`)
	}()
	if _, err := conn.ExecContext(ctx, `SET statement_timeout = 0`); err != nil {
		return nil, true, fmt.Errorf("search indexes: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SET lock_timeout = 0`); err != nil {
		return nil, true, fmt.Errorf("search indexes: %w", err)
	}
	ran, err = Ensure(ctx, conn)
	return ran, true, err
}

// isPostgres reports whether db is a PostgreSQL database (not the SQLite backend).
func isPostgres(db *sql.DB) bool {
	return db != nil && !strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

// Every is how often Run looks again once the indexes are there or could not be built.
var Every = time.Hour

// Run is the home API's loop (api:search_index): BuildOnce now, then every Every, until ctx
// ends. A build that fails is said and tried again at the next round.
func Run(ctx context.Context, db *sql.DB, log *slog.Logger) {
	// a SQLite-backed API has no BM25 index to build (its keyword search reads FTS5): the
	// round would ask to_regprocedure, which SQLite lacks, and warn every hour (#1483)
	if !isPostgres(db) {
		log.Info("search index loop not needed", "event", "search_index_skipped", "reason", "not postgres")
		return
	}
	for {
		t0 := time.Now()
		ran, _, err := BuildOnce(ctx, db)
		for _, s := range ran {
			log.Info("search index built", "event", "search_index_built", "statement", s,
				"took_s", int(time.Since(t0).Seconds()))
		}
		if err != nil && ctx.Err() == nil {
			log.Warn("search index not built; keyword search uses its lines meanwhile",
				"event", "search_index_failed", "err", err, "retry_in", Every.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(Every):
		}
	}
}
