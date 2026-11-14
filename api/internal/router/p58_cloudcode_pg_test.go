package router_test

// Epic 58 story 58.03 T03: a TV signed in with a cloud code, behind the production pairing
// mount (MountP10) over a real migrated Postgres. A viewer, an admin and a restricted child
// each approve a code; the relay agent is a fake that plays the cloud. The TV then redeems the
// grant the cloud would hand it, and is signed in as exactly the approver: their role, their
// libraries, their parental policy. No one signed in gets 401, a cloud sign-in 403, and a home
// with no relay agent says remote access is off.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink/cloudlinktest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/router"
)

const (
	ccAdmin    = "00000000-0000-4000-8000-0000005803a1"
	ccViewer   = "00000000-0000-4000-8000-0000005803b1"
	ccChild    = "00000000-0000-4000-8000-0000005803c1"
	ccGuardian = "00000000-0000-4000-8000-0000005803d1"
	ccLibA     = "00000000-0000-4000-8000-00000058a000"
	ccLibB     = "00000000-0000-4000-8000-00000058b000"
)

// ccAgent is the relay agent as the home reaches it, playing the cloud: any code of the cloud's
// alphabet is open until it is claimed, and the grant each claim carried is kept.
type ccAgent struct {
	mu      sync.Mutex
	claimed map[string]string
}

func (a *ccAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rest := strings.TrimPrefix(r.URL.Path, "/admin/cloud-link/tv-codes/")
	code, claim := strings.CutSuffix(rest, "/claim")
	if _, done := a.claimed[code]; done {
		w.WriteHeader(map[bool]int{true: http.StatusConflict, false: http.StatusNotFound}[claim])
		return
	}
	if claim {
		var body struct{ Grant string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		a.claimed[code] = body.Grant
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"code":"`+code+`","platform":"tizen","app_version":"1.0.0","country":"JO","expires_at":"2030-01-01T00:00:00Z"}`)
}

func (a *ccAgent) grant(code string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.claimed[code]
}

func setupCloudCode(t *testing.T) (http.Handler, *ccAgent, func(q string, args ...any) string) {
	t.Helper()
	h, agent, read, _ := setupCloudCodeWith(t, nil)
	return h, agent, read
}

// setupCloudCodeWith is setupCloudCode on a home claimed in a (test) Maktaba Cloud when cloud is
// set: main.go's wiring, the auth handler finding the user a cloud tvcode assertion stands for.
// exec seeds more rows.
func setupCloudCodeWith(t *testing.T, cloud *cloudlinktest.Cloud) (http.Handler, *ccAgent, func(q string, args ...any) string, func(q string, args ...any)) {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	read := func(q string, args ...any) string {
		t.Helper()
		var s string
		if err := db.QueryRow(q, args...).Scan(&s); err != nil {
			t.Fatalf("read %q: %v", q, err)
		}
		return s
	}
	exec(`INSERT INTO users (id, username, pw_hash, is_admin) VALUES ($1,'cc-admin','x',true), ($2,'cc-viewer','x',false),
		($3,'cc-child','x',false), ($4,'cc-guardian','x',false)`, ccAdmin, ccViewer, ccChild, ccGuardian)
	exec(`INSERT INTO libraries (id, name, roots) VALUES ($1,'CcAlpha','{/media/cc-a}'), ($2,'CcBeta','{/media/cc-b}')`, ccLibA, ccLibB)
	// the viewer reads library A only; the child reads both, under its policy
	exec(`INSERT INTO library_acl (user_id, library_id) VALUES ($1,$2), ($3,$2), ($3,$4)`, ccViewer, ccLibA, ccChild, ccLibB)
	exec(`INSERT INTO parental_policies (user_id, max_tier, version) VALUES ($1, 9, 1)`, ccChild)
	exec(`INSERT INTO parental_guardians (child_id, guardian_id) VALUES ($1,$2)`, ccChild, ccGuardian)

	agent := &ccAgent{claimed: map[string]string{}}
	srv := httptest.NewServer(agent)
	t.Cleanup(srv.Close)
	t.Setenv("MAKTABA_CLOUDLINK_ADMIN", strings.TrimPrefix(srv.URL, "http://"))

	k, err := keys.Generate(keys.MinBits)
	if err != nil {
		t.Fatal(err)
	}
	ks := keys.NewSet(0)
	ks.Rotate(k, keys.RotateImmediate)
	r := router.New(router.Deps{IdempotencyStore: idempotency.NewMemoryStore()})
	p9deps := router.P9Deps{DB: db, Keys: ks}
	if cloud != nil {
		p9deps.CloudIdentityPath = cloud.IdentityPath
	}
	p9 := router.MountP9(r, p9deps)
	p10deps := router.P10Deps{DB: db, Keys: ks, Logger: slog.Default()}
	if cloud != nil {
		p10deps.CloudTVCodes = p9
	}
	router.MountP10(r, p10deps)
	router.MountP11(r, router.P11Deps{DB: db})
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var p *principal.Principal
		switch req.Header.Get("X-Test-As") {
		case "admin":
			p = &principal.Principal{UserID: ccAdmin, IsAdmin: true, AccessAllLibraries: true, Source: principal.SourceJWT}
		case "viewer":
			p = &principal.Principal{UserID: ccViewer, Libraries: []string{ccLibA}, Source: principal.SourceJWT}
		case "child":
			p = &principal.Principal{UserID: ccChild, Libraries: []string{ccLibA, ccLibB}, Source: principal.SourceJWT}
		case "guardian":
			p = &principal.Principal{UserID: ccGuardian, Source: principal.SourceJWT}
		case "tv-admin": // the admin's cloud-code TV, as its token's rst claim makes it
			p = &principal.Principal{UserID: ccAdmin, Libraries: []string{ccLibA, ccLibB}, Source: principal.SourceJWT}
			p.ApplyRestricted(true)
		case "cloud-viewer":
			p = &principal.Principal{UserID: ccViewer, Libraries: []string{ccLibA}, Source: principal.SourceJWT, CloudScope: "play"}
		}
		if p != nil {
			req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		}
		r.ServeHTTP(w, req)
	})
	return h, agent, read, exec
}

func ccDo(h http.Handler, who, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if who != "" {
		req.Header.Set("X-Test-As", who)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ccClaims is the access token's payload.
type ccClaims struct {
	Usr     string   `json:"usr"`
	IsAdmin bool     `json:"is_admin"`
	Lib     []string `json:"lib"`
	Fam     string   `json:"fam"`
	Rst     bool     `json:"rst"`
}

// redeem plays the TV: it redeems the grant the cloud handed it on the home.
func redeem(t *testing.T, h http.Handler, grant string) ccClaims {
	t.Helper()
	rec := ccDo(h, "", http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+grant+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body.String())
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	parts := strings.Split(tok.AccessToken, ".")
	if len(parts) != 3 {
		t.Fatalf("access token %q", tok.AccessToken)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c ccClaims
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	// once: the grant is single-use
	if again := ccDo(h, "", http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+grant+`"}`); again.Code == http.StatusOK {
		t.Fatal("the grant redeemed twice")
	}
	return c
}

func TestPGCloudCodeSignsTheTVInAsTheApprover(t *testing.T) {
	h, agent, read := setupCloudCode(t)
	for i, c := range []struct {
		who, body string
		code      string
		wantUser  string
		wantAdmin bool
		wantLibs  []string
	}{
		{"viewer", "", "VWER2345", ccViewer, false, []string{ccLibA}},
		// an admin's TV never acts as an admin (a code can be read out to anyone): it reads
		// every library, named one by one
		{"admin", "", "ADMN2345", ccAdmin, false, []string{ccLibA, ccLibB}},
		// a restricted child approving for itself is allowed; the TV is the child's, restricted
		{"child", "", "CHVD2345", ccChild, false, []string{ccLibA, ccLibB}},
		// a guardian approving as the child they look after (57.02)
		{"guardian", `,"as_user":"` + ccChild + `"`, "GRDN2345", ccChild, false, []string{ccLibA, ccLibB}},
	} {
		look := ccDo(h, c.who, http.MethodGet, "/api/pairing/cloud-code/"+c.code[:4]+"-"+c.code[4:], "")
		if look.Code != http.StatusOK || !strings.Contains(look.Body.String(), `"platform":"tizen"`) || !strings.Contains(look.Body.String(), `"country":"JO"`) {
			t.Fatalf("%s lookup: %d %s", c.who, look.Code, look.Body.String())
		}
		rec := ccDo(h, c.who, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"`+strings.ToLower(c.code)+`"`+c.body+`}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s approve: %d %s", c.who, rec.Code, rec.Body.String())
		}
		grant := agent.grant(c.code)
		if grant == "" {
			t.Fatalf("%s: the cloud got no grant", c.who)
		}
		got := redeem(t, h, grant)
		if got.Usr != c.wantUser || got.IsAdmin != c.wantAdmin || !got.Rst || strings.Join(got.Lib, ",") != strings.Join(c.wantLibs, ",") {
			t.Fatalf("row %d (%s): the TV signed in as %+v", i, c.who, got)
		}
		// the sign-in is the approver's, and says it came by a cloud code
		meta := read(`SELECT client_meta::text FROM refresh_tokens WHERE family_id = $1 AND user_id = $2`, got.Fam, c.wantUser)
		if !strings.Contains(meta, `"via": "cloud_code"`) || !strings.Contains(meta, `"device_kind": "tv"`) || !strings.Contains(meta, `"device_label": "tizen"`) {
			t.Fatalf("%s: client_meta %s", c.who, meta)
		}
		// approving the same code again: the cloud refuses, nothing is made
		if again := ccDo(h, c.who, http.MethodPost, "/api/pairing/cloud-approve", `{"code":"`+c.code+`"}`); again.Code != http.StatusNotFound {
			t.Fatalf("%s approving twice: %d", c.who, again.Code)
		}
	}
	// the child's TV is held to its policy: the parental policy is the child's own
	if tier := read(`SELECT max_tier::text FROM parental_policies WHERE user_id = $1`, ccChild); tier != "9" {
		t.Fatalf("child policy %s", tier)
	}
}

// An admin's cloud-code TV stays a viewer when it refreshes its token; the admin's own sign-ins
// are untouched.
func TestPGCloudCodeAdminTVRefreshesAsAViewer(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	if rec := ccDo(h, "admin", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"RFSH2345"}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	rec := ccDo(h, "", http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+agent.grant("RFSH2345")+`"}`)
	var tok struct {
		RefreshToken string `json:"refresh_token"`
	}
	if _ = json.Unmarshal(rec.Body.Bytes(), &tok); rec.Code != http.StatusOK || tok.RefreshToken == "" {
		t.Fatalf("redeem: %d %s", rec.Code, rec.Body.String())
	}
	for range 2 { // and after a rotation too
		rec = ccDo(h, "", http.MethodPost, "/api/auth/refresh", `{"refresh_token":"`+tok.RefreshToken+`"}`)
		var out struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			User         struct {
				IsAdmin bool `json:"is_admin"`
			} `json:"user"`
		}
		if _ = json.Unmarshal(rec.Body.Bytes(), &out); rec.Code != http.StatusOK || out.User.IsAdmin {
			t.Fatalf("refresh: %d %s", rec.Code, rec.Body.String())
		}
		parts := strings.Split(out.AccessToken, ".")
		b, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c ccClaims
		_ = json.Unmarshal(b, &c)
		if c.Usr != ccAdmin || c.IsAdmin || !c.Rst || strings.Join(c.Lib, ",") != ccLibA+","+ccLibB {
			t.Fatalf("refreshed claims %+v", c)
		}
		tok.RefreshToken = out.RefreshToken
	}
}

// A cloud code's TV mints no credential of its own: no API token, no approval of another TV
// (either kind of code), so it cannot climb back to the admin it was approved by.
func TestPGCloudCodeTVMintsNothing(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	if rec := ccDo(h, "tv-admin", http.MethodPost, "/api/me/tokens", `{"name":"climb"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an API token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := ccDo(h, "admin", http.MethodPost, "/api/me/tokens", `{"name":"mine"}`); rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("the admin's own API token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := ccDo(h, "tv-admin", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"CLMB2345"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a cloud code: %d %s", rec.Code, rec.Body.String())
	}
	if agent.grant("CLMB2345") != "" {
		t.Fatal("the cloud got a grant")
	}
	start := ccDo(h, "", http.MethodPost, "/api/pairing/device", "")
	var s struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &s)
	if rec := ccDo(h, "tv-admin", http.MethodPost, "/api/pairing/approve", `{"code":"`+s.Code+`"}`); s.Code == "" || rec.Code != http.StatusForbidden {
		t.Fatalf("a home code %q: %d %s", s.Code, rec.Code, rec.Body.String())
	}
}

func TestPGCloudCodeRefusals(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	// no one signed in
	if rec := ccDo(h, "", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"NNNE2345"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one: %d", rec.Code)
	}
	if rec := ccDo(h, "", http.MethodGet, "/api/pairing/cloud-code/NNNE2345", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one, lookup: %d", rec.Code)
	}
	// a sign-in through Maktaba Cloud cannot sign a device in
	if rec := ccDo(h, "cloud-viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"CXQD2345"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("cloud sign-in: %d %s", rec.Code, rec.Body.String())
	}
	// a viewer cannot name someone else
	if rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"ASAS2345","as_user":"`+ccChild+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("viewer as the child: %d", rec.Code)
	}
	// a child cannot name its guardian
	if rec := ccDo(h, "child", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"ASCH2345","as_user":"`+ccGuardian+`"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("child as the guardian: %d", rec.Code)
	}
	// a home code (with a 0 or 1) is not a cloud code
	if rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"AB01-CD23"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a home-shaped code: %d", rec.Code)
	}
	if len(agent.claimed) != 0 {
		t.Fatalf("refused approvals reached the cloud: %v", agent.claimed)
	}
	// no relay agent: remote access is off on this home
	t.Setenv("MAKTABA_CLOUDLINK_ADMIN", "")
	rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"NFFF2345"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cloud_not_linked") {
		t.Fatalf("no cloud link: %d %s", rec.Code, rec.Body.String())
	}
	_ = context.Background()
}

// The approver's list of their devices shows the TV as a cloud-code sign-in, and signs it out.
func TestPGAppSignInsShowTheCloudCodeTV(t *testing.T) {
	h, agent, read := setupCloudCode(t)
	if rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"LIST2345"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an L is not in the alphabet: %d", rec.Code)
	}
	if rec := ccDo(h, "viewer", http.MethodPost, "/api/pairing/cloud-approve", `{"code":"MYTV2345"}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	tv := redeem(t, h, agent.grant("MYTV2345"))
	rec := ccDo(h, "viewer", http.MethodGet, "/api/me/app-sign-ins", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []struct {
			ID, Kind, Label, Via string
		}
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].ID != tv.Fam || list.Items[0].Kind != "tv" || list.Items[0].Label != "tizen" || list.Items[0].Via != "cloud_code" {
		t.Fatalf("list: %s", rec.Body.String())
	}
	// it is the viewer's alone: the admin sees none, and cannot sign it out
	if rec := ccDo(h, "admin", http.MethodGet, "/api/me/app-sign-ins", ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("admin's list: %s", rec.Body.String())
	}
	if rec := ccDo(h, "admin", http.MethodDelete, "/api/me/app-sign-ins/"+tv.Fam, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("admin signing out the viewer's TV: %d", rec.Code)
	}
	if rec := ccDo(h, "", http.MethodGet, "/api/me/app-sign-ins", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no one: %d", rec.Code)
	}
	if rec := ccDo(h, "viewer", http.MethodDelete, "/api/me/app-sign-ins/"+tv.Fam, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("sign out: %d %s", rec.Code, rec.Body.String())
	}
	if live := read(`SELECT count(*)::text FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`, tv.Fam); live != "0" {
		t.Fatalf("%s live tokens after sign-out", live)
	}
	if rec := ccDo(h, "viewer", http.MethodGet, "/api/me/app-sign-ins", ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("list after sign-out: %s", rec.Body.String())
	}
}

// A code approved on maktaba.tv (the cloud's tvcode assertion, sent through the relay with no
// sign-in on the home): the TV signs in as the home user the cloud account is linked to,
// restricted like every cloud code's TV, an admin's included. Role test: an assertion for an
// unlinked account, another code, another purpose, or a second use approves nothing.
func TestPGCloudAssertionSignsTheTVInAsTheLinkedUser(t *testing.T) {
	cloud := cloudlinktest.New(t)
	h, agent, _, exec := setupCloudCodeWith(t, cloud)
	exec(`INSERT INTO home_user_links (user_id, cloud_user_id, scope) VALUES ($1,'cloud-viewer','play'), ($2,'cloud-admin','manage')`,
		ccViewer, ccAdmin)
	approve := func(assertion, code string) *httptest.ResponseRecorder {
		return ccDo(h, "", http.MethodPost, "/api/pairing/cloud-assertion", `{"code":"`+code+`","assertion":"`+assertion+`"}`)
	}

	for _, c := range []struct {
		cloudUser, code string
		wantUser        string
		wantLibs        []string
	}{
		{"cloud-viewer", "WEBV2345", ccViewer, []string{ccLibA}},
		// an admin's own link, manage scope: the TV still never acts as an admin
		{"cloud-admin", "WEBA2345", ccAdmin, []string{ccLibA, ccLibB}},
	} {
		a := cloud.Assertion(cloudlink.PurposeTVCode, c.cloudUser, c.code)
		rec := approve(a, c.code)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"platform":"tizen"`) {
			t.Fatalf("%s: approve = %d %s", c.cloudUser, rec.Code, rec.Body.String())
		}
		got := redeem(t, h, agent.grant(c.code))
		if got.Usr != c.wantUser || got.IsAdmin || !got.Rst || strings.Join(got.Lib, ",") != strings.Join(c.wantLibs, ",") {
			t.Fatalf("%s: signed in as %+v", c.cloudUser, got)
		}
		// one use: the same assertion again approves nothing
		if again := approve(a, c.code); again.Code != http.StatusForbidden {
			t.Fatalf("%s: replayed = %d %s", c.cloudUser, again.Code, again.Body.String())
		}
	}

	for name, a := range map[string]string{
		"an account the home never linked": cloud.Assertion(cloudlink.PurposeTVCode, "cloud-stranger", "WEBX2345"),
		"an assertion for another code":    cloud.Assertion(cloudlink.PurposeTVCode, "cloud-viewer", "WEBY2345"),
		"a sign-in assertion":              cloud.Assertion(cloudlink.PurposeSignIn, "cloud-viewer", ""),
		"another cloud's key":              cloudlinktest.New(t).Assertion(cloudlink.PurposeTVCode, "cloud-viewer", "WEBX2345"),
	} {
		if rec := approve(a, "WEBX2345"); rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		if g := agent.grant("WEBX2345"); g != "" {
			t.Fatalf("%s: claimed", name)
		}
	}
}

// A home never claimed in the cloud holds no cloud key: an assertion approves nothing there.
func TestPGCloudAssertionWithoutACloudIdentity(t *testing.T) {
	h, agent, _ := setupCloudCode(t)
	rec := ccDo(h, "", http.MethodPost, "/api/pairing/cloud-assertion", `{"code":"WEBZ2345","assertion":"mkha1.x.y"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cloud_not_linked") {
		t.Fatalf("no identity = %d %s", rec.Code, rec.Body.String())
	}
	if agent.grant("WEBZ2345") != "" {
		t.Fatal("claimed")
	}
}

// A TV approved on maktaba.tv is a cloud sign-in of the link it came through: it refreshes as a
// restricted viewer (an admin's too), and unlinking the cloud account ends it, at once and for
// every token of its family.
func TestPGCloudAssertionTVEndsWithItsLink(t *testing.T) {
	cloud := cloudlinktest.New(t)
	h, agent, read, exec := setupCloudCodeWith(t, cloud)
	exec(`INSERT INTO home_user_links (user_id, cloud_user_id, scope) VALUES ($1,'cloud-viewer','play'), ($2,'cloud-admin','manage')`,
		ccViewer, ccAdmin)
	signIn := func(cloudUser, code string) (string, ccClaims) {
		t.Helper()
		a := cloud.Assertion(cloudlink.PurposeTVCode, cloudUser, code)
		if rec := ccDo(h, "", http.MethodPost, "/api/pairing/cloud-assertion", `{"code":"`+code+`","assertion":"`+a+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: approve = %d %s", cloudUser, rec.Code, rec.Body.String())
		}
		rec := ccDo(h, "", http.MethodPost, "/api/pairing/device/token", `{"device_code":"`+agent.grant(code)+`"}`)
		var tok struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		if _ = json.Unmarshal(rec.Body.Bytes(), &tok); rec.Code != http.StatusOK || tok.RefreshToken == "" {
			t.Fatalf("%s: redeem = %d %s", cloudUser, rec.Code, rec.Body.String())
		}
		return tok.RefreshToken, ccTokenClaims(t, tok.AccessToken)
	}
	refresh := func(rt string) (*httptest.ResponseRecorder, string, ccClaims) {
		t.Helper()
		rec := ccDo(h, "", http.MethodPost, "/api/auth/refresh", `{"refresh_token":"`+rt+`"}`)
		var out struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != http.StatusOK {
			return rec, "", ccClaims{}
		}
		return rec, out.RefreshToken, ccTokenClaims(t, out.AccessToken)
	}

	viewerRT, viewer := signIn("cloud-viewer", "TVVW2345")
	adminRT, admin := signIn("cloud-admin", "TVAD2345")
	// recorded as each link's cloud sign-in, scope play
	for _, c := range []ccClaims{viewer, admin} {
		if got := read(`SELECT string_agg(kind||' '||scope, ',') FROM cloud_sign_ins WHERE user_id = $1`, c.Usr); got != "refresh play" {
			t.Fatalf("%s: cloud sign-in = %q", c.Usr, got)
		}
	}
	// an admin's TV refreshes restricted and never as an admin, twice (a rotation too)
	for range 2 {
		rec, next, c := refresh(adminRT)
		if rec.Code != http.StatusOK || c.Usr != ccAdmin || c.IsAdmin || !c.Rst {
			t.Fatalf("admin TV refresh = %d %+v %s", rec.Code, c, rec.Body.String())
		}
		adminRT = next
	}
	rec, next, c := refresh(viewerRT)
	if rec.Code != http.StatusOK || c.Usr != ccViewer || !c.Rst || strings.Join(c.Lib, ",") != ccLibA {
		t.Fatalf("viewer TV refresh = %d %+v", rec.Code, c)
	}
	viewerRT = next

	// the admin unlinks the viewer's cloud account: the viewer's TV is signed out at once
	if rec := ccDo(h, "admin", http.MethodDelete, "/api/users/"+ccViewer+"/cloud-link", ""); rec.Code/100 != 2 {
		t.Fatalf("unlink = %d %s", rec.Code, rec.Body.String())
	}
	if live := read(`SELECT count(*)::text FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`, viewer.Fam); live != "0" {
		t.Fatalf("live tokens of the unlinked TV = %s", live)
	}
	if rec, _, _ := refresh(viewerRT); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unlinked TV refresh = %d %s", rec.Code, rec.Body.String())
	}
	// the admin's TV, another link, is untouched
	if rec, _, _ := refresh(adminRT); rec.Code != http.StatusOK {
		t.Fatalf("other TV refresh = %d %s", rec.Code, rec.Body.String())
	}
}

// ccTokenClaims reads an access token's claims.
func ccTokenClaims(t *testing.T, access string) ccClaims {
	t.Helper()
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		t.Fatalf("access token %q", access)
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var c ccClaims
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
