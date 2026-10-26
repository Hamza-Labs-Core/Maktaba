//go:build integration

package telemetry

import (
	"context"
	"database/sql"
	neturl "net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Against Postgres (MAKTABA_CLOUD_DB_URL), in a scratch database of its own.
func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_tel_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 4, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	if err := db.NewMigrator(pool, migrations.FS, ".").Up(ctx); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: pool.DB}, ctx
}

func ingest(ctx context.Context, t *testing.T, s *Store, now time.Time, batches ...map[string]any) {
	t.Helper()
	bs, err := Parse(envelope(batches...), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ingest(ctx, bs, now); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIngestUpsertAndForget(t *testing.T) {
	s, ctx := testStore(t)
	ingest(ctx, t, s, testNow, serverBatch(), appBatch())

	// a resend of the same day replaces the report and its errors
	again := serverBatch()
	again["report"].(map[string]any)["version"] = "0.3.27"
	again["report"].(map[string]any)["errors"] = []any{map[string]any{"message": "db timeout", "count": 5}}
	ingest(ctx, t, s, testNow, again)
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_server_reports`); n != 1 {
		t.Fatalf("reports: %d", n)
	}
	var version string
	_ = s.DB.QueryRow(`SELECT version FROM telemetry_server_reports`).Scan(&version)
	if version != "0.3.27" {
		t.Fatalf("version %s", version)
	}
	if n := count(t, s.DB, `SELECT sum(count) FROM telemetry_error_occurrences o JOIN telemetry_error_groups g USING (fingerprint) WHERE g.source='server'`); n != 5 {
		t.Fatalf("server error count after resend: %d", n)
	}

	// app errors add up; events and vitals are kept; error.uncaught is not an event
	ingest(ctx, t, s, testNow, appBatch())
	if n := count(t, s.DB, `SELECT sum(count) FROM telemetry_error_occurrences o JOIN telemetry_error_groups g USING (fingerprint) WHERE g.source='app'`); n != 2 {
		t.Fatalf("app error count: %d", n)
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_app_events WHERE kind = 'error.uncaught'`); n != 0 {
		t.Fatal("error.uncaught stored as an event")
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_app_events`); n != 8 {
		t.Fatalf("events: %d", n)
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_app_installs`); n != 1 {
		t.Fatalf("app install days: %d", n)
	}

	// forget the app: every row of its install id goes, the home's stay
	ingest(ctx, t, s, testNow, map[string]any{"type": "forget", "install_id": appID})
	for _, tbl := range installTables {
		if n := count(t, s.DB, `SELECT count(*) FROM `+tbl+` WHERE install_id = $1`, appID); n != 0 {
			t.Errorf("%s keeps %d rows of a forgotten install", tbl, n)
		}
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_error_groups WHERE source = 'app'`); n != 0 {
		t.Errorf("orphan app error groups: %d", n)
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_server_reports WHERE install_id = $1`, homeID); n != 1 {
		t.Error("forgetting the app removed the home's report")
	}
}

// One envelope is kept whole or not at all.
func TestIngestIsAtomic(t *testing.T) {
	s, ctx := testStore(t)
	bs, err := Parse(envelope(appBatch(), serverBatch()), testNow)
	if err != nil {
		t.Fatal(err)
	}
	bs[1].Server.Errors = []ReportError{{Message: "x", Count: -1}} // violates the count CHECK
	if err := s.Ingest(ctx, bs, testNow); err == nil {
		t.Fatal("bad batch accepted")
	}
	for _, tbl := range installTables {
		if n := count(t, s.DB, `SELECT count(*) FROM `+tbl); n != 0 {
			t.Errorf("%s has %d rows from a rejected envelope", tbl, n)
		}
	}
}

func TestSweepRetention(t *testing.T) {
	s, ctx := testStore(t)
	ingest(ctx, t, s, testNow, serverBatch(), appBatch())
	exec := func(q string, args ...any) {
		if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	old := func(days int) time.Time { return testNow.AddDate(0, 0, -days) }
	exec(`INSERT INTO telemetry_server_reports (install_id, day, version, os, arch, install_kind, library_size, libraries, users, machines)
        VALUES ('OldHome0000000001', $1, '0.1.0', 'linux', 'amd64', 'docker', '0', '0', '1', '1'),
               ('KeptHome000000001', $2, '0.1.0', 'linux', 'amd64', 'docker', '0', '0', '1', '1')`, old(401), old(399))
	exec(`INSERT INTO telemetry_app_installs (install_id, day, app, platform, app_version) VALUES ('OldApp00000000001', $1, 'tv', 'tvos', '1.0')`, old(91))
	exec(`INSERT INTO telemetry_app_events (install_id, app, app_version, kind, ts) VALUES ('OldApp00000000001', 'tv', '1.0', 'app.open', $1)`, old(91))
	exec(`INSERT INTO telemetry_error_groups (fingerprint, source, app, message, created_at) VALUES ('oldfp', 'app', 'tv', 'old', $1)`, old(95))
	exec(`INSERT INTO telemetry_error_occurrences (fingerprint, install_id, day, version, count, first_seen, last_seen) VALUES ('oldfp', 'OldApp00000000001', $1, '1.0', 1, $2, $2)`, old(91), old(91))
	exec(`INSERT INTO telemetry_web_vitals (install_id, app, app_version, metric, value, received_at) VALUES ('OldApp00000000001', 'web', '1.0', 'LCP', 1, $1)`, old(31))

	res, err := s.Sweep(ctx, DefaultRetention, testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := SweepResult{ServerReports: 1, AppInstalls: 1, AppEvents: 1, ErrorOccurrences: 1, ErrorGroups: 1, Vitals: 1}
	if res != want {
		t.Fatalf("sweep %+v, want %+v", res, want)
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_server_reports`); n != 2 {
		t.Fatalf("reports left %d, want today's and the 399-day-old one", n)
	}
	if n := count(t, s.DB, `SELECT count(*) FROM telemetry_web_vitals`); n != 1 {
		t.Fatalf("vitals left %d", n)
	}
}

func TestQueryUsage(t *testing.T) {
	s, ctx := testStore(t)
	ingest(ctx, t, s, testNow, serverBatch(), appBatch())
	// a second home on an older version, without the live_tv feature
	h2 := serverBatch()
	h2["install_id"] = "HomeInstall0000002"
	h2["day"] = "2026-10-25"
	r2 := h2["report"].(map[string]any)
	r2["version"], r2["os"], r2["install_kind"] = "0.3.25", "linux", "docker"
	r2["features"] = map[string]any{"live_tv": false, "transcoding": "0"}
	r2["errors"] = []any{map[string]any{"message": "db timeout", "count": 2}}
	ingest(ctx, t, s, testNow, h2)
	// a tv app install
	tv := appBatch()
	tv["install_id"], tv["app"], tv["platform"], tv["form_factor"], tv["app_version"] = "TvInstall000000001", "tv", "tvos", "tv", "2.0.0"
	tv["events"] = []any{map[string]any{"kind": "feature.used", "payload": map[string]any{"feature": "library", "count": 6}, "ts": "2026-10-26T10:00:00Z"}}
	tv["vitals"] = []any{}
	ingest(ctx, t, s, testNow, tv)

	u, err := QueryUsage(ctx, s.DB, testNow, 30)
	if err != nil {
		t.Fatal(err)
	}
	if u.Installs.Homes != 2 || u.Installs.AppInstalls != 2 || len(u.Installs.ByApp) != 2 {
		t.Fatalf("installs %+v", u.Installs)
	}
	if len(u.ServerVersions) != 2 || u.ServerVersions[0].Homes != 1 {
		t.Errorf("server versions %+v", u.ServerVersions)
	}
	if len(u.AppVersions) != 2 {
		t.Errorf("app versions %+v", u.AppVersions)
	}
	if len(u.InstallKinds) != 2 || len(u.ServerPlatforms) != 2 {
		t.Errorf("kinds %+v platforms %+v", u.InstallKinds, u.ServerPlatforms)
	}
	var web *AppInUse
	for i := range u.AppsInUse {
		if u.AppsInUse[i].App == "web" {
			web = &u.AppsInUse[i]
		}
	}
	if web == nil || web.Homes != 2 || web.Installs != 1 {
		t.Errorf("apps in use %+v", u.AppsInUse)
	}
	shares := map[string]FeatureShare{}
	for _, f := range u.Features {
		shares[f.Feature] = f
	}
	if shares["live_tv"].Homes != 1 || shares["live_tv"].Share != 0.5 || shares["transcoding"].Homes != 1 {
		t.Errorf("features %+v", u.Features)
	}
	if _, ok := shares["sync"]; ok {
		t.Error("a feature no home uses is listed")
	}
	var lib int64
	for _, f := range u.FeatureUse {
		if f.Feature == "library" {
			lib += f.Total
		}
	}
	if lib != 10 || len(u.FeatureUse) != 2 {
		t.Errorf("feature use %+v", u.FeatureUse)
	}
	var serverErr *ErrorGroup
	for i := range u.Errors {
		if u.Errors[i].Source == "server" {
			serverErr = &u.Errors[i]
		}
	}
	if serverErr == nil || serverErr.Count != 5 || serverErr.Installs != 2 || len(serverErr.Versions) != 2 || serverErr.Versions[0] != "0.3.26" {
		t.Errorf("errors %+v", u.Errors)
	}
	if len(u.Vitals) != 1 || u.Vitals[0].Metric != "LCP" || u.Vitals[0].P75 != 1234.5 {
		t.Errorf("vitals %+v", u.Vitals)
	}

	// a one-day window leaves out the home that last reported yesterday
	u1, err := QueryUsage(ctx, s.DB, testNow, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u1.Installs.Homes != 1 {
		t.Errorf("1-day homes %d", u1.Installs.Homes)
	}
}
