package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"

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

// The admin.request row's status is what the handler answered: its first WriteHeader (a later
// one is ignored), 200 for a body written without one or for nothing written; and the
// wrapper keeps the handler's Flusher (a streamed answer still flushes).
func TestServedStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		h    http.HandlerFunc
		want int
	}{
		"header":    {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }, 404},
		"body only": {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) }, 200},
		"nothing":   {func(http.ResponseWriter, *http.Request) {}, 200},
		"second write": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
			w.WriteHeader(http.StatusOK)
		}, 409},
		"flushed": {func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("data: 1\n\n"))
			f.Flush()
		}, 200},
	} {
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/x", nil)
		rec := httptest.NewRecorder()
		ww := chimw.NewWrapResponseWriter(rec, req.ProtoMajor)
		tc.h(ww, req)
		if got := servedStatus(ww); got != tc.want {
			t.Errorf("%s: status %d, want %d", name, got, tc.want)
		}
	}
}
