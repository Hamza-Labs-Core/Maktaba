package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// T10 (story 50.33 §7): admin.ip_allowlist. Empty, every client is served; set, a client
// inside a prefix is served, one outside is 403 ip_not_allowed; the probes always answer.
func TestIPAllowList(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func(allow []netip.Prefix, ip, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		cmw.WithClientIP(nil)(IPAllowList(allow, nil, slog.Default())(ok)).ServeHTTP(rec, req)
		return rec
	}
	if rec := call(nil, "203.0.113.9:1", "/v1/admin/fleet"); rec.Code != http.StatusOK {
		t.Fatalf("empty list: %d", rec.Code)
	}
	allow := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	for _, ip := range []string{"198.51.100.7:1", "[::ffff:198.51.100.7]:1", "[2001:db8::1]:1"} {
		if rec := call(allow, ip, "/v1/admin/fleet"); rec.Code != http.StatusOK {
			t.Errorf("inside (%s): %d", ip, rec.Code)
		}
	}
	for _, ip := range []string{"203.0.113.9:1", "", "not-an-ip"} {
		rec := call(allow, ip, "/v1/staff/login")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"ip_not_allowed"`) {
			t.Errorf("outside (%q): %d %s", ip, rec.Code, rec.Body.String())
		}
	}
	if rec := call(allow, "203.0.113.9:1", "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("the probe from outside: %d", rec.Code)
	}
}

// An operator is only what StaffGate put in the context.
func TestServiceAdminContext(t *testing.T) {
	if _, ok := ServiceAdmin(context.Background()); ok {
		t.Fatal("an operator in an empty context")
	}
	o, ok := ServiceAdmin(WithOperator(context.Background(), Operator{ID: "s1", Email: "ops@example.test", SessionID: "x"}))
	if !ok || o.ID != "s1" || o.SessionID != "x" {
		t.Fatalf("operator: %+v %v", o, ok)
	}
}
