package pluginsig

import (
	"path/filepath"
	"testing"
	"time"
)

// Story 54.14 §3, §4 and T04: the newest-seen state is never lowered, by a restored file
// or by Postgres holding an older copy, and a clock in the past does not move time back.
func TestStateNeverGoesBack(t *testing.T) {
	path := TrustFile(t.TempDir())
	t0 := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if err := SaveState(path, State{RevokedVersion: 120, RevokedSHA256: "a", StoreKeysVersion: 3, NewestIssued: t0}); err != nil {
		t.Fatal(err)
	}
	// an older copy (say, Postgres restored from last week) written over it
	if err := SaveState(path, State{RevokedVersion: 100, StoreKeysVersion: 2, NewestIssued: t0.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.RevokedVersion != 120 || st.RevokedSHA256 != "a" || st.StoreKeysVersion != 3 || !st.NewestIssued.Equal(t0) {
		t.Fatalf("lowered: %+v", st)
	}
	if got := st.Now(t0.Add(-48 * time.Hour)); !got.Equal(t0) {
		t.Fatalf("a clock in the past gave %s", got)
	}
	if got := st.Now(t0.Add(time.Hour)); !got.Equal(t0.Add(time.Hour)) {
		t.Fatalf("a later clock gave %s", got)
	}
}

func TestStateMergeTakesTheNewer(t *testing.T) {
	a := State{CatalogVersions: map[string]int64{"1": 5}, RevokedVersion: 3}
	a.Merge(State{CatalogVersions: map[string]int64{"1": 4, "2": 9}, RevokedVersion: 7, RevokedSHA256: "x"})
	if a.CatalogVersion(1) != 5 || a.CatalogVersion(2) != 9 || a.RevokedVersion != 7 || a.RevokedSHA256 != "x" {
		t.Fatalf("%+v", a)
	}
}

func TestLoadStateMissingIsZero(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || st.RevokedVersion != 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

// A release build trusts no test key: the committed root never lists one of the vectors'
// keys (anyone can recompute them). Until the owner makes the root keys it lists none.
func TestTheCommittedRootListsNoTestKey(t *testing.T) {
	r, err := EmbeddedRoot()
	if err != nil {
		t.Fatal(err)
	}
	v := loadVectors(t)
	for _, id := range r.KeyIDs() {
		for name, k := range v.Keys {
			if k.ID == id {
				t.Fatalf("plugins-root.json lists the test key %s (%s)", name, id)
			}
		}
	}
}
