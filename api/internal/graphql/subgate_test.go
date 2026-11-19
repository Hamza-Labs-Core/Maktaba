package graphql

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Story 16.4: a field's REST sub-request runs under SubRequestGate (the seat gate in
// production), so a GraphQL mutation is refused where its REST route would be.
func TestSubRequestsRunUnderTheGate(t *testing.T) {
	var gated atomic.Int32
	refuse := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gated.Add(1)
			if r.Method != http.MethodGet {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"type":"https://maktaba.tv/problems/seat-read-only","title":"forbidden","status":403}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	SubRequestGate.Store(&refuse)
	t.Cleanup(func() { SubRequestGate.Store(nil) })
	rest := &fakeREST{}
	h := newTestHandler(rest)
	req := withUser(httptest.NewRequest(http.MethodPost, "/graphql",
		gqlBody(`mutation { saveSearch(name: "x", query: {q: "y"}) }`, nil)), "u1", false)
	code, env := post(t, h, req)
	if gated.Load() == 0 {
		t.Fatalf("the sub-request did not go through the gate (status %d, %v)", code, env)
	}
	if len(rest.taken()) != 0 {
		t.Fatal("the REST route ran although the gate refused it")
	}
	if env["errors"] == nil {
		t.Fatalf("a refused mutation reads as success: %v", env)
	}
}
