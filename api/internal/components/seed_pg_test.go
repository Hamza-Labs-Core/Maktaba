package components

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.03: the seed of desired state on Postgres (the supervisor's componentseed
// makes the plan; these test what the store does with it).

func fullPlan(release string) func(context.Context, store.Queryer) (store.SeedPlan, error) {
	return func(context.Context, store.Queryer) (store.SeedPlan, error) {
		return store.SeedPlan{Bundle: "full", Release: release, TomlImported: map[string]string{"tv.slots": "on"},
			Wants: map[string]string{"livetv.slots": "enabled", "livetv.inprocess": "installed", "youtube": "installed",
				"model.whisper-medium": "absent"}}, nil
	}
}

func generations(t *testing.T, s *store.Store) map[string]int64 {
	t.Helper()
	d, _, err := s.Desired(ctx, store.Home)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for id, x := range d {
		out[id] = x.Generation
	}
	return out
}

// T04: seeded twice, the rows are written once, seeded_at once, and nothing moves.
func TestSeedOnce(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := New(db)
	ok, err := s.Seed(ctx, fullPlan("0.3.67"))
	if err != nil || !ok {
		t.Fatalf("first seed: %v %v", ok, err)
	}
	m1, err := s.Meta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !m1.Seeded() || m1.Bundle != "full" || m1.SeededRelease != "0.3.67" || m1.TomlImported["tv.slots"] != "on" {
		t.Fatalf("meta after the seed: %+v", m1)
	}
	gen := generations(t, s)
	if len(gen) != 4 {
		t.Fatalf("%d rows", len(gen))
	}
	var by string
	if err := db.QueryRow(`SELECT updated_by FROM components WHERE id = 'youtube'`).Scan(&by); err != nil || by != store.SeedBy {
		t.Fatalf("updated_by %q %v", by, err)
	}
	ok, err = s.Seed(ctx, func(context.Context, store.Queryer) (store.SeedPlan, error) {
		t.Fatal("the plan was asked for again")
		return store.SeedPlan{}, nil
	})
	if err != nil || ok {
		t.Fatalf("second seed: %v %v", ok, err)
	}
	m2, _ := s.Meta(ctx)
	if !m2.SeededAt.Equal(m1.SeededAt) || m2.Revision != m1.Revision {
		t.Fatalf("meta moved: %+v -> %+v", m1, m2)
	}
	for id, g := range generations(t, s) {
		if gen[id] != g {
			t.Fatalf("%s generation %d -> %d", id, gen[id], g)
		}
	}
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE category = 'components' AND action = 'component.seed'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("%d seed audit rows", audits)
	}
}

// A seed that fails halfway leaves nothing: the next boot seeds.
func TestSeedFailureRollsBack(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := New(db)
	if _, err := s.Seed(ctx, func(context.Context, store.Queryer) (store.SeedPlan, error) {
		return store.SeedPlan{Bundle: "full", Wants: map[string]string{"llm": "on"}}, nil
	}); err == nil {
		t.Fatal("a bad want was seeded")
	}
	if _, err := s.Seed(ctx, func(context.Context, store.Queryer) (store.SeedPlan, error) {
		return store.SeedPlan{}, errors.New("crash")
	}); err == nil {
		t.Fatal("no error")
	}
	if m, _ := s.Meta(ctx); m.Seeded() {
		t.Fatal("the marker stayed after a failed seed")
	}
	if n := len(generations(t, s)); n != 0 {
		t.Fatalf("%d rows after failed seeds", n)
	}
	if ok, err := s.Seed(ctx, fullPlan("x")); err != nil || !ok {
		t.Fatalf("the seed after: %v %v", ok, err)
	}
}

// T05: two boots racing: exactly one seed commits, the rows are its.
func TestSeedRace(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	var wg sync.WaitGroup
	results := make([]bool, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := New(db)
			results[i], errs[i] = s.Seed(ctx, func(ctx context.Context, q store.Queryer) (store.SeedPlan, error) {
				time.Sleep(300 * time.Millisecond) // the other is at the lock meanwhile
				return fullPlan("race")(ctx, q)
			})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0] == results[1] {
		t.Fatalf("seeds committed: %v", results)
	}
	if n := len(generations(t, New(db))); n != 4 {
		t.Fatalf("%d rows", n)
	}
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'component.seed'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("%d seed audit rows", audits)
	}
}

// T07: a machine seeds overrides for itself only, once per component, through the guard;
// an override already there (an admin's, or one cleared) is kept.
func TestOverrideSeedGuarded(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	home := New(db)
	if ok, err := home.Seed(ctx, fullPlan("x")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	a := joinNode(t, db, dsn, "a")
	b := joinNode(t, db, dsn, "b")
	sa := store.New(a.db)
	n, err := sa.SeedOverrides(ctx, a.id, map[string]string{"livetv.slots": "installed", "livetv.inprocess": "enabled"})
	if err != nil || n != 2 {
		t.Fatalf("wrote %d, %v", n, err)
	}
	if n, err := sa.SeedOverrides(ctx, a.id, map[string]string{"livetv.slots": "enabled"}); err != nil || n != 0 {
		t.Fatalf("again: wrote %d, %v", n, err)
	}
	if _, err := sa.SeedOverrides(ctx, b.id, map[string]string{"livetv.slots": "installed"}); !isDenied(err) {
		t.Fatalf("a wrote b's overrides: %v", err)
	}
	da, _, err := sa.Desired(ctx, a.id)
	if err != nil {
		t.Fatal(err)
	}
	if da["livetv.slots"].Want != "installed" || da["livetv.inprocess"].Want != "enabled" {
		t.Fatalf("a's merged desired state: %+v", da)
	}
	db2, _, err := store.New(b.db).Desired(ctx, b.id)
	if err != nil {
		t.Fatal(err)
	}
	if db2["livetv.slots"].Want != "enabled" {
		t.Fatalf("b follows the home: %+v", db2["livetv.slots"])
	}
	if hd, _ := home.DesiredOf(ctx, "livetv.slots"); hd.Want != "enabled" {
		t.Fatalf("the home's row moved: %+v", hd)
	}
	// b's admin cleared an override before: the seed keeps it cleared
	if err := home.SetOverride(ctx, store.Override{Node: b.id, ID: "llm"}, admin("component.override")); err != nil {
		t.Fatal(err)
	}
	if n, err := store.New(b.db).SeedOverrides(ctx, b.id, map[string]string{"llm": "installed", "livetv.slots": "installed"}); err != nil || n != 1 {
		t.Fatalf("b wrote %d, %v", n, err)
	}
	var want *string
	if err := db.QueryRow(`SELECT want FROM component_overrides WHERE node_id = $1 AND id = 'llm'`, b.id).Scan(&want); err != nil || want != nil {
		t.Fatalf("b's cleared override: %v %v", want, err)
	}
	var audits int
	_ = db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'component.seed' AND target_id IN ($1, $2)`, a.id, b.id).Scan(&audits)
	if audits != 2 {
		t.Fatalf("%d node seed audit rows", audits)
	}
}

// T08 (the store's side): the seed leaves job_stages as it is, an operator's off included.
func TestSeedLeavesStages(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	if _, err := db.Exec(`UPDATE job_stages SET enabled = false, disabled_reason = 'operator'
		WHERE stage IN ('youtube_sync', 'youtube_fetch', 'youtube_backfill', 'exact_dates')`); err != nil {
		t.Fatal(err)
	}
	snap := func() string {
		var s string
		if err := db.QueryRow(`SELECT string_agg(stage || ':' || enabled || ':' || coalesce(disabled_reason, '-'), ',' ORDER BY stage)
			FROM job_stages`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snap()
	if ok, err := New(db).Seed(ctx, fullPlan("x")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if after := snap(); after != before {
		t.Fatalf("job_stages moved:\n%s\n%s", before, after)
	}
}
