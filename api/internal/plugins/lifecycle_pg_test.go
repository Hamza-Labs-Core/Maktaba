package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginmanifest"
)

const lcPlugin = "dev.example.lifecycle"

// fakeSup is the supervisor's install route: the package's bytes are a manifest.toml (the
// tests write one), validated by the real validator; the folder holds its i18n words.
type fakeSup struct {
	t         *testing.T
	root      string
	discarded []string
}

func (s *fakeSup) InstallPackage(_ context.Context, r io.Reader) (*Installed, error) {
	src, _ := io.ReadAll(r)
	m, errs := pluginmanifest.Validate(src, nil)
	if len(errs) > 0 {
		return nil, &Problem{Status: 400, Code: errs[0].Code, Detail: errs.Error()}
	}
	id, version := m["id"].(string), m["version"].(string)
	folder := version + "_" + strings.Repeat("a", 12)
	dir := filepath.Join(s.root, id, folder)
	if err := os.MkdirAll(filepath.Join(dir, "i18n"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "i18n", "en.json"), []byte(`{"plugin.name":"Lifecycle","plugin.summary":"Tests."}`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "i18n", "ar.json"), []byte(`{"plugin.name":"دورة","plugin.summary":"اختبار."}`), 0o644)
	return &Installed{PluginID: id, Version: version, Digest: strings.Repeat("d", 60) + version[:1] + version[2:3] + "00",
		Folder: folder, Path: dir, Manifest: pluginmanifest.Normalised(m)}, nil
}

func (s *fakeSup) Discard(_ context.Context, id, folder string) error {
	s.discarded = append(s.discarded, id+"/"+folder)
	return nil
}

func lcManifest(version string, required, optional []string, dataVersion int) []byte {
	q := func(l []string) string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = `"` + s + `"`
		}
		return "[" + strings.Join(out, ", ") + "]"
	}
	return []byte(`schema = 1
id = "` + lcPlugin + `"
version = "` + version + `"
plugin_api = "1.0"
maktaba = ">=0.1.0"
licence = "MIT"
data_version = ` + string(rune('0'+dataVersion)) + `

[strings]
name = "plugin.name"
summary = "plugin.summary"

[capabilities]
required = ` + q(required) + `
optional = ` + q(optional) + `

[[stage]]
name = "work"
subject = "video"
export = "run-work"
version = 1
`)
}

type lcFixture struct {
	db  *sql.DB
	l   *Lifecycle
	sup *fakeSup
	lib string
}

func newLC(t *testing.T) *lcFixture {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	t.Cleanup(func() { components.SetLive(nil) })
	sup := &fakeSup{t: t, root: t.TempDir()}
	s := svc.NewService(db)
	l := &Lifecycle{DB: db, Components: s, Sup: sup, Version: "0.3.83", ArtifactsDir: t.TempDir()}
	s.Plugins = l
	f := &lcFixture{db: db, l: l, sup: sup}
	if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ('Films', '{}') RETURNING id::text`).Scan(&f.lib); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *lcFixture) allowUnreviewed(t *testing.T) {
	t.Helper()
	if _, err := f.l.SetPolicy(context.Background(), Policy{AllowUnreviewed: true}, admin); err != nil {
		t.Fatal(err)
	}
}

var admin = svc.Actor{By: "owner", Source: "web"}

func (f *lcFixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func asProblem(err error) *Problem {
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

// 54.07 T01, 54.08 T02, T09, 54.10 T01: the policy refuses an unreviewed file; a grant
// the manifest does not ask for refuses the install and writes nothing; an install writes
// plugin_installs, the grants (no library unless ticked), the desired row (installed) and
// the audit rows, and the live catalog has its entry; the same package again changes
// nothing.
func TestInstallWritesTheRows(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	pkg := lcManifest("1.0.0", nil, []string{"tags.write"}, 1)
	_, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(pkg)}, admin)
	if p := asProblem(err); p == nil || p.Code != CodeUnreviewed {
		t.Fatalf("with the policy off: %v", err)
	}
	f.allowUnreviewed(t)
	_, err = f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(pkg), Grants: []string{"media.read"}, GrantsSet: true}, admin)
	if p := asProblem(err); p == nil || p.Code != CodeUnknownCapability {
		t.Fatalf("a grant not asked for: %v", err)
	}
	if n := f.count(t, `SELECT (SELECT count(*) FROM plugin_installs) + (SELECT count(*) FROM plugin_grants) +
		(SELECT count(*) FROM components WHERE id LIKE 'plugin.%')`); n != 0 || len(f.sup.discarded) != 1 {
		t.Fatalf("a refused install left %d rows (discarded %v)", n, f.sup.discarded)
	}
	res, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(pkg)}, admin)
	if err != nil || res.Already || res.Enabled {
		t.Fatalf("install: %+v %v", res, err)
	}
	cid := components.PluginPrefix + lcPlugin
	var want string
	if err := f.db.QueryRow(`SELECT want FROM components WHERE id = $1`, cid).Scan(&want); err != nil || want != components.WantInstalled {
		t.Fatalf("desired %q %v", want, err)
	}
	var libs []string
	var all bool
	if err := f.db.QueryRow(`SELECT CAST(libraries AS text[]), all_libraries FROM plugin_grants WHERE plugin_id = $1`, lcPlugin).
		Scan(pq.Array(&libs), &all); err != nil || len(libs) != 0 || all {
		t.Fatalf("no library is in scope until ticked: %v %v %v", libs, all, err)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE target_id = $1 AND action IN ('component.install', 'plugin.install')`, cid); n != 2 {
		t.Fatalf("audit rows %d", n)
	}
	if _, ok := components.Live().Get(cid); !ok {
		t.Fatal("the live catalog has no entry for the plugin")
	}
	again, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(pkg)}, admin)
	if err != nil || !again.Already {
		t.Fatalf("again: %+v %v", again, err)
	}
	d, err := f.l.Show(ctx, lcPlugin)
	if err != nil || d.Names.Name["ar"] != "دورة" || d.Rating != 5 || len(d.Stages) != 1 {
		t.Fatalf("detail %+v %v", d, err)
	}
}

// 54.08 T03, T06 and 54.07 T12, T13 (keep-off): enable needs the required grants; a grant
// change moves the generation with an audit row; the Epic 51 route and the plugin route
// write the same keep-off; a reinstall of a kept-off plugin installs it off, and only an
// enable clears it.
func TestGrantsEnableAndKeepOff(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	f.allowUnreviewed(t)
	cid := components.PluginPrefix + lcPlugin
	if _, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", []string{"media.read"}, nil, 1)),
		Enable: true}, admin); err == nil || asProblem(err).Code != CodeRequiredCapability {
		t.Fatalf("install and turn on without the required grant: %v", err)
	}
	if _, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", []string{"media.read"}, nil, 1))}, admin); err != nil {
		t.Fatal(err)
	}
	on := svc.Request{Changes: []svc.Change{{ID: cid, Want: components.WantEnabled}}}
	if _, err := f.l.Components.Apply(ctx, on, admin); asProblem(err) == nil || asProblem(err).Code != CodeRequiredCapability {
		t.Fatalf("enable without the grant: %v", err)
	}
	g0, _ := loadGrantSet(ctx, f.db, lcPlugin)
	g, err := f.l.SetGrants(ctx, lcPlugin, GrantSet{Capabilities: []string{"media.read"}, Libraries: []string{f.lib}}, admin)
	if err != nil || g.Generation != g0.Generation+1 {
		t.Fatalf("grants %+v %v (was %d)", g, err, g0.Generation)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE action = 'plugin.grants' AND target_id = $1`, cid); n != 1 {
		t.Fatalf("grant audit rows %d", n)
	}
	if _, err := f.l.Components.Apply(ctx, on, admin); err != nil {
		t.Fatalf("enable: %v", err)
	}
	// the Epic 51 route's want installed is the plugin route's disable: keep-off
	if _, err := f.l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: cid, Want: components.WantInstalled}}}, admin); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM plugin_keep_off WHERE plugin_id = $1 AND set_by = 'owner'`, lcPlugin); n != 1 {
		t.Fatal("a disable did not set keep-off")
	}
	// uninstalled (the supervisor's runner deletes the install row) and installed again,
	// with "install and turn on": it stays off
	if _, err := f.db.Exec(`DELETE FROM plugin_installs; DELETE FROM plugin_grants`); err != nil {
		t.Fatal(err)
	}
	res, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", []string{"media.read"}, nil, 1)),
		Grants: []string{"media.read"}, GrantsSet: true, Enable: true}, admin)
	if err != nil || res.KeptOff == nil || res.Enabled {
		t.Fatalf("a kept-off reinstall: %+v %v", res, err)
	}
	if _, err := f.l.Components.Apply(ctx, on, admin); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM plugin_keep_off`); n != 0 {
		t.Fatal("an admin's enable did not clear keep-off")
	}
}

// 54.07 T06: a rollback to a version whose data_version is lower is refused unless the
// data is cleared.
func TestRollbackAcrossDataVersions(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	f.allowUnreviewed(t)
	if _, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", nil, nil, 2))}, admin); err != nil {
		t.Fatal(err)
	}
	if err := f.l.Rollback(ctx, lcPlugin, false, admin); asProblem(err) == nil || asProblem(err).Code != CodeNoPrevious {
		t.Fatalf("no previous: %v", err)
	}
	res, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.1.0", nil, nil, 3)), ExpectID: lcPlugin}, admin)
	if err != nil || !res.Updated {
		t.Fatalf("update %+v %v", res, err)
	}
	if _, err := f.db.Exec(`INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, bytes)
		VALUES ($1, 'video', 'v', 'k', '1', 1)`, lcPlugin); err != nil {
		t.Fatal(err)
	}
	if err := f.l.Rollback(ctx, lcPlugin, false, admin); asProblem(err) == nil || asProblem(err).Code != CodeDataNewer {
		t.Fatalf("rollback to an older data version: %v", err)
	}
	if err := f.l.Rollback(ctx, lcPlugin, true, admin); err != nil {
		t.Fatal(err)
	}
	var version, prev string
	_ = f.db.QueryRow(`SELECT version, previous_version FROM plugin_installs WHERE plugin_id = $1`, lcPlugin).Scan(&version, &prev)
	if version != "1.0.0" || prev != "1.1.0" || f.count(t, `SELECT count(*) FROM plugin_data`) != 0 {
		t.Fatalf("after the rollback: %s (previous %s)", version, prev)
	}
}

// 54.07 T07, 54.08 T07: a purge reverts each write whose target still holds what the
// plugin left, keeps an admin's later edit, and deletes the plugin's data.
func TestPurgeRevertsOnlyUntouchedWrites(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	f.allowUnreviewed(t)
	if _, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", nil, []string{"metadata.write"}, 1))}, admin); err != nil {
		t.Fatal(err)
	}
	video := func(hash string) string {
		var id string
		if err := f.db.QueryRow(`INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec, description)
			VALUES ($1, $2, '/x/'||$2, 'a.mp4', 1, now(), 'T', 60, 'mine') RETURNING id::text`, f.lib, hash).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := video(strings.Repeat("1", 64)), video(strings.Repeat("2", 64))
	for _, v := range []string{a, b} {
		if _, err := f.db.Exec(`UPDATE videos SET description = 'from the plugin' WHERE id = $1`, v); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`INSERT INTO plugin_writes (plugin_id, version, target, subject_id, field, before, after)
			VALUES ($1, '1.0.0', 'videos', $2, 'description', '"mine"', '"from the plugin"')`, lcPlugin, v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`UPDATE videos SET description = 'the admin''s' WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, bytes)
		VALUES ($1, 'video', $2, 'k', '1', 1)`, lcPlugin, a); err != nil {
		t.Fatal(err)
	}
	n, err := f.l.Components.PurgePlugin(ctx, components.PluginPrefix+lcPlugin, admin)
	if err != nil || n != 1 {
		t.Fatalf("purge reverted %d: %v", n, err)
	}
	var da, db string
	_ = f.db.QueryRow(`SELECT description FROM videos WHERE id = $1`, a).Scan(&da)
	_ = f.db.QueryRow(`SELECT description FROM videos WHERE id = $1`, b).Scan(&db)
	if da != "mine" || db != "the admin's" {
		t.Fatalf("after the purge: %q %q", da, db)
	}
	if f.count(t, `SELECT count(*) FROM plugin_data`) != 0 {
		t.Fatal("the purge kept the plugin's data")
	}
	if ws, _ := f.l.Writes(ctx, lcPlugin, 0, 10); len(ws) != 2 || (ws[0].RevertedAt == nil) == (ws[1].RevertedAt == nil) {
		t.Fatalf("writes %+v", ws)
	}
}

// 54.09 T01 (the API's half): a tripped breaker holds the plugin once, by plugin-breaker,
// with one notice to admins; an admin's Start lifts it and clears the breaker.
func TestBreakerHoldsOnce(t *testing.T) {
	f := newLC(t)
	ctx := context.Background()
	f.allowUnreviewed(t)
	if _, err := f.l.Install(ctx, InstallRequest{Package: bytes.NewReader(lcManifest("1.0.0", nil, nil, 1)), Enable: true}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO plugin_health (plugin_id, tripped, trip_reason) VALUES ($1, true, 'traps')`, lcPlugin); err != nil {
		t.Fatal(err)
	}
	held, err := f.l.Trip(ctx, lcPlugin, "traps")
	if err != nil || !held {
		t.Fatalf("trip %v %v", held, err)
	}
	if again, _ := f.l.Trip(ctx, lcPlugin, "traps"); again {
		t.Fatal("a held plugin was held again")
	}
	var by string
	_ = f.db.QueryRow(`SELECT hold_by FROM components WHERE id = $1`, components.PluginPrefix+lcPlugin).Scan(&by)
	if by != HoldBreaker || f.count(t, `SELECT count(*) FROM notifications WHERE kind = 'plugin' AND subject = $1`, lcPlugin) != 1 {
		t.Fatalf("hold %q", by)
	}
	off := false
	if _, err := f.l.Components.Apply(ctx, svc.Request{Changes: []svc.Change{{ID: components.PluginPrefix + lcPlugin, Hold: &off}}}, admin); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM plugin_health WHERE tripped`) != 0 {
		t.Fatal("Start did not clear the breaker")
	}
}

// 54.37 T03 (the API): a plugin_installs change heard on NOTIFY swaps the live catalog.
func TestCatalogFollowsTheInstalls(t *testing.T) {
	db, dsn := clustertest.ScratchDB(t)
	t.Cleanup(func() { components.SetLive(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go WatchCatalog(ctx, db, dsn, nil)
	m, errs := pluginmanifest.Validate(lcManifest("1.0.0", nil, nil, 1), nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	time.Sleep(300 * time.Millisecond) // its first read and LISTEN
	if _, err := db.Exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest) VALUES ($1, '1.0.0', 'd', '/p', $2)`,
		lcPlugin, string(pluginmanifest.Normalised(m))); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if c, ok := components.Live().Get(components.PluginPrefix + lcPlugin); ok {
			if c.Kind != components.KindPlugin || len(c.Stages) != 1 {
				t.Fatalf("entry %+v", c)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the live catalog did not follow plugin_installs")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := db.Exec(`DELETE FROM plugin_installs`); err != nil {
		t.Fatal(err)
	}
	for {
		if _, ok := components.Live().Get(components.PluginPrefix + lcPlugin); !ok {
			return
		}
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatal("an uninstall stayed in the live catalog")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
