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
	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// The account-side integration suite drives the api role's real router
// (newAPI) against a scratch Postgres database, with fakes for every
// external party: a recording mailer, a fake OpenID provider standing in
// for Google and Apple, and (newPaidEnv) a fake payment provider. Only
// test values are used — nothing reaches a real provider. newEnv runs
// the production default payment provider, "none".

const (
	testTokenSecret   = "integration-test-secret-at-least-32-bytes!"
	testWebhookSecret = "test-webhook-secret"
	testWebURL        = "https://app.example.test"
)

type env struct {
	t       *testing.T
	h       http.Handler
	router  *chi.Mux
	pool    *db.Pool
	mail    *mail.Recorder
	pay     *fakeProvider // nil: the "none" provider
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

// newEnv runs the api with the production default payment provider,
// "none": no payments, plans set by hand.
func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil)
}

// newPaidEnv runs the api with a fake payment provider, so the
// provider-neutral checkout, portal and webhook paths are exercised.
func newPaidEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, &fakeProvider{})
}

func newEnvWith(t *testing.T, pay *fakeProvider) *env {
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
	e := &env{t: t, pool: pool, mail: &mail.Recorder{}, pay: pay, idp: newFakeIDP(t)}

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
	cfg.Billing.Provider = "none"
	cfg.OAuthGoogle.ClientID = "google-test-client"
	cfg.OAuthGoogle.ClientSecret = "google-test-secret"
	cfg.Entitlement.PrivateKeyPath = e.entPath
	if err := config.Validate(cfg, config.RoleAPI); err != nil {
		t.Fatal(err)
	}
	e.logs = &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	o := apiOverrides{
		Mailer: e.mail,
		Google: &oauth.GoogleConfig{
			AuthEndpoint:  e.idp.srv.URL + "/authorize",
			TokenEndpoint: e.idp.srv.URL + "/token",
			JWKSURL:       e.idp.srv.URL + "/jwks",
		},
	}
	if pay != nil {
		o.BillingProvider = pay
	}
	r, stop, err := newAPI(logger, cfg, pool, mig, server.BuildInfo{Role: "api"}, o)
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

// ---- fake payment provider ----------------------------------------------

// fakeProvider is a payment provider for tests ("testpay"). Its webhook
// body is a provider-neutral event as JSON, signed with an HMAC-SHA256 of
// the body in X-Test-Signature.
type fakeProvider struct {
	mu        sync.Mutex
	checkouts []billingpkg.CheckoutParams
	portals   []string
	seq       int
}

func (*fakeProvider) Name() string  { return "testpay" }
func (*fakeProvider) Enabled() bool { return true }
func (*fakeProvider) Sells(plan string) bool {
	return plan == billingpkg.PlanPro || plan == billingpkg.PlanFamily
}

func (f *fakeProvider) CreateCheckout(_ context.Context, p billingpkg.CheckoutParams) (billingpkg.CheckoutSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.checkouts = append(f.checkouts, p)
	id := fmt.Sprintf("cs_%d", f.seq)
	return billingpkg.CheckoutSession{ID: id, URL: "https://pay.example.test/checkout/" + id}, nil
}

func (f *fakeProvider) CreatePortal(_ context.Context, customer, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.portals = append(f.portals, customer)
	return "https://pay.example.test/portal/" + customer, nil
}

func (f *fakeProvider) lastCheckout() billingpkg.CheckoutParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.checkouts) == 0 {
		return billingpkg.CheckoutParams{}
	}
	return f.checkouts[len(f.checkouts)-1]
}

func (f *fakeProvider) lastPortal() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.portals) == 0 {
		return ""
	}
	return f.portals[len(f.portals)-1]
}

// testEvent is the fake provider's wire format.
type testEvent struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	At             time.Time `json:"at"`
	UserID         string    `json:"user_id,omitempty"`
	CustomerID     string    `json:"customer_id,omitempty"`
	SubscriptionID string    `json:"subscription_id,omitempty"`
	Plan           string    `json:"plan,omitempty"`
	Status         string    `json:"status,omitempty"`
	Interval       string    `json:"interval,omitempty"`
	PeriodEnd      time.Time `json:"period_end,omitempty"`
	InvoiceID      string    `json:"invoice_id,omitempty"`
	AmountCents    int64     `json:"amount_cents,omitempty"`
	Currency       string    `json:"currency,omitempty"`
	DisputeID      string    `json:"dispute_id,omitempty"`
}

func (*fakeProvider) ParseWebhook(_ context.Context, payload []byte, h http.Header) (billingpkg.Event, error) {
	if !hmac.Equal([]byte(h.Get("X-Test-Signature")), []byte(sign(payload))) {
		return billingpkg.Event{}, billingpkg.ErrBadSignature
	}
	var te testEvent
	if err := json.Unmarshal(payload, &te); err != nil {
		return billingpkg.Event{}, err
	}
	return billingpkg.Event{
		ID: te.ID, Type: te.Kind, Kind: billingpkg.EventKind(te.Kind), At: te.At,
		UserID: te.UserID, CustomerID: te.CustomerID, SubscriptionID: te.SubscriptionID,
		Plan: te.Plan, PriceID: "price_" + te.Plan, Interval: te.Interval, Status: te.Status, PeriodEnd: te.PeriodEnd,
		InvoiceID: te.InvoiceID, AmountCents: te.AmountCents, Currency: te.Currency, DisputeID: te.DisputeID,
		Payload: payload,
	}, nil
}

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// signedEvent builds a fake-provider event and its signature.
func signedEvent(ev testEvent) ([]byte, string) {
	body, _ := json.Marshal(ev)
	return body, sign(body)
}

// webhook posts a delivery to /v1/billing/webhook/{provider}.
func (c *client) webhook(provider string, body []byte, sig string) resp {
	req := httptest.NewRequest("POST", "/v1/billing/webhook/"+provider, bytes.NewReader(body))
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("X-Test-Signature", sig)
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
