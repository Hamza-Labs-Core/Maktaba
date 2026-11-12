package parental_test

// `until: "day_end"` on the add-rule and approve routes (Epic 57): the home, not
// the caller's clock, says when "today" ends: the next 04:00 in the home's time
// zone, the end a PIN unlock has. The value, the refusals (both ends given, an
// unknown name, a request for time) and the roles, against a real Postgres.

import (
	"testing"
	"time"
)

// dayEnd is the home's day end for both clocks used below: 04:00 on 11 March 2030
// in Riyadh.
const dayEnd = "2030-03-11T01:00:00Z"

func fieldOf(out map[string]any) string {
	errs, _ := out["errors"].([]any)
	if len(errs) != 1 {
		return ""
	}
	f, _ := errs[0].(map[string]any)["field"].(string)
	return f
}

func TestAddRuleUntilDayEnd(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.riyadh()
	base := "/api/users/" + uChild + "/parental/rules"
	rule := func(id string, more map[string]any) map[string]any {
		b := map[string]any{"effect": "allow", "subject_kind": "video", "subject_id": id}
		for k, v := range more {
			b[k] = v
		}
		return b
	}
	today := map[string]any{"until": "day_end"}

	// the roles are the route's own, whatever the body says
	e.clock = time.Date(2030, 3, 10, 12, 0, 0, 0, time.UTC) // 15:00 in Riyadh
	for i, want := range [6]int{401, 403, 403, 201, 404, 201} {
		e.reset()
		code, out := e.do(who[i], "POST", base, rule("v-day", today))
		if code != want {
			t.Errorf("add a rule until the day ends as %q = %d, want %d (%v)", who[i], code, want, out)
		}
		if code == 201 && out["expires_at"] != dayEnd {
			t.Errorf("as %q the rule ends at %v, want %s (04:00 the next morning at home)", who[i], out["expires_at"], dayEnd)
		}
		if n := e.count(`SELECT count(*) FROM parental_rules WHERE subject_id = 'v-day'`); (n == 1) != (want == 201) {
			t.Errorf("as %q: %d rules stored", who[i], n)
		}
	}

	// after midnight and before 04:00 the day ends the same morning
	e.reset()
	e.clock = time.Date(2030, 3, 10, 23, 30, 0, 0, time.UTC) // 02:30 in Riyadh
	if out := e.want(201, "guardian", "POST", base, rule("v-night", today)); out["expires_at"] != dayEnd {
		t.Errorf("a rule added at 02:30 ends at %v, want %s", out["expires_at"], dayEnd)
	}
	if got := e.text(`SELECT to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM parental_rules WHERE subject_id = 'v-night'`); got != dayEnd {
		t.Errorf("stored end = %s, want %s", got, dayEnd)
	}

	// one end or the other, and only an end the home knows
	for name, more := range map[string]map[string]any{
		"both ends":       {"until": "day_end", "expires_at": "2030-03-12T00:00:00Z"},
		"an unknown name": {"until": "week_end"},
	} {
		code, out := e.do("admin", "POST", base, rule("v-bad", more))
		if code != 422 || fieldOf(out) != "until" {
			t.Errorf("%s = %d on field %q, want 422 on until (%v)", name, code, fieldOf(out), out)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE subject_id = 'v-bad'`); n != 0 {
		t.Errorf("a refused rule was stored (%d)", n)
	}
	// expires_at alone is as before
	if out := e.want(201, "admin", "POST", base, rule("v-at", map[string]any{"expires_at": "2099-01-01T00:00:00Z"})); out["expires_at"] != "2099-01-01T00:00:00Z" {
		t.Errorf("a rule with expires_at = %v", out)
	}
}

func TestApproveUntilDayEnd(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	e.riyadh()
	e.clock = time.Date(2030, 3, 10, 12, 0, 0, 0, time.UTC) // 15:00 in Riyadh
	today := map[string]any{"until": "day_end"}
	asked := func(kind, id string) string {
		t.Helper()
		return e.want(201, "child", "POST", "/api/me/parental/requests", ask(kind, id))["request"].(map[string]any)["id"].(string)
	}
	id := asked("video", vidOpen)
	approve := "/api/parental/requests/" + id + "/approve"

	// refused bodies leave the request waiting
	for name, body := range map[string]map[string]any{
		"both ends":       {"until": "day_end", "expires_at": "2030-03-12T00:00:00Z"},
		"an unknown name": {"until": "tomorrow"},
	} {
		code, out := e.do("guardian", "POST", approve, body)
		if code != 422 || fieldOf(out) != "until" {
			t.Errorf("approving with %s = %d on field %q, want 422 on until (%v)", name, code, fieldOf(out), out)
		}
	}
	// the roles are the route's own: no one, a viewer, the child, another child's guardian
	for as, want := range map[string]int{"": 401, "viewer": 403, "child": 403, "other-guardian": 404} {
		if code, out := e.do(as, "POST", approve, today); code != want {
			t.Errorf("approve until the day ends as %q = %d, want %d (%v)", as, code, want, out)
		}
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, id); st != "pending" {
		t.Fatalf("after the refusals the request is %s, want pending", st)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE subject_id = $1`, vidOpen); n != 0 {
		t.Fatalf("a refused approval added a rule (%d)", n)
	}

	// the guardian, then the admin: the allow rule ends when the home's day does
	out := e.want(200, "guardian", "POST", approve, today)
	if r := out["rule"].(map[string]any); r["effect"] != "allow" || r["subject_id"] != vidOpen || r["expires_at"] != dayEnd {
		t.Errorf("rule = %v, want it to end at %s", r, dayEnd)
	}
	e.clock = time.Date(2030, 3, 10, 23, 30, 0, 0, time.UTC) // 02:30 in Riyadh
	out = e.want(200, "admin", "POST", "/api/parental/requests/"+asked("video", vidOpen2)+"/approve", today)
	if r := out["rule"].(map[string]any); r["expires_at"] != dayEnd {
		t.Errorf("approved at 02:30 the rule ends at %v, want %s", r["expires_at"], dayEnd)
	}

	// a request for more time has no end to name
	timeID := asked("time", "30")
	code, body := e.do("guardian", "POST", "/api/parental/requests/"+timeID+"/approve", today)
	if code != 422 || fieldOf(body) != "until" {
		t.Errorf("until on a request for time = %d on field %q, want 422 on until (%v)", code, fieldOf(body), body)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, timeID); st != "pending" {
		t.Errorf("the request for time is %s, want pending", st)
	}
}
