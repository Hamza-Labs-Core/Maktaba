package cluster

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home's side of a machine's copy (slot 0147, specs/epics/37-node-replica), on a
// home set up as the Postgres bundled with Maktaba.app sets it up: the app role owns
// the schema, has run the migrations and was given, at the cluster bootstrap, the
// right to make roles and slots and the replicas' group. A machine's own database role
// asks for its copy: its role, its publication (every table but the secrets and other
// machines' rows); revoking the machine ends it, removing it drops its role. Taken as a
// database that is not Maktaba's own Postgres, the home keeps copies only with the rule
// (rules.cluster.copies_on_operator_postgres). The server must run with wal_level=logical.
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
	sessUser, sessID := uuid.NewString(), uuid.NewString()
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
	exec(admin, `GRANT maktaba_replicas TO `+app+` WITH ADMIN OPTION, INHERIT FALSE, SET FALSE`)
	exec(admin, `GRANT ALTER SYSTEM ON PARAMETER max_slot_wal_keep_size TO `+app)

	exec(admin, `CREATE DATABASE `+dbName+` OWNER `+app)
	t.Cleanup(func() {
		// this test's slots only: its database's, and its physical one (another test's
		// slots on the same server are left alone)
		_, _ = admin.Exec(`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
		    WHERE (database = $1 OR slot_name = $2) AND NOT active`, dbName, "maktaba_test_physical_"+suffix)
		_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, dbName)
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + dbName); err != nil {
			t.Logf("drop %s: %v", dbName, err)
		}
		_, _ = admin.Exec(`ALTER SYSTEM RESET max_slot_wal_keep_size`)
		_, _ = admin.Exec(`SELECT pg_reload_conf()`)
		_, _ = admin.Exec(`REVOKE ALTER SYSTEM ON PARAMETER max_slot_wal_keep_size FROM ` + app)
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
	// as the cluster bootstrap does, in the app's database (a function's grant is its
	// database's): and the SQL functions of slots are the app role's alone
	exec(sup, `GRANT EXECUTE ON FUNCTION pg_reload_conf() TO `+app)
	rows, err := sup.Query(`SELECT p.oid::regprocedure::text FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
	    WHERE n.nspname = 'pg_catalog' AND p.proname IN ('pg_create_logical_replication_slot',
	          'pg_create_physical_replication_slot', 'pg_copy_logical_replication_slot', 'pg_copy_physical_replication_slot',
	          'pg_logical_slot_get_changes', 'pg_logical_slot_get_binary_changes', 'pg_logical_slot_peek_changes',
	          'pg_logical_slot_peek_binary_changes', 'pg_replication_slot_advance', 'pg_drop_replication_slot')`)
	if err != nil {
		t.Fatal(err)
	}
	var slotFuncs []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			t.Fatal(err)
		}
		slotFuncs = append(slotFuncs, f)
	}
	_ = rows.Close()
	for _, f := range slotFuncs {
		exec(sup, `REVOKE EXECUTE ON FUNCTION `+f+` FROM PUBLIC`)
		exec(sup, `GRANT EXECUTE ON FUNCTION `+f+` TO `+app)
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
		exec(home, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by,
		       full_node) VALUES ($1, 'n', 'approved', 'windows', 'amd64', 'k', $2, now(), 'test', true)`, n.id, n.role)
	}
	node, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, nodeRole, "node-pass-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	type grant struct {
		Role, Slot, Database, Node, Publication string
		SlotExists                              bool     `json:"slot_exists"`
		Tables                                  []string `json:"tables"`
		SchemaVersion                           int64    `json:"schema_version"`
		SystemID                                string   `json:"system_identifier"`
		Version                                 int      `json:"server_version_num"`
		CA                                      *string  `json:"ca_pem"`
	}
	// a machine sends its password's SCRAM verifier, never the password
	askWith := func(db *sql.DB, id, verifier string, fresh bool) (grant, error) {
		var raw []byte
		err := db.QueryRow(`SELECT cluster_node_replica_grant($1, $2, $3)`, id, verifier, fresh).Scan(&raw)
		var g grant
		if err == nil {
			err = json.Unmarshal(raw, &g)
		}
		return g, err
	}
	ask := func(db *sql.DB, id, pw string, fresh bool) (grant, error) {
		return askWith(db, id, scramVerifier(t, pw), fresh)
	}
	pw := strings.Repeat("p", 40)
	g, err := ask(node, nodeID, pw, false)
	if err != nil {
		t.Fatalf("a machine asks for its copy: %v", err)
	}
	if g.Role != copyRole || g.Slot != slot || g.Publication != slot || g.SlotExists || g.SystemID == "" ||
		g.Version < 160000 || g.Database != dbName || g.CA != nil || g.SchemaVersion < 147 {
		t.Fatalf("grant: %+v", g)
	}
	published := map[string]bool{}
	for _, tab := range g.Tables {
		published[tab] = true
	}
	for _, want := range []string{"users", "web_session_keys", "videos", "cluster_replica_beat", "cluster_nodes"} {
		if !published[want] {
			t.Fatalf("%s is not published", want)
		}
	}
	// a session's id and CSRF token are live cookies: a copy holds their hashes only
	for _, never := range []string{"web_sessions", "home_secrets", "refresh_tokens", "tv_cluster_secret", "goose_db_version"} {
		if published[never] {
			t.Fatalf("%s is published", never)
		}
	}
	// its replication role reads what is published, from the home too, and nothing else
	cp, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, copyRole, pw))
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	var n int
	if err := cp.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("the copy's role reads the users: %v", err)
	}
	if err := cp.QueryRow(`SELECT count(*) FROM home_secrets`).Scan(&n); !isCode(err, "42501") {
		t.Fatalf("the copy's role reads the home's secrets: %v", err)
	}
	if err := cp.QueryRow(`SELECT count(*) FROM web_sessions`).Scan(&n); !isCode(err, "42501") {
		t.Fatalf("the copy's role reads the home's session cookies: %v", err)
	}
	// a session as a copy holds it: the hashes of its id and CSRF token, kept by trigger
	exec(home, `INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'sam', 'x', false)`, sessUser)
	exec(home, `INSERT INTO web_sessions (id, user_id, csrf_token, created_at, last_seen_at, expires_at)
	       VALUES ($1, $2, 'csrf-value', now(), now(), now() + interval '1 day')`, sessID, sessUser)
	var idHash, csrfHash string
	if err := cp.QueryRow(`SELECT id_sha256, csrf_sha256 FROM web_session_keys WHERE user_id = $1`, sessUser).
		Scan(&idHash, &csrfHash); err != nil || idHash != sha256Hex(sessID) || csrfHash != sha256Hex("csrf-value") {
		t.Fatalf("the session's hashes: %q %q %v", idHash, csrfHash, err)
	}
	exec(home, `UPDATE web_sessions SET revoked_at = now() WHERE id = $1`, sessID)
	var revoked bool
	_ = cp.QueryRow(`SELECT revoked_at IS NOT NULL FROM web_session_keys WHERE id_sha256 = $1`, idHash).Scan(&revoked)
	exec(home, `DELETE FROM web_sessions WHERE id = $1`, sessID)
	var kept int
	_ = cp.QueryRow(`SELECT count(*) FROM web_session_keys WHERE id_sha256 = $1`, idHash).Scan(&kept)
	if !revoked || kept != 0 {
		t.Fatalf("the session's hashes follow it: revoked %v, kept %d", revoked, kept)
	}
	if err := cp.QueryRow(`SELECT count(*) FROM cluster_nodes WHERE home_privkey IS NOT NULL`).Scan(&n); !isCode(err, "42501") {
		t.Fatalf("the copy's role reads the machines' keys: %v", err)
	}
	if _, err := cp.Exec(`UPDATE cluster_replica_beat SET at = now()`); err == nil {
		t.Fatal("the copy's role may write")
	}
	// nor does it make, read or move a slot with SQL (its REPLICATION would decode every
	// table with test_decoding, past its publication): it follows over the replication
	// protocol, where the only output plugin Maktaba's Postgres has is pgoutput
	if _, err := cp.Exec(`SELECT pg_create_logical_replication_slot('maktaba_test_evil', 'test_decoding')`); !isCode(err, "42501") {
		t.Fatalf("the copy's role makes a slot with SQL: %v", err)
	}
	if _, err := cp.Exec(`SELECT * FROM pg_logical_slot_peek_changes('maktaba_test_evil', NULL, NULL)`); !isCode(err, "42501") {
		t.Fatalf("the copy's role reads a slot with SQL: %v", err)
	}
	// the subscription makes the slot (here by hand); fresh drops a slot left from before
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	if g, err := ask(node, nodeID, pw, false); err != nil || !g.SlotExists {
		t.Fatalf("asked again: %+v %v", g, err)
	}
	if g, err := ask(node, nodeID, pw, true); err != nil || g.SlotExists {
		t.Fatalf("asked fresh: %+v %v", g, err)
	}
	// a machine asks for another's copy, or with a short password: refused
	if _, err := ask(node, otherID, pw, false); !isCode(err, "42501") {
		t.Fatalf("another machine's copy: %v, want insufficient_privilege", err)
	}
	if _, err := askWith(node, nodeID, strings.Repeat("p", 40), false); !isCode(err, "22023") {
		t.Fatalf("a password, not its verifier: %v, want invalid_parameter_value", err)
	}
	// a worker-only machine keeps no copy
	exec(home, `UPDATE cluster_nodes SET full_node = false WHERE id = $1`, nodeID)
	if _, err := ask(node, nodeID, pw, false); !isCode(err, "MK403") {
		t.Fatalf("a machine that is not a full one: %v, want MK403", err)
	}
	exec(home, `UPDATE cluster_nodes SET full_node = true WHERE id = $1`, nodeID)

	// a full machine is the home admin's decision (slot 0151): the machine's own report
	// says it can be one, and gives it no copy until an admin allowed it
	exec(home, `UPDATE cluster_nodes SET full_node = false, full_capable = false, full_allowed = false WHERE id = $1`, nodeID)
	report := func() {
		t.Helper()
		if _, err := node.Exec(`SELECT cluster_node_report_full($1, '', '', '', true, NULL)`, nodeID); err != nil {
			t.Fatalf("the machine's report: %v", err)
		}
	}
	fullOf := func() (full, capable bool) {
		t.Helper()
		if err := home.QueryRow(`SELECT full_node, full_capable FROM cluster_nodes WHERE id = $1`, nodeID).Scan(&full, &capable); err != nil {
			t.Fatal(err)
		}
		return full, capable
	}
	report()
	if full, capable := fullOf(); full || !capable {
		t.Fatalf("a machine made itself full: full %v, capable %v", full, capable)
	}
	if _, err := ask(node, nodeID, pw, false); !isCode(err, "MK403") {
		t.Fatalf("a copy for a machine no admin allowed: %v, want MK403", err)
	}
	// nor can the machine allow itself
	if _, err := node.Exec(`UPDATE cluster_nodes SET full_allowed = true WHERE id = $1`, nodeID); !isCode(err, "42501") {
		t.Fatalf("the machine allowed itself: %v, want insufficient_privilege", err)
	}
	// the admin allows it: the machine's next report makes it full
	exec(home, `UPDATE cluster_nodes SET full_allowed = true WHERE id = $1`, nodeID)
	report()
	if full, _ := fullOf(); !full {
		t.Fatal("an allowed, capable machine is not full")
	}

	// the home's loop: the beat (a slot exists), the CA, the cap
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec(home, `UPDATE cluster_replica_beat SET at = '2000-01-01'`)
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`) // a machine follows
	// not Bundled (an idle physical slot is checked both ways below): copies allowed by the rule
	c := &Copies{DB: home, CAFile: ca, Logger: slog.Default(), OperatorPG: ruleOn}
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
	if g, err := ask(node, nodeID, pw, false); err != nil || g.SlotExists {
		t.Fatalf("after the cap the grant says the slot is gone (the copy subscribes anew): %+v %v", g, err)
	}
	// Postgres's own cap gave a slot's changes up while its machine was away (wal_status
	// lost): no subscription can follow it again, and dropOver does not see it (it has no
	// restart_lsn). The home drops it, so the machine's next ask hears its slot is gone
	// and its copy is made anew; the Machines page says the copy is rebuilding meanwhile
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	exec(admin, `ALTER SYSTEM SET max_slot_wal_keep_size = '1MB'`)
	exec(admin, `SELECT pg_reload_conf()`)
	exec(sup, `CREATE TABLE IF NOT EXISTS wal_filler (x text)`)
	lost := func() string {
		var st sql.NullString
		_ = home.QueryRow(`SELECT wal_status FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&st)
		return st.String
	}
	for i := 0; lost() != "lost"; i++ {
		if i == 50 {
			t.Fatalf("the slot is %q after 50 WAL segments, want lost", lost())
		}
		exec(sup, `INSERT INTO wal_filler SELECT repeat(md5(random()::text), 2000) FROM generate_series(1, 20)`)
		exec(sup, `SELECT pg_switch_wal()`)
		exec(admin, `CHECKPOINT`)
	}
	exec(sup, `DROP TABLE wal_filler`) // not the app's: no publication may hold it
	exec(admin, `ALTER SYSTEM RESET max_slot_wal_keep_size`)
	exec(admin, `SELECT pg_reload_conf()`)
	c.dropLost(context.Background())
	_ = home.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&slots)
	if slots != 0 {
		t.Fatal("a lost slot was kept")
	}
	var copyState, copyDetail string
	if err := home.QueryRow(`SELECT replica->>'state', replica->>'detail' FROM cluster_nodes WHERE id = $1`, nodeID).
		Scan(&copyState, &copyDetail); err != nil || copyState != "rebuilding" || !strings.Contains(copyDetail, "lost") {
		t.Fatalf("the machine's copy after its slot was lost: %q (%q) %v, want rebuilding", copyState, copyDetail, err)
	}
	if g, err := ask(node, nodeID, pw, false); err != nil || g.SlotExists {
		t.Fatalf("after a lost slot the grant says the slot is gone (the copy subscribes anew): %+v %v", g, err)
	}
	// Postgres holds the cap too (it holds with the API down)
	c.KeepGB = func() int { return 7 }
	c.holdCap(context.Background())
	waitSetting := func(want string) {
		t.Helper()
		var got string
		for i := 0; i < 50; i++ {
			_ = admin.QueryRow(`SHOW max_slot_wal_keep_size`).Scan(&got)
			if got == want {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("max_slot_wal_keep_size = %q, want %q", got, want)
	}
	waitSetting("7GB")
	c.KeepGB = func() int { return 0 } // no cap
	c.holdCap(context.Background())
	waitSetting("-1")

	// the slots that are not Maktaba's are dropped; Maktaba's stay. Slot names are the
	// server's, not the database's: this test's are its own (its database's oid, its
	// suffix), so another test's on the same server never meets them
	nodeHex := strings.ReplaceAll(nodeID, "-", "")
	strayHex := strings.ReplaceAll(uuid.NewString(), "-", "")
	var dbOID int64
	if err := home.QueryRow(`SELECT oid FROM pg_database WHERE datname = current_database()`).Scan(&dbOID); err != nil {
		t.Fatal(err)
	}
	fork, tableSync := fmt.Sprintf("maktaba_fork_%d", dbOID), fmt.Sprintf("pg_%d_sync_16500_7301", dbOID)
	stray, decoder, physical := "maktaba_test_stray_"+suffix, "maktaba_test_decoder_"+suffix, "maktaba_test_physical_"+suffix
	logical := map[string]string{
		"maktaba_copy_" + nodeHex:  "pgoutput",      // an approved full machine's: kept
		"maktaba_copy_" + strayHex: "pgoutput",      // no such machine: dropped
		fork:                       "pgoutput",      // an emergency's fork: kept
		tableSync:                  "pgoutput",      // a table's first copy: kept
		stray:                      "pgoutput",      // not Maktaba's: dropped
		decoder:                    "test_decoding", // decodes every table: dropped
	}
	for name, plugin := range logical {
		exec(sup, `SELECT pg_create_logical_replication_slot($1, $2)`, name, plugin)
	}
	exec(sup, `SELECT pg_create_physical_replication_slot($1)`, physical)
	slotsLeft := func() map[string]bool {
		out := map[string]bool{}
		rows, err := sup.Query(`SELECT slot_name FROM pg_replication_slots`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			out[s] = true
		}
		return out
	}
	c.guardSlots(context.Background())
	got := slotsLeft()
	for name, keep := range map[string]bool{"maktaba_copy_" + nodeHex: true, "maktaba_copy_" + strayHex: false,
		fork: true, tableSync: true, stray: false, decoder: false, physical: true} {
		if got[name] != keep {
			t.Fatalf("slot %s kept %v, want %v", name, got[name], keep)
		}
	}
	// on Maktaba's own Postgres an idle physical slot is no one's either
	c.Bundled = true
	c.guardSlots(context.Background())
	if slotsLeft()[physical] {
		t.Fatal("an idle physical slot was kept on Maktaba's own Postgres")
	}
	exec(sup, `SELECT pg_drop_replication_slot('maktaba_copy_`+nodeHex+`')`)

	// on a database that is not Maktaba's own Postgres, without the rule: the machines may
	// not ask for a copy (the grant is not theirs to call), and the copies they have end:
	// their replication role, slot and publication, and a table's first copy; an
	// emergency's fork stays. Each machine refused is logged once.
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	var logs strings.Builder
	off := &Copies{DB: home, Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	off.guardSlots(context.Background())
	off.guardSlots(context.Background())
	if _, err := ask(node, nodeID, pw, false); !isCode(err, "42501") || !strings.Contains(err.Error(), "cluster_node_replica_grant") {
		t.Fatalf("a machine asks with copies refused: %v, want permission denied for the grant", err)
	}
	var roleLeft bool
	var pubsLeft int
	_ = home.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, copyRole).Scan(&roleLeft)
	_ = home.QueryRow(`SELECT count(*) FROM pg_publication WHERE pubname = $1`, slot).Scan(&pubsLeft)
	got = slotsLeft()
	if roleLeft || pubsLeft != 0 || got[slot] || got[tableSync] || !got[fork] {
		t.Fatalf("with copies refused: role %v, publications %d, slot %v, a table's first copy %v, the fork %v",
			roleLeft, pubsLeft, got[slot], got[tableSync], got[fork])
	}
	if n := strings.Count(logs.String(), "event=cluster_copy_refused"); n != 2 || !strings.Contains(logs.String(), "event=cluster_copies_shut") {
		t.Fatalf("refused (two full machines, each once; the door shut once):\n%s", logs.String())
	}
	// the admin allows copies on such a database: the machines may ask again (said once)
	off.OperatorPG = ruleOn
	off.guardSlots(context.Background())
	off.guardSlots(context.Background())
	if g, err := ask(node, nodeID, pw, false); err != nil || g.SlotExists {
		t.Fatalf("a machine asks with the rule on: %+v %v", g, err)
	}
	if !strings.Contains(logs.String(), "event=cluster_copies_open") ||
		strings.Count(logs.String(), "event=cluster_copies_operator_postgres") != 1 {
		t.Fatalf("allowed (the door open, copies on an operator's Postgres said once):\n%s", logs.String())
	}
	// Maktaba's own Postgres needs no rule
	off.OperatorPG, off.Bundled = nil, true
	off.guardSlots(context.Background())
	if _, err := ask(node, nodeID, pw, false); err != nil {
		t.Fatalf("a machine asks on Maktaba's own Postgres without the rule: %v", err)
	}

	// revoked: the copy's role may not sign in, its slot and publication are gone;
	// removed: its role too
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	if _, err := roles.Disable(context.Background(), nodeRole); err != nil {
		t.Fatal(err)
	}
	var login bool
	var pubs int
	_ = home.QueryRow(`SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, copyRole).Scan(&login)
	_ = home.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&slots)
	_ = home.QueryRow(`SELECT count(*) FROM pg_publication WHERE pubname = $1`, slot).Scan(&pubs)
	if login || slots != 0 || pubs != 0 {
		t.Fatalf("after the revocation: the copy's role may sign in %v, slots %d, publications %d", login, slots, pubs)
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

// On a database that is not Maktaba's own Postgres the machines keep no copy without the
// rule: the grant is not the machines' group's to call, a copy a machine had ends (its
// replication role, publication and slot) and each machine refused is logged once; with the
// rule, or on Maktaba's own Postgres, they may ask again; a rule that could not be read
// changes nothing. A home whose database user does not own the grant cannot shut it, and
// says so once. On a migrated scratch database (the integration tier's server will do; its
// slot only with wal_level=logical).
func TestCopiesOnAnOperatorsPostgres(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	nodeID := uuid.NewString()
	role := RoleName(nodeID)
	copyRole, slot, _ := CopyNames(role)
	app := "app_" + strings.TrimPrefix(role, "maktaba_node_")[:12]
	roles := &PGRoles{DB: db}
	if err := roles.Create(ctx, role, "node-pass-x"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = roles.Drop(context.Background(), role)
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(copyRole))
		_, _ = db.Exec(`DROP OWNED BY ` + pq.QuoteIdentifier(app))
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(app))
	})
	exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by, full_node)
	      VALUES ($1, 'studio', 'approved', 'linux', 'amd64', 'k', $2, now(), 'test', true)`, nodeID, role)
	// the copy it has: its replication role, its publication, its slot (with logical decoding)
	exec(`CREATE ROLE ` + copyRole + ` LOGIN REPLICATION`)
	exec(`CREATE PUBLICATION ` + slot)
	var level string
	_ = db.QueryRow(`SHOW wal_level`).Scan(&level)
	if level == "logical" {
		exec(`SELECT pg_create_logical_replication_slot($1, 'pgoutput')`, slot)
	}
	node, err := sql.Open("postgres", clustertest.WithUser(t, dsn, role, "node-pass-x"))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	verifier := scramVerifier(t, strings.Repeat("p", 40))
	// denied: the grant is not this machine's to call (any other answer is the grant's own)
	denied := func() error {
		_, err := node.Exec(`SELECT cluster_node_replica_grant($1, $2, false)`, nodeID, verifier)
		if isCode(err, "42501") && strings.Contains(err.Error(), "cluster_node_replica_grant") {
			return nil
		}
		return fmt.Errorf("not denied: %v", err)
	}
	if denied() == nil {
		t.Fatal("the machine may not ask for its copy before any rule was read")
	}
	hasCopy := func() bool {
		var left bool
		_ = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)
		    OR EXISTS (SELECT 1 FROM pg_publication WHERE pubname = $2)
		    OR EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $2)`, copyRole, slot).Scan(&left)
		return left
	}
	var logs strings.Builder
	// a rule that could not be read changes nothing (no copy ends for a database that
	// did not answer)
	c := &Copies{DB: db, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		OperatorPG: func(context.Context) (bool, error) { return false, errors.New("the rule could not be read") }}
	c.guardSlots(ctx)
	if denied() == nil || !hasCopy() || logs.Len() != 0 {
		t.Fatalf("with the rule unread the door shut, or the copy ended:\n%s", logs.String())
	}
	c.OperatorPG = nil // off
	c.guardSlots(ctx)
	c.guardSlots(ctx)
	if err := denied(); err != nil {
		t.Fatalf("copies refused, the machine asks: %v", err)
	}
	if hasCopy() {
		t.Fatal("the copy the machine had was kept (its replication role, publication or slot)")
	}
	if strings.Count(logs.String(), "event=cluster_copy_refused") != 1 || !strings.Contains(logs.String(), "had_copy=true") ||
		strings.Count(logs.String(), "event=cluster_copies_shut") != 1 {
		t.Fatalf("the machine refused once, the door shut once:\n%s", logs.String())
	}
	// the admin allows copies there: the machines may ask again; Maktaba's own needs no rule
	c.OperatorPG = ruleOn
	c.guardSlots(ctx)
	if denied() == nil || !strings.Contains(logs.String(), "event=cluster_copies_open") ||
		!strings.Contains(logs.String(), "event=cluster_copies_operator_postgres") {
		t.Fatalf("with the rule the machine may ask:\n%s", logs.String())
	}
	door := func(c *Copies) {
		why, _ := c.refusal(ctx)
		c.holdDoor(ctx, why)
	}
	c.OperatorPG = nil
	door(c)
	c.Bundled = true // (the door alone: the sweep of idle physical slots is for a server of the home's own)
	door(c)
	if denied() == nil {
		t.Fatal("on Maktaba's own Postgres the machine may not ask")
	}
	// a home whose database user does not own the grant cannot shut it: said once
	exec(`CREATE ROLE ` + app + ` LOGIN PASSWORD 'app-pass'`)
	exec(`GRANT SELECT ON cluster_nodes TO ` + app)
	appDB, err := sql.Open("postgres", clustertest.WithUser(t, dsn, app, "app-pass"))
	if err != nil {
		t.Fatal(err)
	}
	defer appDB.Close()
	var notOwner strings.Builder
	other := &Copies{DB: appDB, Logger: slog.New(slog.NewTextHandler(&notOwner, nil))}
	door(other)
	door(other)
	if denied() == nil || strings.Count(notOwner.String(), "event=cluster_copies_door_failed") != 1 {
		t.Fatalf("a home user that does not own the grant (said once):\n%s", notOwner.String())
	}
}

// A slot that is not Maktaba's, in use by a stream that does not end on its own (a role
// with REPLICATION decoding every table through test_decoding over the replication
// protocol): the home ends the stream before it drops the slot, and says "dropped" only
// once it did. A home whose database user may not end it (no pg_signal_backend, as on an
// operator's own Postgres) says so once, with what to do, and drops it once it may. Needs
// pg_recvlogical (MAKTABA_TEST_PG_BIN, or on PATH) and wal_level=logical; touches no
// other database's slots.
func TestAForeignStreamIsEndedBeforeItsSlotIsDropped(t *testing.T) {
	base := os.Getenv("MAKTABA_TEST_PG_URL")
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset")
	}
	recv := pgProgram("pg_recvlogical")
	if recv == "" {
		t.Skip("no pg_recvlogical (MAKTABA_TEST_PG_BIN, or on PATH)")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var level string
	if err := admin.QueryRow(`SHOW wal_level`).Scan(&level); err != nil {
		t.Skipf("scratch Postgres unreachable: %v", err)
	}
	if level != "logical" {
		t.Skip("the server does not run with wal_level=logical")
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	app, evil, dbName, slot := "app_"+suffix, "evil_"+suffix, "t_guard_"+suffix, "maktaba_test_evil_"+suffix
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(admin, `CREATE ROLE `+app+` LOGIN REPLICATION PASSWORD 'app-pass'`)
	exec(admin, `CREATE ROLE `+evil+` LOGIN REPLICATION PASSWORD 'evil-pass'`)
	exec(admin, `CREATE DATABASE `+dbName+` OWNER `+app)
	t.Cleanup(func() {
		_, _ = admin.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, dbName)
		for i := 0; i < 20; i++ {
			_, _ = admin.Exec(`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots
			    WHERE database = $1 AND NOT active`, dbName)
			var left int
			_ = admin.QueryRow(`SELECT count(*) FROM pg_replication_slots WHERE database = $1`, dbName).Scan(&left)
			if left == 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + dbName); err != nil {
			t.Logf("drop %s: %v", dbName, err)
		}
		for _, r := range []string{evil, app} {
			if _, err := admin.Exec(`DROP ROLE IF EXISTS ` + r); err != nil {
				t.Logf("drop role %s: %v", r, err)
			}
		}
	})
	home, err := sql.Open("postgres", clustertest.DSN(t, base, dbName, app, "app-pass"))
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	exec(home, `CREATE TABLE cluster_nodes (id uuid PRIMARY KEY, name text NOT NULL, state text NOT NULL,
	    full_node boolean NOT NULL)`)
	exec(home, `CREATE TABLE notes (id serial PRIMARY KEY, body text)`)

	// a role with REPLICATION streams every table through test_decoding, for as long as it likes
	var decoded, stderr syncBuffer
	cmd := osexec.Command(recv, "-d", clustertest.DSN(t, base, dbName, evil, "evil-pass"), "--slot", slot,
		"--plugin", "test_decoding", "--create-slot", "--start", "--no-loop", "-f", "-")
	cmd.Stdout, cmd.Stderr = &decoded, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ended)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-ended
	})
	active := func() (exists, active bool) {
		err := home.QueryRow(`SELECT active FROM pg_replication_slots WHERE slot_name = $1`, slot).Scan(&active)
		return err == nil, active
	}
	// streaming once a row written reaches it decoded (a slot still being made is in use
	// too, and goes with its connection: the server's other transactions delay it)
	for deadline := time.Now().Add(20 * time.Second); !strings.Contains(decoded.String(), "decoded past every publication"); {
		if time.Now().After(deadline) {
			t.Fatalf("pg_recvlogical never streamed: %s", stderr.String())
		}
		exec(home, `INSERT INTO notes (body) VALUES ('decoded past every publication')`)
		time.Sleep(100 * time.Millisecond)
	}
	if _, on := active(); !on {
		t.Fatal("the stream's slot is not in use")
	}

	var logs strings.Builder
	c := &Copies{DB: home, Logger: slog.New(slog.NewTextHandler(&logs, nil)), OperatorPG: ruleOn}
	c.guardSlots(context.Background())
	c.guardSlots(context.Background())
	if exists, on := active(); !exists || !on {
		t.Fatalf("without pg_signal_backend the stream was ended (slot there %v, in use %v)", exists, on)
	}
	if strings.Count(logs.String(), "event=cluster_slot_foreign_failed") != 1 || !strings.Contains(logs.String(), "pg_signal_backend") ||
		strings.Contains(logs.String(), "event=cluster_slot_foreign_dropped") {
		t.Fatalf("a stream the home cannot end is said once, with what to do, and never as dropped:\n%s", logs.String())
	}
	exec(admin, `GRANT pg_signal_backend TO `+app)
	c.guardSlots(context.Background())
	if exists, _ := active(); exists {
		t.Fatal("the slot was kept once its stream could be ended")
	}
	if !strings.Contains(logs.String(), "event=cluster_slot_foreign_dropped") || !strings.Contains(logs.String(), "stream_ended=true") {
		t.Fatalf("the slot dropped, its stream ended first:\n%s", logs.String())
	}
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the foreign stream did not end")
	}
}

// syncBuffer is a program's output, read while the program writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// pgProgram is a Postgres program of MAKTABA_TEST_PG_BIN (an install: its bin), else on
// PATH ("": none).
func pgProgram(name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if dir := os.Getenv("MAKTABA_TEST_PG_BIN"); dir != "" {
		for _, p := range []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
	}
	p, _ := osexec.LookPath(name)
	return p
}

// scramVerifier is the SCRAM-SHA-256 verifier a machine sends for its copy's password
// (nodecopy computes it the same way).
func scramVerifier(t *testing.T, password string) string {
	t.Helper()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, 4096, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	mac := func(msg string) []byte {
		h := hmac.New(sha256.New, salted)
		h.Write([]byte(msg))
		return h.Sum(nil)
	}
	stored := sha256.Sum256(mac("Client Key"))
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$4096:%s$%s:%s", b64(salt), b64(stored[:]), b64(mac("Server Key")))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func isCode(err error, code string) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && string(pe.Code) == code
}
