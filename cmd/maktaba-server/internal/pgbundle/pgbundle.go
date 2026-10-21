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
	// LAN: it listens on every interface for the cluster's machines (cluster.go);
	// TLS: their connections are encrypted with the cluster database CA's certificate;
	// MachinesFrom: the networks they may connect from (Bundle.machinesFrom).
	LAN          bool
	TLS          bool
	MachinesFrom string
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
		LAN:     !strings.EqualFold(strings.TrimSpace(cfg.Postgres.Listen), "loopback"),
		TLS:     !strings.EqualFold(strings.TrimSpace(cfg.Postgres.TLS), "off"),
		// the networks the machines connect from
		MachinesFrom: cfg.Postgres.MachinesFrom,
	}, true, nil
}

// FindHome is the Postgres install this machine has for Maktaba (findHome), "" for
// none: a full node's copy of its home's database runs on it rather than on programs
// it fetches.
func FindHome(cfg config.Config, exe string) string { return findHome(cfg, exe) }

// findHome: $MAKTABA_POSTGRES_HOME, [postgres] home, else the payload's postgres/
// ($MAKTABA_APP_BUNDLE, or beside the bin/ of this executable).
func findHome(cfg config.Config, exe string) string {
	for _, h := range []string{os.Getenv("MAKTABA_POSTGRES_HOME"), cfg.Postgres.Home} {
		if h = strings.TrimSpace(h); h != "" {
			if hasServer(h) {
				return h
			}
			return ""
		}
	}
	if b := config.AppBundle(); b != "" && hasServer(filepath.Join(b, "postgres")) {
		return filepath.Join(b, "postgres")
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
	info, err := os.Stat(filepath.Join(home, "bin", exe("postgres")))
	return err == nil && !info.IsDir()
}

// exe is a program's file name on this OS.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
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
func (b Bundle) Bin(name string) string { return filepath.Join(b.Home, "bin", exe(name)) }

// StopCommand is the fast shutdown where a signal cannot ask for it (Windows has no
// SIGINT for another process): pg_ctl stop -m fast, not waiting.
func (b Bundle) StopCommand() *exec.Cmd {
	return exec.Command(b.Bin("pg_ctl"), "stop", "-D", b.Data, "-m", "fast", "-W")
}

// stop asks a server of this cluster to shut down fast.
func (b Bundle) stop(p *os.Process) {
	if runtime.GOOS == "windows" {
		_ = b.StopCommand().Run()
		return
	}
	_ = p.Signal(os.Interrupt)
}

// Addr is the loopback TCP address.
func (b Bundle) Addr() string { return "127.0.0.1:" + strconv.Itoa(b.Port) }

// ServerArgs are the arguments of the foreground `postgres`: this cluster, on
// loopback (and the LAN for the cluster's machines, TLS on), the unix socket in the
// data directory (none when that path is too long for a socket: every role connects
// over loopback TCP anyway).
func (b Bundle) ServerArgs() []string { return b.serverArgs(b.LAN) }

// serverArgs: lan false for the short-lived servers of Prepare (never on the LAN).
func (b Bundle) serverArgs(lan bool) []string {
	sock := b.Data
	if len(filepath.Join(sock, ".s.PGSQL."+strconv.Itoa(b.Port))) > maxSocketPath || runtime.GOOS == "windows" {
		sock = ""
	}
	listen := "127.0.0.1"
	if lan {
		listen = "*"
	}
	// wal_level=logical: a home publishes its database to its machines' copies, and a
	// machine's copy may become the home (specs/epics/37-node-replica). Room for every
	// machine's slot and sender, the first copy's tables in parallel on each side.
	args := []string{"-D", b.Data, "-p", strconv.Itoa(b.Port),
		"-c", "listen_addresses=" + listen, "-c", "unix_socket_directories=" + sock, "-c", "wal_level=logical",
		"-c", "max_replication_slots=40", "-c", "max_wal_senders=40", "-c", "max_worker_processes=24",
		"-c", "max_logical_replication_workers=12", "-c", "max_sync_workers_per_subscription=8"}
	if b.TLS && b.TLSReady() {
		args = append(args, "-c", "ssl=on", "-c", "ssl_cert_file="+b.serverCert(), "-c", "ssl_key_file="+b.serverKey())
	}
	return args
}

// ServerCommand is the foreground `postgres` of this cluster (ServerArgs). On
// Windows, when this process runs as an administrator (an elevated session, a
// CI runner), it gets a restricted token, as pg_ctl and initdb give theirs:
// postgres.exe refuses to run with administrative rights.
func (b Bundle) ServerCommand() (*exec.Cmd, error) {
	return b.serverCommand(b.ServerArgs())
}

// serverCommand is `postgres` with args, under ServerCommand's token.
func (b Bundle) serverCommand(args []string) (*exec.Cmd, error) {
	cmd := exec.Command(b.Bin("postgres"), args...)
	if err := restrict(cmd); err != nil {
		return nil, fmt.Errorf("a restricted token for postgres: %w", err)
	}
	return cmd, nil
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

// SuperURL is the superuser's database URL (the copy's subscription, its roles; a
// switch of the home). It writes when the database is frozen too (a switch sets
// default_transaction_read_only on it for the app's roles): only this machine's own
// copy and switch work connects as the superuser. It needs Prepare to have run.
func (b Bundle) SuperURL() (string, error) {
	pw, err := readPassword(b.passwordFile(SuperUser))
	if err != nil {
		return "", err
	}
	return "postgres://" + SuperUser + ":" + pw + "@" + b.Addr() + "/" + AppDatabase +
		"?sslmode=disable&default_transaction_read_only=off", nil
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
	if b.Initialized() {
		// a cluster outlives a lost superuser password only by hand
		if _, err := os.Stat(b.passwordFile(SuperUser)); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bundled postgres: %s is missing but the cluster in %s exists: restore the file, "+
				"or move the cluster away to start a new one", b.passwordFile(SuperUser), b.Data)
		}
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
	// what the cluster's machines connect through, made again at every start
	if err := b.writeHBA(); err != nil {
		return fmt.Errorf("bundled postgres: pg_hba.conf: %w", err)
	}
	if b.TLS {
		if err := b.ensureTLS(time.Now()); err != nil {
			return err
		}
	}
	// the app role, its database and extensions (once, or when its password file is
	// gone), then what lets it provision the machines (once, also on a cluster made
	// before that), in one session of a server of its own
	_, statErr := os.Stat(b.passwordFile(AppRole))
	_, appErr := os.Stat(filepath.Join(b.Data, bootstrapMarker))
	_, clusterErr := os.Stat(filepath.Join(b.Data, clusterMarker))
	needApp, needCluster := appErr != nil || statErr != nil, clusterErr != nil
	if !needApp && !needCluster {
		return nil
	}
	sql := ""
	if needApp {
		appPW, err := ensurePassword(b.passwordFile(AppRole))
		if err != nil {
			return err
		}
		sql = bootstrapSQL(appPW)
	}
	if needCluster {
		sql += clusterSQL
	}
	if err := b.bootstrap(ctx, superPW, sql, log); err != nil {
		return fmt.Errorf("bundled postgres: create the %s role and database: %w", AppRole, err)
	}
	stamp := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
	for _, m := range []string{bootstrapMarker, clusterMarker} {
		if err := os.WriteFile(filepath.Join(b.Data, m), stamp, 0o600); err != nil {
			return err
		}
	}
	return nil
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
	// into a directory beside, renamed into place when complete: a cluster that
	// initdb did not finish (killed, power cut) is never taken for a real one
	tmp := b.Data + ".initdb"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	fmt.Fprintf(log, "[postgres] creating the database cluster in %s\n", b.Data)
	cmd := exec.CommandContext(ctx, b.Bin("initdb"), "-D", tmp, "-U", SuperUser, "--pwfile="+pwfile.Name(),
		"--auth=scram-sha-256", "--encoding=UTF8", "--locale=C")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("bundled postgres: initdb %s: %w", b.Data, err)
	}
	if err := os.RemoveAll(b.Data); err != nil { // an empty or half-made directory
		return err
	}
	return os.Rename(tmp, b.Data)
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
func (b Bundle) bootstrap(ctx context.Context, superPW, sql string, log io.Writer) error {
	return b.withServer(ctx, log, func() error {
		psql := exec.CommandContext(ctx, b.Bin("psql"), "-X", "-q", "-h", "127.0.0.1", "-p", strconv.Itoa(b.Port),
			"-U", SuperUser, "-d", "postgres")
		psql.Env = append(os.Environ(), "PGPASSWORD="+superPW)
		psql.Stdin = strings.NewReader(sql)
		psql.Stdout, psql.Stderr = log, log
		return psql.Run()
	})
}

// withServer runs fn with a server of its own up (the supervisor's is not
// running), then stops it.
func (b Bundle) withServer(ctx context.Context, log io.Writer, fn func() error) error {
	srv, err := b.serverCommand(b.serverArgs(false))
	if err != nil {
		return err
	}
	srv.Stdout, srv.Stderr = log, log
	if err := srv.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- srv.Wait() }()
	defer func() {
		b.stop(srv.Process) // fast shutdown
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
	return fn()
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
