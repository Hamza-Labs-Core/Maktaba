package router_test

// Epic 57 story 57.12 behind the production router on a real migrated Postgres: an
// existing home after the update. Nothing is restricted by itself, a child's device
// that signs in before parental controls are set up is recorded and limits nothing,
// the admins are told once, the first library default tier turns parental controls on,
// and every app reads its own `parental` block in GET /api/capabilities.
//
// Also the two answers the bypass rehearsal (story 57.13) found and this change fixed,
// so they are held by the untagged tests too.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

func (h *p57Home) json(who, method, path, body string) (int, map[string]any) {
	h.t.Helper()
	rec := h.raw(who, method, path, body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// updated is the home as an update leaves it: users, libraries and videos, and nothing
// of parental controls set up.
func updated(t *testing.T) *p57Home {
	t.Helper()
	h := setupHome(t)
	h.exec(`DELETE FROM parental_policies`)
	h.exec(`UPDATE libraries SET age_tier = NULL`)
	h.exec(`DELETE FROM session_age_signals`)
	h.exec(`UPDATE app_settings SET value = 'false' WHERE key = 'parental.enabled'`)
	h.cache.Invalidate("")
	return h
}

func TestP57ExistingHome(t *testing.T) {
	h := updated(t)

	// T01: every account sees what it saw: the 16+ and the unrated video are listed for
	// the accounts that were restricted in setupHome's home and are not in this one
	for _, who := range []string{"viewer", "child", "guardian", "admin"} {
		code, body := h.get(who, "/api/videos?library="+pfLibA)
		if code != http.StatusOK || !strings.Contains(body, pfTeen) || !strings.Contains(body, pfUnrated) || !strings.Contains(body, pfKid) {
			t.Errorf("%s after the update: %d, the list lacks a video: %s", who, code, body)
		}
		_, caps := h.json(who, http.MethodGet, "/api/capabilities", "")
		block, _ := caps["parental"].(map[string]any)
		if block == nil || block["enabled"] != false || block["restricted"] != false || block["tier_limit"] != nil ||
			block["can_manage"] != (who == "admin") {
			t.Errorf("%s capabilities.parental after the update = %v", who, caps["parental"])
		}
	}

	// T02: a child's device speaks before setup. It is recorded and limits nothing.
	if _, err := h.cache.Store().RecordSignal(t.Context(), pfChild, phFamily,
		parental.SignalInput{AgeRange: &parental.AgeRange{Lower: ptr(9), Upper: ptr(12)}}); err != nil {
		t.Fatal(err)
	}
	h.cache.Invalidate("")
	for i := 0; i < 2; i++ { // the device speaks again: still one notice
		if _, err := h.cache.Store().RecordSignal(t.Context(), pfChild, phFamily,
			parental.SignalInput{AgeRange: &parental.AgeRange{Lower: ptr(9), Upper: ptr(12)}}); err != nil {
			t.Fatal(err)
		}
	}
	if code, body := h.get("child", "/api/videos?library="+pfLibA); code != http.StatusOK || !strings.Contains(body, pfTeen) {
		t.Errorf("the child after its device's signal, before setup: %d, the 16+ video is gone: %s", code, body)
	}
	var notices int
	if err := h.db.QueryRow(`SELECT count(*) FROM notifications WHERE kind = $1`, router.NoticeChildDevice).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("%d child-device notices (%v), want 1", notices, err)
	}
	list, err := (&notify.Store{DB: h.db}).List(t.Context(), pfAdmin, true, 10, false)
	if err != nil || len(list) != 1 || list[0].TitleKey != "notify.parental.child_device" || list[0].Link != "/family" ||
		strings.Contains(list[0].Subject+list[0].Link, pfChild) {
		t.Fatalf("the admins' notice = %+v (%v)", list, err)
	}
	if viewer, err := (&notify.Store{DB: h.db}).List(t.Context(), pfViewer, false, 10, false); err != nil || len(viewer) != 0 {
		t.Fatalf("a viewer reads the notice: %+v (%v)", viewer, err)
	}
	code, set := h.json("admin", http.MethodGet, "/api/parental/settings", "")
	if code != http.StatusOK || set["enabled"] != false || set["child_devices"] != float64(1) || set["child_device_notice"] != true ||
		set["restricted_accounts"] != float64(0) || set["can_disable"] != true {
		t.Fatalf("settings before setup: %d %v", code, set)
	}
	if code, set = h.json("admin", http.MethodPost, "/api/parental/settings/notice/dismiss", ""); code != http.StatusOK || set["child_device_notice"] != false {
		t.Fatalf("dismiss: %d %v", code, set)
	}

	// T03: the first library default tier turns parental controls on, and the device's
	// limit holds from the next request
	if code, out := h.json("admin", http.MethodPut, "/api/libraries/"+pfLibB+"/age-tier", `{"tier":0}`); code != http.StatusOK {
		t.Fatalf("set a library default tier: %d %v", code, out)
	}
	h.cache.Invalidate("")
	if code, set = h.json("admin", http.MethodGet, "/api/parental/settings", ""); code != http.StatusOK || set["enabled"] != true {
		t.Fatalf("settings after the first library default tier: %d %v", code, set)
	}
	// the child's app sign-in (the one whose device spoke) is now held to 9+
	h.as = withFamily(pfChild, phFamily)
	if code, body := h.get("child", "/api/videos?library="+pfLibA); code != http.StatusOK || strings.Contains(body, pfTeen) || !strings.Contains(body, pfKid) {
		t.Errorf("the child's device once parental controls are on: %d %s", code, body)
	}
	_, caps := h.json("child", http.MethodGet, "/api/capabilities", "")
	if block, _ := caps["parental"].(map[string]any); block == nil || block["enabled"] != true || block["restricted"] != false ||
		block["tier_limit"] != float64(9) || block["can_manage"] != false {
		t.Errorf("the child's capabilities.parental once on = %v", caps["parental"])
	}
	h.as = nil

	// off again is an admin's, and only with no restricted account
	if code, _ := h.json("guardian", http.MethodPut, "/api/parental/settings", `{"enabled":false}`); code != http.StatusForbidden {
		t.Errorf("a guardian turning it off: %d", code)
	}
	h.reset() // the two children are restricted again
	code, out := h.json("admin", http.MethodPut, "/api/parental/settings", `{"enabled":false}`)
	if code != http.StatusConflict || out["code"] != "restrictions_exist" || out["type"] != httperror.Type("parental-restrictions-exist") {
		t.Errorf("off with restricted accounts: %d %v", code, out)
	}
	h.exec(`DELETE FROM parental_policies`)
	if code, out = h.json("admin", http.MethodPut, "/api/parental/settings", `{"enabled":false}`); code != http.StatusOK || out["enabled"] != false {
		t.Errorf("off with none: %d %v", code, out)
	}
	if code, out = h.json("admin", http.MethodPut, "/api/parental/settings", `{}`); code != http.StatusUnprocessableEntity {
		t.Errorf("a PUT that says nothing: %d %v", code, out)
	}
}

func ptr(n int) *int { return &n }

// The `parental` block of GET /api/capabilities for each caller, and its ETag.
func TestP57CapabilitiesParental(t *testing.T) {
	h := setupHome(t)
	for who, want := range map[string]map[string]any{
		"viewer":         {"enabled": true, "restricted": false, "tier_limit": nil, "can_manage": false},
		"child":          {"enabled": true, "restricted": true, "tier_limit": float64(9), "can_manage": false},
		"child-cloud":    {"enabled": true, "restricted": true, "tier_limit": float64(9), "can_manage": false},
		"guardian":       {"enabled": true, "restricted": false, "tier_limit": nil, "can_manage": true},
		"other-guardian": {"enabled": true, "restricted": false, "tier_limit": nil, "can_manage": true},
		"admin":          {"enabled": true, "restricted": false, "tier_limit": nil, "can_manage": true},
		"admin-token":    {"enabled": true, "restricted": false, "tier_limit": nil, "can_manage": true},
		"viewer-device":  {"enabled": true, "restricted": false, "tier_limit": float64(9), "can_manage": false},
	} {
		code, caps := h.json(who, http.MethodGet, "/api/capabilities", "")
		block, _ := caps["parental"].(map[string]any)
		if code != http.StatusOK || len(block) != len(want) {
			t.Errorf("%s: %d parental = %v", who, code, caps["parental"])
			continue
		}
		for k, v := range want {
			if block[k] != v {
				t.Errorf("%s: parental.%s = %v, want %v", who, k, block[k], v)
			}
		}
		if _, ok := caps["features"]; !ok {
			t.Errorf("%s: the answer lost its features: %v", who, caps)
		}
	}
	if rec := h.raw("", http.MethodGet, "/api/capabilities", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no one: %d", rec.Code)
	}
	// the ETag covers the block: the child's changes when its limit does, and a
	// matching one is 304
	first := h.raw("child", http.MethodGet, "/api/capabilities", "")
	tag := first.Header().Get("ETag")
	if tag == "" || tag == h.raw("viewer", http.MethodGet, "/api/capabilities", "").Header().Get("ETag") {
		t.Fatalf("the child's ETag %q is the viewer's", tag)
	}
	again := h.rawWith("child", http.MethodGet, "/api/capabilities", "If-None-Match", tag)
	if again.Code != http.StatusNotModified {
		t.Errorf("If-None-Match with the current ETag: %d", again.Code)
	}
	h.exec(`UPDATE parental_policies SET max_tier = 13, version = version + 1 WHERE user_id = $1`, pfChild)
	h.cache.Invalidate("")
	if after := h.rawWith("child", http.MethodGet, "/api/capabilities", "If-None-Match", tag); after.Code != http.StatusOK ||
		!strings.Contains(after.Body.String(), `"tier_limit":13`) {
		t.Errorf("after the limit changed: %d %s", after.Code, after.Body.String())
	}
	// a policy that cannot be read answers no capabilities rather than an unlimited block
	_ = h.db.Close()
	h.cache.Invalidate("")
	if rec := h.raw("child", http.MethodGet, "/api/capabilities", ""); rec.Code < 500 {
		t.Errorf("with the database gone: %d %s", rec.Code, rec.Body.String())
	}
}

// What the bypass rehearsal found (story 57.13), held here too: the context card of an
// allowed episode left out its 16+ sibling, and a stream for a video in a library a rule
// blocks answers as for a video that is not there.
func TestP57RehearsalFindings(t *testing.T) {
	h := setupHome(t)
	code, card := h.get("child", "/api/videos/"+pfKid+"/context")
	if code != http.StatusOK || strings.Contains(card, pfTeen) || strings.Contains(card, pfTeenName) {
		t.Errorf("the child's context card of the all-ages episode: %d %s", code, card)
	}
	if code, card = h.get("viewer", "/api/videos/"+pfKid+"/context"); code != http.StatusOK || !strings.Contains(card, pfTeen) {
		t.Errorf("the viewer's context card lacks the sibling episode: %d %s", code, card)
	}
	h.rule("block", parental.SubjectLibrary, pfLibB)
	h.same("child", http.MethodPost, "/api/stream/sessions", pfKidB, `{"video_id":"{id}"}`)
	// an account nothing limits keeps the answer it had for a library it cannot read
	h.exec(`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, pfViewer, pfLibB)
	h.as = withLibraries(pfViewer, pfLibA)
	if rec := h.raw("viewer", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKidB+`"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer without the library: %d %s", rec.Code, rec.Body.String())
	}
}
