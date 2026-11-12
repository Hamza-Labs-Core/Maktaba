package router_test

// Epic 57, review of the filter: what a restricted account's old token may still claim,
// what a policy that cannot be read means, and the rest of the review's findings, behind
// the production router on a real migrated Postgres.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
)

// An admin is demoted and then restricted. Its access token, minted while it was an admin,
// still says `is_admin` and every library until it runs out. The gate reads the account's
// policy, so the claim counts for nothing: the account cannot lift or loosen its own
// restriction, is refused the admin's routes, is not shown the other children, and reads
// only the libraries it was given.
func TestP57StaleAdminTokenOnARestrictedAccount(t *testing.T) {
	h := setupHome(t)
	// the child was given library A only
	h.exec(`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, pfChild, pfLibB)
	h.as = func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == pfChild {
			return &principal.Principal{UserID: pfChild, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		}
		return p
	}
	policy := func() (tier, guardians, rules int) {
		t.Helper()
		if err := h.db.QueryRow(`SELECT COALESCE((SELECT max_tier FROM parental_policies WHERE user_id = $1), -99),
			(SELECT count(*) FROM parental_guardians WHERE child_id = $1),
			(SELECT count(*) FROM parental_rules WHERE user_id = $1)`, pfChild).Scan(&tier, &guardians, &rules); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, x := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/users/" + pfChild + "/parental", ""},
		{http.MethodPut, "/api/users/" + pfChild + "/parental", `{"max_tier":18,"unrated":"tier_0","allow_only":false,"version":1}`},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/guardians", `{"guardian_id":"` + pfViewer + `"}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental/guardians/" + pfGuardian, ""},
		{http.MethodPost, "/api/users/" + pfChild + "/parental/rules", `{"effect":"allow","subject_kind":"video","subject_id":"` + pfTeen + `"}`},
		{http.MethodDelete, "/api/users/" + pfChild + "/parental/rules/" + phRule, ""},
		{http.MethodPost, "/api/parental/requests/" + phRequest + "/approve", "{}"},
		{http.MethodPut, "/api/users/" + pfChild2 + "/parental", `{"max_tier":18,"unrated":"tier_0","allow_only":false,"version":1}`},
		{http.MethodPut, "/api/videos/" + pfTeen + "/age-tier", `{"tier":0}`},
	} {
		if rec := h.raw("child", x.method, x.path, x.body); rec.Code/100 == 2 {
			t.Errorf("%s %s with the old admin token: %d %s", x.method, x.path, rec.Code, rec.Body.String())
		}
	}
	if tier, guardians, rules := policy(); tier != 9 || guardians != 1 || rules != 1 {
		t.Errorf("after the attempts: limit %d, %d guardians, %d rules; want 9, 1, 1", tier, guardians, rules)
	}
	// the admin's routes
	for _, p := range []string{"/api/users", "/api/parental/settings", "/api/parental/review"} {
		if rec := h.raw("child", http.MethodGet, p, ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"restricted"`) {
			t.Errorf("GET %s with the old admin token: %d %s, want 403 restricted", p, rec.Code, rec.Body.String())
		}
	}
	// the routes that guard themselves read the role the gate left
	if code, body := h.get("child", "/api/parental/children"); code/100 == 2 && (strings.Contains(body, pfChild2) || strings.Contains(body, "pf-child-two")) {
		t.Errorf("GET /api/parental/children with the old admin token names the other child: %d %s", code, body)
	}
	if code, body := h.get("child", "/api/users/"+pfChild2+"/parental"); code/100 == 2 {
		t.Errorf("GET another child's policy with the old admin token: %d %s", code, body)
	}
	// every library: only the ones the account was given
	if code, body := h.get("child", "/api/libraries"); code != http.StatusOK || strings.Contains(body, pfLibB) || !strings.Contains(body, pfLibA) {
		t.Errorf("libraries with the old admin token: %d %s", code, body)
	}
	if code, body := h.get("child", "/api/videos"); code != http.StatusOK || strings.Contains(body, pfKidB) || !strings.Contains(body, pfKid) {
		t.Errorf("videos with the old admin token: %d %s", code, body)
	}
	// (a video of a library the account was not given answers as it does for any viewer)
	if code, body := h.get("child", "/api/videos/"+pfKidB); code/100 == 2 || strings.Contains(body, "KidB-Title") {
		t.Errorf("a video of a library it was not given, with the old admin token: %d %s", code, body)
	}

	// an admin with no policy keeps its role, also on a device that said 9+ (the device
	// limits what it is shown, not who it is)
	nine := 9
	h.as = func(_ *http.Request, p *principal.Principal) *principal.Principal {
		if p != nil && p.UserID == pfAdmin {
			q := *p
			q.AgeCeiling = &nine
			return &q
		}
		return p
	}
	if code, body := h.get("admin", "/api/parental/children"); code != http.StatusOK || !strings.Contains(body, pfChild2) {
		t.Errorf("an admin on a 9+ device, the children: %d %s", code, body)
	}
}

// The policies cannot be read (a node whose home database is down). An account read a
// moment ago with no limit at all keeps being served, so what the node holds still plays;
// a restricted account, a sign-in its device limits and an account never read are refused.
// Nothing above a limit is shown to anyone.
func TestP57FilterWhenThePolicyCannotBeRead(t *testing.T) {
	e := setupFilter(t)
	policies, err := sql.Open("postgres", e.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = policies.Close() }()
	e.gate.SetCache(parental.NewCache(parental.NewStore(policies)))
	paths := []string{"/api/videos", "/api/videos/" + pfKid, "/api/home"}
	for _, who := range []string{"viewer", "viewer-device", "child"} {
		if code, body := e.get(who, "/api/videos"); code != http.StatusOK {
			t.Fatalf("%s before: %d %s", who, code, body)
		}
	}
	_ = policies.Close() // the policies' database is gone; the handlers' own still answers

	for _, p := range paths {
		if code, body := e.get("viewer", p); code != http.StatusOK {
			t.Errorf("an unrestricted account read a moment ago, GET %s: %d %s; want it served", p, code, body)
		}
	}
	if code, body := e.get("viewer", "/api/videos"); !strings.Contains(body, pfTeen) {
		t.Errorf("the unrestricted account's list lost the 16+ video: %d %s", code, body)
	}
	if rec := e.raw("viewer", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`); rec.Code == http.StatusServiceUnavailable {
		t.Errorf("the unrestricted account's stream was refused for the policy: %d %s", rec.Code, rec.Body.String())
	}
	for _, who := range []string{"child", "viewer-device", "admin", "guardian"} {
		for _, p := range append(paths, "/api/videos/"+pfTeen) {
			code, body := e.get(who, p)
			if code != http.StatusServiceUnavailable || strings.Contains(body, pfTeenName) || strings.Contains(body, "Kid-Title") {
				t.Errorf("%s GET %s with the policies gone: %d %s, want 503", who, p, code, body)
			}
		}
	}
}

// Finding 10 (story 57.03's history row): what the child watched and may no longer see
// stays in its own history and activity as a row that is not available. The row says when
// and how much was watched and nothing that names the video: no id, title, length or
// position, and a key made from the time alone.
func TestP57FilterHistoryShowsHiddenAsNotAvailable(t *testing.T) {
	e := setupHome(t).pfEnv
	for _, u := range []string{pfChild, pfViewer} {
		e.exec(`INSERT INTO watch_sessions (id, user_id, video_id, started_at, duration_sec, percent_complete, state) VALUES
			(gen_random_uuid(), $1, $2, now() - interval '3 hours', 60, 50, 'stopped'),
			(gen_random_uuid(), $1, $3, now() - interval '2 hours', 40, 30, 'stopped'),
			(gen_random_uuid(), $1, $3, now() - interval '1 hour', 30, 70, 'stopped')`, u, pfKid, pfTeen)
		e.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1, $2, 33, false, now())`, u, pfTeen)
	}
	type item struct {
		Key           string  `json:"key"`
		Available     *bool   `json:"available"`
		VideoID       string  `json:"video_id"`
		Title         string  `json:"title"`
		DurationSec   float64 `json:"duration_sec"`
		TimesWatched  int     `json:"times_watched"`
		TotalWatchSec int     `json:"total_watch_sec"`
		BestPercent   float64 `json:"best_percent"`
		LastWatchedAt string  `json:"last_watched_at"`
		PositionSec   float64 `json:"position_sec"`
	}
	history := func(who string) (string, []item) {
		t.Helper()
		code, body := e.get(who, "/api/me/history")
		var page struct {
			Items []item `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil || code != http.StatusOK {
			t.Fatalf("%s history: %d %s (%v)", who, code, body, err)
		}
		return body, page.Items
	}
	// an account with no limit: both rows, each available, keyed by its video
	_, rows := history("viewer")
	if len(rows) != 2 || rows[0].VideoID != pfTeen || rows[1].VideoID != pfKid {
		t.Fatalf("viewer history: %+v", rows)
	}
	for _, r := range rows {
		if r.Available == nil || !*r.Available || r.Key != r.VideoID || r.Title == "" {
			t.Errorf("viewer row: %+v, want available and keyed by its video", r)
		}
	}
	for _, who := range []string{"child", "child-cookie", "viewer-device"} {
		body, rows := history(who)
		if len(rows) != 2 {
			t.Fatalf("%s history has %d rows, want the hidden one kept: %s", who, len(rows), body)
		}
		hidden, shown := rows[0], rows[1]
		if shown.VideoID != pfKid || shown.Available == nil || !*shown.Available || shown.Key != pfKid || shown.Title != "Kid-Title" {
			t.Errorf("%s: the row it may see: %+v", who, shown)
		}
		if hidden.Available == nil || *hidden.Available {
			t.Errorf("%s: the hidden row is not marked unavailable: %+v", who, hidden)
		}
		if hidden.VideoID != "" || hidden.Title != "" || hidden.DurationSec != 0 || hidden.PositionSec != 0 || hidden.BestPercent != 0 {
			t.Errorf("%s: the hidden row says more than when and how much: %+v", who, hidden)
		}
		if hidden.TimesWatched != 2 || hidden.TotalWatchSec != 70 || hidden.LastWatchedAt == "" {
			t.Errorf("%s: the hidden row lost the child's own numbers: %+v", who, hidden)
		}
		if hidden.Key == "" || hidden.Key == shown.Key {
			t.Errorf("%s: the hidden row has no key of its own: %+v", who, hidden)
		}
		for _, m := range append([]string{pfTeen, pfTeenName}, e.hashOf(pfTeen), strings.ReplaceAll(pfTeen, "-", "")) {
			if strings.Contains(body, m) {
				t.Errorf("%s history names %s: %s", who, m, body)
			}
		}
		// the key is the same on every read
		if _, again := history(who); again[0].Key != hidden.Key {
			t.Errorf("%s: the hidden row's key changed between reads: %q, %q", who, hidden.Key, again[0].Key)
		}
		// the activity feed keeps the entry the same way
		code, feed := e.get(who, "/api/me/activity?types=watched")
		if code != http.StatusOK || strings.Contains(feed, pfTeen) || strings.Contains(feed, pfTeenName) {
			t.Errorf("%s activity names the hidden video: %d %s", who, code, feed)
		}
		if strings.Count(feed, `"available":false`) != 2 || strings.Count(feed, `"available":true`) != 1 {
			t.Errorf("%s activity: want two entries not available and one available: %s", who, feed)
		}
	}
	// allowed again, the row is the video's again
	e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen)
	if _, rows := history("child"); len(rows) != 2 || rows[0].VideoID != pfTeen || rows[0].Title != pfTeenName || rows[0].Available == nil || !*rows[0].Available {
		t.Errorf("child history after an allow: %+v", rows)
	}
}

func (e *pfEnv) hashOf(id string) string {
	e.t.Helper()
	var h string
	if err := e.db.QueryRow(`SELECT content_hash FROM videos WHERE id = $1`, id).Scan(&h); err != nil {
		e.t.Fatal(err)
	}
	return h
}
