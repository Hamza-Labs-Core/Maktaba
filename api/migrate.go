package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// runMigrate executes the `migrate` subcommand. The subcommand wraps
// goose against $DATABASE_URL with the migrations directory under
// shared/db/migrations.
//
// Disk-based migrations (rather than embed) are intentional for v1:
// the same binary works against the repo checkout in CI and against a
// migrations directory installed alongside the binary in the container
// image (Story 22.3). Story 22.6 may revisit single-binary embedding.
func runMigrate(argv []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		dir      string
		dsn      string
		dialect  string
		timeout  time.Duration
		showHelp bool
	)
	fs.StringVar(&dir, "dir", defaultMigrationsDir(), "Migrations directory")
	fs.StringVar(&dsn, "dsn", os.Getenv("DATABASE_URL"), "Database DSN (default: $DATABASE_URL)")
	fs.StringVar(&dialect, "dialect", "postgres", "SQL dialect (postgres only in v1)")
	fs.DurationVar(&timeout, "timeout", 5*time.Minute, "Overall migration timeout")
	fs.BoolVar(&showHelp, "help", false, "Show help")

	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage: maktaba-api migrate <action> [flags]

Actions:
  up        Apply all pending migrations
  status    Show applied vs. pending migrations
  version   Print the current schema version
  validate  Verify the migrations directory is well-formed

Flags:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if showHelp {
		fs.Usage()
		return nil
	}

	action := "up"
	if fs.NArg() > 0 {
		action = fs.Arg(0)
	}

	if dir == "" {
		return errors.New("migrations directory is empty: pass --dir")
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("migrations directory %q: %w", dir, err)
	}

	// `validate` is a static directory check — short-circuit before
	// touching dialect/DSN.
	if action == "validate" {
		return validateDir(dir)
	}

	if dialect != "postgres" {
		return fmt.Errorf("dialect %q is not supported (only postgres in v1)", dialect)
	}
	if dsn == "" {
		return errors.New("DSN is empty: pass --dsn or set DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	// Goose loads .sql files matching its name regex; .sqlite.sql
	// siblings would be applied too against Postgres. Filter them out
	// by pointing goose at the postgres-only set we materialize via the
	// migrations source filter below.
	goose.SetLogger(stderrLogger{})

	switch action {
	case "up":
		fmt.Fprintf(os.Stderr, "migrate: applying migrations from %s against %s\n",
			dir, redactDSN(dsn))
		return runGooseUp(ctx, db, dir)
	case "status":
		return goose.StatusContext(ctx, db, dir)
	case "version":
		return goose.VersionContext(ctx, db, dir)
	default:
		return fmt.Errorf("unknown migrate action %q (try 'up', 'status', 'version', 'validate')", action)
	}
}

// runGooseUp wraps goose.UpContext after copying postgres-only files
// to a clean staging directory: goose loads every *.sql in its dir,
// and we want SQLite-parity siblings (foo.sqlite.sql) excluded from
// the Postgres run.
func runGooseUp(ctx context.Context, db *sql.DB, dir string) error {
	stage, err := stagePostgresMigrations(dir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	return goose.UpContext(ctx, db, stage)
}

// stagePostgresMigrations creates a tmp dir holding only the
// postgres-targeted migration files (i.e. files that don't end in
// `.sqlite.sql`). It returns the tmp path; the caller is responsible
// for cleanup.
func stagePostgresMigrations(dir string) (string, error) {
	stage, err := os.MkdirTemp("", "maktaba-migrations-*")
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		os.RemoveAll(stage)
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".sql" {
			continue
		}
		if hasSuffix(name, ".sqlite.sql") {
			continue
		}
		if err := copyFile(filepath.Join(dir, name), filepath.Join(stage, name)); err != nil {
			os.RemoveAll(stage)
			return "", err
		}
	}
	return stage, nil
}

func validateDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		count++
	}
	fmt.Fprintf(os.Stderr, "migrate: %s contains %d *.sql files\n", dir, count)
	return nil
}

// defaultMigrationsDir resolves the migrations directory by walking up
// from the current working directory looking for shared/db/migrations.
// This lets `go run .` and the built binary both find the directory
// when run from anywhere inside the repo.
func defaultMigrationsDir() string {
	if env := os.Getenv("MAKTABA_MIGRATIONS_DIR"); env != "" {
		return env
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "shared/db/migrations"
	}
	for cur := cwd; ; {
		candidate := filepath.Join(cur, "shared", "db", "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "shared/db/migrations"
		}
		cur = parent
	}
}

func redactDSN(dsn string) string {
	// Shallow redact: hide everything between "://" and "@" so logs
	// don't echo passwords.
	const sep = "://"
	i := indexOf(dsn, sep)
	if i < 0 {
		return dsn
	}
	rest := dsn[i+len(sep):]
	at := indexOf(rest, "@")
	if at < 0 {
		return dsn
	}
	return dsn[:i+len(sep)] + "***" + rest[at:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func hasSuffix(s, suf string) bool {
	if len(s) < len(suf) {
		return false
	}
	return s[len(s)-len(suf):] == suf
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type stderrLogger struct{}

func (stderrLogger) Printf(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, format, v...)
}

func (stderrLogger) Fatalf(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, format, v...)
	os.Exit(1)
}
