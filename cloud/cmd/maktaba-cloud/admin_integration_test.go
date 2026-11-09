//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// adminRoutes lists every /v1/admin/* route the api role's router
// actually serves, so a route added later is covered without editing
// this test.
func adminRoutes(t *testing.T, r chi.Routes) [][2]string {
	t.Helper()
	var out [][2]string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/v1/admin/") {
			out = append(out, [2]string{method, route})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 15 {
		t.Fatalf("only %d admin routes found: %v", len(out), out)
	}
	return out
}

func concrete(route, id string) string {
	return strings.NewReplacer("{id}", id).Replace(route)
}

// T01 (story 50.33): the console is enforced server-side on every admin route the router
// serves. No session is 401; a customer's token is 401 (it is no staff token: another key,
// another audience); a staff account without service_admin is 403 admin_access_denied; a
// staff token minted without a second factor is 403 mfa_required; a service admin is served,
// except that a sensitive route is 401 reauth_required until a fresh MFA check (T07). Every
// refusal of a presented credential is audited, and every request served is admin.request.
func TestAdminRoutesRequireServiceAdmin(t *testing.T) {
	e := newEnv(t)
	e.registerVerified("victim@example.test")
	victimID := e.userID("victim@example.test")
	customer := e.registerVerified("user@example.test")
	domainUser := e.registerVerified("mallory@hamzalabs.com")
	noRole := e.staff("helper@example.test", "")
	adminAcct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	admin := e.staffSignIn(adminAcct)
	anon := e.client()

	// a token for the admin's own live session, minted without a second factor (a bug or a
	// forged signing path: sign-in can't produce one)
	var sid string
	if err := e.pool.DB.QueryRow(`SELECT id FROM staff_sessions WHERE staff_id = $1 AND revoked_at IS NULL`, adminAcct.id).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	pwdOnly := e.client()
	tok, err := staffauth.NewSigner([]byte(testStaffTokenSecret)).Issue(adminAcct.id, sid, []string{staffauth.AMRPassword}, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pwdOnly.token = tok

	routes := adminRoutes(t, e.admin)
	// the problem reports homes sent (53.16): a home admin's cloud token reads none of them
	if !slices.ContainsFunc(routes, func(rt [2]string) bool { return rt[1] == "/v1/admin/reports" }) {
		t.Fatalf("the walk missed /v1/admin/reports: %v", routes)
	}
	// nor the release symbol tables their crashes are read against (53.14)
	if !slices.ContainsFunc(routes, func(rt [2]string) bool { return rt[1] == "/v1/admin/symbols" }) {
		t.Fatalf("the walk missed /v1/admin/symbols: %v", routes)
	}
	// the upload is the symbols token's, and off with none set up
	if r := anon.do("POST", "/v1/symbols", nil); r.code != http.StatusServiceUnavailable || r.errCode() != "symbols_off" {
		t.Errorf("an upload with no symbols token set up = %d %s", r.code, r.errCode())
	}
	body := map[string]any{"reason": "role test", "subject_kind": "ip", "subject": "203.0.113.9"}
	for _, rt := range routes {
		method, path := rt[0], concrete(rt[1], victimID)
		if r := anon.do(method, path, body); r.code != http.StatusUnauthorized || r.errCode() != "no_session" {
			t.Errorf("anonymous %s %s = %d %s, want 401 no_session", method, path, r.code, r.body)
		}
		for name, c := range map[string]*client{"customer": customer, "operator-domain customer": domainUser} {
			if r := c.do(method, path, body); r.code != http.StatusUnauthorized || r.errCode() != "no_session" {
				t.Errorf("%s %s %s = %d %s, want 401 no_session", name, method, path, r.code, r.body)
			}
		}
		if r := noRole.do(method, path, body); r.code != http.StatusForbidden || r.errCode() != "admin_access_denied" {
			t.Errorf("staff without the role %s %s = %d %s, want 403 admin_access_denied", method, path, r.code, r.body)
		}
		if r := pwdOnly.do(method, path, body); r.code != http.StatusForbidden || r.errCode() != "mfa_required" {
			t.Errorf("a token without MFA %s %s = %d %s, want 403 mfa_required", method, path, r.code, r.body)
		}
	}
	denied := e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.access_denied' AND actor_kind = 'staff'`)
	if want := 4 * len(routes); denied != want {
		t.Errorf("refusals audited %d, want %d (two customers, the staff account without the role, the token without MFA)", denied, want)
	}
	if victimSusp := e.count(`SELECT count(*) FROM users WHERE id = $1 AND status = 'suspended'`, victimID); victimSusp != 0 {
		t.Fatal("a denied request changed state")
	}

	// T07: a sensitive route needs an MFA check in the last 5 minutes. The admin's last one is
	// put 6 minutes back: every sensitive route is 401 reauth_required, the others served.
	e.exec(`UPDATE staff_sessions SET auth_time = now() - interval '6 minutes' WHERE id = $1`, sid)
	served := func(r resp) bool {
		// the cloud's log routes answer 503 logs_unavailable with no index on W (53.17): past
		// the gate, and saying why; the unblock of a subject not listed is a 404, past it too
		if r.code == http.StatusServiceUnavailable && r.errCode() == "logs_unavailable" {
			return true
		}
		return r.code != http.StatusUnauthorized && r.code != http.StatusForbidden && r.code < 500
	}
	sensitive := 0
	for _, rt := range routes {
		method, path := rt[0], concrete(rt[1], victimID)
		r := admin.do(method, path, body)
		if staffauth.IsSensitive(method, rt[1]) {
			sensitive++
			if r.code != http.StatusUnauthorized || r.errCode() != "reauth_required" {
				t.Errorf("sensitive %s %s with a 6-minute-old check = %d %s, want 401 reauth_required", method, path, r.code, r.body)
			}
			continue
		}
		if !served(r) {
			t.Errorf("service admin %s %s = %d %s", method, path, r.code, r.body)
		}
	}
	if sensitive != len(staffauth.Sensitive) {
		t.Errorf("the router serves %d of the %d sensitive routes", sensitive, len(staffauth.Sensitive))
	}
	// a fresh TOTP check opens them, and is audited
	re := admin.mustStatus(admin.do("POST", "/v1/staff/reauth/totp", map[string]string{"code": e.totp(adminAcct)}), http.StatusOK).json(t)
	admin.token, _ = re["access_token"].(string)
	for _, rt := range routes {
		if !staffauth.IsSensitive(rt[0], rt[1]) {
			continue
		}
		if r := admin.do(rt[0], concrete(rt[1], victimID), body); !served(r) {
			t.Errorf("sensitive %s %s after the re-check = %d %s", rt[0], rt[1], r.code, r.body)
		}
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.reauth' AND actor_id = $1`, adminAcct.id) != 1 {
		t.Error("the re-check is not audited")
	}
	// every request served is an admin.request row naming the account, the route and the status
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.request' AND actor_kind = 'staff'
		AND actor_id = $1 AND detail ? 'route' AND detail ? 'status'`, adminAcct.id); n < len(routes) {
		t.Errorf("admin.request rows %d, want at least %d", n, len(routes))
	}

	// Revoking the role takes effect at once (the role is read from the database, not the
	// token), and ends the account's sessions (§4).
	var out bytes.Buffer
	if err := e.staffCLI(&out).run(context.Background(), "revoke", "ops@example.test", stores.StaffRoleServiceAdmin); err != nil {
		t.Fatal(err)
	}
	if r := admin.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked admin: %d %s", r.code, r.body)
	}
}

// Suspension: reason required, takes effect at once on access tokens,
// refresh and login; audited; reversible; an admin cannot be suspended.
func TestAdminSuspendAndConsole(t *testing.T) {
	e := newEnv(t)
	const email = "target@example.test"
	target := e.registerVerified(email)
	tid := e.userID(email)
	adminAcct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	admin := e.staffSignIn(adminAcct)
	aid := adminAcct.id

	list := admin.mustStatus(admin.do("GET", "/v1/admin/accounts?q=TARGET@", nil), 200).json(t)
	if list["total"].(float64) != 1 || list["accounts"].([]any)[0].(map[string]any)["id"] != tid {
		t.Fatalf("search: %v", list)
	}
	if r := admin.do("GET", "/v1/admin/accounts?q=%25", nil); r.json(t)["total"].(float64) != 0 {
		t.Fatalf("LIKE wildcard not escaped: %s", r.body)
	}
	if r := admin.do("GET", "/v1/admin/accounts/not-a-uuid", nil); r.code != 404 {
		t.Fatalf("bad id: %d", r.code)
	}

	if r := admin.do("POST", "/v1/admin/accounts/"+tid+"/suspend", map[string]string{"reason": "  "}); r.code != 400 || r.errCode() != "reason_required" {
		t.Fatalf("no reason: %d %s", r.code, r.body)
	}
	// a staff account is no cloud account: there is none to suspend by its id
	if r := admin.do("POST", "/v1/admin/accounts/"+aid+"/suspend", map[string]string{"reason": "oops"}); r.code != 404 {
		t.Fatalf("suspend a staff id: %d %s", r.code, r.body)
	}
	admin.mustStatus(admin.do("POST", "/v1/admin/accounts/"+tid+"/suspend", map[string]string{"reason": "abuse report #12"}), http.StatusNoContent)

	if r := target.do("GET", "/v1/account/me", nil); r.code != 403 || r.errCode() != "account_suspended" {
		t.Fatalf("suspended access token: %d %s", r.code, r.body)
	}
	if r := target.do("POST", "/v1/auth/refresh", nil); r.code != 401 {
		t.Fatalf("suspended refresh: %d", r.code)
	}
	if r := e.client().do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword}); r.code != 403 || r.errCode() != "account_suspended" {
		t.Fatalf("suspended login: %d %s", r.code, r.body)
	}

	detail := admin.mustStatus(admin.do("GET", "/v1/admin/accounts/"+tid, nil), 200).json(t)
	acct := detail["account"].(map[string]any)
	if acct["status"] != "suspended" || acct["suspended_reason"] != "abuse report #12" {
		t.Fatalf("detail: %v", acct)
	}
	audits := detail["audit"].([]any)
	if len(audits) == 0 || audits[0].(map[string]any)["action"] != "admin.account.suspend" ||
		audits[0].(map[string]any)["actor_email"] != "ops@example.test" {
		t.Fatalf("audit in detail: %v", audits)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.account.view' AND target_id = $1`, tid) != 1 {
		t.Fatal("detail view not audited")
	}

	admin.mustStatus(admin.do("POST", "/v1/admin/accounts/"+tid+"/unsuspend", map[string]string{"reason": "appeal accepted"}), http.StatusNoContent)
	target.login(email, goodPassword)

	// Fleet, servers, revenue, abuse, audit read correctly.
	e.exec(`INSERT INTO servers (owner_user_id, name, slug, server_secret_hash) VALUES ($1,'Box','tgt-box','x')`, tid)
	f := admin.mustStatus(admin.do("GET", "/v1/admin/fleet", nil), 200).json(t)
	if f["users_total"].(float64) != 1 || f["servers_total"].(float64) != 1 {
		t.Fatalf("fleet: %v", f)
	}
	sv := admin.mustStatus(admin.do("GET", "/v1/admin/servers?status=offline", nil), 200).json(t)
	if sv["total"].(float64) != 1 || sv["servers"].([]any)[0].(map[string]any)["owner_email"] != email {
		t.Fatalf("servers: %v", sv)
	}
	withPrices(t)
	e.exec(`UPDATE users SET plan = 'basic' WHERE id = $1`, tid)
	e.exec(`INSERT INTO subscriptions (user_id, plan, status) VALUES ($1, 'basic', 'active')`, tid)
	rev := admin.mustStatus(admin.do("GET", "/v1/admin/revenue", nil), 200).json(t)
	if rev["mrr_cents"].(float64) != 500 || rev["active_paid"].(float64) != 1 {
		t.Fatalf("revenue: %v", rev)
	}

	admin.mustStatus(admin.do("POST", "/v1/admin/abuse/block", map[string]any{"subject_kind": "user", "subject": tid, "reason": "spam", "expires_in_hours": 2}), http.StatusNoContent)
	if r := e.client().do("POST", "/v1/auth/login", map[string]string{"email": email, "password": goodPassword}); r.code != 403 || r.errCode() != "blocked" {
		t.Fatalf("blocked user login: %d %s", r.code, r.body)
	}
	ab := admin.mustStatus(admin.do("GET", "/v1/admin/abuse", nil), 200).json(t)
	if len(ab["blocklist"].([]any)) != 1 {
		t.Fatalf("blocklist: %v", ab)
	}
	admin.mustStatus(admin.do("POST", "/v1/admin/abuse/unblock", map[string]any{"subject_kind": "user", "subject": tid, "reason": "cleared"}), http.StatusNoContent)
	if r := admin.do("POST", "/v1/admin/abuse/unblock", map[string]any{"subject_kind": "user", "subject": tid, "reason": "again"}); r.code != 404 {
		t.Fatalf("unblock twice: %d", r.code)
	}

	log := admin.mustStatus(admin.do("GET", "/v1/admin/audit?action=admin.account.&actor="+aid, nil), 200).json(t)
	if log["total"].(float64) < 3 {
		t.Fatalf("audit filter: %v", log)
	}
	if r := admin.do("GET", "/v1/admin/audit?limit=500", nil); len(r.json(t)["entries"].([]any)) > 50 {
		t.Fatal("audit page larger than 50")
	}
}

// T08 (story 50.33 §5): every state-changing admin route the router serves is either in
// staffauth.Sensitive (it needs a fresh MFA check) or in staffauth.NotSensitive with the
// reason it doesn't. A new mutating route that is in neither fails here, by name.
func TestEveryMutatingAdminRouteIsClassified(t *testing.T) {
	e := newEnv(t)
	for _, rt := range adminRoutes(t, e.admin) {
		method, route := rt[0], rt[1]
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
			continue
		}
		key := method + " " + route
		if !staffauth.Sensitive[key] && staffauth.NotSensitive[key] == "" {
			t.Errorf("%s changes state but is neither in staffauth.Sensitive nor in NotSensitive with a reason", key)
		}
	}
	for key := range staffauth.Sensitive {
		method, route, _ := strings.Cut(key, " ")
		if !slices.Contains(adminRoutes(t, e.admin), [2]string{method, route}) {
			t.Errorf("staffauth.Sensitive names %s, which the router does not serve", key)
		}
	}
}
