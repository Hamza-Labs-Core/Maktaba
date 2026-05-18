package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/auth"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

type fixture struct {
	priv     *rsa.PrivateKey
	jwks     *auth.JWKSCache
	verifier *auth.Verifier
	sess     uuid.UUID
	video    uuid.UUID
	user     uuid.UUID
	libA     uuid.UUID
	libB     uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa: %v", err)
	}
	jwks, err := auth.NewJWKSCache(context.Background(), "", time.Minute)
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	jwks.Set(map[string]*rsa.PublicKey{"k1": &priv.PublicKey})
	v := &auth.Verifier{JWKS: jwks, Leeway: time.Minute, Now: time.Now}
	return &fixture{
		priv:     priv,
		jwks:     jwks,
		verifier: v,
		sess:     uuid.New(),
		video:    uuid.New(),
		user:     uuid.New(),
		libA:     uuid.New(),
		libB:     uuid.New(),
	}
}

func (f *fixture) mint(t *testing.T, claims auth.Claims) string {
	t.Helper()
	if claims.Exp == 0 {
		claims.Exp = time.Now().Add(15 * time.Minute).Unix()
	}
	if claims.Iat == 0 {
		claims.Iat = time.Now().Unix()
	}
	tok, err := auth.Sign(&claims, f.priv, "k1")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func runMiddleware(t *testing.T, mw func(http.Handler) http.Handler, method, target string, headers map[string]string, sub string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.With(mw).Method(method, "/stream/{sub}/foo", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	r.With(mw).Method(method, "/stream/direct/{sub}", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	_ = sub
	return rec
}

func TestSignedURL_Missing(t *testing.T) {
	f := newFixture(t)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo", nil, f.sess.String())
	if rec.Code != 401 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "signed-url-missing") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSignedURL_BadSignature(t *testing.T) {
	f := newFixture(t)
	// Sign with a different key.
	otherPriv, _ := rsa.GenerateKey(rand.Reader, 2048)
	claims := auth.Claims{Aud: "streaming", Sub: f.sess.String(), Lib: []string{f.libA.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok, _ := auth.Sign(&claims, otherPriv, "k1")
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if rec.Code != 401 {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "signed-url-bad-signature") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

// Story 23.2 AC-3: an expired (but well-formed, signature-valid)
// signed-URL token must produce a clear 403, NOT 401, so the player
// can distinguish "re-mint the URL" from "log in again".
func TestSignedURL_Expired(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming", Sub: f.sess.String(), Lib: []string{f.libA.String()}, Exp: time.Now().Add(-10 * time.Minute).Unix()}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if rec.Code != 403 {
		t.Fatalf("expired status=%d want 403 (AC-3: not 401)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "signed-url-expired") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSignedURL_ExpiredWithinLeeway(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{
		Aud: "streaming",
		Sub: f.sess.String(),
		Lib: []string{f.libA.String()},
		Exp: time.Now().Add(-30 * time.Second).Unix(),
	}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s — leeway=60s should accept exp-30s", rec.Code, rec.Body.String())
	}
}

func TestSignedURL_WrongAud(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming-direct", Sub: f.sess.String(), Lib: []string{f.libA.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if !strings.Contains(rec.Body.String(), "signed-url-wrong-aud") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSignedURL_WrongSub(t *testing.T) {
	f := newFixture(t)
	wrong := uuid.New().String()
	claims := auth.Claims{Aud: "streaming", Sub: wrong, Lib: []string{f.libA.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if !strings.Contains(rec.Body.String(), "signed-url-wrong-sub") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSignedURL_AcceptsBearerHeader(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming-direct", Sub: f.video.String(), Lib: []string{f.libA.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudDirect, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/direct/"+f.video.String(),
		map[string]string{"Authorization": "Bearer " + tok}, f.video.String())
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSignedURL_LibClaimMissing(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming", Sub: f.sess.String(), Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)
	mw := auth.SignedURL(f.verifier, auth.AudSession, "sub")
	rec := runMiddleware(t, mw, "GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil, f.sess.String())
	if !strings.Contains(rec.Body.String(), "signed-url-wrong-lib") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestLibraryGuard_AcceptsCovered(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming", Sub: f.sess.String(), Lib: []string{f.libA.String(), f.libB.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)

	resolver := auth.LibraryResolverFunc(func(_ context.Context, _ *http.Request, _ *auth.Claims) (uuid.UUID, error) {
		return f.libA, nil
	})

	r := chi.NewRouter()
	r.With(auth.SignedURL(f.verifier, auth.AudSession, "sub"), auth.LibraryGuard(resolver)).
		Get("/stream/{sub}/foo", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	req := httptest.NewRequest("GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLibraryGuard_RejectsForeign(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{Aud: "streaming", Sub: f.sess.String(), Lib: []string{f.libA.String()}, Exp: time.Now().Add(time.Hour).Unix()}
	tok := f.mint(t, claims)

	resolver := auth.LibraryResolverFunc(func(_ context.Context, _ *http.Request, _ *auth.Claims) (uuid.UUID, error) {
		return f.libB, nil
	})

	r := chi.NewRouter()
	r.With(auth.SignedURL(f.verifier, auth.AudSession, "sub"), auth.LibraryGuard(resolver)).
		Get("/stream/{sub}/foo", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	req := httptest.NewRequest("GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "signed-url-wrong-lib") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestSignedURL_ContextCarriesClaims(t *testing.T) {
	f := newFixture(t)
	claims := auth.Claims{
		Aud: "streaming", Sub: f.sess.String(), Usr: f.user.String(),
		Lib: []string{f.libA.String()},
		Exp: time.Now().Add(time.Hour).Unix(),
	}
	tok := f.mint(t, claims)

	var seen *auth.Claims
	r := chi.NewRouter()
	r.With(auth.SignedURL(f.verifier, auth.AudSession, "sub")).
		Get("/stream/{sub}/foo", func(w http.ResponseWriter, req *http.Request) {
			c, _ := auth.ClaimsFromContext(req.Context())
			seen = c
			w.WriteHeader(200)
		})

	req := httptest.NewRequest("GET", "/stream/"+f.sess.String()+"/foo?sig="+tok, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	if seen == nil {
		t.Fatal("ctx claims missing")
	}
	if seen.Sub != f.sess.String() || seen.Usr != f.user.String() {
		t.Fatalf("seen=%+v", seen)
	}
}

func TestProblemBodyShape(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.WriteSignedURLError(rec, httpx.SignedURLMissing)
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("json: %v", err)
	}
	if p.Status != 401 {
		t.Fatalf("p=%+v", p)
	}
}
