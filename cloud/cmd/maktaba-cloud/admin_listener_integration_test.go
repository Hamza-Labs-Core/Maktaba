//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Epic 50 story 50.32 §1: the operator console's routes exist only on the admin listener,
// which answers only admin.maktaba.tv; on the user listener every one of them is the same
// 404 as a route that doesn't exist, whoever asks.

// at sends method path straight to listener h with Host host, as c (its token, its address).
func (c *client) at(h http.Handler, host, method, path string, body any, header map[string]string) (int, string) {
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = host
	req.RemoteAddr = c.ip + ":40000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// T01: every route of the admin router, on the user router, as no one, as a customer and as
// staff: 404, with the body of an unknown route.
func TestAdminRoutesAre404OnTheUserListener(t *testing.T) {
	e := newEnv(t)
	victimID := func() string { e.registerVerified("victim@example.test"); return e.userID("victim@example.test") }()
	who := map[string]*client{
		"no one":   e.client(),
		"customer": e.registerVerified("user@example.test"),
		"staff":    e.serviceAdmin("ops@example.test"),
	}
	_, unknown := who["no one"].at(e.router, "api.maktaba.tv", http.MethodGet, "/v1/no-such-route", nil, nil)
	routes := adminRoutes(t, e.admin)
	body := map[string]any{"reason": "role test", "subject_kind": "ip", "subject": "203.0.113.9"}
	for _, rt := range routes {
		method, path := rt[0], concrete(rt[1], victimID)
		for _, host := range []string{"api.maktaba.tv", "app.maktaba.tv"} {
			for name, c := range who {
				code, got := c.at(e.router, host, method, path, body, map[string]string{"Origin": "https://" + testAdminHost, "X-Maktaba-Staff": "1"})
				if code != http.StatusNotFound || got != unknown {
					t.Errorf("%s %s %s on %s: %d %q, want 404 %q", name, method, path, host, code, got, unknown)
				}
			}
		}
	}
	// the staff account still reaches them on its own listener
	if code, got := who["staff"].at(e.admin, testAdminHost, http.MethodGet, "/v1/admin/accounts", nil, nil); code != http.StatusOK {
		t.Fatalf("staff GET /v1/admin/accounts on the admin listener: %d %s", code, got)
	}
}

// T02: the admin listener answers its one name; any other Host is 404, staff or not.
func TestTheAdminListenerAnswersOnlyItsName(t *testing.T) {
	e := newEnv(t)
	staff := e.serviceAdmin("ops@example.test")
	for _, host := range []string{"api.maktaba.tv", "app.maktaba.tv", "admin.example.test.evil.test", "evil.test"} {
		if code, got := staff.at(e.admin, host, http.MethodGet, "/v1/admin/accounts", nil, nil); code != http.StatusNotFound {
			t.Errorf("Host %s: %d %s, want 404", host, code, got)
		}
	}
	if code, _ := staff.at(e.admin, testAdminHost+":8091", http.MethodGet, "/v1/admin/accounts", nil, nil); code != http.StatusOK {
		t.Errorf("Host %s:8091 (a port): %d, want 200", testAdminHost, code)
	}
	// the node's own probes answer on any Host
	if code, _ := staff.at(e.admin, "127.0.0.1:8091", http.MethodGet, "/healthz", nil, nil); code != http.StatusOK {
		t.Errorf("/healthz on the loopback address: %d, want 200", code)
	}
	if !strings.Contains(e.logs.String(), "wrong_host") {
		t.Error("a request for another name was not logged")
	}
}

// T03: the user router has no admin or staff route at all, and the admin router nothing
// but the console's (its /v1/staff/* sign-in included) and the probes.
func TestTheRoutersDoNotShareRoutes(t *testing.T) {
	e := newEnv(t)
	_ = chi.Walk(e.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if server.IsAdminPath(route) {
			t.Errorf("the user router serves %s %s", method, route)
		}
		return nil
	})
	_ = chi.Walk(e.admin, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !server.IsAdminPath(route) && route != "/healthz" && route != "/readyz" {
			t.Errorf("the admin router serves %s %s", method, route)
		}
		return nil
	})
}

// T05 through the role: a state-changing console request without the console's Origin and
// X-Maktaba-Staff is refused before its handler (nothing is blocked), and with them it runs.
func TestAConsoleChangeNeedsTheConsolesOrigin(t *testing.T) {
	e := newEnv(t)
	staff := e.serviceAdmin("ops@example.test")
	body := map[string]any{"reason": "csrf test", "subject_kind": "ip", "subject": "203.0.113.77"}
	for name, h := range map[string]map[string]string{
		"no Origin":          {"X-Maktaba-Staff": "1"},
		"another Origin":     {"Origin": "https://app.maktaba.tv", "X-Maktaba-Staff": "1"},
		"no X-Maktaba-Staff": {"Origin": "https://" + testAdminHost},
	} {
		code, got := staff.at(e.admin, testAdminHost, http.MethodPost, "/v1/admin/abuse/block", body, h)
		if code != http.StatusForbidden || !strings.Contains(got, `"bad_origin"`) {
			t.Errorf("%s: %d %s, want 403 bad_origin", name, code, got)
		}
	}
	if n := e.count(`SELECT count(*) FROM blocklist WHERE subject = '203.0.113.77'`); n != 0 {
		t.Fatalf("a refused request blocked the address (%d rows)", n)
	}
	code, got := staff.at(e.admin, testAdminHost, http.MethodPost, "/v1/admin/abuse/block", body,
		map[string]string{"Origin": "https://" + testAdminHost, "X-Maktaba-Staff": "1"})
	if code >= 300 {
		t.Fatalf("from the console: %d %s", code, got)
	}
	// a preflight is refused: the console is same-origin only
	if code, _ := staff.at(e.admin, testAdminHost, http.MethodOptions, "/v1/admin/abuse/block", nil,
		map[string]string{"Origin": "https://evil.test", "Access-Control-Request-Method": "POST"}); code != http.StatusForbidden {
		t.Errorf("preflight on the admin listener: %d, want 403", code)
	}
}

// The user listener with server.user_hosts set answers those names only.
func TestTheUserListenerAnswersItsNames(t *testing.T) {
	e := newEnvCfg(t, nil, func(c *config.Config) { c.Server.UserHosts = []string{"api.maktaba.tv", "app.maktaba.tv"} })
	c := e.client()
	if code, _ := c.at(e.router, "api.maktaba.tv", http.MethodGet, "/v1/billing/plans", nil, nil); code == http.StatusNotFound {
		t.Error("api.maktaba.tv was refused")
	}
	if code, _ := c.at(e.router, testAdminHost, http.MethodGet, "/v1/billing/plans", nil, nil); code != http.StatusNotFound {
		t.Errorf("the console's name on the user listener: %d, want 404", code)
	}
	if code, _ := c.at(e.router, "127.0.0.1:8090", http.MethodGet, "/readyz", nil, nil); code != http.StatusOK {
		t.Errorf("/readyz on the loopback address: %d, want 200", code)
	}
}

// The console signs in on its own host with the staff routes (story 50.33): the sign-in
// needs the console's Origin like any other change, and the session cookie is the host-only
// __Host-maktaba_staff one, Strict, so it is never sent to a user name. The customer sign-in
// is not on the console's listener at all.
func TestTheConsoleSignsInOnItsOwnHost(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolStaff("ops@example.test", stores.StaffRoleServiceAdmin)
	anon := e.client()
	login := map[string]any{"email": acct.email, "password": goodStaffPassword}
	if code, got := anon.at(e.admin, testAdminHost, http.MethodPost, "/v1/staff/login", login, nil); code != http.StatusForbidden || !strings.Contains(got, "bad_origin") {
		t.Fatalf("login without the console's Origin: %d %s, want 403 bad_origin", code, got)
	}
	for _, p := range []string{"/v1/auth/login", "/v1/auth/refresh", "/v1/account/me"} {
		if code, _ := anon.at(e.admin, testAdminHost, http.MethodPost, p, login, map[string]string{"Origin": "https://" + testAdminHost, "X-Maktaba-Staff": "1"}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s on the console's listener: %d, want 404", p, code)
		}
	}
	post := func(path string, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Host = testAdminHost
		req.RemoteAddr = "198.51.100.20:40000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://"+testAdminHost)
		req.Header.Set("X-Maktaba-Staff", "1")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		e.admin.ServeHTTP(rec, req)
		return rec
	}
	rec := post("/v1/staff/login", `{"email":"ops@example.test","password":"`+goodStaffPassword+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login from the console: %d %s", rec.Code, rec.Body.String())
	}
	var step1 struct {
		MFAToken string `json:"mfa_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &step1)
	rec = post("/v1/staff/mfa/totp", `{"mfa_token":"`+step1.MFAToken+`","code":"`+e.totp(acct)+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the second factor from the console: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "__Host-maktaba_staff" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Domain != "" || cookie.Path != "/" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("the console's session cookie: %+v", cookie)
	}
	staff := &client{e: e, ip: "198.51.100.20", token: res.AccessToken}
	if code, got := staff.at(e.admin, testAdminHost, http.MethodGet, "/v1/staff/me", nil, nil); code != http.StatusOK || !strings.Contains(got, "ops@example.test") {
		t.Fatalf("GET /v1/staff/me on the console: %d %s", code, got)
	}
	if rec := post("/v1/staff/refresh", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("refresh from the console: %d %s", rec.Code, rec.Body.String())
	}
}
