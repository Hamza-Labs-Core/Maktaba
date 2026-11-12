package router_test

// A request for more time behind the production router (Epic 57 stories 57.06
// and 57.14), against a real migrated Postgres (MAKTABA_TEST_PG_URL): a child
// whose stream is refused asks, a guardian approves, and the stream is allowed
// again at once, with streaming told the new end.

import (
	"net/http"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	tmGuardian = "00000000-0000-4000-8000-000000571403"
	tmOtherG   = "00000000-0000-4000-8000-000000571404"
	tmChild2   = "00000000-0000-4000-8000-000000571405"
	tmAdmin    = "00000000-0000-4000-8000-000000571406"
)

// setupTimeRequests is setupTimeRouter with the ask routes mounted as main.go
// mounts them (the keeper MountP6 made), and the child's guardian, another child's
// guardian and an admin signed in.
func setupTimeRequests(t *testing.T, svc *tmStreaming) *tmEnv {
	t.Helper()
	db := seedTime(t)
	e := &tmEnv{acEnv: &acEnv{t: t, db: db}}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'tm-guardian','x',false),
		($2,'tm-other-guardian','x',false), ($3,'tm-child-two','x',false), ($4,'tm-admin','x',true)`,
		tmGuardian, tmOtherG, tmChild2, tmAdmin)
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, tmChild2)
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1,$2), ($3,$4)`, tmChild, tmGuardian, tmChild2, tmOtherG)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	var cache *parental.Cache
	router.MountP6(r, router.P6Deps{DB: db, StreamingClient: svc, URLSigner: &tmSigner{},
		OnParental:     func(c *parental.Cache) { cache = c },
		OnParentalTime: func(k *parental.TimeKeeper) { e.keeper = k }})
	if e.keeper == nil || cache == nil {
		t.Fatal("MountP6 made no time keeper or no cache")
	}
	router.MountP29(r, router.P29Deps{DB: db, Driver: "postgres", Time: e.keeper})
	router.MountP57Requests(r, router.P57RequestsDeps{DB: db, Cache: cache, Time: e.keeper})
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "child":
			p = &principal.Principal{UserID: tmChild, Libraries: []string{tmLib}, Source: principal.SourceJWT}
		case "viewer":
			p = &principal.Principal{UserID: tmViewer, Libraries: []string{tmLib}, Source: principal.SourceJWT}
		case "guardian":
			p = &principal.Principal{UserID: tmGuardian, Source: principal.SourceJWT}
		case "other-guardian":
			p = &principal.Principal{UserID: tmOtherG, Source: principal.SourceJWT}
		case "admin":
			p = &principal.Principal{UserID: tmAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func TestPGTimeRequestApprovedAllowsTheStream(t *testing.T) {
	svc := &tmStreaming{leases: map[string]time.Time{}}
	e := setupTimeRequests(t, svc)

	// a stream is running, then the limit is reached
	code, s := open(e, "child")
	if code != http.StatusCreated {
		t.Fatalf("inside the limit: %d %v", code, s)
	}
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, $2::date, $3, 3600)
		ON CONFLICT (user_id, day, library_id) DO UPDATE SET seconds = 3600`, tmChild, e.today(), tmLib)
	code, body := open(e, "child")
	if code != http.StatusForbidden || body["type"] != tmProblem || body["reason"] != parental.ReasonLimitReached {
		t.Fatalf("with the limit reached: %d %v, want 403 parental-time", code, body)
	}
	if svc.end == nil || svc.end.After(time.Now()) {
		t.Fatalf("streaming holds the end %v, want one that has passed", svc.end)
	}

	// browsing stays open, so the child can ask; the role of each step is the server's
	const asks, list = "/api/me/parental/requests", "/api/parental/requests"
	time30 := map[string]any{"subject_kind": "time", "subject_id": "30"}
	for as, want := range map[string]int{"": 401, "viewer": 403, "guardian": 403, "other-guardian": 403, "admin": 403} {
		if code, out := e.do(as, http.MethodPost, asks, time30); code != want {
			t.Errorf("asking for time as %q = %d, want %d (%v)", as, code, want, out)
		}
	}
	req, _ := e.want(http.StatusCreated, "child", http.MethodPost, asks, time30)["request"].(map[string]any)
	id, _ := req["id"].(string)
	if id == "" || req["subject_kind"] != "time" || req["minutes"] != float64(30) {
		t.Fatalf("the request = %v", req)
	}
	for as, want := range map[string]int{"": 401, "viewer": 403, "child": 403, "guardian": 200, "other-guardian": 200, "admin": 200} {
		code, out := e.do(as, http.MethodGet, list, nil)
		if code != want {
			t.Errorf("listing requests as %q = %d, want %d (%v)", as, code, want, out)
			continue
		}
		if code == 200 {
			n := len(out["requests"].([]any))
			if (as == "other-guardian") != (n == 0) {
				t.Errorf("%q sees %d requests", as, n)
			}
		}
	}
	approve := "/api/parental/requests/" + id + "/approve"
	for as, want := range map[string]int{"": 401, "viewer": 403, "child": 403, "other-guardian": 404} {
		if code, out := e.do(as, http.MethodPost, approve, map[string]any{"minutes": 30}); code != want {
			t.Errorf("approving as %q = %d, want %d (%v)", as, code, want, out)
		}
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants WHERE user_id = $1`, tmChild); n != 0 {
		t.Fatalf("%d grants after refused approvals", n)
	}
	if code, body := open(e, "child"); code != http.StatusForbidden {
		t.Fatalf("still waiting: %d %v, want 403", code, body)
	}

	// the guardian approves: the grant is in the answer, streaming holds a new end
	// that has not passed, and the stream that was refused is allowed again
	out := e.want(http.StatusOK, "guardian", http.MethodPost, approve, nil)
	grant, _ := out["grant"].(map[string]any)
	if grant == nil || grant["minutes"] != float64(30) || grant["granted_by"] != tmGuardian || grant["day"] != e.today() {
		t.Fatalf("the approval's grant = %v", out)
	}
	// about 30 minutes from now; when those minutes reach the day line (04:00 home
	// time, here the server's zone) the new day's own 60 minutes follow on, so the
	// end is that much later
	now := time.Now()
	latest := now.Add(31 * time.Minute)
	line := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, now.Location())
	if !line.After(now) {
		line = line.AddDate(0, 0, 1)
	}
	if !line.After(latest) {
		latest = line.Add(61 * time.Minute)
	}
	if svc.end == nil || !svc.end.After(now.Add(25*time.Minute)) || svc.end.After(latest) {
		t.Fatalf("after the approval streaming holds the end %v, want about 30 minutes from now (no later than %v)", svc.end, latest)
	}
	code, s = open(e, "child")
	if code != http.StatusCreated {
		t.Fatalf("after the approval: %d %v, want the stream allowed again", code, s)
	}
	mine := e.want(http.StatusOK, "child", http.MethodGet, "/api/me/parental/time?library_id="+tmLib, nil)
	if mine["allowed"] != true {
		t.Fatalf("own time after the approval: %v", mine)
	}
	// the activity shows it to the guardian and the admin only
	activity := "/api/users/" + tmChild + "/parental/activity"
	for as, want := range map[string]int{"": 401, "viewer": 404, "child": 404, "guardian": 200, "other-guardian": 404, "admin": 200} {
		if code, out := e.do(as, http.MethodGet, activity, nil); code != want {
			t.Errorf("the activity as %q = %d, want %d (%v)", as, code, want, out)
		}
	}

	// denied: nothing is given
	e.exec(`UPDATE parental_time_grants SET used_seconds = minutes * 60 WHERE user_id = $1`, tmChild)
	if code, body := open(e, "child"); code != http.StatusForbidden {
		t.Fatalf("with the grant used up: %d %v, want 403", code, body)
	}
	req, _ = e.want(http.StatusCreated, "child", http.MethodPost, asks, map[string]any{"subject_kind": "time", "subject_id": "15"})["request"].(map[string]any)
	deny := "/api/parental/requests/" + req["id"].(string) + "/deny"
	for as, want := range map[string]int{"": 401, "viewer": 403, "child": 403, "other-guardian": 404} {
		if code, out := e.do(as, http.MethodPost, deny, nil); code != want {
			t.Errorf("denying as %q = %d, want %d (%v)", as, code, want, out)
		}
	}
	e.want(http.StatusOK, "admin", http.MethodPost, deny, nil)
	if code, body := open(e, "child"); code != http.StatusForbidden {
		t.Fatalf("after a denial: %d %v, want 403", code, body)
	}
	if n := e.count(`SELECT count(*) FROM parental_time_grants WHERE user_id = $1`, tmChild); n != 1 {
		t.Fatalf("%d grants, want the one approved", n)
	}
}
