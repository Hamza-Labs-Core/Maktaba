//go:build integration

package analytics

// Runs every analytics endpoint against a real, migrated Postgres (the same
// seeded home and checks as the SQLite test), because SQLite cannot tell
// whether the Postgres SQL is valid.
//
//	MAKTABA_TEST_PG_URL=postgres://u:p@127.0.0.1:5432/postgres?sslmode=disable \
//	    go test -tags integration ./internal/handlers/analytics/ -run Postgres
//
// The URL names a Postgres whose user may create databases, as for every other
// MAKTABA_TEST_PG_URL test: the test makes its own scratch database there,
// migrates it and drops it afterwards.

import (
	"database/sql"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

func pgDB(t *testing.T) *sql.DB {
	t.Helper()
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		base = os.Getenv("DATABASE_URL")
	}
	if base == "" {
		t.Skip("neither MAKTABA_TEST_PG_URL nor DATABASE_URL is set")
	}
	u, err := neturl.Parse(base)
	if err != nil {
		t.Fatalf("Postgres URL: %v", err)
	}
	admin := openPG(t, base)
	name := fmt.Sprintf("maktaba_analytics_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`) })
	u.Path = "/" + name
	q := u.Query()
	q.Set("timezone", "UTC") // the day and hour buckets are the session's
	u.RawQuery = q.Encode()
	db := openPG(t, u.String())
	applyMigrations(t, db)
	return db
}

func openPG(t *testing.T, url string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return db
}

// applyMigrations applies shared/db/migrations the way `maktaba-api migrate
// up` does: slot 0000 by hand (goose refuses version 0), then every
// Postgres migration (the .sqlite.sql siblings left out) through goose.
func applyMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	dir := migrationsDir(t)
	if raw, err := os.ReadFile(filepath.Join(dir, "0000_schema_version.sql")); err == nil {
		up := string(raw)
		if i := strings.Index(up, "-- +goose Down"); i >= 0 {
			up = up[:i]
		}
		if _, err := db.Exec(up); err != nil {
			t.Fatalf("0000_schema_version: %v", err)
		}
	}
	stage := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".sql" || strings.HasSuffix(n, ".sqlite.sql") || strings.HasPrefix(n, "0000_") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, n), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, stage); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

// migrationsDir finds shared/db/migrations above the package directory.
func migrationsDir(t *testing.T) string {
	t.Helper()
	cur, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		dir := filepath.Join(cur, "shared", "db", "migrations")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatal("shared/db/migrations not found")
		}
		cur = parent
	}
}

func TestAnalytics_Postgres(t *testing.T) {
	db := pgDB(t)
	seed(t, db, func(at time.Time, _ int) any { return at })
	runScenario(t, &Handler{DB: db, Driver: "postgres"})
}
