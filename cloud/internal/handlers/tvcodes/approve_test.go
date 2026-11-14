package tvcodes

// Story 58.03: approving a TV's code on maktaba.tv, signed in to the cloud.

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// fakeHomes: the account "linked-user" is linked to homeID (online) and to "home-off" (offline);
// it owns "home-owned" without a link. Every other account reaches nothing.
type fakeHomes struct{}

func (fakeHomes) HomesFor(_ context.Context, uid string) ([]stores.LinkedHome, error) {
	if uid != "linked-user" {
		return nil, nil
	}
	return []stores.LinkedHome{
		{Server: stores.Server{ID: homeID, Name: "The hilltop home", Slug: "hilltop"}, Linked: true, Online: true},
		{Server: stores.Server{ID: "home-off", Name: "Cabin", Slug: "cabin"}, Linked: true, Online: false},
		{Server: stores.Server{ID: "home-owned", Name: "Owned", Slug: "owned"}, Owned: true, Online: true},
	}, nil
}

// fakeHome plays the home behind the relay: it checks the cloud's assertion (signature, purpose,
// account, home, code), then claims the code through the agent route, as a real home's
// cloud-assertion route does. answer, when set, is all it says instead.
type fakeHome struct {
	t      *testing.T
	rg     *rig
	pub    ed25519.PublicKey
	answer *HomeAnswer
	err    error
	calls  []string
	grant  string
}

func (f *fakeHome) ApproveOnHome(_ context.Context, slug, code, assertion string) (HomeAnswer, error) {
	f.calls = append(f.calls, slug+" "+code)
	if f.err != nil {
		return HomeAnswer{}, f.err
	}
	if f.answer != nil {
		return *f.answer, nil
	}
	rest, ok := strings.CutPrefix(assertion, homeassert.Prefix)
	b, sigB, ok2 := strings.Cut(rest, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(sigB)
	if !ok || !ok2 || !ed25519.Verify(f.pub, []byte(homeassert.SigningPrefix+"."+b), sig) {
		return HomeAnswer{Status: http.StatusForbidden}, nil
	}
	raw, _ := base64.RawURLEncoding.DecodeString(b)
	var p homeassert.Payload
	_ = json.Unmarshal(raw, &p)
	if p.Pur != homeassert.PurposeTVCode || p.Sub != "linked-user" || p.Aud != homeID || p.Non != code {
		f.t.Errorf("assertion %+v for code %s", p, code)
		return HomeAnswer{Status: http.StatusForbidden}, nil
	}
	f.grant = "grant-for-" + code + "-0123456789"
	w := f.rg.do("POST", "/v1/homes/"+homeID+"/tv-codes/"+code+"/claim", "", claimReq{Grant: f.grant}, true)
	if w.Code != http.StatusOK {
		return HomeAnswer{Status: w.Code, Body: w.Body.Bytes()}, nil
	}
	return HomeAnswer{Status: http.StatusOK, Body: []byte(`{"status":"approved","platform":"tizen","country":"JO"}`)}, nil
}

func newApproveRig(t *testing.T) (*rig, *fakeHome) {
	t.Helper()
	rg := newRig(t)
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, "a seed for the assertion signer.")
	signer := homeassert.DeriveFromSeed(seed)
	home := &fakeHome{t: t, rg: rg, pub: signer.PublicKey()}
	rg.deps.Homes, rg.deps.Signer, rg.deps.Caller = fakeHomes{}, signer, home
	r := chi.NewRouter()
	MountPublic(r, rg.deps)
	MountAgent(r, rg.deps)
	r.Group(func(g chi.Router) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if uid := req.Header.Get("X-Test-User"); uid != "" {
					req = req.WithContext(middleware.WithUserID(req.Context(), uid))
				}
				next.ServeHTTP(w, req)
			})
		})
		MountUser(g, rg.deps)
	})
	rg.h = r
	return rg, home
}

func (rg *rig) as(user, method, path string, body any) *httptest.ResponseRecorder {
	rg.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	}
	req.RemoteAddr = "198.51.100.4:40000"
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	rg.h.ServeHTTP(w, req)
	return w
}

// A person signed in to the cloud reads the code, approves it on their linked home, and the TV's
// next poll collects that home's grant.
func TestApproveOnMaktabaTV(t *testing.T) {
	rg, home := newApproveRig(t)
	c := rg.create("")
	code := Normalize(c.Code)

	w := rg.as("linked-user", "GET", "/v1/tv/codes/"+strings.ToLower(c.Code), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"platform":"tizen"`) {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	w = rg.as("linked-user", "POST", "/v1/tv/codes/"+c.Code+"/approve", webApproveReq{ServerID: homeID})
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	got := decode[webApproveResp](t, w)
	if got.Status != "approved" || got.HomeName != "The hilltop home" || got.Platform != "tizen" {
		t.Fatalf("approve = %+v", got)
	}
	if len(home.calls) != 1 || home.calls[0] != "hilltop "+code {
		t.Fatalf("home calls = %v", home.calls)
	}
	p := rg.poll(c)
	if p.Code != http.StatusOK || !strings.Contains(p.Body.String(), home.grant) {
		t.Fatalf("poll: %d %s", p.Code, p.Body.String())
	}
	// approved already: no longer open to anyone
	if w := rg.as("linked-user", "GET", "/v1/tv/codes/"+c.Code, nil); w.Code != http.StatusNotFound {
		t.Fatalf("lookup after: %d", w.Code)
	}
	if w := rg.as("linked-user", "POST", "/v1/tv/codes/"+c.Code+"/approve", webApproveReq{ServerID: homeID}); w.Code != http.StatusNotFound {
		t.Fatalf("approve again: %d", w.Code)
	}
}

// Role test: no account is 401; an account approves only on a home that linked it, and not one it
// merely owns or does not reach; an offline home or one the relay cannot reach approves nothing.
func TestApproveOnMaktabaTVRefusals(t *testing.T) {
	for _, c := range []struct {
		name, user, server string
		home               *fakeHome
		want               int
		wantErr            string
	}{
		{"no account", "", homeID, nil, http.StatusUnauthorized, "unauthorized"},
		{"an account the home never linked", "stranger", homeID, nil, http.StatusForbidden, "not_linked"},
		{"a home it owns but never linked", "linked-user", "home-owned", nil, http.StatusForbidden, "not_linked"},
		{"a home it does not reach", "linked-user", "home-else", nil, http.StatusForbidden, "not_linked"},
		{"an offline home", "linked-user", "home-off", nil, http.StatusConflict, "home_offline"},
		{"the relay cannot reach it", "linked-user", homeID, &fakeHome{err: errors.New("dial: refused")}, http.StatusServiceUnavailable, "home_unreachable"},
		{"the relay's 502", "linked-user", homeID, &fakeHome{answer: &HomeAnswer{Status: http.StatusBadGateway}}, http.StatusServiceUnavailable, "home_unreachable"},
		{"the home refuses the assertion", "linked-user", homeID, &fakeHome{answer: &HomeAnswer{Status: http.StatusForbidden}}, http.StatusForbidden, "not_linked"},
		{"approved first on the home", "linked-user", homeID, &fakeHome{answer: &HomeAnswer{Status: http.StatusConflict, Body: []byte(`{"title":"code already approved"}`)}}, http.StatusConflict, "already_approved"},
		{"the home cannot reach the cloud", "linked-user", homeID, &fakeHome{answer: &HomeAnswer{Status: http.StatusConflict, Body: []byte(`{"code":"cloud_not_linked"}`)}}, http.StatusServiceUnavailable, "home_unreachable"},
	} {
		rg, home := newApproveRig(t)
		if c.home != nil {
			c.home.t, c.home.rg = t, rg
			rg.deps.Caller = c.home
			home = c.home
		}
		cr := rg.create("")
		w := rg.as(c.user, "POST", "/v1/tv/codes/"+cr.Code+"/approve", webApproveReq{ServerID: c.server})
		if w.Code != c.want || !strings.Contains(w.Body.String(), `"error":"`+c.wantErr+`"`) {
			t.Fatalf("%s: %d %s", c.name, w.Code, w.Body.String())
		}
		if c.home == nil && len(home.calls) != 0 {
			t.Fatalf("%s: the home was asked", c.name)
		}
		// nothing approved: the TV is still waiting
		if p := rg.poll(cr); p.Code != http.StatusAccepted {
			t.Fatalf("%s: poll %d %s", c.name, p.Code, p.Body.String())
		}
	}
}

func TestApproveOnMaktabaTVBadInput(t *testing.T) {
	rg, home := newApproveRig(t)
	cr := rg.create("")
	if w := rg.as("linked-user", "GET", "/v1/tv/codes/NOPE", nil); w.Code != http.StatusNotFound {
		t.Fatalf("bad code lookup: %d", w.Code)
	}
	if w := rg.as("", "GET", "/v1/tv/codes/"+cr.Code, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("lookup with no account: %d", w.Code)
	}
	if w := rg.as("linked-user", "POST", "/v1/tv/codes/"+cr.Code+"/approve", map[string]string{}); w.Code != http.StatusBadRequest {
		t.Fatalf("no server_id: %d", w.Code)
	}
	if w := rg.as("linked-user", "POST", "/v1/tv/codes/ABCD2345/approve", webApproveReq{ServerID: homeID}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", w.Code)
	}
	if len(home.calls) != 0 {
		t.Fatalf("home calls = %v", home.calls)
	}
	// nothing wired: no approving from the web
	rg.deps.Signer = nil
	if w := rg.as("linked-user", "POST", "/v1/tv/codes/"+cr.Code+"/approve", webApproveReq{ServerID: homeID}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no signer: %d", w.Code)
	}
}

// Each account's lookups and approvals are capped, so trying codes from an account runs out.
func TestAnAccountsLookupsAreCapped(t *testing.T) {
	rg, _ := newApproveRig(t)
	var last int
	for i := 0; i < 25; i++ {
		last = rg.as("linked-user", "GET", "/v1/tv/codes/ABCD2345", nil).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("25th lookup = %d", last)
	}
	// another account has its own allowance
	if w := rg.as("stranger", "GET", "/v1/tv/codes/ABCD2345", nil); w.Code != http.StatusNotFound {
		t.Fatalf("another account = %d", w.Code)
	}
}

// RelayCaller posts the code and the assertion to the home's cloud-assertion route.
func TestRelayCallerReachesTheHome(t *testing.T) {
	var got struct{ Code, Assertion string }
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"title":"refused"}`))
	}))
	defer srv.Close()
	c := RelayCaller{Base: func(slug string) string {
		if slug != "hilltop" {
			t.Errorf("slug %q", slug)
		}
		return srv.URL
	}}
	ans, err := c.ApproveOnHome(context.Background(), "hilltop", "ABCD2345", "mkha1.a.b")
	if err != nil || ans.Status != http.StatusForbidden || path != "/api/pairing/cloud-assertion" ||
		got.Code != "ABCD2345" || got.Assertion != "mkha1.a.b" {
		t.Fatalf("answer %+v %v, path %q, body %+v", ans, err, path, got)
	}
	if _, err := (RelayCaller{}).ApproveOnHome(context.Background(), "hilltop", "ABCD2345", "x"); err == nil {
		t.Fatal("no relay host: no error")
	}
}
