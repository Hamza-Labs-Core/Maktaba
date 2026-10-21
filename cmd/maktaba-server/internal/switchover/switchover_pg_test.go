package switchover

// Switching the home on real Postgres servers on 127.0.0.1 (specs/epics/37-node-replica):
// a home A set up as Maktaba.app sets one up and migrated by maktaba-api, and two
// machines B and C whose copies the nodecopy Manager makes and keeps. Planned: A frozen
// with the marker as its last write, the kit gathered, sealed to B and opened there, B
// promoted from its copy, A made a copy of B and C moved to B, both without copying
// again; then B writes and both follow, the sequences go on from A's, the secrets and the
// machines are B's, the machines keep their passwords. Emergency: A cut off with a change
// B never got, B promoted alone, A back: fenced, and the change kept aside.
//
// Opt-in: MAKTABA_TEST_PG_BIN, a Postgres 16 install; it builds maktaba-api.

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/curve25519"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/nodecopy"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
)

func keypair(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

// cluster is a home and its machines' copies, as the supervisors would keep them.
type cluster struct {
	t        *testing.T
	ctx      context.Context
	programs string
	tmp      string
	migrate  func(ctx context.Context, dsn string) error
	home     pgbundle.Bundle
	homeSrv  *pgtest.Server
	app      *sql.DB
	machines map[string]*machine
}

type machine struct {
	id, role, password string
	copy               nodecopy.Copy
	srv                *pgtest.Server
	stop               context.CancelFunc
	done               chan struct{}
	m                  *nodecopy.Manager
}

func open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func super(t *testing.T, b pgbundle.Bundle) *sql.DB {
	t.Helper()
	dsn, err := b.SuperURL()
	if err != nil {
		t.Fatal(err)
	}
	return open(t, dsn)
}

func (c *cluster) exec(db *sql.DB, q string, args ...any) {
	c.t.Helper()
	if _, err := db.ExecContext(c.ctx, q, args...); err != nil {
		c.t.Fatalf("%s: %v", q, err)
	}
}

func newCluster(t *testing.T) *cluster {
	t.Helper()
	programs := pgtest.Programs(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := &cluster{t: t, ctx: ctx, programs: programs, tmp: t.TempDir(), machines: map[string]*machine{}}
	c.migrate = pgtest.Migrator(t, c.tmp)
	c.home, c.homeSrv = pgtest.Bundle(ctx, t, programs, filepath.Join(c.tmp, "a"), true)
	appURL, err := c.home.URL()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.migrate(ctx, appURL); err != nil {
		t.Fatalf("migrate the home: %v", err)
	}
	c.app = open(t, appURL)
	ca, err := os.ReadFile(c.home.CACert())
	if err != nil {
		t.Fatal(err)
	}
	c.exec(c.app, `UPDATE cluster_replica_beat SET ca_pem = $1`, string(ca))
	go func() {
		for ctx.Err() == nil {
			_, _ = c.app.ExecContext(ctx, `UPDATE cluster_replica_beat SET at = now()`)
			time.Sleep(time.Second)
		}
	}()
	return c
}

// join registers a full machine on the home and starts its copy's Manager.
func (c *cluster) join(name string) *machine {
	t := c.t
	m := &machine{id: uuid.NewString(), password: "pass-" + name + "-0123456789"}
	m.role = "maktaba_node_" + strings.ReplaceAll(m.id, "-", "")
	c.exec(c.app, `CREATE ROLE `+m.role+` LOGIN PASSWORD '`+m.password+`' IN ROLE maktaba_worker`)
	c.exec(c.app, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, home_privkey, db_role, approved_at,
	        approved_by, full_node) VALUES ($1, $2, 'approved', 'windows', 'amd64', 'pub-'||$2, 'home-key-for-'||$2, $3,
	        now(), 'test', true)`, m.id, name, m.role)
	dir := filepath.Join(c.tmp, name)
	m.copy = nodecopy.Copy{NodeID: m.id, HomeDSN: dsnAs(m.role, m.password, c.home.Addr(), c.home.CACert()), Migrate: c.migrate,
		DB: pgbundle.Bundle{Home: c.programs, Data: filepath.Join(dir, "pg"), Keys: filepath.Join(dir, "keys"),
			Backups: filepath.Join(dir, "backups"), Port: pgtest.FreePort(t)}}
	m.srv = &pgtest.Server{}
	t.Cleanup(func() { m.srv.Stop(m.copy.DB.StopCommand()) })
	c.machines[name] = m
	c.follow(m)
	return m
}

// dsnAs is a machine's role on a home's database, verifying the home's CA (the file ca)
// as a machine does (config.NodeConfig.HomeDSN).
func dsnAs(role, password, addr, ca string) string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(role, password), Host: addr,
		Path: "/" + pgbundle.AppDatabase, RawQuery: url.Values{"sslmode": {"verify-ca"},
			"sslrootcert": {filepath.ToSlash(ca)}}.Encode()}).String()
}

// follow runs the machine's Manager until stopped.
func (c *cluster) follow(m *machine) {
	ctx, cancel := context.WithCancel(c.ctx)
	m.stop, m.done = cancel, make(chan struct{})
	m.m = &nodecopy.Manager{Copy: m.copy, StateDir: filepath.Join(c.tmp, m.id+"-state"),
		Programs: func(context.Context) (string, error) { return c.programs, nil },
		Every:    300 * time.Millisecond, Retry: 300 * time.Millisecond, Log: &bytes.Buffer{},
		Want: func(on bool) error {
			if on {
				m.srv.Start(c.t, m.copy.DB.Bin("postgres"), m.copy.DB.ServerArgs()...)
			} else {
				m.srv.Stop(m.copy.DB.StopCommand())
			}
			return nil
		},
		Running: m.srv.Running}
	go func() { m.m.Run(ctx); close(m.done) }()
}

func (m *machine) halt() { m.stop(); <-m.done }

func (c *cluster) following(m *machine, what string) {
	c.t.Helper()
	pgtest.WaitFor(c.t, what+": "+m.copy.NodeID+" following", 4*time.Minute, func() bool {
		return m.m.Status().State == nodecopy.StateFollowing
	})
}

func (m *machine) super(t *testing.T) *sql.DB { return super(t, m.copy.DB) }

func value(ctx context.Context, db *sql.DB, key string) string {
	var v string
	_ = db.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, key).Scan(&v)
	return v
}

func count(ctx context.Context, db *sql.DB, q string, args ...any) int {
	var n int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return -1
	}
	return n
}

func TestAPlannedSwitchover(t *testing.T) {
	c := newCluster(t)
	ctx := c.ctx
	b, cm := c.join("b"), c.join("c")
	c.exec(c.app, `INSERT INTO home_secrets (name, key_id, nonce, ciphertext) VALUES ('ext.plex.token', 'k1', '\x01'::bytea, '\x02'::bytea)`)
	c.exec(c.app, `INSERT INTO app_settings (key, value, updated_at) VALUES ('test.switch', '"before"', now())`)
	// a person signed in: the session (a live cookie, never in a copy) moves with the kit
	sessUser, sessID := uuid.NewString(), uuid.NewString()
	c.exec(c.app, `INSERT INTO users (id, username, pw_hash) VALUES ($1, 'sam', 'x')`, sessUser)
	c.exec(c.app, `INSERT INTO web_sessions (id, user_id, csrf_token, created_at, last_seen_at, expires_at)
	    VALUES ($1, $2, 'csrf-value', now(), now(), now() + interval '1 day')`, sessID, sessUser)
	for i := 0; i < 5; i++ {
		c.exec(c.app, `INSERT INTO events (channel, type) VALUES ('test', 'before')`)
	}
	c.following(b, "the first copy")
	c.following(cm, "the first copy")
	bdb, cdb := b.super(t), cm.super(t)
	pgtest.WaitFor(t, "the copies have the home's changes", time.Minute, func() bool {
		return strings.Contains(value(ctx, bdb, "test.switch"), "before") && strings.Contains(value(ctx, cdb, "test.switch"), "before")
	})

	// ---- the switch: A frozen, the marker its last write ----
	id := uuid.NewString()
	c.exec(c.app, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name, state)
	    VALUES ($1, 2, 'planned', $2, 'b', 'a', 'freezing')`, id, b.id)
	adb := super(t, c.home)
	if err := Freeze(ctx, adb, id); err != nil {
		t.Fatal(err)
	}
	if _, err := c.app.ExecContext(ctx, `UPDATE app_settings SET value = '"frozen"' WHERE key = 'test.switch'`); err == nil {
		t.Fatal("the frozen home took a write")
	}
	for _, db := range []*sql.DB{bdb, cdb} {
		pgtest.WaitFor(t, "the marker on a copy", time.Minute, func() bool { ok, _ := HasMarker(ctx, db, id); return ok })
	}
	b.halt()
	cm.halt()

	// ---- the kit: gathered on A, sealed to B, opened there ----
	homePriv, homePub := keypair(t)
	bPriv, bPub := keypair(t)
	caCert, _ := os.ReadFile(c.home.CACert())
	caKey, _ := os.ReadFile(filepath.Join(c.home.Keys, "postgres-ca.key"))
	kit, err := Gather(ctx, adb, id, 2, map[string][]byte{"secrets.key": []byte("the-secrets-key"),
		pgbundle.CACertFile: caCert, "postgres-ca.key": caKey})
	if err != nil {
		t.Fatal(err)
	}
	sendKey, _ := KitKey(homePriv, bPub, id)
	var wire bytes.Buffer
	s := NewSealer(sendKey, id, func(i uint64, last bool, sealed []byte) error { return WriteChunk(&wire, i, last, sealed) })
	if err := kit.Write(s); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire.Bytes(), []byte("the-secrets-key")) {
		t.Fatal("the sealed kit carries a secret in the clear")
	}
	openKey, _ := KitKey(bPriv, homePub, id)
	var plain bytes.Buffer
	o := NewOpener(openKey, id, &plain)
	if err := ReadChunks(&wire, o.Chunk); err != nil || !o.Done() {
		t.Fatalf("open the kit: %v (done %v)", err, o.Done())
	}
	got, err := ReadKit(&plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tables["home_secrets"]) != 1 || len(got.Tables["cluster_nodes"]) != 2 || len(got.Roles) < 2 ||
		string(got.Files["secrets.key"]) != "the-secrets-key" {
		t.Fatalf("the kit: %d secrets, %d machines, %d roles", len(got.Tables["home_secrets"]),
			len(got.Tables["cluster_nodes"]), len(got.Roles))
	}

	// ---- B promoted; A joins it as a machine ----
	a := Machine{ID: uuid.NewString(), Name: "a", OS: "darwin", Arch: "arm64", PublicKey: "pub-a",
		RolePassword: "pass-a-0123456789"}
	aCopy := nodecopy.Copy{NodeID: a.ID, DB: c.home}
	if a.CopyPassword, err = aCopy.Password(); err != nil {
		t.Fatal(err)
	}
	done, err := Promote(ctx, bdb, Promotion{Self: b.id, Name: "b", Term: 2, Reason: "planned", Kit: got, OldHome: &a})
	if err != nil {
		t.Fatal(err)
	}
	if done.Slots[cm.id] == "" || done.Slots[a.ID] == "" || done.Slots[b.id] != "" {
		t.Fatalf("the slots made: %v", done.Slots)
	}
	// B listens as the home: TLS from the same CA (the machines pinned it)
	b.srv.Stop(b.copy.DB.StopCommand())
	nb := b.copy.DB
	nb.TLS = true
	for name, data := range map[string][]byte{pgbundle.CACertFile: caCert, "postgres-ca.key": caKey} {
		if err := os.WriteFile(filepath.Join(nb.Keys, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := nb.Prepare(ctx, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b.srv.Start(t, nb.Bin("postgres"), nb.ServerArgs()...)
	pgtest.WaitUp(ctx, t, nb)
	ca := string(caCert)
	grant := func(id string) nodecopy.Grant {
		slot := Machine{ID: id}.Slot()
		return nodecopy.Grant{Role: Machine{ID: id}.CopyRole(), Slot: slot, SlotExists: true, Publication: slot,
			SystemID: done.SystemID, Database: pgbundle.AppDatabase, CA: &ca}
	}
	local := int64(144)
	// the supervisor opens its sessions after the freeze: so do these
	if err := Demote(ctx, super(t, c.home)); err != nil {
		t.Fatal(err)
	}
	aCopy.HomeDSN = dsnAs(a.Role(), a.RolePassword, nb.Addr(), nb.CACert())
	if err := aCopy.Handover(ctx, grant(a.ID), local); err != nil {
		t.Fatalf("the old home follows the new one: %v", err)
	}
	cm.copy.HomeDSN = dsnAs(cm.role, cm.password, nb.Addr(), nb.CACert())
	if err := cm.copy.Handover(ctx, grant(cm.id), local); err != nil {
		t.Fatalf("a machine follows the new home: %v", err)
	}

	// ---- the new home writes; both follow it ----
	bapp, err := nb.URL()
	if err != nil {
		t.Fatal(err)
	}
	bw := open(t, bapp)
	c.exec(bw, `UPDATE app_settings SET value = '"after"' WHERE key = 'test.switch'`)
	var newID, oldMax int64
	_ = adb.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&oldMax)
	if err := bw.QueryRowContext(ctx, `INSERT INTO events (channel, type) VALUES ('test', 'after') RETURNING id`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID <= oldMax {
		t.Fatalf("the new home's sequence went back: %d after the old home's %d", newID, oldMax)
	}
	for name, db := range map[string]*sql.DB{"the old home": adb, "c": cdb} {
		pgtest.WaitFor(t, name+" follows the new home", time.Minute, func() bool {
			return strings.Contains(value(ctx, db, "test.switch"), "after") &&
				count(ctx, db, `SELECT count(*) FROM events WHERE id = $1`, newID) == 1
		})
	}
	// the old home's processes still run as the home's until it starts again: frozen for
	// them, while the subscription applies; its copy ends the freeze when it starts
	aApp, err := c.home.URL()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open(t, aApp).ExecContext(ctx, `UPDATE app_settings SET value = '"stray"' WHERE key = 'test.switch'`); err == nil {
		t.Fatal("the demoted home took a write of its own before it started as a machine")
	}
	if err := aCopy.EnsureReader(ctx); err != nil {
		t.Fatal(err)
	}
	var ro string
	if err := open(t, aApp).QueryRowContext(ctx, `SHOW default_transaction_read_only`).Scan(&ro); err != nil || ro != "off" {
		t.Fatalf("the old home's copy still frozen once it started as a machine: %q %v", ro, err)
	}
	// the secrets and the machines are the new home's; the old home holds neither
	if n := count(ctx, bdb, `SELECT count(*) FROM home_secrets`); n != 1 {
		t.Fatalf("the new home's secrets: %d", n)
	}
	if n := count(ctx, adb, `SELECT count(*) FROM home_secrets`); n != 0 {
		t.Fatalf("the old home kept %d secrets", n)
	}
	// the person stays signed in on the new home; the old home, a copy now, holds the
	// session's hashes only
	if n := count(ctx, bdb, `SELECT count(*) FROM web_sessions WHERE id = $1`, sessID); n != 1 {
		t.Fatalf("the session on the new home: %d", n)
	}
	if n := count(ctx, adb, `SELECT count(*) FROM web_sessions`); n != 0 {
		t.Fatalf("the old home kept %d web sessions", n)
	}
	if n := count(ctx, adb, `SELECT count(*) FROM web_session_keys WHERE user_id = $1`, sessUser); n != 1 {
		t.Fatalf("the session's hashes on the old home: %d", n)
	}
	if n := count(ctx, bdb, `SELECT count(*) FROM cluster_nodes WHERE id = $1`, b.id); n != 0 {
		t.Fatal("the new home is still one of its machines")
	}
	if n := count(ctx, bdb, `SELECT count(*) FROM cluster_nodes WHERE id IN ($1, $2) AND home_privkey IS NOT NULL OR id = $2`, cm.id, a.ID); n != 2 {
		t.Fatalf("the new home's machines: %d", n)
	}
	pgtest.WaitFor(t, "the old home's own row reaches it", time.Minute, func() bool {
		return count(ctx, adb, `SELECT count(*) FROM cluster_nodes`) == 1 &&
			count(ctx, adb, `SELECT count(*) FROM cluster_nodes WHERE id = $1`, a.ID) == 1
	})
	for name, db := range map[string]*sql.DB{"the old home": adb, "c": cdb} {
		pgtest.WaitFor(t, name+" has the new term", time.Minute, func() bool {
			return count(ctx, db, `SELECT count(*) FROM cluster_terms WHERE term = 2 AND reason = 'planned'`) == 1
		})
	}
	// the machines keep their passwords
	cOnB := open(t, cm.copy.HomeDSN)
	if err := cOnB.PingContext(ctx); err != nil {
		t.Fatalf("a machine signs in to the new home with its old password: %v", err)
	}
	// the new home's app role administers the machines' roles, as the old home's did (its
	// grants set their passwords, its revokes disable them)
	for _, r := range []string{cm.role, Machine{ID: cm.id}.CopyRole(), a.Role(), a.CopyRole()} {
		if _, err := bw.ExecContext(ctx, `ALTER ROLE `+r+` LOGIN`); err != nil {
			t.Fatalf("the new home's app role alters %s: %v", r, err)
		}
	}
}

// A switch that does not go through thaws the old home, from a session opened after the
// freeze (the supervisor opens its own).
func TestAFrozenHomeThaws(t *testing.T) {
	c := newCluster(t)
	ctx := c.ctx
	id := uuid.NewString()
	c.exec(c.app, `INSERT INTO cluster_switchovers (id, term, kind, to_node, to_name, from_name, state)
	    VALUES ($1, 2, 'planned', $2, 'b', 'a', 'freezing')`, id, uuid.NewString())
	if err := Freeze(ctx, super(t, c.home), id); err != nil {
		t.Fatal(err)
	}
	appURL, err := c.home.URL()
	if err != nil {
		t.Fatal(err)
	}
	write := `INSERT INTO app_settings (key, value) VALUES ('test.thaw', '"x"')
	    ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`
	if _, err := open(t, appURL).ExecContext(ctx, write); err == nil {
		t.Fatal("the frozen home took a write")
	}
	if ok, err := HasMarker(ctx, super(t, c.home), id); !ok || err != nil {
		t.Fatalf("no marker on the frozen home: %v", err)
	}
	if err := Thaw(ctx, super(t, c.home)); err != nil {
		t.Fatalf("thaw from a session opened after the freeze: %v", err)
	}
	if _, err := open(t, appURL).ExecContext(ctx, write); err != nil {
		t.Fatalf("the thawed home refuses writes: %v", err)
	}
}

func TestAnEmergencyPromotion(t *testing.T) {
	c := newCluster(t)
	ctx := c.ctx
	b := c.join("b")
	for i := 0; i < 3; i++ {
		c.exec(c.app, `INSERT INTO events (channel, type) VALUES ('test', 'before')`)
	}
	for _, k := range []string{"test.shared", "test.untouched", "test.gone"} {
		c.exec(c.app, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1, '"before"', now())`, k)
	}
	c.following(b, "the first copy")
	bdb := b.super(t)
	pgtest.WaitFor(t, "the copy has the events and settings", time.Minute, func() bool {
		return count(ctx, bdb, `SELECT count(*) FROM events WHERE channel = 'test'`) == 3 &&
			count(ctx, bdb, `SELECT count(*) FROM app_settings WHERE key LIKE 'test.%'`) == 3
	})
	// the home is cut off: what it writes never reaches B (its beat goes on too)
	b.halt()
	c.exec(bdb, `ALTER SUBSCRIPTION maktaba_copy DISABLE`)
	c.exec(c.app, `INSERT INTO app_settings (key, value, updated_at) VALUES ('test.lost', '"only on the old home"', now())`)
	c.exec(c.app, `UPDATE app_settings SET value = '"a"' WHERE key = 'test.shared'`)
	c.exec(c.app, `UPDATE app_settings SET value = '"a"' WHERE key = 'test.untouched'`)
	c.exec(c.app, `DELETE FROM app_settings WHERE key = 'test.gone'`)
	c.exec(c.app, `INSERT INTO events (channel, type) VALUES ('test', 'on the old home')`)
	c.exec(c.app, `UPDATE cluster_replica_beat SET at = now()`)
	userA := uuid.NewString()
	c.exec(c.app, `INSERT INTO users (id, username, pw_hash) VALUES ($1, 'zed', 'x')`, userA)
	var oldMax int64
	_ = c.app.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&oldMax)
	c.homeSrv.Stop(c.home.StopCommand())

	done, err := Promote(ctx, bdb, Promotion{Self: b.id, Name: "b", Term: 2, Reason: "emergency", Gap: 1000, Fork: true})
	if err != nil {
		t.Fatal(err)
	}
	// B, the home now, changes a row the old home changed too, and takes a user name the
	// old home gave to another user
	c.exec(bdb, `UPDATE app_settings SET value = '"b"' WHERE key = 'test.shared'`)
	userB := uuid.NewString()
	c.exec(bdb, `INSERT INTO users (id, username, pw_hash) VALUES ($1, 'zed', 'y')`, userB)
	if done.CutLSN == "" {
		t.Fatal("no cut recorded")
	}
	var newID int64
	if err := bdb.QueryRowContext(ctx, `INSERT INTO events (channel, type) VALUES ('test', 'after') RETURNING id`).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	if newID < oldMax+1000 {
		t.Fatalf("the new home's ids may meet the old home's: %d after %d", newID, oldMax)
	}
	if n := count(ctx, bdb, `SELECT count(*) FROM cluster_terms WHERE term = 2 AND reason = 'emergency' AND cut_lsn IS NOT NULL`); n != 1 {
		t.Fatal("the emergency term and its cut")
	}

	// the old home is back: it learns of the term, fences itself, keeps its change aside
	c.homeSrv.Start(t, c.home.Bin("postgres"), c.home.ServerArgs()...)
	pgtest.WaitUp(ctx, t, c.home)
	adb := super(t, c.home)
	if err := Fence(ctx, adb); err != nil {
		t.Fatal(err)
	}
	appAgain := open(t, func() string { u, _ := c.home.URL(); return u }())
	if _, err := appAgain.ExecContext(ctx, `UPDATE app_settings SET value = '"x"' WHERE key = 'test.lost'`); err == nil {
		t.Fatal("the fenced old home took a write")
	}
	slot := Machine{ID: b.id}.Slot()
	aside, err := KeptAside(ctx, adb, slot, slot, done.CutLSN)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tx := range aside.Transactions {
		for _, ch := range tx.Changes {
			if ch.Table == "public.app_settings" && ch.Kind == "insert" && ch.Row["key"] != nil && *ch.Row["key"] == "test.lost" {
				found = true
			}
		}
	}
	if !found || aside.Counts["public.app_settings"] < 1 {
		t.Fatalf("the change kept aside: %+v", aside)
	}
	for _, tx := range aside.Transactions {
		for _, ch := range tx.Changes {
			if ch.Table == "public.events" && ch.Row["type"] != nil && *ch.Row["type"] == "before" {
				t.Fatalf("a change the new home had is reported as kept aside: %+v", ch)
			}
		}
	}

	// ---- the report on the new home: what applies, and what does not and why ----
	report, _ := json.Marshal(aside)
	reportID := uuid.NewString()
	c.exec(bdb, `INSERT INTO cluster_kept_aside (id, term, from_node, from_name, cut_lsn, report)
	    VALUES ($1, 2, $2, 'a', $3, $4::jsonb)`, reportID, uuid.NewString(), done.CutLSN, string(report))
	plan, err := PlanReport(ctx, bdb, reportID)
	if err != nil {
		t.Fatal(err)
	}
	reasonOf := func(table, key string) (PlannedTx, bool) {
		for i, kt := range aside.Transactions {
			for _, ch := range kt.Changes {
				if ch.Table != table {
					continue
				}
				for _, col := range []string{"key", "username", "type"} {
					if v := ch.Row[col]; v != nil && *v == key {
						return plan.Transactions[i], true
					}
				}
			}
		}
		return PlannedTx{}, false
	}
	for _, want := range []struct {
		table, key, reason string
		apply              bool
	}{
		{"public.app_settings", "test.lost", "", true},
		{"public.app_settings", "test.untouched", "", true},
		{"public.app_settings", "test.shared", ReasonChangedHere, false},
		{"public.events", "on the old home", "", true},
		{"public.users", "zed", ReasonFails, false},
	} {
		pt, ok := reasonOf(want.table, want.key)
		if !ok {
			t.Fatalf("the plan has no transaction of %s %s", want.table, want.key)
		}
		if pt.Apply != want.apply || pt.Reason != want.reason {
			t.Errorf("%s %s: applies %v (%s %s), want %v %q", want.table, want.key, pt.Apply, pt.Reason, pt.Detail,
				want.apply, want.reason)
		}
	}
	beatSkipped := false
	for _, pt := range plan.Transactions {
		for _, tb := range pt.Tables {
			if tb == "public.cluster_replica_beat" {
				beatSkipped = !pt.Apply && pt.Reason == ReasonClusterRows
			}
		}
	}
	if !beatSkipped {
		t.Error("the old home's cluster rows (its beat) would be applied")
	}
	if value(ctx, bdb, "test.lost") != "" {
		t.Fatal("a plan applied something")
	}
	// a plan that is not the one shown applies nothing
	if _, err := ApplyReport(ctx, bdb, reportID, "not-the-plan", ""); !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("an old plan: %v", err)
	}
	if value(ctx, bdb, "test.lost") != "" {
		t.Fatal("an old plan applied something")
	}
	applied, err := ApplyReport(ctx, bdb, reportID, plan.Hash, "")
	if err != nil {
		t.Fatal(err)
	}
	if applied.Apply != plan.Apply {
		t.Fatalf("applied %d of the %d the plan applies", applied.Apply, plan.Apply)
	}
	if value(ctx, bdb, "test.lost") != `"only on the old home"` || value(ctx, bdb, "test.untouched") != `"a"` ||
		value(ctx, bdb, "test.shared") != `"b"` || value(ctx, bdb, "test.gone") != "" ||
		count(ctx, bdb, `SELECT count(*) FROM events WHERE type = 'on the old home'`) != 1 ||
		count(ctx, bdb, `SELECT count(*) FROM users WHERE username = 'zed' AND id = $1`, userB) != 1 {
		t.Fatal("the new home after applying: not what the plan said")
	}
	var state string
	var recorded []byte
	_ = bdb.QueryRowContext(ctx, `SELECT state, applied FROM cluster_kept_aside WHERE id = $1`, reportID).
		Scan(&state, &recorded)
	if state != "applied" || !bytes.Contains(recorded, []byte(ReasonChangedHere)) {
		t.Fatalf("what was applied is not recorded: %s %s", state, recorded)
	}
	if _, err := ApplyReport(ctx, bdb, reportID, plan.Hash, ""); !errors.Is(err, ErrNotReported) {
		t.Fatalf("applied twice: %v", err)
	}
	if err := DropFork(ctx, bdb, 2); err != nil {
		t.Fatal(err)
	}
	if n := count(ctx, bdb, `SELECT count(*) FROM pg_replication_slots WHERE slot_name = $1`, ForkSlot(2)); n != 0 {
		t.Fatal("the fork's slot is kept")
	}
}
