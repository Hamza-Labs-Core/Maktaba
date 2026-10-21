package cluster

import (
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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home's side of a machine's copy (slot 0144, specs/epics/37-node-replica), on a
// home set up as the Postgres bundled with Maktaba.app sets it up: the app role owns
// the schema, has run the migrations and was given, at the cluster bootstrap, the
// right to make roles and slots and the replicas' group. A machine's own database role
// asks for its copy: its role, its publication (every table but the secrets and other
// machines' rows); revoking the machine ends it, removing it drops its role. The
// server must run with wal_level=logical.
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
		_, _ = admin.Exec(`SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name LIKE 'maktaba\_copy\_%' AND NOT active`)
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
	// database's)
	exec(sup, `GRANT EXECUTE ON FUNCTION pg_reload_conf() TO `+app)
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
		g.Version < 160000 || g.Database != dbName || g.CA != nil || g.SchemaVersion < 144 {
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

	// the home's loop: the beat (a slot exists), the CA, the cap
	ca := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exec(home, `UPDATE cluster_replica_beat SET at = '2000-01-01'`)
	exec(sup, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`) // a machine follows
	c := &Copies{DB: home, CAFile: ca, Logger: slog.Default()}
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
