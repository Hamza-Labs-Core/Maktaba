//go:build unix

package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// Offline (the library stopped), the INVALID index of a migration goose has recorded (an
// earlier try took it for done) is dropped and built again plainly: an older snapshot held
// by another session, a machine's copy of another table, does not hold it up. Online it is
// built CONCURRENTLY, which waits for that snapshot until the lock timeout. On a real
// PostgreSQL, as TestMigrationsDownAfterPartialUp.
func TestOfflineRepairOfARecordedInvalidIndexWaitsForNoSnapshot(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG_HOME")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_HOME: a PostgreSQL install with pgvector (the Linux payload's postgres-18)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db := scratchPostgres(t, home)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "0001_t.sql"), []byte("-- +goose NO TRANSACTION\n-- +goose Up\n"+
		"CREATE TABLE IF NOT EXISTS t (a int);\nCREATE TABLE IF NOT EXISTS other (a int);\n"+
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS t_a ON t (a);\n-- +goose Down\nDROP TABLE t;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	must := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	invalid := func() { t.Helper(); must(`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 't_a'::regclass`) }
	valid := func() bool {
		var ok bool
		_ = db.QueryRowContext(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass('t_a')`).Scan(&ok)
		return ok
	}
	must(`ALTER SYSTEM SET lock_timeout = '2s'`)
	must(`SELECT pg_reload_conf()`)
	// the copy of another table: an older snapshot, held
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT count(*) FROM other`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var lt string
		if err := db.QueryRowContext(ctx, `SHOW lock_timeout`).Scan(&lt); err != nil {
			t.Fatal(err)
		}
		if lt == "2s" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock_timeout %s", lt)
		}
		db.SetMaxIdleConns(0) // a connection made from now on has it
		time.Sleep(100 * time.Millisecond)
	}

	invalid()
	var pe *pq.Error
	if err := repairInvalidIndexes(ctx, db, dir, false); !errors.As(err, &pe) || pe.Code != "55P03" {
		t.Fatalf("online: %v, want the lock timeout", err)
	}
	if valid() {
		t.Fatal("online: the index is valid")
	}
	if err := repairInvalidIndexes(ctx, db, dir, true); err != nil {
		t.Fatalf("offline: %v", err)
	}
	if !valid() {
		t.Fatal("offline: the index is not built again")
	}
}
