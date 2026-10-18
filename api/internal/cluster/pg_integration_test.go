package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// Real Postgres tier (clustertest.ScratchDB): runs only when
// MAKTABA_TEST_PG_URL is set; skips otherwise.

func scratchDB(t *testing.T) (*sql.DB, string) { return clustertest.ScratchDB(t) }

func withUser(t *testing.T, dsn, user, password string) string {
	return clustertest.WithUser(t, dsn, user, password)
}

func TestPGStoreAndRoles(t *testing.T) {
	db, dsn := scratchDB(t)
	ctx := context.Background()
	store := &PGStore{DB: db}
	roles := &PGRoles{DB: db}

	// Identity: created once, kept.
	id1, err := store.EnsureIdentity(ctx, "home-mac")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := store.EnsureIdentity(ctx, "other")
	if err != nil || id2.InstanceID != id1.InstanceID || id2.Name != "home-mac" || id1.AcceptJoins {
		t.Fatalf("identity = %+v / %+v (%v)", id1, id2, err)
	}
	now := time.Now().UTC()
	until := now.Add(-time.Second)
	if _, err := store.SetAcceptJoins(ctx, true, &until); err != nil {
		t.Fatal(err)
	}
	if closed, err := store.CloseExpiredJoins(ctx, now); err != nil || !closed {
		t.Fatalf("close expired = %v %v", closed, err)
	}

	ok, reason := roles.Supported(ctx)
	if !ok {
		t.Fatalf("roles unsupported on a superuser test database: %s", reason)
	}
	dbName, err := roles.DatabaseName(ctx)
	if err != nil || !strings.HasPrefix(dbName, "t1_cluster_") {
		t.Fatalf("database = %q %v", dbName, err)
	}

	// A machine asks, is approved; its role logs in.
	nodeID := uuid.NewString()
	role := RoleName(nodeID)
	t.Cleanup(func() { _ = roles.Drop(context.Background(), role) })
	exp := now.Add(15 * time.Minute)
	hp, hpriv, api := "HOMEPUB", "HOMEPRIV", "http://127.0.0.1:8080"
	if err := store.PutPending(ctx, Node{ID: nodeID, Name: "linux-box", NodePubkey: "NODEPUB", HomePubkey: &hp,
		HomePrivkey: &hpriv, ExpiresAt: &exp, APIURL: &api, CreatedAt: now,
		Capabilities: json.RawMessage(`{"cpu_count":8}`)}); err != nil {
		t.Fatal(err)
	}
	if n, err := store.WrongCode(ctx, nodeID, 5, now); err != nil || n != 1 {
		t.Fatalf("wrong code = %d %v", n, err)
	}
	pw, _ := NewPassword()
	if err := roles.Create(ctx, role, pw); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if err := store.Approve(ctx, nodeID, role, json.RawMessage(`{"nonce":"n","ciphertext":"c"}`), "user:u1", now); err != nil {
		t.Fatal(err)
	}
	n, err := store.Node(ctx, nodeID)
	if err != nil || n.State != StateApproved || n.HomePrivkey != nil || n.Sealed == nil || *n.APIURL != api ||
		*n.DBRole != role || string(n.Capabilities) != `{"cpu_count": 8}` {
		t.Fatalf("node = %+v %v", n, err)
	}
	if err := store.PutPending(ctx, Node{ID: nodeID, Name: "x", NodePubkey: "K", CreatedAt: now}); !errors.Is(err, ErrAlreadyMember) {
		t.Errorf("re-ask while approved: %v", err)
	}

	nodeDB, err := sql.Open("postgres", withUser(t, dsn, role, pw))
	if err != nil {
		t.Fatal(err)
	}
	defer nodeDB.Close()
	nodeDB.SetMaxOpenConns(1)
	var cnt int
	if err := nodeDB.QueryRow(`SELECT count(*) FROM job_stages`).Scan(&cnt); err != nil {
		t.Fatalf("node reads job_stages: %v", err)
	}
	if _, err := nodeDB.Exec(`SELECT 1 FROM cluster_nodes LIMIT 1`); err == nil {
		t.Error("a node must not read cluster_nodes directly")
	}
	if _, err := nodeDB.Exec(`CREATE TABLE node_ddl (x int)`); err == nil {
		t.Error("a node must not change the schema")
	}
	var state string
	var drain bool
	if err := nodeDB.QueryRow(`SELECT state, drain FROM cluster_node_beat($1)`, nodeID).Scan(&state, &drain); err != nil || state != StateApproved {
		t.Fatalf("beat = %q %v", state, err)
	}

	// Its worker registers: the envelope goes.
	if _, err := db.Exec(`INSERT INTO workers (id, host, node_id, role) VALUES ('node-w1', 'linux-box', $1, 'node')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearSealed(ctx, now.Add(-15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.Node(ctx, nodeID); n.Sealed != nil {
		t.Error("sealed must be cleared once the worker registered")
	}
	ws, err := store.Workers(ctx)
	if err != nil || len(ws) != 1 || ws[0].NodeID != nodeID || string(ws[0].StagesOff) != "{}" {
		t.Errorf("workers = %+v %v", ws, err)
	}

	// Admin edits.
	tr, fa := true, false
	if err := store.SetStages(ctx, nodeID, map[string]*bool{"probe": &fa}); err != nil {
		var stage string
		_ = db.QueryRow(`SELECT stage FROM job_stages LIMIT 1`).Scan(&stage)
		if stage == "" {
			t.Log("no job_stages rows; stage switches checked against the FK only")
		} else if err := store.SetStages(ctx, nodeID, map[string]*bool{stage: &fa}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetStages(ctx, nodeID, map[string]*bool{"no_such_stage": &tr}); !errors.Is(err, ErrUnknownStage) {
		t.Errorf("unknown stage: %v", err)
	}
	if _, err := store.Stages(ctx); err != nil {
		t.Fatal(err)
	}
	name := "NAS box"
	if err := store.UpdateNode(ctx, nodeID, NodePatch{Name: &name, Drain: &tr,
		PathMap: json.RawMessage(`{"/Volumes/nas":"/mnt/nas"}`)}, now); err != nil {
		t.Fatal(err)
	}
	n, _ = store.Node(ctx, nodeID)
	if n.Name != name || !n.Drain || n.PathMapSource == nil || *n.PathMapSource != PathMapAdmin {
		t.Errorf("after edit = %+v", n)
	}
	if err := store.UpdateNode(ctx, nodeID, NodePatch{ClearPathMap: true}, now); err != nil {
		t.Fatal(err)
	}
	if n, _ = store.Node(ctx, nodeID); n.PathMap != nil || n.PathMapSource != nil || n.Name != name {
		t.Errorf("after clear = %+v", n)
	}

	// Revoke: NOLOGIN, new password, the open session ends.
	ended, err := roles.Disable(ctx, role)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if ended < 1 {
		t.Errorf("sessions ended = %d, want >= 1", ended)
	}
	if err := store.Revoke(ctx, nodeID, now); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := nodeDB.QueryRow(`SELECT 1`).Scan(&cnt); err == nil {
		// the pool may reconnect: a fresh login must fail regardless
		t.Log("old session still answered (reconnected?)")
	}
	fresh, _ := sql.Open("postgres", withUser(t, dsn, role, pw))
	defer fresh.Close()
	if err := fresh.Ping(); err == nil {
		t.Error("a revoked role must not log in")
	}

	// Removed: the role goes, the id may ask again.
	if err := store.DeleteNode(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := roles.Drop(ctx, role); err != nil {
		t.Fatalf("drop role: %v", err)
	}
	var exists bool
	_ = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists)
	if exists {
		t.Error("role should be dropped")
	}
	if err := store.PutPending(ctx, Node{ID: nodeID, Name: "again", NodePubkey: "K", CreatedAt: now}); err != nil {
		t.Errorf("re-ask after removal: %v", err)
	}
	if n, err := store.WrongCode(ctx, nodeID, 1, now); err != nil || n != 1 {
		t.Fatal(err)
	}
	if n, _ := store.Node(ctx, nodeID); n.State != StateRejected {
		t.Errorf("max attempts should reject: %s", n.State)
	}
}

func TestPGRolesProbeWithoutCreateRole(t *testing.T) {
	db, dsn := scratchDB(t)
	user := fmt.Sprintf("t1_plain_%s", strings.ReplaceAll(uuid.NewString()[:8], "-", ""))
	pw, _ := NewPassword()
	if _, err := db.Exec(`CREATE ROLE ` + pq.QuoteIdentifier(user) + ` LOGIN PASSWORD ` + pq.QuoteLiteral(pw)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + pq.QuoteIdentifier(user))
	})
	plain, err := sql.Open("postgres", withUser(t, dsn, user, pw))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	ok, reason := (&PGRoles{DB: plain}).Supported(context.Background())
	if ok || reason != ReasonCannotCreate {
		t.Errorf("supported = %v %q", ok, reason)
	}
}
