package parental_test

// The device's signal routes against a real migrated Postgres (story 57.04): each
// route as no one, a viewer, the restricted child, that child's guardian, another
// child's guardian and an admin, and what each does.

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

var sigUsers = map[string]string{"viewer": uViewer, "child": uChild, "guardian": uGuardian, "other-guardian": uOtherG, "admin": uAdmin}

// sigEnv is the routes with callers that are app sign-ins: "<who>" is that
// account's own sign-in (fam[who]); "<who>@<family>" names another of its
// sign-ins; "<who>!web" is its web session; "<who>#<n>" carries the claim ac=n;
// "<who>+play" came through a cloud link that may only play.
type sigEnv struct {
	*env
	fam map[string]string
}

func setupSignal(t *testing.T) *sigEnv {
	t.Helper()
	e := &sigEnv{env: setup(t), fam: map[string]string{}}
	for who, id := range sigUsers {
		e.fam[who] = e.family(id)
	}
	store := parental.NewStore(e.db)
	h := &parentalh.Handler{Store: store, Cache: parental.NewCache(store)}
	r := chi.NewRouter()
	h.Mount(r)
	h.MountSignal(r)
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		as := req.Header.Get("X-Test-As")
		var p *principal.Principal
		if as == "admin-token" {
			p = &principal.Principal{UserID: "admin-token", IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceAdminToken}
		} else if as != "" {
			who, mod := as, ""
			for i, c := range as {
				if c == '@' || c == '!' || c == '#' || c == '+' {
					who, mod = as[:i], as[i:]
					break
				}
			}
			p = &principal.Principal{UserID: sigUsers[who], IsAdmin: who == "admin", Source: principal.SourceJWT, RefreshFamily: e.fam[who]}
			switch {
			case mod == "":
			case mod[0] == '@':
				p.RefreshFamily = mod[1:]
			case mod == "!web":
				p.Source, p.RefreshFamily = principal.SourceCookie, ""
			case mod[0] == '#':
				n, _ := strconv.Atoi(mod[1:])
				p.AgeCeiling = &n
			case mod == "+play":
				p.CloudScope = principal.CloudScopePlay
			}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

var famSeq int

func (e *env) family(user string) string {
	e.t.Helper()
	famSeq++
	fam := fmt.Sprintf("00000000-0000-4000-8000-0000572f%04d", famSeq)
	e.exec(`INSERT INTO refresh_tokens (id, user_id, hash, family_id, expires_at)
	        VALUES (gen_random_uuid(), $1, $2, $3, now() + interval '30 days')`, user, "h-"+fam, fam)
	return fam
}

func clearPath(user, fam string) string {
	return "/api/users/" + user + "/parental/signals/" + fam + "/clear"
}

type obj = map[string]any

// Every signal route, as every caller. The columns are who: no one, a viewer, the
// child, the child's guardian, another child's guardian, an admin.
func TestSignalRoles(t *testing.T) {
	e := setupSignal(t)
	pg13 := obj{"movie_ceiling": 300}

	// POST /api/me/age-signal: anyone signed in, about their own sign-in
	for i, want := range [6]int{401, 200, 200, 200, 200, 200} {
		out := e.want(want, who[i], "POST", "/api/me/age-signal", pg13)
		if want == 200 && (out["tier_limit"] != float64(13) || out["refresh"] != true || out["pending_looser"] != false) {
			t.Fatalf("age-signal as %q = %v", who[i], out)
		}
	}
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE tier_limit = 13`); n != 5 {
		t.Fatalf("signals stored = %d, want one for each of the five accounts", n)
	}
	e.exec(`DELETE FROM session_age_signals`)

	// POST .../signals/{family}/clear
	seed := func() {
		t.Helper()
		e.exec(`DELETE FROM session_age_signals`)
		for _, w := range []string{"child", "viewer"} {
			e.want(200, w, "POST", "/api/me/age-signal", pg13)
			if out := e.want(200, w, "POST", "/api/me/age-signal", obj{"movie_ceiling": 400}); out["pending_looser"] != true || out["tier_limit"] != float64(13) {
				t.Fatalf("looser as %q = %v", w, out)
			}
		}
	}
	held := func(user string) int {
		return e.count(`SELECT count(*) FROM session_age_signals WHERE user_id = $1 AND tier_limit = 13 AND pending_looser IS NOT NULL`, user)
	}
	for _, c := range []struct {
		name, who string
		want      [6]int
	}{
		// the child's signal: an admin and the child's guardian; the child gets 403
		// `restricted`, another child's guardian 404
		{"a restricted child's signal", "child", [6]int{401, 403, 403, 200, 404, 200}},
		// an account with no policy has no guardian: only an admin
		{"an unrestricted account's signal", "viewer", [6]int{401, 403, 403, 404, 404, 200}},
	} {
		for i, want := range c.want {
			seed()
			code, out := e.do(who[i], "POST", clearPath(sigUsers[c.who], e.fam[c.who]), nil)
			if code != want {
				t.Fatalf("clear %s as %q = %d, want %d (%v)", c.name, who[i], code, want, out)
			}
			if want == 200 {
				if out["tier_limit"] != float64(16) || out["removed"] != false || held(sigUsers[c.who]) != 0 {
					t.Fatalf("clear %s as %q: %v", c.name, who[i], out)
				}
			} else if held(sigUsers[c.who]) != 1 {
				t.Fatalf("clear %s as %q was refused with %d and still changed the signal", c.name, who[i], code)
			}
			if who[i] == "child" && out["code"] != "restricted" {
				t.Fatalf("clear %s as the child: code %v, want restricted", c.name, out["code"])
			}
		}
	}
}

// T06 over HTTP, and the edges of the two routes.
func TestSignalRoutes(t *testing.T) {
	e := setupSignal(t)
	fam := e.fam["child"]

	// what cannot be read is refused, field by field, and stores nothing
	for _, body := range []obj{
		{"movie_ceiling": 1001}, {"tv_ceiling": -1}, {"region": "United States"},
		{"age_range": obj{"lower": 13, "upper": 9}}, {"age_range": obj{"upper": 400}},
		{"age_range": obj{"declaration": "Self Declared"}},
	} {
		e.want(422, "child", "POST", "/api/me/age-signal", body)
	}
	// a credential that is no app sign-in has no device to speak for
	for _, as := range []string{"child!web", "child@", "child@00000000-0000-4000-8000-00000000dead", "child@" + e.fam["viewer"], "admin-token"} {
		if out := e.want(409, as, "POST", "/api/me/age-signal", obj{"movie_ceiling": 300}); out["code"] != "no_sign_in" {
			t.Fatalf("age-signal as %q: %v", as, out)
		}
	}
	if n := e.count(`SELECT count(*) FROM session_age_signals`); n != 0 {
		t.Fatalf("refused signals stored %d rows", n)
	}

	// the strictest of three, then tighter, then looser
	out := e.want(200, "child", "POST", "/api/me/age-signal", obj{
		"age_range": obj{"lower": 13, "upper": 15, "declaration": "guardian_declared"}, "movie_ceiling": 400, "tv_ceiling": 500, "region": "US"})
	if out["tier_limit"] != float64(13) {
		t.Fatalf("first = %v", out)
	}
	// the account is 9+: its limit stays 9, and the device's is shown beside it
	me := e.want(200, "child", "GET", "/api/me/parental", nil)
	if me["restricted"] != true || me["max_tier"] != float64(9) || me["device_limit"] != float64(13) {
		t.Fatalf("me = %v", me)
	}
	out = e.want(200, "child", "POST", "/api/me/age-signal", obj{"age_range": obj{"lower": 5, "upper": 9}})
	if out["tier_limit"] != float64(0) || out["pending_looser"] != false {
		t.Fatalf("tighter = %v", out)
	}
	if me := e.want(200, "child", "GET", "/api/me/parental", nil); me["max_tier"] != float64(0) {
		t.Fatalf("me after a tighter device = %v", me)
	}
	out = e.want(200, "child", "POST", "/api/me/age-signal", obj{"age_range": obj{"lower": 18}, "movie_ceiling": 1000, "tv_ceiling": 1000})
	if out["tier_limit"] != float64(0) || out["pending_looser"] != true || out["refresh"] != true {
		t.Fatalf("looser = %v", out)
	}
	if me := e.want(200, "child", "GET", "/api/me/parental", nil); me["max_tier"] != float64(0) || me["device_pending"] != true {
		t.Fatalf("me with a looser statement waiting = %v", me)
	}
	// the guardian sees it on the child's policy
	pol := e.want(200, "guardian", "GET", "/api/users/"+uChild+"/parental", nil)
	sigs, _ := pol["signals"].([]any)
	if len(sigs) != 1 || sigs[0].(obj)["refresh_family_id"] != fam || sigs[0].(obj)["pending_looser"] == nil {
		t.Fatalf("signals on the policy = %v", pol["signals"])
	}

	// who cannot clear: the child (403), a guardian whose own device is limited,
	// a cloud sign-in that may only play
	if out := e.want(403, "child", "POST", clearPath(uChild, fam), nil); out["code"] != "restricted" {
		t.Fatalf("clear by the child = %v", out)
	}
	if out := e.want(403, "guardian#9", "POST", clearPath(uChild, fam), nil); out["code"] != "device_limited" {
		t.Fatalf("clear from a limited token = %v", out)
	}
	e.want(200, "guardian", "POST", "/api/me/age-signal", obj{"tv_ceiling": 400})
	if out := e.want(403, "guardian", "POST", clearPath(uChild, fam), nil); out["code"] != "device_limited" {
		t.Fatalf("clear from a limited device = %v", out)
	}
	e.want(403, "guardian+play", "POST", clearPath(uChild, fam), nil)
	if n := e.count(`SELECT count(*) FROM session_age_signals WHERE refresh_family_id = $1 AND tier_limit = 0 AND pending_looser IS NOT NULL`, fam); n != 1 {
		t.Fatal("a refused clear changed the signal")
	}
	// the guardian, from a device with no limit, clears: the adult statement applies
	other := e.family(uGuardian)
	out = e.want(200, "guardian@"+other, "POST", clearPath(uChild, fam), nil)
	if out["tier_limit"] != nil || out["removed"] != false {
		t.Fatalf("clear by the guardian = %v", out)
	}
	if me := e.want(200, "child", "GET", "/api/me/parental", nil); me["max_tier"] != float64(9) || me["device_limit"] != nil || me["device_pending"] != nil {
		t.Fatalf("me after the clear = %v", me)
	}
	// nothing waits now: the admin token's clear removes the signal; then there is none
	if out := e.want(200, "admin-token", "POST", clearPath(uChild, fam), nil); out["removed"] != true {
		t.Fatalf("clear with nothing waiting = %v", out)
	}
	e.want(404, "admin", "POST", clearPath(uChild, fam), nil)
	e.want(404, "admin", "POST", clearPath(uChild, "not-a-family"), nil)
	e.want(404, "admin", "POST", clearPath("nobody", fam), nil)
	// the child's sign-in is not the viewer's to name
	e.want(200, "child", "POST", "/api/me/age-signal", obj{"movie_ceiling": 300})
	e.want(404, "admin", "POST", clearPath(uViewer, fam), nil)

	// an account with no policy: its sign-in is limited, and it is not "restricted"
	e.want(200, "viewer", "POST", "/api/me/age-signal", obj{"age_range": obj{"lower": 9, "upper": 12}})
	me = e.want(200, "viewer", "GET", "/api/me/parental", nil)
	if me["restricted"] != false || me["max_tier"] != float64(9) || me["device_limit"] != float64(9) || me["unrated"] != "hide" {
		t.Fatalf("me on an open account with a signal = %v", me)
	}
	if me := e.want(200, "viewer@"+e.family(uViewer), "GET", "/api/me/parental", nil); me["restricted"] != false || me["max_tier"] != nil {
		t.Fatalf("me on the open account's other sign-in = %v", me)
	}
}
