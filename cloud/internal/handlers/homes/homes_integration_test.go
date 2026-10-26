//go:build integration

package homes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	serversh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/servers"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// world is the api role's servers + homes routes, mounted as role_api.go
// mounts them (agent routes outside the user group), on Postgres.
type world struct {
	t      *testing.T
	srv    *httptest.Server
	tokens *token.Signer
	signer *homeassert.Signer
	srvs   *stores.Servers
	users  *stores.Users
}

func newWorld(t *testing.T) *world {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := db.Open(url, 4, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	mig := db.NewMigrator(pool, migrations.FS, ".")
	for attempt := 1; ; attempt++ {
		if err = mig.Up(ctx); err == nil {
			break
		}
		if attempt == 5 {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
	}
	w := &world{t: t, tokens: token.NewSigner([]byte(strings.Repeat("s", 32))),
		signer: homeassert.DeriveFromSeed([]byte(t.Name())),
		srvs:   stores.NewServers(pool.DB), users: stores.NewUsers(pool.DB)}
	sd := serversh.Deps{Servers: w.srvs, Assertions: w.signer}
	hd := &Deps{Servers: w.srvs, Users: w.users, Signer: w.signer, RelayHost: "relay.test"}
	r := chi.NewRouter()
	serversh.MountAgent(r, sd)
	MountPublic(r, hd)
	r.Group(func(g chi.Router) {
		g.Use(authmw.RequireUser(w.tokens))
		serversh.MountUser(g, sd)
		MountUser(g, hd)
	})
	w.srv = httptest.NewServer(r)
	t.Cleanup(w.srv.Close)
	return w
}

// user makes a cloud account and its bearer token.
func (w *world) user() (id, bearer string) {
	w.t.Helper()
	u, err := w.users.Create(context.Background(), uuid.NewString()+"@example.test", "", "T", true)
	if err != nil {
		w.t.Fatal(err)
	}
	tok, err := w.tokens.Issue(u.ID, u.Email, "free", time.Now())
	if err != nil {
		w.t.Fatal(err)
	}
	return u.ID, "Bearer " + tok
}

type reply struct {
	code int
	body map[string]any
}

func (w *world) do(method, path string, body any, auth func(*http.Request)) reply {
	w.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, w.srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if auth != nil {
		auth(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := reply{code: resp.StatusCode}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

func bearer(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", tok) }
}

func basic(id, secret string) func(*http.Request) {
	return func(r *http.Request) { r.SetBasicAuth(id, secret) }
}

// claim mints a code as the owner and redeems it as the agent (no user
// token): the server id and secret.
func (w *world) claim(owner, slug string) (id, secret string) {
	w.t.Helper()
	code := w.do("POST", "/v1/servers/claims", nil, bearer(owner)).body["code"].(string)
	got := w.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "Home", "slug": slug}, nil)
	if got.code != 201 {
		w.w(got)
	}
	return got.body["server_id"].(string), got.body["server_secret"].(string)
}

func (w *world) w(r reply) { w.t.Helper(); w.t.Fatalf("unexpected reply %d %v", r.code, r.body) }

func slug() string { return "h" + strings.ReplaceAll(uuid.NewString()[:12], "-", "") }

// The agent claims without a user token, gets the assertion key it pins,
// and a taken slug leaves the code usable; a code serves once.
func TestClaimRedeem(t *testing.T) {
	w := newWorld(t)
	_, owner := w.user()
	code := w.do("POST", "/v1/servers/claims", nil, bearer(owner)).body["code"].(string)
	s := slug()
	taken := slug()
	_, _ = w.claim(owner, taken)
	if got := w.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "Home", "slug": taken}, nil); got.code != 409 {
		w.w(got)
	}
	if got := w.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "Home", "slug": "admin"}, nil); got.code != 409 {
		t.Fatalf("a reserved slug: %d", got.code)
	}
	got := w.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "Home", "slug": s}, nil)
	if got.code != 201 || got.body["assertion_public_key"] != w.signer.PublicKeyB64() {
		w.w(got)
	}
	if again := w.do("POST", "/v1/servers/claims/redeem", map[string]string{"code": code, "name": "Home", "slug": slug()}, nil); again.code != 404 {
		t.Fatalf("a code redeemed twice: %d", again.code)
	}
	if got := w.do("GET", "/v1/homes/assertion-key", nil, nil); got.body["public_key"] != w.signer.PublicKeyB64() {
		w.w(got)
	}
}

// The agent's routes need its own server id and secret; another server's
// secret, or none, is 401.
func TestAgentAuth(t *testing.T) {
	w := newWorld(t)
	_, owner := w.user()
	id, secret := w.claim(owner, slug())
	otherID, otherSecret := w.claim(owner, slug())
	hb := map[string]any{"online": true}
	for name, auth := range map[string]func(*http.Request){
		"none":         nil,
		"wrong secret": basic(id, "nope"),
		"another home": basic(otherID, otherSecret),
		"a user":       bearer(owner),
	} {
		if got := w.do("POST", "/v1/servers/"+id+"/heartbeat", hb, auth); got.code != 401 {
			t.Errorf("heartbeat with %s: %d", name, got.code)
		}
		if got := w.do("PUT", "/v1/servers/"+id+"/links", map[string]any{"cloud_user_ids": []string{}}, auth); got.code != 401 {
			t.Errorf("links with %s: %d", name, got.code)
		}
	}
	if got := w.do("POST", "/v1/servers/"+id+"/heartbeat", hb, basic(id, secret)); got.code != 204 {
		w.w(got)
	}
}

// A sign-in assertion only for a home that linked the account; a link
// assertion for any home (the home binds it with its nonce); the home
// list shows linked and owned homes.
func TestAssertions(t *testing.T) {
	w := newWorld(t)
	_, owner := w.user()
	friendID, friend := w.user()
	_, stranger := w.user()
	sl := slug()
	id, secret := w.claim(owner, sl)

	signin := map[string]string{"purpose": "signin"}
	if got := w.do("POST", "/v1/homes/"+id+"/assertion", signin, bearer(friend)); got.code != 404 {
		t.Fatalf("sign-in assertion before the link: %d", got.code)
	}
	// the owner owns the registration, which is no account in the home
	if got := w.do("POST", "/v1/homes/"+id+"/assertion", signin, bearer(owner)); got.code != 404 {
		t.Fatalf("the owner, unlinked: %d", got.code)
	}
	nonce := strings.Repeat("n", 32)
	link := w.do("POST", "/v1/homes/"+id+"/assertion", map[string]string{"purpose": "link", "nonce": nonce}, bearer(friend))
	if link.code != 200 {
		w.w(link)
	}
	p, err := homeassert.Verify(link.body["assertion"].(string), w.signer.PublicKey(), time.Now())
	if err != nil || p.Pur != "link" || p.Non != nonce || p.Aud != id || p.Sub != friendID || !strings.HasSuffix(p.Lbl, "@example.test") {
		t.Fatalf("link assertion %+v %v", p, err)
	}

	// the home reports the link (unknown ids are dropped)
	if got := w.do("PUT", "/v1/servers/"+id+"/links", map[string]any{"cloud_user_ids": []string{friendID, uuid.NewString(), "not-a-uuid"}}, basic(id, secret)); got.code != 204 {
		w.w(got)
	}
	got := w.do("POST", "/v1/homes/"+sl+"/assertion", nil, bearer(friend))
	if got.code != 200 || got.body["relay_url"] != "https://"+sl+".relay.test" {
		w.w(got)
	}
	p, err = homeassert.Verify(got.body["assertion"].(string), w.signer.PublicKey(), time.Now())
	if err != nil || p.Pur != "signin" || p.Sub != friendID || p.Exp-p.Iat != 60 {
		t.Fatalf("sign-in assertion %+v %v", p, err)
	}
	if got := w.do("POST", "/v1/homes/"+id+"/assertion", signin, bearer(stranger)); got.code != 404 {
		t.Fatalf("a stranger's sign-in assertion: %d", got.code)
	}
	homes := w.do("GET", "/v1/homes", nil, bearer(friend)).body["homes"].([]any)
	if len(homes) != 1 || homes[0].(map[string]any)["linked"] != true || homes[0].(map[string]any)["owned"] != false {
		t.Fatalf("friend's homes %v", homes)
	}
	homes = w.do("GET", "/v1/homes", nil, bearer(owner)).body["homes"].([]any)
	if len(homes) != 1 || homes[0].(map[string]any)["owned"] != true || homes[0].(map[string]any)["linked"] != false {
		t.Fatalf("owner's homes %v", homes)
	}
	// unlinked at the home: the sign-in assertion stops
	if got := w.do("PUT", "/v1/servers/"+id+"/links", map[string]any{"cloud_user_ids": []string{}}, basic(id, secret)); got.code != 204 {
		w.w(got)
	}
	if got := w.do("POST", "/v1/homes/"+id+"/assertion", signin, bearer(friend)); got.code != 404 {
		t.Fatalf("after the unlink: %d", got.code)
	}
}
