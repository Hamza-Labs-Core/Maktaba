package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/schemaguard"
)

func TestSchemaAhead(t *testing.T) {
	if err := schemaAhead(143, 143); err != nil {
		t.Fatalf("the same version: %v", err)
	}
	if err := schemaAhead(12, 143); err != nil {
		t.Fatalf("behind (migrate up applies the rest): %v", err)
	}
	err := schemaAhead(150, 143)
	if !errors.Is(err, errSchemaAhead) || !strings.Contains(err.Error(), "at 0150") ||
		!strings.Contains(err.Error(), "up to 0143") {
		t.Fatalf("ahead = %v", err)
	}
}

func TestNewestMigration(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"0000_schema_version.sql", "0001_a.sql", "0001_a.sqlite.sql", "0007_b.sql",
		"0009_c.sqlite.sql", "MANIFEST.md", "notes.sql"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("-- +goose Up\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := newestMigration(dir, false); err != nil || v != 7 {
		t.Fatalf("postgres = %d, %v", v, err)
	}
	if v, err := newestMigration(dir, true); err != nil || v != 9 {
		t.Fatalf("sqlite = %d, %v", v, err)
	}
	// the shipped set ends where SchemaRev says (serve's guard compares with it)
	if v, err := newestMigration(repoMigrationsDir(t), false); err != nil || v != schemaguard.SchemaRev {
		t.Fatalf("shipped = %d, %v; SchemaRev %d", v, err, schemaguard.SchemaRev)
	}
}

// On a real SQLite database: `migrate up` applies its migrations, then refuses
// the database once it holds a newer one than they (a newer Maktaba ran);
// serve's guard says the same against the code's newest migration.
func TestSQLiteMigrateRefusesADatabaseAhead(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"0001_a.sqlite.sql": "-- +goose Up\nCREATE TABLE a (id INTEGER PRIMARY KEY);\n",
		"0002_b.sqlite.sql": "-- +goose Up\nCREATE TABLE b (id INTEGER PRIMARY KEY);\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "maktaba.db"))
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := checkSchemaNotAhead(logger, dsn, 2); err != nil {
		t.Fatalf("a new database: %v", err)
	}
	if err := runSQLiteMigrate(ctx, dsn, dir, "up"); err != nil {
		t.Fatal(err)
	}
	if err := runSQLiteMigrate(ctx, dsn, dir, "up"); err != nil {
		t.Fatalf("again, up to date: %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDriverPath(dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (5, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := runSQLiteMigrate(ctx, dsn, dir, "up"); !errors.Is(err, errSchemaAhead) {
		t.Fatalf("up on a database ahead = %v", err)
	}
	if err := checkSchemaNotAhead(logger, dsn, 2); !errors.Is(err, errSchemaAhead) {
		t.Fatalf("serve's guard = %v", err)
	}
	if err := checkSchemaNotAhead(logger, dsn, 5); err != nil {
		t.Fatalf("serve's guard, a code that knows 5: %v", err)
	}
	// a database that cannot be read is left to the readiness checks
	if err := checkSchemaNotAhead(logger, "postgres://127.0.0.1:1/none?sslmode=disable&connect_timeout=1", 2); err != nil {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestMigrateContextZeroIsNoBound(t *testing.T) {
	ctx, cancel := migrateContext(0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok || ctx.Err() != nil {
		t.Fatal("--timeout 0 has a deadline")
	}
	ctx2, cancel2 := migrateContext(5)
	defer cancel2()
	if _, ok := ctx2.Deadline(); !ok {
		t.Fatal("--timeout 5ns has none")
	}
}
