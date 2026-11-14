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
	d := &Deps{
		Agents:    fakeAgents{"pro-home": "s3cret", "free-home": "s3cret"},
		Plans:     fakePlans{"pro-home": {Publisher: "user-pro", MaxRungs: 2, MaxTopKbps: 3000}},
		IngestURL: "https://ingest.maktaba.tv",
		Now:       func() time.Time { return at },
	}
	if withKey {
		d.Key = &k
	}
	r := chi.NewRouter()
	MountAgent(r, d)
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
