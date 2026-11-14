package broadcast

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

type fakeAgents map[string]string

func (f fakeAgents) VerifySecret(_ context.Context, id, secret string) error {
	if s, ok := f[id]; ok && s == secret {
		return nil
	}
	return errors.New("bad secret")
}

type fakePlans map[string]Allowance

func (f fakePlans) ForServer(_ context.Context, id string) (Allowance, error) {
	a, ok := f[id]
	if !ok {
		return Allowance{}, ErrNotInPlan
	}
	return a, nil
}

var at = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T, withKey bool) (*chi.Mux, bc.Key) {
	t.Helper()
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: handler"))
	k, _ := bc.KeyFromSeed(seed[:])
	d := &IngestDeps{
		Agents:    fakeAgents{"pro-home": "s3cret", "free-home": "s3cret"},
		Plans:     fakePlans{"pro-home": {Publisher: "user-pro", MaxRungs: 2, MaxTopKbps: 3000}},
		IngestURL: "https://ingest.maktaba.tv",
		Now:       func() time.Time { return at },
	}
	if withKey {
		d.Key = &k
	}
	r := chi.NewRouter()
	MountIngest(r, d)
	return r, k
}

func post(r http.Handler, user, pass, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/broadcast/ingest-token", strings.NewReader(body))
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errOf(w *httptest.ResponseRecorder) string {
	var b map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	return b["error"]
}

const twoRungs = `{"channel":"7","rungs":["src","480p"]}`

// Role rule: only a home's own agent credential gets a token; a user, an anonymous caller or a
// wrong secret gets 401.
func TestIngestTokenNeedsTheHomesCredential(t *testing.T) {
	r, _ := setup(t, true)
	for name, c := range map[string][2]string{
		"anonymous":    {"", ""},
		"wrong secret": {"pro-home", "nope"},
		"unknown home": {"ghost", "s3cret"},
	} {
		if w := post(r, c[0], c[1], twoRungs); w.Code != 401 || errOf(w) != "unauthorized" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	// a user's bearer token is not a home credential
	req := httptest.NewRequest("POST", "/v1/broadcast/ingest-token", strings.NewReader(twoRungs))
	req.Header.Set("Authorization", "Bearer user-session-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("a user token: %d", w.Code)
	}
}

// Role rule: a home whose owner's plan has no broadcasting gets 403.
func TestIngestTokenNeedsThePlan(t *testing.T) {
	r, _ := setup(t, true)
	if w := post(r, "free-home", "s3cret", twoRungs); w.Code != 403 || errOf(w) != "broadcast_not_in_plan" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Role rule: more rungs than the plan allows is 403.
func TestIngestTokenRungsWithinThePlan(t *testing.T) {
	r, _ := setup(t, true)
	w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src","720p","480p"]}`)
	if w.Code != 403 || errOf(w) != "plan_rungs" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["SRC","x"]}`); w.Code != 400 {
		t.Fatalf("bad rung names: %d", w.Code)
	}
	if w := post(r, "pro-home", "s3cret", `{"channel":"../7","rungs":["src"]}`); w.Code != 400 {
		t.Fatalf("bad channel: %d", w.Code)
	}
}

func TestIngestTokenIssued(t *testing.T) {
	r, k := setup(t, true)
	w := post(r, "pro-home", "s3cret", twoRungs)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a token must not be cached")
	}
	var resp tokenResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	c, err := bc.VerifyIngest(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, resp.Token, at)
	if err != nil {
		t.Fatal(err)
	}
	if c.Pub != "user-pro" || c.Home != "pro-home" || c.Ch != bc.ChannelID("pro-home", "7") || c.Kbps != 3000 || len(c.Rungs) != 2 {
		t.Fatalf("claims %+v", c)
	}
	if resp.Channel != c.Ch || !resp.ExpiresAt.Equal(at.Add(24*time.Hour)) || resp.RefreshAfter != 3600 {
		t.Fatalf("resp %+v", resp)
	}
}

func TestIngestTokenOffWithoutAKey(t *testing.T) {
	r, _ := setup(t, false)
	if w := post(r, "pro-home", "s3cret", twoRungs); w.Code != 503 || errOf(w) != "broadcast_disabled" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAllowanceFor(t *testing.T) {
	if a, err := AllowanceFor("u1", "pro", "active"); err != nil || a.Publisher != "u1" || a.MaxRungs != 2 {
		t.Fatalf("pro: %+v %v", a, err)
	}
	for _, c := range [][2]string{{"free", "active"}, {"basic", "active"}, {"pro", "suspended"}} {
		if _, err := AllowanceFor("u1", c[0], c[1]); !errors.Is(err, ErrNotInPlan) {
			t.Errorf("%v: %v", c, err)
		}
	}
}

// Story 50.23 §5: the token carries the channel's rule with Maktaba's deny list merged in.
func TestIngestTokenCarriesGeo(t *testing.T) {
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: handler"))
	k, _ := bc.KeyFromSeed(seed[:])
	var denyErr error
	d := &IngestDeps{
		Agents:    fakeAgents{"pro-home": "s3cret"},
		Plans:     fakePlans{"pro-home": {Publisher: "user-pro", MaxRungs: 2, MaxTopKbps: 3000}},
		IngestURL: "https://ingest.maktaba.tv",
		Now:       func() time.Time { return at },
		Key:       &k,
		GeoDeny:   func(context.Context) ([]string, error) { return []string{"KP"}, denyErr },
	}
	r := chi.NewRouter()
	MountIngest(r, d)

	w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src"],"geo":{"mode":"allow","countries":["SA","EG"]}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var resp tokenResp
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	c, err := bc.VerifyIngest(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, resp.Token, at)
	if err != nil {
		t.Fatal(err)
	}
	if c.Geo == nil || strings.Join(c.Geo.Allow, ",") != "EG,SA" || strings.Join(c.Geo.Deny, ",") != "KP" {
		t.Fatalf("geo %+v", c.Geo)
	}

	// no channel rule: Maktaba's list still goes in
	w = post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src"]}`)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	c, _ = bc.VerifyIngest(map[string]ed25519.PublicKey{k.Kid: k.Priv.Public().(ed25519.PublicKey)}, resp.Token, at)
	if c.Geo == nil || strings.Join(c.Geo.Deny, ",") != "KP" {
		t.Fatalf("maktaba list dropped: %+v", c.Geo)
	}

	if w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src"],"geo":{"mode":"everywhere"}}`); w.Code != 400 || errOf(w) != "invalid_geo" {
		t.Fatalf("bad mode: %d %s", w.Code, w.Body)
	}

	// never a token without Maktaba's list
	denyErr = errors.New("db down")
	if w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src"]}`); w.Code != 500 || errOf(w) != "geo_lookup_failed" {
		t.Fatalf("lookup failure: %d %s", w.Code, w.Body)
	}
}

// Story 50.24: a token asked with the channel's cloud id records the channel's edge id (a
// takedown's programme slate names it); an older home's body without it records nothing.
func TestIngestTokenRecordsTheEdgeChannel(t *testing.T) {
	seed := sha256.Sum256([]byte("maktaba-broadcast TEST KEY, never for production: handler"))
	k, _ := bc.KeyFromSeed(seed[:])
	var got []string
	d := &IngestDeps{
		Agents: fakeAgents{"pro-home": "s3cret"}, Key: &k,
		Plans: fakePlans{"pro-home": {Publisher: "user-pro", MaxRungs: 2, MaxTopKbps: 3000}},
		Now:   func() time.Time { return at },
		EdgeChannels: func(_ context.Context, serverID, cloudID, edge string) error {
			got = append(got, serverID+" "+cloudID+" "+edge)
			if edge != bc.ChannelID("pro-home", "7") {
				return ErrEdgeMismatch // bound to channel 7 already
			}
			return nil
		},
	}
	r := chi.NewRouter()
	MountIngest(r, d)
	const cloudID = "0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60"
	if w := post(r, "pro-home", "s3cret", `{"channel":"7","rungs":["src"],"cloud_id":"`+cloudID+`"}`); w.Code != http.StatusOK {
		t.Fatalf("token = %d %s", w.Code, w.Body)
	}
	if want := "pro-home " + cloudID + " " + bc.ChannelID("pro-home", "7"); len(got) != 1 || got[0] != want {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	for _, body := range []string{`{"channel":"7","rungs":["src"]}`, `{"channel":"7","rungs":["src"],"cloud_id":"'; DROP"}`} {
		if w := post(r, "pro-home", "s3cret", body); w.Code != http.StatusOK {
			t.Fatalf("token = %d", w.Code)
		}
	}
	if len(got) != 1 {
		t.Fatalf("a body without a cloud id recorded %q", got)
	}
	// another local channel under the bound cloud id: refused, no token
	if w := post(r, "pro-home", "s3cret", `{"channel":"8","rungs":["src"],"cloud_id":"`+cloudID+`"}`); w.Code != http.StatusConflict ||
		errOf(w) != "edge_channel_mismatch" {
		t.Fatalf("a rebind = %d %s", w.Code, w.Body)
	}
}
