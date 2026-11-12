package parental_test

// Ask a parent, the parent PIN and a child's activity (Epic 57 stories 57.06 and
// 57.08) against a real migrated Postgres (slot 0222). The role of every route is
// tried as no one, a viewer, the restricted child, that child's guardian, another
// child's guardian and an admin.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	vidOpen   = "00000000-0000-4000-8000-0000572a0001" // in the library the child can open, rated 16+
	vidOpen2  = "00000000-0000-4000-8000-0000572a0002"
	vidClosed = "00000000-0000-4000-8000-0000572b0001" // in the library the child cannot open
	reqID     = "00000000-0000-4000-8000-0000572000a1"
	thePIN    = "482915"
	thePass   = "the-right-password"
)

type notice struct {
	kind string
	p    notify.Payload
}

type recorder struct{ e *env }

func (n recorder) Notify(_ context.Context, kind string, p notify.Payload) error {
	n.e.notices = append(n.e.notices, notice{kind, p})
	return nil
}

var askOf = map[*env]*parentalh.AskHandler{}

// mountAsk mounts the ask, PIN and activity routes beside 57.02's, on the env's
// clock, with a recording notifier, one right password and no least answer time.
func (e *env) mountAsk(r chi.Router, store *parental.Store) {
	e.store = store
	now := func() time.Time {
		if e.clock.IsZero() {
			return time.Now()
		}
		return e.clock
	}
	store.Now = now
	h := &parentalh.AskHandler{
		Handler: parentalh.Handler{Store: store, Cache: parental.NewCache(store)},
		Notify:  recorder{e},
		VerifyPassword: func(_ context.Context, _, pw string) error {
			if pw != thePass {
				return fmt.Errorf("wrong")
			}
			return nil
		},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		MinAnswer: -1,
		Now:       now,
	}
	h.Mount(r)
	askOf[e] = h
	e.t.Cleanup(func() { delete(askOf, e) })
}

func (e *env) video(id, lib, title string, tier any) {
	e.t.Helper()
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at, age_tier)
		VALUES ($1::uuid, $2, '/media/' || $1::text || '.mp4', $1::text || '.mp4', encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', $3, 100, now(), now(), $4)
		ON CONFLICT (id) DO NOTHING`, id, lib, title, tier)
}

var pinHash string

// askReset is reset plus the two videos, no PINs but the guardian's and the
// admin's, and nothing asked.
func (e *env) askReset() {
	e.t.Helper()
	e.reset()
	e.notices = nil
	e.video(vidOpen, libOpen, "Open Sixteen", 16)
	e.video(vidOpen2, libOpen, "Open Unrated", nil)
	e.video(vidClosed, libClosed, "Closed One", 16)
	e.exec(`DELETE FROM parental_pins`)
	if pinHash == "" {
		if err := e.store.SetPIN(context.Background(), uGuardian, thePIN); err != nil {
			e.t.Fatalf("set pin: %v", err)
		}
		if err := e.db.QueryRow(`SELECT pin_hash FROM parental_pins WHERE guardian_id = $1`, uGuardian).Scan(&pinHash); err != nil {
			e.t.Fatal(err)
		}
		e.exec(`DELETE FROM parental_pins`)
	}
	e.exec(`INSERT INTO parental_pins (guardian_id, pin_hash) VALUES ($1, $3), ($2, $3)`, uGuardian, uAdmin, pinHash)
}

func (e *env) raw(as, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", as)
	if e.remote != "" {
		req.RemoteAddr = e.remote
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) text(q string, args ...any) string {
	e.t.Helper()
	var s string
	if err := e.db.QueryRow(q, args...).Scan(&s); err != nil {
		e.t.Fatalf("text %q: %v", q, err)
	}
	return s
}

func ask(kind, id string) map[string]any {
	return map[string]any{"subject_kind": kind, "subject_id": id}
}

func unlock(guardian, pin, id string) map[string]any {
	return map[string]any{"guardian_id": guardian, "pin": pin, "subject_kind": "video", "subject_id": id}
}

// 57.06 T06 and 57.08 T04: every new route, as every caller, and a refusal
// changes nothing.
func TestAskRoles(t *testing.T) {
	e := setup(t)
	routes := []struct {
		name, method, path string
		body               any
		want               [6]int
	}{
		{"ask", "POST", "/api/me/parental/requests", ask("video", vidOpen2), [6]int{401, 403, 201, 403, 403, 403}},
		{"who can unlock", "GET", "/api/me/parental/guardians", nil, [6]int{401, 403, 200, 403, 403, 403}},
		{"unlock", "POST", "/api/me/parental/unlock", unlock(uGuardian, thePIN, vidOpen2), [6]int{401, 403, 200, 403, 403, 403}},
		{"has a PIN", "GET", "/api/me/parental-pin", nil, [6]int{401, 403, 403, 200, 200, 200}},
		{"set a PIN", "PUT", "/api/me/parental-pin", map[string]any{"pin": "135790", "password": thePass},
			[6]int{401, 403, 403, 204, 204, 204}},
		{"list requests", "GET", "/api/parental/requests?status=pending", nil, [6]int{401, 403, 403, 200, 200, 200}},
		{"approve", "POST", "/api/parental/requests/" + reqID + "/approve", map[string]any{}, [6]int{401, 403, 403, 200, 404, 200}},
		{"deny", "POST", "/api/parental/requests/" + reqID + "/deny", nil, [6]int{401, 403, 403, 200, 404, 200}},
		{"activity", "GET", "/api/users/" + uChild + "/parental/activity", nil, [6]int{401, 404, 404, 200, 404, 200}},
	}
	state := func() string {
		return fmt.Sprint(
			e.count(`SELECT count(*) FROM parental_requests`),
			e.count(`SELECT count(*) FROM parental_requests WHERE status = 'pending'`),
			e.count(`SELECT count(*) FROM parental_rules`),
			e.count(`SELECT count(*) FROM parental_events`),
			e.count(`SELECT count(*) FROM parental_pin_failures`),
			e.text(`SELECT COALESCE(string_agg(guardian_id::text || pin_hash || failed::text, ',' ORDER BY guardian_id), '') FROM parental_pins`))
	}
	for _, rt := range routes {
		for i, as := range who {
			e.askReset()
			e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'video', $3)`, reqID, uChild, vidOpen)
			before := state()
			code, out := e.do(as, rt.method, rt.path, rt.body)
			if code != rt.want[i] {
				t.Errorf("%s as %q = %d, want %d (%v)", rt.name, as, code, rt.want[i], out)
			}
			if code >= 400 && state() != before {
				t.Errorf("%s as %q was refused (%d) but changed something", rt.name, as, code)
			}
		}
	}

	// a restricted account is told so, by its code
	e.askReset()
	for _, path := range []string{"/api/parental/requests", "/api/me/parental-pin"} {
		if out := e.want(403, "child", "GET", path, nil); out["code"] != "restricted" {
			t.Errorf("GET %s as the child: code = %v, want restricted", path, out["code"])
		}
	}
	// a guardian's list holds only their own children's requests
	e.exec(`INSERT INTO parental_requests (id, child_id, subject_kind, subject_id) VALUES ($1, $2, 'video', $3)`, reqID, uChild, vidOpen)
	if out := e.want(200, "other-guardian", "GET", "/api/parental/requests", nil); len(out["requests"].([]any)) != 0 {
		t.Errorf("another child's guardian sees the child's request: %v", out)
	}
	if out := e.want(200, "admin", "GET", "/api/parental/requests", nil); len(out["requests"].([]any)) != 1 {
		t.Errorf("the admin does not see the request: %v", out)
	}
	// a sign-in through the cloud that may only play decides nothing and sets no PIN
	e.want(403, "guardian-cloud-play", "POST", "/api/parental/requests/"+reqID+"/approve", nil)
	e.want(403, "guardian-cloud-play", "PUT", "/api/me/parental-pin", map[string]any{"pin": "135790", "password": thePass})
	// a request that is not there: 404 for those who may decide, 403 for the rest
	missing := "/api/parental/requests/00000000-0000-4000-8000-0000572000ff/approve"
	for i, want := range [6]int{401, 403, 403, 404, 404, 404} {
		if code, _ := e.do(who[i], "POST", missing, nil); code != want {
			t.Errorf("approve a missing request as %q = %d, want %d", who[i], code, want)
		}
	}
}

// 57.06 T01: a request, approved with an expiry, allows the subject until then.
func TestAskApproveWithExpiry(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	eff, _ := e.store.Effective(ctx, uChild)
	item := parental.Item{Tier: ptr(16), Subjects: []parental.Subject{{Kind: "video", ID: vidOpen}}}
	if eff.Permits(item, e.clock) {
		t.Fatal("the 16+ video is open to the 9+ child before anything was asked")
	}
	out := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))
	req := out["request"].(map[string]any)
	if req["status"] != "pending" || req["subject_id"] != vidOpen || req["expires_at"] != "2026-11-18T12:00:00Z" {
		t.Fatalf("request = %v", req)
	}
	id := req["id"].(string)

	// the guardian cannot open the library: the request shows no title
	list := e.want(200, "guardian", "GET", "/api/parental/requests?status=pending", nil)["requests"].([]any)
	if len(list) != 1 {
		t.Fatalf("guardian's list = %v", list)
	}
	first := list[0].(map[string]any)
	if s := first["subject"].(map[string]any); s["hidden"] != true || s["title"] != nil {
		t.Errorf("subject for a guardian without the library = %v", s)
	}
	if c := first["child"].(map[string]any); c["id"] != uChild || c["username"] != "h-child" {
		t.Errorf("child = %v", c)
	}
	list = e.want(200, "admin", "GET", "/api/parental/requests", nil)["requests"].([]any)
	if s := list[0].(map[string]any)["subject"].(map[string]any); s["title"] != "Open Sixteen" || s["tier"] != float64(16) {
		t.Errorf("subject for the admin = %v", s)
	}

	e.want(422, "guardian", "POST", "/api/parental/requests/"+id+"/approve", map[string]any{"expires_at": "2026-11-11T11:00:00Z"})
	until := "2026-11-11T18:00:00Z"
	out = e.want(200, "guardian", "POST", "/api/parental/requests/"+id+"/approve", map[string]any{"expires_at": until})
	if r := out["request"].(map[string]any); r["status"] != "approved" || r["decided_by"] != uGuardian {
		t.Errorf("approved request = %v", r)
	}
	if r := out["rule"].(map[string]any); r["effect"] != "allow" || r["subject_id"] != vidOpen || r["expires_at"] != until {
		t.Errorf("rule = %v", r)
	}
	eff, _ = e.store.Effective(ctx, uChild)
	if !eff.Permits(item, e.clock) {
		t.Error("approved, and still hidden")
	}
	if eff.Permits(item, e.clock.Add(6*time.Hour)) {
		t.Error("still allowed at the expiry")
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'parental.rule.added' AND target_id = $1`, uChild); n != 1 {
		t.Errorf("audit rows for the rule = %d, want 1 (the same path as a rule added by hand)", n)
	}
	// answered once
	if out := e.want(409, "admin", "POST", "/api/parental/requests/"+id+"/deny", nil); out["code"] != "request_decided" {
		t.Errorf("second answer: %v", out)
	}
	kinds := e.text(`SELECT string_agg(kind || ':' || COALESCE(actor_id::text, ''), ',' ORDER BY kind) FROM parental_events WHERE child_id = $1`, uChild)
	if kinds != "approved:"+uGuardian+",asked:" {
		t.Errorf("events = %s", kinds)
	}

	// denied: no rule
	out = e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen2))
	id = out["request"].(map[string]any)["id"].(string)
	rules := e.count(`SELECT count(*) FROM parental_rules`)
	if r := e.want(200, "admin", "POST", "/api/parental/requests/"+id+"/deny", nil)["request"].(map[string]any); r["status"] != "denied" {
		t.Errorf("denied request = %v", r)
	}
	if e.count(`SELECT count(*) FROM parental_rules`) != rules {
		t.Error("a denial added a rule")
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'denied' AND actor_id = $1`, uAdmin); n != 1 {
		t.Errorf("denied events = %d", n)
	}
}

func ptr(n int) *int { return &n }

// 57.06 T02: the same thing asked twice is one row; the 21st waiting request is
// refused; a subject that is missing or in a library the child cannot open is 404.
func TestAskDuplicateAndLimit(t *testing.T) {
	e := setup(t)
	e.askReset()
	first := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)
	again := e.want(200, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)
	if first["id"] != again["id"] || e.count(`SELECT count(*) FROM parental_requests`) != 1 {
		t.Errorf("asked twice: %v then %v", first["id"], again["id"])
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'asked'`); n != 1 {
		t.Errorf("asked events = %d, want 1", n)
	}
	for _, body := range []map[string]any{
		ask("video", vidClosed), ask("video", "00000000-0000-4000-8000-0000572a00ff"), ask("video", "not-an-id"),
		ask("library", libClosed), ask("series", "00000000-0000-4000-8000-0000572a00ff"),
	} {
		e.want(404, "child", "POST", "/api/me/parental/requests", body)
	}
	e.want(422, "child", "POST", "/api/me/parental/requests", ask("time", "30"))
	e.want(422, "child", "POST", "/api/me/parental/requests", ask("video", ""))
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("library", libOpen))

	for i := 0; i < parental.MaxPendingRequests-2; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-0000572a1%03d", i)
		e.video(id, libOpen, "More", 18)
		e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", id))
	}
	e.video("00000000-0000-4000-8000-0000572a1999", libOpen, "One too many", 18)
	rec := e.raw("child", "POST", "/api/me/parental/requests", ask("video", "00000000-0000-4000-8000-0000572a1999"))
	if rec.Code != 429 || !strings.Contains(rec.Body.String(), `"too_many_requests"`) {
		t.Errorf("21st request = %d %s", rec.Code, rec.Body.String())
	}
	if n := e.count(`SELECT count(*) FROM parental_requests WHERE status = 'pending'`); n != parental.MaxPendingRequests {
		t.Errorf("waiting = %d, want %d", n, parental.MaxPendingRequests)
	}
	// one that is already waiting is still answered
	e.want(200, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))
}

// A request expires after seven days, or when what it asks for is deleted; one
// the child can now see anyway closes as approved.
func TestRequestsClose(t *testing.T) {
	e := setup(t)
	e.askReset()
	ctx := context.Background()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.exec(`DELETE FROM parental_rules`)
	old := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)["id"].(string)

	e.clock = e.clock.Add(7*24*time.Hour + time.Minute)
	if out := e.want(200, "guardian", "GET", "/api/parental/requests", nil); len(out["requests"].([]any)) != 0 {
		t.Errorf("a request older than seven days is still listed as waiting: %v", out)
	}
	if out := e.want(410, "guardian", "POST", "/api/parental/requests/"+old+"/approve", nil); out["code"] != "request_expired" {
		t.Errorf("approving an old request: %v", out)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, old); st != "expired" {
		t.Errorf("status = %s, want expired", st)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules`); n != 0 {
		t.Errorf("an expired request added %d rules", n)
	}
	e.want(410, "guardian", "POST", "/api/parental/requests/"+old+"/deny", nil)

	// deleted subject: approving closes it as expired; the loop does the same
	gone := "00000000-0000-4000-8000-0000572a2001"
	e.video(gone, libOpen, "Going", 18)
	id := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", gone))["request"].(map[string]any)["id"].(string)
	stays := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)["id"].(string)
	e.exec(`DELETE FROM videos WHERE id = $1`, gone)
	e.want(410, "admin", "POST", "/api/parental/requests/"+id+"/approve", nil)

	e.video(gone, libOpen, "Going", 18)
	id = e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", gone))["request"].(map[string]any)["id"].(string)
	e.exec(`DELETE FROM videos WHERE id = $1`, gone)
	if n, err := e.store.CloseRequests(ctx); err != nil || n != 1 {
		t.Fatalf("CloseRequests = %d, %v; want 1", n, err)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, id); st != "expired" {
		t.Errorf("status of a request for a deleted video = %s", st)
	}
	if st := e.text(`SELECT status FROM parental_requests WHERE id = $1`, stays); st != "pending" {
		t.Errorf("a live request became %s", st)
	}

	// the limit is raised to 16+: the request for the 16+ video closes as approved, with no rule
	e.exec(`UPDATE parental_policies SET max_tier = 16 WHERE user_id = $1`, uChild)
	if n, err := e.store.CloseRequests(ctx); err != nil || n != 1 {
		t.Fatalf("CloseRequests after the limit was raised = %d, %v; want 1", n, err)
	}
	if st := e.text(`SELECT status || ':' || COALESCE(decided_by::text, '') FROM parental_requests WHERE id = $1`, stays); st != "approved:" {
		t.Errorf("a request the child can now see = %s", st)
	}
	if n := e.count(`SELECT count(*) FROM parental_rules`); n != 0 {
		t.Errorf("closing added %d rules", n)
	}
	// with a block rule in place nothing is closed that way
	e.exec(`UPDATE parental_requests SET status = 'pending', decided_at = NULL WHERE id = $1`, stays)
	e.exec(`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id) VALUES ($1, 'block', 'library', $2)`, uChild, libOpen)
	if n, _ := e.store.CloseRequests(ctx); n != 0 {
		t.Errorf("closed %d requests of a child with a block rule", n)
	}
}

// 57.06 T03: a right PIN allows the subject until 04:00 home time, writes the
// event and closes the waiting request for it.
func TestUnlockRightPIN(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.exec(`INSERT INTO app_settings (key, value) VALUES ('home.timezone', '"Asia/Riyadh"')
	        ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) // 15:00 in Riyadh
	waiting := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)["id"].(string)

	// who the child's device offers: the guardian and the admin, both with a PIN
	list := e.want(200, "child", "GET", "/api/me/parental/guardians", nil)["guardians"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["id"] != uAdmin || list[1].(map[string]any)["username"] != "h-guardian" {
		t.Errorf("who can unlock = %v", list)
	}

	out := e.want(200, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, thePIN, vidOpen))
	rule := out["rule"].(map[string]any)
	if rule["effect"] != "allow" || rule["subject_kind"] != "video" || rule["subject_id"] != vidOpen ||
		rule["expires_at"] != "2026-11-12T01:00:00Z" { // 04:00 the next morning in Riyadh
		t.Errorf("rule = %v", rule)
	}
	eff, _ := e.store.Effective(context.Background(), uChild)
	item := parental.Item{Tier: ptr(16), Subjects: []parental.Subject{{Kind: "video", ID: vidOpen}}}
	if !eff.Permits(item, e.clock) || eff.Permits(item, time.Date(2026, 11, 12, 1, 0, 0, 0, time.UTC)) {
		t.Error("the video is not open exactly until the day ends")
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'approved' AND actor_id = $1 AND subject_id = $2`, uGuardian, vidOpen); n != 1 {
		t.Errorf("approved events = %d, want 1", n)
	}
	if st := e.text(`SELECT status || ':' || decided_by::text FROM parental_requests WHERE id = $1`, waiting); st != "approved:"+uGuardian {
		t.Errorf("the waiting request = %s", st)
	}
	// before 04:00 the day ends the same morning
	e.clock = time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC) // 02:30 in Riyadh
	rule = e.want(200, "child", "POST", "/api/me/parental/unlock", unlock(uAdmin, thePIN, vidOpen2))["rule"].(map[string]any)
	if rule["expires_at"] != "2026-11-12T01:00:00Z" {
		t.Errorf("an unlock at 02:30 ends at %v", rule["expires_at"])
	}
	// nothing to open: 404, whatever the PIN
	e.want(404, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, thePIN, vidClosed))
	e.want(404, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, "000000", vidClosed))
	e.want(422, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, "12345", vidOpen))
	e.want(422, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, "12345a", vidOpen))
	if n := e.count(`SELECT count(*) FROM parental_pin_failures`); n != 0 {
		t.Errorf("wrong tries counted = %d, want 0", n)
	}
}

// 57.06 T04: wrong PINs. Five lock that guardian's PIN (15 minutes, then twice as
// long each time, up to 24 hours); ten in an hour from one child close PIN entry
// for that account; twenty from one address close it for that address.
func TestUnlockWrongPINs(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	try := func(guardian, pin string) (int, string, string) {
		rec := e.raw("child", "POST", "/api/me/parental/unlock", unlock(guardian, pin, vidOpen))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		code, _ := out["code"].(string)
		return rec.Code, code, rec.Header().Get("Retry-After")
	}
	// the same answer for a wrong PIN, a guardian of another child, a guardian with
	// no PIN, a user who is no guardian and a user who does not exist
	e.exec(`DELETE FROM parental_pins WHERE guardian_id = $1`, uAdmin)
	e.exec(`INSERT INTO parental_pins (guardian_id, pin_hash) VALUES ($1, $2)`, uOtherG, pinHash)
	for i, g := range []string{uGuardian, uOtherG, uAdmin, uViewer, "00000000-0000-4000-8000-0000572000ee", "nobody"} {
		pin := "000000"
		if g == uOtherG {
			pin = thePIN // their own right PIN opens nothing for another guardian's child
		}
		e.remote = fmt.Sprintf("198.51.100.%d:4000", i+1)
		if status, code, _ := try(g, pin); status != 403 || code != "pin_wrong" {
			t.Errorf("try as %s = %d %s, want 403 pin_wrong", g, status, code)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_rules WHERE effect = 'allow'`); n != 0 {
		t.Fatalf("a wrong try allowed something (%d rules)", n)
	}

	// --- the guardian's lock, doubling ---
	e.askReset()
	n := 0
	wrong := func() (int, string, string) {
		n++
		e.remote = fmt.Sprintf("203.0.113.%d:4000", n%250+1) // a new address each time: only the stored limits count
		return try(uGuardian, "000000")
	}
	wantLock := 15 * time.Minute
	for round := 1; round <= 9; round++ {
		e.exec(`DELETE FROM parental_pin_failures`) // the child's own limit is tried below
		for i := 1; i < parental.PINMaxWrong; i++ {
			if status, code, _ := wrong(); status != 403 || code != "pin_wrong" {
				t.Fatalf("round %d, wrong try %d = %d %s, want 403 pin_wrong", round, i, status, code)
			}
		}
		status, code, retry := wrong()
		if status != 429 || code != "pin_locked" || retry != fmt.Sprint(int(wantLock/time.Second)) {
			t.Fatalf("round %d, fifth wrong try = %d %s Retry-After %s; want 429 pin_locked %d", round, status, code, retry, int(wantLock/time.Second))
		}
		// while it is locked the right PIN is refused too, and nothing is counted
		before := e.count(`SELECT count(*) FROM parental_pin_failures`)
		e.clock = e.clock.Add(wantLock - time.Second)
		if status, code, retry := try(uGuardian, thePIN); status != 429 || code != "pin_locked" || retry != "1" {
			t.Fatalf("round %d, right PIN while locked = %d %s Retry-After %s", round, status, code, retry)
		}
		if e.count(`SELECT count(*) FROM parental_pin_failures`) != before {
			t.Fatalf("round %d: a refused try was counted", round)
		}
		e.clock = e.clock.Add(2 * time.Second)
		if wantLock *= 2; wantLock > 24*time.Hour {
			wantLock = 24 * time.Hour
		}
	}
	if wantLock != 24*time.Hour {
		t.Fatalf("the lock never reached 24 hours (%v)", wantLock)
	}
	if got := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'pin_locked' AND subject_kind = 'guardian' AND subject_id = $1`, uGuardian); got != 9 {
		t.Errorf("pin_locked events = %d, want 9", got)
	}
	locks := 0
	for _, nt := range e.notices {
		if nt.kind == "parental_pin_locked" && nt.p.Audience == uGuardian {
			locks++
		}
	}
	if locks != 9 {
		t.Errorf("pin_locked notices to the guardian = %d, want 9", locks)
	}
	// a right PIN starts the count again: the next lock is 15 minutes
	e.exec(`DELETE FROM parental_pin_failures`)
	if status, _, _ := try(uGuardian, thePIN); status != 200 {
		t.Fatalf("right PIN after the lock ended = %d", status)
	}
	for i := 1; i < parental.PINMaxWrong; i++ {
		wrong()
	}
	if status, _, retry := wrong(); status != 429 || retry != "900" {
		t.Errorf("first lock after a right PIN = %d Retry-After %s, want 429 900", status, retry)
	}

	// --- the child's lock: ten wrong tries in an hour, across guardians ---
	e.askReset()
	e.clock = time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= parental.PINChildMaxWrong; i++ {
		e.exec(`UPDATE parental_pins SET failed = 0, locks = 0, locked_until = NULL`) // no guardian lock in the way
		g := uGuardian
		if i%2 == 0 {
			g = uAdmin
		}
		e.remote = fmt.Sprintf("203.0.113.%d:5000", i)
		e.clock = e.clock.Add(time.Minute)
		status, code, retry := try(g, "000000")
		if i < parental.PINChildMaxWrong && (status != 403 || code != "pin_wrong") {
			t.Fatalf("wrong try %d of the child = %d %s", i, status, code)
		}
		if i == parental.PINChildMaxWrong && (status != 429 || code != "pin_locked" || retry != "3600") {
			t.Fatalf("tenth wrong try of the child = %d %s Retry-After %s; want 429 pin_locked 3600", status, code, retry)
		}
	}
	e.exec(`UPDATE parental_pins SET failed = 0, locks = 0, locked_until = NULL`)
	e.clock = e.clock.Add(59 * time.Minute)
	if status, code, _ := try(uAdmin, thePIN); status != 429 || code != "pin_locked" {
		t.Errorf("right PIN of another guardian while the account is closed = %d %s", status, code)
	}
	if got := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'pin_locked' AND subject_kind = 'account'`); got != 1 {
		t.Errorf("account pin_locked events = %d, want 1", got)
	}
	e.clock = e.clock.Add(2 * time.Minute)
	if status, _, _ := try(uAdmin, thePIN); status != 200 {
		t.Errorf("right PIN after the account's hour = %d, want 200", status)
	}

	// --- the address's lock: twenty wrong tries in an hour from one address ---
	e.askReset()
	e.clock = time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	e.remote = "192.0.2.77:6000"
	for i := 1; i <= parentalh.SourceMaxWrong; i++ {
		// neither stored limit in the way
		e.exec(`DELETE FROM parental_pin_failures`)
		e.exec(`UPDATE parental_pins SET failed = 0, locks = 0, locked_until = NULL`)
		status, code, retry := try(uGuardian, "000000")
		if i < parentalh.SourceMaxWrong && (status != 403 || code != "pin_wrong") {
			t.Fatalf("wrong try %d from the address = %d %s", i, status, code)
		}
		if i == parentalh.SourceMaxWrong && (status != 429 || code != "pin_locked" || retry != "3600") {
			t.Fatalf("twentieth wrong try from the address = %d %s Retry-After %s", status, code, retry)
		}
	}
	e.exec(`DELETE FROM parental_pin_failures`)
	if status, code, _ := try(uGuardian, thePIN); status != 429 || code != "pin_locked" {
		t.Errorf("right PIN from the closed address = %d %s", status, code)
	}
	if got := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'pin_locked' AND subject_kind = 'source' AND subject_id = ''`); got != 1 {
		t.Errorf("source pin_locked events = %d, want 1 (and no address in it)", got)
	}
	e.remote = "192.0.2.78:6000"
	if status, _, _ := try(uGuardian, thePIN); status != 200 {
		t.Errorf("right PIN from another address = %d, want 200", status)
	}
	e.remote = "192.0.2.77:6000"
	e.clock = e.clock.Add(time.Hour + time.Second)
	if status, _, _ := try(uGuardian, thePIN); status != 200 {
		t.Errorf("right PIN from the address after its hour = %d, want 200", status)
	}
	// the PIN is nowhere but its hash
	for _, q := range []string{
		`SELECT count(*) FROM audit_log WHERE payload::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM parental_events WHERE subject_id LIKE '%' || $1 || '%' OR dedupe LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM parental_pins WHERE pin_hash LIKE '%' || $1 || '%' OR pin_hash NOT LIKE '$argon2id$%'`,
	} {
		if got := e.count(q, thePIN); got != 0 {
			t.Errorf("the PIN is stored in the clear: %s = %d", q, got)
		}
	}
}

// 57.06 T05: a right PIN, a wrong one and a refused try all take at least 250 ms
// and the refusals have one shape.
func TestUnlockTiming(t *testing.T) {
	e := setup(t)
	e.askReset()
	askOf[e].MinAnswer = 0 // the real least time
	shape := func(body []byte) string {
		var out map[string]any
		_ = json.Unmarshal(body, &out)
		keys := []string{}
		for _, k := range []string{"type", "title", "status", "detail", "code", "instance", "requestId"} {
			if _, ok := out[k]; ok {
				keys = append(keys, k)
			}
		}
		return strings.Join(keys, ",")
	}
	shapes := map[string]bool{}
	for i, c := range []struct {
		guardian, pin string
		want          int
	}{
		{uGuardian, thePIN, 200}, {uGuardian, "000000", 403}, {uViewer, "000000", 403}, {"nobody", "000000", 403},
		{uGuardian, thePIN, 200}, {uAdmin, "111111", 403},
	} {
		e.remote = fmt.Sprintf("198.51.100.%d:7000", i+1)
		start := time.Now()
		rec := e.raw("child", "POST", "/api/me/parental/unlock", unlock(c.guardian, c.pin, vidOpen))
		took := time.Since(start)
		if rec.Code != c.want {
			t.Errorf("try %d = %d, want %d", i, rec.Code, c.want)
		}
		if took < parentalh.DefaultMinAnswer {
			t.Errorf("try %d (%d) answered in %v, under %v", i, rec.Code, took, parentalh.DefaultMinAnswer)
		}
		if rec.Code == 403 {
			shapes[shape(rec.Body.Bytes())] = true
		}
	}
	if len(shapes) != 1 {
		t.Errorf("the refusals have %d shapes: %v", len(shapes), shapes)
	}
}

// 57.06 T07: a new request tells each guardian once; the notice has no title, no
// name and no id of what was asked for.
func TestAskNotice(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2)`, uChild, uViewer)
	id := e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))["request"].(map[string]any)["id"].(string)
	e.want(200, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen)) // asked again: no second notice
	if len(e.notices) != 2 {
		t.Fatalf("notices = %d, want one per guardian (2): %+v", len(e.notices), e.notices)
	}
	to := map[string]bool{}
	for _, n := range e.notices {
		to[n.p.Audience] = true
		if n.kind != "parental_request" || n.p.TitleKey != "notify.parental.request" || len(n.p.Params) != 0 {
			t.Errorf("notice = %+v", n)
		}
		all := fmt.Sprintf("%+v", n)
		for _, secret := range []string{"Open Sixteen", "h-child", vidOpen, uChild} {
			if strings.Contains(all, secret) {
				t.Errorf("the notice carries %q: %s", secret, all)
			}
		}
		if n.p.Subject != "request:"+id {
			t.Errorf("subject = %q", n.p.Subject)
		}
	}
	if !to[uGuardian] || !to[uViewer] {
		t.Errorf("sent to %v", to)
	}
	// a child with no named guardian: the admins are told
	e.askReset()
	e.exec(`DELETE FROM parental_guardians WHERE child_id = $1`, uChild)
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))
	if len(e.notices) != 1 || e.notices[0].p.Audience != notify.AudienceAdmins {
		t.Errorf("notices for a child with no guardian = %+v", e.notices)
	}
	// no notifier: the request is still made
	e.askReset()
	askOf[e].Notify = nil
	e.want(201, "child", "POST", "/api/me/parental/requests", ask("video", vidOpen))
}

// Setting a PIN: six digits, the password typed again, and wrong passwords are
// limited.
func TestSetPIN(t *testing.T) {
	e := setup(t)
	e.askReset()
	e.exec(`DELETE FROM parental_pins`)
	if out := e.want(200, "guardian", "GET", "/api/me/parental-pin", nil); out["set"] != false {
		t.Errorf("before: %v", out)
	}
	for _, pin := range []string{"", "12345", "1234567", "12345a", "١٢٣٤٥٦"} {
		e.want(422, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": pin, "password": thePass})
	}
	if out := e.want(403, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": "wrong"}); out["code"] != "password_wrong" {
		t.Errorf("wrong password: %v", out)
	}
	e.want(403, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802"})
	if n := e.count(`SELECT count(*) FROM parental_pins`); n != 0 {
		t.Fatalf("a PIN was set without the password")
	}
	e.want(204, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": thePass})
	if out := e.want(200, "guardian", "GET", "/api/me/parental-pin", nil); out["set"] != true || len(out) != 1 {
		t.Errorf("after: %v", out)
	}
	if h := e.text(`SELECT pin_hash FROM parental_pins WHERE guardian_id = $1`, uGuardian); !strings.HasPrefix(h, "$argon2id$") || strings.Contains(h, "246802") {
		t.Errorf("stored as %q", h)
	}
	e.want(200, "child", "POST", "/api/me/parental/unlock", unlock(uGuardian, "246802", vidOpen))
	// a new PIN clears a lock
	e.exec(`UPDATE parental_pins SET failed = 3, locks = 2, locked_until = now() + interval '1 hour' WHERE guardian_id = $1`, uGuardian)
	e.want(204, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "135791", "password": thePass})
	if s := e.text(`SELECT failed::text || locks::text || COALESCE(locked_until::text, '-') FROM parental_pins WHERE guardian_id = $1`, uGuardian); s != "00-" {
		t.Errorf("after a new PIN the lock is %s", s)
	}
	// ten wrong passwords close the route to that user for an hour
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < parentalh.PasswordMaxWrong; i++ {
		e.want(403, "other-guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": "wrong"})
	}
	rec := e.raw("other-guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": thePass})
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "3600" {
		t.Errorf("after ten wrong passwords = %d Retry-After %s", rec.Code, rec.Header().Get("Retry-After"))
	}
	e.want(204, "guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": thePass})
	e.clock = e.clock.Add(time.Hour + time.Second)
	e.want(204, "other-guardian", "PUT", "/api/me/parental-pin", map[string]any{"pin": "246802", "password": thePass})
}

func (e *env) play(user, video string, seconds int, percent float64) {
	e.t.Helper()
	e.exec(`INSERT INTO watch_sessions (id, user_id, video_id, started_at, last_heartbeat, duration_sec, percent_complete)
		VALUES (gen_random_uuid(), $1, $2, $3, $3, $4, $5)`, user, video, e.store.Clock().Add(-time.Minute), seconds, percent)
}

// 57.08 T01 and T05: a play of 60 seconds (or 20 percent) is one watched event, a
// shorter one none, and an account that is not restricted gets nothing.
func TestActivityWatched(t *testing.T) {
	e := setup(t)
	e.askReset()
	ctx := context.Background()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.play(uChild, vidOpen, 60, 5)
	e.play(uChild, vidOpen2, 10, 3)
	e.play(uViewer, vidOpen, 600, 90) // not restricted
	for i := 0; i < 2; i++ {          // twice: once per play
		if _, err := e.store.RecordWatched(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.text(`SELECT COALESCE(string_agg(child_id::text || ':' || subject_kind || ':' || subject_id, ','), '') FROM parental_events WHERE kind = 'watched'`); got != uChild+":video:"+vidOpen {
		t.Errorf("watched events = %s", got)
	}
	// 20 percent of a short video counts too; the short play grows past a minute
	e.play(uChild, vidClosed, 30, 20)
	e.exec(`UPDATE watch_sessions SET duration_sec = 75 WHERE video_id = $1 AND user_id = $2`, vidOpen2, uChild)
	if n, err := e.store.RecordWatched(ctx); err != nil || n != 2 {
		t.Fatalf("RecordWatched = %d, %v; want 2", n, err)
	}
	// the signal of a restricted account is an event; of an unrestricted one, nothing
	if err := e.store.RecordSignalEvent(ctx, uChild, "Living room TV"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.RecordSignalEvent(ctx, uViewer, "A phone"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.RecordBlocked(ctx, uViewer, "video", vidOpen); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE child_id = $1`, uViewer); n != 0 {
		t.Errorf("an unrestricted account has %d events", n)
	}

	out := e.want(200, "admin", "GET", "/api/users/"+uChild+"/parental/activity", nil)
	events := out["events"].([]any)
	if len(events) != 4 {
		t.Fatalf("activity = %v", events)
	}
	var sawDevice, sawTitle bool
	for _, raw := range events {
		ev := raw.(map[string]any)
		if ev["kind"] == "signal" && ev["device"] == "Living room TV" && ev["subject"] == nil {
			sawDevice = true
		}
		if s, ok := ev["subject"].(map[string]any); ok && s["id"] == vidOpen && s["title"] == "Open Sixteen" &&
			s["tier"] == float64(16) && ev["kind"] == "watched" {
			sawTitle = true
		}
	}
	if !sawDevice || !sawTitle {
		t.Errorf("activity lacks the signal's device or the video's title: %v", events)
	}
	// the guardian cannot open either library: ids only
	for _, raw := range e.want(200, "guardian", "GET", "/api/users/"+uChild+"/parental/activity?kind=watched", nil)["events"].([]any) {
		s := raw.(map[string]any)["subject"].(map[string]any)
		if s["hidden"] != true || s["title"] != nil || s["tier"] != nil {
			t.Errorf("a subject the guardian cannot open = %v", s)
		}
	}
	// a deleted video stays in the list, marked removed
	e.exec(`DELETE FROM watch_sessions WHERE video_id = $1`, vidClosed)
	e.exec(`DELETE FROM videos WHERE id = $1`, vidClosed)
	removed := 0
	for _, raw := range e.want(200, "admin", "GET", "/api/users/"+uChild+"/parental/activity?kind=watched", nil)["events"].([]any) {
		if s := raw.(map[string]any)["subject"].(map[string]any); s["removed"] == true && s["id"] == vidClosed && s["title"] == nil {
			removed++
		}
	}
	if removed != 1 {
		t.Errorf("removed subjects = %d, want 1", removed)
	}
	e.want(400, "admin", "GET", "/api/users/"+uChild+"/parental/activity?kind=nonsense", nil)
	e.want(400, "admin", "GET", "/api/users/"+uChild+"/parental/activity?cursor=nonsense", nil)
	e.want(404, "admin", "GET", "/api/users/"+uViewer+"/parental/activity", nil) // not restricted
}

// 57.08 T02: three blocked hits on one thing in one day are one event; the day
// ends at 04:00 home time.
func TestActivityBlockedOncePerDay(t *testing.T) {
	e := setup(t)
	e.askReset()
	ctx := context.Background()
	e.exec(`INSERT INTO app_settings (key, value) VALUES ('home.timezone', '"UTC"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := e.store.RecordBlocked(ctx, uChild, "video", vidOpen); err != nil {
			t.Fatal(err)
		}
		e.clock = e.clock.Add(3 * time.Hour)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'blocked'`); n != 1 {
		t.Fatalf("blocked events after three hits = %d, want 1", n)
	}
	_ = e.store.RecordBlocked(ctx, uChild, "video", vidOpen2) // another thing: its own event
	e.clock = time.Date(2026, 11, 12, 3, 59, 0, 0, time.UTC)  // still the same day
	_ = e.store.RecordBlocked(ctx, uChild, "video", vidOpen)
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'blocked'`); n != 2 {
		t.Errorf("blocked events = %d, want 2", n)
	}
	e.clock = time.Date(2026, 11, 12, 4, 1, 0, 0, time.UTC) // the next day
	_ = e.store.RecordBlocked(ctx, uChild, "video", vidOpen)
	if n := e.count(`SELECT count(*) FROM parental_events WHERE kind = 'blocked'`); n != 3 {
		t.Errorf("blocked events the next day = %d, want 3", n)
	}
	_ = e.store.RecordBlocked(ctx, uChild, "nonsense", vidOpen)
	_ = e.store.RecordBlocked(ctx, "not-a-user", "video", vidOpen)
	if n := e.count(`SELECT count(*) FROM parental_events`); n != 3 {
		t.Errorf("events = %d, want 3", n)
	}
}

// 57.08 T03: the daily pass deletes events older than 90 days; the list never
// shows one; pages are 50, newest first.
func TestActivityRetentionAndPages(t *testing.T) {
	e := setup(t)
	e.askReset()
	ctx := context.Background()
	e.clock = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	e.exec(`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, at)
		SELECT $1, 'blocked', 'video', $2, $3::timestamptz - make_interval(days => g) FROM generate_series(1, 120) g`,
		uChild, vidOpen, e.clock)
	e.exec(`INSERT INTO parental_pin_failures (child_id, at) VALUES ($1, $2), ($1, $3)`, uChild, e.clock.Add(-25*time.Hour), e.clock.Add(-time.Hour))

	seen, cursor, pages := map[string]bool{}, "", 0
	var last string
	for {
		path := "/api/users/" + uChild + "/parental/activity"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		out := e.want(200, "guardian", "GET", path, nil)
		pages++
		for _, raw := range out["events"].([]any) {
			ev := raw.(map[string]any)
			at := ev["at"].(string)
			if last != "" && at >= last {
				t.Fatalf("not newest first: %s after %s", at, last)
			}
			last = at
			if seen[ev["id"].(string)] {
				t.Fatalf("event %v listed twice", ev["id"])
			}
			seen[ev["id"].(string)] = true
		}
		next, _ := out["next_cursor"].(string)
		if next == "" {
			break
		}
		if len(out["events"].([]any)) != parental.EventsPageSize {
			t.Fatalf("a page with a next cursor has %d events", len(out["events"].([]any)))
		}
		cursor = next
	}
	if len(seen) != 90 || pages != 2 {
		t.Errorf("listed %d events over %d pages; want the 90 inside 90 days, over 2 pages", len(seen), pages)
	}

	var swept time.Time
	router.ParentalActivityOnce(ctx, e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), &swept)
	if n := e.count(`SELECT count(*) FROM parental_events`); n != 90 {
		t.Errorf("events after the daily pass = %d, want 90", n)
	}
	if n := e.count(`SELECT count(*) FROM parental_events WHERE at < $1`, e.clock.Add(-parental.EventsRetention)); n != 0 {
		t.Errorf("%d events older than 90 days survived", n)
	}
	if n := e.count(`SELECT count(*) FROM parental_pin_failures`); n != 1 {
		t.Errorf("wrong tries kept = %d, want the one of the last day", n)
	}
	if !swept.Equal(e.clock) {
		t.Errorf("swept at %v", swept)
	}
	// not again the same day
	e.exec(`INSERT INTO parental_events (child_id, kind, at) VALUES ($1, 'asked', $2)`, uChild, e.clock.Add(-100*24*time.Hour))
	e.clock = e.clock.Add(23 * time.Hour)
	router.ParentalActivityOnce(ctx, e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), &swept)
	if n := e.count(`SELECT count(*) FROM parental_events`); n != 91 {
		t.Errorf("the delete ran twice in a day (%d events)", n)
	}
	e.clock = e.clock.Add(2 * time.Hour)
	router.ParentalActivityOnce(ctx, e.store, slog.New(slog.NewTextHandler(io.Discard, nil)), &swept)
	// 25 hours on: the 100-day-old one and the two that were 89 and 90 days old
	if n := e.count(`SELECT count(*) FROM parental_events`); n != 88 {
		t.Errorf("events after the next day's pass = %d, want 88", n)
	}
	// lifting the restriction takes the activity with it
	e.want(204, "admin", "DELETE", "/api/users/"+uChild+"/parental", nil)
	if n := e.count(`SELECT count(*) FROM parental_events`) + e.count(`SELECT count(*) FROM parental_requests`); n != 0 {
		t.Errorf("%d rows left after the restriction was lifted", n)
	}
}
