package router_test

// Review findings 1 and 11 on live TV, behind the production router against a real
// migrated Postgres: a channel counts toward the time rules and is refused by them,
// and the streaming service's verdict is held to the sign-in's device limit.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/signedurl"
	parentalh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

var liveTimeProblem = parentalh.TypeParentalTime

type liveTimeEnv struct {
	*liveEnv
	cache  *parental.Cache
	keeper *parental.TimeKeeper
}

// setupLiveTime is setupLive with the time keeper wired into the live TV routes, as
// main.go wires it. X-Test-Family and X-Test-Ac give the test's principal a sign-in.
func setupLiveTime(t *testing.T) *liveTimeEnv {
	t.Helper()
	e := &liveTimeEnv{liveEnv: setupLive(t)}
	e.exec(`UPDATE app_settings SET value = 'true' WHERE key = 'parental.enabled'`)
	e.cache = parental.NewCache(parental.NewStore(e.db))
	e.keeper = parental.NewTimeKeeper(e.cache, nil)
	key, _, err := signedurl.LoadOrCreateKey(filepath.Join(t.TempDir(), "stream.key"))
	if err != nil {
		t.Fatal(err)
	}
	gate := parental.NewGate()
	gate.SetCache(e.cache)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore(), Parental: gate})
	router.MountP27(r, router.P27Deps{DB: e.db, DataDir: t.TempDir(), ChannelSigner: signedurl.FromKey(key),
		Parental: e.cache, Time: e.keeper})
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if who := req.Header.Get("X-Test-As"); who != "" {
			p := &principal.Principal{UserID: e.users[who], IsAdmin: who == "admin", AccessAllLibraries: true,
				Source: principal.SourceJWT, RefreshFamily: req.Header.Get("X-Test-Family")}
			if ac := req.Header.Get("X-Test-Ac"); ac != "" {
				n, _ := strconv.Atoi(ac)
				p.AgeCeiling = &n
			}
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *liveTimeEnv) verdict(query string) (int, map[string]any) {
	e.t.Helper()
	code, _, out := e.do("", "GET", parentalh.VerdictPath+"?"+query, "", mw.LocalTokenHeader, e.token)
	return code, out
}

func (e *liveTimeEnv) usedSeconds(user string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COALESCE(sum(seconds), 0)::int FROM parental_time_usage WHERE user_id = $1`, user).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// reach sets the child's count for today to seconds.
func (e *liveTimeEnv) reach(seconds int) {
	e.t.Helper()
	loc, err := e.cache.Store().HomeZone(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	e.exec(`DELETE FROM parental_time_usage WHERE user_id = $1`, e.users["child"])
	e.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, $2::date, $3, $4)`,
		e.users["child"], parental.DayKey(time.Now(), loc), parental.NoLibrary, seconds)
}

// Finding 1: live TV counts like any playback and is refused by the same rules.
func TestP57LiveTimePG(t *testing.T) {
	e := setupLiveTime(t)
	child, viewer := e.users["child"], e.users["viewer"]
	e.exec(`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, child)
	q := "channel=" + e.ch9 + "&user=" + child

	// inside the limit: the verdict says so, and when the stream must end
	code, v := e.verdict(q)
	if code != 200 || v["time_allowed"] != true || v["allowed"] != true || v["time_reason"] != nil {
		t.Fatalf("inside the limit: %d %v", code, v)
	}
	if at, err := time.Parse(time.RFC3339, v["time_ends_at"].(string)); err != nil || time.Until(at) < 59*time.Minute || time.Until(at) > 61*time.Minute {
		t.Fatalf("time_ends_at = %v, want an hour from now", v["time_ends_at"])
	}
	for _, path := range []string{"/access", "/play"} {
		method := map[string]string{"/access": "GET", "/play": "POST"}[path]
		if code, raw, _ := e.do("child", method, "/api/channels/"+e.ch9+path, ""); code != 200 {
			t.Fatalf("%s inside the limit = %d %s", path, code, raw)
		}
	}

	// what the streaming service says it served is counted, from the stream alone
	if code, v = e.verdict(q + "&served=120"); code != 200 || v["time_allowed"] != true {
		t.Fatalf("a served report: %d %v", code, v)
	}
	if got := e.usedSeconds(child); got != 120 {
		t.Fatalf("120 s of live TV served: %d s counted", got)
	}
	if at, _ := time.Parse(time.RFC3339, v["time_ends_at"].(string)); time.Until(at) > 58*time.Minute+30*time.Second {
		t.Fatalf("after 2 minutes of live TV the stream still ends at %v", at)
	}
	// one report never adds more than the keeper's bound, and a viewer is not counted
	e.verdict(q + "&served=86400")
	if got := e.usedSeconds(child); got != 120+int(parental.MaxLiveServed/time.Second) {
		t.Fatalf("an absurd report counted to %d s", got)
	}
	if code, v = e.verdict("channel=" + e.ch9 + "&user=" + viewer + "&served=300"); code != 200 || v["time_allowed"] != true || v["time_ends_at"] != nil {
		t.Fatalf("a viewer: %d %v", code, v)
	}
	if got := e.usedSeconds(viewer); got != 0 {
		t.Fatalf("an unrestricted viewer was counted %d s", got)
	}
	for _, bad := range []string{"served=-1", "served=abc", "served=1.5", "served=86401"} {
		if code, _ := e.verdict(q + "&" + bad); code != 400 {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}

	// the limit is reached by the last seconds served: the verdict refuses on time,
	// and access and play answer the parental-time problem, not the programme's
	e.reach(3590)
	code, v = e.verdict(q + "&served=15")
	if code != 200 || v["time_allowed"] != false || v["time_reason"] != parental.ReasonLimitReached || v["allowed"] != true {
		t.Fatalf("the limit reached: %d %v", code, v)
	}
	if at, err := time.Parse(time.RFC3339, v["time_next_allowed_at"].(string)); err != nil || !at.After(time.Now()) {
		t.Fatalf("time_next_allowed_at = %v", v["time_next_allowed_at"])
	}
	var noted int
	if err := e.db.QueryRow(`SELECT count(*) FROM parental_events WHERE child_id = $1 AND kind = 'time_up'`, child).Scan(&noted); err != nil || noted != 1 {
		t.Fatalf("%d time_up events (%v), want 1", noted, err)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/channels/" + e.ch9 + "/access"}, {"POST", "/api/channels/" + e.ch9 + "/play"},
		{"POST", "/api/channels/" + e.chNow + "/play"}, // a 16 programme too: the time's answer comes first
	} {
		code, _, body := e.do("child", c.method, c.path, "")
		if code != 403 || body["type"] != liveTimeProblem || body["code"] != "parental_time" ||
			body["reason"] != parental.ReasonLimitReached || body["next_allowed_at"] == nil || body["url"] != nil {
			t.Errorf("%s %s with the limit reached = %d %v, want 403 parental-time", c.method, c.path, code, body)
		}
	}
	// a channel the child may not see stays a 404, and nobody else is touched
	if code, _, _ := e.do("child", "POST", "/api/channels/"+e.ch16+"/play", ""); code != 404 {
		t.Errorf("a hidden channel with the limit reached = %d, want 404", code)
	}
	for _, who := range []string{"viewer", "guardian", "admin"} {
		if code, raw, _ := e.do(who, "POST", "/api/channels/"+e.ch9+"/play", ""); code != 200 {
			t.Errorf("%s with the child's limit reached = %d %s", who, code, raw)
		}
	}

	// a grant opens it again
	if _, err := e.cache.Store().GrantTime(context.Background(), e.users["guardian"], child, 30); err != nil {
		t.Fatal(err)
	}
	if code, v = e.verdict(q); code != 200 || v["time_allowed"] != true {
		t.Fatalf("after a grant: %d %v", code, v)
	}
	if code, _, _ := e.do("child", "POST", "/api/channels/"+e.ch9+"/play", ""); code != 200 {
		t.Errorf("play after a grant = %d", code)
	}
}

// failingTime is a keeper that cannot read the time.
type failingTime struct{}

var errNoClock = errors.New("the time cannot be read")

func (failingTime) Time(context.Context, *principal.Principal, string) (parental.TimeState, error) {
	return parental.TimeState{}, errNoClock
}

func (failingTime) LiveServed(context.Context, string, string, time.Duration) (parental.TimeState, error) {
	return parental.TimeState{}, errNoClock
}

func (failingTime) RemoteBeat(context.Context, string, string, string, time.Time) (parental.TimeState, error) {
	return parental.TimeState{}, errNoClock
}

// Fail closed: a time that cannot be read refuses the channel and the verdict.
func TestP57LiveTimeUnknownPG(t *testing.T) {
	e := setupLiveTime(t)
	live := parental.NewLiveTV(e.db, e.cache)
	r := chi.NewRouter()
	(&parentalh.LiveHandler{DB: e.db, Live: live, Time: failingTime{}}).Mount(r)
	ask := func(method, path string, p *principal.Principal, hdr ...string) (int, map[string]any, http.Header) {
		req := httptest.NewRequest(method, path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out, rec.Header()
	}
	child := &principal.Principal{UserID: e.users["child"], AccessAllLibraries: true}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/channels/" + e.ch9 + "/access"}, {"POST", "/api/channels/" + e.ch9 + "/play"},
	} {
		code, body, hdr := ask(c.method, c.path, child)
		if code != 503 || body["code"] != "parental_time_unknown" || hdr.Get("Retry-After") == "" || body["url"] != nil {
			t.Errorf("%s %s with the time unknown = %d %v", c.method, c.path, code, body)
		}
	}
	code, body, _ := ask("GET", parentalh.VerdictPath+"?channel="+e.ch9+"&user="+e.users["child"], nil, mw.LocalTokenHeader, e.token)
	if code != 503 || body["time_allowed"] != nil {
		t.Errorf("the verdict with the time unknown = %d %v, want 503 and no answer", code, body)
	}
	gate := parentalh.RemoteGate(e.db, live, failingTime{})
	req := httptest.NewRequest("POST", "/api/tv/remote/tune", nil)
	if err := gate(req, child, e.ch9, ""); err == nil || err.Status != 503 {
		t.Errorf("a remote tune with the time unknown = %v, want 503", err)
	}
}

// Finding 1, away from home: a remote viewing session's calls are counted, and the
// time rules refuse its tune and its heartbeat.
func TestP57LiveRemoteTimePG(t *testing.T) {
	e := setupLiveTime(t)
	child := e.users["child"]
	e.exec(`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, child)
	gate := parentalh.RemoteGate(e.db, parental.NewLiveTV(e.db, e.cache), e.keeper)
	ask := func(who, channel, session string) (int, string) {
		req := httptest.NewRequest("POST", "/api/tv/remote/tune", nil)
		p := &principal.Principal{UserID: e.users[who], IsAdmin: who == "admin", AccessAllLibraries: true}
		if err := gate(req, p, channel, session); err != nil {
			return err.Status, err.Type
		}
		return 0, ""
	}
	if code, _ := ask("child", e.ch9, ""); code != 0 {
		t.Fatalf("a first tune = %d", code)
	}
	if got := e.usedSeconds(child); got != 0 {
		t.Fatalf("a first tune counted %d s", got)
	}
	// the session the tune made, last heard from 40 seconds ago
	session := uuid.NewString()
	e.exec(`INSERT INTO tv_remote_sessions (id, user_id, channel_id, device, last_beat_at) VALUES ($1, $2, $3, 'tablet', now() - interval '40 seconds')`,
		session, child, e.ch9)
	if code, _ := ask("child", "", session); code != 0 {
		t.Fatalf("a heartbeat = %d", code)
	}
	first := e.usedSeconds(child)
	if first < 39 || first > 45 {
		t.Fatalf("a heartbeat 40 s after the last counted %d s", first)
	}
	// a second call at once adds nothing, whatever the session's row says: a client
	// that tunes again instead of sending heartbeats is counted the same
	e.exec(`UPDATE tv_remote_sessions SET last_beat_at = now() - interval '30 seconds' WHERE id = $1`, session)
	if code, _ := ask("child", e.ch9, ""); code != 0 {
		t.Fatalf("a second tune = %d", code)
	}
	if got := e.usedSeconds(child); got > first+2 {
		t.Fatalf("a tune right after a heartbeat counted %d s more", got-first)
	}
	// a long silence is capped, and a session that ended is not counted
	e.keeper = parental.NewTimeKeeper(e.cache, nil) // the API restarted: no marks
	gate = parentalh.RemoteGate(e.db, parental.NewLiveTV(e.db, e.cache), e.keeper)
	e.exec(`UPDATE tv_remote_sessions SET last_beat_at = now() - interval '3 hours' WHERE id = $1`, session)
	before := e.usedSeconds(child)
	ask("child", "", session)
	if got := e.usedSeconds(child) - before; got != int(parental.RemoteBeatCap/time.Second) {
		t.Fatalf("a heartbeat after 3 hours counted %d s, want the cap", got)
	}
	ended := uuid.NewString()
	e.exec(`INSERT INTO tv_remote_sessions (id, user_id, channel_id, device, last_beat_at, ended_at) VALUES ($1, $2, $3, 'phone', now() - interval '50 seconds', now())`,
		ended, child, e.ch9)
	before = e.usedSeconds(child)
	ask("child", "", ended)
	if got := e.usedSeconds(child) - before; got > 2 {
		t.Fatalf("an ended session counted %d s", got)
	}
	// a viewer's session is never counted
	vs := uuid.NewString()
	e.exec(`INSERT INTO tv_remote_sessions (id, user_id, channel_id, device, last_beat_at) VALUES ($1, $2, $3, 'tv', now() - interval '40 seconds')`,
		vs, e.users["viewer"], e.ch9)
	if code, _ := ask("viewer", "", vs); code != 0 || e.usedSeconds(e.users["viewer"]) != 0 {
		t.Fatalf("a viewer's heartbeat = %d, %d s counted", code, e.usedSeconds(e.users["viewer"]))
	}

	// the limit is reached: tune and heartbeat answer parental-time
	e.reach(3600)
	if code, typ := ask("child", e.ch9, ""); code != 403 || typ != liveTimeProblem {
		t.Errorf("a tune with the limit reached = %d %s", code, typ)
	}
	if code, typ := ask("child", "", session); code != 403 || typ != liveTimeProblem {
		t.Errorf("a heartbeat with the limit reached = %d %s", code, typ)
	}
	if code, _ := ask("child", e.ch16, ""); code != 404 {
		t.Errorf("a hidden channel with the limit reached = %d, want 404", code)
	}
	if code, _ := ask("viewer", e.ch9, ""); code != 0 {
		t.Errorf("a viewer with the child's limit reached = %d", code)
	}
}

// Finding 11: the verdict is held to the sign-in's device limit as well as to the
// account's policy, and the channel URL carries the sign-in.
func TestP57LiveDeviceLimitPG(t *testing.T) {
	e := setupLiveTime(t)
	viewer, child := e.users["viewer"], e.users["child"]
	fam, childFam, gone := uuid.NewString(), uuid.NewString(), uuid.NewString()
	// the viewer's account has no policy; the device it is signed in on is a
	// child's, limited to 9+. The child's own device is limited to All ages.
	e.exec(`INSERT INTO session_age_signals (refresh_family_id, user_id, tier_limit) VALUES ($1, $2, 9), ($3, $4, 0)`,
		fam, viewer, childFam, child)

	type want struct{ restricted, channel, allowed bool }
	for _, c := range []struct {
		name, query string
		want        want
	}{
		{"the account alone: no limit", "channel=" + e.chNow + "&user=" + viewer, want{false, true, true}},
		{"on the child's device, a 16 programme on a 9 channel", "channel=" + e.chNow + "&user=" + viewer + "&family=" + fam, want{true, true, false}},
		{"on the child's device, a 9 programme", "channel=" + e.ch9 + "&user=" + viewer + "&family=" + fam, want{true, true, true}},
		{"on the child's device, a 16 channel", "channel=" + e.ch16 + "&user=" + viewer + "&family=" + fam, want{true, false, false}},
		// the home's own record of the sign-in decides, not what an old URL carries
		{"an old looser ceiling beside the stored one", "channel=" + e.chNow + "&user=" + viewer + "&family=" + fam + "&ac=18", want{true, true, false}},
		{"a signal the guardian cleared since", "channel=" + e.chNow + "&user=" + viewer + "&family=" + gone + "&ac=9", want{false, true, true}},
		// a sign-in with no family is held to the ceiling its credential carries
		{"a ceiling with no family", "channel=" + e.chNow + "&user=" + viewer + "&ac=9", want{true, true, false}},
		{"a ceiling of nothing", "channel=" + e.ch9 + "&user=" + viewer + "&ac=-1", want{true, false, false}},
		// the stricter of the account's policy and the device: 9+ account, All ages device
		{"a 9+ account alone on a 9 channel", "channel=" + e.ch9 + "&user=" + child, want{true, true, true}},
		{"a 9+ account on an All ages device", "channel=" + e.ch9 + "&user=" + child + "&family=" + childFam, want{true, false, false}},
		// another account's sign-in limits no one
		{"another account's family", "channel=" + e.chNow + "&user=" + viewer + "&family=" + childFam, want{false, true, true}},
	} {
		code, v := e.verdict(c.query)
		if code != 200 || v["restricted"] != c.want.restricted || v["channel_allowed"] != c.want.channel || v["allowed"] != c.want.allowed {
			t.Errorf("%s: %d %v, want %+v", c.name, code, v, c.want)
		}
	}
	for _, bad := range []string{"family=nope", "ac=nine", "family=" + fam + "&ac=x"} {
		if code, _ := e.verdict("channel=" + e.ch9 + "&user=" + viewer + "&" + bad); code != 400 {
			t.Errorf("%s = %d, want 400: a sign-in that cannot be read is no answer", bad, code)
		}
	}

	// the URL of POST …/play carries the sign-in, and play itself is held to it
	code, _, body := e.do("viewer", "POST", "/api/channels/"+e.ch9+"/play", "", "X-Test-Family", fam, "X-Test-Ac", "9")
	if code != 200 {
		t.Fatalf("play on the child's device = %d %v", code, body)
	}
	u, _ := body["url"].(string)
	_, sig, _ := strings.Cut(u, "sig=")
	parts := strings.Split(sig, ".")
	if len(parts) != 3 {
		t.Fatalf("no token in %q", u)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	_ = json.Unmarshal(raw, &claims)
	if claims["usr"] != viewer || claims["fam"] != fam || claims["ac"] != float64(9) {
		t.Fatalf("the channel URL's claims = %v, want the sign-in's family and ceiling", claims)
	}
	if code, _, body := e.do("viewer", "POST", "/api/channels/"+e.chNow+"/play", "", "X-Test-Family", fam, "X-Test-Ac", "9"); code != 403 || body["reason"] != "programme" {
		t.Errorf("play of a 16 programme on the child's device = %d %v", code, body)
	}
	// a sign-in with neither carries neither
	_, _, body = e.do("viewer", "POST", "/api/channels/"+e.ch9+"/play", "")
	u, _ = body["url"].(string)
	_, sig, _ = strings.Cut(u, "sig=")
	if parts = strings.Split(sig, "."); len(parts) == 3 {
		raw, _ = base64.RawURLEncoding.DecodeString(parts[1])
		claims = map[string]any{}
		_ = json.Unmarshal(raw, &claims)
		if claims["fam"] != nil || claims["ac"] != nil {
			t.Errorf("a sign-in with no family got %v", claims)
		}
	}
}
