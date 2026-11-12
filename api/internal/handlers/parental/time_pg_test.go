package parental_test

// 57.14's routes against a real migrated Postgres (slot 0223): the role of every
// route (T06), a grant through its route (T05), the rules as they are kept, and
// the account's own countdown.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// timePush is streaming as the keeper sees it: it counts what it is told.
type timePush struct {
	mu     sync.Mutex
	limits int
	leases int
}

func (p *timePush) SetTimeLimit(_ context.Context, _, _ string, lim *parental.TimeLimit) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limits++
	if lim != nil && lim.LeaseUntil != nil {
		p.leases++
	}
	return nil
}

func (p *timePush) ClearTimeLimits(context.Context, string) error { return nil }

func (p *timePush) SetTimeKept(context.Context, string, bool) error { return nil }

func (p *timePush) TimeServed(context.Context, string, string, string, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

func (e *env) mountTime(r chi.Router, store *parental.Store) {
	e.push = &timePush{}
	cache := parental.NewCache(store)
	e.keeper = parental.NewTimeKeeper(cache, e.push)
	(&parentalh.Handler{Store: store, Cache: cache, Time: e.keeper}).MountTime(r)
	// an approved request for more time tells the same keeper (requests_time_pg_test.go)
	if ask := e.ask; ask != nil {
		ask.Time = e.keeper
	}
}

// timeWho is the role table's callers: no one, a viewer, the restricted child, the
// child's guardian, another child's guardian, an admin, the guardian signed in
// through a cloud link that may only play, and the admin token.
var timeWho = []string{"", "viewer", "child", "guardian", "other-guardian", "admin", "guardian-cloud-play", "admin-token"}

// riyadh puts the home in Riyadh at 17:00 on Tuesday 10 November 2026.
func (e *env) riyadh() *time.Location {
	e.t.Helper()
	loc, err := time.LoadLocation("Asia/Riyadh")
	if err != nil {
		e.t.Skip("no time zone data")
	}
	e.exec(`INSERT INTO app_settings (key, value) VALUES ('home.timezone', '"Asia/Riyadh"')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	e.clock = time.Date(2026, 11, 10, 17, 0, 0, 0, loc)
	return loc
}

// T06: every time route, as every caller.
func TestTimeRoles(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.riyadh()
	base := "/api/parental/users/" + uChild
	rule := map[string]any{"rules": []map[string]any{{"days": 127, "daily_minutes": 60}}}
	routes := []struct {
		name, method, path string
		body               any
		want               [8]int
	}{
		{"read the time rules", "GET", base + "/time-rules", nil, [8]int{401, 403, 403, 200, 404, 200, 200, 200}},
		{"set the time rules", "PUT", base + "/time-rules", rule, [8]int{401, 403, 403, 200, 404, 200, 403, 200}},
		{"read the time used", "GET", base + "/time-usage", nil, [8]int{401, 403, 403, 200, 404, 200, 200, 200}},
		{"give more time", "POST", base + "/time-grants", map[string]any{"minutes": 30}, [8]int{401, 403, 403, 201, 404, 201, 403, 201}},
		{"own time", "GET", "/api/me/parental/time", nil, [8]int{401, 200, 200, 200, 200, 200, 200, 200}},
	}
	state := func() [2]int {
		return [2]int{e.count(`SELECT count(*) FROM parental_time_rules`), e.count(`SELECT count(*) FROM parental_time_grants`)}
	}
	for _, rt := range routes {
		for i, as := range timeWho {
			e.reset() // the policies go, and the rules and grants with them
			before := state()
			code, body := e.do(as, rt.method, rt.path, rt.body)
			if code != rt.want[i] {
				t.Errorf("%s as %q = %d, want %d (%v)", rt.name, as, code, rt.want[i], body)
				continue
			}
			if code >= 400 && state() != before {
				t.Errorf("%s as %q was refused (%d) and still changed something", rt.name, as, code)
			}
			if as == "child" && code == 403 && body["code"] != "restricted" {
				t.Errorf("%s as the child: code = %v, want restricted", rt.name, body["code"])
			}
			if as == "guardian-cloud-play" && code == 403 && body["type"] != parentalh.TypeCloudSession {
				t.Errorf("%s through a play-only cloud sign-in: type = %v", rt.name, body["type"])
			}
		}
	}
	// the other guardian's own child: the same routes answer them
	e.reset()
	e.want(200, "other-guardian", "GET", "/api/parental/users/"+uChild2+"/time-rules", nil)
	e.want(201, "other-guardian", "POST", "/api/parental/users/"+uChild2+"/time-grants", map[string]any{"minutes": 15})
	// an account that is not restricted has no time rules: 404, for an admin too
	for _, rt := range routes[:4] {
		path := "/api/parental/users/" + uViewer + rt.path[len(base):]
		if code, body := e.do("admin", rt.method, path, rt.body); code != 404 {
			t.Errorf("%s of an unrestricted account = %d, want 404 (%v)", rt.name, code, body)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_time_rules WHERE user_id = $1`, uViewer); n != 0 {
		t.Errorf("%d time rules on an unrestricted account", n)
	}
}

// The rules as the routes keep them: a PUT replaces them all, a bad rule is 422
// and changes nothing, and streaming is told of a change at once.
func TestTimeRulesRoute(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.riyadh()
	path := "/api/parental/users/" + uChild + "/time-rules"
	out := e.want(200, "guardian", "GET", path, nil)
	if rules := out["rules"].([]any); len(rules) != 0 || out["time_zone"] != "Asia/Riyadh" {
		t.Fatalf("no rules yet: %v", out)
	}
	out = e.want(200, "guardian", "PUT", path, map[string]any{"rules": []map[string]any{
		{"days": 31, "slot_start": "16:00", "slot_end": "19:30"},
		{"days": 127, "daily_minutes": 90, "library_id": libOpen},
	}})
	if rules := out["rules"].([]any); len(rules) != 2 {
		t.Fatalf("two rules kept: %v", out)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1`, parental.AuditTimeRulesSet); n != 1 {
		t.Errorf("%d audit rows for the change", n)
	}
	for name, body := range map[string]any{
		"no rules key":      map[string]any{},
		"no day":            map[string]any{"rules": []map[string]any{{"days": 0, "daily_minutes": 60}}},
		"half a slot":       map[string]any{"rules": []map[string]any{{"days": 1, "slot_start": "16:00"}}},
		"a rule of nothing": map[string]any{"rules": []map[string]any{{"days": 1}}},
		"minutes below 0":   map[string]any{"rules": []map[string]any{{"days": 1, "daily_minutes": -1}}},
		"not a library":     map[string]any{"rules": []map[string]any{{"days": 1, "daily_minutes": 5, "library_id": "x"}}},
	} {
		if code, got := e.do("guardian", "PUT", path, body); code != 422 {
			t.Errorf("%s = %d, want 422 (%v)", name, code, got)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_time_rules WHERE user_id = $1`, uChild); n != 2 {
		t.Fatalf("a refused change left %d rules, want 2", n)
	}
	// an empty list removes every rule
	out = e.want(200, "admin", "PUT", path, map[string]any{"rules": []map[string]any{}})
	if rules := out["rules"].([]any); len(rules) != 0 || e.count(`SELECT count(*) FROM parental_time_rules`) != 0 {
		t.Fatalf("the rules are removed: %v", out)
	}
}

// T05 through the route: a grant opens today for its minutes and no other day; the
// child's own countdown shows it; asking the countdown renews no stream's lease.
func TestTimeGrantRoute(t *testing.T) {
	t.Parallel()
	e := setup(t)
	loc := e.riyadh()
	base := "/api/parental/users/" + uChild
	e.want(200, "guardian", "PUT", base+"/time-rules", map[string]any{"rules": []map[string]any{{"days": 127, "daily_minutes": 60}}})
	e.exec(`INSERT INTO parental_time_usage (user_id, day, seconds) VALUES ($1, '2026-11-10', 3600)`, uChild)

	out := e.want(200, "child", "GET", "/api/me/parental/time", nil)
	if out["allowed"] != false || out["limited"] != true || out["reason"] != parental.ReasonLimitReached ||
		out["remaining_seconds"] != float64(0) || out["next_allowed_at"] != "2026-11-11T01:00:00Z" {
		t.Fatalf("the limit is reached: %v", out)
	}
	for _, m := range []int{0, -1, parental.MaxGrantMinutes + 1} {
		if code, got := e.do("guardian", "POST", base+"/time-grants", map[string]any{"minutes": m}); code != 422 {
			t.Errorf("a grant of %d minutes = %d, want 422 (%v)", m, code, got)
		}
	}
	g := e.want(201, "guardian", "POST", base+"/time-grants", map[string]any{"minutes": 30})
	if g["day"] != "2026-11-10" || g["minutes"] != float64(30) || g["granted_by"] != uGuardian {
		t.Fatalf("the grant: %v", g)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE child_id = $1 AND kind = 'time_granted'
		AND subject_id = '30' AND actor_id = $2`, uChild, uGuardian); n != 1 {
		t.Errorf("%d time_granted events in the child's activity, want 1", n)
	}
	leases := e.push.leases
	out = e.want(200, "child", "GET", "/api/me/parental/time", nil)
	if out["allowed"] != true || out["remaining_seconds"] != float64(1800) {
		t.Fatalf("after the grant: %v", out)
	}
	if e.push.leases != leases {
		t.Fatal("reading the countdown renewed a stream's lease")
	}
	usage := e.want(200, "guardian", "GET", base+"/time-usage?from=2026-11-10&to=2026-11-10", nil)
	if rows, grants := usage["usage"].([]any), usage["grants"].([]any); len(rows) != 1 || len(grants) != 1 || usage["today"] != "2026-11-10" {
		t.Fatalf("the day's count: %v", usage)
	}
	if code, got := e.do("guardian", "GET", base+"/time-usage?from=yesterday", nil); code != 422 {
		t.Errorf("a bad day = %d, want 422 (%v)", code, got)
	}
	// that day only: past the day line the grant is gone and the day has its own hour
	e.clock = time.Date(2026, 11, 11, 4, 0, 0, 0, loc)
	out = e.want(200, "child", "GET", "/api/me/parental/time", nil)
	if out["allowed"] != true || out["remaining_seconds"] != float64(3600) {
		t.Fatalf("the next day: %v", out)
	}
	// someone with no rules is not limited, and has no countdown
	out = e.want(200, "viewer", "GET", "/api/me/parental/time", nil)
	if out["allowed"] != true || out["limited"] != false || out["remaining_seconds"] != nil {
		t.Fatalf("an unrestricted account: %v", out)
	}
	if code, got := e.do("child", "GET", "/api/me/parental/time?library_id=nope", nil); code != 422 {
		t.Errorf("a bad library id = %d, want 422 (%v)", code, got)
	}
}
