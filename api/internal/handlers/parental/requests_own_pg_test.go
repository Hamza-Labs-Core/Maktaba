package parental_test

// GET /api/me/parental/requests (Epic 57 story 57.06): a restricted account reads
// back what became of the requests it made. Its own only, by id only, seven days
// back and fifty at most.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

const ownPath = "/api/me/parental/requests"

// ownList is the child's list as the route answers it.
func (e *env) ownList() []map[string]any {
	e.t.Helper()
	raw, ok := e.want(200, "child", "GET", ownPath, nil)["requests"].([]any)
	if !ok {
		e.t.Fatal("requests is not a list")
	}
	out := make([]map[string]any, len(raw))
	for i := range raw {
		out[i] = raw[i].(map[string]any)
	}
	return out
}

func keysOf(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// Every caller: only a restricted account reads the list, exactly as only it asks.
func TestOwnRequestsRoles(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'video', $3)`, reqID, uChild, vidOpen)
	callers := append(append([]string{}, who...), "guardian-cloud-play", "admin-token")
	for i, want := range []int{401, 403, 200, 403, 403, 403, 403, 403} {
		// the same answer as asking gives each caller (201 where this gives 200)
		code, out := e.do(callers[i], "GET", ownPath, nil)
		if code != want {
			t.Errorf("GET %s as %q = %d, want %d (%v)", ownPath, callers[i], code, want, out)
		}
		if code == 403 && out["code"] != "not_restricted" {
			t.Errorf("GET %s as %q: code = %v, want not_restricted", ownPath, callers[i], out["code"])
		}
		if code != 200 && out["requests"] != nil {
			t.Errorf("GET %s as %q was refused and still lists requests", ownPath, callers[i])
		}
	}
	// reading changes nothing
	if n := e.count(`SELECT count(*) FROM parental_requests WHERE status = 'pending'`); n != 1 {
		t.Errorf("%d pending requests after the reads, want 1", n)
	}
	if n := e.count(`SELECT count(*) FROM parental_events`); n != 0 {
		t.Errorf("%d events after the reads, want 0", n)
	}
}

// The child reads its own requests and no one else's; a second child's are never
// listed, and the second child reads only its own.
func TestOwnRequestsAreTheCallersOnly(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	const other = "00000000-0000-4000-8000-0000572000c1"
	e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'video', $3)`, reqID, uChild, vidOpen)
	e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, status, decided_by, decided_at)
		VALUES ($1, $2, 'video', $3, 'approved', $4, now())`, other, uChild2, vidOpen, uOtherG)

	list := e.ownList()
	if len(list) != 1 || list[0]["id"] != reqID {
		t.Fatalf("the child's list = %v, want only its own request", list)
	}
	ctx := context.Background()
	two, err := e.store.OwnRequests(ctx, uChild2)
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 1 || two[0].ID != other || two[0].Status != "approved" {
		t.Errorf("child two's list = %+v, want only its own request", two)
	}
	// an account with no requests, and a caller that is no account, read nothing
	for _, id := range []string{uViewer, "admin-token", ""} {
		got, err := e.store.OwnRequests(ctx, id)
		if err != nil || len(got) != 0 {
			t.Errorf("OwnRequests(%q) = %+v, %v; want none", id, got, err)
		}
	}
}

// Each status as the child reads it, and what a row says: ids and times only, no
// title, no child, no one who answered.
func TestOwnRequestsStatusesAndShape(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	loc := e.riyadh() // 17:00 on 10 November 2026, Riyadh
	now := e.clock

	// asked and still waiting
	e.want(201, "child", "POST", ownPath, ask("video", vidOpen))
	list := e.ownList()
	if len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
	first := list[0]
	if keysOf(first) != "created_at,expires_at,id,status,subject_id,subject_kind" {
		t.Errorf("a waiting request has the keys %s", keysOf(first))
	}
	if first["status"] != "pending" || first["subject_kind"] != "video" || first["subject_id"] != vidOpen ||
		first["created_at"] != "2026-11-10T14:00:00Z" || first["expires_at"] != "2026-11-17T14:00:00Z" {
		t.Errorf("waiting request = %v", first)
	}
	id := first["id"].(string)

	// denied: the child reads `denied` and when, not by whom
	e.clock = now.Add(time.Minute)
	e.want(200, "guardian", "POST", "/api/parental/requests/"+id+"/deny", nil)
	first = e.ownList()[0]
	if keysOf(first) != "created_at,decided_at,expires_at,id,status,subject_id,subject_kind" {
		t.Errorf("a denied request has the keys %s", keysOf(first))
	}
	if first["status"] != "denied" || first["decided_at"] != "2026-11-10T14:01:00Z" {
		t.Errorf("denied request = %v", first)
	}

	// asked again and approved: the newer request is first, the denied one stays below
	e.clock = now.Add(2 * time.Minute)
	again := e.want(201, "child", "POST", ownPath, ask("video", vidOpen))["request"].(map[string]any)["id"].(string)
	e.clock = now.Add(3 * time.Minute)
	e.want(200, "guardian", "POST", "/api/parental/requests/"+again+"/approve", nil)
	list = e.ownList()
	if len(list) != 2 || list[0]["id"] != again || list[0]["status"] != "approved" || list[1]["id"] != id || list[1]["status"] != "denied" {
		t.Fatalf("list after asking again = %v", list)
	}

	// more time: the minutes, and an end at the home's day line (04:00 Riyadh)
	e.clock = now.Add(4 * time.Minute)
	e.want(201, "child", "POST", ownPath, ask("time", "30"))
	first = e.ownList()[0]
	if keysOf(first) != "created_at,expires_at,id,minutes,status,subject_id,subject_kind" {
		t.Errorf("a request for time has the keys %s", keysOf(first))
	}
	if first["status"] != "pending" || first["subject_kind"] != "time" || first["subject_id"] != "30" ||
		first["minutes"] != float64(30) || first["expires_at"] != "2026-11-11T01:00:00Z" {
		t.Errorf("request for time = %v", first)
	}
	// past the day line it is over, before the activity loop has closed its row
	e.clock = time.Date(2026, 11, 11, 4, 0, 1, 0, loc)
	first = e.ownList()[0]
	if first["subject_kind"] != "time" || first["status"] != "expired" {
		t.Errorf("request for time after the day line = %v", first)
	}
	if got := e.text(`SELECT status FROM parental_requests WHERE subject_kind = 'time'`); got != "pending" {
		t.Errorf("reading the list changed the row to %s", got)
	}

	// a request the home closed as expired
	e.exec(`UPDATE parental_requests SET status = 'expired', decided_at = $1 WHERE subject_kind = 'time'`, e.clock.UTC())
	if first = e.ownList()[0]; first["status"] != "expired" || first["decided_at"] == nil {
		t.Errorf("closed request for time = %v", first)
	}

	// no answer names a title, the child, or a guardian
	code, _ := e.do("child", "GET", ownPath, nil)
	body := e.raw("child", "GET", ownPath, nil).Body.String()
	for _, hidden := range []string{"Open Sixteen", "title", "decided_by", "child_id", uGuardian, "h-guardian", "subject\":{"} {
		if code != 200 || strings.Contains(body, hidden) {
			t.Errorf("the child's list says %q: %s", hidden, body)
		}
	}
}

// A request whose subject the child can no longer reach, or that is gone, is still
// listed, by its id only.
func TestOwnRequestsKeepAnUnreachableSubject(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	id := e.want(201, "child", "POST", ownPath, ask("video", vidOpen))["request"].(map[string]any)["id"].(string)

	// the video moves to a library the child cannot open
	e.exec(`UPDATE videos SET library_id = $2 WHERE id = $1`, vidOpen, libClosed)
	list := e.ownList()
	if len(list) != 1 || list[0]["id"] != id || list[0]["subject_id"] != vidOpen || list[0]["status"] != "pending" {
		t.Errorf("list after the video moved = %v", list)
	}
	if keysOf(list[0]) != "created_at,expires_at,id,status,subject_id,subject_kind" {
		t.Errorf("keys after the video moved = %s", keysOf(list[0]))
	}
	// and then is deleted
	e.exec(`DELETE FROM videos WHERE id = $1`, vidOpen)
	list = e.ownList()
	if len(list) != 1 || list[0]["id"] != id || list[0]["subject_id"] != vidOpen {
		t.Errorf("list after the video was deleted = %v", list)
	}
}

// The bounds: seven days back, fifty at most, newest first.
func TestOwnRequestsBounds(t *testing.T) {
	t.Parallel()
	e := setup(t)
	e.askReset()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return e.clock.Add(-d) }
	row := func(n int, status string, created time.Time) string {
		id := fmt.Sprintf("00000000-0000-4000-8000-00005720%04d", n)
		e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id, status, created_at) VALUES ($1, $2, 'video', $3, $4, $5)`,
			id, uChild, fmt.Sprintf("v-%04d", n), status, created)
		return id
	}

	// seven days: a second inside is listed, exactly seven days and older are not
	inside := row(1, "denied", at(7*24*time.Hour-time.Second))
	row(2, "denied", at(7*24*time.Hour))
	row(3, "approved", at(8*24*time.Hour))
	row(4, "pending", at(30*24*time.Hour))
	list := e.ownList()
	if len(list) != 1 || list[0]["id"] != inside {
		t.Fatalf("list at the seven-day bound = %v, want only %s", list, inside)
	}

	// fifty: of sixty in the window, the fifty newest, newest first
	e.exec(`DELETE FROM parental_requests`)
	ids := make([]string, 60)
	for i := range ids {
		status := "denied"
		if i%3 == 0 {
			status = "approved"
		}
		ids[i] = row(100+i, status, at(time.Duration(i+1)*time.Minute)) // ids[0] is the newest
	}
	list = e.ownList()
	if len(list) != 50 {
		t.Fatalf("%d requests listed, want 50", len(list))
	}
	for i, r := range list {
		if r["id"] != ids[i] {
			t.Fatalf("row %d is %v, want %s (newest first)", i, r["id"], ids[i])
		}
	}
}
