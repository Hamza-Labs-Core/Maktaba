package router_test

// Epic 57 story 57.04 end to end, behind the routes as main.go mounts them
// (MountP9 and MountParental) and the real bearer-token chain, on a real migrated
// Postgres: a sign-in posts its device's signal, refreshes, and the new access
// token carries the limit; a looser statement waits for the guardian.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	authh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/auth"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
)

type signalHome struct {
	t    *testing.T
	keys *keys.Set
	h    http.Handler
}

type signIn struct {
	home            *signalHome
	access, refresh string
}

func (s *signalHome) call(method, path, bearer string, body any) (int, map[string]any) {
	s.t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authh.HeaderClient, "native")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (s *signalHome) signIn(name string) *signIn {
	s.t.Helper()
	code, out := s.call("POST", "/api/auth/login", "", map[string]string{"username": name, "password": "correct horse battery"})
	if code != 200 {
		s.t.Fatalf("sign-in %s: %d %v", name, code, out)
	}
	return &signIn{home: s, access: out["access_token"].(string), refresh: out["refresh_token"].(string)}
}

func (in *signIn) claims() *jwt.Claims {
	in.home.t.Helper()
	c, err := jwt.Verify(in.home.keys, in.access, "api")
	if err != nil {
		in.home.t.Fatal(err)
	}
	return c
}

// renew refreshes the access token, as the Player does after it posts a signal.
func (in *signIn) renew() {
	in.home.t.Helper()
	code, out := in.home.call("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": in.refresh})
	if code != 200 {
		in.home.t.Fatalf("refresh: %d %v", code, out)
	}
	in.access, in.refresh = out["access_token"].(string), out["refresh_token"].(string)
}

func (in *signIn) do(want int, method, path string, body any) map[string]any {
	in.home.t.Helper()
	code, out := in.home.call(method, path, in.access, body)
	if code != want {
		in.home.t.Fatalf("%s %s = %d, want %d (%v)", method, path, code, want, out)
	}
	return out
}

func TestDeviceSignalRidesTheAccessToken(t *testing.T) {
	db, _ := clustertest.ScratchDB(t)
	set := keys.NewSet(time.Hour)
	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	set.Replace(k)
	r := chi.NewRouter()
	if router.MountP9(r, router.P9Deps{DB: db, Keys: set}) == nil {
		t.Fatal("MountP9 mounted nothing")
	}
	router.MountParental(context.Background(), r, db, runner.New(runner.Options{}), "", nil)
	home := &signalHome{t: t, keys: set, h: authmw.JWTBearer(set, "api")(r)}

	ctx := context.Background()
	us := users.New(db)
	mk := func(name string, admin bool) string {
		u, err := us.Create(ctx, users.CreateInput{Username: name, Password: "correct horse battery", IsAdmin: admin})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	mk("s-admin", true)
	guardianID, childID := mk("s-guardian", false), mk("s-child", false)
	mk("s-viewer", false)
	if _, err := db.Exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 16, 1)`, childID); err != nil {
		t.Fatal(err)
	}
	// the home has set parental controls up (slot 0225 starts them off)
	if _, err := db.Exec(`UPDATE app_settings SET value = 'true' WHERE key = 'parental.enabled'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1, $2)`, childID, guardianID); err != nil {
		t.Fatal(err)
	}

	// T04: the child's iPhone says 13 to 15, films up to R, TV up to TV-PG
	child := home.signIn("s-child")
	first := child.claims()
	if first.Fam == "" || first.Ac != nil {
		t.Fatalf("a new sign-in: fam %q, ac %v", first.Fam, first.Ac)
	}
	out := child.do(200, "POST", "/api/me/age-signal", map[string]any{
		"age_range":     map[string]any{"lower": 13, "upper": 15, "declaration": "guardian_declared"},
		"movie_ceiling": 400, "tv_ceiling": 400, "region": "US"})
	if out["tier_limit"] != float64(9) || out["refresh"] != true {
		t.Fatalf("age-signal = %v", out)
	}
	// the limit holds on the token already out (the home reads the stored signal)...
	if me := child.do(200, "GET", "/api/me/parental", nil); me["max_tier"] != float64(9) || me["device_limit"] != float64(9) {
		t.Fatalf("me before the refresh = %v", me)
	}
	// ...and the refresh puts it in the next one, for the same sign-in
	child.renew()
	if c := child.claims(); c.Fam != first.Fam || c.Ac == nil || *c.Ac != 9 {
		t.Fatalf("after the refresh: fam %q (was %q), ac %v; want 9", c.Fam, first.Fam, c.Ac)
	}

	// T06: Screen Time is switched off on the phone: the looser statement waits
	out = child.do(200, "POST", "/api/me/age-signal", map[string]any{"movie_ceiling": 1000, "tv_ceiling": 1000, "region": "US"})
	if out["tier_limit"] != float64(9) || out["pending_looser"] != true {
		t.Fatalf("looser = %v", out)
	}
	child.renew()
	if c := child.claims(); c.Ac == nil || *c.Ac != 9 {
		t.Fatalf("after a looser statement: ac %v, want 9 still", c.Ac)
	}
	clearPath := "/api/users/" + childID + "/parental/signals/" + first.Fam + "/clear"
	if out := child.do(403, "POST", clearPath, nil); out["code"] != "restricted" {
		t.Fatalf("clear by the child = %v", out)
	}
	home.signIn("s-viewer").do(403, "POST", clearPath, nil)
	if code, _ := home.call("POST", clearPath, "", nil); code != 401 {
		t.Fatalf("clear by no one = %d, want 401", code)
	}
	guardian := home.signIn("s-guardian")
	if out := guardian.do(200, "POST", clearPath, nil); out["tier_limit"] != nil || out["removed"] != false {
		t.Fatalf("clear by the guardian = %v", out)
	}
	// the token already out still carries 9 until it is renewed; then the account's
	// own 16+ is the limit
	if me := child.do(200, "GET", "/api/me/parental", nil); me["max_tier"] != float64(9) {
		t.Fatalf("me on the old token after the clear = %v", me)
	}
	child.renew()
	if c := child.claims(); c.Ac != nil {
		t.Fatalf("after the clear and a refresh: ac %v, want none", *c.Ac)
	}
	if me := child.do(200, "GET", "/api/me/parental", nil); me["max_tier"] != float64(16) || me["device_limit"] != nil {
		t.Fatalf("me after the clear = %v", me)
	}

	// T07: signing in afresh starts a new sign-in with no signal
	again := home.signIn("s-child")
	if c := again.claims(); c.Fam == first.Fam || c.Ac != nil {
		t.Fatalf("a fresh sign-in: fam %q (the old one %q), ac %v", c.Fam, first.Fam, c.Ac)
	}

	// T05 and the unrestricted account: a 9 to 12 device limits that sign-in to 9+,
	// and the account's other sign-in not at all
	viewer, viewerTV := home.signIn("s-viewer"), home.signIn("s-viewer")
	viewer.do(200, "POST", "/api/me/age-signal", map[string]any{"age_range": map[string]any{"lower": 9, "upper": 12}, "region": "SA"})
	viewer.renew()
	if c := viewer.claims(); c.Ac == nil || *c.Ac != 9 {
		t.Fatalf("the open account's limited sign-in: ac %v, want 9", c.Ac)
	}
	if me := viewer.do(200, "GET", "/api/me/parental", nil); me["restricted"] != false || me["max_tier"] != float64(9) {
		t.Fatalf("me on the limited sign-in = %v", me)
	}
	viewerTV.renew()
	if c := viewerTV.claims(); c.Ac != nil {
		t.Fatalf("the open account's other sign-in: ac %v, want none", *c.Ac)
	}
	if me := viewerTV.do(200, "GET", "/api/me/parental", nil); me["max_tier"] != nil {
		t.Fatalf("me on the other sign-in = %v", me)
	}
	// an adult device says nothing limiting: no claim
	adult := home.signIn("s-guardian")
	if out := adult.do(200, "POST", "/api/me/age-signal", map[string]any{"age_range": map[string]any{"lower": 18}, "movie_ceiling": 1000, "tv_ceiling": 1000}); out["tier_limit"] != nil {
		t.Fatalf("adult signal = %v", out)
	}
	adult.renew()
	if c := adult.claims(); c.Ac != nil {
		t.Fatalf("adult sign-in: ac %v, want none", *c.Ac)
	}
}
