package components

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	comps "github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func extRow(t *testing.T, db *sql.DB, name string, enabled bool, at string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO extensions (name, enabled, settings, updated_at)
		VALUES ($1, $2, '{}'::jsonb, $3::timestamptz)
		ON CONFLICT (name) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`,
		name, enabled, at); err != nil {
		t.Fatal(err)
	}
}

func extEnabled(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var on bool
	if err := db.QueryRow(`SELECT enabled FROM extensions WHERE name = $1`, name).Scan(&on); err != nil {
		t.Fatal(err)
	}
	return on
}

func audits(t *testing.T, db *sql.DB, action, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, action, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// T03 (story 51.07): the desired state written through the table moves the mirror with
// it; a hold leaves it alone.
func TestSetDesiredWritesTheMirror(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	extRow(t, db, "plex", true, "2026-11-01T00:00:00Z")
	s := New(db)
	if _, err := SetDesired(ctx, s, store.Desired{ID: "ext.plex", Want: comps.WantInstalled}, admin("component.disable")); err != nil {
		t.Fatal(err)
	}
	if extEnabled(t, db, "plex") {
		t.Fatal("extensions.enabled still true")
	}
	if _, err := SetDesired(ctx, s, store.Desired{ID: "ext.plex", Want: comps.WantEnabled}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	if !extEnabled(t, db, "plex") {
		t.Fatal("extensions.enabled still false")
	}
	held := store.Desired{ID: "ext.plex", Want: comps.WantEnabled, Hold: &store.Hold{By: "admin"}}
	if _, err := SetDesired(ctx, s, held, admin("component.hold")); err != nil {
		t.Fatal(err)
	}
	if !extEnabled(t, db, "plex") {
		t.Fatal("a hold turned the extension off")
	}
}

// T02: a mirror that cannot be written rolls the whole change back.
func TestAFailedMirrorRollsTheChangeBack(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	extRow(t, db, "plex", true, "2026-11-01T00:00:00Z")
	s := New(db)
	if _, err := SetDesired(ctx, s, store.Desired{ID: "ext.plex", Want: comps.WantEnabled}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	keep := Mirrors["ext.plex"]
	t.Cleanup(func() { Mirrors["ext.plex"] = keep })
	Mirrors["ext.plex"] = Mirror{Read: keep.Read, Write: func(context.Context, *sql.Tx, bool) error {
		return errors.New("boom")
	}}
	if _, err := SetDesired(ctx, s, store.Desired{ID: "ext.plex", Want: comps.WantInstalled}, admin("component.disable")); err == nil {
		t.Fatal("no error")
	}
	d, err := s.DesiredOf(ctx, "ext.plex")
	if err != nil || d.Want != comps.WantEnabled {
		t.Fatalf("desired %+v %v, want enabled", d, err)
	}
	if !extEnabled(t, db, "plex") || audits(t, db, "component.disable", "ext.plex") != 0 {
		t.Fatal("one side or the audit row was written")
	}
}

// Switch keeps the hold and names the verb.
func TestSwitchKeepsTheHold(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	extRow(t, db, "dizquetv", true, "2026-11-01T00:00:00Z")
	s := New(db)
	held := store.Desired{ID: "ext.dizquetv", Want: comps.WantEnabled, Hold: &store.Hold{By: "admin"}}
	if _, err := SetDesired(ctx, s, held, admin("component.hold")); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Switch(ctx, s, tx, "ext.dizquetv", false, store.Audit{By: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	d, _ := s.DesiredOf(ctx, "ext.dizquetv")
	if d.Want != comps.WantInstalled || d.Hold == nil {
		t.Fatalf("got %+v", d)
	}
	if extEnabled(t, db, "dizquetv") || audits(t, db, "component.disable", "ext.dizquetv") != 1 {
		t.Fatal("mirror or audit")
	}
}

// The edge case of a rollback and forward again: the newer side wins, an audit row each.
func TestRepairMirrors(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	s := New(db)
	// plex: the desired state is newer than the row an older release left
	extRow(t, db, "plex", false, "2020-01-01T00:00:00Z")
	if _, err := s.SetDesired(ctx, store.Desired{ID: "ext.plex", Want: comps.WantEnabled}, admin("component.enable")); err != nil {
		t.Fatal(err)
	}
	// dizquetv: an older release turned the row on after the desired state was written
	if _, err := s.SetDesired(ctx, store.Desired{ID: "ext.dizquetv", Want: comps.WantInstalled}, admin("component.disable")); err != nil {
		t.Fatal(err)
	}
	extRow(t, db, "dizquetv", true, "2099-01-01T00:00:00Z")
	// cloudflare_stream: in step
	extRow(t, db, "cloudflare_stream", false, "2026-11-01T00:00:00Z")
	if _, err := s.SetDesired(ctx, store.Desired{ID: "ext.cloudflare_stream", Want: comps.WantInstalled}, admin("component.disable")); err != nil {
		t.Fatal(err)
	}

	got, err := RepairMirrors(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "ext.dizquetv" || got[1] != "ext.plex" {
		t.Fatalf("repaired %v", got)
	}
	if !extEnabled(t, db, "plex") {
		t.Fatal("plex's row not put back on")
	}
	if d, _ := s.DesiredOf(ctx, "ext.dizquetv"); d.Want != comps.WantEnabled {
		t.Fatalf("dizquetv want %q, want enabled (its row was newer)", d.Want)
	}
	for _, id := range []string{"ext.plex", "ext.dizquetv"} {
		if audits(t, db, "component.mirror_repaired", id) != 1 {
			t.Errorf("%s: no audit row", id)
		}
	}
	again, err := RepairMirrors(ctx, s, nil)
	if err != nil || len(again) != 0 {
		t.Fatalf("a second pass repaired %v %v", again, err)
	}
}

func TestExtensionComponent(t *testing.T) {
	if ExtensionComponent("plex") != "ext.plex" || ExtensionComponent("websearch") != "" {
		t.Fatal("ExtensionComponent")
	}
	for id := range Mirrors {
		if _, ok := comps.Default().Get(id); !ok {
			t.Errorf("mirror %s is not in the catalog", id)
		}
	}
}
