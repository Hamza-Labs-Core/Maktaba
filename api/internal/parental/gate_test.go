package parental

// Story 57.03: the gate's parts that need no database: how it finds the object in
// a path, how a hidden object's answer is sent, and that it fails closed before
// it has a cache.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
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

const (
	echoStand = "5b0e6d0c-7f3a-4a51-9d0e-2f6c1a7b8c9d"
	echoAsked = "0A1B2C3D-4E5F-4A6B-8C7D-9E0F1A2B3C4D"
	echoLower = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	echoBare  = "0a1b2c3d4e5f4a6b8c7d9e0f1a2b3c4d"
)

// A hidden object's answer names the id that was asked, in the spelling it was
// asked in, in the body and the headers, with a length that fits.
func TestEchoWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	ew := &echoWriter{ResponseWriter: rec, from: echoStand, to: echoAsked}
	ew.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	ew.Header().Set("Location", "/api/videos/"+echoStand)
	ew.Header().Set("Content-Length", "33")
	ew.WriteHeader(http.StatusNotFound)
	ew.WriteHeader(http.StatusOK) // a second status is ignored
	_, _ = ew.Write([]byte(`{"detail":"video ` + echoStand + `"}`))
	if rec.Body.Len() != 0 {
		t.Fatal("the answer went out before flush")
	}
	ew.flush()
	want := `{"detail":"video ` + echoAsked + `"}`
	if rec.Code != http.StatusNotFound || rec.Body.String() != want {
		t.Errorf("answer = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/api/videos/"+echoAsked || rec.Header().Get("Content-Length") != strconv.Itoa(len(want)) {
		t.Errorf("headers = %v", rec.Header())
	}
	// nothing else changes: the type stays the handler's, and no header is added
	if rec.Header().Get("Content-Type") != "application/problem+json; charset=utf-8" || len(rec.Header()) != 3 {
		t.Errorf("headers = %v", rec.Header())
	}
	if strings.Contains(rec.Body.String()+rec.Header().Get("Location"), echoStand) {
		t.Error("the stand-in id reached the caller")
	}

	// nothing written: 200 with no body
	rec = httptest.NewRecorder()
	(&echoWriter{ResponseWriter: rec, from: echoStand, to: echoAsked}).flush()
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || len(rec.Header()) != 0 {
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
	var seenAs *principal.Principal
	r := chi.NewRouter()
	g := NewGate()
	g.Table = SurfaceTable{
		{Pattern: "/open", Class: Open, Reason: "test"},
		{Pattern: "/guarded", Class: Guarded, Reason: "test"},
		{Pattern: "/list", Class: Filtered, Reason: "test"},
		{Pattern: "/videos/{id}", Class: ByID, Kind: SubjectVideo, Param: "id", Reason: "test"},
		{Pattern: "/admin", Class: Refuse, Reason: "test"},
	}
	r.Use(g.Middleware(r))
	ok := func(w http.ResponseWriter, req *http.Request) {
		seen = From(req.Context())
		seenAs = principal.FromContext(req.Context())
		w.WriteHeader(http.StatusNoContent)
	}
	for _, p := range []string{"/open", "/guarded", "/list", "/videos/{id}", "/admin", "/unnamed"} {
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
		"/guarded":    http.StatusNoContent,
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
	// a route that reads the limit or the role runs with a limit that shows nothing and
	// with no admin role, whatever the token says
	stale := &principal.Principal{UserID: user, IsAdmin: true, AccessAllLibraries: true}
	if do(stale, "/guarded"); seen == nil || !seen.Limits() || seen.Permits(Item{}, time.Now()) || seen.MaxTier != NothingAllowed || !seen.AllowOnly {
		t.Errorf("a guarded route with no policy read ran under %+v, want a limit that shows nothing", seen)
	}
	if seenAs == nil || seenAs.IsAdmin || seenAs.AccessAllLibraries || seenAs.UserID != user || !stale.IsAdmin {
		t.Errorf("a guarded route with no policy read ran as %+v, want the caller with no admin role (and the caller's own principal untouched)", seenAs)
	}
	// an open route reads nothing: it runs as the caller, under no limit of the gate's
	if do(stale, "/open"); seen == nil || seen.Limits() || seenAs != stale {
		t.Errorf("an open route ran under %+v as %+v, want the request as it came", seen, seenAs)
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
	// the live registry: the live TV routes story 57.10 filters are named; any other
	// route under them stays closed
	for _, p := range []string{"/api/channels", "/api/channels/guide", "/api/channels/{id}/access"} {
		if s, ok := Surfaces.Lookup(http.MethodGet, p); !ok || s.Class != Filtered {
			t.Errorf("%s = %+v, want filtered", p, s)
		}
	}
	for _, p := range []string{"/api/channels/sources", "/api/channels/{id}/new", "/api/tv/guide", "/api/tv/onair"} {
		if s, ok := Surfaces.Lookup(http.MethodGet, p); !ok || s.Class != Closed {
			t.Errorf("%s = %+v, want closed", p, s)
		}
	}
	if s, ok := Surfaces.Lookup(http.MethodPatch, "/api/channels/{id}"); !ok || s.Class != Closed {
		t.Errorf("PATCH /api/channels/{id} = %+v, want closed", s)
	}
}

// An event stream is not held back to be rewritten: its frames go out as they are written.
func TestEchoWriterPassesAnEventStream(t *testing.T) {
	rec := httptest.NewRecorder()
	ew := &echoWriter{ResponseWriter: rec, from: echoStand, to: echoAsked}
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

// What the request asked for is never written into an answer unless it is a UUID:
// text that is none (a script, a header break) is answered as the nil UUID, in the
// body and in every header, whatever the answer's type (the API answers a browser's
// navigation with a page).
func TestEchoWriterNeverEchoesWhatIsNoUUID(t *testing.T) {
	for _, ctype := range []string{"application/problem+json", "text/html; charset=utf-8", "text/plain", ""} {
		for _, asked := range []string{
			`<script>alert(1)</script>`, `"><img src=x onerror=alert(1)>`, "x\r\nSet-Cookie: a=b", "", "abc",
			echoLower + "<script>", "{" + echoLower + "}x", "<" + echoLower + ">", `"` + echoLower + `'`,
			" " + echoLower, echoLower + "\n", "urn:uuid:" + echoLower + "&",
		} {
			rec := httptest.NewRecorder()
			ew := &echoWriter{ResponseWriter: rec, from: echoStand, to: asked}
			if ctype != "" {
				ew.Header().Set("Content-Type", ctype)
			}
			ew.Header().Set("Location", "/api/videos/"+echoStand)
			ew.WriteHeader(http.StatusNotFound)
			_, _ = ew.Write([]byte(`<p>/api/videos/` + echoStand + `</p>`))
			ew.flush()
			if rec.Body.String() != `<p>/api/videos/`+nilID+`</p>` || rec.Header().Get("Location") != "/api/videos/"+nilID {
				t.Errorf("%q, asked %q: answer %s, Location %q", ctype, asked, rec.Body.String(), rec.Header().Get("Location"))
			}
			if got := rec.Header().Get("Content-Type"); got != ctype {
				t.Errorf("%q, asked %q: the type became %q", ctype, asked, got)
			}
		}
	}
}

// Every spelling of a UUID is put back as it was asked: a hidden object asked for in
// another spelling answers as a missing one asked for in that spelling.
func TestEchoWriterKeepsTheSpellingAsked(t *testing.T) {
	for _, asked := range []string{echoLower, echoAsked, echoBare, strings.ToUpper(echoBare), "{" + echoLower + "}", "urn:uuid:" + echoLower, "URN:UUID:" + echoAsked} {
		rec := httptest.NewRecorder()
		ew := &echoWriter{ResponseWriter: rec, from: echoStand, to: asked}
		_, _ = ew.Write([]byte("video " + echoStand))
		ew.flush()
		if rec.Body.String() != "video "+asked {
			t.Errorf("asked %q: answer %q", asked, rec.Body.String())
		}
	}
}

// An event stream goes out under the type its handler gave it.
func TestEchoWriterEventStreamType(t *testing.T) {
	rec := httptest.NewRecorder()
	ew := &echoWriter{ResponseWriter: rec, from: echoStand, to: `<script>`}
	ew.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	_, _ = ew.Write([]byte(": hello\n\n"))
	_, _ = ew.Write([]byte(": again\n\n"))
	if h := rec.Header(); h.Get("Content-Type") != "text/event-stream; charset=utf-8" || len(h) != 1 {
		t.Fatalf("headers %v", h)
	}
	if rec.Body.String() != ": hello\n\n: again\n\n" {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// failingDB is a database that never answers: every connection fails, and each try is
// counted.
type failingDB struct{ tries atomic.Int32 }

func (f *failingDB) Connect(context.Context) (driver.Conn, error) {
	f.tries.Add(1)
	return nil, errors.New("the home's database is down")
}
func (f *failingDB) Driver() driver.Driver { return nil }

// The home's database is down: no policy can be read. Every Open route of the API's
// registry still reaches its handler, for an admin, a viewer and a machine, and the gate
// does not even try to read (CI's replica job failed when GET /api/cluster/emergency, the
// route for that very moment, was refused 503 by the gate). Every other route still
// refuses a signed-in user: nothing is shown on a guess.
func TestGateReadsNoPolicyForAnOpenRoute(t *testing.T) {
	down := &failingDB{}
	db := sql.OpenDB(down)
	defer func() { _ = db.Close() }()
	g := NewGate()
	g.SetCache(NewCache(NewStore(db)))
	r := chi.NewRouter()
	r.Use(g.Middleware(r))
	reached := 0
	h := func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusNoContent)
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	type route struct {
		method, pattern string
		class           Class
	}
	var routes []route
	mounted := map[string]bool{}
	for _, s := range Surfaces {
		pattern := s.Pattern
		if s.Prefix {
			pattern = strings.TrimSuffix(s.Pattern, "/") + "/gate-test-{x}"
		}
		for _, m := range methods {
			got, ok := Surfaces.Lookup(m, pattern)
			if !ok || mounted[m+" "+pattern] {
				continue
			}
			mounted[m+" "+pattern] = true
			r.MethodFunc(m, pattern, h)
			routes = append(routes, route{m, pattern, got.Class})
		}
	}
	param := regexp.MustCompile(`\{[^}]+\}`)
	callers := []struct {
		name string
		p    *principal.Principal
		user bool
	}{
		{"an admin", &principal.Principal{UserID: "00000000-0000-4000-8000-000000570321", IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}, true},
		{"a viewer", &principal.Principal{UserID: "00000000-0000-4000-8000-000000570322", Source: principal.SourceJWT}, true},
		{"a machine", &principal.Principal{IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceAdminToken}, false},
		{"no one", nil, false},
	}
	open, others := 0, 0
	for _, rt := range routes {
		path := param.ReplaceAllString(rt.pattern, "00000000-0000-4000-8000-000000570399")
		for _, c := range callers {
			req := httptest.NewRequest(rt.method, path, nil)
			if c.p != nil {
				req = req.WithContext(principal.WithPrincipal(req.Context(), c.p))
			}
			rec := httptest.NewRecorder()
			before, tries := reached, down.tries.Load()
			r.ServeHTTP(rec, req)
			ran := reached == before+1
			switch {
			case rt.class == Open:
				if !ran || rec.Code != http.StatusNoContent {
					t.Errorf("%s %s (open) as %s with the database down: %d, want its handler", rt.method, rt.pattern, c.name, rec.Code)
				}
				if got := down.tries.Load(); got != tries {
					t.Errorf("%s %s (open) as %s: the gate tried to read a policy", rt.method, rt.pattern, c.name)
				}
			case !c.user:
				if !ran {
					t.Errorf("%s %s as %s: %d, want its handler (the gate is for signed-in users)", rt.method, rt.pattern, c.name, rec.Code)
				}
			case rt.class == Guarded:
				if !ran {
					t.Errorf("%s %s (guarded) as %s with the database down: %d, want its handler, under a limit that shows nothing", rt.method, rt.pattern, c.name, rec.Code)
				}
			default:
				if ran || rec.Code != http.StatusServiceUnavailable {
					t.Errorf("%s %s (%s) as %s with the database down: %d (handler ran: %v), want 503", rt.method, rt.pattern, rt.class, c.name, rec.Code, ran)
				}
			}
		}
		if rt.class == Open {
			open++
		} else {
			others++
		}
	}
	if open < 150 || others < 300 {
		t.Fatalf("only %d open and %d other routes were tried", open, others)
	}
	// the routes named as needed with the database down are among them
	for _, d := range DatabaseDown {
		if !mounted["GET "+d.Pattern] {
			t.Errorf("%s (parental.DatabaseDown) was not tried", d.Pattern)
		}
	}
}
