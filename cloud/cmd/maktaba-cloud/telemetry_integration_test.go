//go:build integration

package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// On the api role's real router: a home posts usage data with no account
// and no server secret; nothing of the request's address is stored; the
// operator view is for service admins only (Epic 16 story 16.7).
func TestTelemetryOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	home := e.client() // anonymous
	day := time.Now().UTC().Format("2006-01-02")
	ts := time.Now().UTC().Format(time.RFC3339)
	env := map[string]any{"schema": 1, "batches": []any{
		map[string]any{"type": "server", "install_id": "HomeInstall0000001", "day": day, "report": map[string]any{
			"version": "0.3.26", "os": "darwin", "arch": "arm64", "install_kind": "mac-app",
			"library_size": "1k-10k", "libraries": "2-5", "users": "2-5", "machines": "1",
			"apps":     []any{map[string]any{"app": "web", "platform": "web", "people": "2-5", "requests": "101-1k"}},
			"features": map[string]any{"live_tv": true},
			"errors":   []any{map[string]any{"message": "open /Users/bob/x.db failed", "count": 2}},
		}},
		map[string]any{"type": "app", "install_id": "AppInstall00000001", "app": "web", "platform": "web",
			"app_version": "1.2.3", "events": []any{
				map[string]any{"kind": "feature.used", "payload": map[string]any{"feature": "library", "count": 3}, "ts": ts}},
			"vitals": []any{map[string]any{"metric": "LCP", "value": 900, "route": "/videos/:id"}}},
	}}
	home.mustStatus(home.do("POST", "/v1/telemetry", env), http.StatusNoContent)
	if r := home.do("POST", "/v1/telemetry", map[string]any{"schema": 1, "batches": []any{map[string]any{"type": "app", "install_id": "x"}}}); r.code != http.StatusBadRequest {
		t.Fatalf("invalid envelope: %d", r.code)
	}
	if n := e.count(`SELECT count(*) FROM telemetry_error_groups WHERE message LIKE '%bob%'`); n != 0 {
		t.Fatal("a home folder reached the store")
	}
	// no table of the usage data has a column that could hold an address
	if n := e.count(`SELECT count(*) FROM information_schema.columns
        WHERE table_name LIKE 'telemetry_%' AND (column_name LIKE '%ip%' OR column_name LIKE '%addr%')`); n != 0 {
		t.Fatalf("%d address-like columns", n)
	}

	user := e.registerVerified("user@example.test")
	if r := user.do("GET", "/v1/admin/usage?days=30", nil); r.code != http.StatusForbidden || r.errCode() != "admin_access_denied" {
		t.Fatalf("normal account: %d %s", r.code, r.body)
	}
	if r := e.client().do("GET", "/v1/admin/usage", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", r.code)
	}
	admin := e.serviceAdmin("ops@example.test")
	u := admin.mustStatus(admin.do("GET", "/v1/admin/usage?days=30", nil), http.StatusOK).json(t)
	inst := u["installs"].(map[string]any)
	if inst["homes"].(float64) != 1 || inst["app_installs"].(float64) != 1 {
		t.Fatalf("installs %v", inst)
	}
	errs := u["errors"].([]any)
	if len(errs) != 1 || !strings.Contains(errs[0].(map[string]any)["message"].(string), "<path>") {
		t.Fatalf("errors %v", errs)
	}

	// forget removes the app install's rows
	home.mustStatus(home.do("POST", "/v1/telemetry", map[string]any{"schema": 1, "batches": []any{
		map[string]any{"type": "forget", "install_id": "AppInstall00000001"}}}), http.StatusNoContent)
	if n := e.count(`SELECT count(*) FROM telemetry_web_vitals`) + e.count(`SELECT count(*) FROM telemetry_app_events`); n != 0 {
		t.Fatalf("%d rows left after forget", n)
	}
}
