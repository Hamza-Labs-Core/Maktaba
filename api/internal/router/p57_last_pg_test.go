package router_test

// Epic 57, the last server changes the client work asked for, behind the production
// router against a real migrated Postgres (MAKTABA_TEST_PG_URL):
//
//   - a progress post refused because the account's time is over stores the
//     position it carries (its own, open session only), counts no time for it and
//     keeps the roles of the route as they were;
//   - the channel answers with a time to wait for carry `now`, the home's clock.

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	lastGuardian = "00000000-0000-4000-8000-000000571403"
	lastOther    = "00000000-0000-4000-8000-000000571404"
	lastAdmin    = "00000000-0000-4000-8000-000000571405"
)

// lastRoles: no one, a viewer, the restricted child, its guardian, another
// guardian, an admin.
var lastRoles = []string{"", "viewer", "child", "guardian", "other", "admin"}

// setupLastRouter is setupTimeRouter with a guardian of the child, a guardian of
// someone else and an admin.
func setupLastRouter(t *testing.T, db *sql.DB, svc *tmStreaming) *tmEnv {
	t.Helper()
	e := &tmEnv{acEnv: &acEnv{t: t, db: db}}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'last-guardian','x',false), ($2,'last-other','x',false), ($3,'last-admin','x',true)`,
		lastGuardian, lastOther, lastAdmin)
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2)`, tmChild, lastGuardian)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: svc, URLSigner: &tmSigner{},
		OnParentalTime: func(k *parental.TimeKeeper) { e.keeper = k }})
	if e.keeper == nil {
		t.Fatal("MountP6 made no time keeper")
	}
	ids := map[string]string{"viewer": tmViewer, "child": tmChild, "guardian": lastGuardian, "other": lastOther, "admin": lastAdmin}
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if who := req.Header.Get("X-Test-As"); who != "" {
			req = req.WithContext(principal.WithPrincipal(req.Context(), &principal.Principal{
				UserID: ids[who], IsAdmin: who == "admin", Libraries: []string{tmLib}, Source: principal.SourceJWT}))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func TestPGTimeStopStoresThePosition(t *testing.T) {
	db := seedTime(t)
	svc := &tmStreaming{leases: map[string]time.Time{}}
	e := setupLastRouter(t, db, svc)

	code, s := open(e, "child")
	if code != http.StatusCreated {
		t.Fatalf("opening the session: %d %v", code, s)
	}
	session, _ := s["session_id"].(string)
	path := "/api/stream/sessions/" + session + "/progress"
	position := func() float64 {
		t.Helper()
		var pos float64
		err := db.QueryRow(`SELECT position_sec FROM playback_state WHERE user_id = $1 AND video_id = $2`, tmChild, tmVideo).Scan(&pos)
		if err != nil {
			t.Fatalf("the child's resume point: %v", err)
		}
		return pos
	}
	used := func() int {
		return e.count(`SELECT COALESCE(sum(seconds), 0)::int FROM parental_time_usage WHERE user_id = $1`, tmChild)
	}
	// post sends one post as who, a debounce interval after the one before
	post := func(who string, pos float64) (int, map[string]any) {
		time.Sleep(1100 * time.Millisecond)
		return e.do(who, http.MethodPost, path, map[string]any{"position_sec": pos})
	}
	// roles asks the route as every role; want is in lastRoles' order
	roles := func(when string, want [6]int) {
		t.Helper()
		for i, who := range lastRoles {
			if code, body := post(who, float64(1000+i)); code != want[i] {
				name := who
				if name == "" {
					name = "no one"
				}
				t.Errorf("%s: a progress post as %s = %d (%v), want %d", when, name, code, body, want[i])
			}
		}
	}

	// inside the limit the route is its owner's and an admin's, as it was
	roles("inside the limit", [6]int{403, 404, 200, 404, 404, 200})
	if code, body := post("child", 40); code != http.StatusOK {
		t.Fatalf("a post inside the limit: %d %v", code, body)
	}
	if got := position(); got != 40 {
		t.Fatalf("an accepted post stored %v, want 40", got)
	}

	// the limit is reached
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, $2::date, $3, 3600)
		ON CONFLICT (user_id, day, library_id) DO UPDATE SET seconds = 3600`, tmChild, e.today(), tmLib)
	refused := func(what string, code int, body map[string]any) {
		t.Helper()
		if code != http.StatusForbidden || body["type"] != tmProblem || body["code"] != "parental_time" ||
			body["reason"] != parental.ReasonLimitReached || body["next_allowed_at"] == nil {
			t.Fatalf("%s: %d %v, want 403 parental-time limit_reached", what, code, body)
		}
	}
	before, started := used(), time.Now()
	code, body := post("child", 77.25)
	refused("the child's post at the time stop", code, body)
	if got := position(); got != 77.25 {
		t.Fatalf("the refused post stored %v, want 77.25", got)
	}
	// the play's record keeps it too, as an accepted post's would
	if n := e.count(`SELECT count(*) FROM watch_sessions WHERE stream_session_id = $1 AND user_id = $2`, session, tmChild); n != 1 {
		t.Fatalf("%d plays recorded for the session, want 1", n)
	}
	// nothing is counted for the storing: the count grew by no more than the time
	// that passed since the post before, which the beat itself counts
	if grew, passed := used()-before, int(time.Since(started)/time.Second)+2; grew > passed {
		t.Fatalf("the refused post counted %d s in %d s", grew, passed)
	}
	// and streaming holds an end that has passed: the stream is not kept alive
	if svc.end == nil || svc.end.After(time.Now()) {
		t.Fatalf("streaming holds the end %v for the account, want one that has passed", svc.end)
	}

	// the roles are as they were: who could not post still gets a missing
	// session's answer, and only the child's own post stores
	roles("at the time stop", [6]int{403, 404, 403, 404, 404, 403})
	if got := position(); got != 1002 { // the child's own post in the round above
		t.Fatalf("after every role posted at the time stop the resume point is %v, want the child's 1002", got)
	}
	code, body = post("admin", 5000)
	refused("an admin's post on the child's session", code, body)
	if got := position(); got != 1002 {
		t.Fatalf("an admin's refused post on the child's session stored %v", got)
	}

	// a closed session's refused post stores nothing
	if code, body := e.do("child", http.MethodDelete, "/api/stream/sessions/"+session, nil); code != http.StatusNoContent {
		t.Fatalf("closing the session: %d %v", code, body)
	}
	code, body = post("child", 300)
	refused("a post on the closed session", code, body)
	if got := position(); got != 1002 {
		t.Fatalf("a closed session's refused post stored %v", got)
	}
}

// nowOf reads an answer's `now` and checks it is the home's clock at the request.
func nowOf(t *testing.T, what string, out map[string]any, before time.Time) time.Time {
	t.Helper()
	raw, _ := out["now"].(string)
	now, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("%s: now = %v (%v) in %v", what, out["now"], err, out)
	}
	if now.Before(before.Truncate(time.Second)) || now.After(time.Now().Add(time.Second)) {
		t.Fatalf("%s: now = %s, want the time of the request (%s)", what, raw, before.Format(time.RFC3339Nano))
	}
	return now
}

func TestP57ChannelAnswersCarryNowPG(t *testing.T) {
	e := setupLiveTime(t)
	child := e.users["child"]
	ch := "/api/channels/"

	// allowed: access and play say until when, and the home's time now
	for _, who := range []string{"child", "viewer", "guardian", "other", "admin"} {
		before := time.Now()
		code, _, out := e.do(who, "GET", ch+e.ch9+"/access", "")
		if code != 200 {
			t.Fatalf("access as %s = %d %v", who, code, out)
		}
		now := nowOf(t, "access as "+who, out, before)
		if who == "child" && !now.Before(e.boundary) {
			t.Fatalf("access: now %s is not before until %s", now, e.boundary)
		}
		before = time.Now()
		code, _, out = e.do(who, "POST", ch+e.ch9+"/play", "")
		if code != 200 {
			t.Fatalf("play as %s = %d %v", who, code, out)
		}
		nowOf(t, "play as "+who, out, before)
	}
	// no one gets no clock either
	if code, _, out := e.do("", "GET", ch+e.ch9+"/access", ""); code == 200 || out["now"] != nil {
		t.Fatalf("access as no one = %d %v", code, out)
	}
	// a channel the child may not see is a missing one, with nothing to wait for
	if code, _, out := e.do("child", "GET", ch+e.ch16+"/access", ""); code != 404 || out["now"] != nil {
		t.Fatalf("access to a hidden channel = %d %v", code, out)
	}

	// a programme above the limit: access says not allowed until the boundary, play
	// refuses with retry_at, and both carry now
	before := time.Now()
	_, _, out := e.do("child", "GET", ch+e.chNow+"/access", "")
	if out["allowed"] != false || out["until"] != e.boundary.Format(time.RFC3339) {
		t.Fatalf("access while a 16 programme plays = %v", out)
	}
	nowOf(t, "access while a 16 programme plays", out, before)
	before = time.Now()
	code, _, out := e.do("child", "POST", ch+e.chNow+"/play", "")
	if code != 403 || out["code"] != "restricted" || out["retry_at"] != e.boundary.Format(time.RFC3339) {
		t.Fatalf("play while a 16 programme plays = %d %v", code, out)
	}
	if now := nowOf(t, "the programme refusal", out, before); !now.Before(e.boundary) {
		t.Fatalf("the programme refusal: now %s is not before retry_at %s", now, e.boundary)
	}

	// the time is over: both refuse with next_allowed_at, and now
	e.exec(`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, child)
	e.reach(3600)
	for _, path := range []string{"/access", "/play"} {
		method := map[string]string{"/access": "GET", "/play": "POST"}[path]
		before := time.Now()
		code, _, out := e.do("child", method, ch+e.ch9+path, "")
		if code != 403 || out["code"] != "parental_time" {
			t.Fatalf("%s with the time over = %d %v", path, code, out)
		}
		next, err := time.Parse(time.RFC3339, out["next_allowed_at"].(string))
		if err != nil {
			t.Fatalf("%s: next_allowed_at = %v", path, out["next_allowed_at"])
		}
		if now := nowOf(t, path+" with the time over", out, before); !now.Before(next) {
			t.Fatalf("%s: now %s is not before next_allowed_at %s", path, now, next)
		}
	}
}
