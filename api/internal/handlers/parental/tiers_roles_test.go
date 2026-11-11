package parental

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const (
	testID  = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"
	testSID = "7a2e3d4c-5b6f-4071-9b8c-0d1e2f3a4b5c"
)

var tierRoutes = []string{
	"/api/videos/" + testID + "/age-tier",
	"/api/libraries/" + testID + "/age-tier",
	"/api/series/" + testID + "/age-tier",
	"/api/collections/" + testID + "/age-tier",
	"/api/channels/" + testID + "/age-tier",
	"/api/libraries/" + testID + "/youtube/sources/" + testSID + "/age-tier",
}

func serve(h *TiersHandler, p *principal.Principal, method, path, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// T08: the API enforces the role. No one gets 401; a viewer gets 403, even one the
// library is shared with; so does a guardian who is not an admin (a guardian is a
// user who looks after a restricted account, which gives no say over the home's
// ratings). All before any query: the handler has no database here, so one would
// panic. The admin's 200 is in the router's Postgres test.
func TestAgeTierRoutesRefuseNonAdmins(t *testing.T) {
	h := &TiersHandler{}
	viewer := &principal.Principal{UserID: "viewer", Libraries: []string{testID}}
	guardian := &principal.Principal{UserID: "guardian", Libraries: []string{testID}, AccessAllLibraries: true}
	for _, path := range tierRoutes {
		for _, c := range []struct {
			who  string
			p    *principal.Principal
			want int
		}{
			{"no one", nil, http.StatusUnauthorized},
			{"a viewer", viewer, http.StatusForbidden},
			{"a guardian", guardian, http.StatusForbidden},
		} {
			for _, body := range []string{`{"tier":13}`, `{"tier":null}`, `not json`} {
				if rec := serve(h, c.p, "PUT", path, body); rec.Code != c.want {
					t.Errorf("PUT %s %s as %s: %d, want %d", path, body, c.who, rec.Code, c.want)
				}
			}
		}
	}
}

// An admin's bad request is refused before any query (no database here either).
func TestAgeTierRoutesValidate(t *testing.T) {
	h := &TiersHandler{}
	admin := &principal.Principal{UserID: "admin", IsAdmin: true}
	for _, path := range tierRoutes {
		for _, body := range []string{`{}`, `{"tier":12}`, `{"tier":-1}`, `{"tier":"13"}`, `{"tier":13.5}`, `{"tier":13,"x":1}`, ``, `[]`} {
			if rec := serve(h, admin, "PUT", path, body); rec.Code != http.StatusBadRequest {
				t.Errorf("PUT %s %q: %d, want 400", path, body, rec.Code)
			}
		}
	}
	for _, path := range []string{
		"/api/videos/not-an-id/age-tier",
		"/api/libraries/not-an-id/age-tier",
		"/api/libraries/" + testID + "/youtube/sources/not-an-id/age-tier",
		"/api/libraries/not-an-id/youtube/sources/" + testSID + "/age-tier",
	} {
		if rec := serve(h, admin, "PUT", path, `{"tier":13}`); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s: %d, want 400", path, rec.Code)
		}
	}
}

// The ratings are every signed-in user's (an app maps a device's limit with them)
// and no one else's.
func TestRatingsNeedASignIn(t *testing.T) {
	h := &TiersHandler{}
	if rec := serve(h, nil, "GET", "/api/parental/ratings", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one: %d, want 401", rec.Code)
	}
	for _, p := range []*principal.Principal{{UserID: "viewer"}, {UserID: "admin", IsAdmin: true}} {
		rec := serve(h, p, "GET", "/api/parental/ratings", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d, want 200", p.UserID, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content type %q", ct)
		}
		for _, want := range []string{`"tiers"`, `"us-movie"`, `"tier_labels"`, `"ar"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Fatalf("the ratings lack %s", want)
			}
		}
	}
}
