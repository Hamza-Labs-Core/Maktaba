package components

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store/pqlisten"
)

var ctx = context.Background()

func admin(action string) store.Audit {
	return store.Audit{By: "admin", ActorSource: "admin", Action: action}
}

// node is a joined machine: its cluster_nodes row and its database login (a member of
// maktaba_worker, as every joined machine's role is).
type node struct {
	id string
	db *sql.DB
}

func joinNode(t *testing.T, db *sql.DB, dsn, name string) node {
	t.Helper()
	id := uuid.NewString()
	role := cluster.RoleName(id)
	roles := &cluster.PGRoles{DB: db}
	if err := roles.Create(ctx, role, "node-pass-"+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP OWNED BY ` + pq.QuoteIdentifier(role))
		_ = roles.Drop(context.Background(), role)
	})
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by)
		VALUES ($1, $2, 'approved', 'linux', 'amd64', $3, $4, now(), 'test')`, id, name, "k-"+name, role); err != nil {
		t.Fatal(err)
	}
	ndb, err := sql.Open("postgres", clustertest.WithUser(t, dsn, role, "node-pass-"+name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ndb.Close() })
	return node{id: id, db: ndb}
}

func isDenied(err error) bool {
	var pe *pq.Error
	if err == nil {
		return false
	}
	if e, ok := err.(*pq.Error); ok {
		pe = e
	}
	return pe != nil && pe.Code == "42501"
}

// T01: the migrated database has the tables, its one meta row and the catch-up stage; the
// slot's Up again changes nothing.
func TestMigrationShape(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN
		('components','component_overrides','component_status','component_meta')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("%d tables, %v", n, err)
	}
	var placement string
	if err := db.QueryRow(`SELECT placement FROM job_stages WHERE stage = 'component_catchup'`).Scan(&placement); err != nil || placement != "home" {
		t.Fatalf("catch-up stage: %q %v", placement, err)
	}
	// the slot's Up again, as its SQL (goose would skip it: it is applied already): no
	// error, no second meta row, no second catch-up stage
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "db", "migrations", "0178_components.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up, _, ok := strings.Cut(string(src), "-- +goose Down")
	if !ok {
		t.Fatal("0178 has no Down")
	}
	if _, err := db.Exec(up); err != nil {
		t.Fatalf("the slot's Up again: %v", err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM component_meta`).Scan(&n)
	if n != 1 {
		t.Fatalf("component_meta has %d rows", n)
	}
	if err := db.QueryRow(`SELECT count(*) FROM job_stages WHERE stage = 'component_catchup'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d catch-up stages, %v", n, err)
	}
	// a bad want or bundle is refused by the table itself
	if _, err := db.Exec(`INSERT INTO components (id, want) VALUES ('x', 'on')`); err == nil {
		t.Fatal("want 'on' was stored")
	}
	if _, err := db.Exec(`UPDATE component_meta SET bundle = 'everything'`); err == nil {
		t.Fatal("bundle 'everything' was stored")
	}
}

// T03: a desired change notifies components_changed with its revision and id within a
// second, and moves the generation by one.
func TestChangeNotifies(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	l, err := pqlisten.New(dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := New(db)
	d, err := s.SetDesired(ctx, store.Desired{ID: "transcription", Want: "enabled"}, admin("component.enable"))
	if err != nil {
		t.Fatal(err)
	}
	rev, _ := s.Revision(ctx)
	select {
	case e := <-l.Events():
		if e.Revision != rev || e.ID != "transcription" || e.Node != "" {
			t.Fatalf("notification %+v, revision %d", e, rev)
		}
	case <-time.After(time.Second):
		t.Fatal("no notification within 1 s")
	}
	d2, err := s.SetDesired(ctx, store.Desired{ID: "transcription", Want: "installed"}, admin("component.disable"))
	if err != nil {
		t.Fatal(err)
	}
	if d2.Generation != d.Generation+1 {
		t.Fatalf("generation %d -> %d", d.Generation, d2.Generation)
	}
	// a no-op update (same values) still counts: the trigger does not compare
	if rev2, _ := s.Revision(ctx); rev2 != rev+1 {
		t.Fatalf("revision %d -> %d", rev, rev2)
	}
}

// T04 + T05: a machine's login writes no component table directly; through the guarded
// function it writes its own status rows and no other machine's.
func TestMachinesWriteOnlyTheirOwnStatus(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	a := joinNode(t, db, dsn, "a")
	b := joinNode(t, db, dsn, "b")
	for _, q := range []string{
		`INSERT INTO components (id, want) VALUES ('llm', 'enabled')`,
		`INSERT INTO component_overrides (node_id, id, want) VALUES ('` + a.id + `', 'llm', 'enabled')`,
		`INSERT INTO component_status (machine, id, part, phase) VALUES ('` + a.id + `', 'llm', 'pipeline', 'running')`,
		`UPDATE component_meta SET revision = revision + 1`,
	} {
		if _, err := a.db.Exec(q); !isDenied(err) {
			t.Errorf("a machine's login: %s: %v (want permission denied)", q, err)
		}
	}
	sa := &store.Store{DB: a.db}
	row := []store.Status{{ID: "llm", Phase: "running"}}
	if err := sa.ReportStatus(ctx, a.id, "pipeline", row); err != nil {
		t.Fatalf("own status: %v", err)
	}
	if err := sa.ReportStatus(ctx, b.id, "pipeline", row); !isDenied(err) {
		t.Fatalf("another machine's status: %v (want permission denied)", err)
	}
	if err := sa.ReportStatus(ctx, store.Home, "pipeline", row); err == nil {
		t.Fatal("a machine wrote the home's status")
	}
	rows, err := New(db).Statuses(ctx)
	if err != nil || len(rows) != 1 || rows[0].Machine != a.id {
		t.Fatalf("status rows %+v, %v", rows, err)
	}
	// it can still read: desired state and every machine's status
	if _, _, err := sa.Desired(ctx, a.id); err != nil {
		t.Fatalf("a machine reads its desired state: %v", err)
	}
	if _, _, err := sa.Desired(ctx, b.id); !isDenied(err) {
		t.Fatalf("a machine read another's merged desired state: %v", err)
	}
	if _, err := sa.Statuses(ctx); err != nil {
		t.Fatal(err)
	}
	// the function's report: pruning, the since rule and the detail cap hold there too
	if err := sa.ReportStatus(ctx, a.id, "pipeline", []store.Status{{ID: "transcription", Phase: "running",
		Detail: map[string]any{"log": strings.Repeat("x", store.MaxDetailBytes)}}}); err != nil {
		t.Fatal(err)
	}
	rows, _ = New(db).Statuses(ctx)
	if len(rows) != 1 || rows[0].ID != "transcription" || rows[0].Detail["truncated"] != true {
		t.Fatalf("after the second report: %+v", rows)
	}
}

// T06: a node's desired state is the home's with its overrides; its generation moves with
// either.
func TestDesiredForANode(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	a := joinNode(t, db, dsn, "a")
	s := New(db)
	for _, d := range []store.Desired{{ID: "transcription", Want: "enabled"}, {ID: "llm", Want: "enabled"}} {
		if _, err := s.SetDesired(ctx, d, admin("component.enable")); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	if err := s.SetOverride(ctx, store.Override{Node: a.id, ID: "transcription", Want: "installed",
		Hold: &store.Hold{By: "admin", At: at}}, admin("component.disable")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(ctx, store.Override{Node: a.id, ID: "chapters", Want: "enabled"}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	got, _, err := (&store.Store{DB: a.db}).Desired(ctx, a.id)
	if err != nil {
		t.Fatal(err)
	}
	tr := got["transcription"]
	if tr.Want != "installed" || tr.Hold == nil || !tr.Hold.At.Equal(at) || got["llm"].Want != "enabled" ||
		got["chapters"].Want != "enabled" || len(got) != 3 {
		t.Fatalf("merged %+v", got)
	}
	before := tr.Generation
	if _, err := s.SetDesired(ctx, store.Desired{ID: "transcription", Want: "enabled"}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Desired(ctx, a.id)
	if got["transcription"].Generation <= before || got["transcription"].Want != "installed" {
		t.Fatalf("a home change under an override: %+v (was generation %d)", got["transcription"], before)
	}
}

// T13: a desired write and its audit row are one transaction; a failed audit leaves no
// change.
func TestAuditInTheSameTransaction(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	old := version.Version
	version.Version = "0.3.66"
	defer func() { version.Version = old }()
	s := New(db)
	if s.Version != "0.3.66" {
		t.Fatalf("version %q", s.Version)
	}
	if _, err := s.SetDesired(ctx, store.Desired{ID: "llm", Want: "enabled"}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	var n int
	var payload []byte
	if err := db.QueryRow(`SELECT count(*), max(payload::text) FROM audit_log WHERE category = 'components'
		AND action = 'component.enable' AND target_kind = 'component' AND target_id = 'llm'`).Scan(&n, &payload); err != nil || n != 1 {
		t.Fatalf("%d audit rows, %v", n, err)
	}
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil || p["after"] == nil {
		t.Fatalf("payload %s", payload)
	}
	// an action the audit refuses (not component.*) rolls the change back
	if _, err := s.SetDesired(ctx, store.Desired{ID: "llm", Want: "installed"}, admin("enable")); err == nil {
		t.Fatal("an unaudited change was written")
	}
	d, _ := s.DesiredOf(ctx, "llm")
	if d.Want != "enabled" {
		t.Fatalf("want %s after a refused change", d.Want)
	}
	if err := s.ReportStatus(ctx, store.Home, "api", []store.Status{{ID: "llm", Phase: "running"}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.StatusOf(ctx, "llm")
	if len(rows) != 1 || rows[0].Version != "0.3.66" {
		t.Fatalf("status %+v", rows)
	}
}
