package components

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// Story 51.07 §1 through the components routes: turning ext.plex, ext.dizquetv or the
// HDHomeRun on or off by PUT, apply or a bundle writes the older switch
// (extensions.enabled, hdhr_device.enabled) in the same transaction as the desired row,
// so the pipeline stops answering extension_disabled at once, not at the next start.

// mirrorFixture is pgFixture with the extensions rows off and the tuner row off.
func mirrorFixture(t *testing.T) *fixture {
	t.Helper()
	f, _ := pgFixture(t)
	for _, name := range []string{"plex", "dizquetv", "cloudflare_stream"} {
		if _, err := f.db.Exec(`INSERT INTO extensions (name, enabled, settings, updated_at)
			VALUES ($1, false, '{}'::jsonb, now()) ON CONFLICT (name) DO UPDATE SET enabled = false`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO hdhr_device (id, device_id, enabled) VALUES (1, 'T1', false)
		ON CONFLICT (id) DO UPDATE SET enabled = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SetDesiredBatch(context.Background(), func(map[string]store.Desired, int64) (store.BatchPlan, error) {
		var p store.BatchPlan
		for _, id := range []string{"ext.plex", "ext.dizquetv", "livetv.hdhomerun"} {
			p.Writes = append(p.Writes, store.BatchWrite{Desired: store.Desired{ID: id, Want: components.WantInstalled},
				Audit: store.Audit{By: store.SeedBy, ActorSource: "seed", Action: "component.seed"}})
		}
		return p, nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) extEnabled(t *testing.T, name string) bool {
	t.Helper()
	var on bool
	if err := f.db.QueryRow(`SELECT enabled FROM extensions WHERE name = $1`, name).Scan(&on); err != nil {
		t.Fatal(err)
	}
	return on
}

func (f *fixture) tunerEnabled(t *testing.T) bool {
	t.Helper()
	var on bool
	if err := f.db.QueryRow(`SELECT enabled FROM hdhr_device WHERE id = 1`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	return on
}

func TestPGPutWritesTheMirror(t *testing.T) {
	f := mirrorFixture(t)
	decode(t, f.do("PUT", "/api/components/ext.plex", `{"want":"enabled"}`, admin()), 200, nil)
	if !f.extEnabled(t, "plex") {
		t.Fatal("PUT ext.plex enabled left extensions.enabled false")
	}
	decode(t, f.do("PUT", "/api/components/livetv.hdhomerun", `{"want":"enabled"}`, admin()), 200, nil)
	if !f.tunerEnabled(t) {
		t.Fatal("PUT livetv.hdhomerun enabled left hdhr_device.enabled false")
	}
	decode(t, f.do("PUT", "/api/components/ext.plex", `{"want":"installed"}`, admin()), 200, nil)
	if f.extEnabled(t, "plex") {
		t.Fatal("PUT ext.plex installed left extensions.enabled true")
	}
	// a hold leaves the mirror: the extension is still enabled, only stopped now
	decode(t, f.do("PUT", "/api/components/ext.dizquetv", `{"want":"enabled"}`, admin()), 200, nil)
	decode(t, f.do("POST", "/api/components/ext.dizquetv/stop", "", admin()), 200, nil)
	if !f.extEnabled(t, "dizquetv") {
		t.Fatal("a hold turned extensions.enabled off")
	}
}

func TestPGApplyWritesTheMirror(t *testing.T) {
	f := mirrorFixture(t)
	decode(t, f.do("POST", "/api/components/apply", `{"changes":[{"id":"ext.plex","want":"enabled"},
		{"id":"ext.dizquetv","want":"enabled"},{"id":"livetv.hdhomerun","want":"enabled"}]}`, admin()), 200, nil)
	if !f.extEnabled(t, "plex") || !f.extEnabled(t, "dizquetv") || !f.tunerEnabled(t) {
		t.Fatalf("apply: plex %v, dizquetv %v, tuner %v", f.extEnabled(t, "plex"), f.extEnabled(t, "dizquetv"), f.tunerEnabled(t))
	}
}

// A bundle applies through the same batch: full turns the HDHomeRun on and Plex (in no
// bundle) off.
func TestPGBundleWritesTheMirror(t *testing.T) {
	f := mirrorFixture(t)
	decode(t, f.do("PUT", "/api/components/ext.plex", `{"want":"enabled"}`, admin()), 200, nil)
	decode(t, f.do("POST", "/api/components/bundle", `{"bundle":"full"}`, admin()), 200, nil)
	if f.want(t, "livetv.hdhomerun").Want != components.WantEnabled || f.want(t, "ext.plex").Want != components.WantInstalled {
		t.Fatalf("bundle full: hdhomerun %+v, plex %+v", f.want(t, "livetv.hdhomerun"), f.want(t, "ext.plex"))
	}
	if !f.tunerEnabled(t) || f.extEnabled(t, "plex") {
		t.Fatalf("bundle full: tuner %v, plex %v", f.tunerEnabled(t), f.extEnabled(t, "plex"))
	}
}

// Same transaction: a mirror that cannot be written rolls the desired row back with it.
func TestPGMirrorFailureRollsBackTheChange(t *testing.T) {
	f := mirrorFixture(t)
	for _, q := range []string{
		`CREATE FUNCTION mirror_refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refused'; END $$`,
		`CREATE TRIGGER mirror_refuse BEFORE UPDATE ON extensions FOR EACH ROW EXECUTE FUNCTION mirror_refuse()`,
	} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rev := f.revision(t)
	if rec := f.do("PUT", "/api/components/ext.plex", `{"want":"enabled"}`, admin()); rec.Code != 500 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if d := f.want(t, "ext.plex"); d.Want != components.WantInstalled || f.revision(t) != rev {
		t.Fatalf("the desired row moved without its mirror: %+v, revision %d -> %d", d, rev, f.revision(t))
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM audit_log WHERE target_id = 'ext.plex' AND action = 'component.enable'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d audit rows (%v)", n, err)
	}
}
