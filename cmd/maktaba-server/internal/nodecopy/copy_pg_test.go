package nodecopy

// Two Postgres servers on 127.0.0.1: a home set up as the Postgres bundled with
// Maktaba.app sets one up (pgbundle.Prepare: the app role, the cluster bootstrap, TLS)
// and migrated by maktaba-api, and this machine's copy of it, made and kept by the
// Manager alone: its own database (made, migrated by maktaba-api), subscribed to the
// home's publication. It follows (changes on the home show on the copy), holds none of
// the home's secrets nor other machines' rows, refuses writes, subscribes anew when the
// home gives up on its slot, and is wiped when the machine is revoked.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install (bin/postgres, initdb, pg_ctl,
// psql); it builds maktaba-api from the repository.

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// server is a postgres the test runs (the home, or the copy as the supervisor would).
type server struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
	log  bytes.Buffer
}

func (s *server) start(t *testing.T, exe string, args ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = &s.log, &s.log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", exe, err)
	}
	s.cmd, s.done = cmd, make(chan struct{})
	done := s.done
	go func() { _ = cmd.Wait(); close(done) }()
}

func (s *server) stop(stop *exec.Cmd) {
	s.mu.Lock()
	cmd, done := s.cmd, s.done
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	if stop != nil {
		_ = stop.Run()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()
}

func (s *server) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return false
	}
	select {
	case <-s.done:
		s.cmd = nil
		return false
	default:
		return true
	}
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "shared", "db", "migrations")); err == nil {
			return d
		}
	}
	t.Fatal("repository root not found")
	return ""
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestCopyFollowsTheHome(t *testing.T) {
	pgHome := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgHome == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	if testing.Short() {
		t.Skip("runs two Postgres servers and builds maktaba-api")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- maktaba-api, which migrates both databases ----
	api := filepath.Join(tmp, "maktaba-api"+map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"])
	build := exec.Command("go", "build", "-o", api, ".")
	build.Dir = filepath.Join(root, "api")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build maktaba-api: %v\n%s", err, out)
	}
	migrations := filepath.Join(root, "shared", "db", "migrations")
	migrate := func(ctx context.Context, dsn string) error {
		cmd := exec.CommandContext(ctx, api, "migrate", "--dir", migrations, "--dsn", dsn, "up")
		cmd.Env = append(os.Environ(), "MAKTABA_NODE_MODE=")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%v: %s", err, out)
		}
		return nil
	}

	// ---- the home: the bundled Postgres's own preparation, on loopback ----
	b := pgbundle.Bundle{Home: pgHome, Data: filepath.Join(tmp, "home"), Keys: filepath.Join(tmp, "home-keys"),
		Backups: filepath.Join(tmp, "backups"), Port: freePort(t), TLS: true}
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatalf("prepare the home: %v", err)
	}
	home := &server{}
	home.start(t, b.Bin("postgres"), b.ServerArgs()...)
	defer func() { home.stop(b.StopCommand()) }()
	waitFor(t, "the home's Postgres", 60*time.Second, func() bool {
		rctx, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		return pgbundle.Ready(rctx, b.Addr(), pgbundle.AppRole, pgbundle.AppDatabase) == nil
	})
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, appURL); err != nil {
		t.Fatalf("migrate the home: %v", err)
	}
	app, err := sql.Open("postgres", appURL)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	exec1 := func(q string, args ...any) {
		t.Helper()
		if _, err := app.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the home's API publishes its CA and beats while a machine follows (api cluster.Copies)
	ca, err := os.ReadFile(b.CACert())
	if err != nil {
		t.Fatal(err)
	}
	exec1(`UPDATE cluster_replica_beat SET ca_pem = $1`, string(ca))
	var beating atomic.Bool // the home's API runs
	beating.Store(true)
	go func() {
		for ctx.Err() == nil {
			if beating.Load() {
				_, _ = app.ExecContext(ctx, `UPDATE cluster_replica_beat SET at = now()`)
			}
			time.Sleep(time.Second)
		}
	}()
	// a secret the copy must never hold, and a web session: its id and CSRF token are
	// live cookies (the copy holds their hashes only)
	exec1(`INSERT INTO home_secrets (name, key_id, nonce, ciphertext) VALUES ('plex.token', 'k1', '\x00'::bytea, '\x00'::bytea)
	       ON CONFLICT DO NOTHING`)
	sessUser, sessID := uuid.NewString(), uuid.NewString()
	exec1(`INSERT INTO users (id, username, pw_hash) VALUES ($1, 'sam', 'x')`, sessUser)
	exec1(`INSERT INTO web_sessions (id, user_id, csrf_token, created_at, last_seen_at, expires_at)
	       VALUES ($1, $2, 'csrf-value', now(), now(), now() + interval '1 day')`, sessID, sessUser)

	// ---- an approved machine with its own role, and another one ----
	nodeID, otherID := uuid.NewString(), uuid.NewString()
	role := "maktaba_node_" + strings.ReplaceAll(nodeID, "-", "")
	exec1(`CREATE ROLE ` + role + ` LOGIN PASSWORD 'node-pass-123' IN ROLE maktaba_worker`)
	for _, n := range []struct{ id, role, priv string }{{nodeID, role, "node-secret-key"}, {otherID, "", "other-secret-key"}} {
		exec1(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, home_privkey, db_role, approved_at, approved_by,
		       full_node) VALUES ($1, 'studio', 'approved', 'windows', 'amd64', 'k', $2, NULLIF($3, ''), now(), 'test', true)`,
			n.id, n.priv, n.role)
	}
	// as the join hands it, with the home's CA it seals (config.NodeConfig.HomeDSN)
	nodeDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass-123"), Host: b.Addr(),
		Path: "/" + pgbundle.AppDatabase, RawQuery: url.Values{"sslmode": {"verify-ca"},
			"sslrootcert": {filepath.ToSlash(b.CACert())}}.Encode()}).String()

	// ---- this machine's copy, kept by the Manager alone ----
	c := Copy{NodeID: nodeID, HomeDSN: nodeDSN, Migrate: migrate,
		DB: pgbundle.Bundle{Data: filepath.Join(tmp, "node-pg"), Keys: filepath.Join(tmp, "node-keys"),
			Backups: filepath.Join(tmp, "node-backups"), Port: freePort(t)}}
	cp := &server{}
	var mlog bytes.Buffer
	var mlogMu sync.Mutex
	m := &Manager{Copy: c, StateDir: filepath.Join(tmp, "node-state"),
		Programs: func(context.Context) (string, error) { return pgHome, nil },
		Every:    300 * time.Millisecond, Retry: 300 * time.Millisecond, WipeAfter: 2, WipeOver: time.Nanosecond,
		Log: lockedWriter{w: &mlog, mu: &mlogMu}}
	db := func() pgbundle.Bundle { d := m.Copy.DB; d.Home = pgHome; return d }
	m.Want = func(on bool) error {
		if on {
			cp.start(t, db().Bin("postgres"), db().ServerArgs()...)
		} else {
			cp.stop(db().StopCommand())
		}
		return nil
	}
	m.Running = cp.running
	defer func() { cp.stop(db().StopCommand()) }()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	state := func() string { return m.Status().State }
	dump := func() string {
		mlogMu.Lock()
		defer mlogMu.Unlock()
		return "manager:\n" + mlog.String() + "\ncopy:\n" + cp.log.String()
	}
	following := func(what string) {
		t.Helper()
		end := time.Now().Add(4 * time.Minute)
		for state() != StateFollowing {
			if time.Now().After(end) {
				t.Fatalf("%s: the copy is %q (%s), want following\n%s", what, state(), m.Status().Detail, dump())
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	following("the first copy")

	dsn, err := c.DSN()
	if err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	// a change on the home shows on the copy
	exec1(`INSERT INTO app_settings (key, value, updated_at) VALUES ('test.copy', '"one"', now())
	       ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	seen := func(want string) func() bool {
		return func() bool {
			var v string
			err := copyDB.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'test.copy'`).Scan(&v)
			return err == nil && strings.Contains(v, want)
		}
	}
	waitFor(t, "the home's change on the copy", 30*time.Second, seen("one"))
	// what a sign-in needs is there; the secrets, other machines' rows and this
	// machine's keys are not
	var n int
	if err := copyDB.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("the copy's reader reads the users: %v", err)
	}
	_ = copyDB.QueryRowContext(ctx, `SELECT count(*) FROM home_secrets`).Scan(&n)
	if n != 0 {
		t.Fatalf("the copy holds %d of the home's secrets", n)
	}
	_ = copyDB.QueryRowContext(ctx, `SELECT count(*) FROM web_sessions`).Scan(&n)
	var keys int
	_ = copyDB.QueryRowContext(ctx, `SELECT count(*) FROM web_session_keys WHERE user_id = $1`, sessUser).Scan(&keys)
	if n != 0 || keys != 1 {
		t.Fatalf("the copy holds %d web sessions (want none) and %d of their hashes (want 1)", n, keys)
	}
	var nodes int
	var priv sql.NullString
	if err := copyDB.QueryRowContext(ctx, `SELECT count(*), max(home_privkey) FROM cluster_nodes`).Scan(&nodes, &priv); err != nil ||
		nodes != 1 || priv.Valid {
		t.Fatalf("cluster_nodes on the copy: %d rows, a key %v (%v)", nodes, priv.Valid, err)
	}
	// it writes nothing
	if _, err := copyDB.ExecContext(ctx, `UPDATE app_settings SET value = '"x"' WHERE key = 'test.copy'`); err == nil {
		t.Fatal("a write on the copy succeeded")
	}
	if _, err := os.Stat(filepath.Join(m.StateDir, StatusFile)); err != nil {
		t.Fatalf("no status file: %v", err)
	}
	waitFor(t, "the home told the copy follows", 30*time.Second, func() bool {
		var reported string
		_ = app.QueryRowContext(ctx, `SELECT replica->>'state' FROM cluster_nodes WHERE id = $1`, nodeID).Scan(&reported)
		return reported == StateFollowing
	})

	// the home's API stops, and its beat with it, while its database runs: the copy, its
	// slot caught up with the home's WAL, still follows, and takes the home's changes
	beating.Store(false)
	time.Sleep(FollowingWithin + 5*time.Second)
	if s := m.Status(); s.State != StateFollowing || !strings.Contains(s.Detail, "beat is late") {
		t.Fatalf("the beat late, the slot caught up: the copy is %q (%s)\n%s", s.State, s.Detail, dump())
	}
	exec1(`UPDATE app_settings SET value = '"late beat"' WHERE key = 'test.copy'`)
	waitFor(t, "a change while the beat is late", 30*time.Second, seen("late beat"))
	beating.Store(true)
	waitFor(t, "the beat back", 30*time.Second, func() bool { s := m.Status(); return s.State == StateFollowing && s.Detail == "" })

	// this machine updated (its schema moved on): a fresh copy while the subscription
	// still streams from the slot; it stops first, and the home drops the slot then
	updated, _ := c.Mark()
	stale := updated
	stale.LocalSchema--
	if err := c.writeMark(stale); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a fresh copy after an update", 3*time.Minute, func() bool {
		mk, ok := c.Mark()
		return ok && mk.Subscribed.After(updated.Subscribed) && mk.LocalSchema == updated.LocalSchema
	})
	following("after an update of this machine")
	exec1(`UPDATE app_settings SET value = '"updated"' WHERE key = 'test.copy'`)
	waitFor(t, "the change after the fresh copy", 30*time.Second, seen("updated"))

	// the home gives up on the slot (past the cap): the copy subscribes anew and follows
	before, _ := c.Mark()
	waitFor(t, "the slot dropped", 30*time.Second, func() bool {
		_, _ = app.ExecContext(ctx, `SELECT pg_terminate_backend(active_pid) FROM pg_replication_slots
		                              WHERE slot_name = $1 AND active`, c.Slot())
		_, err := app.ExecContext(ctx, `SELECT pg_drop_replication_slot($1)`, c.Slot())
		return err == nil
	})
	exec1(`UPDATE app_settings SET value = '"two"' WHERE key = 'test.copy'`)
	waitFor(t, "the copy subscribed anew", 3*time.Minute, func() bool {
		mk, ok := c.Mark()
		return ok && mk.Subscribed.After(before.Subscribed)
	})
	following("after the home gave up on the slot")
	waitFor(t, "the change on the new subscription", 30*time.Second, seen("two"))

	// revoked: the machine's role may not sign in; the copy is wiped
	exec1(`ALTER ROLE ` + role + ` NOLOGIN`)
	waitFor(t, "the copy wiped", 2*time.Minute, func() bool { return state() == StateRevoked })
	if c.DB.Initialized() || cp.running() {
		t.Fatalf("after the revocation: a database is kept %v, running %v", c.DB.Initialized(), cp.running())
	}
	if _, err := os.Stat(filepath.Join(c.DB.Keys, passwordFile)); err == nil {
		t.Fatal("the copy's password outlived the revocation")
	}
	mlogMu.Lock()
	t.Logf("manager:%c%s", 10, mlog.String())
	mlogMu.Unlock()
}
