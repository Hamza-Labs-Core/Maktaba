package pgbundle

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// WatchReach against a real server (on loopback: the restart it asks for is the test's,
// which keeps it there): it opens when joins open, adds an approved machine's address
// with a reload, closes when no machine may connect, and changes nothing on a look the
// database does not answer.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install.
func TestWatchReachFollowsTheCluster(t *testing.T) {
	home := os.Getenv("MAKTABA_TEST_PG_BIN")
	if home == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	if testing.Short() {
		t.Skip("runs a Postgres server")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	root, err := os.MkdirTemp("", "mkreach") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	b := Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: port, LAN: true, TLS: true}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv, err := b.serverCommand(b.serverArgs(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.StopCommand().Run(); _ = srv.Wait() })
	deadline := time.Now().Add(60 * time.Second)
	for Ready(ctx, b.Addr(), AppRole, AppDatabase) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the server is not ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	q, err := b.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	sql := func(s string) {
		t.Helper()
		if err := q.Exec(ctx, AppDatabase, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	restarts := 0
	restart := func() error { restarts++; return nil }
	look := func() {
		t.Helper()
		if err := b.followReach(ctx, restart, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	hba := func() string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(b.Data, "pg_hba.conf"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	// the tables not made yet (the API migrates them): nothing changes
	if err := b.followReach(ctx, restart, io.Discard); err == nil {
		t.Fatal("a look without the cluster's tables answered")
	}
	if b.LastReach().Open || restarts != 0 {
		t.Fatalf("a failed look changed what the machines reach: %+v, %d restarts", b.LastReach(), restarts)
	}
	sql(`CREATE TABLE instance_identity (id smallint PRIMARY KEY, accept_joins boolean NOT NULL DEFAULT false,
	     accept_joins_until timestamptz);
	     CREATE TABLE cluster_nodes (id serial PRIMARY KEY, state text NOT NULL, remote_addr text);
	     INSERT INTO instance_identity (id) VALUES (1);`)

	// no machine: closed, as it was
	look()
	if b.LastReach().Open || restarts != 0 || strings.Contains(hba(), "maktaba_worker") {
		t.Fatalf("no machine: %+v, %d restarts:\n%s", b.LastReach(), restarts, hba())
	}
	// joins open, their hour not up: it opens (a start again), but not while a maintenance
	// run (the update's dump, its migrations) holds the server: the look after it does
	sql(`UPDATE instance_identity SET accept_joins = true, accept_joins_until = now() + interval '1 hour'`)
	release := Hold()
	look()
	if b.LastReach().Open || restarts != 0 {
		t.Fatalf("held: %+v, %d restarts", b.LastReach(), restarts)
	}
	release()
	release() // once only
	look()
	if !b.LastReach().Open || restarts != 1 || !strings.Contains(hba(), "+maktaba_worker  samenet") {
		t.Fatalf("joins open: %+v, %d restarts:\n%s", b.LastReach(), restarts, hba())
	}
	// a machine approved from another network: its address, by a reload alone
	sql(`INSERT INTO cluster_nodes (state, remote_addr) VALUES ('approved', '100.64.0.9'), ('pending', '100.64.0.10')`)
	look()
	if restarts != 1 || !strings.Contains(hba(), "100.64.0.9/32") || strings.Contains(hba(), "100.64.0.10") {
		t.Fatalf("a machine approved: %d restarts:\n%s", restarts, hba())
	}
	look() // nothing new: nothing done
	if restarts != 1 {
		t.Fatalf("an unchanged look started it again (%d)", restarts)
	}
	// the link required (story 35.16): the machine's role needs its certificate only once
	// its machine says it presents one and it holds a valid one; a database without those
	// columns (the look above) needed none
	if err := os.WriteFile(filepath.Join(b.Keys, ClusterCAFile), []byte("CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	const role = "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10"
	sql(`CREATE TABLE app_settings (key text PRIMARY KEY, value jsonb NOT NULL);
	     INSERT INTO app_settings VALUES ('rules.cluster', '{"link_mode": "require"}');
	     ALTER TABLE cluster_nodes ADD COLUMN db_role text, ADD COLUMN cert_serial text,
	       ADD COLUMN cert_not_after timestamptz, ADD COLUMN update_state jsonb NOT NULL DEFAULT '{}';
	     CREATE TABLE cluster_certs (serial text PRIMARY KEY, not_before timestamptz NOT NULL,
	       not_after timestamptz NOT NULL, revoked_at timestamptz);
	     INSERT INTO cluster_certs VALUES ('0a', now() - interval '1 hour', now() + interval '30 days', NULL);
	     UPDATE cluster_nodes SET db_role = '` + role + `', cert_serial = '0a', cert_not_after = now() + interval '30 days'
	       WHERE state = 'approved';`)
	look()
	if !b.LastReach().Require || restarts != 2 || strings.Contains(hba(), "clientcert") {
		t.Fatalf("required, the machine not saying it presents its certificate: %+v, %d restarts:\n%s",
			b.LastReach(), restarts, hba())
	}
	sql(`UPDATE cluster_nodes SET update_state = update_state || '{"db_client_cert": true}' WHERE state = 'approved'`)
	look()
	if restarts != 2 || !strings.Contains(hba(), role+" samenet         scram-sha-256 clientcert=verify-full") ||
		!strings.Contains(hba(), role+" 100.64.0.9/32   scram-sha-256 clientcert=verify-full") {
		t.Fatalf("the machine presents its certificate (a reload alone): %d restarts:\n%s", restarts, hba())
	}
	// its certificate revoked, or its machine saying it no more (an older release again):
	// its password alone
	sql(`UPDATE cluster_certs SET revoked_at = now()`)
	look()
	if strings.Contains(hba(), "clientcert") || len(b.LastReach().CertRoles) != 0 {
		t.Fatalf("its certificate revoked: %+v:\n%s", b.LastReach(), hba())
	}
	sql(`UPDATE cluster_certs SET revoked_at = NULL;
	     UPDATE cluster_nodes SET update_state = update_state || '{"db_client_cert": false}'`)
	look()
	if strings.Contains(hba(), "clientcert") || restarts != 2 {
		t.Fatalf("the machine no more presenting its certificate: %d restarts:\n%s", restarts, hba())
	}
	// joins whose hour is up still count for nothing, the machine still does
	sql(`UPDATE instance_identity SET accept_joins_until = now() - interval '1 minute'`)
	look()
	if !b.LastReach().Open || restarts != 2 {
		t.Fatalf("the machine still approved: %+v, %d restarts", b.LastReach(), restarts)
	}
	// revoked, joins closed: this machine only again
	sql(`UPDATE cluster_nodes SET state = 'revoked'`)
	look()
	if b.LastReach().Open || restarts != 3 || strings.Contains(hba(), "maktaba_worker") {
		t.Fatalf("no machine left: %+v, %d restarts:\n%s", b.LastReach(), restarts, hba())
	}
	if args := strings.Join(b.ServerArgs(), " "); !strings.Contains(args, "listen_addresses=127.0.0.1") {
		t.Fatalf("its next start, with no machine: %s", args)
	}
}
