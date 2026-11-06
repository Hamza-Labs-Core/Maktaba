package componentseed

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 52.17: a home seeded on release N, updated to N+1 that adds review.words,
// runtime.lang, lang.ar and lang.en, gets them as its bundle says: a Core home keeps word
// review off and downloads nothing, a Full home turns it on and installs its assets; a row
// an admin set stays, and a second boot writes nothing more.
func TestSeedNewFollowsTheBundle(t *testing.T) {
	cat := components.Default()
	newIDs := []string{"review.words", "runtime.lang", "lang.ar", "lang.en", "lang.hunspell.fr"}
	for _, c := range []struct {
		bundle string
		want   map[string]string
	}{
		{"core", map[string]string{"review.words": "installed", "runtime.lang": "absent", "lang.ar": "absent",
			"lang.en": "absent", "lang.hunspell.fr": "absent"}},
		{"full", map[string]string{"review.words": "enabled", "runtime.lang": "installed", "lang.ar": "installed",
			"lang.en": "installed", "lang.hunspell.fr": "absent"}},
	} {
		t.Run(c.bundle, func(t *testing.T) {
			db := sqliteHome(t)
			st := store.New(db)
			// the home as release N seeded it: every id but the new ones
			wants := map[string]string{}
			for id, w := range PlanNew(cat, c.bundle, nil) {
				wants[id] = w
			}
			for _, id := range newIDs {
				delete(wants, id)
			}
			wants["transcription"] = components.WantInstalled // an admin's choice
			if _, err := st.Seed(ctx, func(_ context.Context, _ store.Queryer) (store.SeedPlan, error) {
				return store.SeedPlan{Wants: wants, Bundle: c.bundle, Release: "0.3.81"}, nil
			}); err != nil {
				t.Fatal(err)
			}
			wrote, err := SeedNew(ctx, st, cat, "0.3.82")
			if err != nil {
				t.Fatal(err)
			}
			for id, w := range c.want {
				if wrote[id] != w {
					t.Errorf("%s: %q, want %q", id, wrote[id], w)
				}
			}
			if len(wrote) != len(newIDs)+len(c.want)-len(newIDs) && len(wrote) < len(newIDs) {
				t.Errorf("wrote %v", wrote)
			}
			d, _, _ := st.Desired(ctx, store.Home)
			if d["transcription"].Want != components.WantInstalled {
				t.Error("the admin's row changed")
			}
			again, err := SeedNew(ctx, st, cat, "0.3.82")
			if err != nil || len(again) != 0 {
				t.Fatalf("second boot wrote %v (%v)", again, err)
			}
			var audits int
			_ = db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'component.seed' AND payload LIKE '%"new":true%'`).Scan(&audits)
			if audits != 1 {
				t.Fatalf("%d audit rows", audits)
			}
		})
	}
}

// A custom home based on core follows core; an unseeded home is the first seed's.
func TestSeedNewCustomAndUnseeded(t *testing.T) {
	cat := components.Default()
	have := PlanNew(cat, "core", nil)
	delete(have, "review.words")
	have["transcription"] = components.WantEnabled // ticked on top of core
	if got := PlanNew(cat, "custom", have); got["review.words"] != components.WantInstalled {
		t.Fatalf("custom on core: %v", got["review.words"])
	}
	st := store.New(sqliteHome(t))
	if wrote, err := SeedNew(ctx, st, cat, "0.3.82"); err != nil || len(wrote) != 0 {
		t.Fatalf("unseeded: %v %v", wrote, err)
	}
}

// An asset an enabled component already needs keeps no row (the first seed leaves it so:
// no row reads as wanted), rather than being written absent.
func TestSeedNewLeavesANeededAssetUnset(t *testing.T) {
	cat := components.Default()
	have := map[string]string{"transcription": components.WantEnabled}
	got := PlanNew(cat, "core", have)
	if _, ok := got["runtime.ml"]; ok {
		t.Fatalf("runtime.ml planned %q under an enabled transcription", got["runtime.ml"])
	}
}
