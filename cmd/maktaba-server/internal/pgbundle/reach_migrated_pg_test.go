package pgbundle_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
)

// On a home migrated as maktaba-api migrates it, the link required: an approved
// machine's role is asked for its certificate once its machine said its processes present
// one (update_state's db_client_cert, as the home API keeps it) and its current certificate
// is valid and names that role; a machine that did not say so, one whose certificate is
// revoked, and one joined before 2026-10-18 (an 8-hex role) whose certificate names the
// 32-hex one keep their password alone.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install.
func TestCertRolesOnAMigratedHome(t *testing.T) {
	programs := pgtest.Programs(t)
	ctx := context.Background()
	dir := t.TempDir()
	b, _ := pgtest.Bundle(ctx, t, programs, dir, true)
	appURL, err := b.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := pgtest.Migrator(t, dir)(ctx, appURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	superURL, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", superURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const (
		presents = "maktaba_node_0b6f3c1e2a4d4e8f9c715d2a8b3e6f10"
		older    = "maktaba_node_1c7a4d2f3b5e4f90ad826e3b9c4f7a21"
		// joined before 2026-10-18: its role is its id's 8-hex prefix, its certificate's CN
		// the whole id's (cluster.RoleName)
		early   = "maktaba_node_2d8b5e3a"
		earlyID = "2d8b5e3a-4c6f-4a01-be93-7f4c0d5e8b32"
	)
	for i, m := range []struct{ role, cn, id string }{{presents, presents, ""}, {older, older, ""},
		{early, "maktaba_node_2d8b5e3a4c6f4a01be937f4c0d5e8b32", earlyID}} {
		id := m.id
		if id == "" {
			h := strings.TrimPrefix(m.role, "maktaba_node_")
			id = h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		}
		serial := []string{"0a", "0b", "0c"}[i]
		exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, cert_serial, cert_not_after,
		        remote_addr)
		    VALUES ($1, $2, 'approved', 'windows', 'amd64', 'cHVi', $3, $4, now() + interval '30 days', '192.168.50.94')`,
			id, "m"+serial, m.role, serial)
		exec(`INSERT INTO cluster_certs (serial, node_id, kind, subject, not_before, not_after)
		    VALUES ($1, $2, 'node', $3, now() - interval '1 hour', now() + interval '30 days')`, serial, id, m.cn)
	}
	exec(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.cluster', '{"link_mode": "require"}', now())
	    ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	// the home API's record of the first and the early machine's word (RecordDBClientCert)
	exec(`UPDATE cluster_nodes SET update_state = COALESCE(update_state, '{}'::jsonb) ||
	    jsonb_build_object('db_client_cert', true::boolean) WHERE db_role IN ($1, $2)`, presents, early)
	if err := os.WriteFile(filepath.Join(b.Keys, pgbundle.ClusterCAFile), []byte("CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.LAN = true
	look := func() string {
		t.Helper()
		if err := pgbundle.FollowReach(ctx, b, func() error { return nil }, io.Discard); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(b.Data, "pg_hba.conf"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	hba := look()
	if r := b.LastReach(); !r.Open || !r.Require || len(r.CertRoles) != 1 || r.CertRoles[0] != presents {
		t.Fatalf("the reach: %+v", r)
	}
	if !strings.Contains(hba, presents+" samenet         scram-sha-256 clientcert=verify-full") ||
		strings.Contains(hba, older) || strings.Contains(hba, early) ||
		!strings.Contains(hba, "+maktaba_worker  samenet         scram-sha-256\n") {
		t.Fatalf("one machine presenting its certificate, one whose certificate names another role:\n%s", hba)
	}
	// once the early machine's certificate names its role, it is asked for it too
	exec(`UPDATE cluster_certs SET subject = $1 WHERE serial = '0c'`, early)
	if hba := look(); !strings.Contains(hba, early+" samenet         scram-sha-256 clientcert=verify-full") {
		t.Fatalf("the early machine's certificate names its role:\n%s", hba)
	}
	exec(`UPDATE cluster_certs SET revoked_at = now() WHERE serial = '0c'`)
	exec(`UPDATE cluster_certs SET revoked_at = now() WHERE serial = '0a'`)
	if hba := look(); strings.Contains(hba, "clientcert") {
		t.Fatalf("its certificate revoked:\n%s", hba)
	}
}
