package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Story 50.30 T04: the verifier against a fake siteverify.
func TestVerify(t *testing.T) {
	used := map[string]bool{}
	var gotIP, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotIP, gotSecret = r.Form.Get("remoteip"), r.Form.Get("secret")
		tok := r.Form.Get("response")
		switch {
		case tok == "slow":
			time.Sleep(4 * time.Second)
		case used[tok]:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error-codes": []string{"timeout-or-duplicate"}})
		case tok == "bad":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}})
		case tok == "elsewhere":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "hostname": "evil.example"})
		default:
			used[tok] = true
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "hostname": "app.maktaba.tv"})
		}
	}))
	defer srv.Close()
	c := &Client{Secret: "s3cret", Hostnames: []string{"app.maktaba.tv", "admin.maktaba.tv"}, URL: srv.URL}
	ctx := context.Background()

	if err := c.Verify(ctx, "good-1", "198.51.100.7"); err != nil {
		t.Fatalf("success: %v", err)
	}
	if gotIP != "198.51.100.7" || gotSecret != "s3cret" {
		t.Fatalf("sent remoteip %q secret %q", gotIP, gotSecret)
	}
	for name, tok := range map[string]string{"success:false": "bad", "another site's": "elsewhere", "reused": "good-1", "missing": ""} {
		if err := c.Verify(ctx, tok, ""); !errors.Is(err, ErrFailed) {
			t.Errorf("%s: %v, want ErrFailed", name, err)
		}
	}
	start := time.Now()
	if err := c.Verify(ctx, "slow", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > Timeout+time.Second {
		t.Fatalf("waited %s", time.Since(start))
	}
	srv.Close()
	if err := (&Client{Secret: "s", URL: srv.URL}).Verify(ctx, "x", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unreachable: %v", err)
	}
}

type fakeVerifier struct {
	err   error
	calls int
}

func (f *fakeVerifier) Verify(context.Context, string, string) error { f.calls++; return f.err }

// A web origin is asked for a token; a native app (no Origin) and another origin are not.
func TestGuard(t *testing.T) {
	f := &fakeVerifier{err: ErrFailed}
	g := &Guard{Enabled: true, Verifier: f, WebOrigins: []string{"https://app.maktaba.tv"}}
	req := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader("{}"))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	w := httptest.NewRecorder()
	if g.Require(w, req("https://app.maktaba.tv"), "tok") || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), CodeFailed) {
		t.Fatalf("web origin, failed token: %d %s", w.Code, w.Body)
	}
	f.err = ErrUnavailable
	w = httptest.NewRecorder()
	if g.Require(w, req("https://app.maktaba.tv/"), "tok") || w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("siteverify down: %d %v", w.Code, w.Header())
	}
	f.calls = 0
	for _, o := range []string{"", "https://evil.example"} {
		if !g.Require(httptest.NewRecorder(), req(o), "") {
			t.Fatalf("origin %q was asked for a token", o)
		}
	}
	if f.calls != 0 {
		t.Fatal("verified a request that needs no token")
	}
	var off *Guard
	if !off.Require(httptest.NewRecorder(), req("https://app.maktaba.tv"), "") {
		t.Fatal("a nil guard refused")
	}
	if !(&Guard{Verifier: f, WebOrigins: g.WebOrigins}).Require(httptest.NewRecorder(), req("https://app.maktaba.tv"), "") {
		t.Fatal("a guard not enabled refused")
	}
}
