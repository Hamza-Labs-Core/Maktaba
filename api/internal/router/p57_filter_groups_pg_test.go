package router_test

// Epic 57 story 57.03, review of the filter: the pipeline's own collections (a topic
// collection, a clip group) and the play queue that reads them. Their page
// (GET /api/libraries/{id}/collections/{kind}/{gid}) once listed every member, counted the
// hidden ones and fed the play queue's `topic:<id>` source the same list.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	pfTopic       = "00000000-0000-4000-8000-0000573e0201" // a topic collection: the kid and the teen video
	pfTopic16     = "00000000-0000-4000-8000-0000573e0202" // a topic collection: the teen video only
	pfClip        = "00000000-0000-4000-8000-0000573e0203" // a clip group: the kid video and the late episode
	pfClip16      = "00000000-0000-4000-8000-0000573e0204" // a clip group: the unrated video only
	pfTopic16Name = "Zarqa-Topic-Late"
	pfClip16Name  = "Zarqa-Clips-Late"
)

// groups gives library A the pipeline's collections.
func (e *pfEnv) groups() {
	e.t.Helper()
	e.exec(`INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash) VALUES
		($1, $3, 'PLpf1', 'Pf topic', 'h'), ($2, $3, 'PLpf2', $4, 'h')`, pfTopic, pfTopic16, pfLibA, pfTopic16Name)
	e.exec(`INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1, $3, 1), ($1, $4, 2), ($2, $4, 1)`,
		pfTopic, pfTopic16, pfKid, pfTeen)
	e.exec(`INSERT INTO clip_groups (id, library_id, kind, basis, name, title, membership_hash) VALUES
		($1, $3, 'clips', 'rest', 'Pf clips', 'Pf clips', 'h'), ($2, $3, 'standalone', 'rest', $4, $4, 'h')`,
		pfClip, pfClip16, pfLibA, pfClip16Name)
	e.exec(`INSERT INTO clip_group_items (group_id, video_id, position) VALUES ($1, $3, 1), ($1, $4, 2), ($2, $5, 1)`,
		pfClip, pfClip16, pfKid, pfTeen2, pfUnrated)
}

type pfGroupPage struct {
	Collection struct {
		Videos int `json:"videos"`
	} `json:"collection"`
	Items []struct {
		ID string `json:"id"`
	} `json:"items"`
	Total int `json:"total"`
}

func TestP57FilterAutoCollection(t *testing.T) {
	e := setupFilter(t)
	e.groups()
	page := func(who, kind, gid string) (int, string, pfGroupPage) {
		t.Helper()
		code, body := e.get(who, "/api/libraries/"+pfLibA+"/collections/"+kind+"/"+gid)
		var p pfGroupPage
		_ = json.Unmarshal([]byte(body), &p)
		return code, body, p
	}
	// an account nothing limits is shown every member
	for _, c := range []struct{ kind, gid, hidden string }{{"topic", pfTopic, pfTeen}, {"clip", pfClip, pfTeen2}} {
		code, body, p := page("viewer", c.kind, c.gid)
		if code != http.StatusOK || p.Total != 2 || len(p.Items) != 2 || !strings.Contains(body, c.hidden) {
			t.Fatalf("viewer %s: %d %s", c.kind, code, body)
		}
	}
	hidden := []string{pfTeen, pfTeenName, pfTeen2, "Late-Episode-Title", pfUnrated, "Unrated-Title"}
	for _, who := range []string{"child", "child-cookie", "child-all", "viewer-device"} {
		for _, c := range []struct{ kind, gid string }{{"topic", pfTopic}, {"clip", pfClip}} {
			code, body, p := page(who, c.kind, c.gid)
			if code != http.StatusOK || p.Total != 1 || p.Collection.Videos != 1 || len(p.Items) != 1 || p.Items[0].ID != pfKid {
				t.Errorf("%s %s: %d %s, want the one video it may see, counted as one", who, c.kind, code, body)
			}
			for _, m := range hidden {
				if strings.Contains(body, m) {
					t.Errorf("%s %s names %s: %s", who, c.kind, m, body)
				}
			}
		}
		// a group with nothing the account may see is not listed, and answers as one that is not there
		_, list := e.get(who, "/api/libraries/"+pfLibA+"/collections")
		for _, m := range []string{pfTopic16, pfTopic16Name, pfClip16, pfClip16Name} {
			if strings.Contains(list, m) {
				t.Errorf("%s: the library's collections name %s: %s", who, m, list)
			}
		}
		if !strings.Contains(list, pfTopic) || !strings.Contains(list, pfClip) {
			t.Errorf("%s: the library's collections lack the groups it may open: %s", who, list)
		}
		e.same(who, http.MethodGet, "/api/libraries/"+pfLibA+"/collections/topic/{id}", pfTopic16, "")
		e.same(who, http.MethodGet, "/api/libraries/"+pfLibA+"/collections/clip/{id}", pfClip16, "")
	}
	if code, body, p := page("viewer", "topic", pfTopic16); code != http.StatusOK || p.Total != 1 {
		t.Errorf("viewer, the late topic: %d %s", code, body)
	}

	// the play queue's topic: and clip: sources read the same page
	for _, c := range []struct{ source, hidden string }{{"topic:" + pfTopic, pfTeen}, {"clip:" + pfClip, pfTeen2}} {
		rec := e.raw("child", http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"collection","id":"`+c.source+`"}}`)
		body := rec.Body.String()
		if rec.Code != http.StatusCreated || !strings.Contains(body, pfKid) {
			t.Fatalf("child, a queue of %s: %d %s", c.source, rec.Code, body)
		}
		for _, m := range hidden {
			if strings.Contains(body, m) {
				t.Errorf("child, a queue of %s names %s: %s", c.source, m, body)
			}
		}
		rec = e.raw("viewer", http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"collection","id":"`+c.source+`"}}`)
		if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), c.hidden) {
			t.Errorf("viewer, a queue of %s: %d %s", c.source, rec.Code, rec.Body.String())
		}
	}
}

// The recommendations answer is kept per account for a minute. One account has more than
// one limit (a device's signal holds one sign-in only) and a limit can be tightened at any
// moment, so an answer kept for one request is never given to a request with a limit.
func TestP57FilterRecommendationsNotKept(t *testing.T) {
	e := setupFilter(t)
	for _, u := range []string{pfViewer, pfChild} {
		e.exec(`INSERT INTO playback_state (user_id, video_id, position_sec, completed, updated_at) VALUES ($1,$2,10,false,now()), ($1,$3,10,false,now())`, u, pfTeen, pfKid)
	}
	// the account's own sign-in fills the kept answer
	if code, body := e.get("viewer", "/api/recommendations"); code != http.StatusOK || !strings.Contains(body, pfTeen) {
		t.Fatalf("viewer: %d %s", code, body)
	}
	// the same account on a device that said 9+
	if code, body := e.get("viewer-device", "/api/recommendations"); code != http.StatusOK || strings.Contains(body, pfTeen) || !strings.Contains(body, pfKid) {
		t.Errorf("the same account on a 9+ device was given the kept answer: %d %s", code, body)
	}
	// and the limited request's answer is not kept for the unlimited one
	if code, body := e.get("viewer", "/api/recommendations"); code != http.StatusOK || !strings.Contains(body, pfTeen) {
		t.Errorf("viewer, after the device's read: %d %s", code, body)
	}

	// a child's answer made under a looser limit is not served once the limit is tightened
	e.exec(`UPDATE parental_policies SET max_tier = 18, version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
	if code, body := e.get("child", "/api/recommendations"); code != http.StatusOK || !strings.Contains(body, pfTeen) {
		t.Fatalf("child at 18+: %d %s", code, body)
	}
	e.exec(`UPDATE parental_policies SET max_tier = 9, version = version + 1 WHERE user_id = $1`, pfChild)
	e.gate.Cache().Invalidate("")
	if code, body := e.get("child", "/api/recommendations"); code != http.StatusOK || strings.Contains(body, pfTeen) {
		t.Errorf("child back at 9+: %d %s", code, body)
	}
}

// A hidden object answers as a missing one whatever headers the request carries: an
// Upgrade header once made the gate answer with its stand-in id, which told the two apart.
func TestP57FilterByIDIgnoresUpgrade(t *testing.T) {
	e := setupFilter(t)
	ask := func(path string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Test-As", "child")
		req.Header.Set("Upgrade", "x")
		req.Header.Set("Connection", "Upgrade")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code, pfRequestID.ReplaceAllString(rec.Body.String(), `"requestId":"R"`)
	}
	for _, c := range []struct{ path, hidden string }{
		{"/api/videos/{id}", pfTeen}, {"/api/videos/{id}/chapters", pfTeen}, {"/api/series/{id}", pfSeries16},
		{"/api/collections/{id}", pfCol16}, {"/api/me/history/{id}", pfUnrated},
	} {
		missing := uuid.NewString()
		hc, hb := ask(strings.Replace(c.path, "{id}", c.hidden, 1))
		mc, mb := ask(strings.Replace(c.path, "{id}", missing, 1))
		if hc != mc || strings.ReplaceAll(hb, c.hidden, "ID") != strings.ReplaceAll(mb, missing, "ID") {
			t.Errorf("GET %s with an Upgrade header: a hidden id answers\n  %d %s\na missing id answers\n  %d %s", c.path, hc, hb, mc, mb)
		}
	}
}
