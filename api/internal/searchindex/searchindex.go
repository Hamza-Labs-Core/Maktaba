// Package searchindex makes keyword search's BM25 indexes (Epic 49, story 49.5) where the
// database can hold them: slot 0232's maktaba_search_index_statements() says which are
// missing (none without pg_textsearch), and Ensure runs its statements one by one, outside
// a transaction, as CREATE INDEX CONCURRENTLY needs. `maktaba-api migrate up` calls it after
// the migrations, on every start: a fresh home on PostgreSQL 18 gains the indexes there, and
// a home whose schema migrated on 16 at its first start on 18.
package searchindex

import (
	"context"
	"database/sql"
	"fmt"
)

// Ensure makes the BM25 indexes missing here and returns the statements it ran. A database
// whose schema has no slot 0232 yet (an older release's migrations) needs none.
func Ensure(ctx context.Context, db *sql.DB) ([]string, error) {
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
