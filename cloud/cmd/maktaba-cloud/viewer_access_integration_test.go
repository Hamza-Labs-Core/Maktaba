//go:build integration

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	broadcasth "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/broadcast"
)

// Story 50.26 on the api role's real router: invites and channel access, the role rule of each
// door (the home, the account that owns it, a service admin), and the access posts the edge gets.

// fakeEdge records the access posts and answers the passes post with fixed passes.
type fakeEdge struct {
	srv    *httptest.Server
	mu     sync.Mutex
	posts  []bc.AccessState
	passes string // the JSON body of /v1/control/passes
}

func newFakeEdge(t *testing.T) *fakeEdge {
	f := &fakeEdge{passes: `{"passes":[],"uses":{}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/control/access":
			var s bc.AccessState
			if err := json.Unmarshal(controlPayload(t, string(body)), &s); err != nil {
				t.Errorf("access post: %v", err)
			}
			f.mu.Lock()
			f.posts = append(f.posts, s)
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case "/v1/control/passes":
			f.mu.Lock()
			defer f.mu.Unlock()
			_, _ = w.Write([]byte(f.passes))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeEdge) last(t *testing.T) bc.AccessState {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		t.Fatal("no access post reached the edge")
	}
	return f.posts[len(f.posts)-1]
}

// controlPayload is a control post's payload JSON (the signature is the edge's to check).
func controlPayload(t *testing.T, text string) []byte {
	t.Helper()
	rest := strings.TrimPrefix(text, bc.ControlPrefix)
	body, _, _ := strings.Cut(rest, ".")
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// accessEnv runs the api with a broadcast key and the fake edge as its ingest host.
func accessEnv(t *testing.T, edge *fakeEdge, adjust func(*config.Config)) (*env, ed25519.PublicKey) {
	e, k := accessEnvKey(t, edge, adjust)
	accessKey = &k
	return e, k.Priv.Public().(ed25519.PublicKey)
}

// accessKey is the last accessEnv's signing key (the tests' own worker uses it).
var accessKey *bc.Key

func accessEnvKey(t *testing.T, edge *fakeEdge, adjust func(*config.Config)) (*env, bc.Key) {
	t.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	key := filepath.Join(t.TempDir(), "broadcast.key")
	if err := os.WriteFile(key, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEnvCfg(t, nil, func(c *config.Config) {
		c.Broadcast.KeyPath = key
		c.Broadcast.IngestURL = edge.srv.URL
		c.Broadcast.WatchURL = "https://watch.example.test"
		if adjust != nil {
			adjust(c)
		}
	})
	k, err := bc.KeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	return e, k
}

// publisherHome is a Pro home, a test publisher with the agreement accepted, with one channel.
func publisherHome(e *env, email, slug, chSlug string) (*client, bhome, string) {
	e.t.Helper()
	owner := e.registerVerified(email)
	h := claimHome(e, owner, slug)
	setPlan(e, e.userID(email), "pro")
	e.exec(`UPDATE servers SET broadcast_test_publisher = true WHERE id = $1`, h.id)
	owner.mustStatus(owner.do("POST", "/v1/broadcast/agreement", map[string]int{"version": 1}), http.StatusNoContent)
	ch := h.agent.mustStatus(h.agent.do("POST", "/v1/broadcast/channels", channel(chSlug, "invite", true, 1)), http.StatusCreated).json(e.t)
	return owner, h, ch["id"].(string)
}

func TestViewerInvitesAndTheirRoles(t *testing.T) {
	edge := newFakeEdge(t)
	e, pub := accessEnv(t, edge, nil)
	owner, h, ch := publisherHome(e, "pub@example.test", "pubhome", "news")
	stranger, h2, ch2 := publisherHome(e, "other@example.test", "otherhome", "sport")
	base := "/v1/broadcast/homes/" + h.id

	// a publish already told the edge about the channel
	if s := edge.last(t); s.Pub != h2.id && s.Pub != h.id {
		t.Fatalf("publish push: %+v", s)
	}

	// Role rule: no credential, no door
	if r := e.client().do("GET", "/v1/broadcast/invites", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("the agent door without a home's secret: %d", r.code)
	}
	if r := e.client().do("GET", base+"/invites", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("the account door signed out: %d", r.code)
	}
	// Role rule: only the home's owner may use the account door; anyone else sees no such home
	for _, c := range []struct{ method, path string }{
		{"GET", base + "/invites"}, {"POST", base + "/invites"}, {"GET", base + "/access"},
		{"PUT", base + "/channels/" + ch + "/access"}, {"PUT", base + "/viewer-limits"},
	} {
		if r := stranger.do(c.method, c.path, map[string]any{"access": "invite", "max_uses": 2}); r.code != http.StatusNotFound {
			t.Fatalf("a stranger %s %s: %d %s", c.method, c.path, r.code, r.body)
		}
	}

	// the owner creates an invite in the cloud web app; the home creates one from its page
	inv := owner.mustStatus(owner.do("POST", base+"/invites", map[string]any{"channels": []string{ch}, "label": "Family in Cairo", "max_uses": 2}), http.StatusCreated).json(t)
	if !strings.HasPrefix(inv["url"].(string), "https://watch.example.test/i/"+bc.InvitePrefix) || !bc.ValidShortCode(inv["short_code"].(string)) {
		t.Fatalf("invite: %v", inv)
	}
	token := strings.TrimPrefix(inv["url"].(string), "https://watch.example.test/i/")
	claims, err := bc.VerifyInvite(map[string]ed25519.PublicKey{bc.KidFor(pub): pub}, token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claims.Pub != h.id || claims.Max != 2 || len(claims.Chs) != 1 || claims.Chs[0] != bc.ChannelID(h.id, "news") || claims.Inv != inv["id"] {
		t.Fatalf("claims %+v", claims)
	}
	if exp := time.Unix(claims.Exp, 0); exp.Before(time.Now().Add(29*24*time.Hour)) || exp.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("default expiry: %v", exp)
	}
	all := h.agent.mustStatus(h.agent.do("POST", "/v1/broadcast/invites", map[string]any{"expires_in_days": 365, "max_uses": 500}), http.StatusCreated).json(t)
	if s := edge.last(t); s.Pub != h.id || s.Codes[inv["short_code"].(string)] != token || len(s.Codes) != 2 || s.MaxSessionsPerIP != 3 {
		t.Fatalf("the edge's access state: %+v", s)
	}
	for _, bad := range []map[string]any{{"max_uses": 501}, {"expires_in_days": 366}, {"label": strings.Repeat("x", 81)}, {"channels": []string{"nope"}}} {
		if r := owner.do("POST", base+"/invites", bad); r.code != http.StatusBadRequest {
			t.Fatalf("%v: %d", bad, r.code)
		}
	}
	// Role rule: an invite grants only its publisher's channels
	if r := owner.do("POST", base+"/invites", map[string]any{"channels": []string{ch2}}); r.code != http.StatusBadRequest || r.errCode() != "bad_channel" {
		t.Fatalf("another home's channel: %d %s", r.code, r.body)
	}
	// Role rule: another home can't see or revoke this home's invites
	if r := h2.agent.do("DELETE", "/v1/broadcast/invites/"+inv["id"].(string), nil); r.code != http.StatusNotFound {
		t.Fatalf("another home revoked: %d", r.code)
	}
	if list := h2.agent.mustStatus(h2.agent.do("GET", "/v1/broadcast/invites", nil), http.StatusOK).json(t)["invites"].([]any); len(list) != 0 {
		t.Fatalf("another home's list: %v", list)
	}
	if r := stranger.do("DELETE", base+"/invites/"+inv["id"].(string), nil); r.code != http.StatusNotFound {
		t.Fatalf("a stranger revoked: %d", r.code)
	}

	// uses and devices come from the edge; one device's pass revoked
	edge.mu.Lock()
	edge.passes = `{"passes":[{"id":"p1","inv":"` + inv["id"].(string) + `","first_use":1,"last_use":2},{"id":"p2","inv":"` + all["id"].(string) + `","first_use":1,"last_use":2}],"uses":{"` + inv["id"].(string) + `":1}}`
	edge.mu.Unlock()
	list := owner.mustStatus(owner.do("GET", base+"/invites", nil), http.StatusOK).json(t)["invites"].([]any)
	if len(list) != 2 {
		t.Fatalf("list: %v", list)
	}
	for _, x := range list {
		m := x.(map[string]any)
		if m["id"] == inv["id"] && (m["uses"].(float64) != 1 || m["label"] != "Family in Cairo" || m["status"] != "active") {
			t.Fatalf("uses: %v", m)
		}
	}
	devs := owner.mustStatus(owner.do("GET", base+"/invites/"+inv["id"].(string)+"/devices", nil), http.StatusOK).json(t)["devices"].([]any)
	if len(devs) != 1 || devs[0].(map[string]any)["id"] != "p1" {
		t.Fatalf("devices: %v", devs)
	}
	owner.mustStatus(owner.do("DELETE", base+"/invites/"+inv["id"].(string)+"/devices/p1", nil), http.StatusNoContent)
	if s := edge.last(t); len(s.RevokedPasses) != 1 || s.RevokedPasses[0] != "p1" {
		t.Fatalf("revoked pass: %+v", s)
	}
	if r := h2.agent.do("DELETE", "/v1/broadcast/invites/"+inv["id"].(string)+"/devices/p1", nil); r.code != http.StatusNotFound {
		t.Fatalf("another home revoked a device: %d", r.code)
	}

	// revoking an invite: no code at the edge, its id in the revoked list (new tokens stop)
	before := edge.last(t).Version
	owner.mustStatus(owner.do("DELETE", base+"/invites/"+inv["id"].(string), nil), http.StatusNoContent)
	s := edge.last(t)
	if s.Version <= before || len(s.RevokedInvites) != 1 || s.RevokedInvites[0] != inv["id"] {
		t.Fatalf("after revoking: %+v", s)
	}
	if _, ok := s.Codes[inv["short_code"].(string)]; ok {
		t.Fatal("a revoked invite's code is still mapped")
	}
	if r := owner.do("DELETE", base+"/invites/"+inv["id"].(string), nil); r.code != http.StatusNotFound {
		t.Fatalf("revoked twice: %d", r.code)
	}

	// Role rule: staff may list and revoke (with a reason, audited), never through a customer token
	ops := e.serviceAdmin("ops@example.test")
	path := "/v1/admin/servers/" + h.id + "/broadcast/invites"
	if r := owner.do("GET", path, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("a customer on the console: %d", r.code)
	}
	if r := e.staff("norole@example.test", "").do("GET", path, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff without the role: %d", r.code)
	}
	for _, x := range ops.mustStatus(ops.do("GET", path, nil), http.StatusOK).json(t)["invites"].([]any) {
		if m := x.(map[string]any); m["url"] != "" || m["short_code"] != "" {
			t.Fatalf("staff saw a link: %v", m)
		}
	}
	rev := "/v1/admin/broadcast/invites/" + all["id"].(string) + "/revoke"
	if r := ops.do("POST", rev, map[string]any{}); r.code != http.StatusBadRequest || r.errCode() != "reason_required" {
		t.Fatalf("no reason: %d", r.code)
	}
	ops.mustStatus(ops.do("POST", rev, map[string]any{"reason": "abuse report 12"}), http.StatusNoContent)
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'admin.broadcast.invite.revoke' AND target_id = $1`, all["id"]) != 1 {
		t.Fatal("staff revoke not audited")
	}
	if e.count(`SELECT count(*) FROM servers WHERE id = $1 AND broadcast_access_version > broadcast_access_pushed`, h.id) != 1 {
		t.Fatal("a staff revoke left nothing for the worker to push")
	}
	// the worker's push (what PushPending does every minute) and its hourly refresh
	wd := &broadcasth.Deps{DB: e.pool.DB, Store: &billingpkg.BroadcastStore{DB: e.pool.DB}, Key: accessKey, Edge: broadcasth.HTTPEdge{Base: edge.srv.URL}}
	if n, err := wd.PushPending(context.Background()); err != nil || n != 1 {
		t.Fatalf("push pending: %d %v", n, err)
	}
	if s := edge.last(t); len(s.RevokedInvites) != 2 {
		t.Fatalf("staff revoke at the edge: %+v", s)
	}
	if n, err := wd.RefreshAll(context.Background()); err != nil || n != 2 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	if n, _ := wd.PushPending(context.Background()); n != 2 {
		t.Fatalf("refresh pushed %d", n)
	}

	// limits: the publisher's own, validated
	if r := owner.do("PUT", base+"/viewer-limits", map[string]any{"max_sessions_per_ip": 51}); r.code != http.StatusBadRequest {
		t.Fatalf("51 per IP: %d", r.code)
	}
	owner.mustStatus(owner.do("PUT", base+"/viewer-limits", map[string]any{"max_sessions_per_ip": 20, "daily_minutes": 120}), http.StatusNoContent)
	acc := owner.mustStatus(owner.do("GET", base+"/access", nil), http.StatusOK).json(t)
	if acc["max_sessions_per_ip"].(float64) != 20 || acc["daily_minutes"].(float64) != 120 || acc["viewers_pay"] != false || acc["max_viewers"].(float64) != 5 {
		t.Fatalf("access: %v", acc)
	}
	if r := owner.do("PUT", base+"/channels/"+ch+"/access", map[string]any{"max_viewers": 6}); r.code != http.StatusPaymentRequired {
		t.Fatalf("a channel cap above the publisher's: %d", r.code)
	}
	owner.mustStatus(owner.do("PUT", base+"/channels/"+ch+"/access", map[string]any{"max_viewers": 4, "indexable": true}), http.StatusNoContent)
	if c := edge.last(t).Chans[bc.ChannelID(h.id, "news")]; c.MaxViewers != 4 || !c.Indexable || c.Access != "invite" {
		t.Fatalf("channel at the edge: %+v", c)
	}
	// guest sharing never opens a channel
	if r := owner.do("PUT", base+"/channels/"+ch+"/access", map[string]any{"access": "open"}); r.code != http.StatusPaymentRequired || r.errCode() != "broadcast_limit" {
		t.Fatalf("guest sharing opened: %d %s", r.code, r.body)
	}
}

func TestViewerAccessOpenNeedsVerification(t *testing.T) {
	withBroadcastPrices(t)
	edge := newFakeEdge(t)
	// the flag off: the Broadcaster plan alone doesn't open a channel
	off, _ := accessEnv(t, edge, nil)
	owner, h, ch := publisherHome(off, "a@example.test", "ahome", "news")
	off.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
	if r := owner.do("PUT", "/v1/broadcast/homes/"+h.id+"/channels/"+ch+"/access", map[string]any{"access": "open"}); r.code != http.StatusForbidden || r.errCode() != "broadcast_open_not_enabled" {
		t.Fatalf("flag off: %d %s", r.code, r.body)
	}

	on, _ := accessEnv(t, edge, func(c *config.Config) {
		c.Broadcast.Enabled = true
		c.Broadcast.OpenAccess = true
	})
	owner, h, ch = publisherHome(on, "b@example.test", "bhome", "news")
	on.exec(`UPDATE servers SET broadcast_plan = 'broadcaster' WHERE id = $1`, h.id)
	path := "/v1/broadcast/homes/" + h.id + "/channels/" + ch + "/access"
	// the first publish was just now: not verified
	r := owner.do("PUT", path, map[string]any{"access": "open"})
	if r.code != http.StatusForbidden || r.errCode() != "broadcast_open_needs_verification" || r.json(t)["key"] != "broadcast.openNeedsVerification" {
		t.Fatalf("unverified: %d %s", r.code, r.body)
	}
	if r := h.agent.do("POST", "/v1/broadcast/channels", channel("sport", "open", true, 1)); r.code != http.StatusForbidden || r.errCode() != "broadcast_open_needs_verification" {
		t.Fatalf("an unverified publish as open: %d %s", r.code, r.body)
	}
	if v := owner.mustStatus(owner.do("GET", "/v1/broadcast/homes/"+h.id+"/access", nil), http.StatusOK).json(t)["verification"].(map[string]any); v["verified"] != false || v["paid_plan"] != true || v["email_confirmed"] != true {
		t.Fatalf("verification: %v", v)
	}
	// 8 days later: verified, the channel opens and the edge hears open is allowed
	on.exec(`UPDATE broadcast_channels SET published_at = now() - interval '8 days' WHERE server_id = $1`, h.id)
	owner.mustStatus(owner.do("PUT", path, map[string]any{"access": "open"}), http.StatusNoContent)
	if s := edge.last(t); !s.OpenAllowed || s.Chans[bc.ChannelID(h.id, "news")].Access != "open" {
		t.Fatalf("open at the edge: %+v", s)
	}
	// an unconfirmed email undoes the verification
	on.exec(`UPDATE users SET email_verified = false WHERE id = $1`, on.userID("b@example.test"))
	if r := owner.do("PUT", path, map[string]any{"access": "open"}); r.code != http.StatusForbidden {
		t.Fatalf("unconfirmed email: %d", r.code)
	}
}

func TestViewerInvitesOffWithoutAKey(t *testing.T) {
	e := newEnv(t)
	owner, h, _ := publisherHome(e, "c@example.test", "chome", "news")
	if r := owner.do("POST", "/v1/broadcast/homes/"+h.id+"/invites", map[string]any{}); r.code != http.StatusServiceUnavailable || r.errCode() != "broadcast_disabled" {
		t.Fatalf("no key: %d %s", r.code, r.body)
	}
}
