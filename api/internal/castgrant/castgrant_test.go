package castgrant

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

const (
	userID  = "00000000-0000-4000-8000-0000580700aa"
	videoID = "00000000-0000-4000-8000-0000580700bb"
	queueID = "00000000-0000-4000-8000-0000580700cc"
)

var testKeys *keys.Set

func set(t *testing.T) *keys.Set {
	t.Helper()
	if testKeys == nil {
		k, err := keys.Generate(2048)
		if err != nil {
			t.Fatal(err)
		}
		testKeys = keys.NewSet(0)
		testKeys.Replace(k)
	}
	return testKeys
}

func TestMintAndParse(t *testing.T) {
	ks := set(t)
	ac := 9
	now := time.Now()
	g, exp, err := Mint(ks, userID, &ac, principal.CastScope{VideoID: videoID}, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g, Prefix) {
		t.Fatalf("grant %q has no %s prefix", g, Prefix)
	}
	if d := exp.Sub(now); d > MaxTTL || d < MaxTTL-2*time.Second {
		t.Fatalf("a default grant lives %v, want %v", d, MaxTTL)
	}
	c, err := Parse(ks, g)
	if err != nil {
		t.Fatal(err)
	}
	if c.Usr != userID || c.Cv != videoID || c.Cq != "" || c.Aud != Audience || c.Ac == nil || *c.Ac != 9 {
		t.Fatalf("claims %+v", c)
	}
	// longer than six hours is six hours
	_, exp, _ = Mint(ks, userID, nil, principal.CastScope{QueueID: queueID}, now, 48*time.Hour)
	if exp.Sub(now) > MaxTTL {
		t.Fatalf("a grant asked for 48 h lives %v", exp.Sub(now))
	}
	// a grant is no token of the home's: the API audience refuses it
	if _, err := jwt.Verify(ks, strings.TrimPrefix(g, Prefix), "api"); !errors.Is(err, jwt.ErrAudience) {
		t.Fatalf("the grant verified as an API token: %v", err)
	}
	// and an API token is no grant, prefixed or not
	api, err := jwt.Sign(ks, jwt.Claims{Aud: "api", Usr: userID, Cv: videoID})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{api, Prefix + api} {
		if _, err := Parse(ks, s); err == nil {
			t.Fatalf("an API token parsed as a grant: %q", s)
		}
	}
}

func TestMintRefusesAScopeOfBothOrNeither(t *testing.T) {
	ks := set(t)
	for _, s := range []principal.CastScope{{}, {VideoID: videoID, QueueID: queueID}} {
		if _, _, err := Mint(ks, userID, nil, s, time.Now(), 0); !errors.Is(err, ErrScope) {
			t.Fatalf("scope %+v minted: %v", s, err)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	ks := set(t)
	sign := func(c jwt.Claims) string {
		tok, err := jwt.Sign(ks, c)
		if err != nil {
			t.Fatal(err)
		}
		return Prefix + tok
	}
	now := time.Now()
	for name, g := range map[string]string{
		"no prefix":   strings.TrimPrefix(sign(jwt.Claims{Aud: Audience, Usr: userID, Cv: videoID}), Prefix),
		"no item":     sign(jwt.Claims{Aud: Audience, Usr: userID}),
		"two items":   sign(jwt.Claims{Aud: Audience, Usr: userID, Cv: videoID, Cq: queueID}),
		"no user":     sign(jwt.Claims{Aud: Audience, Cv: videoID}),
		"seven hours": sign(jwt.Claims{Aud: Audience, Usr: userID, Cv: videoID, Iat: now.Unix(), Exp: now.Add(7 * time.Hour).Unix()}),
		"expired":     sign(jwt.Claims{Aud: Audience, Usr: userID, Cv: videoID, Iat: now.Add(-3 * time.Hour).Unix(), Exp: now.Add(-time.Hour).Unix()}),
		"wrong aud":   sign(jwt.Claims{Aud: "api", Usr: userID, Cv: videoID}),
		"another key": func() string {
			k, _ := keys.Generate(2048)
			o := keys.NewSet(0)
			o.Replace(k)
			g, _, _ := Mint(o, userID, nil, principal.CastScope{VideoID: videoID}, now, 0)
			return g
		}(),
		"just a prefix": Prefix,
	} {
		if _, err := Parse(ks, g); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

type fakeUsers map[string]*users.User

func (f fakeUsers) GetByID(_ context.Context, id string) (*users.User, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return nil, users.ErrNotFound
}

type fakeACL struct {
	libs []string
	err  error
}

func (f fakeACL) LibrariesFor(context.Context, string) ([]string, error) { return f.libs, f.err }

// seen runs a request through Bearer and answers the principal it attached.
func seen(t *testing.T, mw func(http.Handler) http.Handler, bearer string, before *principal.Principal) *principal.Principal {
	t.Helper()
	var got *principal.Principal
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = principal.FromContext(r.Context()) }))
	req := httptest.NewRequest(http.MethodGet, "/api/videos/"+videoID, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if before != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), before))
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestBearer(t *testing.T) {
	ks := set(t)
	ac := 13
	g, _, err := Mint(ks, userID, &ac, principal.CastScope{VideoID: videoID}, time.Now(), 0)
	if err != nil {
		t.Fatal(err)
	}
	viewer := fakeUsers{userID: {ID: userID}}
	mw := Bearer(ks, viewer, fakeACL{libs: []string{"lib-a"}})

	p := seen(t, mw, g, nil)
	if p == nil || p.Source != principal.SourceCast || p.UserID != userID || p.IsAdmin || p.AccessAllLibraries ||
		len(p.Libraries) != 1 || p.Cast == nil || p.Cast.VideoID != videoID || p.AgeCeiling == nil || *p.AgeCeiling != 13 {
		t.Fatalf("principal %+v", p)
	}
	// an admin's grant is still no admin
	admin := Bearer(ks, fakeUsers{userID: {ID: userID, IsAdmin: true}}, nil)
	if p := seen(t, admin, g, nil); p == nil || p.IsAdmin || !p.AccessAllLibraries {
		t.Fatalf("admin's grant: %+v", p)
	}
	// no one: the user is gone, the libraries cannot be read, the grant is not one
	for name, c := range map[string]struct {
		mw     func(http.Handler) http.Handler
		bearer string
	}{
		"user gone":      {Bearer(ks, fakeUsers{}, fakeACL{}), g},
		"libraries fail": {Bearer(ks, viewer, fakeACL{err: errors.New("down")}), g},
		"no acl":         {Bearer(ks, viewer, nil), g},
		"garbage":        {mw, Prefix + "x.y.z"},
		"no bearer":      {mw, ""},
	} {
		if p := seen(t, c.mw, c.bearer, nil); p != nil {
			t.Errorf("%s: %+v", name, p)
		}
	}
	// a principal already attached is left alone
	before := &principal.Principal{UserID: "someone", Source: principal.SourceJWT}
	if p := seen(t, mw, g, before); p != before {
		t.Fatalf("replaced %+v", p)
	}
}

// gated runs one request as p through the gate over a router with Routes mounted (plus
// one route outside them) and answers the status.
func gated(t *testing.T, g *Gate, p *principal.Principal, method, path string) int {
	t.Helper()
	r := chi.NewRouter()
	r.Use(g.Middleware(r))
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }
	for k := range Routes {
		m, pat, _ := strings.Cut(k, " ")
		r.MethodFunc(m, pat, ok)
	}
	r.Get("/api/home", ok)
	req := httptest.NewRequest(method, path, nil)
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestGateWithoutADatabase(t *testing.T) {
	g := NewGate()
	cast := &principal.Principal{UserID: userID, Source: principal.SourceCast, Cast: &principal.CastScope{QueueID: queueID}}
	for _, c := range []struct {
		p            *principal.Principal
		method, path string
		want         int
	}{
		// anyone else passes
		{nil, http.MethodGet, "/api/home", http.StatusTeapot},
		{&principal.Principal{UserID: userID, Source: principal.SourceJWT}, http.MethodGet, "/api/home", http.StatusTeapot},
		// a grant: the open route, its queue (no database needed), nothing else
		{cast, http.MethodGet, "/api/discovery/home", http.StatusTeapot},
		{cast, http.MethodGet, "/api/me/play-queues/" + queueID, http.StatusTeapot},
		{cast, http.MethodGet, "/api/me/play-queues/" + strings.ToUpper(queueID), http.StatusTeapot},
		{cast, http.MethodGet, "/api/me/play-queues/" + videoID, http.StatusForbidden},
		{cast, http.MethodGet, "/api/home", http.StatusForbidden},
		{cast, http.MethodGet, "/api/nowhere", http.StatusForbidden},
		// an item needs the database: refused until the gate has it (fail closed)
		{cast, http.MethodGet, "/api/videos/" + videoID, http.StatusForbidden},
		// a grant principal that names nothing
		{&principal.Principal{UserID: userID, Source: principal.SourceCast}, http.MethodGet, "/api/discovery/home", http.StatusForbidden},
	} {
		if got := gated(t, g, c.p, c.method, c.path); got != c.want {
			t.Errorf("%v %s %s = %d, want %d", c.p, c.method, c.path, got, c.want)
		}
	}
}

func TestMayPlayAVideoGrant(t *testing.T) {
	p := &principal.Principal{UserID: userID, Source: principal.SourceCast, Cast: &principal.CastScope{VideoID: videoID}}
	for v, want := range map[string]bool{videoID: true, strings.ToUpper(videoID): true, queueID: false, "": false} {
		if got, err := MayPlay(context.Background(), nil, p, v); err != nil || got != want {
			t.Errorf("MayPlay(%q) = %v, %v", v, got, err)
		}
	}
	if got, _ := MayPlay(context.Background(), nil, &principal.Principal{UserID: userID}, videoID); got {
		t.Fatal("a principal with no grant may play")
	}
}
