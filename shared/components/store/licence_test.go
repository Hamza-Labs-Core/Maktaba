package store

import (
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Story 51.19 (slot 0181): what the licence allows is published on the desired row. A
// change of it moves the revision, not the generation; a publish that changes nothing
// writes nothing; Desired reads it back.
func TestSetLicencesPublishesWithoutANewGeneration(t *testing.T) {
	db := sqliteDB(t)
	st := New(db)
	if _, err := st.SetDesired(ctx, Desired{ID: "cloud.relay", Want: components.WantEnabled}, audit("component.enable")); err != nil {
		t.Fatal(err)
	}
	d, rev0, err := st.Desired(ctx, Home)
	if err != nil {
		t.Fatal(err)
	}
	if d["cloud.relay"].Licence != nil {
		t.Fatalf("nothing published yet: %+v", d["cloud.relay"].Licence)
	}
	gen := d["cloud.relay"].Generation
	until := time.Date(2026, 12, 7, 0, 0, 0, 0, time.UTC)
	n, err := st.SetLicences(ctx, map[string]Licence{"cloud.relay": {Licensed: true, Until: &until},
		"no.row": {Licensed: false}})
	if err != nil || n != 1 {
		t.Fatalf("publish: %d %v", n, err)
	}
	d, rev1, err := st.Desired(ctx, Home)
	if err != nil {
		t.Fatal(err)
	}
	got := d["cloud.relay"]
	if got.Licence == nil || !got.Licence.Licensed || got.Licence.Until == nil || !got.Licence.Until.Equal(until) {
		t.Fatalf("licence %+v", got.Licence)
	}
	if got.Generation != gen {
		t.Fatalf("generation moved: %d -> %d", gen, got.Generation)
	}
	if rev1 != rev0+1 {
		t.Fatalf("revision %d -> %d, want one move", rev0, rev1)
	}
	if _, ok := d["no.row"]; ok {
		t.Fatal("a component with no desired row got one")
	}

	// the same again: nothing written, the revision stays
	if n, err := st.SetLicences(ctx, map[string]Licence{"cloud.relay": {Licensed: true, Until: &until}}); err != nil || n != 0 {
		t.Fatalf("second publish: %d %v", n, err)
	}
	if rev, _ := st.Revision(ctx); rev != rev1 {
		t.Fatalf("revision moved on a publish that changed nothing: %d -> %d", rev1, rev)
	}

	// revoked: licensed false, no until
	if n, err := st.SetLicences(ctx, map[string]Licence{"cloud.relay": {Licensed: false}}); err != nil || n != 1 {
		t.Fatalf("revoke: %d %v", n, err)
	}
	d, _, _ = st.Desired(ctx, Home)
	if l := d["cloud.relay"].Licence; l == nil || l.Licensed || l.Until != nil {
		t.Fatalf("revoked licence %+v", l)
	}
	if d["cloud.relay"].Generation != gen {
		t.Fatal("generation moved on a revocation")
	}

	// an admin's change still moves the generation, and keeps what the licence allows
	if _, err := st.SetDesired(ctx, Desired{ID: "cloud.relay", Want: components.WantInstalled}, audit("component.disable")); err != nil {
		t.Fatal(err)
	}
	d, _, _ = st.Desired(ctx, Home)
	if d["cloud.relay"].Generation != gen+1 || d["cloud.relay"].Licence == nil || d["cloud.relay"].Licence.Licensed {
		t.Fatalf("after an admin's change %+v", d["cloud.relay"])
	}
}

// A database before slot 0181 has no licence columns: Desired reads the rows as before.
func TestDesiredWithoutTheLicenceColumns(t *testing.T) {
	db := sqliteDB(t)
	for _, s := range []string{
		`DROP TRIGGER components_revision_licence`,
		`DROP TRIGGER components_generation_upd`,
		`ALTER TABLE components DROP COLUMN licensed_until`,
		`ALTER TABLE components DROP COLUMN licensed`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO components (id, want, updated_by) VALUES ('cloud.relay', 'enabled', 'seed')`); err != nil {
		t.Fatal(err)
	}
	d, _, err := New(db).Desired(ctx, Home)
	if err != nil {
		t.Fatal(err)
	}
	if d["cloud.relay"].Want != components.WantEnabled || d["cloud.relay"].Licence != nil {
		t.Fatalf("%+v", d["cloud.relay"])
	}
}

func TestLicensedAt(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Minute), now.Add(-time.Minute)
	for _, c := range []struct {
		name string
		l    *Licence
		want bool
	}{
		{"nothing published", nil, true},
		{"granted, no expiry", &Licence{Licensed: true}, true},
		{"granted, inside the grace", &Licence{Licensed: true, Until: &later}, true},
		{"granted, past the grace", &Licence{Licensed: true, Until: &earlier}, false},
		{"exactly at the end", &Licence{Licensed: true, Until: &now}, false},
		{"not granted", &Licence{Licensed: false}, false},
	} {
		if got := (Desired{Licence: c.l}).LicensedAt(now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
