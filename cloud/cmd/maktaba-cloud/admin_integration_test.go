//go:build integration

package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

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

// The service-admin console is enforced server-side: no token is 401,
// a normal cloud account is 403 on every admin route — including one
// whose email is at the old operator domain — and only an account with
// the service_admin role (granted from the CLI) gets through. Denied
// attempts are audited.
func TestAdminRoutesRequireServiceAdmin(t *testing.T) {
	e := newEnv(t)
	victim := e.registerVerified("victim@example.test")
	_ = victim
	victimID := e.userID("victim@example.test")
	user := e.registerVerified("user@example.test")
	domainUser := e.registerVerified("mallory@hamzalabs.com")
	admin := e.serviceAdmin("ops@example.test")
	anon := e.client()

	routes := adminRoutes(t, e.router)
	for _, rt := range routes {
		method, path := rt[0], concrete(rt[1], victimID)
		body := map[string]any{"reason": "role test", "subject_kind": "ip", "subject": "203.0.113.9"}
		if r := anon.do(method, path, body); r.code != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", method, path, r.code)
		}
		for name, c := range map[string]*client{"user": user, "operator-domain user": domainUser} {
			if r := c.do(method, path, body); r.code != http.StatusForbidden || r.errCode() != "admin_access_denied" {
				t.Errorf("%s %s %s = %d %s, want 403 admin_access_denied", name, method, path, r.code, r.body)
			}
		}
	}
	denied := e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.access_denied'`)
	if denied != 2*len(routes) {
		t.Errorf("denied attempts audited %d, want %d", denied, 2*len(routes))
	}
	if victimSusp := e.count(`SELECT count(*) FROM users WHERE id = $1 AND status = 'suspended'`, victimID); victimSusp != 0 {
		t.Fatal("a denied request changed state")
	}

	// The service admin gets through every route (the unblock of a
	// subject that is not listed is a 404, which is still past the gate).
	for _, rt := range routes {
		method, path := rt[0], concrete(rt[1], victimID)
		body := map[string]any{"reason": "role test", "subject_kind": "ip", "subject": "203.0.113.9"}
		r := admin.do(method, path, body)
		if r.code == http.StatusUnauthorized || r.code == http.StatusForbidden || r.code >= 500 {
			t.Errorf("service admin %s %s = %d %s", method, path, r.code, r.body)
		}
	}

	// Revoking the role takes effect on the next request (the role is
	// read from the database, not the token).
	var out bytes.Buffer
	if err := adminRole(context.Background(), stores.NewUsers(e.pool.DB), auditLogFor(e), "revoke", "ops@example.test", &out); err != nil {
		t.Fatal(err)
	}
	if r := admin.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusForbidden {
		t.Fatalf("revoked admin: %d", r.code)
	}
}

// Suspension: reason required, takes effect at once on access tokens,
// refresh and login; audited; reversible; an admin cannot be suspended.
func TestAdminSuspendAndConsole(t *testing.T) {
	e := newEnv(t)
	const email = "target@example.test"
	target := e.registerVerified(email)
	tid := e.userID(email)
	admin := e.serviceAdmin("ops@example.test")
	aid := e.userID("ops@example.test")

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
	if r := admin.do("POST", "/v1/admin/accounts/"+aid+"/suspend", map[string]string{"reason": "oops"}); r.code != 409 || r.errCode() != "cannot_suspend_admin" {
		t.Fatalf("suspend admin: %d %s", r.code, r.body)
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
	if f["users_total"].(float64) != 2 || f["servers_total"].(float64) != 1 {
		t.Fatalf("fleet: %v", f)
	}
	sv := admin.mustStatus(admin.do("GET", "/v1/admin/servers?status=offline", nil), 200).json(t)
	if sv["total"].(float64) != 1 || sv["servers"].([]any)[0].(map[string]any)["owner_email"] != email {
		t.Fatalf("servers: %v", sv)
	}
	e.exec(`UPDATE users SET plan = 'pro' WHERE id = $1`, tid)
	e.exec(`INSERT INTO subscriptions (user_id, plan, status) VALUES ($1, 'pro', 'active')`, tid)
	rev := admin.mustStatus(admin.do("GET", "/v1/admin/revenue", nil), 200).json(t)
	if rev["mrr_cents"].(float64) != 500 || rev["active_paid"].(float64) != 1 {
		t.Fatalf("revenue: %v", rev)
	}

	if r := admin.do("POST", "/v1/admin/abuse/block", map[string]any{"subject_kind": "user", "subject": aid, "reason": "x"}); r.code != 409 {
		t.Fatalf("block admin: %d", r.code)
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

// The role can only be granted from the CLI, to a verified active
// account.
func TestAdminCLIGrant(t *testing.T) {
	e := newEnv(t)
	users := stores.NewUsers(e.pool.DB)
	c := e.client()
	c.mustStatus(c.do("POST", "/v1/auth/register", map[string]string{"email": "new@example.test", "password": goodPassword}), http.StatusAccepted)
	var out bytes.Buffer
	if err := adminRole(context.Background(), users, auditLogFor(e), "grant", "new@example.test", &out); err == nil {
		t.Fatal("granted to an unverified account")
	}
	if err := adminRole(context.Background(), users, auditLogFor(e), "grant", "nobody@example.test", &out); err == nil {
		t.Fatal("granted to a missing account")
	}
	e.serviceAdmin("ops@example.test")
	out.Reset()
	if err := adminRole(context.Background(), users, auditLogFor(e), "list", "", &out); err != nil || !strings.Contains(out.String(), "ops@example.test") {
		t.Fatalf("list: %v %q", err, out.String())
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'role.grant' AND actor_email = 'cli'`) != 1 {
		t.Fatal("grant not audited")
	}
	// No HTTP route can set the role: a PATCH carrying role is ignored.
	u := e.registerVerified("climber@example.test")
	u.do("PATCH", "/v1/account/me", map[string]string{"role": "service_admin"})
	if r := u.do("GET", "/v1/admin/fleet", nil); r.code != 403 {
		t.Fatalf("self-promotion: %d", r.code)
	}
}
