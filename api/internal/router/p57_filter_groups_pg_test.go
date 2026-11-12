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

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
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

const (
	pfTeenTag       = "00000000-0000-4000-8000-0000573e0102" // on the teen video only
	pfSmartLate     = "00000000-0000-4000-8000-0000573e0301" // a saved filter: the teen tag
	pfSmartLateOld  = "00000000-0000-4000-8000-0000573e0302" // the older smart query: the teen tag
	pfSmartOpen     = "00000000-0000-4000-8000-0000573e0303" // a saved filter: the kid tag (the kid and the teen video)
	pfSmartOpenOld  = "00000000-0000-4000-8000-0000573e0304" // the older smart query: every video of library A
	pfSmartLateName = "Zarqa-Smart-Late"
	pfSmartOldName  = "Zarqa-Smart-Old"
	pfKidTag        = "00000000-0000-4000-8000-0000573e0103"
)

// smart gives library A its saved filters, in both shapes of smart_query: two that list
// only what is above the child's limit, two that also list what it may see.
func (e *pfEnv) smart() {
	e.t.Helper()
	e.exec(`INSERT INTO tags (id, name, name_norm) VALUES ($1, 'zarqa-teen-tag', 'zarqa-teen-tag'), ($2, 'pf-kid-tag', 'pf-kid-tag') ON CONFLICT DO NOTHING`, pfTeenTag, pfKidTag)
	e.exec(`INSERT INTO video_tags (video_id, tag_id) VALUES ($1, $2), ($1, $4), ($3, $4) ON CONFLICT DO NOTHING`, pfTeen, pfTeenTag, pfKid, pfKidTag)
	e.exec(`INSERT INTO collections (id, library_id, name, is_smart, smart_query) VALUES
		($1, $5, $6, true, jsonb_build_object('filters', jsonb_build_object('tag', $8::text))),
		($2, $5, $7, true, '{"tag":["zarqa-teen-tag"]}'),
		($3, $5, 'Pf smart open', true, jsonb_build_object('filters', jsonb_build_object('tag', $9::text))),
		($4, $5, 'Pf smart all', true, '{}')`,
		pfSmartLate, pfSmartLateOld, pfSmartOpen, pfSmartOpenOld, pfLibA, pfSmartLateName, pfSmartOldName, pfTeenTag, pfKidTag)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// Finding 7: a smart collection (a saved filter) is as visible as its list. One that lists
// nothing the account may see is not listed and answers as missing, by id and as a play
// queue's source; one that lists something is shown, with only that.
func TestP57FilterSmartCollections(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	e.smart()
	late := []string{pfSmartLate, pfSmartLateOld}
	open := []string{pfSmartOpen, pfSmartOpenOld}
	lists := []string{"/api/collections", "/api/collections?library=" + pfLibA, "/api/libraries/" + pfLibA + "/collections"}
	// an account nothing limits is shown all four, with their items
	for _, p := range lists {
		_, body := e.get("viewer", p)
		for _, id := range append(append([]string{}, late...), open...) {
			if !strings.Contains(body, id) {
				t.Fatalf("viewer GET %s lacks %s: %s", p, id, body)
			}
		}
	}
	for _, id := range late {
		if code, body := e.get("viewer", "/api/collections/"+id+"/items"); code != http.StatusOK || !strings.Contains(body, pfTeen) {
			t.Fatalf("viewer, the items of %s: %d %s", id, code, body)
		}
	}
	for _, who := range []string{"child", "child-cookie", "child-all", "viewer-device"} {
		for _, p := range lists {
			code, body := e.get(who, p)
			if code != http.StatusOK {
				t.Errorf("%s GET %s: %d %s", who, p, code, body)
			}
			for _, m := range []string{pfSmartLate, pfSmartLateOld, pfSmartLateName, pfSmartOldName, "zarqa-teen-tag", pfTeenTag} {
				if strings.Contains(body, m) {
					t.Errorf("%s GET %s names %s: %s", who, p, m, body)
				}
			}
			for _, id := range open {
				if !strings.Contains(body, id) {
					t.Errorf("%s GET %s lacks %s, which lists a video it may see: %s", who, p, id, body)
				}
			}
		}
		for _, id := range late {
			e.same(who, http.MethodGet, "/api/collections/{id}", id, "")
			e.same(who, http.MethodGet, "/api/collections/{id}/items", id, "")
		}
		for _, id := range open {
			if code, body := e.get(who, "/api/collections/"+id); code != http.StatusOK {
				t.Errorf("%s GET the open smart collection %s: %d %s", who, id, code, body)
			}
			code, body := e.get(who, "/api/collections/"+id+"/items")
			if code != http.StatusOK || !strings.Contains(body, pfKid) {
				t.Errorf("%s, the items of %s: %d %s", who, id, code, body)
			}
			for _, m := range []string{pfTeen, pfTeenName, pfTeen2, pfUnrated} {
				if strings.Contains(body, m) {
					t.Errorf("%s, the items of %s name %s: %s", who, id, m, body)
				}
			}
		}
		// the older shape ranks what the account may see: its one item is the first, so
		// its position does not count the hidden videos ranked before it
		_, body := e.get(who, "/api/collections/"+pfSmartOpenOld+"/items")
		var page struct {
			Items []struct {
				VideoID  string `json:"video_id"`
				Position int    `json:"position"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil || len(page.Items) != 1 || page.Items[0].VideoID != pfKid || page.Items[0].Position != 1 {
			t.Errorf("%s, the older smart collection's items: %s (%v); want the one video at position 1", who, body, err)
		}
	}
	// an allow on the video shows the collection that lists it
	e.rule(parental.EffectAllow, parental.SubjectVideo, pfTeen)
	for _, id := range late {
		if code, body := e.get("child", "/api/collections/"+id); code != http.StatusOK {
			t.Errorf("child, after an allow on its one video, GET %s: %d %s", id, code, body)
		}
	}
	if _, body := e.get("child", "/api/collections"); !strings.Contains(body, pfSmartLate) || !strings.Contains(body, pfSmartLateOld) {
		t.Errorf("child, after the allow, the list lacks the smart collections: %s", body)
	}
	// a block on the collection itself still hides it
	e.rule(parental.EffectBlock, parental.SubjectCollection, pfSmartOpen)
	e.same("child", http.MethodGet, "/api/collections/{id}", pfSmartOpen, "")
}

// Finding 8: a play queue's source that is hidden answers exactly as one that is not
// there, whatever kind it is and whyever it is hidden.
func TestP57FilterQueueSourceHiddenIsMissing(t *testing.T) {
	t.Parallel()
	e := setupFilter(t)
	e.groups()
	e.smart()
	const (
		season16    = "00000000-0000-4000-8000-0000573e0401" // the late show's season
		seasonMixed = "00000000-0000-4000-8000-0000573e0402" // a season of the open show with only its 16+ episode
		seasonOpen  = "00000000-0000-4000-8000-0000573e0403" // the open show's season with the kid video
		emptyList   = "00000000-0000-4000-8000-0000573e0404" // the child's own empty playlist
	)
	e.exec(`INSERT INTO series_seasons (id, series_id, season, kind, name, membership_hash, detector_version) VALUES ($1, $4, 1, 'episodic', 'S1', 'h', 1), ($2, $5, 2, 'episodic', 'S2', 'h', 1), ($3, $5, 1, 'episodic', 'S1', 'h', 1)`, season16, seasonMixed, seasonOpen, pfSeries16, pfSeries)
	e.exec(`UPDATE series_episodes SET season = 1, episode = 1 WHERE video_id IN ($1, $2)`, pfTeen2, pfKid)
	e.exec(`UPDATE series_episodes SET season = 2, episode = 1 WHERE video_id = $1`, pfTeen)
	body := func(kind string) string { return `{"source":{"kind":"` + kind + `","id":"{id}"}}` }
	hidden := []struct{ what, kind, id string }{
		{"a series whose episodes are all hidden", "series", pfSeries16},
		{"its season", "season", season16},
		{"a season with nothing it may see, of a series it may open", "season", seasonMixed},
		{"a collection whose items are all hidden", "collection", pfCol16},
		{"a smart collection that lists nothing it may see", "collection", pfSmartLate},
		{"the same, older shape", "collection", pfSmartLateOld},
		{"a hidden video", "video", pfTeen},
		{"an unrated video", "video", pfUnrated},
	}
	for _, who := range []string{"child", "child-cookie", "viewer-device"} {
		for _, c := range hidden {
			if code := e.alike(who, http.MethodPost, "/api/me/play-queues", c.id, body(c.kind)); code != http.StatusNotFound {
				t.Errorf("%s, a queue of %s: %d, want 404", who, c.what, code)
			}
		}
		// topic: and clip: ids carry a prefix
		for _, c := range []struct{ prefix, id string }{{"topic:", pfTopic16}, {"clip:", pfClip16}} {
			if code := e.alike(who, http.MethodPost, "/api/me/play-queues", c.id, `{"source":{"kind":"collection","id":"`+c.prefix+`{id}"}}`); code != http.StatusNotFound {
				t.Errorf("%s, a queue of a %s group with nothing it may see: %d, want 404", who, c.prefix, code)
			}
		}
	}
	// what the child may open still plays, with only what it may see
	for _, c := range []struct{ kind, id string }{
		{"series", pfSeries}, {"season", seasonOpen}, {"collection", pfCol}, {"collection", pfSmartOpen}, {"collection", pfSmartOpenOld},
		{"collection", "topic:" + pfTopic}, {"video", pfKid}, {"library", pfLibA},
	} {
		rec := e.raw("child", http.MethodPost, "/api/me/play-queues", strings.Replace(body(c.kind), "{id}", c.id, 1))
		if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), pfKid) {
			t.Errorf("child, a queue of %s %s: %d %s", c.kind, c.id, rec.Code, rec.Body.String())
		}
		for _, m := range []string{pfTeen, pfTeen2, pfUnrated, pfTeenName} {
			if strings.Contains(rec.Body.String(), m) {
				t.Errorf("child, a queue of %s %s names %s", c.kind, c.id, m)
			}
		}
	}
	// a blocked library, and a group inside it, are missing too (the group's page would
	// name the library)
	e.exec(`INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash) VALUES ('00000000-0000-4000-8000-0000573e0405', $1, 'PLpfB', 'Pf topic B', 'h')`, pfLibB)
	e.exec(`INSERT INTO topic_items (topic_id, video_id, position) VALUES ('00000000-0000-4000-8000-0000573e0405', $1, 1)`, pfKidB)
	e.rule(parental.EffectBlock, parental.SubjectLibrary, pfLibB)
	e.alike("child", http.MethodPost, "/api/me/play-queues", pfLibB, body("library"))
	e.alike("child", http.MethodPost, "/api/me/play-queues", "00000000-0000-4000-8000-0000573e0405", `{"source":{"kind":"collection","id":"topic:{id}"}}`)
	e.alike("child", http.MethodPost, "/api/me/play-queues", pfKidB, body("video"))

	// the child's own empty playlist is its own: empty, not missing; and an account with
	// no limit is answered as before (an empty series is empty)
	e.exec(`INSERT INTO user_playlists (id, user_id, name) VALUES ($1, $2, 'mine')`, emptyList, pfChild)
	if rec := e.raw("child", http.MethodPost, "/api/me/play-queues", strings.Replace(body("playlist"), "{id}", emptyList, 1)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("child, a queue of its own empty playlist: %d %s, want 422", rec.Code, rec.Body.String())
	}
	if rec := e.raw("viewer", http.MethodPost, "/api/me/play-queues", strings.Replace(body("series"), "{id}", pfSeries16, 1)); rec.Code != http.StatusCreated {
		t.Errorf("viewer, a queue of the late show: %d %s", rec.Code, rec.Body.String())
	}
}
