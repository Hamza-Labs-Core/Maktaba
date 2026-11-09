//go:build integration

package main

import (
	"net/http"
	"testing"
	"time"
)

// Epic 53 story 53.17 §3: the service admin's Errors lists the homes' anonymous groups with
// the problem reports that name each (53.16); with no log index on W set up it says so for
// the cloud's own groups, and the log search answers 503 logs_unavailable. A normal account
// is refused (TestAdminRoutesRequireServiceAdmin walks these routes too).
func TestErrorsJoinTheHomesGroupsWithTheirReports(t *testing.T) {
	e := newEnv(t)
	admin := e.serviceAdmin("ops@example.test")
	user := e.registerVerified("owner@example.test")
	uid := e.userID("owner@example.test")
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1,'Home','err-home','x')`, uid)
	var sid string
	if err := e.pool.DB.QueryRow(`SELECT id FROM servers WHERE slug = 'err-home'`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	e.exec(`INSERT INTO telemetry_error_groups (fingerprint, source, app, message) VALUES ('fp-seg','server','server','segment_missing'),
        ('fp-other','app','apple.player','decode_failed')`)
	e.exec(`INSERT INTO telemetry_error_occurrences (fingerprint, install_id, day, version, count, first_seen, last_seen)
        VALUES ('fp-seg','i1',$1::date,'0.3.95',30,$2,$2), ('fp-seg','i2',$1::date,'0.3.95',12,$2,$2),
               ('fp-other','i3',$1::date,'0.3.94',5,$2,$2)`, now, now)
	for i, fps := range []string{"{fp-seg}", "{fp-seg,fp-x}", "{fp-x}"} {
		e.exec(`INSERT INTO problem_reports (server_id, home_report_id, fingerprints, body) VALUES ($1, $2, $3::text[], '{}')`,
			sid, i+1, fps)
	}

	r := admin.mustStatus(admin.do("GET", "/v1/admin/errors?days=7", nil), http.StatusOK).json(t)
	if r["cloud_error"] != "logs_unavailable" {
		t.Fatalf("no W set up: %v", r["cloud_error"])
	}
	homes, _ := r["homes"].([]any)
	if len(homes) != 2 {
		t.Fatalf("homes: %v", r["homes"])
	}
	first := homes[0].(map[string]any)
	if first["fp"] != "fp-seg" || first["count"] != float64(42) || first["installs"] != float64(2) || first["reports"] != float64(2) {
		t.Fatalf("the most frequent group: %v", first)
	}
	if second := homes[1].(map[string]any); second["reports"] != float64(0) {
		t.Fatalf("a group no report names: %v", second)
	}
	if got := admin.do("GET", "/v1/admin/logs/search?q=x", nil); got.code != http.StatusServiceUnavailable || got.errCode() != "logs_unavailable" {
		t.Fatalf("logs without W: %d %s", got.code, got.body)
	}
	if got := user.do("GET", "/v1/admin/errors", nil); got.code != http.StatusUnauthorized {
		t.Fatalf("a home admin's token: %d", got.code)
	}
}
