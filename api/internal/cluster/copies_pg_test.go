package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home's side of a machine's copy (slot 0142, specs/epics/37-node-replica), on a
// home set up as the Postgres bundled with Maktaba.app sets it up: the app role owns
// the schema, has run the migrations and was given, at the cluster bootstrap, the
// right to make roles and slots and the replicas' group. A machine's own database role
// asks for its copy; revoking the machine ends it, removing it drops its role.
func TestAMachineGetsItsCopy(t *testing.T) {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var exists bool
	if err := admin.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname IN ('maktaba_worker', 'maktaba_replicas'))`).Scan(&exists); err != nil {
		t.Skipf("scratch Postgres unreachable: %v", err)
	}
	if exists {
		t.Skip("needs a Postgres server where maktaba_worker and maktaba_replicas do not exist yet (a fresh one)")
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	app, dbName := "app_"+suffix, "t_copy_"+suffix
	nodeID, otherID := uuid.NewString(), uuid.NewString()
	nodeRole, otherRole := RoleName(nodeID), RoleName(otherID)
	copyRole, slot, _ := CopyNames(nodeRole)
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// the bundled Postgres's cluster bootstrap (cmd/maktaba-server pgbundle clusterSQL)
	exec(admin, `CREATE ROLE `+app+` LOGIN PASSWORD 'app-pass'`)
	exec(admin, `ALTER ROLE `+app+` WITH CREATEROLE REPLICATION`)
	exec(admin, `GRANT pg_monitor, pg_signal_backend TO `+app)
	exec(admin, `CREATE ROLE maktaba_replicas NOLOGIN`)
	exec(admin, `GRANT pg_read_all_data TO maktaba_replicas`)
	exec(admin, `GRANT maktaba_replicas TO `+app+` WITH ADMIN OPTION, INHERIT FALSE, SET FALSE`)
	exec(admin, `CREATE DATABASE `+dbName+` OWNER `+app)
	t.Cleanup(func() {
		_, _ = admin.Exec(`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name LIKE 'maktaba\_copy\_%' AND NOT active`)
		_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, dbName)
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + dbName); err != nil {
			t.Logf("drop %s: %v", dbName, err)
		}
		for _, r := range []string{nodeRole, otherRole, copyRole, "maktaba_rep_" + strings.TrimPrefix(otherRole, "maktaba_node_"),
			"maktaba_worker", "maktaba_replicas", app} {
			if _, err := admin.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(r)); err != nil {
				t.Logf("drop role %s: %v", r, err)
			}
		}
	})
	sup, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()
	for _, x := range []string{"pg_trgm", "unaccent", "pgcrypto"} {
		exec(sup, `CREATE EXTENSION IF NOT EXISTS `+x)
	}
	home, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, app, "app-pass"))
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	clustertest.MigrateTo(t, home, 0)

	// two approved machines, each with its own role
	roles := &PGRoles{DB: home}
	for _, n := range []struct{ id, role string }{{nodeID, nodeRole}, {otherID, otherRole}} {
		if err := roles.Create(context.Background(), n.role, "node-pass-"+suffix); err != nil {
			t.Fatal(err)
		}
		exec(home, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by)
		       VALUES ($1, 'n', 'approved', 'windows', 'amd64', 'k', $2, now(), 'test')`, n.id, n.role)
	}
	node, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, nodeRole, "node-pass-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	type grant struct {
		Role, Slot, Database, Node string
		SlotMade                   bool    `json:"slot_made"`
		SystemID                   string  `json:"system_identifier"`
		Version                    int     `json:"server_version_num"`
		CA                         *string `json:"ca_pem"`
	}
	ask := func(db *sql.DB, id, pw string, fresh bool) (grant, error) {
		var raw []byte
		err := db.QueryRow(`SELECT cluster_node_replica_grant($1, $2, $3)`, id, pw, fresh).Scan(&raw)
		var g grant
		if err == nil {
			err = json.Unmarshal(raw, &g)
		}
		return g, err
	}
	pw := strings.Repeat("p", 40)
	g, err := ask(node, nodeID, pw, false)
	if err != nil {
		t.Fatalf("a machine asks for its copy: %v", err)
	}
	if g.Role != copyRole || g.Slot != slot || !g.SlotMade || g.SystemID == "" || g.Version < 160000 ||
		g.Database != dbName || g.CA != nil {
		t.Fatalf("grant: %+v", g)
	}
	// its replication role reads everything, from the home too (it streams every change already)
	cp, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, copyRole, pw))
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	var n int
	if err := cp.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("the copy's role reads the users: %v", err)
	}
	if _, err := cp.Exec(`UPDATE cluster_replica_beat SET at = now()`); err == nil {
		t.Fatal("the copy's role may write")
	}
	// again: the same slot; fresh: made anew
	if g, err := ask(node, nodeID, pw, false); err != nil || g.SlotMade {
		t.Fatalf("asked again: %+v %v", g, err)
	}
	if g, err := ask(node, nodeID, pw, true); err != nil || !g.SlotMade {
		t.Fatalf("asked fresh: %+v %v", g, err)
	}
	// a machine asks for another's copy, or with a short password: refused
	if _, err := ask(node, otherID, pw, false); !isCode(err, "42501") {
		t.Fatalf("another machine's copy: %v, want insufficient_privilege", err)
	}
	if _, err := ask(node, nodeID, "short", false); !isCode(err, "22023") {
		t.Fatalf("a short password: %v, want invalid_parameter_value", err)
	}

	// the home's loop: the beat (a slot exists), the CA, the cap
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec(home, `UPDATE cluster_replica_beat SET at = '2000-01-01'`)
	c := &Copies{DB: home, CAFile: ca}
	c.beat(context.Background())
	c.publishCA(context.Background())
	var at time.Time
	var pem sql.NullString
	if err := home.QueryRow(`SELECT at, ca_pem FROM cluster_replica_beat`).Scan(&at, &pem); err != nil ||
		time.Since(at) > time.Minute || !strings.Contains(pem.String, "BEGIN CERTIFICATE") {
		t.Fatalf("beat %v, ca %q, %v", at, pem.String, err)
	}
	if g, _ := ask(node, nodeID, pw, false); g.CA == nil || !strings.Contains(*g.CA, "BEGIN CERTIFICATE") {
		t.Fatalf("the grant does not carry the CA: %+v", g)
	}
	exec(sup, `SELECT pg_switch_wal()`) // past one byte of kept changes
	exec(home, `UPDATE cluster_replica_beat SET at = now()`)
	c.dropOver(context.Background(), 1)
	var slots int
	_ = home.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&slots)
	if slots != 0 {
		t.Fatal("a slot past the cap was kept")
	}
	if g, err := ask(node, nodeID, pw, false); err != nil || !g.SlotMade {
		t.Fatalf("after the cap the slot is made anew (the copy rebuilds): %+v %v", g, err)
	}

	// revoked: the copy's role may not sign in, its slot is gone; removed: its role too
	if _, err := roles.Disable(context.Background(), nodeRole); err != nil {
		t.Fatal(err)
	}
	var login bool
	_ = home.QueryRow(`SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, copyRole).Scan(&login)
	_ = home.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&slots)
	if login || slots != 0 {
		t.Fatalf("after the revocation: the copy's role may sign in %v, slots %d", login, slots)
	}
	if err := roles.Drop(context.Background(), nodeRole); err != nil {
		t.Fatal(err)
	}
	var left bool
	_ = home.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, copyRole).Scan(&left)
	if left {
		t.Fatal("the copy's role outlived the machine")
	}
}

func isCode(err error, code string) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && string(pe.Code) == code
}
