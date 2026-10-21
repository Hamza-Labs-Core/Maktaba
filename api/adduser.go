package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/pgdsn"
)

// runAddUser implements `maktaba-api adduser <username>` (Story 10.1
// AC-4). The command:
//
//   - prompts for the password from a TTY without echo,
//   - hashes it with the package-default argon2id parameters,
//   - inserts the user with `is_admin=true`,
//   - refuses if a non-sentinel user already exists (so an unattended
//     re-run doesn't accidentally seed a second admin and confuse
//     ops).
//
// The `--admin` flag (default true) lets us reuse the same code path
// for follow-up non-admin user creation in the future; v1 only has
// admins until handlers are wired up.
func runAddUser(argv []string) error {
	fs := flag.NewFlagSet("adduser", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		dsn          string
		isAdmin      bool
		passwordFile string
		hashFile     string
		showHelp     bool
	)
	fs.StringVar(&dsn, "dsn", os.Getenv("DATABASE_URL"), "Database DSN (default: $DATABASE_URL)")
	fs.BoolVar(&isAdmin, "admin", true, "Create as admin")
	fs.StringVar(&passwordFile, "password-file", "", "Read password from file (one line). Useful for non-interactive seeding.")
	fs.StringVar(&hashFile, "password-hash-file", "", "Read the password's argon2id hash from file (one line, from 'maktaba-api hash-password'): the first-run setup of the desktop app.")
	fs.BoolVar(&showHelp, "help", false, "Show help")

	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage: maktaba-api adduser <username> [flags]

Creates a Maktaba user. Prompts for the password without echoing it.
Story 10.1 AC-4: used to seed the first admin before HTTP user
management is reachable.

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
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("adduser: expected exactly one positional <username>")
	}
	username := fs.Arg(0)
	if dsn == "" {
		return errors.New("DSN is empty: pass --dsn or set DATABASE_URL")
	}

	if passwordFile != "" && hashFile != "" {
		return errors.New("adduser: --password-file and --password-hash-file are exclusive")
	}
	var password, hash string
	var err error
	if hashFile != "" {
		if hash, err = readHashFile(hashFile); err != nil {
			return err
		}
	} else if password, err = readPassword(passwordFile); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dsn = pgdsn.ForLibPQ(ctx, dsn, pgdsn.Ping)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	store := users.New(db)
	already, err := store.HasAnyUser(ctx)
	if err != nil {
		return fmt.Errorf("user count: %w", err)
	}
	if already {
		return errUserExists
	}

	u, err := store.Create(ctx, users.CreateInput{
		Username:     username,
		Password:     password,
		PasswordHash: hash,
		IsAdmin:      isAdmin,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "adduser: created user %s (id=%s, is_admin=%v)\n", u.Username, u.ID, u.IsAdmin)
	return nil
}

// errUserExists: the database has a real user already (`adduser` exits 3 for
// it, so the desktop app's setup, run again after it seeded the admin, can
// tell it from a failure).
var errUserExists = errors.New("adduser: a real user already exists; use the admin HTTP API to add more users")

// readPassword pulls the password from --password-file when given,
// otherwise prompts the operator on the controlling TTY without echo.
// Story 10.1 AC-4 calls for "no echo"; term.ReadPassword handles the
// terminal-mode flip on both POSIX and Windows.
func readPassword(file string) (string, error) {
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		// Trim a trailing newline left behind by `printf 'pw\n' > f`,
		// but preserve other whitespace — `   ` is a deliberate
		// password if someone configured it that way.
		s := string(b)
		s = strings.TrimRight(s, "\r\n")
		return s, nil
	}

	fmt.Fprint(os.Stderr, "Password: ")
	b, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr) // newline after the silent prompt
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	pw := string(b)
	if pw == "" {
		return "", errors.New("password: empty")
	}
	return pw, nil
}

// readHashFile reads a password hash from `maktaba-api hash-password` (one
// line). Its format is checked when the user is created.
func readHashFile(file string) (string, error) {
	b, err := os.ReadFile(file) //nolint:gosec // the operator's own file
	if err != nil {
		return "", err
	}
	h := strings.TrimSpace(string(b))
	if h == "" {
		return "", errors.New("password hash: empty")
	}
	return h, nil
}
