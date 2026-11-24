package broadcastgate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testID = "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60"

type fakePublishers struct {
	test bool
	err  error
}

func (f fakePublishers) IsTest(context.Context, string) (bool, error)   { return f.test, f.err }
func (f fakePublishers) OwnsTest(context.Context, string) (bool, error) { return f.test, f.err }

type fakeAgents struct {
	err   error
	asked *int
}

func (f fakeAgents) VerifySecret(context.Context, string, string) error { *f.asked++; return f.err }

func ask(g Gate, method, path string) int {
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	req := httptest.NewRequest(method, path, nil)
	req.SetBasicAuth(testID, "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// The gate fails closed: an error, a missing dependency or a wrong secret is an unknown path; a
// home that isn't marked never reaches the slow secret check, so its time tells nothing.
func TestTheGateFailsClosed(t *testing.T) {
	n := 0
	ok := fakeAgents{asked: &n}
	for name, c := range map[string]struct {
		g      Gate
		want   int
		hashed int
	}{
		"nothing wired":  {Gate{}, http.StatusNotFound, 0},
		"no agents":      {Gate{Publishers: fakePublishers{test: true}}, http.StatusNotFound, 0},
		"unmarked":       {Gate{Publishers: fakePublishers{}, Agents: ok}, http.StatusNotFound, 0},
		"a lookup error": {Gate{Publishers: fakePublishers{test: true, err: errors.New("down")}, Agents: ok}, http.StatusNotFound, 0},
		"a wrong secret": {Gate{Publishers: fakePublishers{test: true}, Agents: fakeAgents{err: errors.New("no"), asked: &n}}, http.StatusNotFound, 1},
		"marked":         {Gate{Publishers: fakePublishers{test: true}, Agents: ok}, http.StatusTeapot, 1},
		"broadcast on":   {Gate{Enabled: true}, http.StatusTeapot, 0},
	} {
		n = 0
		if got := ask(c.g, http.MethodGet, "/v1/broadcast/state"); got != c.want || n != c.hashed {
			t.Errorf("%s: %d with %d secret checks, want %d with %d", name, got, n, c.want, c.hashed)
		}
	}
}

// A staff exception is the console's alone, and a path outside the gate is untouched.
func TestStaffRoutesAndOtherPaths(t *testing.T) {
	const kill = "/v1/admin/broadcast/revocations"
	if got := ask(Gate{}, http.MethodGet, kill); got != http.StatusNotFound {
		t.Errorf("the kill switch on the user listener: %d", got)
	}
	if got := ask(Gate{Console: true}, http.MethodGet, kill); got != http.StatusTeapot {
		t.Errorf("the kill switch on the console: %d", got)
	}
	for _, p := range []string{"/v1/admin/servers/x/broadcast/invites", "/v1/admin/moderation", "/v1/admin/broadcast/geo-deny"} {
		if got := ask(Gate{Console: true}, http.MethodGet, p); got != http.StatusNotFound {
			t.Errorf("%s: %d", p, got)
		}
	}
	for _, p := range []string{"/v1/account/export", "/v1/billing/plans", "/v1/admin/servers", "/v1/homes/broadcast/link", "/healthz"} {
		if got := ask(Gate{}, http.MethodGet, p); got != http.StatusTeapot {
			t.Errorf("%s: %d", p, got)
		}
	}
}
