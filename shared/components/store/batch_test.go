package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// A batch writes every row and its audit (with the batch id and who) in one
// transaction, sets the bundle, and hands the plan the rows as they are.
func TestSetDesiredBatchWritesAllWithAudit(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	if _, err := s.SetDesired(ctx, Desired{ID: "llm", Want: components.WantEnabled}, audit("component.enable")); err != nil {
		t.Fatal(err)
	}
	var seen map[string]Desired
	var seenRev int64
	a := Audit{By: "maya", ActorSource: "web", Action: "component.bundle_apply", BatchID: "b-1"}
	out, err := s.SetDesiredBatch(ctx, func(cur map[string]Desired, rev int64) (BatchPlan, error) {
		seen, seenRev = cur, rev
		return BatchPlan{Bundle: "core", Writes: []BatchWrite{
			{Desired{ID: "llm", Want: components.WantInstalled}, a},
			{Desired{ID: "youtube", Want: components.WantEnabled, Hold: &Hold{By: "maya"}}, a},
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen["llm"].Want != components.WantEnabled || seenRev != 1 {
		t.Fatalf("plan saw %v at %d", seen, seenRev)
	}
	if len(out) != 2 || out[0].Want != components.WantInstalled || out[0].Generation != 2 ||
		out[1].Hold == nil || out[1].Hold.By != "maya" {
		t.Fatalf("answered %+v", out)
	}
	m, err := s.Meta(ctx)
	if err != nil || m.Bundle != "core" {
		t.Fatalf("bundle %q %v", m.Bundle, err)
	}
	rows, err := s.AuditOf(ctx, "llm", 20)
	if err != nil || len(rows) != 2 {
		t.Fatalf("audit of llm: %v %v", rows, err)
	}
	if rows[0].Action != "component.bundle_apply" || rows[0].ActorSource != "web" {
		t.Fatalf("newest first: %+v", rows[0])
	}
	var p map[string]any
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["batch_id"] != "b-1" || p["by"] != "maya" || p["before"] == nil || p["after"] == nil {
		t.Fatalf("payload %v", p)
	}
	if rows[1].Action != "component.enable" {
		t.Fatalf("older row %+v", rows[1])
	}
	if one, _ := s.AuditOf(ctx, "llm", 1); len(one) != 1 {
		t.Fatalf("limit: %d rows", len(one))
	}
}

// A plan's error, or a bad row anywhere in the batch, writes nothing.
func TestSetDesiredBatchAllOrNone(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	refused := errors.New("refused")
	if _, err := s.SetDesiredBatch(ctx, func(map[string]Desired, int64) (BatchPlan, error) {
		return BatchPlan{}, refused
	}); !errors.Is(err, refused) {
		t.Fatalf("plan error answered %v", err)
	}
	_, err := s.SetDesiredBatch(ctx, func(map[string]Desired, int64) (BatchPlan, error) {
		return BatchPlan{Bundle: "core", Writes: []BatchWrite{
			{Desired{ID: "llm", Want: components.WantEnabled}, audit("component.enable")},
			{Desired{ID: "youtube", Want: components.WantEnabled}, audit("component.enable")},
			{Desired{ID: "telemetry", Want: "on"}, audit("component.enable")},
		}}, nil
	})
	if err == nil {
		t.Fatal("a bad third row was written")
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM components`).Scan(&n)
	var a int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&a)
	m, _ := s.Meta(ctx)
	if n != 0 || a != 0 || m.Bundle != "" || m.Revision != 0 {
		t.Fatalf("rows %d, audit %d, bundle %q, revision %d after a refused batch", n, a, m.Bundle, m.Revision)
	}
}
