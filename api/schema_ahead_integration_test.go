//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// On Postgres, in a schema of its own (the other integration tests share the
// database): `migrate up` refuses a database whose goose table names a newer
// migration than the directory's, before applying anything, and serve's
// guard refuses it against the code's newest migration.
func TestPostgresMigrateRefusesADatabaseAhead(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL unset: skipping the Postgres schema guard test")
	}
	admin := openTestDB(t, dsn)
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("ahead_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE") })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	scoped := dsn + sep + "search_path=" + schema
	db := openTestDB(t, scoped)
	t.Cleanup(func() { db.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := checkSchemaNotAhead(logger, scoped, 143); err != nil {
		t.Fatalf("no goose table yet: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL,
		is_applied boolean NOT NULL, tstamp timestamp DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, true), (9999, true)`); err != nil {
		t.Fatal(err)
	}
	err := runGooseUp(ctx, db, repoMigrationsDir(t))
	if !errors.Is(err, errSchemaAhead) || !strings.Contains(err.Error(), "at 9999") {
		t.Fatalf("up on a database ahead = %v", err)
	}
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = $1`, schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("%d tables in the schema: up applied something before refusing", tables)
	}
	if err := checkSchemaNotAhead(logger, scoped, 143); !errors.Is(err, errSchemaAhead) {
		t.Fatalf("serve's guard = %v", err)
	}
}
