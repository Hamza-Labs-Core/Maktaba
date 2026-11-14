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
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// The account-side integration suite drives the api role's real router
// (newAPI) against a scratch Postgres database, with fakes for every
// external party: a recording mailer, a fake OpenID provider standing in
// for Google and Apple, and (newPaidEnv) a fake payment provider. Only
// test values are used — nothing reaches a real provider. newEnv runs
// the production default payment provider, "none".

const (
	testTokenSecret = "integration-test-secret-at-least-32-bytes!"
	// the console's staff keys (story 50.33): a different token secret, and a 32-byte key
	testStaffTokenSecret = "integration-staff-secret-at-least-32-bytes"
	testStaffSecretKey   = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	testWebhookSecret    = "test-webhook-secret"
	testWebURL           = "https://app.example.test"
)

type env struct {
	t       *testing.T
	h       http.Handler
	router  *chi.Mux // the user listener's router (story 50.32)
	admin   *chi.Mux // the operator console's listener's router
	pool    *db.Pool
	mail    *mail.Recorder
	pay     *fakeProvider // nil: the "none" provider
	idp     *fakeIDP
	entPath string
	ipSeq   int
	// logs is everything the api logged (the usage-data test reads it)
	logs *syncBuffer
	// what newAPI was given, for another node on the same database (story 50.17)
	cfg    config.Config
	o      apiOverrides
	mig    *db.Migrator
	logger *slog.Logger
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
	return newEnvFull(t, pay, nil, nil)
}

// newEnvOpts is newEnvWith with the auth pages' bot check on, checked by ts (story 50.30).
func newEnvOpts(t *testing.T, pay *fakeProvider, ts turnstile.Verifier) *env {
	t.Helper()
	return newEnvFull(t, pay, ts, nil)
}

// newEnvCfg is newEnvWith with a hook that adjusts the config before it is validated (50.31).
func newEnvCfg(t *testing.T, pay *fakeProvider, adjust func(*config.Config)) *env {
	t.Helper()
	return newEnvFull(t, pay, nil, adjust)
}

func newEnvFull(t *testing.T, pay *fakeProvider, ts turnstile.Verifier, adjust func(*config.Config)) *env {
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
	cfg.Server.AdminListenAddr = "127.0.0.1:0"
	cfg.Server.AdminHost = testAdminHost
	cfg.Auth.TokenSecret = testTokenSecret
	cfg.Auth.StaffTokenSecret = testStaffTokenSecret
	cfg.Auth.StaffSecretKey = testStaffSecretKey
	cfg.Mail.Driver = "log"
	cfg.Billing.Provider = "none"
	cfg.OAuthGoogle.ClientID = "google-test-client"
	cfg.OAuthGoogle.ClientSecret = "google-test-secret"
	cfg.Entitlement.PrivateKeyPath = e.entPath
	if ts != nil {
		cfg.Turnstile.Enabled = true
		cfg.Turnstile.Secret = "test-turnstile-secret"
		cfg.Turnstile.SiteKey = "test-site-key"
		cfg.Turnstile.WebOrigins = []string{strings.TrimSuffix(testWebURL, "/")}
	}
	if adjust != nil {
		adjust(&cfg)
	}
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
	o.Turnstile = ts
	a, stop, err := newAPI(logger, cfg, pool, mig, server.BuildInfo{Role: "api"}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	e.router, e.admin = a.User, a.Admin
	e.h = consoleOrUser(a)
	e.cfg, e.o, e.mig, e.logger = cfg, o, mig, logger
	return e
}

// node is another api process on e's database (the api pool, story 50.17): its own routers,
// limiters and goroutines, the same rows.
func (e *env) node() http.Handler {
	e.t.Helper()
	a, stop, err := newAPI(e.logger, e.cfg, e.pool, e.mig, server.BuildInfo{Role: "api"}, e.o)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(stop)
	return consoleOrUser(a)
}

// testAdminHost is the console's name in these tests (server.admin_host).
const testAdminHost = "admin.example.test"

// consoleOrUser stands in for the two hostnames in front of the api's two listeners (story
// 50.32): an admin path goes to the admin router as the console sends it (its Host, its
// Origin, X-Maktaba-Staff), anything else to the user router. The listeners' own rules are
// tested on e.router and e.admin directly.
func consoleOrUser(a apiRouters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !server.IsAdminPath(r.URL.Path) {
			a.User.ServeHTTP(w, r)
			return
		}
		r = r.Clone(r.Context())
		r.Host = testAdminHost
		if r.Header.Get("Origin") == "" {
			r.Header.Set("Origin", "https://"+testAdminHost)
		}
		if r.Header.Get(cmw.StaffHeader) == "" {
			r.Header.Set(cmw.StaffHeader, "1")
		}
		a.Admin.ServeHTTP(w, r)
	})
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
	header  map[string]string // extra request headers
	// origin is sent as Origin (a web page's request; empty: a native app)
	origin string
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
	for k, v := range c.header {
		req.Header.Set(k, v)
	}
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
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

// serviceAdmin is a signed-in console operator (story 50.33): a staff account added from the
// CLI with service_admin, enrolled with a password and a TOTP app, signed in with both. Its
// session's MFA check is fresh, so the sensitive routes answer too for the next 5 minutes.
func (e *env) serviceAdmin(email string) *client {
	e.t.Helper()
	return e.staff(email, stores.StaffRoleServiceAdmin)
}

// staffAccount is a test's enrolled staff account: its TOTP secret mints the codes.
type staffAccount struct {
	id, email, secret string
}

// enrolStaff adds a staff account from the CLI (with role, or none for "") and enrols it
// through the console's routes.
func (e *env) enrolStaff(email, role string) staffAccount {
	e.t.Helper()
	ctx := context.Background()
	st := &stores.Staff{DB: e.pool.DB}
	var link string
	if role == "" {
		a, err := st.Create(ctx, email, nil)
		if err != nil {
			e.t.Fatal(err)
		}
		raw, _ := staffauth.NewRawToken()
		if err := st.NewEnrolment(ctx, a.ID, staffauth.TokenHash(raw), time.Now().Add(time.Hour)); err != nil {
			e.t.Fatal(err)
		}
		link = raw
	} else {
		var out bytes.Buffer
		if err := e.staffCLI(&out).run(ctx, "add", email, role); err != nil {
			e.t.Fatal(err)
		}
		_, link, _ = strings.Cut(out.String(), "/enroll#")
		link = strings.TrimSpace(link)
	}
	c := e.client()
	st0 := c.mustStatus(c.do("POST", "/v1/staff/enroll/start", map[string]string{"token": link}), http.StatusOK).json(e.t)
	secret, _ := st0["totp_secret"].(string)
	acct := staffAccount{email: email, secret: secret}
	a, err := st.ByEmail(ctx, email)
	if err != nil {
		e.t.Fatal(err)
	}
	acct.id = a.ID
	c.mustStatus(c.do("POST", "/v1/staff/enroll/finish", map[string]string{
		"token": link, "password": goodStaffPassword, "code": e.totp(acct)}), http.StatusOK)
	return acct
}

// goodStaffPassword passes the staff password rule (14 to 256 characters, not breached).
const goodStaffPassword = "correct-horse-battery-staple-9"

// totp is a code for acct's factor. Each code's step is good once, so the test forgets the
// last one used first: a test signs in and re-checks faster than a step goes by.
func (e *env) totp(acct staffAccount) string {
	e.t.Helper()
	e.exec(`UPDATE staff_totp SET last_step = 0 WHERE staff_id = $1`, acct.id)
	code, err := staffauth.TOTPCode(acct.secret, time.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return code
}

// staffSignIn signs acct in on a new client: the password, then a TOTP code. The client
// carries the staff access token and the staff cookie.
func (e *env) staffSignIn(acct staffAccount) *client {
	e.t.Helper()
	c := e.client()
	r := c.mustStatus(c.do("POST", "/v1/staff/login", map[string]string{"email": acct.email, "password": goodStaffPassword}), http.StatusOK).json(e.t)
	mfa, _ := r["mfa_token"].(string)
	s := c.mustStatus(c.do("POST", "/v1/staff/mfa/totp", map[string]string{"mfa_token": mfa, "code": e.totp(acct)}), http.StatusOK).json(e.t)
	c.token, _ = s["access_token"].(string)
	return c
}

// staff is a signed-in staff account holding role ("" for none).
func (e *env) staff(email, role string) *client {
	e.t.Helper()
	return e.staffSignIn(e.enrolStaff(email, role))
}

// staffCLI is `maktaba-cloud staff` against the test's database.
func (e *env) staffCLI(out io.Writer) staffCmd {
	return staffCmd{Staff: &stores.Staff{DB: e.pool.DB}, Audit: auditLogFor(e), Out: out,
		EnrolURL: "https://" + testAdminHost + "/enroll#", Now: time.Now}
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
	// items 2 and 3 and usage reports (story 50.19): the billing package's fake
	billing billingpkg.Fake
}

func (p *fakeProvider) ReportUsage(ctx context.Context, r billingpkg.UsageReport) error {
	return p.billing.ReportUsage(ctx, r)
}

func (p *fakeProvider) SetItems(ctx context.Context, sub string, payg bool, extra int) error {
	return p.billing.SetItems(ctx, sub, payg, extra)
}

func (p *fakeProvider) ReportedUsage(ctx context.Context, sub string, month time.Time) (int64, error) {
	return p.billing.ReportedUsage(ctx, sub, month)
}

func (p *fakeProvider) SetBroadcastItems(ctx context.Context, sub string, it billingpkg.BroadcastItems) error {
	return p.billing.SetBroadcastItems(ctx, sub, it)
}

func (p *fakeProvider) ReportBroadcastUsage(ctx context.Context, r billingpkg.BroadcastUsageReport) error {
	return p.billing.ReportBroadcastUsage(ctx, r)
}

func (p *fakeProvider) ReportedBroadcastUsage(ctx context.Context, sub, server string, month time.Time) (int64, error) {
	return p.billing.ReportedBroadcastUsage(ctx, sub, server, month)
}

func (*fakeProvider) Name() string  { return "testpay" }
func (*fakeProvider) Enabled() bool { return true }
func (*fakeProvider) Sells(plan string) bool {
	return plan == billingpkg.PlanBasic || plan == billingpkg.PlanPro
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
	ExtraStreams   int       `json:"extra_streams,omitempty"`
	PAYG           bool      `json:"payg,omitempty"`
	// story 50.20: the broadcast items per home
	Broadcast []billingpkg.BroadcastItems `json:"broadcast,omitempty"`
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
		ExtraStreams: te.ExtraStreams, PAYG: te.PAYG, Broadcast: te.Broadcast,
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

// withPrices sets the paid plans' prices for the test (story 50.19: they are TBD until the
// owner sets them, and a plan without a price can't be bought), as they will be after launch.
func withPrices(t *testing.T) {
	t.Helper()
	old := billingpkg.Tiers
	set := map[string]billingpkg.Tier{}
	for k, v := range old {
		set[k] = v
	}
	basic, pro := set[billingpkg.PlanBasic], set[billingpkg.PlanPro]
	p500, p900 := 500, 900
	basic.PriceCents, pro.PriceCents = &p500, &p900
	set[billingpkg.PlanBasic], set[billingpkg.PlanPro] = basic, pro
	billingpkg.Tiers = set
	t.Cleanup(func() { billingpkg.Tiers = old })
}
