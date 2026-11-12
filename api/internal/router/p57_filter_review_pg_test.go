package router_test

// Epic 57, review of the filter: what a restricted account's old token may still claim,
// what a policy that cannot be read means, and the rest of the review's findings, behind
// the production router on a real migrated Postgres.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// An admin is demoted and then restricted. Its access token, minted while it was an admin,
// still says `is_admin` and every library until it runs out. The gate reads the account's
// policy, so the claim counts for nothing: the account cannot lift or loosen its own
// restriction, is refused the admin's routes, is not shown the other children, and reads
// only the libraries it was given.
func TestP57StaleAdminTokenOnARestrictedAccount(t *testing.T) {
	h := setupHome(t)
	// the child was given library A only
	h.exec(`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, pfChild, pfLibB)
	h.as = func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == pfChild {
			return &principal.Principal{UserID: pfChild, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		}
		return p
	}
	policy := func() (tier, guardians, rules int) {
		t.Helper()
		if err := h.db.QueryRow(`SELECT COALESCE((SELECT max_tier FROM parental_policies WHERE user_id = $1), -99),
			(SELECT count(*) FROM parental_guardians WHERE child_id = $1),
			(SELECT count(*) FROM parental_rules WHERE user_id = $1)`, pfChild).Scan(&tier, &guardians, &rules); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, x := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/users/" + pfChild + "/parental", ""},
		{http.MethodPut, "/api/users/" + pfChild + "/parental", `{"max_tier":18,"unrated":"tier_0","allow_only":false,"version":1}`},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/guardians", `{"guardian_id":"` + pfViewer + `"}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental/guardians/" + pfGuardian, ""},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/rules", `{"effect":"allow","subject_kind":"video","subject_id":"` + pfTeen + `"}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental/rules/" + phRule, ""},
		{http.MethodPost, "/api/parental/requests/" + phRequest + "/approve", "{}"},
		{http.MethodPut, "/api/users/" + pfChild2 + "/parental", `{"max_tier":18,"unrated":"tier_0","allow_only":false,"version":1}`},
		{http.MethodPut, "/api/videos/" + pfTeen + "/age-tier", `{"tier":0}`},
	} {
		if rec := h.raw("child", x.method, x.path, x.body); rec.Code/100 == 2 {
			t.Errorf("%s %s with the old admin token: %d %s", x.method, x.path, rec.Code, rec.Body.String())
		}
	}
	if tier, guardians, rules := policy(); tier != 9 || guardians != 1 || rules != 1 {
		t.Errorf("after the attempts: limit %d, %d guardians, %d rules; want 9, 1, 1", tier, guardians, rules)
	}
	// the admin's routes
	for _, p := range []string{"/api/users", "/api/parental/settings", "/api/parental/review"} {
		if rec := h.raw("child", http.MethodGet, p, ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"restricted"`) {
			t.Errorf("GET %s with the old admin token: %d %s, want 403 restricted", p, rec.Code, rec.Body.String())
		}
	}
	// the routes that guard themselves read the role the gate left
	if code, body := h.get("child", "/api/parental/children"); code/100 == 2 && (strings.Contains(body, pfChild2) || strings.Contains(body, "pf-child-two")) {
		t.Errorf("GET /api/parental/children with the old admin token names the other child: %d %s", code, body)
	}
	if code, body := h.get("child", "/api/users/"+pfChild2+"/parental"); code/100 == 2 {
		t.Errorf("GET another child's policy with the old admin token: %d %s", code, body)
	}
	// every library: only the ones the account was given
	if code, body := h.get("child", "/api/libraries"); code != http.StatusOK || strings.Contains(body, pfLibB) || !strings.Contains(body, pfLibA) {
		t.Errorf("libraries with the old admin token: %d %s", code, body)
	}
	if code, body := h.get("child", "/api/videos"); code != http.StatusOK || strings.Contains(body, pfKidB) || !strings.Contains(body, pfKid) {
		t.Errorf("videos with the old admin token: %d %s", code, body)
	}
	// (a video of a library the account was not given answers as it does for any viewer)
	if code, body := h.get("child", "/api/videos/"+pfKidB); code/100 == 2 || strings.Contains(body, "KidB-Title") {
		t.Errorf("a video of a library it was not given, with the old admin token: %d %s", code, body)
	}

	// an admin with no policy keeps its role, also on a device that said 9+ (the device
	// limits what it is shown, not who it is)
	nine := 9
	h.as = func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == pfAdmin {
			q := *p
			q.AgeCeiling = &nine
			return &q
		}
		return p
	}
	if code, body := h.get("admin", "/api/parental/children"); code != http.StatusOK || !strings.Contains(body, pfChild2) {
		t.Errorf("an admin on a 9+ device, the children: %d %s", code, body)
	}
}
