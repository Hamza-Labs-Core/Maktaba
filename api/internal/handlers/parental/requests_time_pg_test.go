package parental_test

// The `time` kind of an Ask a parent request (Epic 57 stories 57.06 and 57.14)
// against a real migrated Postgres (slot 0223): a restricted child asks for more
// minutes today, and a guardian's approval is a time grant, not a rule. The role
// of every route that changed is tried as no one, a viewer, the restricted child,
// that child's guardian, another child's guardian and an admin.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const timeReqID = "00000000-0000-4000-8000-0000572000b1"

// waitingTime puts one waiting request of the child for 30 minutes in, asked at
// the env's clock.
func (e *env) waitingTime() {
	e.t.Helper()
	at := e.clock
	if at.IsZero() {
		at = time.Now()
	}
	e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, created_at) VALUES ($1, $2, 'time', '30', $3)`,
		timeReqID, uChild, at.UTC())
}

func TestAskTimeRoles(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.riyadh()
	routes := []struct {
		name, method, path string
		body               any
		seeded             bool
		want               [6]int
	}{
		{"ask for time", "POST", "/api/me/parental/requests", ask("time", "30"), false, [6]int{401, 403, 201, 403, 403, 403}},
		{"ask for time again", "POST", "/api/me/parental/requests", ask("time", "60"), true, [6]int{401, 403, 200, 403, 403, 403}},
		{"list requests", "GET", "/api/parental/requests?status=pending", nil, true, [6]int{401, 403, 403, 200, 200, 200}},
		{"approve", "POST", "/api/parental/requests/" + timeReqID + "/approve", nil, true, [6]int{401, 403, 403, 200, 404, 200}},
		{"approve with minutes", "POST", "/api/parental/requests/" + timeReqID + "/approve", map[string]any{"minutes": 20}, true,
			[6]int{401, 403, 403, 200, 404, 200}},
		{"deny", "POST", "/api/parental/requests/" + timeReqID + "/deny", nil, true, [6]int{401, 403, 403, 200, 404, 200}},
		{"activity", "GET", "/api/users/" + uChild + "/parental/activity", nil, true, [6]int{401, 404, 404, 200, 404, 200}},
	}
	state := func() string {
		return fmt.Sprint(
			e.count(`SELECT count(*) FROM parental_requests`),
			e.count(`SELECT count(*) FROM parental_requests WHERE status = 'pending'`),
			e.count(`SELECT count(*) FROM parental_time_grants`),
			e.count(`SELECT count(*) FROM parental_rules`),
			e.count(`SELECT count(*) FROM parental_events`))
	}
	for _, rt := range routes {
		for i, as := range who {
			e.askReset()
			if rt.seeded {
				e.waitingTime()
				e.exec(`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, at) VALUES ($1, 'asked', 'time', '30', $2)`,
					uChild, e.clock.UTC())
			}
			before := state()
			code, out := e.do(as, rt.method, rt.path, rt.body)
			if code != rt.want[i] {
				t.Errorf("%s as %q = %d, want %d (%v)", rt.name, as, code, rt.want[i], out)
			}
			if code >= 400 && state() != before {
				t.Errorf("%s as %q was refused (%d) but changed something", rt.name, as, code)
			}
			if as == "child" && code == 403 && out["code"] != "restricted" {
				t.Errorf("%s as the child: code = %v, want restricted", rt.name, out["code"])
			}
		}
	}
	// the other guardian's list holds no request of this child; the admin's does
	e.askReset()
	e.waitingTime()
	if out := e.want(200, "other-guardian", "GET", "/api/parental/requests", nil); len(out["requests"].([]any)) != 0 {
		t.Errorf("another child's guardian sees the request for time: %v", out)
	}
	if out := e.want(200, "admin", "GET", "/api/parental/requests", nil); len(out["requests"].([]any)) != 1 {
		t.Errorf("the admin does not see the request for time: %v", out)
	}
	// a sign-in through the cloud that may only play gives no time
	e.want(403, "guardian-cloud-play", "POST", "/api/parental/requests/"+timeReqID+"/approve", nil)
	if n := e.count(`SELECT count(*) FROM parental_time_grants`); n != 0 {
		t.Errorf("%d grants after a refused approval", n)
	}
}

// Ask, ask twice, bad minutes; approving gives the time (the minutes asked for, or
// the guardian's own number), tells the time keeper, and shows in the list and
// the activity.
func TestAskTimeAndApprove(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.riyadh() // Tuesday 10 November 2026, 17:00 in Riyadh (14:00 UTC)
	ctx := context.Background()
	base := "/api/parental/users/" + uChild
	e.want(200, "guardian", "PUT", base+"/time-rules", map[string]any{"rules": []map[string]any{{"days": 127, "daily_minutes": 60}}})
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, '2026-11-10', $2, 3600)`, uChild, libOpen)
	if out := e.want(200, "child", "GET", "/api/me/parental/time", nil); out["allowed"] != false {
		t.Fatalf("the limit is reached: %v", out)
	}

	for _, bad := range []string{"", "0", "-5", "481", "abc", "030", "+30", "30.5", " 30"} {
		if code, out := e.do("child", "POST", "/api/me/parental/requests", ask("time", bad)); code != 422 {
			t.Errorf("asking for %q minutes = %d, want 422 (%v)", bad, code, out)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_requests`); n != 0 {
		t.Fatalf("%d requests after only bad ones", n)
	}

	e.notices = nil
	first := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "30"))["request"].(map[string]any)
	if first["subject_kind"] != "time" || first["subject_id"] != "30" || first["minutes"] != float64(30) ||
		first["status"] != "pending" || first["expires_at"] != "2026-11-11T01:00:00Z" {
		t.Fatalf("the request = %v, want 30 minutes, waiting until the day ends (04:00 in Riyadh)", first)
	}
	if len(e.notices) != 1 || e.notices[0].kind != "parental_request" {
		t.Errorf("notices = %v, want one to the guardian", e.notices)
	}
	// asking again, for the same or another number, is the waiting request
	for _, m := range []string{"30", "60"} {
		again := e.want(200, "child", "POST", "/api/me/parental/requests", ask("time", m))["request"].(map[string]any)
		if again["id"] != first["id"] || again["subject_id"] != "30" {
			t.Errorf("asked again for %s: %v, want the waiting request %v", m, again, first["id"])
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_requests`); n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}
	if len(e.notices) != 1 || e.count(`SELECT count(*) FROM parental_events WHERE kind = 'asked'`) != 1 {
		t.Errorf("asking again told the guardians or the activity again")
	}
	id := first["id"].(string)

	// the list shows it, with no subject to hide or load
	for _, as := range []string{"guardian", "admin"} {
		list := e.want(200, as, "GET", "/api/parental/requests?status=pending", nil)["requests"].([]any)
		if len(list) != 1 {
			t.Fatalf("%s's list = %v", as, list)
		}
		it := list[0].(map[string]any)
		s := it["subject"].(map[string]any)
		if it["subject_kind"] != "time" || it["minutes"] != float64(30) || s["kind"] != "time" || s["id"] != "30" ||
			s["removed"] != nil || s["hidden"] != nil || it["expires_at"] != "2026-11-11T01:00:00Z" {
			t.Errorf("%s's list item = %v", as, it)
		}
	}

	// what an approval may say
	for name, body := range map[string]any{
		"an expiry":       map[string]any{"expires_at": "2026-11-12T00:00:00Z"},
		"no minutes":      map[string]any{"minutes": 0},
		"too many":        map[string]any{"minutes": 481},
		"negative":        map[string]any{"minutes": -1},
		"expiry and time": map[string]any{"minutes": 10, "expires_at": "2026-11-12T00:00:00Z"},
	} {
		if code, out := e.do("guardian", "POST", "/api/parental/requests/"+id+"/approve", body); code != 422 {
			t.Errorf("approving with %s = %d, want 422 (%v)", name, code, out)
		}
	}
	if e.count(`SELECT count(*) FROM parental_time_grants`) != 0 || e.count(`SELECT count(*) FROM parental_requests WHERE status = 'pending'`) != 1 {
		t.Fatal("a refused approval gave time or closed the request")
	}
	// minutes belong to a request for time only
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))
	vid := e.text(`SELECT id::text FROM parental_requests WHERE subject_kind = 'video'`)
	if code, out := e.do("guardian", "POST", "/api/parental/requests/"+vid+"/approve", map[string]any{"minutes": 30}); code != 422 {
		t.Errorf("minutes on a video's request = %d, want 422 (%v)", code, out)
	}
	if e.count(`SELECT count(*) FROM parental_rules WHERE subject_kind = 'video' AND subject_id = $1`, vidOpen) != 0 {
		t.Error("a refused approval added a rule")
	}

	// a stream of the child is running: the keeper knows its library
	if err := e.keeper.Opened(ctx, uChild, libOpen, vidOpen, "s-time-1"); err != nil {
		t.Fatalf("opening a session: %v", err)
	}
	told := e.push.limits
	rules := e.count(`SELECT count(*) FROM parental_rules`)
	out := e.want(200, "guardian", "POST", "/api/parental/requests/"+id+"/approve", map[string]any{"minutes": 45})
	if r := out["request"].(map[string]any); r["status"] != "approved" || r["decided_by"] != uGuardian || r["minutes"] != float64(30) {
		t.Errorf("approved request = %v", r)
	}
	g, _ := out["grant"].(map[string]any)
	if g == nil || g["minutes"] != float64(45) || g["day"] != "2026-11-10" || g["granted_by"] != uGuardian || g["user_id"] != uChild {
		t.Fatalf("grant = %v, want the guardian's 45 minutes for 2026-11-10", out["grant"])
	}
	if out["rule"] != nil || e.count(`SELECT count(*) FROM parental_rules`) != rules {
		t.Error("approving a request for time added a rule")
	}
	if e.push.limits <= told {
		t.Error("the time keeper was not told of the grant")
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants WHERE user_id = $1 AND minutes = 45`, uChild); n != 1 {
		t.Errorf("%d grants, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2`, parental.AuditTimeGranted, uChild); n != 1 {
		t.Errorf("audit rows for the grant = %d, want 1 (the same path as a grant given by hand)", n)
	}
	if mine := e.want(200, "child", "GET", "/api/me/parental/time", nil); mine["allowed"] != true || mine["remaining_seconds"] != float64(2700) {
		t.Fatalf("after the approval: %v, want 45 minutes left", mine)
	}
	// answered once
	if out := e.want(409, "admin", "POST", "/api/parental/requests/"+id+"/approve", nil); out["code"] != "request_decided" {
		t.Errorf("second answer: %v", out)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants`); n != 1 {
		t.Errorf("%d grants after a second answer", n)
	}
	// with no minutes in the body, the minutes asked for
	e.clock = e.clock.Add(time.Minute)
	next := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "15"))["request"].(map[string]any)
	out = e.want(200, "admin", "POST", "/api/parental/requests/"+next["id"].(string)+"/approve", nil)
	if g, _ := out["grant"].(map[string]any); g == nil || g["minutes"] != float64(15) || g["granted_by"] != uAdmin {
		t.Errorf("grant = %v, want the 15 minutes asked for", out["grant"])
	}

	// the lists and the activity show it all, and load no subject for it
	list := e.want(200, "guardian", "GET", "/api/parental/requests?status=approved", nil)["requests"].([]any)
	if len(list) != 2 {
		t.Errorf("approved list = %v", list)
	}
	events := e.want(200, "guardian", "GET", "/api/users/"+uChild+"/parental/activity", nil)["events"].([]any)
	got := map[string]int{}
	for _, raw := range events {
		ev := raw.(map[string]any)
		if ev["subject"] != nil && ev["subject"].(map[string]any)["kind"] == "time" {
			t.Errorf("a time event carries a subject: %v", ev)
		}
		if m, ok := ev["minutes"].(float64); ok {
			got[fmt.Sprintf("%s:%d", ev["kind"], int(m))]++
		}
	}
	for _, want := range []string{"asked:30", "approved:30", "time_granted:45", "asked:15", "approved:15", "time_granted:15"} {
		if got[want] != 1 {
			t.Errorf("activity has %d of %s, want 1 (%v)", got[want], want, got)
		}
	}
	for _, kind := range []string{"asked", "approved", "time_granted"} {
		e.want(200, "admin", "GET", "/api/users/"+uChild+"/parental/activity?kind="+kind, nil)
	}
}

// Denying gives nothing.
func TestAskTimeDenied(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.riyadh()
	req := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "60"))["request"].(map[string]any)
	told := e.push.limits
	out := e.want(200, "guardian", "POST", "/api/parental/requests/"+req["id"].(string)+"/deny", nil)
	if r := out["request"].(map[string]any); r["status"] != "denied" || r["decided_by"] != uGuardian || out["grant"] != nil {
		t.Errorf("denied = %v", out)
	}
	if e.count(`SELECT count(*) FROM parental_time_grants`) != 0 || e.push.limits != told {
		t.Error("a denial gave time, or told the time keeper")
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'denied' AND subject_kind = 'time' AND actor_id = $1`, uGuardian); n != 1 {
		t.Errorf("denied events = %d", n)
	}
	if out := e.want(409, "guardian", "POST", "/api/parental/requests/"+req["id"].(string)+"/approve", nil); out["code"] != "request_decided" {
		t.Errorf("approving a denied request: %v", out)
	}
	// denied, the child may ask again the same day
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "60"))
}

// A request for more time is over when its home day ends (04:00 home time): it is
// listed as expired, approving it gives nothing, the loop closes it, and the child
// can ask again for the new day.
func TestAskTimeExpiresAtDayEnd(t *testing.T) {
	e := setup(t)
	e.askReset()
	loc := e.riyadh()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	req := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "30"))["request"].(map[string]any)
	id := req["id"].(string)
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen)) // waits seven days, as before

	// 03:59 the next morning is still the same home day
	e.clock = time.Date(2026, 11, 11, 3, 59, 0, 0, loc)
	var swept time.Time
	router.ParentalActivityOnce(ctx, e.store, quiet, &swept)
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, id); st != "pending" {
		t.Fatalf("before the day line the request is %s", st)
	}
	if list := e.want(200, "guardian", "GET", "/api/parental/requests", nil)["requests"].([]any); len(list) != 2 {
		t.Fatalf("waiting before the day line = %v", list)
	}

	// at 04:00 the day is over
	e.clock = time.Date(2026, 11, 11, 4, 0, 0, 0, loc)
	list := e.want(200, "guardian", "GET", "/api/parental/requests", nil)["requests"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["subject_kind"] != "video" {
		t.Fatalf("waiting after the day line = %v, want the video's request only", list)
	}
	list = e.want(200, "guardian", "GET", "/api/parental/requests?status=expired", nil)["requests"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != id || list[0].(map[string]any)["status"] != "expired" {
		t.Fatalf("expired after the day line = %v", list)
	}
	if out := e.want(410, "guardian", "POST", "/api/parental/requests/"+id+"/approve", nil); out["code"] != "request_expired" {
		t.Errorf("approving yesterday's request: %v", out)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants`); n != 0 {
		t.Fatalf("yesterday's request gave %d grants", n)
	}

	// the loop closes it, and leaves the video's request alone
	e.exec(`UPDATE parental_requests SET status = 'pending', decided_at = NULL WHERE id = $1`, id)
	router.ParentalActivityOnce(ctx, e.store, quiet, &swept)
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, id); st != "expired" {
		t.Errorf("after the loop the request is %s, want expired", st)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE subject_kind = 'video'`); st != "pending" {
		t.Errorf("the video's request is %s, want pending", st)
	}

	// asking closes yesterday's too, and starts today's
	e.exec(`UPDATE parental_requests SET status = 'pending', decided_at = NULL WHERE id = $1`, id)
	today := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "15"))["request"].(map[string]any)
	if today["id"] == id || today["expires_at"] != "2026-11-12T01:00:00Z" {
		t.Errorf("today's request = %v", today)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, id); st != "expired" {
		t.Errorf("yesterday's request is %s after asking again, want expired", st)
	}
	// a request for time with no subject is never closed as one whose subject is gone
	router.ParentalActivityOnce(ctx, e.store, quiet, &swept)
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, today["id"]); st != "pending" {
		t.Errorf("today's request is %s after the loop, want pending", st)
	}
}

// Story 57.14: a guardian gives more time with their PIN on the child's device.
// A right PIN is a time grant for the minutes typed, no rule, and it answers the
// waiting request for time; a wrong PIN or bad minutes give nothing.
func TestUnlockTime(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.riyadh()
	base := "/api/parental/users/" + uChild
	e.want(200, "guardian", "PUT", base+"/time-rules", map[string]any{"rules": []map[string]any{{"days": 127, "daily_minutes": 60}}})
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, '2026-11-10', $2, 3600)`, uChild, libOpen)
	if out := e.want(200, "child", "GET", "/api/me/parental/time", nil); out["allowed"] != false {
		t.Fatalf("the limit is reached: %v", out)
	}
	waiting := e.want(201, "child", "POST", "/api/me/parental/requests", ask("time", "30"))["request"].(map[string]any)["id"].(string)
	timeUnlock := func(pin, minutes string) map[string]any {
		return map[string]any{"guardian_id": uGuardian, "pin": pin, "subject_kind": "time", "subject_id": minutes}
	}

	// Review finding 5: the child's device unlocks 15, 30 or 60 minutes and nothing
	// else. A changed client that sends 480 beside a right PIN gets no grant.
	for _, bad := range []string{"0", "481", "abc", "030", "1", "45", "120", "480"} {
		e.want(422, "child", "POST", "/api/me/parental/unlock", timeUnlock(thePIN, bad))
		// asking is held to the same three
		e.want(422, "child", "POST", "/api/me/parental/requests", ask("time", bad))
	}
	e.want(403, "child", "POST", "/api/me/parental/unlock", timeUnlock("000000", "60"))
	if n := e.count(`SELECT count(*) FROM parental_time_grants`); n != 0 {
		t.Fatalf("%d grants after bad minutes and a wrong PIN, want 0", n)
	}
	if out := e.want(200, "child", "GET", "/api/me/parental/time", nil); out["allowed"] != false {
		t.Fatalf("a wrong PIN gave time: %v", out)
	}

	rulesBefore := e.count(`SELECT count(*) FROM parental_rules WHERE user_id = $1`, uChild)
	out := e.want(200, "child", "POST", "/api/me/parental/unlock", timeUnlock(thePIN, "60"))
	grant, _ := out["grant"].(map[string]any)
	if grant == nil || grant["minutes"] != float64(60) || grant["granted_by"] != uGuardian || out["rule"] != nil {
		t.Fatalf("the unlock = %v, want a grant of 60 minutes by the guardian and no rule", out)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE user_id = $1`, uChild); n != rulesBefore {
		t.Errorf("%d rules after a time unlock, want the %d there were", n, rulesBefore)
	}
	if st := e.text(`SELECT status || ':' || decided_by::text FROM parental_requests WHERE id = $1`, waiting); st != "approved:"+uGuardian {
		t.Errorf("the waiting request = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE child_id = $1 AND kind = 'time_granted' AND actor_id = $2`, uChild, uGuardian); n != 1 {
		t.Errorf("time_granted events = %d, want 1", n)
	}
	if now := e.want(200, "child", "GET", "/api/me/parental/time", nil); now["allowed"] != true {
		t.Errorf("after the grant: %v, want allowed", now)
	}
	// only a restricted account's own device unlocks
	for _, who := range []string{"viewer", "guardian", "admin"} {
		if code, _ := e.do(who, "POST", "/api/me/parental/unlock", timeUnlock(thePIN, "15")); code != 403 {
			t.Errorf("%s unlocking time = %d, want 403", who, code)
		}
	}
	if code, _ := e.do("", "POST", "/api/me/parental/unlock", timeUnlock(thePIN, "15")); code != 401 {
		t.Errorf("no one unlocking time = %d, want 401", code)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants`); n != 1 {
		t.Errorf("%d grants at the end, want 1", n)
	}
	// a guardian's own sign-in still gives any number from 1 to 480
	e.want(201, "guardian", "POST", base+"/time-grants", map[string]any{"minutes": 480})
	e.want(422, "guardian", "POST", base+"/time-grants", map[string]any{"minutes": 481})
}
