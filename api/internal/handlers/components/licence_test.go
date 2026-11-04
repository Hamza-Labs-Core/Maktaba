package components

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// freeFixture is newFixture on a free home: the licence grants no flag.
func freeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.licence = false
	f.r = f.router()
	return f
}

// Story 51.19 T01: on a free home an admin's enable of cloud.relay is 403 with the
// licence's own problem (type license-required), code licence_missing and the
// component; the row and the audit log are unchanged.
func TestLicenceRefusalBody(t *testing.T) {
	f := freeFixture(t)
	before, rev := f.want(t, "cloud.relay"), f.revision(t)
	for _, c := range []struct{ method, path, body string }{
		{"PUT", "/api/components/cloud.relay", `{"want":"enabled"}`},
		{"POST", "/api/components/apply", `{"changes":[{"id":"cloud.relay","want":"enabled"}]}`},
	} {
		body := code(t, f.do(c.method, c.path, c.body, admin()), http.StatusForbidden, svc.CodeLicence)
		for k, want := range map[string]any{"type": subscriptions.TypeLicenseRequired, "title": "Not included in this home's plan",
			"component": "cloud.relay", "feature": "cloud_relay", "tier": "free", "required_tier": "home",
			"id": "cloud.relay", "licence": "cloud_relay", "status": float64(403)} {
			if body[k] != want {
				t.Errorf("%s %s: %s = %v, want %v", c.method, c.path, k, body[k], want)
			}
		}
		if d, _ := body["detail"].(string); !strings.Contains(d, "This home is on Free") {
			t.Errorf("detail %q", d)
		}
	}
	if d := f.want(t, "cloud.relay"); d.Want != before.Want || d.Generation != before.Generation {
		t.Fatalf("row changed: %+v -> %+v", before, d)
	}
	if f.revision(t) != rev || f.audits(t) != 0 {
		t.Fatal("a refusal wrote")
	}
	// the preview lists it and changes nothing
	var p svc.Preview
	decode(t, f.do("POST", "/api/components/preview", `{"changes":[{"id":"cloud.relay","want":"enabled"}]}`, admin()), 200, &p)
	if len(p.Refused) != 1 || p.Refused[0].Code != svc.CodeLicence || p.Refused[0].Tier != "free" {
		t.Fatalf("preview refused %+v", p.Refused)
	}
	// a lapsed licence keeps the wish: enabled already, asking again is still refused
	if _, err := f.db.Exec(`UPDATE components SET want = 'enabled' WHERE id = 'cloud.relay'`); err != nil {
		t.Fatal(err)
	}
	code(t, f.do("PUT", "/api/components/cloud.relay", `{"want":"enabled"}`, admin()), http.StatusForbidden, svc.CodeLicence)
	// and it reads waiting, licence_missing, while no runner has reported
	var v svc.Detail
	decode(t, f.do("GET", "/api/components/cloud.relay", "", admin()), 200, &v)
	if v.Licensed || v.Phase != components.PhaseWaiting || v.Reason != svc.CodeLicence || v.Want != components.WantEnabled {
		t.Fatalf("view %+v", v.View)
	}
}

// T02: with the licence the same enable passes and moves the generation.
func TestLicensedEnable(t *testing.T) {
	f := newFixture(t)
	gen := f.want(t, "cloud.relay").Generation
	decode(t, f.do("PUT", "/api/components/cloud.relay", `{"want":"enabled"}`, admin()), 200, nil)
	if d := f.want(t, "cloud.relay"); d.Want != components.WantEnabled || d.Generation != gen+1 {
		t.Fatalf("cloud.relay %+v", d)
	}
}

// T03: the role check comes first: no one is 401, a viewer and an admin's play sign-in
// are 403 admin-only, on every enable path; never licence_missing (a viewer never
// learns the plan).
func TestLicenceRoleOrder(t *testing.T) {
	f := freeFixture(t)
	paths := []struct{ method, path, body string }{
		{"PUT", "/api/components/cloud.relay", `{"want":"enabled"}`},
		{"POST", "/api/components/apply", `{"changes":[{"id":"cloud.relay","want":"enabled"}]}`},
		{"POST", "/api/components/preview", `{"changes":[{"id":"cloud.relay","want":"enabled"}]}`},
		{"POST", "/api/components/apply", `{"bundle":"full"}`},
	}
	for _, c := range paths {
		rec := f.do(c.method, c.path, c.body, nil)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "licence") {
			t.Errorf("%s %s as no one: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
		for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin()} {
			rec := f.do(c.method, c.path, c.body, p)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "admin-only") ||
				strings.Contains(rec.Body.String(), "licence") || strings.Contains(rec.Body.String(), "license") {
				t.Errorf("%s %s as %s: %d %s", c.method, c.path, name, rec.Code, rec.Body)
			}
		}
	}
	if f.audits(t) != 0 {
		t.Fatal("a refused caller wrote")
	}
}

// T04: installing, removing, stopping and starting need no plan.
func TestLicenceOnlyGatesEnable(t *testing.T) {
	f := freeFixture(t)
	decode(t, f.do("DELETE", "/api/components/cloud.relay", "", admin()), 200, nil)
	if f.want(t, "cloud.relay").Want != components.WantAbsent {
		t.Fatal("not removed")
	}
	decode(t, f.do("POST", "/api/components/cloud.relay/install", "", admin()), 200, nil)
	decode(t, f.do("PUT", "/api/components/cloud.relay", `{"want":"installed"}`, admin()), 200, nil)
	if f.want(t, "cloud.relay").Want != components.WantInstalled {
		t.Fatal("not installed")
	}
	decode(t, f.do("POST", "/api/components/cloud.relay/stop", "", admin()), 200, nil)
	decode(t, f.do("POST", "/api/components/cloud.relay/start", "", admin()), 200, nil)
	// cloud.link is never gated
	decode(t, f.do("PUT", "/api/components/cloud.link", `{"want":"installed"}`, admin()), 200, nil)
	decode(t, f.do("PUT", "/api/components/cloud.link", `{"want":"enabled"}`, admin()), 200, nil)
}
