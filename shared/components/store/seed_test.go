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

// Story 52.17 (SQLite side): a seeded home gets a row for each component a later release
// adds, as its bundle says; rows already there are kept, a failed or bad plan writes
// nothing, an unseeded home is the first seed's, and one audit row tells of the new rows.
func TestSeedMissingSQLite(t *testing.T) {
	db := sqliteDB(t)
	s := New(db)
	never := func(context.Context, string, map[string]string) (map[string]string, error) {
		t.Fatal("planned on an unseeded home")
		return nil, nil
	}
	if wrote, err := s.SeedMissing(ctx, "r2", never); err != nil || wrote != nil {
		t.Fatalf("unseeded: %v %v", wrote, err)
	}
	if _, err := s.Seed(ctx, func(context.Context, Queryer) (SeedPlan, error) {
		return SeedPlan{Bundle: "full", Release: "r1", Wants: map[string]string{"llm": "installed"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedMissing(ctx, "r2", func(context.Context, string, map[string]string) (map[string]string, error) {
		return nil, errors.New("crash")
	}); err == nil {
		t.Fatal("no error from a failed plan")
	}
	if _, err := s.SeedMissing(ctx, "r2", func(context.Context, string, map[string]string) (map[string]string, error) {
		return map[string]string{"lang.fr": "on"}, nil
	}); err == nil {
		t.Fatal("a bad want was seeded")
	}
	var gotBundle string
	var gotHave map[string]string
	plan := func(_ context.Context, bundle string, have map[string]string) (map[string]string, error) {
		gotBundle, gotHave = bundle, have
		return map[string]string{"llm": "disabled", "lang.fr": "installed", "lang.de": "enabled"}, nil
	}
	wrote, err := s.SeedMissing(ctx, "r2", plan)
	if err != nil || len(wrote) != 2 || wrote["lang.fr"] != "installed" || wrote["lang.de"] != "enabled" {
		t.Fatalf("wrote %v, %v", wrote, err)
	}
	if gotBundle != "full" || gotHave["llm"] != "installed" || len(gotHave) != 1 {
		t.Fatalf("plan saw bundle %q, have %v", gotBundle, gotHave)
	}
	if d, _ := s.DesiredOf(ctx, "llm"); d.Want != "installed" {
		t.Fatalf("llm's row was changed: %+v", d)
	}
	if d, _ := s.DesiredOf(ctx, "lang.de"); d.Want != "enabled" {
		t.Fatalf("lang.de %+v", d)
	}
	var audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'component.seed' AND payload LIKE '%"new":true%'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("%d audit rows, %v", audits, err)
	}
	if wrote, err := s.SeedMissing(ctx, "r2", plan); err != nil || wrote != nil {
		t.Fatalf("nothing new: %v %v", wrote, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'component.seed' AND payload LIKE '%"new":true%'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("a pass with nothing new wrote an audit row (%d), %v", audits, err)
	}
}
