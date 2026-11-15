package broadcast

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// lookupCount is a ServerLookup that counts its calls: a refused caller must not reach it.
type lookupCount struct{ n int }

func (l *lookupCount) ByID(context.Context, string) (stores.Server, error) {
	l.n++
	return stores.Server{}, stores.ErrServerNotFound
}

func homeGet(r http.Handler, user, pass, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", HomeAnalyticsPath+query, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Story 50.28, the home door: a home's agent sees only its own home. No credential or a wrong
// secret is 401; a home naming another home's id is 403, before anything is read; a CSV ask is
// refused (the agent passes JSON only).
func TestHomeAnalyticsAnswersOnlyTheHomeItself(t *testing.T) {
	const home, other = "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60", "7e1f0c2d-3b4a-4c5d-8e9f-a0b1c2d3e4f5"
	look := &lookupCount{}
	r := chi.NewRouter()
	MountAnalyticsHome(r, &AnalyticsDeps{Servers: look, Agents: fakeAgents{home: "s3cret", other: "0ther"}})

	cases := []struct {
		name, user, pass, query string
		want                    int
		code                    string
	}{
		{"no credential", "", "", "", http.StatusUnauthorized, "unauthorized"},
		{"wrong secret", home, "nope", "", http.StatusUnauthorized, "unauthorized"},
		{"another home's secret for this id", home, "0ther", "", http.StatusUnauthorized, "unauthorized"},
		{"another home's id", home, "s3cret", "?server=" + other, http.StatusForbidden, "broadcast_analytics_forbidden"},
		{"csv", home, "s3cret", "?format=csv", http.StatusBadRequest, "bad_format"},
	}
	for _, c := range cases {
		w := homeGet(r, c.user, c.pass, c.query)
		if w.Code != c.want || errOf(w) != c.code {
			t.Errorf("%s: %d %q, want %d %q", c.name, w.Code, errOf(w), c.want, c.code)
		}
	}
	if look.n != 0 {
		t.Fatalf("a refused caller reached the store: %d lookups", look.n)
	}
	// its own id, named or not, passes the door (an unknown home is then 403, never 404)
	for _, q := range []string{"", "?server=" + home} {
		if w := homeGet(r, home, "s3cret", q); w.Code != http.StatusForbidden || look.n == 0 {
			t.Fatalf("own id %q: %d after %d lookups", q, w.Code, look.n)
		}
	}
	// no agent checker wired: nobody passes
	r2 := chi.NewRouter()
	MountAnalyticsHome(r2, &AnalyticsDeps{Servers: look})
	if w := homeGet(r2, home, "s3cret", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("without Agents: %d", w.Code)
	}
}
