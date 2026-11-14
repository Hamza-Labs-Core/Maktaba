package router_test

// Epic 58 story 58.07 (and 58.06's T01): the cast grant against a real migrated Postgres
// behind the production router, its parental gate and the grant's own gate, with the
// grant's credential middleware and the auth gate in front as the home runs them.
//
//   - who may mint: a viewer, an admin, a restricted child within its limit; not no one
//     (401), not the admin token, a Maktaba Cloud sign-in or a grant itself (403); a hidden
//     video answers as a missing one (404);
//   - what a grant does: opens and keeps sessions, reports progress, reads its video, walks
//     its queue; any other item or route is 403 `cast_scope`;
//   - what holds it: its user's limit (a rule added after the mint hides the video), its
//     user (a deleted user's grant is no one), its six hours.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/castgrant"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

var (
	castKeysOnce sync.Once
	castKeySet   *keys.Set
	castKeysErr  error
)

// castKeys is one signing key set for the package's tests (an RSA key takes a while).
func castKeys(t testing.TB) *keys.Set {
	t.Helper()
	castKeysOnce.Do(func() {
		k, err := keys.Generate(2048)
		if err != nil {
			castKeysErr = err
			return
		}
		castKeySet = keys.NewSet(0)
		castKeySet.Replace(k)
	})
	if castKeysErr != nil {
		t.Fatal(castKeysErr)
	}
	return castKeySet
}

type castEnv struct {
	*pfEnv
	front http.Handler // the grant's middleware and the auth gate in front of the test's router
}

func setupCast(t *testing.T) *castEnv {
	t.Helper()
	e := setupFilterWith(t, func(r chi.Router, e *pfEnv) {
		router.MountCast(r, router.CastDeps{DB: e.db, Keys: castKeys(t), Gate: e.cast})
	})
	front := authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(e.h)
	front = castgrant.Bearer(castKeys(t), users.New(e.db), &authz.ACLStore{DB: e.db})(front)
	return &castEnv{pfEnv: e, front: front}
}

// mint asks for a grant as who; the status and the answer.
func (c *castEnv) mint(who, body string) (int, map[string]any) {
	c.t.Helper()
	rec := c.raw(who, http.MethodPost, "/api/cast/grants", body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (c *castEnv) grant(who, body string) string {
	c.t.Helper()
	code, out := c.mint(who, body)
	if code != http.StatusCreated {
		c.t.Fatalf("%s mint %s = %d %v", who, body, code, out)
	}
	g, _ := out["grant"].(string)
	return g
}

// with asks as a grant (no other credential).
func (c *castEnv) with(grant, method, path, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+grant)
	rec := httptest.NewRecorder()
	c.front.ServeHTTP(rec, req)
	return rec
}

func (c *castEnv) wantWith(status int, grant, method, path, body string) map[string]any {
	c.t.Helper()
	rec := c.with(grant, method, path, body)
	if rec.Code != status {
		c.t.Fatalf("grant %s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), status)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func wantScope(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"cast_scope"`) {
		t.Fatalf("got %d %s, want 403 cast_scope", rec.Code, rec.Body.String())
	}
}

func TestPGCastGrantWhoMayMint(t *testing.T) {
	c := setupCast(t)
	video := `{"video_id":"` + pfKid + `"}`
	for _, tc := range []struct {
		who, body string
		want      int
	}{
		{"", video, http.StatusUnauthorized},
		{"viewer", video, http.StatusCreated},
		{"viewer", `{"video_id":"` + pfTeen + `"}`, http.StatusCreated},
		{"admin", video, http.StatusCreated},
		{"child", video, http.StatusCreated},
		// a video above the child's limit answers as a missing one
		{"child", `{"video_id":"` + pfTeen + `"}`, http.StatusNotFound},
		{"child", `{"video_id":"00000000-0000-4000-8000-0000573affff"}`, http.StatusNotFound},
		{"admin-token", video, http.StatusForbidden},
		{"child-cloud", video, http.StatusForbidden},
		{"viewer", `{}`, http.StatusUnprocessableEntity},
		{"viewer", `{"video_id":"` + pfKid + `","queue_id":"00000000-0000-4000-8000-0000573affff"}`, http.StatusUnprocessableEntity},
		{"viewer", `{"video_id":"not-an-id"}`, http.StatusBadRequest},
		// another person's queue, and no queue at all, are missing
		{"viewer", `{"queue_id":"00000000-0000-4000-8000-0000573affff"}`, http.StatusNotFound},
	} {
		if code, out := c.mint(tc.who, tc.body); code != tc.want {
			t.Errorf("%q mint %s = %d %v, want %d", tc.who, tc.body, code, out, tc.want)
		}
	}

	// the answer: a cast_ grant, scope cast, at most six hours
	before := time.Now()
	code, out := c.mint("viewer", video)
	if code != http.StatusCreated || out["scope"] != "cast" || out["video_id"] != pfKid ||
		!strings.HasPrefix(out["grant"].(string), castgrant.Prefix) {
		t.Fatalf("mint answered %d %v", code, out)
	}
	exp, err := time.Parse(time.RFC3339, out["expires_at"].(string))
	if err != nil || exp.After(before.Add(castgrant.MaxTTL+time.Second)) || exp.Before(before.Add(castgrant.MaxTTL-time.Minute)) {
		t.Fatalf("expires_at %v (%v), want six hours from now", out["expires_at"], err)
	}
	// a grant mints no grant
	wantScope(t, c.with(out["grant"].(string), http.MethodPost, "/api/cast/grants", video))
	// and a grant is no token of the home's: the auth gate takes it for no one without
	// the cast_ prefix
	if rec := c.with(strings.TrimPrefix(out["grant"].(string), castgrant.Prefix), http.MethodGet, "/api/videos/"+pfKid, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the grant without its prefix answered %d", rec.Code)
	}
}

func TestPGCastGrantVideo(t *testing.T) {
	c := setupCast(t)
	g := c.grant("viewer", `{"video_id":"`+pfTeen+`"}`)

	// its video: read it, open a session, keep it, report progress, close it
	c.wantWith(http.StatusOK, g, http.MethodGet, "/api/videos/"+pfTeen, "")
	sess := c.wantWith(http.StatusCreated, g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfTeen+`","start_sec":12}`)
	sid, _ := sess["session_id"].(string)
	if sid == "" {
		t.Fatalf("no session: %v", sess)
	}
	c.wantWith(http.StatusOK, g, http.MethodGet, "/api/stream/sessions/"+sid, "")
	if rec := c.with(g, http.MethodPost, "/api/stream/sessions/"+sid+"/progress", `{"position_sec":42}`); rec.Code/100 != 2 {
		t.Fatalf("progress = %d %s", rec.Code, rec.Body.String())
	}
	var pos float64
	if err := c.db.QueryRow(`SELECT position_sec FROM playback_state WHERE user_id = $1 AND video_id = $2`, pfViewer, pfTeen).Scan(&pos); err != nil || pos != 42 {
		t.Fatalf("the receiver's progress is not the viewer's: %v %v", pos, err)
	}

	// another video (58.06's acceptance: a grant for A used for B is 403), and the
	// viewer's own session of another video, opened with their own sign-in
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`))
	wantScope(t, c.with(g, http.MethodGet, "/api/videos/"+pfKid, ""))
	// a body the gate cannot read is refused by the gate itself (cast_scope), never passed on
	// to the handler: trailing bytes or a second value carry no video past it
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"} x`))
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}{"video_id":"`+pfTeen+`"}`))
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions", `not json`))
	own := c.raw("viewer", http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`)
	var ownSess map[string]any
	_ = json.Unmarshal(own.Body.Bytes(), &ownSess)
	if own.Code != http.StatusCreated {
		t.Fatalf("viewer's own session: %d %s", own.Code, own.Body.String())
	}
	other := ownSess["session_id"].(string)
	wantScope(t, c.with(g, http.MethodGet, "/api/stream/sessions/"+other, ""))
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions/"+other+"/progress", `{"position_sec":1}`))
	wantScope(t, c.with(g, http.MethodDelete, "/api/stream/sessions/"+other, ""))

	// any other route
	for _, path := range []string{"/api/home", "/api/videos", "/api/me/history", "/api/libraries", "/api/auth/me", "/api/capabilities"} {
		wantScope(t, c.with(g, http.MethodGet, path, ""))
	}
	// a queue it does not name
	wantScope(t, c.with(g, http.MethodGet, "/api/me/play-queues/00000000-0000-4000-8000-0000573affff", ""))
	// its own session closes
	if rec := c.with(g, http.MethodDelete, "/api/stream/sessions/"+sid, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close = %d %s", rec.Code, rec.Body.String())
	}
}

func TestPGCastGrantQueue(t *testing.T) {
	c := setupCast(t)
	rec := c.raw("viewer", http.MethodPost, "/api/me/play-queues", `{"source":{"kind":"library","id":"`+pfLibA+`"}}`)
	var q map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if rec.Code != http.StatusCreated {
		t.Fatalf("queue: %d %s", rec.Code, rec.Body.String())
	}
	qid := q["id"].(string)
	// another person cannot cast the viewer's queue
	if code, _ := c.mint("child", `{"queue_id":"`+qid+`"}`); code != http.StatusNotFound {
		t.Fatalf("another person's queue minted %d", code)
	}
	g := c.grant("viewer", `{"queue_id":"`+qid+`"}`)

	doc := c.wantWith(http.StatusOK, g, http.MethodGet, "/api/me/play-queues/"+qid, "")
	items, _ := doc["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("the queue reads empty through the grant: %v", doc)
	}
	first := items[0].(map[string]any)["id"].(string)
	c.wantWith(http.StatusCreated, g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+first+`"}`)
	c.wantWith(http.StatusOK, g, http.MethodGet, "/api/videos/"+first, "")
	// it walks the queue
	c.wantWith(http.StatusOK, g, http.MethodPut, "/api/me/play-queues/"+qid+"/current", `{"pos":1,"version":`+jsonNum(doc["version"])+`}`)
	// library B's video is no item of this queue
	wantScope(t, c.with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKidB+`"}`))
	wantScope(t, c.with(g, http.MethodPost, "/api/me/play-queues/"+qid+"/items", `{"video_ids":["`+pfKidB+`"],"where":"next"}`))
	wantScope(t, c.with(g, http.MethodDelete, "/api/me/play-queues/"+qid, ""))
}

func jsonNum(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestPGCastGrantHeldToItsUser(t *testing.T) {
	c := setupCast(t)
	// the child's limit holds the grant as it holds the child: a block made after the mint
	g := c.grant("child", `{"video_id":"`+pfKid+`"}`)
	c.wantWith(http.StatusOK, g, http.MethodGet, "/api/videos/"+pfKid, "")
	c.rule("block", "video", pfKid)
	if rec := c.with(g, http.MethodGet, "/api/videos/"+pfKid, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a blocked video read through the child's grant: %d", rec.Code)
	}
	if rec := c.with(g, http.MethodPost, "/api/stream/sessions", `{"video_id":"`+pfKid+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("a blocked video opened through the child's grant: %d", rec.Code)
	}
	c.clearRules()

	// the device age limit the minting sign-in carried rides the grant
	limit := 9
	tok, _, err := castgrant.Mint(castKeys(t), pfViewer, &limit, principal.CastScope{VideoID: pfTeen}, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.with(tok, http.MethodGet, "/api/videos/"+pfTeen, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a 16+ video read through a 9+ device's grant: %d", rec.Code)
	}

	// a library taken from the viewer after the mint
	g = c.grant("viewer", `{"video_id":"`+pfKidB+`"}`)
	c.wantWith(http.StatusOK, g, http.MethodGet, "/api/videos/"+pfKidB, "")
	c.exec(`DELETE FROM library_acl WHERE user_id = $1 AND library_id = $2`, pfViewer, pfLibB)
	if rec := c.with(g, http.MethodGet, "/api/videos/"+pfKidB, ""); rec.Code/100 == 2 {
		t.Fatalf("a library taken away still reads through the grant: %d", rec.Code)
	}

	// a grant whose user is gone is no one
	gone := "00000000-0000-4000-8000-000000573099"
	c.exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1, 'pf-gone', 'x', false)`, gone)
	c.exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1, $2)`, gone, pfLibA)
	tok, _, err = castgrant.Mint(castKeys(t), gone, nil, principal.CastScope{VideoID: pfKid}, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	c.wantWith(http.StatusOK, tok, http.MethodGet, "/api/videos/"+pfKid, "")
	c.exec(`DELETE FROM library_acl WHERE user_id = $1`, gone)
	c.exec(`DELETE FROM users WHERE id = $1`, gone)
	if rec := c.with(tok, http.MethodGet, "/api/videos/"+pfKid, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a deleted user's grant answered %d", rec.Code)
	}

	// an expired grant is no one
	tok, _, err = castgrant.Mint(castKeys(t), pfViewer, nil, principal.CastScope{VideoID: pfKid}, time.Now().Add(-7*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if rec := c.with(tok, http.MethodGet, "/api/videos/"+pfKid, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an expired grant answered %d", rec.Code)
	}
}
