package parental_test

// The parental routes against a real migrated Postgres (slot 0221): the role of
// every route, tried as no one, a viewer, the restricted child, that child's
// guardian, another child's guardian and an admin; and what each route does.
// Runs when MAKTABA_TEST_PG_URL names a Postgres the user may create databases on.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

const (
	uAdmin    = "00000000-0000-4000-8000-000000572001"
	uGuardian = "00000000-0000-4000-8000-000000572002"
	uOtherG   = "00000000-0000-4000-8000-000000572003"
	uChild    = "00000000-0000-4000-8000-000000572004"
	uChild2   = "00000000-0000-4000-8000-000000572005"
	uViewer   = "00000000-0000-4000-8000-000000572006"
	libOpen   = "00000000-0000-4000-8000-00000572a000"
	libClosed = "00000000-0000-4000-8000-00000572b000"
	ruleID    = "00000000-0000-4000-8000-0000057200f1"
)

// who is every caller the role table names.
var who = []string{"", "viewer", "child", "guardian", "other-guardian", "admin"}

type env struct {
	t  *testing.T
	db *sql.DB
	h  http.Handler
	// what the ask, PIN and activity tests (requests_pg_test.go) drive: the store,
	// its clock (zero: the real time), the notices sent and the caller's address
	store   *parental.Store
	clock   time.Time
	notices []notice
	remote  string
}

func setup(t *testing.T) *env {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	e := &env{t: t, db: db}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES
		($1,'h-admin','x',true), ($2,'h-guardian','x',false), ($3,'h-other-guardian','x',false),
		($4,'h-child','x',false), ($5,'h-child-two','x',false), ($6,'h-viewer','x',false)`,
		uAdmin, uGuardian, uOtherG, uChild, uChild2, uViewer)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'HOpen','{/media/h-open}'), ($2,'HClosed','{/media/h-closed}')`, libOpen, libClosed)
	e.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1,$2)`, uChild, libOpen)
	e.reset()

	store := parental.NewStore(db)
	r := chi.NewRouter()
	(&parentalh.Handler{Store: store, Cache: parental.NewCache(store)}).Mount(r)
	e.mountAsk(r, store)
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: uAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "guardian":
			p = &principal.Principal{UserID: uGuardian, Source: principal.SourceJWT}
		case "other-guardian":
			p = &principal.Principal{UserID: uOtherG, Source: principal.SourceJWT}
		case "child":
			p = &principal.Principal{UserID: uChild, Libraries: []string{libOpen}, Source: principal.SourceJWT}
		case "viewer":
			p = &principal.Principal{UserID: uViewer, Source: principal.SourceJWT}
		case "guardian-cloud-play": // the guardian, signed in through a cloud link that may only play
			p = &principal.Principal{UserID: uGuardian, Source: principal.SourceJWT, CloudScope: principal.CloudScopePlay}
		case "admin-token": // the admin token: an admin with no user row
			p = &principal.Principal{UserID: "admin-token", IsAdmin: true, AccessAllLibraries: true}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("exec %q: %v", q, err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// reset puts the two children, their guardians and one rule back as they start:
// the child at 9+ looked after by the guardian, child two at 13+ looked after by
// the other guardian.
func (e *env) reset() {
	e.t.Helper()
	e.exec(`DELETE FROM parental_policies`)
	e.exec(`UPDATE users SET is_admin = false WHERE id NOT IN ($1)`, uAdmin)
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1), ($2, 13, 1)`, uChild, uChild2)
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1,$2), ($3,$4)`, uChild, uGuardian, uChild2, uOtherG)
	e.exec(`INSERT INTO parental_rules (id, user_id, effect, subject_kind, subject_id) VALUES ($1,$2,'block','video','v-blocked')`, ruleID, uChild)
}

func (e *env) do(as, method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", as)
	if e.remote != "" {
		req.RemoteAddr = e.remote
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%s %s as %q: body is not JSON: %s", method, path, as, rec.Body.String())
		}
	}
	return rec.Code, out
}

func (e *env) want(status int, as, method, path string, body any) map[string]any {
	e.t.Helper()
	code, out := e.do(as, method, path, body)
	if code != status {
		e.t.Fatalf("%s %s as %q = %d, want %d (%v)", method, path, as, code, status, out)
	}
	return out
}

// T02: every route, as every caller. The columns are who: no one, a viewer, the
// child, the child's guardian, another child's guardian, an admin.
func TestRoles(t *testing.T) {
	e := setup(t)
	base := "/api/users/" + uChild + "/parental"
	routes := []struct {
		name, method, path string
		body               any
		want               [6]int
	}{
		{"list children", "GET", "/api/parental/children", nil, [6]int{401, 200, 200, 200, 200, 200}},
		{"own limit", "GET", "/api/me/parental", nil, [6]int{401, 200, 200, 200, 200, 200}},
		{"read the policy", "GET", base, nil, [6]int{401, 404, 404, 200, 404, 200}},
		{"read the rules", "GET", base + "/rules", nil, [6]int{401, 404, 404, 200, 404, 200}},
		{"change the policy", "PUT", base, map[string]any{"max_tier": 13, "unrated": "hide", "version": 1},
			[6]int{401, 403, 403, 200, 404, 200}},
		{"lift the restriction", "DELETE", base, nil, [6]int{401, 403, 403, 403, 404, 204}},
		{"add a rule", "POST", base + "/rules", map[string]any{"effect": "block", "subject_kind": "series", "subject_id": "s-1"},
			[6]int{401, 403, 403, 201, 404, 201}},
		{"remove a rule", "DELETE", base + "/rules/" + ruleID, nil, [6]int{401, 403, 403, 204, 404, 204}},
		{"name a guardian", "POST", base + "/guardians", map[string]any{"guardian_id": uViewer},
			[6]int{401, 403, 403, 403, 403, 200}},
		{"remove a guardian", "DELETE", base + "/guardians/" + uGuardian, nil, [6]int{401, 403, 403, 403, 403, 204}},
	}
	for _, rt := range routes {
		for i, as := range who {
			e.reset()
			policies, guardians, rules := e.count(`SELECT count(*) FROM parental_policies`),
				e.count(`SELECT count(*) FROM parental_guardians`), e.count(`SELECT count(*) FROM parental_rules`)
			version := e.count(`SELECT version FROM parental_policies WHERE user_id = $1`, uChild)
			code, body := e.do(as, rt.method, rt.path, rt.body)
			if code != rt.want[i] {
				t.Errorf("%s as %q = %d, want %d (%v)", rt.name, as, code, rt.want[i], body)
				continue
			}
			if code >= 400 {
				// a refusal changes nothing
				if e.count(`SELECT count(*) FROM parental_policies`) != policies ||
					e.count(`SELECT count(*) FROM parental_guardians`) != guardians ||
					e.count(`SELECT count(*) FROM parental_rules`) != rules ||
					e.count(`SELECT version FROM parental_policies WHERE user_id = $1`, uChild) != version {
					t.Errorf("%s as %q was refused (%d) and still changed something", rt.name, as, code)
				}
			}
			// the restricted child's refusal says why
			if as == "child" && code == 403 && body["code"] != "restricted" {
				t.Errorf("%s as the child: code = %v, want restricted", rt.name, body["code"])
			}
			// a 404 says nothing of the child
			if code == 404 {
				if raw, _ := json.Marshal(body); bytes.Contains(raw, []byte("h-child")) {
					t.Errorf("%s as %q: the 404 names the child: %s", rt.name, as, raw)
				}
			}
		}
	}
}

func TestChildrenAndOwnLimit(t *testing.T) {
	e := setup(t)
	ids := func(as string) []string {
		t.Helper()
		out := e.want(200, as, "GET", "/api/parental/children", nil)
		list, ok := out["children"].([]any)
		if !ok {
			t.Fatalf("children as %q = %v, want a list", as, out["children"])
		}
		var got []string
		for _, c := range list {
			got = append(got, c.(map[string]any)["id"].(string))
		}
		return got
	}
	if got := ids("admin"); len(got) != 2 {
		t.Fatalf("admin sees %v, want both children", got)
	}
	if got := ids("admin-token"); len(got) != 2 {
		t.Fatalf("the admin token sees %v, want both children", got)
	}
	if got := ids("guardian"); len(got) != 1 || got[0] != uChild {
		t.Fatalf("guardian sees %v, want their child", got)
	}
	if got := ids("other-guardian"); len(got) != 1 || got[0] != uChild2 {
		t.Fatalf("other guardian sees %v, want their child", got)
	}
	for _, as := range []string{"viewer", "child"} {
		if got := ids(as); len(got) != 0 {
			t.Fatalf("%s sees %v, want no one", as, got)
		}
	}
	me := e.want(200, "child", "GET", "/api/me/parental", nil)
	if me["restricted"] != true || me["max_tier"] != float64(9) || me["unrated"] != "hide" {
		t.Fatalf("the child's own limit = %v", me)
	}
	if _, ok := me["rules"]; ok {
		t.Fatalf("the child's own limit lists rules: %v", me)
	}
	if me := e.want(200, "viewer", "GET", "/api/me/parental", nil); me["restricted"] != false || me["max_tier"] != nil {
		t.Fatalf("a viewer's own limit = %v", me)
	}
	// a child cannot read their own policy by id (their limit is at /api/me/parental)
	e.want(404, "child", "GET", "/api/users/"+uChild+"/parental", nil)
}

func TestPolicyLifecycle(t *testing.T) {
	e := setup(t)
	base := "/api/users/" + uViewer + "/parental"
	// not restricted yet: nothing to read
	e.want(404, "admin", "GET", base, nil)
	e.want(404, "admin", "GET", base+"/rules", nil)
	// only an admin makes an account restricted; a guardian of another child gets 404
	e.want(404, "guardian", "PUT", base, map[string]any{"max_tier": 9})
	e.want(403, "viewer", "PUT", base, map[string]any{"max_tier": 9})
	made := e.want(200, "admin", "PUT", base, map[string]any{"max_tier": 9})
	pol := made["policy"].(map[string]any)
	if pol["version"] != float64(1) || pol["max_tier"] != float64(9) || pol["unrated"] != "hide" || made["access"] != "admin" {
		t.Fatalf("made restricted = %v", made)
	}
	// bad bodies
	for name, body := range map[string]map[string]any{
		"no tier":      {"unrated": "hide", "version": 1},
		"tier 12":      {"max_tier": 12, "version": 1},
		"bad unrated":  {"max_tier": 9, "unrated": "tier_12", "version": 1},
		"negative ver": {"max_tier": 9, "version": -1},
	} {
		if code, out := e.do("admin", "PUT", base, body); code != 422 {
			t.Errorf("%s = %d, want 422 (%v)", name, code, out)
		}
	}
	// a stale version is refused and says so
	e.want(200, "admin", "PUT", base, map[string]any{"max_tier": 13, "version": 1})
	stale := e.want(409, "admin", "PUT", base, map[string]any{"max_tier": 16, "version": 1})
	if stale["code"] != "stale" {
		t.Fatalf("stale = %v", stale)
	}
	// an admin's account cannot be restricted
	refused := e.want(409, "admin", "PUT", "/api/users/"+uAdmin+"/parental", map[string]any{"max_tier": 9})
	if refused["code"] != "restricted_admin" || refused["type"] != parentalh.TypeRestrictedAdmin {
		t.Fatalf("restricting an admin = %v", refused)
	}
	// nor a guardian's
	refused = e.want(409, "admin", "PUT", "/api/users/"+uGuardian+"/parental", map[string]any{"max_tier": 9})
	if refused["code"] != "restricted_guardian" {
		t.Fatalf("restricting a guardian = %v", refused)
	}
	// nor a user who is not there, or an id that is none
	e.want(404, "admin", "PUT", "/api/users/00000000-0000-4000-8000-00000572ffff/parental", map[string]any{"max_tier": 9})
	e.want(404, "admin", "PUT", "/api/users/nobody/parental", map[string]any{"max_tier": 9})
	e.want(404, "admin", "GET", "/api/users/nobody/parental", nil)

	// guardians: named by an admin, shown on the policy
	gs := e.want(200, "admin", "POST", base+"/guardians", map[string]any{"guardian_id": uGuardian})
	if list := gs["guardians"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != uGuardian {
		t.Fatalf("guardians = %v", gs)
	}
	e.want(422, "admin", "POST", base+"/guardians", map[string]any{})
	e.want(422, "admin", "POST", base+"/guardians", map[string]any{"guardian_id": "00000000-0000-4000-8000-00000572ffff"})
	refused = e.want(409, "admin", "POST", base+"/guardians", map[string]any{"guardian_id": uChild})
	if refused["code"] != "restricted_guardian" {
		t.Fatalf("a restricted guardian = %v", refused)
	}
	got := e.want(200, "guardian", "GET", base, nil)
	if got["access"] != "guardian" || len(got["guardians"].([]any)) != 1 || got["signals"] == nil {
		t.Fatalf("the guardian's read = %v", got)
	}
	// the guardian changes the limit, with the version they read
	version := got["policy"].(map[string]any)["version"]
	changed := e.want(200, "guardian", "PUT", base, map[string]any{"max_tier": 0, "unrated": "tier_0", "allow_only": true, "version": version})
	if p := changed["policy"].(map[string]any); p["max_tier"] != float64(0) || p["allow_only"] != true || p["updated_by"] != uGuardian {
		t.Fatalf("the guardian's change = %v", changed)
	}
	// a guardian cannot lift, name or remove guardians
	e.want(403, "guardian", "DELETE", base, nil)
	if out := e.want(403, "guardian", "DELETE", base+"/guardians/"+uGuardian, nil); out["code"] != "admin_only" {
		t.Fatalf("a guardian removing a guardian = %v", out)
	}
	e.want(404, "admin", "DELETE", base+"/guardians/"+uOtherG, nil)
	e.want(204, "admin", "DELETE", base+"/guardians/"+uGuardian, nil)
	e.want(404, "guardian", "GET", base, nil) // no longer theirs
	// lifted: gone, and gone again is 404
	e.want(204, "admin", "DELETE", base, nil)
	e.want(404, "admin", "GET", base, nil)
	e.want(404, "admin", "DELETE", base, nil)
}

// T04: rules, and an allow on a library the child cannot open.
func TestRuleRoutes(t *testing.T) {
	e := setup(t)
	base := "/api/users/" + uChild + "/parental/rules"
	out := e.want(422, "guardian", "POST", base, map[string]any{"effect": "allow", "subject_kind": "library", "subject_id": libClosed})
	if out["code"] != "no_library_access" || out["type"] != parentalh.TypeNoLibraryAccess {
		t.Fatalf("allow on a closed library = %v", out)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE subject_id = $1`, libClosed); n != 0 {
		t.Fatal("the refused rule was stored")
	}
	rule := e.want(201, "guardian", "POST", base, map[string]any{"effect": "allow", "subject_kind": "library", "subject_id": libOpen})
	if rule["id"] == "" || rule["effect"] != "allow" || rule["created_by"] != uGuardian {
		t.Fatalf("rule = %v", rule)
	}
	exp := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	timed := e.want(201, "admin", "POST", base, map[string]any{"effect": "allow", "subject_kind": "video", "subject_id": "v-16", "expires_at": exp})
	if timed["expires_at"] == nil {
		t.Fatalf("timed rule = %v", timed)
	}
	for name, body := range map[string]map[string]any{
		"bad effect":       {"effect": "maybe", "subject_kind": "video", "subject_id": "v"},
		"bad kind":         {"effect": "block", "subject_kind": "user", "subject_id": "v"},
		"no subject":       {"effect": "block", "subject_kind": "video"},
		"already run out":  {"effect": "allow", "subject_kind": "video", "subject_id": "v", "expires_at": "2020-01-01T00:00:00Z"},
		"library not uuid": {"effect": "allow", "subject_kind": "library", "subject_id": "films"},
	} {
		if code, out := e.do("admin", "POST", base, body); code != 422 {
			t.Errorf("%s = %d, want 422 (%v)", name, code, out)
		}
	}
	list := e.want(200, "guardian", "GET", base, nil)
	if rules := list["rules"].([]any); len(rules) != 3 {
		t.Fatalf("rules = %v, want three", list)
	}
	// a rule is removed only through its own child
	e.want(404, "admin", "DELETE", "/api/users/"+uChild2+"/parental/rules/"+rule["id"].(string), nil)
	e.want(404, "other-guardian", "DELETE", base+"/"+rule["id"].(string), nil)
	e.want(204, "guardian", "DELETE", base+"/"+rule["id"].(string), nil)
	e.want(404, "guardian", "DELETE", base+"/"+rule["id"].(string), nil)
	e.want(404, "guardian", "DELETE", base+"/not-an-id", nil)
	// a rule on an account that is not restricted
	e.want(404, "admin", "POST", "/api/users/"+uViewer+"/parental/rules", map[string]any{"effect": "block", "subject_kind": "video", "subject_id": "v"})
}

// A guardian signed in through a cloud link that may only play reads their
// child's policy and changes nothing.
func TestCloudPlayGuardianReadsOnly(t *testing.T) {
	e := setup(t)
	base := "/api/users/" + uChild + "/parental"
	e.want(200, "guardian-cloud-play", "GET", base, nil)
	e.want(200, "guardian-cloud-play", "GET", base+"/rules", nil)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PUT", base, map[string]any{"max_tier": 18, "version": 1}},
		{"POST", base + "/rules", map[string]any{"effect": "allow", "subject_kind": "video", "subject_id": "v"}},
		{"DELETE", base + "/rules/" + ruleID, nil},
	} {
		out := e.want(403, "guardian-cloud-play", c.method, c.path, c.body)
		if out["type"] != parentalh.TypeCloudSession {
			t.Errorf("%s %s: type = %v, want the cloud-session refusal", c.method, c.path, out["type"])
		}
	}
	if n := e.count(`SELECT max_tier FROM parental_policies WHERE user_id = $1`, uChild); n != 9 {
		t.Fatalf("the limit became %d", n)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE user_id = $1`, uChild); n != 1 {
		t.Fatalf("the rules became %d", n)
	}
}

// The admin token (no user row) can manage; its changes are audited with no actor.
func TestAdminTokenManages(t *testing.T) {
	e := setup(t)
	base := "/api/users/" + uChild + "/parental"
	e.want(200, "admin-token", "PUT", base, map[string]any{"max_tier": 13, "version": 1})
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND actor_user_id IS NULL`,
		parental.AuditPolicySet, uChild); n != 1 {
		t.Fatalf("%d audit rows for the admin token's change, want 1", n)
	}
}
