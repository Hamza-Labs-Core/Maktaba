//go:build integration

package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// On the api role's real router (Epic 58 story 58.03): a TV asks for a code with no account,
// its home reads and claims it with the server secret, and the TV's poll gets the payload once.
func TestTVCodesOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("owner@example.test")
	code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
	agent := e.client()
	red := agent.mustStatus(agent.do("POST", "/v1/servers/claims/redeem",
		map[string]string{"code": code, "name": "Home", "slug": "tvhome"}), http.StatusCreated).json(t)
	id, secret := red["server_id"].(string), red["server_secret"].(string)

	tv := e.client() // no token
	made := tv.mustStatus(tv.do("POST", "/v1/tv/codes", map[string]string{"platform": "tizen"}), http.StatusCreated).json(t)
	tvCode, pollSecret := strings.ReplaceAll(made["code"].(string), "-", ""), made["poll_secret"].(string)
	poll := func() resp {
		return tv.do("POST", "/v1/tv/codes/poll", map[string]string{"code": tvCode, "poll_secret": pollSecret})
	}
	tv.mustStatus(poll(), http.StatusAccepted)

	home := func(method, path, body, secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "198.51.100.250:40000"
		req.SetBasicAuth(id, secret)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	base := "/v1/homes/" + id + "/tv-codes/" + tvCode
	if rec := home("GET", base, "", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong secret: %d", rec.Code)
	}
	if rec := home("GET", base, "", secret); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"platform":"tizen"`) {
		t.Fatalf("lookup: %d %s", rec.Code, rec.Body)
	}
	grant := base64.RawURLEncoding.EncodeToString([]byte("a grant the home will accept"))
	if rec := home("POST", base+"/claim", `{"grant":"`+grant+`"}`, secret); rec.Code != http.StatusOK {
		t.Fatalf("claim: %d %s", rec.Code, rec.Body)
	}
	if rec := home("POST", base+"/claim", `{"grant":"`+grant+`"}`, secret); rec.Code != http.StatusConflict {
		t.Fatalf("claimed twice: %d %s", rec.Code, rec.Body)
	}
	got := tv.mustStatus(poll(), http.StatusOK).json(t)
	if got["grant"] != grant || got["home_id"] != id {
		t.Fatalf("payload %v", got)
	}
	if again := poll(); again.code != http.StatusGone || strings.Contains(string(again.body), grant) {
		t.Fatalf("polled again: %d %s", again.code, again.body)
	}
}

// Approving on maktaba.tv on the real router (story 58.03): the routes need a cloud sign-in; the
// owner of a home's registration reads the code but approves only on a home that linked their
// account; a linked home that is offline approves nothing, and the TV still waits.
func TestTVCodesApprovedOnTheWebOnTheRealRouter(t *testing.T) {
	e := newEnv(t)
	owner := e.registerVerified("tv-owner@example.test")
	code := owner.mustStatus(owner.do("POST", "/v1/servers/claims", nil), http.StatusOK).json(t)["code"].(string)
	agent := e.client()
	red := agent.mustStatus(agent.do("POST", "/v1/servers/claims/redeem",
		map[string]string{"code": code, "name": "Home", "slug": "tvwebhome"}), http.StatusCreated).json(t)
	id := red["server_id"].(string)

	tv := e.client()
	made := tv.mustStatus(tv.do("POST", "/v1/tv/codes", map[string]string{"platform": "webos"}), http.StatusCreated).json(t)
	tvCode, pollSecret := strings.ReplaceAll(made["code"].(string), "-", ""), made["poll_secret"].(string)

	if r := tv.do("GET", "/v1/tv/codes/"+tvCode, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("no account: %d", r.code)
	}
	if r := tv.do("POST", "/v1/tv/codes/"+tvCode+"/approve", map[string]string{"server_id": id}); r.code != http.StatusUnauthorized {
		t.Fatalf("no account approves: %d", r.code)
	}
	got := owner.mustStatus(owner.do("GET", "/v1/tv/codes/"+tvCode, nil), http.StatusOK).json(t)
	if got["platform"] != "webos" {
		t.Fatalf("lookup %v", got)
	}
	// owning the registration is not a link: the home has no user to approve as
	if r := owner.do("POST", "/v1/tv/codes/"+tvCode+"/approve", map[string]string{"server_id": id}); r.code != http.StatusForbidden ||
		!strings.Contains(string(r.body), "not_linked") {
		t.Fatalf("owner approves: %d %s", r.code, r.body)
	}
	if _, err := e.pool.DB.Exec(`INSERT INTO home_links (server_id, cloud_user_id) SELECT $1, id FROM users WHERE email = $2`,
		id, "tv-owner@example.test"); err != nil {
		t.Fatal(err)
	}
	// linked, but the home's tunnel is down
	if r := owner.do("POST", "/v1/tv/codes/"+tvCode+"/approve", map[string]string{"server_id": id}); r.code != http.StatusConflict ||
		!strings.Contains(string(r.body), "home_offline") {
		t.Fatalf("offline home: %d %s", r.code, r.body)
	}
	tv.mustStatus(tv.do("POST", "/v1/tv/codes/poll", map[string]string{"code": tvCode, "poll_secret": pollSecret}), http.StatusAccepted)
}
