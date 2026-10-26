package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink/cloudlinktest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/discovery"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/apitokens"
	discoveryh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/discovery"
)

// cloudHome is a home on a real (scratch) Postgres with linked accounts
// wired as main.go wires them, behind the real credential chain: JWT,
// PAT, cookie, the cloud link gate, the auth gate.
type cloudHome struct {
	t     *testing.T
	db    *sql.DB
	h     *Handler
	keys  *keys.Set
	cloud *cloudlinktest.Cloud
	srv   *httptest.Server
	libs  []string
}

func newCloudHome(t *testing.T) *cloudHome {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	set := testKeys(t)
	h := NewHandler(Deps{DB: db, Keys: set})
	cloud := cloudlinktest.New(t)
	store := &cloudlink.Store{DB: db}
	h.Cloud = &CloudLinking{Store: store, Identity: &cloudlink.IdentitySource{Path: cloud.IdentityPath},
		Gate: &cloudlink.Gate{Links: store, Libraries: store.AllLibraries}}
	r := chi.NewRouter()
	h.Mount(r)
	apitokens.New(pat.New(db)).Mount(r)
	(&discoveryh.Handler{Store: discovery.NewMemoryPairingStore()}).Mount(r)
	var stack http.Handler = r
	stack = authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(stack)
	stack = h.CookieAuth(h.Cloud.Gate.Middleware(stack))
	stack = pat.Middleware(pat.New(db), users.New(db), nil)(stack)
	stack = authmw.JWTBearer(set, "api")(stack)
	srv := httptest.NewServer(stack)
	t.Cleanup(srv.Close)
	ch := &cloudHome{t: t, db: db, h: h, keys: set, cloud: cloud, srv: srv}
	for _, name := range []string{"Films", "Talks"} {
		var id string
		if err := db.QueryRow(`INSERT INTO libraries (name, roots) VALUES ($1, '{}') RETURNING CAST(id AS TEXT)`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ch.libs = append(ch.libs, id)
	}
	return ch
}

func (c *cloudHome) user(name string, admin bool) *users.User {
	c.t.Helper()
	u, err := users.New(c.db).Create(context.Background(), users.CreateInput{Username: name, Password: "correct horse battery", IsAdmin: admin})
	if err != nil {
		c.t.Fatal(err)
	}
	return u
}

// link links a cloud account to a user directly in the store (the flow
// itself is TestCloudLinkFlow's).
func (c *cloudHome) link(userID, cloudUser, scope string) {
	c.t.Helper()
	if _, err := c.db.Exec(`INSERT INTO home_user_links (user_id, cloud_user_id, scope) VALUES ($1, $2, $3)`, userID, cloudUser, scope); err != nil {
		c.t.Fatal(err)
	}
}

type answer struct {
	code int
	body map[string]any
	resp *http.Response
}

// do sends a request; auth is "Bearer <tok>", a cookie "mkt_sess=<id>" or "".
func (c *cloudHome) do(method, path, auth string, body any) answer {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	switch {
	case strings.HasPrefix(auth, "Bearer "):
		req.Header.Set("Authorization", auth)
	case strings.HasPrefix(auth, "mkt_sess="):
		req.Header.Set("Cookie", auth)
	case auth == "native":
		req.Header.Set(HeaderClient, "native")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := answer{code: resp.StatusCode, resp: resp}
	_ = json.NewDecoder(resp.Body).Decode(&out.body)
	return out
}

// signIn signs in through the cloud (native) and returns the tokens.
func (c *cloudHome) signIn(cloudUser string) (access, refreshTok string, a answer) {
	c.t.Helper()
	a = c.do("POST", "/api/auth/cloud", "native", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudUser, "")})
	access, _ = a.body["access_token"].(string)
	refreshTok, _ = a.body["refresh_token"].(string)
	return access, refreshTok, a
}

// localSignIn signs in with the password on the home (native).
func (c *cloudHome) localSignIn(name string) string {
	c.t.Helper()
	a := c.do("POST", "/api/auth/login", "native", map[string]string{"username": name, "password": "correct horse battery"})
	if a.code != 200 {
		c.t.Fatalf("local sign-in %s: %d %v", name, a.code, a.body)
	}
	return a.body["access_token"].(string)
}

func (c *cloudHome) claims(tok string) *jwt.Claims {
	c.t.Helper()
	cl, err := jwt.Verify(c.keys, tok, "api")
	if err != nil {
		c.t.Fatal(err)
	}
	return cl
}

const cloudAdmin = "11111111-1111-4111-8111-111111111111"
const cloudViewer = "22222222-2222-4222-8222-222222222222"

// The role tests (the API enforces roles): an admin linked with scope play
// signs in through the cloud and can watch, and every admin route and every
// credential-minting route refuses it.
func TestCloudPlaySignInIsNoAdmin(t *testing.T) {
	c := newCloudHome(t)
	admin := c.user("amina", true)
	c.link(admin.ID, cloudAdmin, "play")

	access, _, a := c.signIn(cloudAdmin)
	if a.code != 200 || access == "" {
		t.Fatalf("cloud sign-in: %d %v", a.code, a.body)
	}
	if a.body["cloud_scope"] != "play" || a.body["user"].(map[string]any)["is_admin"] != false {
		t.Fatalf("answer %v", a.body)
	}
	// the token itself says viewer, with every library to watch, so a
	// verifier that knows nothing of scp (the streaming service) sees one
	cl := c.claims(access)
	if cl.IsAdmin || cl.Scp != "play" || len(cl.Lib) != len(c.libs) {
		t.Fatalf("claims %+v (libraries %v)", cl, c.libs)
	}
	bearer := "Bearer " + access
	me := c.do("GET", "/api/auth/me", bearer, nil)
	if me.code != 200 || me.body["is_admin"] != false || me.body["cloud_scope"] != "play" {
		t.Fatalf("me: %d %v", me.code, me.body)
	}
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/security/audit"},
		{"POST", "/api/users"},
		{"PATCH", "/api/users/" + admin.ID},
		{"DELETE", "/api/users/" + admin.ID},
		{"POST", "/api/users/" + admin.ID + "/reset-password"},
		{"GET", "/api/users/" + admin.ID + "/cloud-link"},
		// credentials that would outlive the link
		{"POST", "/api/me/tokens"},
		{"POST", "/api/pairing/request"},
		{"POST", "/api/me/change-password"},
		{"PATCH", "/api/me/cloud-link"},
		{"POST", "/api/me/cloud-link/start"},
		{"POST", "/api/me/cloud-link"},
	} {
		got := c.do(rt.method, rt.path, bearer, map[string]any{"name": "x", "username": "x", "password": "long enough password",
			"scope": "manage", "current_password": "correct horse battery", "new_password": "another long password"})
		if got.code != http.StatusForbidden {
			t.Errorf("%s %s with a play token: %d, want 403", rt.method, rt.path, got.code)
		}
	}
	// the same admin signed in on the home is an admin
	local := "Bearer " + c.localSignIn("amina")
	if got := c.do("GET", "/api/security/audit", local, nil); got.code != 200 {
		t.Fatalf("local admin audit: %d", got.code)
	}
}

// An admin's play sign-in reads every library by name, never "all": the
// routes that read all-library access as "sees everything" (catalog-wide
// reports, every job) must not open for it. On the cookie path, and on a
// manage token the gate caps to play.
func TestCloudPlayIsNoAllLibraryAccess(t *testing.T) {
	c := newCloudHome(t)
	admin := c.user("amina", true)
	c.link(admin.ID, cloudAdmin, "manage")
	var seen *principal.Principal
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cp := *principal.FromContext(r.Context())
		seen = &cp
	})
	stack := c.h.CookieAuth(c.h.Cloud.Gate.Middleware(probe))
	check := func(name string, req *http.Request, wantAll bool) {
		t.Helper()
		seen = nil
		stack.ServeHTTP(httptest.NewRecorder(), req)
		if seen == nil {
			t.Fatalf("%s: no principal", name)
		}
		if seen.AccessAllLibraries != wantAll || (!wantAll && (seen.IsAdmin || len(seen.Libraries) != len(c.libs))) {
			t.Fatalf("%s: principal %+v (libraries %v)", name, seen, c.libs)
		}
	}
	// a web sign-in while the link is manage: the admin's full role
	web := c.do("POST", "/api/auth/cloud", "", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")})
	var sess string
	for _, ck := range web.resp.Cookies() {
		if ck.Name == CookieSession {
			sess = ck.Value
		}
	}
	manageTok, _, _ := c.signIn(cloudAdmin)
	cookieReq := func() *http.Request {
		r := httptest.NewRequest("GET", "/x", nil)
		r.AddCookie(&http.Cookie{Name: CookieSession, Value: sess})
		return r
	}
	check("manage cookie", cookieReq(), true)
	// lowered to play: the gate caps the manage JWT already out
	if _, err := c.db.Exec(`UPDATE home_user_links SET scope = 'play' WHERE user_id = $1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	c.h.Cloud.Gate.Forget(admin.ID)
	jwtReq := httptest.NewRequest("GET", "/x", nil)
	jwtReq.Header.Set("Authorization", "Bearer "+manageTok)
	var capped *principal.Principal
	authmw.JWTBearer(c.keys, "api")(c.h.Cloud.Gate.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cp := *principal.FromContext(r.Context())
		capped = &cp
	}))).ServeHTTP(httptest.NewRecorder(), jwtReq)
	if capped == nil || capped.IsAdmin || capped.AccessAllLibraries || len(capped.Libraries) != len(c.libs) {
		t.Fatalf("a manage token capped to play: %+v", capped)
	}
	// a play web sign-in's session
	web = c.do("POST", "/api/auth/cloud", "", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")})
	for _, ck := range web.resp.Cookies() {
		if ck.Name == CookieSession {
			sess = ck.Value
		}
	}
	check("play cookie", cookieReq(), false)
	// and the token's lifetime is the cloud's short one
	if cl := c.claims(manageTok); cl.Exp-cl.Iat > int64(CloudAccessTTL/time.Second) {
		t.Fatalf("cloud access token lives %ds", cl.Exp-cl.Iat)
	}
}

// manage is set only on the home, by an admin, for their own link; a
// manage sign-in is then the admin's full role. Lowering it ends the
// manage sign-ins at once.
func TestCloudManageScope(t *testing.T) {
	c := newCloudHome(t)
	admin := c.user("amina", true)
	viewer := c.user("vic", false)
	c.link(admin.ID, cloudAdmin, "play")
	c.link(viewer.ID, cloudViewer, "play")

	// a viewer cannot raise their own link
	vlocal := "Bearer " + c.localSignIn("vic")
	if got := c.do("PATCH", "/api/me/cloud-link", vlocal, map[string]string{"scope": "manage"}); got.code != 403 {
		t.Fatalf("viewer set manage: %d", got.code)
	}
	// the admin, signed in on the home, can
	alocal := "Bearer " + c.localSignIn("amina")
	if got := c.do("PATCH", "/api/me/cloud-link", alocal, map[string]string{"scope": "manage"}); got.code != 200 || got.body["scope"] != "manage" {
		t.Fatalf("admin set manage: %d %v", got.code, got.body)
	}
	access, refreshTok, a := c.signIn(cloudAdmin)
	if a.code != 200 || !c.claims(access).IsAdmin || c.claims(access).Scp != "manage" {
		t.Fatalf("manage sign-in: %d %v", a.code, a.body)
	}
	if got := c.do("GET", "/api/security/audit", "Bearer "+access, nil); got.code != 200 {
		t.Fatalf("manage audit: %d", got.code)
	}
	// still a cloud sign-in: no credential of its own
	if got := c.do("POST", "/api/me/tokens", "Bearer "+access, map[string]string{"name": "x"}); got.code != 403 {
		t.Fatalf("manage sign-in made an API token: %d", got.code)
	}
	// a viewer's manage sign-in is still a viewer: the role is the user's
	vacc, _, _ := c.signIn(cloudViewer)
	if c.claims(vacc).IsAdmin {
		t.Fatal("a viewer's cloud token says admin")
	}

	// lowered to play: the manage family ends, the access token out is
	// capped at the gate
	if got := c.do("PATCH", "/api/me/cloud-link", alocal, map[string]string{"scope": "play"}); got.code != 200 {
		t.Fatalf("set play: %d", got.code)
	}
	if got := c.do("GET", "/api/security/audit", "Bearer "+access, nil); got.code != 403 {
		t.Fatalf("a manage token after its link went play: %d, want 403", got.code)
	}
	if got := c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": refreshTok}); got.code != 401 {
		t.Fatalf("refresh of a manage family after play: %d, want 401", got.code)
	}
}

// One use, this home, the right purpose, a linked user: anything else is
// a refused sign-in with nothing said about why.
func TestCloudSignInRefusals(t *testing.T) {
	c := newCloudHome(t)
	u := c.user("amina", true)
	c.link(u.ID, cloudAdmin, "play")
	post := func(assertion string) int {
		return c.do("POST", "/api/auth/cloud", "native", map[string]string{"assertion": assertion}).code
	}
	tok := c.cloud.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")
	if got := post(tok); got != 200 {
		t.Fatalf("first use: %d", got)
	}
	if got := post(tok); got != 401 {
		t.Fatalf("replay: %d, want 401", got)
	}
	if got := post(c.cloud.Assertion(cloudlink.PurposeLink, cloudAdmin, "nonce-nonce-nonce")); got != 401 {
		t.Fatalf("a link assertion signed in: %d", got)
	}
	if got := post(c.cloud.Assertion(cloudlink.PurposeSignIn, "33333333-3333-4333-8333-333333333333", "")); got != 401 {
		t.Fatalf("an unlinked cloud account signed in: %d", got)
	}
	now := time.Now().Unix()
	other := c.cloud.Sign(cloudlink.Assertion{Typ: "home-assertion", Pur: "signin", Sub: cloudAdmin,
		Aud: "6f1c2a8e-0000-4000-8000-0000000000ff", Iat: now, Exp: now + 60, Jti: "other-home"})
	if got := post(other); got != 401 {
		t.Fatalf("another home's assertion: %d", got)
	}
	forged := cloudlinktest.New(t) // another key
	forged.ServerID = c.cloud.ServerID
	if got := post(forged.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")); got != 401 {
		t.Fatalf("an assertion by another key: %d", got)
	}
	// a locked user is refused
	if _, err := c.db.Exec(`UPDATE users SET locked_until = now() + interval '1 hour' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	if got := post(c.cloud.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")); got != http.StatusLocked {
		t.Fatalf("a locked user: %d, want 423", got)
	}
}

// Unlinking (the user, or an admin) ends the cloud sign-ins at once: the
// web session, the refresh family, and the access token already out.
func TestCloudUnlinkEndsSignIns(t *testing.T) {
	c := newCloudHome(t)
	admin := c.user("amina", true)
	viewer := c.user("vic", false)
	c.link(viewer.ID, cloudViewer, "play")

	access, refreshTok, _ := c.signIn(cloudViewer)
	// a web sign-in too (no native header: a session cookie)
	web := c.do("POST", "/api/auth/cloud", "", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudViewer, "")})
	var cookie string
	for _, ck := range web.resp.Cookies() {
		if ck.Name == CookieSession {
			cookie = "mkt_sess=" + ck.Value
		}
	}
	if web.code != 200 || cookie == "" {
		t.Fatalf("web cloud sign-in: %d", web.code)
	}
	if got := c.do("GET", "/api/auth/me", cookie, nil); got.code != 200 || got.body["cloud_scope"] != "play" {
		t.Fatalf("cookie me: %d %v", got.code, got.body)
	}
	// a refresh keeps the scope
	ref := c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": refreshTok})
	if ref.code != 200 || c.claims(ref.body["access_token"].(string)).Scp != "play" {
		t.Fatalf("refresh: %d %v", ref.code, ref.body)
	}
	refreshTok = ref.body["refresh_token"].(string)

	// an admin unlinks the viewer
	alocal := "Bearer " + c.localSignIn("amina")
	if got := c.do("DELETE", "/api/users/"+viewer.ID+"/cloud-link", alocal, nil); got.code != 204 {
		t.Fatalf("admin unlink: %d %v", got.code, got.body)
	}
	_ = admin
	if got := c.do("GET", "/api/auth/me", "Bearer "+access, nil); got.code != 401 {
		t.Fatalf("access token after unlink: %d, want 401", got.code)
	}
	if got := c.do("GET", "/api/auth/me", cookie, nil); got.code != 401 {
		t.Fatalf("cookie after unlink: %d, want 401", got.code)
	}
	if got := c.do("POST", "/api/auth/refresh", "", map[string]string{"refresh_token": refreshTok}); got.code != 401 {
		t.Fatalf("refresh after unlink: %d, want 401", got.code)
	}
	if got := c.do("POST", "/api/auth/cloud", "native", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudViewer, "")}); got.code != 401 {
		t.Fatalf("sign-in after unlink: %d", got.code)
	}
	// the viewer's own sign-in on the home is untouched
	_ = c.localSignIn("vic")
}

// The link step: a nonce for the signed-in home user, the cloud's link
// assertion carrying it, one cloud account per home user and back.
func TestCloudLinkFlow(t *testing.T) {
	c := newCloudHome(t)
	c.user("amina", true)
	c.user("vic", false)
	vic := "Bearer " + c.localSignIn("vic")
	amina := "Bearer " + c.localSignIn("amina")

	start := c.do("POST", "/api/me/cloud-link/start", vic, nil)
	nonce, _ := start.body["nonce"].(string)
	if start.code != 201 || nonce == "" || start.body["server_id"] != c.cloud.ServerID {
		t.Fatalf("start: %d %v", start.code, start.body)
	}
	// another user's nonce does not link vic's (or anyone's) account
	if got := c.do("POST", "/api/me/cloud-link", amina, map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeLink, cloudAdmin, nonce)}); got.code != 422 {
		t.Fatalf("someone else's nonce: %d", got.code)
	}
	done := c.do("POST", "/api/me/cloud-link", vic, map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeLink, cloudViewer, nonce)})
	if done.code != 200 || done.body["scope"] != "play" || done.body["cloud_user_id"] != cloudViewer {
		t.Fatalf("link: %d %v", done.code, done.body)
	}
	// the nonce served once
	if got := c.do("POST", "/api/me/cloud-link", vic, map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeLink, cloudViewer, nonce)}); got.code != 422 {
		t.Fatalf("nonce reused: %d", got.code)
	}
	// the same cloud account cannot also be amina's
	n2 := c.do("POST", "/api/me/cloud-link/start", amina, nil).body["nonce"].(string)
	if got := c.do("POST", "/api/me/cloud-link", amina, map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeLink, cloudViewer, n2)}); got.code != 409 {
		t.Fatalf("cloud account linked twice: %d", got.code)
	}
	if got := c.do("GET", "/api/me/cloud-link", vic, nil); got.code != 200 || got.body["available"] != true || got.body["link"] == nil {
		t.Fatalf("get link: %d %v", got.code, got.body)
	}
	// the user unlinks their own
	if got := c.do("DELETE", "/api/me/cloud-link", vic, nil); got.code != 204 {
		t.Fatalf("unlink: %d", got.code)
	}
	if ids, _ := (&cloudlink.Store{DB: c.db}).LinkedCloudUsers(context.Background()); len(ids) != 0 {
		t.Fatalf("linked after unlink: %v", ids)
	}
}

// A home not claimed in the cloud has no linked accounts.
func TestCloudSignInWithoutIdentity(t *testing.T) {
	c := newCloudHome(t)
	c.h.Cloud.Identity = &cloudlink.IdentitySource{}
	if got := c.do("POST", "/api/auth/cloud", "native", map[string]string{"assertion": c.cloud.Assertion(cloudlink.PurposeSignIn, cloudAdmin, "")}); got.code != 404 {
		t.Fatalf("unclaimed home: %d, want 404", got.code)
	}
}
