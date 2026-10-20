// Package pgbundle runs the Postgres that ships inside Maktaba.app (Epic 36
// story 36.1): a cluster private to this install under the state root
// (<state>/postgres), listening on loopback only (default port 54329) and on a
// unix socket in its data directory, with scram-sha-256 passwords kept in the
// keys directory. The supervisor runs `postgres` in the foreground as its
// postgres role (it starts first and stops last); this package prepares the
// cluster before that, answers the role's readiness check and makes the
// nightly dumps.
package pgbundle

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

const (
	// DefaultPort is the bundled Postgres's loopback port: off 5432, so it never
	// meets another Postgres on the Mac.
	DefaultPort = 54329
	// AppRole and AppDatabase are what every role connects as.
	AppRole     = "maktaba"
	AppDatabase = "maktaba"
	// SuperUser owns the cluster (initdb, extensions, dumps).
	SuperUser = "maktaba_admin"
	// bootstrapMarker records that the roles, database and extensions exist.
	bootstrapMarker = "maktaba-bootstrap.done"
)

// Bundle is one install's bundled Postgres.
type Bundle struct {
	Home    string // the Postgres install: bin/postgres, bin/initdb, bin/psql, bin/pg_dump
	Data    string // the cluster
	Keys    string // the password files
	Backups string // the nightly dumps
	Port    int
}

// Resolve reports whether this install runs the bundled Postgres, and where.
// exe is the running maktaba-server (the payload keeps Postgres at
// <payload>/postgres beside <payload>/bin/maktaba-server).
//
// [postgres] enabled = "off" never runs it; "on" does whenever its binaries are
// there; "auto" (the default) only on macOS. In every case a [database] url set
// in server.toml wins, and so does a SQLite database already in use at the
// default path (an install from before keeps its data).
func Resolve(cfg config.Config, exe string) (Bundle, bool, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Postgres.Enabled))
	if mode == "off" || cfg.DatabaseURLSet {
		return Bundle{}, false, nil
	}
	if mode != "on" && (runtime.GOOS != "darwin" || sqliteInUse()) {
		return Bundle{}, false, nil
	}
	home := findHome(cfg, exe)
	if home == "" {
		if mode == "on" {
			return Bundle{}, false, errors.New("[postgres] enabled = \"on\" but no Postgres install was found " +
				"(set [postgres] home or MAKTABA_POSTGRES_HOME)")
		}
		return Bundle{}, false, nil
	}
	port := cfg.Postgres.Port
	if port <= 0 {
		port = DefaultPort
	}
	root := cfg.StateDir()
	return Bundle{
		Home:    home,
		Data:    filepath.Join(root, "postgres"),
		Keys:    config.KeysDir(),
		Backups: filepath.Join(root, "backups"),
		Port:    port,
	}, true, nil
}

// findHome: $MAKTABA_POSTGRES_HOME, [postgres] home, else the payload's postgres/.
func findHome(cfg config.Config, exe string) string {
	for _, h := range []string{os.Getenv("MAKTABA_POSTGRES_HOME"), cfg.Postgres.Home} {
		if h = strings.TrimSpace(h); h != "" {
			if hasServer(h) {
				return h
			}
			return ""
		}
	}
	if exe == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	h := filepath.Join(filepath.Dir(filepath.Dir(exe)), "postgres")
	if hasServer(h) {
		return h
	}
	return ""
}

func hasServer(home string) bool {
	info, err := os.Stat(filepath.Join(home, "bin", "postgres"))
	return err == nil && !info.IsDir()
}

// sqliteInUse: the default SQLite database exists (no url is in server.toml).
func sqliteInUse() bool {
	path, ok := strings.CutPrefix(config.Defaults().Database.URL, "sqlite://")
	if !ok {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// Bin is one of the install's programs.
func (b Bundle) Bin(name string) string { return filepath.Join(b.Home, "bin", name) }

// Addr is the loopback TCP address.
func (b Bundle) Addr() string { return "127.0.0.1:" + strconv.Itoa(b.Port) }

// ServerArgs are the arguments of the foreground `postgres`: this cluster,
// loopback only, the unix socket in the data directory (none when that path
// is too long for a socket: every role connects over loopback TCP anyway).
func (b Bundle) ServerArgs() []string {
	sock := b.Data
	if len(filepath.Join(sock, ".s.PGSQL."+strconv.Itoa(b.Port))) > maxSocketPath {
		sock = ""
	}
	return []string{"-D", b.Data, "-p", strconv.Itoa(b.Port),
		"-c", "listen_addresses=127.0.0.1", "-c", "unix_socket_directories=" + sock}
}

// maxSocketPath is the longest unix socket path macOS takes (sun_path is 104
// bytes with its NUL).
const maxSocketPath = 103

// URL is the app role's database URL, handed to every child. It needs
// Prepare to have run (the password file).
func (b Bundle) URL() (string, error) {
	pw, err := readPassword(b.passwordFile(AppRole))
	if err != nil {
		return "", err
	}
	return "postgres://" + AppRole + ":" + pw + "@" + b.Addr() + "/" + AppDatabase + "?sslmode=disable", nil
}

func (b Bundle) passwordFile(role string) string {
	return filepath.Join(b.Keys, "postgres-"+role+".password")
}

// Initialized reports whether the cluster exists.
func (b Bundle) Initialized() bool {
	_, err := os.Stat(filepath.Join(b.Data, "PG_VERSION"))
	return err == nil
}

// Prepare makes the cluster ready for the postgres role: initdb on the first
// start (scram-sha-256, a random superuser password), then — once, or when the
// app role's password file is gone — the app role, its database and the
// extensions the migrations use, with a short-lived server of its own.
func (b Bundle) Prepare(ctx context.Context, log io.Writer) error {
	if err := os.MkdirAll(b.Keys, 0o700); err != nil {
		return err
	}
	superPW, err := ensurePassword(b.passwordFile(SuperUser))
	if err != nil {
		return err
	}
	if !b.Initialized() {
		if err := b.initdb(ctx, superPW, log); err != nil {
			return err
		}
	}
	_, statErr := os.Stat(b.passwordFile(AppRole))
	if _, err := os.Stat(filepath.Join(b.Data, bootstrapMarker)); err == nil && statErr == nil {
		return nil
	}
	appPW, err := ensurePassword(b.passwordFile(AppRole))
	if err != nil {
		return err
	}
	if err := b.bootstrap(ctx, superPW, appPW, log); err != nil {
		return fmt.Errorf("bundled postgres: create the %s role and database: %w", AppRole, err)
	}
	return os.WriteFile(filepath.Join(b.Data, bootstrapMarker), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

func (b Bundle) initdb(ctx context.Context, superPW string, log io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(b.Data), 0o700); err != nil {
		return err
	}
	pwfile, err := os.CreateTemp(b.Keys, ".initdb-pw-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(pwfile.Name()) }()
	if _, err := pwfile.WriteString(superPW + "\n"); err != nil {
		_ = pwfile.Close()
		return err
	}
	if err := pwfile.Close(); err != nil {
		return err
	}
	fmt.Fprintf(log, "[postgres] creating the database cluster in %s\n", b.Data)
	cmd := exec.CommandContext(ctx, b.Bin("initdb"), "-D", b.Data, "-U", SuperUser, "--pwfile="+pwfile.Name(),
		"--auth=scram-sha-256", "--encoding=UTF8", "--locale=C")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bundled postgres: initdb %s: %w", b.Data, err)
	}
	return nil
}

// bootstrapSQL creates (or re-keys) the app role, its database and the
// extensions; every statement is safe to run again.
func bootstrapSQL(appPW string) string {
	return `\set ON_ERROR_STOP on
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '` + AppRole + `') THEN
    CREATE ROLE ` + AppRole + ` LOGIN;
  END IF;
END $$;
ALTER ROLE ` + AppRole + ` WITH LOGIN PASSWORD '` + appPW + `';
SELECT 'CREATE DATABASE ` + AppDatabase + ` OWNER ` + AppRole + `'
  WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '` + AppDatabase + `')\gexec
\c ` + AppDatabase + `
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
`
}

// readyTimeout bounds how long the bootstrap server may take to come up.
var readyTimeout = 2 * time.Minute

// bootstrap runs a server of its own (the supervisor's has not started yet),
// feeds psql the bootstrap SQL and stops it again.
func (b Bundle) bootstrap(ctx context.Context, superPW, appPW string, log io.Writer) error {
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	srv.Stdout, srv.Stderr = log, log
	if err := srv.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	defer func() {
		_ = srv.Process.Signal(os.Interrupt) // fast shutdown
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			_ = srv.Process.Kill()
			<-exited
		}
	}()

	rctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	for {
		err := readyCheck(rctx, b.Addr(), SuperUser, "postgres")
		if err == nil {
			break
		}
		select {
		case werr := <-exited:
			exited <- werr // for the deferred stop
			return fmt.Errorf("postgres exited while starting: %v", werr)
		case <-rctx.Done():
			return fmt.Errorf("postgres did not get ready: %w", err)
		case <-time.After(200 * time.Millisecond):
		}
	}

	psql := exec.CommandContext(ctx, b.Bin("psql"), "-X", "-q", "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
		"-U", SuperUser, "-d", "postgres")
	psql.Env = append(os.Environ(), "PGPASSWORD="+superPW)
	psql.Stdin = strings.NewReader(bootstrapSQL(appPW))
	psql.Stdout, psql.Stderr = log, log
	return psql.Run()
}

// readyCheck is Ready (tests replace it).
var readyCheck = Ready

// ensurePassword reads a password file, creating it (0600, random) when missing.
func ensurePassword(path string) (string, error) {
	if pw, err := readPassword(path); err == nil {
		return pw, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	pw := base64.RawURLEncoding.EncodeToString(raw) // URL- and SQL-safe
	if err := os.WriteFile(path, []byte(pw+"\n"), 0o600); err != nil {
		return "", err
	}
	return pw, nil
}

func readPassword(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	pw := strings.TrimSpace(string(b))
	if pw == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return pw, nil
}
