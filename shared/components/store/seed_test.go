package store

import (
	"context"
	"errors"
	"testing"
)

// Story 51.03 (SQLite side): the seed writes once; a failed plan leaves nothing; a
// machine's override seed keeps a row already there.
func TestSeedSQLite(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	if m, err := s.Meta(ctx); err != nil || m.Seeded() {
		t.Fatalf("fresh meta %+v %v", m, err)
	}
	if _, err := s.Seed(ctx, func(context.Context, Queryer) (SeedPlan, error) { return SeedPlan{}, errors.New("crash") }); err == nil {
		t.Fatal("no error from a failed plan")
	}
	if _, err := s.Seed(ctx, func(context.Context, Queryer) (SeedPlan, error) {
		return SeedPlan{Wants: map[string]string{"llm": "enabled"}}, nil
	}); err == nil {
		t.Fatal("a seed with no bundle")
	}
	plan := func(context.Context, Queryer) (SeedPlan, error) {
		return SeedPlan{Bundle: "full", Release: "r1", Wants: map[string]string{"llm": "installed", "youtube": "enabled"},
			TomlImported: map[string]string{"llm.enabled": "auto"}}, nil
	}
	if ok, err := s.Seed(ctx, plan); err != nil || !ok {
		t.Fatalf("seed: %v %v", ok, err)
	}
	m, err := s.Meta(ctx)
	if err != nil || !m.Seeded() || m.Bundle != "full" || m.SeededRelease != "r1" || m.TomlImported["llm.enabled"] != "auto" {
		t.Fatalf("meta %+v %v", m, err)
	}
	if ok, err := s.Seed(ctx, plan); err != nil || ok {
		t.Fatalf("second seed: %v %v", ok, err)
	}
	if d, _ := s.DesiredOf(ctx, "llm"); d.Want != "installed" || d.Generation != 1 {
		t.Fatalf("llm %+v", d)
	}
	if _, err := db.Exec(`INSERT INTO cluster_nodes (id) VALUES ('n1')`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOverride(ctx, Override{Node: "n1", ID: "llm"}, audit("component.override")); err != nil {
		t.Fatal(err)
	}
	n, err := s.SeedOverrides(ctx, "n1", map[string]string{"llm": "enabled", "youtube": "installed"})
	if err != nil || n != 1 {
		t.Fatalf("override seed wrote %d, %v", n, err)
	}
	if _, err := s.SeedOverrides(ctx, "n1", map[string]string{"llm": "on"}); err == nil {
		t.Fatal("a bad want was seeded")
	}
	if _, err := s.SeedOverrides(ctx, "", nil); err == nil {
		t.Fatal("an override seed with no node")
	}
}
