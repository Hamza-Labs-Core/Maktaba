package parental

// Story 57.03: the gate's parts that need no database: how it finds the object in
// a path, how a hidden object's answer is sent, and that it fails closed before
// it has a cache.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func TestSwapParam(t *testing.T) {
	cases := []struct {
		path, pattern, param, want string
		ok                         bool
	}{
		{"/api/videos/abc", "/api/videos/{id}", "id", "/api/videos/X", true},
		{"/api/videos/abc/poster", "/api/videos/{id}/poster", "id", "/api/videos/X/poster", true},
		{"/api/libraries/abc/collections/tag/g1", "/api/libraries/{id}/collections/{kind}/{gid}", "id", "/api/libraries/X/collections/tag/g1", true},
		{"/api/x/abc", "/api/x/{id:[0-9a-f-]+}", "id", "/api/x/X", true},
		{"/api/videos/abc/", "/api/videos/{id}", "id", "/api/videos/X", true},
		{"/api/videos/abc", "/api/videos/{id}", "video_id", "", false},
		{"/api/videos/abc/extra", "/api/videos/{id}", "id", "", false},
	}
	for _, c := range cases {
		got, ok := swapParam(c.path, c.pattern, c.param, "X")
		if got != c.want || ok != c.ok {
			t.Errorf("swapParam(%q, %q, %q) = %q %v, want %q %v", c.path, c.pattern, c.param, got, ok, c.want, c.ok)
		}
	}
}

// A hidden object's answer names the id that was asked, in the body and the
// headers, with a length that fits.
func TestEchoWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	ew := &echoWriter{ResponseWriter: rec, from: "stand-in-id", to: "asked"}
	ew.Header().Set("Content-Type", "application/problem+json")
	ew.Header().Set("Location", "/api/videos/stand-in-id")
	ew.Header().Set("Content-Length", "33")
	ew.WriteHeader(http.StatusNotFound)
	ew.WriteHeader(http.StatusOK) // a second status is ignored
	_, _ = ew.Write([]byte(`{"detail":"video stand-in-id"}`))
	if rec.Body.Len() != 0 {
		t.Fatal("the answer went out before flush")
	}
	ew.flush()
	if rec.Code != http.StatusNotFound || rec.Body.String() != `{"detail":"video asked"}` {
		t.Errorf("answer = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/api/videos/asked" || rec.Header().Get("Content-Length") != "24" {
		t.Errorf("headers = %v", rec.Header())
	}
	if strings.Contains(rec.Body.String()+rec.Header().Get("Location"), "stand-in-id") {
		t.Error("the stand-in id reached the caller")
	}

	// nothing written: 200 with no body
	rec = httptest.NewRecorder()
	(&echoWriter{ResponseWriter: rec, from: "a", to: "b"}).flush()
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("an empty answer = %d %q", rec.Code, rec.Body.String())
	}
}

// Before the gate has a cache it cannot know who is restricted: every signed-in
// user is refused on every route that returns content, a route that returns none
// still answers (with a limit that shows nothing), and a caller with no user row
// is not limited.
func TestGateWithoutACacheFailsClosed(t *testing.T) {
	const user = "00000000-0000-4000-8000-000000570311"
	var seen *Effective
	r := chi.NewRouter()
	g := NewGate()
	g.Table = SurfaceTable{
		{Pattern: "/open", Class: Open, Reason: "test"},
		{Pattern: "/list", Class: Filtered, Reason: "test"},
		{Pattern: "/videos/{id}", Class: ByID, Kind: SubjectVideo, Param: "id", Reason: "test"},
		{Pattern: "/admin", Class: Refuse, Reason: "test"},
	}
	r.Use(g.Middleware(r))
	ok := func(w http.ResponseWriter, req *http.Request) {
		seen = From(req.Context())
		w.WriteHeader(http.StatusNoContent)
	}
	for _, p := range []string{"/open", "/list", "/videos/{id}", "/admin", "/unnamed"} {
		r.Get(p, ok)
	}
	do := func(p *principal.Principal, path string) int {
		seen = nil
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	viewer := &principal.Principal{UserID: user, AccessAllLibraries: true}
	for path, want := range map[string]int{
		"/open":       http.StatusNoContent,
		"/list":       http.StatusServiceUnavailable,
		"/videos/abc": http.StatusServiceUnavailable,
		"/admin":      http.StatusServiceUnavailable,
		"/unnamed":    http.StatusServiceUnavailable,
		"/nowhere":    http.StatusNotFound,
	} {
		if got := do(viewer, path); got != want {
			t.Errorf("GET %s as a user, no cache: %d, want %d", path, got, want)
		}
	}
	if do(viewer, "/open"); seen == nil || !seen.Limits() || seen.Permits(Item{}, time.Now()) || seen.MaxTier != NothingAllowed || !seen.AllowOnly {
		t.Errorf("an open route with no policy read ran under %+v, want a limit that shows nothing", seen)
	}
	for name, p := range map[string]*principal.Principal{
		"no one":          nil,
		"the admin token": {UserID: "admin-token", IsAdmin: true, AccessAllLibraries: true},
		"a machine":       {IsAdmin: true},
	} {
		for _, path := range []string{"/open", "/list", "/videos/abc", "/admin", "/unnamed"} {
			if got := do(p, path); got != http.StatusNoContent {
				t.Errorf("GET %s as %s: %d, want the handler's 204", path, name, got)
			}
			if seen.Limits() {
				t.Errorf("GET %s as %s ran under a limit", path, name)
			}
		}
	}
	if e, err := g.Limit(httptest.NewRequest(http.MethodGet, "/", nil).Context(), viewer); e != nil || err != ErrNoCache {
		t.Errorf("Limit with no cache = %v, %v", e, err)
	}
}

func TestRestrictedProblem(t *testing.T) {
	p := RestrictedProblem()
	if p.Status != http.StatusForbidden || p.Type != TypeRestricted {
		t.Errorf("the refusal = %+v", p)
	}
}

func TestSurfaceLookup(t *testing.T) {
	tbl := SurfaceTable{
		{Pattern: "/api/admin", Prefix: true, Class: Refuse},
		{Pattern: "/api/admin/open", Class: Open},
		{Pattern: "/api/admin/deep", Prefix: true, Class: Closed, Owner: "57.10"},
		{Methods: []string{http.MethodPost}, Pattern: "/api/me/tokens", Class: Refuse},
		{Methods: []string{http.MethodGet}, Pattern: "/api/me/tokens", Class: Open},
	}
	cases := []struct {
		method, pattern string
		want            Class
		ok              bool
	}{
		{http.MethodGet, "/api/admin/users", Refuse, true},
		{http.MethodGet, "/api/admin/open", Open, true},
		{http.MethodGet, "/api/admin/open/", Open, true},
		{http.MethodGet, "/api/admin/deep/x", Closed, true},
		{http.MethodPost, "/api/me/tokens", Refuse, true},
		{http.MethodHead, "/api/me/tokens", Open, true},
		{http.MethodDelete, "/api/me/tokens", 0, false},
		{http.MethodGet, "/api/videos", 0, false},
	}
	for _, c := range cases {
		s, ok := tbl.Lookup(c.method, c.pattern)
		if ok != c.ok || s.Class != c.want {
			t.Errorf("Lookup(%s %s) = %v %v, want %v %v", c.method, c.pattern, s.Class, ok, c.want, c.ok)
		}
	}
	if owned := tbl.Owned("57.10"); len(owned) != 1 || owned[0] != "* /api/admin/deep*" {
		t.Errorf("Owned(57.10) = %v", owned)
	}
	// the live registry: live TV waits for 57.10
	for _, p := range []string{"/api/channels", "/api/channels/{id}/now", "/api/tv/guide"} {
		if s, ok := Surfaces.Lookup(http.MethodGet, p); !ok || s.Class != Closed || s.Owner != "57.10" {
			t.Errorf("%s = %+v, want closed and owned by 57.10", p, s)
		}
	}
}

// An event stream is not held back to be rewritten: its frames go out as they are written.
func TestEchoWriterPassesAnEventStream(t *testing.T) {
	rec := httptest.NewRecorder()
	ew := &echoWriter{ResponseWriter: rec, from: "stand-in", to: "asked"}
	ew.Header().Set("Content-Type", "text/event-stream")
	ew.WriteHeader(http.StatusOK)
	if _, err := ew.Write([]byte("data: one\n\n")); err != nil {
		t.Fatal(err)
	}
	ew.Flush()
	if rec.Code != http.StatusOK || rec.Body.String() != "data: one\n\n" || !rec.Flushed {
		t.Fatalf("before the end: %d %q flushed=%v", rec.Code, rec.Body.String(), rec.Flushed)
	}
	ew.flush()
	if rec.Body.String() != "data: one\n\n" {
		t.Fatalf("after the end: %q", rec.Body.String())
	}
}
