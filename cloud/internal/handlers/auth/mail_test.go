package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail/outbox"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

type fakeMail struct {
	asked []string
	last  mail.Ask
	err   error
}

func (f *fakeMail) Request(_ context.Context, a mail.Ask) error {
	f.asked, f.last = append(f.asked, a.Kind+" "+a.To+" "+a.Lang), a
	return f.err
}

func (f *fakeMail) Register(_ context.Context, a mail.Ask, hash, name string) error {
	f.asked, f.last = append(f.asked, "register "+a.To+" "+name+" "+a.Lang), a
	if !strings.HasPrefix(hash, "$argon2id$") {
		return errors.New("not a hash")
	}
	return f.err
}

func post(h http.HandlerFunc, body, lang string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	h(rec, req)
	return rec
}

// The handlers that cause a mail look no account up (there are no stores here to look in): what
// they do, and so what they take, can't differ by whether the address has an account.
func TestTheMailHandlersLookNoAccountUp(t *testing.T) {
	m := &fakeMail{}
	d := &Deps{Mail: m, Logger: slog.New(slog.DiscardHandler)}
	if rec := post(d.ForgotPassword, `{"email":"Someone@Example.test"}`, "ar-EG,ar;q=0.9,en;q=0.8"); rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(d.ResendVerification, `{"email":"someone@example.test"}`, "fr"); rec.Code != 202 || rec.Body.Len() != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(d.Register, `{"email":"someone@example.test","password":"correct horse battery","display_name":"S"}`, "en"); rec.Code != 202 ||
		!strings.Contains(rec.Body.String(), "pending_verification") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := mail.KindPasswordReset + " someone@example.test ar|" + mail.KindVerifyEmail + " someone@example.test |register someone@example.test S en"
	if got := strings.Join(m.asked, "|"); got != want {
		t.Fatalf("%s", got)
	}
	// an address that is none asks nothing, and answers the same
	m.asked = nil
	if rec := post(d.ForgotPassword, `{"email":"nothing"}`, ""); rec.Code != 202 || len(m.asked) != 0 {
		t.Fatalf("%d %v", rec.Code, m.asked)
	}
	// a mail that could not be asked for is not the person's to know
	m.err = errors.New("the database is away")
	for _, h := range []http.HandlerFunc{d.ForgotPassword, d.ResendVerification} {
		if rec := post(h, `{"email":"someone@example.test"}`, ""); rec.Code != 202 {
			t.Fatalf("%d", rec.Code)
		}
	}
	// a sign-up whose account could not be kept is
	if rec := post(d.Register, `{"email":"someone@example.test","password":"correct horse battery"}`, ""); rec.Code != 500 {
		t.Fatalf("%d", rec.Code)
	}
}

// What is asked carries the requester's network, and whether it passed the bot check: only a web
// page's request is asked for a token, and one without a good one asks nothing.
func TestAnAskSaysWhoAsked(t *testing.T) {
	m := &fakeMail{}
	ok := false
	d := &Deps{Mail: m, Logger: slog.New(slog.DiscardHandler), Turnstile: &turnstile.Guard{Enabled: true, WebOrigins: []string{"https://app.example.test"},
		Verifier: verifier(func(token string) error {
			if ok = token == "good"; !ok {
				return turnstile.ErrFailed
			}
			return nil
		})}}
	do := func(h http.HandlerFunc, origin, token string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"a@example.test","password":"correct horse battery","turnstile_token":"`+token+`"}`))
		req.RemoteAddr = "203.0.113.9:1"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		h(rec, req)
		return rec.Code
	}
	for name, h := range map[string]http.HandlerFunc{"register": d.Register, "forgot": d.ForgotPassword, "resend": d.ResendVerification} {
		m.asked = nil
		if c := do(h, "https://app.example.test", "bad"); c != http.StatusForbidden || len(m.asked) != 0 {
			t.Errorf("%s with a bad token: %d %v", name, c, m.asked)
		}
		if c := do(h, "https://app.example.test", "good"); c != 202 || !m.last.Checked || m.last.Net != "net:203.0.113.0/24" {
			t.Errorf("%s with a good token: %d %+v", name, c, m.last)
		}
		if c := do(h, "", "good"); c != 202 || m.last.Checked {
			t.Errorf("%s from no web page is not checked: %d %+v", name, c, m.last)
		}
	}
	// a network that asked too much is told when to come back, and nothing else
	m.err = fmt.Errorf("x: %w", &outbox.TooMany{RetryAfter: 90 * time.Second})
	for _, h := range []http.HandlerFunc{d.Register, d.ForgotPassword, d.ResendVerification} {
		rec := post(h, `{"email":"a@example.test","password":"correct horse battery"}`, "")
		if rec.Code != 429 || rec.Header().Get("Retry-After") != "91" || !strings.Contains(rec.Body.String(), "rate_limited") {
			t.Errorf("%d %s %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
		}
	}
	// a request that asks nothing (no address, a weak password) is not counted
	m.err, m.asked = nil, nil
	post(d.Register, `{"email":"a@example.test","password":"short"}`, "")
	post(d.ForgotPassword, `{"email":"nothing"}`, "")
	post(d.ResendVerification, `not json`, "")
	if len(m.asked) != 0 {
		t.Errorf("%v", m.asked)
	}
}

type verifier func(token string) error

func (v verifier) Verify(_ context.Context, token, _ string) error { return v(token) }

func TestRequestLang(t *testing.T) {
	for h, want := range map[string]string{"": "", "ar": "ar", "en-US,en;q=0.9,ar;q=0.8": "en", "en;q=0.2, ar;q=0.8": "ar",
		"fr-FR,fr;q=0.9,ar;q=0.3": "ar", "fr": "", "*": "", "ar;q=0": "", "ar;q=x,en;q=0.1": "en"} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Accept-Language", h)
		if got := requestLang(req); got != want {
			t.Errorf("%q: %q, want %q", h, got, want)
		}
	}
}
