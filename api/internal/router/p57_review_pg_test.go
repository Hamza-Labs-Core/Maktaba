package router_test

// Epic 57 story 57.17: the review routes behind the production router, against a real
// migrated Postgres (MAKTABA_TEST_PG_URL): who may call each (no one, a viewer, a
// restricted child, that child's guardian, another child's guardian, an admin), what
// the list shows to each, and what the admin's answer stores.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/parental"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	rvAdmin    = "00000000-0000-4000-8000-000000571701"
	rvGuardian = "00000000-0000-4000-8000-000000571702"
	rvOtherG   = "00000000-0000-4000-8000-000000571703"
	rvChild    = "00000000-0000-4000-8000-000000571704"
	rvChild2   = "00000000-0000-4000-8000-000000571705"
	rvViewer   = "00000000-0000-4000-8000-000000571706"
	rvLibKids  = "00000000-0000-4000-8000-00005717a000"
	rvLibTeens = "00000000-0000-4000-8000-00005717b000"
	rvLibGrown = "00000000-0000-4000-8000-00005717c000"
)

type reviewEnv struct {
	t  *testing.T
	db *sql.DB
	h  http.Handler
	rc *parental.Recomputer
}

func setupReview(t *testing.T) *reviewEnv {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	e := &reviewEnv{t: t, db: db}
	e.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES
		($1,'rv-admin','x',true), ($2,'rv-guardian','x',false), ($3,'rv-other-guardian','x',false),
		($4,'rv-child','x',false), ($5,'rv-child-two','x',false), ($6,'rv-viewer','x',false)`,
		rvAdmin, rvGuardian, rvOtherG, rvChild, rvChild2, rvViewer)
	e.exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'RvKids','{/media/rv-kids}'),
		($2,'RvTeens','{/media/rv-teens}'), ($3,'RvGrown','{/media/rv-grown}')`, rvLibKids, rvLibTeens, rvLibGrown)
	// the child reads Kids, child two reads Teens, the viewer reads all three, and no
	// restricted account reads Grown
	e.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1,$4), ($2,$5), ($3,$4), ($3,$5), ($3,$6)`,
		rvChild, rvChild2, rvViewer, rvLibKids, rvLibTeens, rvLibGrown)
	e.exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1), ($2, 13, 1)`, rvChild, rvChild2)
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2), ($3, $4)`, rvChild, rvGuardian, rvChild2, rvOtherG)

	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	e.rc = router.MountP57Tiers(r, router.P57TiersDeps{DB: db})
	router.MountP57Review(r, router.P57ReviewDeps{DB: db, Recompute: e.rc})
	e.drain()
	e.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: rvAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceCookie}
		case "admin-token": // the admin token: an admin with no user row
			p = &principal.Principal{UserID: "admin-token", IsAdmin: true, AccessAllLibraries: true}
		case "guardian":
			p = &principal.Principal{UserID: rvGuardian, Source: principal.SourceCookie}
		case "other-guardian":
			p = &principal.Principal{UserID: rvOtherG, Source: principal.SourceCookie}
		case "child":
			p = &principal.Principal{UserID: rvChild, Libraries: []string{rvLibKids}, Source: principal.SourceCookie}
		case "viewer":
			p = &principal.Principal{UserID: rvViewer, Libraries: []string{rvLibKids, rvLibTeens, rvLibGrown}, Source: principal.SourceCookie}
		case "viewer-all": // a viewer every library is shared with is still no admin
			p = &principal.Principal{UserID: uuid.NewString(), AccessAllLibraries: true, Source: principal.SourcePAT}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return e
}

func (e *reviewEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func (e *reviewEnv) drain() {
	e.t.Helper()
	if _, err := e.rc.Drain(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *reviewEnv) video(lib, title string) string {
	e.t.Helper()
	id := uuid.NewString()
	e.exec(`INSERT INTO videos (id, library_id, path, filename, content_hash, size_bytes, mtime, state, title, duration_sec, updated_at, created_at)
		VALUES ($1::uuid, $2::uuid, '/media/v/' || $1::text || '.mp4', $1::text || '.mp4',
		        encode(sha256(convert_to($1::text, 'UTF8')), 'hex'), 1000, now(), 'ready', $3, 100, now(), now())`, id, lib, title)
	return id
}

func (e *reviewEnv) do(who, method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-As", who)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// list reads the review list as who and answers its items by video.
func (e *reviewEnv) list(who, query string) (map[string]map[string]any, map[string]any) {
	e.t.Helper()
	code, out := e.do(who, http.MethodGet, "/api/parental/review"+query, "")
	if code != http.StatusOK {
		e.t.Fatalf("GET review%s as %s: %d %v", query, who, code, out)
	}
	items := map[string]map[string]any{}
	for _, raw := range out["items"].([]any) {
		it := raw.(map[string]any)
		items[it["video_id"].(string)] = it
	}
	if int(out["total"].(float64)) < len(items) {
		e.t.Fatalf("GET review%s as %s: total %v under its %d items", query, who, out["total"], len(items))
	}
	return items, out
}

func (e *reviewEnv) want(video string, tier int, source string) {
	e.t.Helper()
	e.drain()
	var (
		gotTier   sql.NullInt64
		gotSource sql.NullString
	)
	if err := e.db.QueryRow(`SELECT age_tier, age_tier_source FROM videos WHERE id = $1`, video).Scan(&gotTier, &gotSource); err != nil {
		e.t.Fatal(err)
	}
	got := -1
	if gotTier.Valid {
		got = int(gotTier.Int64)
	}
	if got != tier || gotSource.String != source {
		e.t.Fatalf("video tier = %d from %q, want %d from %q", got, gotSource.String, tier, source)
	}
}

// T04: the review list and the admin's answer, as every caller.
func TestP57ReviewPG(t *testing.T) {
	t.Parallel()
	e := setupReview(t)
	low := e.video(rvLibKids, "low")          // a signal under the line
	sure := e.video(rvLibKids, "sure")        // a detected tier nobody confirmed
	silent := e.video(rvLibKids, "silent")    // no transcript
	foreign := e.video(rvLibTeens, "foreign") // a language the screen does not read
	teens := e.video(rvLibTeens, "teens")     // a detected tier, the other child's library
	grown := e.video(rvLibGrown, "grown")     // a library no restricted account reads
	rated := e.video(rvLibKids, "rated")      // an admin rated it already: nothing to review
	clean := e.video(rvLibKids, "clean")      // a signal with no tier over the line: nothing to review
	e.exec(`INSERT INTO video_content_signals (video_id, tier, confidence, categories, not_checked, model, input_hash) VALUES
		($1, 16, 0.55, '{"coarse_language":{"level":2,"evidence":[12.5,80,301.25]}}', NULL, 'm1', 'h'),
		($2, 13, 0.92, '{"violence":{"level":1,"evidence":[40]},"frightening_scenes":{"level":2,"evidence":[41.5,90]}}', NULL, 'm1', 'h'),
		($3, NULL, 0, '{}', 'no_transcript', '', 'h'),
		($4, NULL, 0, '{}', 'unsupported_language', '', 'h'),
		($5, 18, 0.99, '{"mature_themes":{"level":3,"evidence":[5]}}', NULL, 'm1', 'h'),
		($6, 18, 0.85, '{"sexual_content":{"level":3,"evidence":[7]}}', NULL, 'm1', 'h'),
		($7, 16, 0.9, '{}', NULL, 'm1', 'h'),
		($8, NULL, 0.95, '{}', NULL, 'm1', 'h')`, low, sure, silent, foreign, teens, grown, rated, clean)
	e.exec(`UPDATE videos SET age_tier_admin = 9 WHERE id = $1`, rated)
	e.drain()
	e.want(sure, 13, "detected")
	e.want(low, -1, "")
	e.want(rated, 9, "admin_video")

	// who may not read: no one 401; a viewer and a restricted child 403
	for who, want := range map[string]int{"": http.StatusUnauthorized, "viewer": http.StatusForbidden,
		"viewer-all": http.StatusForbidden, "child": http.StatusForbidden} {
		code, out := e.do(who, http.MethodGet, "/api/parental/review", "")
		if code != want || out["items"] != nil {
			t.Errorf("GET review as %q: %d %v, want %d and no items", who, code, out, want)
		}
	}
	if _, out := e.do("child", http.MethodGet, "/api/parental/review", ""); out["code"] != "restricted" {
		t.Errorf("GET review as the child: %v, want the restricted problem", out)
	}

	// the admin: every library; each item's state, categories and evidence times
	items, out := e.list("admin", "")
	if len(items) != 6 || out["total"] != float64(6) || out["min_confidence"] != 0.8 {
		t.Fatalf("the admin's list: %d items, total %v, line %v; want 6, 6, 0.8", len(items), out["total"], out["min_confidence"])
	}
	for video, state := range map[string]string{low: "low_confidence", sure: "unconfirmed", silent: "not_checked",
		foreign: "not_checked", teens: "unconfirmed", grown: "unconfirmed"} {
		if items[video] == nil || items[video]["state"] != state {
			t.Errorf("the admin's item %s = %v, want state %s", video, items[video], state)
		}
	}
	if items[rated] != nil || items[clean] != nil {
		t.Error("the list shows a video an admin rated or a signal with nothing to review")
	}
	it := items[low]
	ev := it["categories"].(map[string]any)["coarse_language"].(map[string]any)
	if it["detected_tier"] != float64(16) || it["confidence"].(float64) > 0.56 || it["tier"] != nil || it["source"] != nil ||
		it["title"] != "low" || it["library_id"] != rvLibKids || it["library_name"] != "RvKids" || it["model"] != "m1" ||
		ev["level"] != float64(2) || len(ev["evidence"].([]any)) != 3 || ev["evidence"].([]any)[2] != 301.25 {
		t.Errorf("the low-confidence item = %v", it)
	}
	if it := items[sure]; it["tier"] != float64(13) || it["source"] != "detected" || it["not_checked"] != nil {
		t.Errorf("the unconfirmed item = %v", it)
	}
	if it := items[silent]; it["not_checked"] != "no_transcript" || it["detected_tier"] != nil {
		t.Errorf("the item with no transcript = %v", it)
	}
	if it := items[foreign]; it["not_checked"] != "unsupported_language" {
		t.Errorf("the item in another language = %v", it)
	}

	// a guardian: only the libraries their children can read
	items, out = e.list("guardian", "")
	if len(items) != 3 || out["total"] != float64(3) || items[low] == nil || items[sure] == nil || items[silent] == nil {
		t.Fatalf("the guardian's list = %v, want the three of the child's library", items)
	}
	items, _ = e.list("other-guardian", "")
	if len(items) != 2 || items[foreign] == nil || items[teens] == nil {
		t.Fatalf("the other guardian's list = %v, want the two of the other child's library", items)
	}
	// the child's access widens: the guardian's list follows on the next request
	e.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2)`, rvChild, rvLibTeens)
	if items, _ = e.list("guardian", ""); len(items) != 5 || items[grown] != nil {
		t.Fatalf("the guardian's list after the child got a library = %d items", len(items))
	}
	e.exec(`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, rvChild, rvLibTeens)
	// a guardian no longer named reads nothing
	e.exec(`DELETE FROM parental_guardians WHERE guardian_id = $1`, rvOtherG)
	if code, _ := e.do("other-guardian", http.MethodGet, "/api/parental/review", ""); code != http.StatusForbidden {
		t.Errorf("GET review as a former guardian: %d, want 403", code)
	}
	e.exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2)`, rvChild2, rvOtherG)

	// the state filter, the page, and what is refused
	if items, out = e.list("admin", "?state=not_checked"); len(items) != 2 || out["total"] != float64(2) {
		t.Errorf("state=not_checked: %d items, total %v", len(items), out["total"])
	}
	if items, _ = e.list("guardian", "?state=unconfirmed"); len(items) != 1 || items[sure] == nil {
		t.Errorf("the guardian's state=unconfirmed: %v", items)
	}
	if items, _ = e.list("admin", "?state=low_confidence"); len(items) != 1 || items[low] == nil {
		t.Errorf("state=low_confidence: %v", items)
	}
	if items, out = e.list("admin", "?limit=2&offset=4"); len(items) != 2 || out["total"] != float64(6) || out["limit"] != float64(2) || out["offset"] != float64(4) {
		t.Errorf("limit=2&offset=4: %d items, %v", len(items), out)
	}
	for _, q := range []string{"?state=all", "?limit=0", "?limit=201", "?limit=x", "?offset=-1"} {
		if code, _ := e.do("admin", http.MethodGet, "/api/parental/review"+q, ""); code != http.StatusBadRequest {
			t.Errorf("GET review%s: %d, want 400", q, code)
		}
	}
	// the line moves: what was under it is now a detected tier to confirm
	e.exec(`INSERT INTO app_settings (key, value) VALUES ('parental.detect_min_confidence', '0.5'::jsonb)`)
	if items, out = e.list("admin", ""); items[low]["state"] != "unconfirmed" || out["min_confidence"] != 0.5 {
		t.Errorf("after the line moved to 0.5: %v, line %v", items[low], out["min_confidence"])
	}
	e.want(low, 16, "detected")
	e.exec(`DELETE FROM app_settings WHERE key = 'parental.detect_min_confidence'`)
	e.want(low, -1, "")

	// the answer: admin only; everyone else stores nothing
	for who, want := range map[string]int{"": http.StatusUnauthorized, "viewer": http.StatusForbidden, "viewer-all": http.StatusForbidden,
		"child": http.StatusForbidden, "guardian": http.StatusForbidden, "other-guardian": http.StatusForbidden} {
		for _, body := range []string{`{"tier":0}`, `{"tier":null}`} {
			if code, _ := e.do(who, http.MethodPost, "/api/parental/review/"+sure, body); code != want {
				t.Errorf("POST review %s as %q: %d, want %d", body, who, code, want)
			}
		}
	}
	var stored int
	if err := e.db.QueryRow(`SELECT (SELECT count(*) FROM video_content_signals WHERE reviewed_at IS NOT NULL)
		+ (SELECT count(*) FROM videos WHERE age_tier_admin IS NOT NULL AND id <> $1)`, rated).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("a refused caller stored %d reviews or tiers (%v)", stored, err)
	}
	e.want(sure, 13, "detected")

	// confirm: the detected tier becomes the admin's, and the item leaves the list
	code, got := e.do("admin", http.MethodPost, "/api/parental/review/"+sure, `{"tier":13}`)
	if code != http.StatusOK || got["tier"] != float64(13) || got["source"] != "admin_video" {
		t.Fatalf("confirm: %d %v", code, got)
	}
	var reviewer sql.NullString
	if err := e.db.QueryRow(`SELECT reviewed_by::text FROM video_content_signals WHERE video_id = $1 AND reviewed_at IS NOT NULL`, sure).Scan(&reviewer); err != nil || reviewer.String != rvAdmin {
		t.Fatalf("the reviewed signal's reviewer = %v (%v), want the admin", reviewer, err)
	}
	e.want(sure, 13, "admin_video")
	// change: another tier than the detected one, looser too (the admin decides)
	if code, got = e.do("admin", http.MethodPost, "/api/parental/review/"+teens, `{"tier":9}`); code != http.StatusOK || got["tier"] != float64(9) || got["source"] != "admin_video" {
		t.Fatalf("change: %d %v", code, got)
	}
	e.want(teens, 9, "admin_video")
	// clear: no admin tier, and the reviewed signal rates nothing any more
	if code, got = e.do("admin", http.MethodPost, "/api/parental/review/"+grown, `{"tier":null}`); code != http.StatusOK || got["tier"] != nil || got["source"] != nil {
		t.Fatalf("clear: %d %v", code, got)
	}
	e.want(grown, -1, "")
	// a video the screen could not check, rated by the admin token (no user row)
	if code, got = e.do("admin-token", http.MethodPost, "/api/parental/review/"+silent, `{"tier":0}`); code != http.StatusOK || got["tier"] != float64(0) {
		t.Fatalf("rate an unchecked video with the admin token: %d %v", code, got)
	}
	e.want(silent, 0, "admin_video")
	if items, _ = e.list("admin", ""); len(items) != 2 || items[low] == nil || items[foreign] == nil {
		t.Fatalf("the list after four answers = %v, want the two left", items)
	}
	if items, _ = e.list("guardian", ""); len(items) != 1 || items[low] == nil {
		t.Fatalf("the guardian's list after the answers = %v", items)
	}

	// what the admin is refused
	for path, want := range map[string]int{
		"/api/parental/review/" + uuid.NewString():                http.StatusNotFound, // no such video
		"/api/parental/review/" + e.video(rvLibKids, "no signal"): http.StatusNotFound, // nothing to review
		"/api/parental/review/not-an-id":                          http.StatusBadRequest,
	} {
		if code, _ := e.do("admin", http.MethodPost, path, `{"tier":9}`); code != want {
			t.Errorf("POST %s as admin: %d, want %d", path, code, want)
		}
	}
	for _, body := range []string{`{"tier":12}`, `{}`, `{"tier":"13"}`, `{"tier":9,"extra":1}`, `not json`} {
		if code, _ := e.do("admin", http.MethodPost, "/api/parental/review/"+low, body); code != http.StatusBadRequest {
			t.Errorf("POST %s as admin: %d, want 400", body, code)
		}
	}
	e.want(low, -1, "")

	// the screen reads the video again (a new transcript): the review starts over
	e.exec(`UPDATE video_content_signals SET tier = 18, confidence = 0.9, input_hash = 'h2', reviewed_by = NULL, reviewed_at = NULL WHERE video_id = $1`, grown)
	e.want(grown, 18, "detected")
	if items, _ = e.list("admin", ""); items[grown] == nil || items[grown]["state"] != "unconfirmed" {
		t.Fatalf("a signal written again is not back in the list: %v", items[grown])
	}
}
