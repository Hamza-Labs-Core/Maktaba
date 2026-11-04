package supervisor

import (
	"slices"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.19: a component whose licence the published row no longer grants waits with
// licence_missing, its want kept, and its dependents wait on it; when the licence's
// grace ends the supervisor stops it on its own clock, with nobody writing the rows.
func TestReconcileLicence(t *testing.T) {
	cat := testCatalog(t, procComp("a", "fake/a", "", `licence = "cloud_relay"`)+procComp("b", "fake/b", `"a"`, ""))
	tab := &fakeTable{}
	tab.add("a", "a", false)
	tab.add("b", "b", false)
	st := &fakeStore{}
	r, _ := newTestReconciler(cat, tab, st)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	until := now.Add(time.Minute)
	rows := func() map[string]store.Desired {
		a := want("a", "enabled", 4)
		a.Licence = &store.Licence{Licensed: true, Until: &until}
		return map[string]store.Desired{"a": a, "b": want("b", "enabled", 1)}
	}
	passWith(r, rows())
	if got := tab.takeCalls(); len(got) != 0 {
		t.Fatalf("calls %v: inside the grace nothing changes", got)
	}

	now = now.Add(2 * time.Minute)
	passWith(r, rows())
	if got := tab.takeCalls(); !slices.Equal(got, []string{"b=false", "a=false"}) {
		t.Fatalf("calls %v, want b then a closed", got)
	}
	row, _ := st.row("a")
	if row.Phase != components.PhaseWaiting || row.Reason() != "licence_missing" || row.ObservedGeneration != 4 {
		t.Fatalf("a %+v, want waiting on licence_missing at generation 4", row)
	}
	if c := row.Conditions[0]; c.Type != components.CondLicensed || c.Params["feature"] != "cloud_relay" {
		t.Fatalf("a's condition %+v", c)
	}
	if b, _ := st.row("b"); b.Phase != components.PhaseWaiting || b.Reason() != "dependency_off" {
		t.Fatalf("b %+v, want waiting on dependency_off", b)
	}

	// a licence applied again: open, requirements first
	until = now.Add(31 * 24 * time.Hour)
	var opens []string
	for range 3 {
		passWith(r, rows())
		opens = append(opens, tab.takeCalls()...)
	}
	if !slices.Equal(opens, []string{"a=true", "b=true"}) {
		t.Fatalf("opens %v", opens)
	}
}
