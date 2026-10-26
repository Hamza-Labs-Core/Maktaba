//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// The account-side integration suite drives the api role's real router
// (newAPI) against a scratch Postgres database, with fakes for every
// external party: a recording mailer, a fake Stripe API, and a fake
// OpenID provider standing in for Google and Apple. Only test values
// are used — nothing reaches a real provider.

const (
	testTokenSecret   = "integration-test-secret-at-least-32-bytes!"
	testWebhookSecret = "whsec_test_integration"
	testPricePro      = "price_test_pro"
	testPriceFamily   = "price_test_family"
	testWebURL        = "https://app.example.test"
)

type env struct {
	t       *testing.T
	h       http.Handler
	router  *chi.Mux
	pool    *db.Pool
	mail    *mail.Recorder
	stripe  *fakeStripe
	idp     *fakeIDP
	entPath string
	ipSeq   int
	// logs is everything the api logged (the usage-data test reads it)
	logs *syncBuffer
}

// syncBuffer is a bytes.Buffer the server's goroutines may write at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	url := os.Getenv("MAKTABA_CLOUD_DB_URL")
	if url == "" {
		t.Skip("MAKTABA_CLOUD_DB_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := scratchDB(ctx, t, url)
	mig := db.NewMigrator(pool, migrations.FS, ".")
	if err := mig.Up(ctx); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, pool: pool, mail: &mail.Recorder{}, stripe: newFakeStripe(t), idp: newFakeIDP(t)}

	// A throwaway Ed25519 seed for entitlement signing.
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	e.entPath = filepath.Join(t.TempDir(), "entitlement.key")
	if err := os.WriteFile(e.entPath, []byte(base64.StdEncoding.EncodeToString(seed)), 0o600); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.Database.URL = url
	cfg.Server.ListenAddr = "127.0.0.1:0"
	cfg.Server.PublicURL = "https://api.example.test"
	cfg.Server.WebURL = testWebURL
	cfg.Auth.TokenSecret = testTokenSecret
	cfg.Mail.Driver = "log"
	cfg.Stripe.SecretKey = "sk_test_integration"
	cfg.Stripe.WebhookSecret = testWebhookSecret
	cfg.Stripe.PriceProMonthly = testPricePro
	cfg.Stripe.PriceFamilyMonthly = testPriceFamily
	cfg.OAuthGoogle.ClientID = "google-test-client"
	cfg.OAuthGoogle.ClientSecret = "google-test-secret"
	cfg.Entitlement.PrivateKeyPath = e.entPath
	if err := config.Validate(cfg, config.RoleAPI); err != nil {
		t.Fatal(err)
	}
	e.logs = &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r, stop, err := newAPI(logger, cfg, pool, mig, server.BuildInfo{Role: "api"}, apiOverrides{
		Mailer:        e.mail,
		StripeAPIBase: e.stripe.srv.URL,
		Google: &oauth.GoogleConfig{
			AuthEndpoint:  e.idp.srv.URL + "/authorize",
			TokenEndpoint: e.idp.srv.URL + "/token",
			JWKSURL:       e.idp.srv.URL + "/jwks",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	e.h, e.router = r, r
	return e
}

// scratchDB creates a database of its own for the test (CI's shared
// database already holds the home api's users table) and drops it after.
func scratchDB(ctx context.Context, t *testing.T, url string) *db.Pool {
	t.Helper()
	admin, err := db.Open(url, 2, 1, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	name := "cloud_acct_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.DB.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	u, err := neturl.Parse(url)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := db.Open(u.String(), 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pool.Close()
		_, _ = admin.DB.ExecContext(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})
	return pool
}

// client is one browser/device: its own IP, bearer token and cookies.
type client struct {
	e       *env
	ip      string
	token   string
	cookies map[string]string
}

func (e *env) client() *client {
	e.ipSeq++
	return &client{e: e, ip: fmt.Sprintf("198.51.100.%d", e.ipSeq), cookies: map[string]string{}}
}

type resp struct {
	code   int
	body   []byte
	header http.Header
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("body is not JSON (%d): %s", r.code, r.body)
	}
	return m
}

func (r resp) errCode() string {
	var m struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.body, &m)
	return m.Error
}

func (c *client) do(method, path string, body any) resp {
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case neturl.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	case []byte:
		rd, ctype = bytes.NewReader(b), "application/json"
	default:
		j, _ := json.Marshal(b)
		rd, ctype = bytes.NewReader(j), "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = c.ip + ":40000"
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	res := rec.Result()
	for _, ck := range res.Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	b, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, body: b, header: res.Header}
}

func (c *client) mustStatus(r resp, want int) resp {
	c.e.t.Helper()
	if r.code != want {
		c.e.t.Fatalf("status %d, want %d: %s", r.code, want, r.body)
	}
	return r
}

const goodPassword = "correct horse battery"

// registerVerified registers email, redeems the mailed link and signs
// in, returning a signed-in client.
func (e *env) registerVerified(email string) *client {
	e.t.Helper()
	c := e.client()
	c.mustStatus(c.do("POST", "/v1/auth/register", map[string]string{"email": email, "password": goodPassword}), http.StatusAccepted)
	tok := e.mailedToken("verify_email", email)
	c.mustStatus(c.do("POST", "/v1/auth/verify-email", map[string]string{"token": tok}), http.StatusOK)
	c.login(email, goodPassword)
	return c
}

func (c *client) login(email, pw string) {
	c.e.t.Helper()
	r := c.mustStatus(c.do("POST", "/v1/auth/login", map[string]string{"email": email, "password": pw}), http.StatusOK)
	c.token = r.json(c.e.t)["access_token"].(string)
}

func (e *env) mailedToken(kind, to string) string {
	e.t.Helper()
	m, ok := e.mail.Last(kind, to)
	if !ok {
		e.t.Fatalf("no %s mail to %s", kind, to)
	}
	i := strings.Index(m.Body, "token=")
	if i < 0 {
		e.t.Fatalf("mail has no token: %s", m.Body)
	}
	tok := m.Body[i+len("token="):]
	if j := strings.IndexAny(tok, "\n "); j >= 0 {
		tok = tok[:j]
	}
	v, _ := neturl.QueryUnescape(tok)
	return v
}

func (e *env) userID(email string) string {
	e.t.Helper()
	u, err := stores.NewUsers(e.pool.DB).ByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.DB.ExecContext(context.Background(), q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.DB.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// serviceAdmin registers an account and grants it the role through the
// CLI path.
func (e *env) serviceAdmin(email string) *client {
	e.t.Helper()
	c := e.registerVerified(email)
	var out bytes.Buffer
	if err := adminRole(context.Background(), stores.NewUsers(e.pool.DB), auditLogFor(e), "grant", email, &out); err != nil {
		e.t.Fatal(err)
	}
	return c
}

// ---- fake Stripe ---------------------------------------------------------

type fakeStripe struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []stripeReq
}

type stripeReq struct {
	Path string
	Form neturl.Values
	Auth string
}

func newFakeStripe(t *testing.T) *fakeStripe {
	f := &fakeStripe{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.reqs = append(f.reqs, stripeReq{Path: r.URL.Path, Form: r.PostForm, Auth: r.Header.Get("Authorization")})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/checkout/sessions":
			_, _ = w.Write([]byte(`{"id":"cs_test_1","url":"https://checkout.stripe.test/c/cs_test_1"}`))
		case r.URL.Path == "/v1/billing_portal/sessions":
			_, _ = w.Write([]byte(`{"id":"bps_test_1","url":"https://billing.stripe.test/p/bps_test_1"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/charges/"):
			_, _ = w.Write([]byte(`{"id":"` + strings.TrimPrefix(r.URL.Path, "/v1/charges/") + `","customer":"cus_test_1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"no such route"}}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeStripe) last(path string) (stripeReq, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.reqs) - 1; i >= 0; i-- {
		if f.reqs[i].Path == path {
			return f.reqs[i], true
		}
	}
	return stripeReq{}, false
}

// signedEvent builds a Stripe event and its Stripe-Signature header.
func signedEvent(id, typ string, created time.Time, object any) ([]byte, string) {
	obj, _ := json.Marshal(object)
	body := []byte(fmt.Sprintf(`{"id":%q,"type":%q,"created":%d,"data":{"object":%s}}`, id, typ, created.Unix(), obj))
	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return body, fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func (c *client) webhook(body []byte, sig string) resp {
	req := httptest.NewRequest("POST", "/v1/billing/webhook", bytes.NewReader(body))
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("Stripe-Signature", sig)
	rec := httptest.NewRecorder()
	c.e.h.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Result().Body)
	return resp{code: rec.Code, body: b, header: rec.Header()}
}

// ---- fake OpenID provider -----------------------------------------------

// fakeIDP issues RS256 id_tokens for whatever identity the test queued
// under a code, and checks the PKCE verifier like Google does.
type fakeIDP struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	mu  sync.Mutex
	// code → pending grant
	grants map[string]idpGrant
}

type idpGrant struct {
	claims    map[string]any
	challenge string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{key: key, grants: map[string]idpGrant{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			n := base64.RawURLEncoding.EncodeToString(key.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())
			_, _ = fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"k1","alg":"RS256","n":%q,"e":%q}]}`, n, e)
		case "/token":
			_ = r.ParseForm()
			f.mu.Lock()
			g, ok := f.grants[r.PostForm.Get("code")]
			delete(f.grants, r.PostForm.Get("code"))
			f.mu.Unlock()
			if !ok || oauth.PKCEChallenge(r.PostForm.Get("code_verifier")) != g.challenge {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"id_token":%q}`, f.sign(g.claims))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) sign(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	d := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, 5 /* crypto.SHA256 */, d[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize plays the provider's consent screen: given our redirect to
// /authorize, it queues an id_token for the identity and returns the
// callback URL the browser would be sent to.
func (f *fakeIDP) authorize(t *testing.T, location string, sub, email string, verified bool) string {
	t.Helper()
	u, err := neturl.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("authorize request lacks PKCE/nonce/state: %s", location)
	}
	code := "code-" + sub + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	f.mu.Lock()
	f.grants[code] = idpGrant{challenge: q.Get("code_challenge"), claims: map[string]any{
		"iss": "https://accounts.google.com", "aud": q.Get("client_id"), "sub": sub,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": q.Get("nonce"),
		"email": email, "email_verified": verified, "name": "Test " + sub,
	}}
	f.mu.Unlock()
	cb, _ := neturl.Parse(q.Get("redirect_uri"))
	return cb.Path + "?" + neturl.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
}

func auditLogFor(e *env) *audit.Log { return audit.New(e.pool.DB) }
