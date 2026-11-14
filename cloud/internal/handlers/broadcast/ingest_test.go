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

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"

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
	if id == "gated-home" {
		return Allowance{}, ErrGateShut
	}
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
		Agents:    fakeAgents{"pro-home": "s3cret", "free-home": "s3cret", "gated-home": "s3cret"},
		Plans:     fakePlans{"pro-home": {MaxRungs: 2, MaxTopKbps: 3000}},
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
	// the publisher is the home: the id the edge's usage windows name and the cloud bills
	if c.Pub != "pro-home" || c.Home != "pro-home" || c.Ch != bc.ChannelID("pro-home", "7") || c.Kbps != 3000 || len(c.Rungs) != 2 {
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

// Role rule: while broadcasting is off, a home that is not a test publisher gets 403.
func TestIngestTokenBehindTheGate(t *testing.T) {
	r, _ := setup(t, true)
	if w := post(r, "gated-home", "s3cret", twoRungs); w.Code != 403 || errOf(w) != "broadcast_not_enabled" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The allowance is 50.20's entitlement: the tier's rungs and top bitrate from the catalog,
// none without a plan or an active owner, none while the gate is shut for the home.
func TestAllowanceOf(t *testing.T) {
	guest := billing.Publisher{Plan: billing.PlanPro, OwnerActive: true}
	caster := billing.Publisher{Plan: billing.PlanPro, BroadcastPlan: billing.BroadcastPlanBroadcaster, OwnerActive: true}
	for name, c := range map[string]struct {
		p     billing.Publisher
		gated bool
		want  Allowance
		err   error
	}{
		"guest sharing, gate open":     {guest, true, Allowance{MaxRungs: billing.Tiers[billing.PlanPro].Broadcast.MaxRungs, MaxTopKbps: billing.Tiers[billing.PlanPro].Broadcast.MaxTopKbps}, nil},
		"Broadcaster, gate open":       {caster, true, Allowance{MaxRungs: billing.Broadcaster.MaxRungs, MaxTopKbps: billing.Broadcaster.MaxTopKbps}, nil},
		"Broadcaster, gate shut":       {caster, false, Allowance{}, ErrGateShut},
		"guest sharing, gate shut":     {guest, false, Allowance{}, ErrGateShut},
		"no plan, a test publisher":    {billing.Publisher{Plan: billing.PlanFree, OwnerActive: true, TestPublisher: true}, true, Allowance{}, ErrNotInPlan},
		"Broadcaster, owner suspended": {billing.Publisher{Plan: billing.PlanPro, BroadcastPlan: billing.BroadcastPlanBroadcaster}, true, Allowance{}, ErrNotInPlan},
	} {
		got, err := AllowanceOf(c.p, c.gated)
		if !errors.Is(err, c.err) || got != c.want {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if billing.Broadcaster.MaxRungs <= billing.Tiers[billing.PlanPro].Broadcast.MaxRungs {
		t.Fatal("the catalog's Broadcaster tier should allow more rungs than guest sharing")
	}
}
