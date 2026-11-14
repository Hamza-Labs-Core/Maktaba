package router_test

// Epic 58 story 58.14 T01 and T05: the bypass rehearsal's rows for the new TVs (A23–A27,
// added to 57.13's table), against a real migrated Postgres behind the production router
// (setupHome), with the cast grant's credential middleware and the auth gate in front as
// the home runs them. The rows run in the `parental-bypass` job of _integration.yml
// (their names match its -run pattern) and in the integration tier with every other test.
//
//   - A23: a cast grant names its items. A restricted child cannot mint one for a hidden
//     video (answered as a missing one), and a grant for an allowed video or the child's
//     own queue reaches nothing hidden. A guardian casting from their own phone plays what
//     the guardian may see.
//   - A24 (T05): a queue grant minted, then the guardian tightens the limit through the
//     real route: the next queue item that is now above the limit answers 404, as missing.
//   - A25: a restricted child approving a cloud code "for" a parent is refused; approving for
//     itself signs the TV in as the child alone, restricted.
//   - A26: a deep link by id (Roku's, Fire TV's SEARCH intent) is a by-id read on the same
//     filtered routes: a hidden id answers exactly as an id that names nothing.
//   - A27: no check depends on the client. The same requests from each TV (Fire TV, Samsung
//     and LG through /tv/, Roku) and from a phone's browser get the same answers: the age
//     limit, a blocked library and the day's time.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// tvClients are the X-Maktaba-Client values of this epic's TVs, and a phone's browser (none).
var tvClients = []struct{ name, header, agent string }{
	{"phone browser", "", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1"},
	{"fire tv", "tv/1.4.0 (firetv; tv)", "Dalvik/2.1.0 (Linux; U; Android 11; AFTKRT Build/RS8101.1849N)"},
	{"samsung /tv/", "tv-web/1.4.0 (tizen; tv)", "Mozilla/5.0 (SMART-TV; LINUX; Tizen 5.5) AppleWebKit/537.36 Chrome/69.0.3497.106 TV Safari/537.36"},
	{"lg /tv/", "tv-web/1.4.0 (webos; tv)", "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 Chrome/68.0.3440.106 Safari/537.36 WebAppManager"},
	{"roku", "roku/1.4.0 (roku; tv)", "Roku/DVP-14.0 (14.0.4185-B1)"},
}

// asClient makes every request of h come from one client (its header and user agent).
func asClient(h *p57Home, header, agent string) {
	h.as = func(req *http.Request, p *principal.Principal) *principal.Principal {
		if header != "" {
			req.Header.Set("X-Maktaba-Client", header)
		}
		req.Header.Set("User-Agent", agent)
		return p
	}
}

// tvGrantFront is the home's credential order for a cast grant: the grant's middleware, the
// auth gate, then the router.
func tvGrantFront(t *testing.T, h *p57Home) func(grant, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	front := authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(h.h)
	front = castgrant.Bearer(castKeys(t), users.New(h.db), &authz.ACLStore{DB: h.db})(front)
	return func(grant, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+grant)
		rec := httptest.NewRecorder()
		front.ServeHTTP(rec, req)
		return rec
	}
}

// tvMint asks for a grant as who; the grant, or a failure naming the answer.
func tvMint(t *testing.T, h *p57Home, who, body string) string {
	t.Helper()
	rec := h.raw(who, http.MethodPost, "/api/cast/grants", body)
	var out struct {
		Grant string `json:"grant"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Grant == "" {
		t.Fatalf("%s mint %s = %d %s", who, body, rec.Code, rec.Body.String())
	}
	return out.Grant
}

// tvQueue makes who's play queue of library A; its id and body.
func tvQueue(t *testing.T, h *p57Home, who string) (string, string) {
	t.Helper()
	rec := h.raw(who, http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"}}`)
	var q struct {
		ID string `json:"id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &q) != nil || q.ID == "" {
		t.Fatalf("%s queue: %d %s", who, rec.Code, rec.Body.String())
	}
	return q.ID, rec.Body.String()
}

// tvLimit sets the child's limit as its guardian does, through the real route; the
// policy's version is read first, as a guardian's screen has it.
func tvLimit(t *testing.T, h *p57Home, tier int) {
	t.Helper()
	var version int
	if err := h.db.QueryRow(`SELECT version FROM parental_policies WHERE user_id = $1`, pfChild).Scan(&version); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"max_tier": tier, "unrated": "hide", "allow_only": false, "version": version})
	if rec := h.raw("guardian", http.MethodPut, "/api/users/"+pfChild+"/parental", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("the guardian sets the limit to %d: %d %s", tier, rec.Code, rec.Body.String())
	}
}

func tvWantScope(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"cast_scope"`) {
		t.Errorf("%s: %d %s, want 403 cast_scope", what, rec.Code, rec.Body.String())
	}
}

// A23: the grant names items; what it opens is held to the minting person.
func TestP58BypassCastGrantNamesItems(t *testing.T) {
	h := setupHome(t)
	with := tvGrantFront(t, h)

	// a hidden video is no item a child can name: answered exactly as a missing one
	h.same("child", http.MethodPost, "/api/cast/grants", pfTeen, `{"video_id":"{id}"}`)
	h.same("child", http.MethodPost, "/api/cast/grants", pfUnrated, `{"video_id":"{id}"}`)
	// the child through Maktaba Cloud mints nothing
	if rec := h.raw("child-cloud", http.MethodPost, "/api/cast/grants", `{"video_id":"`+pfKid+`"}`); rec.Code != http.StatusForbidden {
		t.Errorf("the child through the cloud minted: %d %s", rec.Code, rec.Body.String())
	}

	// a grant for an allowed video reaches that video only
	g := tvMint(t, h, "child", `{"video_id":"`+pfKid+`"}`)
	if rec := with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`); rec.Code != http.StatusCreated {
		t.Fatalf("the child's grant opens its video: %d %s", rec.Code, rec.Body.String())
	}
	tvWantScope(t, "the child's video grant asked for the teen video", with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`))
	tvWantScope(t, "the child's video grant read the teen video", with(g, http.MethodGet, "/api/videos/"+pfTeen, ""))
	tvWantScope(t, "the child's video grant searched", with(g, http.MethodGet, "/api/search?q=Zarqa", ""))

	// the child's own queue holds only what the child may see, and so does its grant
	qid, body := tvQueue(t, h, "child")
	for _, hidden := range []string{pfTeen, pfTeen2, pfUnrated, pfTeenName} {
		if strings.Contains(body, hidden) {
			t.Errorf("the child's queue names %s: %s", hidden, body)
		}
	}
	qg := tvMint(t, h, "child", `{"queue_id":"`+qid+`"}`)
	if rec := with(qg, http.MethodGet, "/api/me/play-queues/"+qid, ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pfTeen) {
		t.Errorf("the queue read through the grant: %d %s", rec.Code, rec.Body.String())
	}
	if rec := with(qg, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`); rec.Code/100 == 2 {
		t.Errorf("the child's queue grant opened the teen video: %d", rec.Code)
	}

	// the guardian casting from their own phone plays what the guardian may see
	gg := tvMint(t, h, "guardian", `{"video_id":"`+pfTeen+`"}`)
	if rec := with(gg, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("the guardian's grant for the teen video: %d %s", rec.Code, rec.Body.String())
	}
}

// A24, T05: a grant minted, then the guardian tightens; the next queue item now above the
// limit answers as missing, through the grant as for the child.
func TestP58BypassCastGrantAfterTighten(t *testing.T) {
	h := setupHome(t)
	with := tvGrantFront(t, h)
	tvLimit(t, h, 16)
	qid, body := tvQueue(t, h, "child")
	if !strings.Contains(body, pfTeen) || !strings.Contains(body, pfKid) {
		t.Fatalf("the child's queue at 16+ lacks the kid or the teen video: %s", body)
	}
	g := tvMint(t, h, "child", `{"queue_id":"`+qid+`"}`)
	rec := with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`)
	var sess struct {
		SessionID string `json:"session_id"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &sess) != nil || sess.SessionID == "" {
		t.Fatalf("the teen video at 16+ through the grant: %d %s", rec.Code, rec.Body.String())
	}
	// the stand-in streaming service keeps no rows: the session as the home would have it
	h.exec(`INSERT INTO streaming_sessions (id, user_id, video_id, mode, opened_at, last_seen_at) VALUES ($1,$2,$3,'direct',now(),now())
		ON CONFLICT (id) DO NOTHING`, sess.SessionID, pfChild, pfTeen)

	tvLimit(t, h, 9)

	// the next item, opened after the change: 404, the answer for a video that is not there
	hidden := with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`"}`)
	if hidden.Code != http.StatusNotFound || strings.Contains(hidden.Body.String(), pfTeenName) {
		t.Errorf("the teen video after the limit was tightened: %d %s, want 404", hidden.Code, hidden.Body.String())
	}
	if rec := with(g, http.MethodGet, "/api/videos/"+pfTeen, ""); rec.Code != http.StatusNotFound {
		t.Errorf("the teen video's page after the change: %d", rec.Code)
	}
	// the session opened before the change mints no new URLs
	if rec := with(g, http.MethodGet, "/api/stream/sessions/"+sess.SessionID, ""); rec.Code/100 == 2 {
		t.Errorf("the session opened before the change answered %d %s", rec.Code, rec.Body.String())
	}
	// the queue read through the grant no longer lists it
	if rec := with(g, http.MethodGet, "/api/me/play-queues/"+qid, ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), pfTeen) {
		t.Errorf("the queue after the change: %d %s", rec.Code, rec.Body.String())
	}
	// an allowed item still plays: the grant is held, not broken
	if rec := with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`); rec.Code != http.StatusCreated {
		t.Errorf("the kid video after the change: %d %s", rec.Code, rec.Body.String())
	}
	// and the child itself is answered the same way: no grant-only rule
	h.same("child", http.MethodPost, "/api/stream/sessions", pfTeen, `{"video_id":"{id}"}`)
}

// A25: the cloud code signs the TV in as the approver only.
func TestP58BypassCloudCodeApprover(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	for _, as := range []string{ccGuardian, ccAdmin} {
		rec := ccDo(h, "child", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"KDAS2345","as_user":"`+as+`"}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"restricted"`) {
			t.Errorf("the child approving as %s: %d %s, want 403 restricted", as, rec.Code, rec.Body.String())
		}
	}
	if len(agent.claimed) != 0 {
		t.Fatalf("a refused approval reached the cloud: %v", agent.claimed)
	}
	// approving for itself: the TV is the child's, restricted, never an admin
	if rec := ccDo(h, "child", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"KDTV2345"}`); rec.Code != http.StatusOK {
		t.Fatalf("the child approving its own TV: %d %s", rec.Code, rec.Body.String())
	}
	got := redeem(t, h, agent.grant("KDTV2345"))
	if got.Usr != ccChild || got.IsAdmin || !got.Rst {
		t.Fatalf("the child's TV signed in as %+v", got)
	}
}

// A26: a deep link by id is a by-id read on the filtered routes, from Roku and Fire TV alike.
func TestP58BypassDeepLinkByID(t *testing.T) {
	h := setupHome(t)
	for _, c := range tvClients {
		if !strings.Contains(c.header, "roku") && !strings.Contains(c.header, "firetv") {
			continue
		}
		asClient(h, c.header, c.agent)
		for _, id := range []string{pfTeen, pfUnrated} {
			h.same("child", http.MethodGet, "/api/videos/{id}", id, "")
			h.same("child", http.MethodPost, "/api/stream/sessions", id, `{"video_id":"{id}"}`)
		}
		h.same("child", http.MethodGet, "/api/series/{id}", pfSeries16, "")
		h.same("child", http.MethodGet, "/api/collections/{id}", pfCol16, "")
	}
	h.as = nil
}

// A27: there is no TV-only check. Each TV and a phone's browser are answered alike: the age
// limit, a library blocked for the child, and the day's time once it is over.
func TestP58BypassNoTVOnlyCheck(t *testing.T) {
	h := setupHome(t)
	h.rule("block", "library", pfLibB)
	answer := func(path string) (int, string) {
		code, body := h.get("child", path)
		return code, pfRequestID.ReplaceAllString(body, `"requestId":"R"`)
	}
	var first struct {
		teen, kidB string
	}
	for i, c := range tvClients {
		asClient(h, c.header, c.agent)
		teenCode, teen := answer("/api/videos/" + pfTeen)
		kidBCode, kidB := answer("/api/videos/" + pfKidB)
		if teenCode != http.StatusNotFound || kidBCode != http.StatusNotFound {
			t.Errorf("%s: the teen video %d, library B's video %d; want 404 both", c.name, teenCode, kidBCode)
		}
		if i == 0 {
			first.teen, first.kidB = teen, kidB
		} else if teen != first.teen || kidB != first.kidB {
			t.Errorf("%s is answered otherwise than a phone's browser:\n  %s\n  %s\nwant\n  %s\n  %s", c.name, teen, kidB, first.teen, first.kidB)
		}
		code, list := answer("/api/videos")
		if code != http.StatusOK || !strings.Contains(list, pfKid) {
			t.Errorf("%s: the video list %d %s", c.name, code, list)
		}
		for _, hidden := range []string{pfTeen, pfTeenName, pfUnrated, pfKidB, "KidB-Title"} {
			if strings.Contains(list, hidden) {
				t.Errorf("%s: the video list names %s", c.name, hidden)
			}
		}
		if rec := h.raw("child", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`); rec.Code != http.StatusCreated {
			t.Errorf("%s: the kid video inside the time: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}

	// the day's time is over: every client is refused a stream, with when it opens again
	loc, err := h.cache.Store().HomeZone(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	h.exec(`INSERT INTO parental_time_rules (user_id, days, daily_minutes) VALUES ($1, 127, 60)`, pfChild)
	h.exec(`INSERT INTO parental_time_usage (user_id, day, library_id, seconds) VALUES ($1, $2::date, $3, 3600)`,
		pfChild, parental.DayKey(time.Now(), loc), parental.NoLibrary)
	h.cache.Invalidate("")
	for _, c := range tvClients {
		asClient(h, c.header, c.agent)
		rec := h.raw("child", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusForbidden || out["code"] != "parental_time" || out["next_allowed_at"] == nil {
			t.Errorf("%s with the time over: %d %s, want 403 parental_time", c.name, rec.Code, rec.Body.String())
		}
	}
	h.as = nil
}
