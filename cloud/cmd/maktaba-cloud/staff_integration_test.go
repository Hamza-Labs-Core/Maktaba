//go:build integration

package main

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// login is step 1 of a staff sign-in from a new client (each its own IP: the per-IP limit
// is burst 5).
func (e *env) staffLogin(email, password string) (*client, resp) {
	c := e.client()
	return c, c.do("POST", "/v1/staff/login", map[string]string{"email": email, "password": password})
}

// T03 (story 50.33): sign-in's answers. The right password with no factor is 403
// mfa_required and no session; a wrong password, an unknown email and a disabled account are
// the same 401 invalid_credentials, the unknown email after the same argon2id work.
func TestStaffSignIn(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)

	_, r := e.staffLogin(acct.email, goodStaffPassword)
	if r.code != http.StatusOK || r.json(t)["mfa_token"] == "" {
		t.Fatalf("step 1: %d %s", r.code, r.body)
	}
	if m, _ := r.json(t)["methods"].([]any); len(m) != 2 || m[0] != "totp" || m[1] != "recovery" {
		t.Fatalf("methods: %v", r.json(t)["methods"])
	}
	// the mfa_token is no access token
	c := e.client()
	c.token, _ = r.json(t)["mfa_token"].(string)
	if r := c.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("an mfa_token on an admin route: %d", r.code)
	}

	timed := func(email, pw string) time.Duration {
		var ds []time.Duration
		for range 5 {
			start := time.Now()
			_, r := e.staffLogin(email, pw)
			ds = append(ds, time.Since(start))
			if r.code != http.StatusUnauthorized || r.errCode() != "invalid_credentials" {
				t.Fatalf("%s: %d %s", email, r.code, r.body)
			}
		}
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[2]
	}
	// the unknown email's median is the wrong password's, within CI's noise (the fifth wrong
	// password locks the account, which the test then clears)
	wrong := timed(acct.email, "not-the-password-at-all")
	e.exec(`UPDATE staff_accounts SET failed_at = '{}', locked_until = NULL WHERE id = $1`, acct.id)
	unknown := timed("nobody@example.test", goodStaffPassword)
	if unknown < wrong/2 || unknown > wrong*2 {
		t.Errorf("unknown email %v, wrong password %v: the miss is told apart by its time", unknown, wrong)
	}
	e.exec(`UPDATE staff_accounts SET failed_at = '{}' WHERE id = $1`, acct.id)

	var out bytes.Buffer
	cli := e.staffCLI(&out)
	if err := cli.run(context.Background(), "reset-mfa", acct.email, ""); err != nil {
		t.Fatal(err)
	}
	if _, r := e.staffLogin(acct.email, goodStaffPassword); r.code != http.StatusForbidden || r.errCode() != "mfa_required" {
		t.Fatalf("no factor: %d %s", r.code, r.body)
	}
	if n := e.count(`SELECT count(*) FROM staff_mfa_tokens WHERE staff_id = $1`, acct.id); n != 0 {
		t.Fatalf("no factor still started a sign-in: %d mfa tokens", n)
	}
	if err := cli.run(context.Background(), "disable", acct.email, ""); err != nil {
		t.Fatal(err)
	}
	if _, r := e.staffLogin(acct.email, goodStaffPassword); r.code != http.StatusUnauthorized || r.errCode() != "invalid_credentials" {
		t.Fatalf("disabled: %d %s", r.code, r.body)
	}
}

// T09 and T11: 5 failed steps in 15 minutes lock the account for 30 (423, Retry-After), with
// an audit row and the owner's alert; the per-IP limit is burst 5. Every step is an audit
// row with actor_kind staff.
func TestStaffLockoutAndAudit(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	wrongCodes := func(c *client, mfa string, n int) {
		for range n {
			if r := c.do("POST", "/v1/staff/mfa/totp", map[string]string{"mfa_token": mfa, "code": "000000"}); r.code != http.StatusUnauthorized || r.errCode() != "mfa_failed" {
				t.Fatalf("a wrong code: %d %s", r.code, r.body)
			}
		}
	}
	c, r := e.staffLogin(acct.email, goodStaffPassword)
	wrongCodes(c, r.json(t)["mfa_token"].(string), 4)
	c, r = e.staffLogin(acct.email, goodStaffPassword)
	wrongCodes(c, r.json(t)["mfa_token"].(string), 1) // the fifth failure locks
	_, r = e.staffLogin(acct.email, goodStaffPassword)
	if r.code != http.StatusLocked || r.errCode() != "account_locked" || r.header.Get("Retry-After") == "" {
		t.Fatalf("the sixth step: %d %s %v", r.code, r.body, r.header)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.signin.locked' AND actor_id = $1`, acct.id) != 1 {
		t.Error("the lockout is not audited")
	}
	if !strings.Contains(e.logs.String(), "code="+"staff_lockout") {
		t.Error("the lockout alerted no one (no staff_lockout error line)")
	}
	var out bytes.Buffer
	if err := e.staffCLI(&out).run(context.Background(), "unlock", acct.email, ""); err != nil {
		t.Fatal(err)
	}
	if _, r := e.staffLogin(acct.email, "not-the-password-at-all"); r.code != http.StatusUnauthorized {
		t.Fatalf("a wrong password after the unlock: %d", r.code)
	}
	admin := e.staffSignIn(acct)
	admin.mustStatus(admin.do("GET", "/v1/admin/fleet", nil), http.StatusOK)

	// a mutation and a denial, then every row's kind
	admin.mustStatus(admin.do("POST", "/v1/admin/abuse/block", map[string]any{"subject_kind": "ip", "subject": "203.0.113.7", "reason": "test"}), http.StatusNoContent)
	for _, action := range []string{"staff.signin.password_ok", "staff.signin.password_failed", "staff.signin.mfa_ok",
		"staff.signin.mfa_failed", "staff.signin.unlocked", "admin.request", "admin.abuse.block"} {
		if e.count(`SELECT count(*) FROM audit_log WHERE action = $1`, action) == 0 {
			t.Errorf("no %s row", action)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE (action LIKE 'staff.signin.%' OR action LIKE 'admin.%') AND actor_kind = 'user'`); n != 0 {
		t.Errorf("%d staff rows written as a customer's", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.signin.unlocked' AND actor_kind = 'cli'`); n != 1 {
		t.Errorf("the CLI's unlock: %d rows with actor_kind cli", n)
	}

	// the per-IP limit: a sixth sign-in from one address in a burst is 429
	ip := e.client()
	for i := range 6 {
		r := ip.do("POST", "/v1/staff/login", map[string]string{"email": "nobody@example.test", "password": "x"})
		if i < 5 && r.code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d limited", i+1)
		}
		if i == 5 && r.code != http.StatusTooManyRequests {
			t.Fatalf("the sixth attempt: %d", r.code)
		}
	}
}

// T06 and §4: the session's clock. 14 minutes idle is still a session; a refresh after 16
// is refused (idle) and ends it; 8 hours from the sign-in end it whatever the activity
// (capped). A refresh rotates the cookie; the old cookie presented again past the grace ends
// every session of the account.
func TestStaffSessions(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	sid := func(c *client) string {
		var id string
		if err := e.pool.DB.QueryRow(`SELECT id FROM staff_sessions WHERE token_hash = $1`,
			staffauth.TokenHash(c.cookies["__Host-maktaba_staff"])).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	a := e.staffSignIn(acct)
	e.exec(`UPDATE staff_sessions SET last_active_at = now() - interval '14 minutes' WHERE id = $1`, sid(a))
	a.mustStatus(a.do("GET", "/v1/admin/fleet", nil), http.StatusOK)
	old := a.cookies["__Host-maktaba_staff"]
	r := a.mustStatus(a.do("POST", "/v1/staff/refresh", nil), http.StatusOK)
	if a.cookies["__Host-maktaba_staff"] == old {
		t.Fatal("the refresh did not rotate the cookie")
	}
	a.token, _ = r.json(t)["access_token"].(string)
	me := a.mustStatus(a.do("GET", "/v1/staff/me", nil), http.StatusOK).json(t)
	if me["staff"].(map[string]any)["email"] != acct.email {
		t.Fatalf("me: %v", me)
	}
	if s := a.mustStatus(a.do("GET", "/v1/staff/sessions", nil), http.StatusOK).json(t)["sessions"].([]any); len(s) != 1 || s[0].(map[string]any)["current"] != true {
		t.Fatalf("sessions: %v", s)
	}

	e.exec(`UPDATE staff_sessions SET last_active_at = now() - interval '16 minutes' WHERE id = $1`, sid(a))
	r = a.do("POST", "/v1/staff/refresh", nil)
	if r.code != http.StatusUnauthorized || r.json(t)["reason"] != "idle" {
		t.Fatalf("16 minutes idle: %d %s", r.code, r.body)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.session.idle_ended'`) != 1 {
		t.Error("the idle end is not audited")
	}

	b := e.staffSignIn(acct)
	e.exec(`UPDATE staff_sessions SET signed_in_at = now() - interval '8 hours 1 minute' WHERE id = $1`, sid(b))
	if r := b.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized || r.json(t)["reason"] != "capped" {
		t.Fatalf("8 h 1 min with activity: %d %s", r.code, r.body)
	}

	// a replay: c refreshes, and its old cookie comes back past the grace
	c := e.staffSignIn(acct)
	d := e.staffSignIn(acct)
	stale := c.cookies["__Host-maktaba_staff"]
	c.mustStatus(c.do("POST", "/v1/staff/refresh", nil), http.StatusOK)
	e.exec(`UPDATE staff_sessions SET rotated_at = now() - interval '1 minute' WHERE id = $1`, sid(c))
	thief := e.client()
	thief.cookies["__Host-maktaba_staff"] = stale
	if r := thief.do("POST", "/v1/staff/refresh", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("a replayed cookie: %d", r.code)
	}
	if r := d.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("after a replay another session still works: %d", r.code)
	}

	// sign out everywhere
	f := e.staffSignIn(acct)
	g := e.staffSignIn(acct)
	f.mustStatus(f.do("POST", "/v1/staff/sessions/revoke-all", nil), http.StatusNoContent)
	if r := g.do("GET", "/v1/staff/me", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("after sign out everywhere: %d", r.code)
	}
	h := e.staffSignIn(acct)
	h.mustStatus(h.do("POST", "/v1/staff/logout", nil), http.StatusNoContent)
	if r := h.do("POST", "/v1/staff/refresh", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("refresh after sign out: %d", r.code)
	}
}

// T10: admin.ip_allowlist. Set, a client inside it signs in and is served; one outside is
// 403 ip_not_allowed on every admin-listener route, and audited.
func TestStaffIPAllowList(t *testing.T) {
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Admin.IPAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	})
	admin := e.serviceAdmin("ops@example.test") // the test clients are 198.51.100.x
	admin.mustStatus(admin.do("GET", "/v1/admin/fleet", nil), http.StatusOK)
	out := e.client()
	out.ip = "203.0.113.9"
	out.token = admin.token
	for _, p := range []string{"/v1/admin/fleet", "/v1/staff/login", "/v1/staff/me"} {
		method := "GET"
		if p == "/v1/staff/login" {
			method = "POST"
		}
		if r := out.do(method, p, map[string]string{}); r.code != http.StatusForbidden || r.errCode() != "ip_not_allowed" {
			t.Errorf("%s from outside: %d %s", p, r.code, r.body)
		}
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.signin.ip_denied' AND ip = '203.0.113.9'`); n != 3 {
		t.Errorf("ip denials audited %d, want 3", n)
	}
}

// T12: the CLI. add prints a link; grant and revoke change the roles and end the sessions;
// disable ends them at once; reset-mfa removes every factor and prints a new link; unlock
// clears a lockout; each is an audit row with actor cli. The old admin command points here,
// and no HTTP route can make anyone staff.
func TestStaffCLI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var out bytes.Buffer
	cli := e.staffCLI(&out)
	if err := cli.run(ctx, "add", "x@example.test", "moderator"); err == nil {
		t.Fatal("a role that does not exist was accepted")
	}
	acct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	if err := cli.run(ctx, "add", "ops@example.test", stores.StaffRoleServiceAdmin); err == nil {
		t.Fatal("added twice")
	}
	ended := func(what string, c *client) {
		t.Helper()
		if r := c.do("GET", "/v1/staff/me", nil); r.code != http.StatusUnauthorized {
			t.Fatalf("after %s the session lives: %d", what, r.code)
		}
	}
	for _, step := range []struct{ sub, role string }{
		{"revoke", stores.StaffRoleServiceAdmin}, {"grant", stores.StaffRoleServiceAdmin}, {"disable", ""},
	} {
		c := e.staffSignIn(acct)
		out.Reset()
		if err := cli.run(ctx, step.sub, acct.email, step.role); err != nil {
			t.Fatalf("%s: %v", step.sub, err)
		}
		ended(step.sub, c)
	}
	if err := cli.run(ctx, "enable", acct.email, ""); err != nil {
		t.Fatal(err)
	}
	c := e.staffSignIn(acct)
	out.Reset()
	if err := cli.run(ctx, "reset-mfa", acct.email, ""); err != nil {
		t.Fatal(err)
	}
	ended("reset-mfa", c)
	if !strings.Contains(out.String(), "https://"+testAdminHost+"/enroll#") {
		t.Fatalf("reset-mfa printed no link: %q", out.String())
	}
	if e.count(`SELECT count(*) FROM staff_totp WHERE staff_id = $1`, acct.id) != 0 {
		t.Fatal("reset-mfa left the TOTP factor")
	}
	out.Reset()
	if err := cli.run(ctx, "list", "", ""); err != nil || !strings.Contains(out.String(), "ops@example.test\tservice_admin\tactive\tnot enrolled") {
		t.Fatalf("list: %v %q", err, out.String())
	}
	for _, action := range []string{"staff.added", "staff.role.revoke", "staff.role.grant", "staff.disabled", "staff.enabled", "staff.reset_mfa"} {
		if e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND actor_kind = 'cli' AND target_id = $2`, action, acct.id) == 0 {
			t.Errorf("no %s row with actor cli", action)
		}
	}
	var p bytes.Buffer
	printAdminPointer(&p)
	if !strings.Contains(p.String(), "maktaba-cloud staff add") {
		t.Fatalf("the admin command's pointer: %q", p.String())
	}

	// No HTTP route makes anyone an operator: a customer's PATCH carrying a role is ignored,
	// and a customer's token is no staff token.
	u := e.registerVerified("climber@example.test")
	u.do("PATCH", "/v1/account/me", map[string]string{"role": "service_admin"})
	if r := u.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("self-promotion: %d", r.code)
	}
}

// T13: the migration resets an old users.role = 'service_admin' to 'user', and that account
// can't reach the admin router.
func TestUsersRoleReset(t *testing.T) {
	e := newEnv(t)
	c := e.registerVerified("old-admin@example.test")
	e.exec(`UPDATE users SET role = 'service_admin' WHERE email = 'old-admin@example.test'`)
	sql, err := fs.ReadFile(migrations.FS, "00500012_users_role_reset.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(strings.SplitN(string(sql), "-- +goose Up", 2)[1], "-- +goose Down")
	e.exec(up)
	if n := e.count(`SELECT count(*) FROM users WHERE role <> 'user'`); n != 0 {
		t.Fatalf("%d accounts kept a role", n)
	}
	if r := c.do("GET", "/v1/admin/fleet", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("the old admin on the admin router: %d", r.code)
	}
}
