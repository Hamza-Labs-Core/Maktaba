package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

// A database a newer Maktaba migrated (Epic 38 story 38.12). The migrations go
// forward only, and goose's `up` finds nothing to apply on a database ahead of
// them, so an older maktaba-api (the previous app installed again, an older
// package) would run its code on a schema it does not know. `migrate up`
// refuses such a database (exit 3) and so does `serve`, for every install.

// errSchemaAhead: the database holds migrations newer than this code's.
var errSchemaAhead = errors.New("the database holds migrations newer than this Maktaba knows")

// exitSchemaAhead is `migrate`'s exit status on errSchemaAhead: the desktop
// app's server tells it from a failed migration (a newer release ran here,
// nothing to put back; cmd/maktaba-server/serve_appbundle.go).
const exitSchemaAhead = 3

// schemaAhead is errSchemaAhead with both versions when applied is above known.
func schemaAhead(applied, known int64) error {
	if applied <= known {
		return nil
	}
	return fmt.Errorf("%w: the database's schema is at %04d and this maktaba-api knows migrations up to %04d. "+
		"A newer Maktaba has migrated it: run that version (or a newer one), or restore the backup taken before its update",
		errSchemaAhead, applied, known)
}

// appliedVersion is the newest migration applied to db (goose's table, which
// the SQLite applier keeps too); 0 before the first.
func appliedVersion(ctx context.Context, db *sql.DB, sqlite bool) (int64, error) {
	exists := `SELECT to_regclass('goose_db_version') IS NOT NULL`
	if sqlite {
		exists = `SELECT COUNT(*) > 0 FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`
	}
	var has bool
	if err := db.QueryRowContext(ctx, exists).Scan(&has); err != nil {
		return 0, fmt.Errorf("read the schema version: %w", err)
	}
	if !has {
		return 0, nil
	}
	var v int64
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read the schema version: %w", err)
	}
	return v, nil
}

// newestMigration is the highest slot among dir's migrations for the dialect
// (*.sqlite.sql for SQLite, the other *.sql for Postgres).
func newestMigration(dir string, sqlite bool) (int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var newest int64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sqlite.sql") != sqlite {
			continue
		}
		slot, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		if v, err := strconv.ParseInt(slot, 10, 64); err == nil && v > newest {
			newest = v
		}
	}
	return newest, nil
}

// refuseAheadOf fails with errSchemaAhead when db holds a migration newer than
// the newest of dir.
func refuseAheadOf(ctx context.Context, db *sql.DB, dir string, sqlite bool) error {
	newest, err := newestMigration(dir, sqlite)
	if err != nil {
		return err
	}
	applied, err := appliedVersion(ctx, db, sqlite)
	if err != nil {
		return err
	}
	return schemaAhead(applied, newest)
}

// checkSchemaNotAhead is serve's guard: the database at dsn must not hold a
// migration newer than known (the newest this code ships). A database that
// cannot be read here is left to the readiness checks.
func checkSchemaNotAhead(logger *slog.Logger, dsn string, known int64) error {
	if strings.TrimSpace(dsn) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sqlite := isSQLiteDSN(dsn)
	var db *sql.DB
	var err error
	if sqlite {
		db, err = sql.Open("sqlite", sqliteDriverPath(dsn))
	} else {
		db, err = sql.Open("postgres", pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping))
	}
	if err == nil {
		defer db.Close()
		var applied int64
		if applied, err = appliedVersion(ctx, db, sqlite); err == nil {
			return schemaAhead(applied, known)
		}
	}
	logger.Warn("schema guard: the database's schema version could not be read; serving anyway",
		"err", err, "event", "schema_version_unread")
	return nil
}
