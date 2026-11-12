package router_test

// Epic 57 story 57.13 T02: one table walks every Epic 57 route as each kind of caller,
// behind the production router on a real migrated Postgres:
//
//	no one, a viewer, a restricted child, that child's guardian, another child's
//	guardian, an admin, the admin token, and the child signed in through Maktaba Cloud
//	(a play-only sign-in).
//
// Every status is what the server answers; hiding a control in an app is not a rule. A
// new Epic 57 route (router.P57Routes reads them from the walk of every mount) fails
// TestP57RoleTableNamesEveryRoute until it has a row here. The rules each story's own
// role test holds in detail (bodies, what each caller reads) stay in those tests; this
// table is the one place that has every route and every caller.

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

// phWho are the table's columns, in order.
var phWho = [8]string{"", "viewer", "child", "guardian", "other-guardian", "admin", "admin-token", "child-cloud"}

type phRow struct {
	method, pattern string
	// path is the pattern with the fixtures' names for its parameters (phPath); body
	// the request body, with the same names.
	path, body string
	// want is the status for each caller of phWho.
	want [8]int
	// elsewhere names the test that walks the route's callers when this home cannot
	// mount it.
	elsewhere string
}

// phPath writes the fixtures' ids into a path or a body.
func phPath(p string) string {
	return strings.NewReplacer("{child}", pfChild, "{video}", pfKid, "{lib}", pfLibA, "{series}", pfSeries, "{col}", pfCol,
		"{ch}", phCh9, "{gid}", pfGuardian, "{rid}", phRule, "{req}", phRequest, "{family}", phFamily, "{src}", phSource,
		"{teen}", pfTeen, "{teen2}", pfTeen2, "{viewer}", pfViewer).Replace(p)
}

// The child is a 9+ restricted account whose guardian is "guardian"; "other-guardian"
// looks after another child. Reading the table: 401 no sign-in; 403 the caller may not
// do this (a restricted account is told `restricted`); 404 the caller is not to know
// the account or the route exists (another child's guardian, a closed route).
var phRows = []phRow{
	{method: http.MethodDelete, pattern: "/api/users/{id}/parental", path: "/api/users/{child}/parental", want: [8]int{401, 403, 403, 403, 404, 204, 204, 403}},
	{method: http.MethodDelete, pattern: "/api/users/{id}/parental/guardians/{gid}", path: "/api/users/{child}/parental/guardians/{gid}", want: [8]int{401, 403, 403, 403, 403, 204, 204, 403}},
	{method: http.MethodDelete, pattern: "/api/users/{id}/parental/rules/{rid}", path: "/api/users/{child}/parental/rules/{rid}", want: [8]int{401, 403, 403, 204, 404, 204, 204, 403}},
	{method: http.MethodGet, pattern: "/api/capabilities", path: "/api/capabilities", want: [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/channels/{id}/access", path: "/api/channels/{ch}/access", want: [8]int{403, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/me/parental", path: "/api/me/parental", want: [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/me/parental-pin", path: "/api/me/parental-pin", want: [8]int{401, 403, 403, 200, 200, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/me/parental/guardians", path: "/api/me/parental/guardians", want: [8]int{401, 403, 200, 403, 403, 403, 403, 200}},
	{method: http.MethodGet, pattern: "/api/me/parental/time", path: "/api/me/parental/time", want: [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/parental/children", path: "/api/parental/children", want: [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/parental/library-suggestions", path: "/api/parental/library-suggestions", want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/parental/ratings", path: "/api/parental/ratings", want: [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodGet, pattern: "/api/parental/requests", path: "/api/parental/requests", want: [8]int{401, 403, 403, 200, 200, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/parental/review", path: "/api/parental/review", want: [8]int{401, 403, 403, 200, 200, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/parental/settings", path: "/api/parental/settings", want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/parental/users/{id}/time-rules", path: "/api/parental/users/{child}/time-rules", want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/parental/users/{id}/time-usage", path: "/api/parental/users/{child}/time-usage", want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/tv/hdhr/allowed", path: "/api/tv/hdhr/allowed", want: [8]int{403, 403, 404, 403, 403, 200, 200, 404}},
	{method: http.MethodGet, pattern: "/api/tv/parental/verdict", path: "/api/tv/parental/verdict", want: [8]int{403, 403, 403, 403, 403, 403, 403, 403}},
	{method: http.MethodGet, pattern: "/api/users/{id}/parental", path: "/api/users/{child}/parental", want: [8]int{401, 404, 404, 200, 404, 200, 200, 404}},
	{method: http.MethodGet, pattern: "/api/users/{id}/parental/activity", path: "/api/users/{child}/parental/activity", want: [8]int{401, 404, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodGet, pattern: "/api/users/{id}/parental/rules", path: "/api/users/{child}/parental/rules", want: [8]int{401, 404, 404, 200, 404, 200, 200, 404}},
	{method: http.MethodPost, pattern: "/api/channels/{id}/play", path: "/api/channels/{ch}/play", want: [8]int{403, 200, 200, 200, 200, 200, 200, 200}},
	{method: http.MethodPost, pattern: "/api/me/age-signal", path: "/api/me/age-signal", want: [8]int{401, 409, 409, 409, 409, 409, 409, 409}},
	{method: http.MethodPost, pattern: "/api/me/parental/requests", path: "/api/me/parental/requests", body: `{"subject_kind":"video","subject_id":"{teen2}"}`, want: [8]int{401, 403, 201, 403, 403, 403, 403, 201}},
	{method: http.MethodPost, pattern: "/api/me/parental/unlock", path: "/api/me/parental/unlock", want: [8]int{401, 403, 422, 403, 403, 403, 403, 422}},
	{method: http.MethodPost, pattern: "/api/pairing/approve", elsewhere: "internal/handlers/discovery TestDeviceApprovedAsTheChild, TestDeviceApproveAsRefusals and TestDeviceApproveAsFromACloudSession (57.02 T06): the pairing routes need the sign-in service, which this home does not start"},
	{method: http.MethodPost, pattern: "/api/parental/requests/{id}/approve", path: "/api/parental/requests/{req}/approve", want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodPost, pattern: "/api/parental/requests/{id}/deny", path: "/api/parental/requests/{req}/deny", want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodPost, pattern: "/api/parental/review/{video_id}", path: "/api/parental/review/{video}", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 404, 404, 403}},
	{method: http.MethodPost, pattern: "/api/parental/settings/notice/dismiss", path: "/api/parental/settings/notice/dismiss", want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPost, pattern: "/api/parental/users/{id}/time-grants", path: "/api/parental/users/{child}/time-grants", body: `{"minutes":15}`, want: [8]int{401, 403, 403, 201, 404, 201, 201, 403}},
	{method: http.MethodPost, pattern: "/api/users/{id}/parental/guardians", path: "/api/users/{child}/parental/guardians", body: `{"guardian_id":"{viewer}"}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPost, pattern: "/api/users/{id}/parental/rules", path: "/api/users/{child}/parental/rules", body: `{"effect":"allow","subject_kind":"video","subject_id":"{teen}"}`, want: [8]int{401, 403, 403, 201, 404, 201, 201, 403}},
	{method: http.MethodPost, pattern: "/api/users/{id}/parental/signals/{family}/clear", path: "/api/users/{child}/parental/signals/{family}/clear", want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/channels/{id}/age-tier", path: "/api/channels/{ch}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 404, 403, 403, 200, 200, 404}},
	{method: http.MethodPut, pattern: "/api/collections/{id}/age-tier", path: "/api/collections/{col}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/libraries/{id}/age-tier", path: "/api/libraries/{lib}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/libraries/{id}/youtube/sources/{sid}/age-tier", path: "/api/libraries/{lib}/youtube/sources/{src}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/me/parental-pin", path: "/api/me/parental-pin", want: [8]int{401, 403, 403, 422, 422, 422, 422, 403}},
	{method: http.MethodPut, pattern: "/api/parental/settings", path: "/api/parental/settings", body: `{"enabled":true}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/parental/users/{id}/time-rules", path: "/api/parental/users/{child}/time-rules", body: `{"rules":[]}`, want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/series/{id}/age-tier", path: "/api/series/{series}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/tv/hdhr/allowed", path: "/api/tv/hdhr/allowed", body: `{"addresses":["192.0.2.7"]}`, want: [8]int{403, 403, 404, 403, 403, 200, 200, 404}},
	{method: http.MethodPut, pattern: "/api/users/{id}/parental", path: "/api/users/{child}/parental", body: `{"max_tier":13,"unrated":"hide","allow_only":false,"version":1}`, want: [8]int{401, 403, 403, 200, 404, 200, 200, 403}},
	{method: http.MethodPut, pattern: "/api/videos/{id}/age-tier", path: "/api/videos/{video}/age-tier", body: `{"tier":9}`, want: [8]int{401, 403, 403, 403, 403, 200, 200, 403}},
}

func TestP57RoleTable(t *testing.T) {
	h := setupHome(t)
	for _, row := range phRows {
		if row.elsewhere != "" {
			continue
		}
		for i, who := range phWho {
			h.reset()
			body := row.body
			if body == "" {
				body = "{}"
			}
			rec := h.raw(who, row.method, phPath(row.path), phPath(body))
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

// The table's own rules: what must hold in every row whatever the route does.
func TestP57RoleTableRules(t *testing.T) {
	const none, viewer, child, guardian, other, admin, token, cloud = 0, 1, 2, 3, 4, 5, 6, 7
	ok := func(code int) bool { return code/100 == 2 }
	for _, row := range phRows {
		if row.elsewhere != "" {
			continue
		}
		name := row.method + " " + row.path
		w := row.want
		if w[none] != http.StatusUnauthorized && w[none] != http.StatusForbidden {
			t.Errorf("%s: no one gets %d, want 401 or 403", name, w[none])
		}
		// the child through the cloud is the child: the same answer, and for a read
		// the story asks for exactly this
		if w[cloud] != w[child] {
			t.Errorf("%s: the child through the cloud gets %d, the child %d", name, w[cloud], w[child])
		}
		// the admin token is an admin
		if ok(w[admin]) != ok(w[token]) {
			t.Errorf("%s: the admin gets %d, the admin token %d", name, w[admin], w[token])
		}
		// what names another account (the child, a request of it) is never answered to
		// a viewer, the child itself or another child's guardian
		if strings.Contains(row.path, "{child}") || strings.Contains(row.path, "{req}") {
			for _, i := range []int{viewer, child, other} {
				if ok(w[i]) {
					t.Errorf("%s: %s gets %d on another account's parental route", name, phWho[i], w[i])
				}
			}
			if w[other] != http.StatusNotFound && w[other] != http.StatusForbidden {
				t.Errorf("%s: another child's guardian gets %d", name, w[other])
			}
		}
		// a change only an admin makes: setting a tier, the home's switch, the tuner list
		if strings.HasSuffix(row.pattern, "/age-tier") || strings.HasPrefix(row.pattern, "/api/parental/settings") ||
			row.pattern == "/api/tv/hdhr/allowed" || row.pattern == "/api/parental/library-suggestions" {
			for _, i := range []int{viewer, child, guardian, other} {
				if ok(w[i]) {
					t.Errorf("%s: %s gets %d on an admin's route", name, phWho[i], w[i])
				}
			}
			if !ok(w[admin]) {
				t.Errorf("%s: the admin gets %d", name, w[admin])
			}
		}
	}
}

// A new Epic 57 route fails here until the table has its row; a row whose route is gone
// fails until it is removed.
func TestP57RoleTableNamesEveryRoute(t *testing.T) {
	rows := map[string]bool{}
	for _, row := range phRows {
		k := row.method + " " + row.pattern
		if rows[k] {
			t.Errorf("%s has two rows", k)
		}
		rows[k] = true
	}
	mounted := map[string]bool{}
	var missing []string
	for _, k := range router.P57Routes(t) {
		mounted[k] = true
		if !rows[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d Epic 57 route(s) have no row in phRows (p57_roles_pg_test.go). Add each with the status every caller gets:\n  %s",
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
		t.Errorf("phRows names %d route(s) the home no longer mounts; remove each:\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
	if len(mounted) < 40 {
		t.Errorf("only %d Epic 57 routes were walked", len(mounted))
	}
}
