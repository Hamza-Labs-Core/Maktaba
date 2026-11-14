package router_test

// Epic 58 story 58.17 T01: one table walks Epic 58's routes (router.P58Routes: each story's
// own, and every route a cast grant may call) as each kind of caller, behind the production
// router on a real migrated Postgres (setupHome, with the discovery and pairing routes):
//
//	no one, a viewer, a viewer who reads no library, a restricted child (9+), that child's
//	guardian, an admin, a viewer signed in through Maktaba Cloud (a play-only sign-in), and a
//	cast grant a viewer minted for video A (through the grant's middleware and the auth gate,
//	as the home runs them).
//
// Video A is the all-ages video of library A (pfKid); video B the all-ages video of library
// B (pfKidB). A video of a library the caller does not read is refused 403, as everywhere on
// the home; a session that is another account's answers 404. Every status is what the server answers; hiding a control in an app is not a
// rule. A new Epic 58 route fails TestP58RoleTableNamesEveryRoute until it has a row here; a
// route this home cannot mount names the test that walks its callers. The cast column of
// every other route the home mounts is TestCastGrantWalkRefusesEveryOtherRoute's.

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// p58Who are the table's columns, in order.
var p58Who = [8]string{"", "viewer", "viewer-nolib", "child", "guardian", "admin", "viewer-cloud", "cast"}

// p58NoLib is a viewer of this home who reads no library.
const p58NoLibUser = "00000000-0000-4000-8000-000000581701"

const (
	p58None, p58Viewer, p58NoLib, p58Child, p58Guardian, p58Admin, p58Cloud, p58Cast = 0, 1, 2, 3, 4, 5, 6, 7
)

type p58Row struct {
	method, pattern string
	// path is the pattern with the fixtures' names: {A} and {B} the two videos, {session}
	// a stream session for A opened by the caller (by the viewer for a caller that cannot
	// open one: no one, a viewer without the library, the viewer through the cloud, the
	// grant, whose sessions are the viewer's)
	path, body string
	want       [8]int
	elsewhere  string
}

func p58Path(p string) string {
	return strings.NewReplacer("{A}", pfKid, "{B}", pfKidB).Replace(p)
}

var p58Rows = []p58Row{
	// the home's identity and the compatibility contract: public (58.01)
	{method: http.MethodGet, pattern: "/api/discovery/home", path: "/api/discovery/home", want: [8]int{200, 200, 200, 200, 200, 200, 200, 200}},
	// the cast grant (58.07): a person's own sign-in on the home mints one for what that
	// person may play; a sign-in through Maktaba Cloud and a grant mint none
	{method: http.MethodPost, pattern: "/api/cast/grants", path: "/api/cast/grants", body: `{"video_id":"{A}"}`, want: [8]int{401, 201, 403, 201, 201, 201, 403, 403}},
	// sessions (58.02's device_caps): the grant opens one for its own video only
	{method: http.MethodPost, pattern: "/api/stream/sessions", path: "/api/stream/sessions",
		body: `{"video_id":"{A}","client_profile":"firetv","device_caps":{"containers":["mp4","mkv"],"video":[{"codec":"hevc","max_height":2160}],"hdr":["hdr10"]}}`,
		want: [8]int{401, 201, 403, 201, 201, 201, 201, 201}},
	{method: http.MethodGet, pattern: "/api/stream/sessions/{id}", path: "/api/stream/sessions/{session}", want: [8]int{401, 200, 404, 200, 200, 200, 200, 200}},
	{method: http.MethodPost, pattern: "/api/stream/sessions/{id}/progress", path: "/api/stream/sessions/{session}/progress", body: `{"position_sec":12}`, want: [8]int{401, 200, 404, 200, 200, 200, 200, 200}},
	// a player's failure report is logged under the caller's own account and looks nothing
	// up (the session may be gone, the very failure reported): any signed-in caller is heard
	{method: http.MethodPost, pattern: "/api/stream/sessions/{id}/errors", path: "/api/stream/sessions/{session}/errors", body: `{"code":"decode","message":"rehearsal"}`, want: [8]int{401, 202, 202, 202, 202, 202, 202, 202}},
	{method: http.MethodDelete, pattern: "/api/stream/sessions/{id}", path: "/api/stream/sessions/{session}", want: [8]int{401, 204, 404, 204, 204, 204, 204, 204}},
	// what a receiver reads of its video
	{method: http.MethodGet, pattern: "/api/videos/{id}", path: "/api/videos/{A}", want: [8]int{401, 200, 403, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/videos/{id}/chapters", path: "/api/videos/{A}/chapters", want: [8]int{401, 200, 403, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/videos/{id}/subtitles", path: "/api/videos/{A}/subtitles", want: [8]int{401, 200, 403, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/videos/{id}/poster", path: "/api/videos/{A}/poster", want: [8]int{401, 404, 404, 404, 404, 404, 404, 404}},
	{method: http.MethodGet, pattern: "/api/me/play-queues/{id}", elsewhere: "internal/router TestPGCastGrantQueue and TestP58BypassCastGrantAfterTighten (58.07, 58.14 A24): a queue grant walks its owner's queue only"},
	{method: http.MethodPut, pattern: "/api/me/play-queues/{id}/current", elsewhere: "internal/router TestPGCastGrantQueue (58.07): a queue grant moves its owner's queue only"},
	// the cloud code (58.03): its routes need the sign-in service and a relay agent
	{method: http.MethodPost, pattern: "/api/pairing/cloud-approve", elsewhere: "internal/router TestPGCloudCodeSignsTheTVInAsTheApprover, TestPGCloudCodeRefusals and TestP58BypassCloudCodeApprover (58.03 T03, 58.14 A25): no one 401, a viewer, an admin, a child and a guardian as the child 200, a sign-in through the cloud 403"},
	{method: http.MethodPost, pattern: "/api/pairing/cloud-assertion", elsewhere: "internal/router TestPGCloudAssertionSignsTheTVInAsTheLinkedUser and TestPGCloudAssertionWithoutACloudIdentity (58.03): only the cloud's signed assertion approves"},
	{method: http.MethodGet, pattern: "/api/pairing/cloud-code/{code}", elsewhere: "internal/router TestPGCloudCodeRefusals (58.03): no one 401"},
	{method: http.MethodGet, pattern: "/api/me/app-sign-ins", elsewhere: "internal/router TestPGAppSignInsShowTheCloudCodeTV (58.03): each person lists their own"},
	{method: http.MethodDelete, pattern: "/api/me/app-sign-ins/{id}", elsewhere: "internal/router TestPGAppSignInsShowTheCloudCodeTV (58.03): each person signs out their own"},
}

// p58Extra are the story's rows for routes that are not Epic 58's, checked here for the new
// callers (the viewer through the cloud, the grant): an admin route and the library list.
var p58Extra = []p58Row{
	{method: http.MethodGet, pattern: "/api/parental/settings", path: "/api/parental/settings", want: [8]int{401, 403, 403, 403, 403, 200, 403, 403}},
	{method: http.MethodGet, pattern: "/api/videos", path: "/api/videos", want: [8]int{401, 200, 200, 200, 200, 200, 200, 403}},
	{method: http.MethodPost, pattern: "/api/stream/sessions", path: "/api/stream/sessions", body: `{"video_id":"{B}"}`, want: [8]int{401, 201, 403, 201, 201, 201, 201, 403}},
}

// p58Home is setupHome with the discovery and pairing routes (MountP10), the extra callers
// of the table, and the grant's front door.
type p58Home struct {
	*p57Home
	grant func(grant, method, path, body string) *httptest.ResponseRecorder
	cast  string // the grant a viewer minted for A
	// front is the auth gate in front of the router: no one is asked through it, as the
	// home asks them
	front http.Handler
}

func setupP58Home(t *testing.T) *p58Home {
	t.Helper()
	h := setupHomeWith(t, func(r chi.Router, e *pfEnv) {
		router.MountP10(r, router.P10Deps{DB: e.db, Keys: castKeys(t), Logger: slog.Default()})
	})
	libs := []string{pfLibA, pfLibB}
	h.as = func(req *http.Request, p *principal.Principal) *principal.Principal {
		switch req.Header.Get("X-Test-As") {
		case "viewer-nolib": // a viewer who reads no library
			return &principal.Principal{UserID: p58NoLibUser, Libraries: []string{}, Source: principal.SourceJWT}
		case "viewer-cloud": // the viewer signed in through Maktaba Cloud: a play-only sign-in
			q := &principal.Principal{UserID: pfViewer, Libraries: libs, Source: principal.SourceJWT}
			q.ApplyCloudScope(principal.CloudScopePlay, libs)
			return q
		}
		return p
	}
	h.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'p58-nolib', 'x', false)`, p58NoLibUser)
	p := &p58Home{p57Home: h, grant: tvGrantFront(t, h), front: authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(h.h)}
	p.cast = tvMint(t, h, "viewer", `{"video_id":"`+pfKid+`"}`)
	return p
}

// call asks as who: the grant through its front door, every other caller as the table's.
func (h *p58Home) call(who, method, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	switch who {
	case "cast":
		return h.grant(h.cast, method, path, body)
	case "":
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.front.ServeHTTP(rec, req)
		return rec
	}
	return h.raw(who, method, path, body)
}

// session opens a stream session for A as the caller that owns it for who.
func (h *p58Home) session(who string) string {
	h.t.Helper()
	switch who {
	case "", "viewer-nolib", "viewer-cloud":
		who = "viewer"
	}
	rec := h.call(who, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`)
	var out struct {
		ID string `json:"session_id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.ID == "" {
		h.t.Fatalf("%q opens a session for A: %d %s", who, rec.Code, rec.Body.String())
	}
	return out.ID
}

func runP58Rows(t *testing.T, h *p58Home, rows []p58Row) {
	t.Helper()
	for _, row := range rows {
		if row.elsewhere != "" {
			continue
		}
		for i, who := range p58Who {
			h.reset()
			path := p58Path(row.path)
			if strings.Contains(path, "{session}") {
				path = strings.ReplaceAll(path, "{session}", h.session(who))
			}
			rec := h.call(who, row.method, path, p58Path(row.body))
			name := who
			if name == "" {
				name = "no one"
			}
			if rec.Code != row.want[i] {
				t.Errorf("%s %s as %s = %d, want %d: %s", row.method, row.path, name, rec.Code, row.want[i], strings.TrimSpace(rec.Body.String()))
			}
		}
	}
}

func TestP58RoleTable(t *testing.T) {
	t.Parallel()
	h := setupP58Home(t)
	runP58Rows(t, h, p58Rows)
	runP58Rows(t, h, p58Extra)
}

// The table's own rules: what must hold in every row whatever the route does.
func TestP58RoleTableRules(t *testing.T) {
	ok := func(code int) bool { return code/100 == 2 }
	for _, row := range append(append([]p58Row{}, p58Rows...), p58Extra...) {
		if row.elsewhere != "" {
			continue
		}
		name := row.method + " " + row.path + " " + row.body
		w := row.want
		public := row.pattern == "/api/discovery/home"
		if !public && w[p58None] != http.StatusUnauthorized {
			t.Errorf("%s: no one gets %d, want 401", name, w[p58None])
		}
		// a play sign-in through the cloud is never more than the same viewer on the home
		if ok(w[p58Cloud]) && !ok(w[p58Viewer]) {
			t.Errorf("%s: the viewer through the cloud gets %d, the viewer %d", name, w[p58Cloud], w[p58Viewer])
		}
		// a grant is never more than the viewer who minted it, and never mints
		if ok(w[p58Cast]) && !ok(w[p58Viewer]) {
			t.Errorf("%s: the grant gets %d, its viewer %d", name, w[p58Cast], w[p58Viewer])
		}
		if row.pattern == "/api/cast/grants" && ok(w[p58Cast]) {
			t.Errorf("%s: a grant mints a grant", name)
		}
		// what names a video, or another account's session, is never answered to a viewer
		// who reads none of its library (but the failure report, which reads nothing)
		if (strings.Contains(row.path, "{A}") || strings.Contains(row.path, "{session}") || strings.Contains(row.body, "{A}") ||
			strings.Contains(row.body, "{B}")) && !strings.HasSuffix(row.pattern, "/errors") {
			if w[p58NoLib] != http.StatusForbidden && w[p58NoLib] != http.StatusNotFound {
				t.Errorf("%s: a viewer without the library gets %d, want 403 or 404", name, w[p58NoLib])
			}
		}
		// video B is not the grant's
		if strings.Contains(row.path, "{B}") || strings.Contains(row.body, "{B}") {
			if w[p58Cast] != http.StatusForbidden {
				t.Errorf("%s: the grant for A gets %d on B, want 403", name, w[p58Cast])
			}
		}
		// only an admin calls an admin route: not the viewer through the cloud, not a grant
		if strings.HasPrefix(row.pattern, "/api/parental/settings") {
			for _, i := range []int{p58Viewer, p58NoLib, p58Child, p58Guardian, p58Cloud, p58Cast} {
				if ok(w[i]) {
					t.Errorf("%s: %s gets %d on an admin's route", name, p58Who[i], w[i])
				}
			}
		}
	}
}

// A new Epic 58 route fails here until the table has its row; a row whose route is gone
// fails until it is removed.
func TestP58RoleTableNamesEveryRoute(t *testing.T) {
	rows := map[string]bool{}
	for _, row := range p58Rows {
		k := row.method + " " + row.pattern
		if rows[k] {
			t.Errorf("%s has two rows", k)
		}
		if row.elsewhere == "" && row.path == "" {
			t.Errorf("%s: a row with no path and nothing elsewhere", k)
		}
		rows[k] = true
	}
	mounted := map[string]bool{}
	var missing []string
	for _, k := range router.P58Routes(t) {
		mounted[k] = true
		if !rows[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d Epic 58 route(s) have no row in p58Rows (p58_roles_pg_test.go). Add each with the status every caller gets:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	var stale []string
	for k := range rows {
		if !mounted[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("p58Rows names %d route(s) that are not Epic 58's mounted routes; remove each:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}
