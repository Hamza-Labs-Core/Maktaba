package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
)

type fakeMail struct {
	asked []string
	err   error
}

func (f *fakeMail) Request(_ context.Context, kind, addr, lang string) error {
	f.asked = append(f.asked, kind+" "+addr+" "+lang)
	return f.err
}

func (f *fakeMail) Register(_ context.Context, email, hash, name, lang string) error {
	f.asked = append(f.asked, "register "+email+" "+name+" "+lang)
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
