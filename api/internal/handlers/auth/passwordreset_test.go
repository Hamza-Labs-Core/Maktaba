package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

type fakeRevoker struct {
	calls []string
	n     int64
	err   error
}

func (f *fakeRevoker) RevokeAllCredentials(_ context.Context, userID string) (int64, error) {
	f.calls = append(f.calls, userID)
	return f.n, f.err
}

const targetID = "22222222-2222-2222-2222-222222222222"

func resetCall(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := withChiParam(adminReq(http.MethodPost, "/api/users/"+targetID+"/reset-password", body), "id", targetID)
	rec := httptest.NewRecorder()
	h.AdminResetPassword(rec, r)
	return rec
}

// captureLogs routes slog to a buffer for the test so it can assert that
// no password is ever logged.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

var tempPwRe = regexp.MustCompile(`^[a-km-zA-HJ-NP-Z2-9]{4}(-[a-km-zA-HJ-NP-Z2-9]{4}){3}$`)

func TestAdminResetPassword_Temporary(t *testing.T) {
	logs := captureLogs(t)
	ua := &fakeUserAdmin{}
	rv := &fakeRevoker{n: 3}
	h := &Handler{UserAdmin: ua, Credentials: rv}
	rec := resetCall(t, h, `{"mode":"temporary"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control=%q", cc)
	}
	var resp resetPasswordResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !tempPwRe.MatchString(resp.TemporaryPassword) {
		t.Fatalf("temporary password %q has the wrong shape", resp.TemporaryPassword)
	}
	if !resp.MustChangePassword || resp.SessionsRevoked != 3 || resp.UserID != targetID {
		t.Fatalf("resp=%+v", resp)
	}
	if ua.updateID != targetID || ua.updated == nil || ua.updated.Password == nil ||
		*ua.updated.Password != resp.TemporaryPassword {
		t.Fatalf("store not given the temporary password: %+v", ua.updated)
	}
	if ua.updated.MustChangePassword == nil || !*ua.updated.MustChangePassword {
		t.Fatal("temporary reset must set must_change_password")
	}
	if ua.updated.Username != nil || ua.updated.IsAdmin != nil {
		t.Fatal("reset must not touch username or role")
	}
	if len(rv.calls) != 1 || rv.calls[0] != targetID {
		t.Fatalf("sessions not revoked: %v", rv.calls)
	}
	if strings.Contains(logs.String(), resp.TemporaryPassword) {
		t.Fatal("the temporary password reached the log")
	}
}

func TestAdminResetPassword_Set(t *testing.T) {
	logs := captureLogs(t)
	ua := &fakeUserAdmin{}
	rv := &fakeRevoker{}
	h := &Handler{UserAdmin: ua, Credentials: rv}
	const pw = "a-new-password-42"
	rec := resetCall(t, h, `{"mode":"set","password":"`+pw+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), pw) || strings.Contains(rec.Body.String(), "temporary_password") {
		t.Fatalf("a typed password is never echoed: %s", rec.Body.String())
	}
	if *ua.updated.Password != pw || *ua.updated.MustChangePassword {
		t.Fatalf("update=%+v", ua.updated)
	}
	if len(rv.calls) != 1 {
		t.Fatal("sessions not revoked")
	}
	if strings.Contains(logs.String(), pw) {
		t.Fatal("the password reached the log")
	}

	// must_change can be asked for with a typed password too.
	rec = resetCall(t, h, `{"mode":"set","password":"`+pw+`","must_change":true}`)
	if rec.Code != http.StatusOK || !*ua.updated.MustChangePassword {
		t.Fatalf("status=%d update=%+v", rec.Code, ua.updated)
	}
}

func TestAdminResetPassword_Validation(t *testing.T) {
	for name, body := range map[string]string{
		"short":          `{"mode":"set","password":"short"}`,
		"no mode":        `{"password":"long-enough-1"}`,
		"bad mode":       `{"mode":"email"}`,
		"temp with pass": `{"mode":"temporary","password":"long-enough-1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			ua := &fakeUserAdmin{}
			rv := &fakeRevoker{}
			rec := resetCall(t, &Handler{UserAdmin: ua, Credentials: rv}, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if ua.updated != nil || len(rv.calls) != 0 {
				t.Fatal("a rejected reset must change nothing")
			}
		})
	}
}

func TestAdminResetPassword_NonAdminForbidden(t *testing.T) {
	ua := &fakeUserAdmin{}
	rv := &fakeRevoker{}
	h := &Handler{UserAdmin: ua, Credentials: rv}
	r := httptest.NewRequest(http.MethodPost, "/api/users/"+targetID+"/reset-password",
		strings.NewReader(`{"mode":"temporary"}`))
	r = r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "u1"}))
	r = withChiParam(r, "id", targetID)
	rec := httptest.NewRecorder()
	h.AdminResetPassword(rec, r)
	if rec.Code != http.StatusForbidden || ua.updated != nil || len(rv.calls) != 0 {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestAdminResetPassword_UnknownUser(t *testing.T) {
	rv := &fakeRevoker{}
	h := &Handler{UserAdmin: &fakeUserAdmin{updateErr: users.ErrNotFound}, Credentials: rv}
	rec := resetCall(t, h, `{"mode":"temporary"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	if len(rv.calls) != 0 {
		t.Fatal("nothing to revoke for a missing user")
	}
	if strings.Contains(rec.Body.String(), "temporary_password") {
		t.Fatal("no password on failure")
	}
}

func TestAdminResetPassword_RevokeFailure(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{}, Credentials: &fakeRevoker{err: errors.New("db down")}}
	rec := resetCall(t, h, `{"mode":"temporary"}`)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "temporary_password") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminResetPassword_SentinelRefused(t *testing.T) {
	ua := &fakeUserAdmin{}
	h := &Handler{UserAdmin: ua, Credentials: &fakeRevoker{}}
	r := withChiParam(adminReq(http.MethodPost, "/api/users/x/reset-password", `{"mode":"temporary"}`),
		"id", users.SentinelAdminID)
	rec := httptest.NewRecorder()
	h.AdminResetPassword(rec, r)
	if rec.Code != http.StatusUnprocessableEntity || ua.updated != nil {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestAdminResetPassword_Routed(t *testing.T) {
	h := &Handler{UserAdmin: &fakeUserAdmin{}, Credentials: &fakeRevoker{}}
	if !routeExists(h, http.MethodPost, "/api/users/"+targetID+"/reset-password") {
		t.Fatal("reset route not mounted")
	}
	for _, gone := range []string{"/api/auth/forgot-password", "/api/auth/reset-password"} {
		if routeExists(h, http.MethodPost, gone) {
			t.Fatalf("%s must be gone", gone)
		}
	}
}

func TestTemporaryPassword_Unique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := temporaryPassword()
		if err != nil {
			t.Fatal(err)
		}
		if !tempPwRe.MatchString(p) || seen[p] {
			t.Fatalf("bad or repeated temporary password %q", p)
		}
		seen[p] = true
	}
}

func TestAllowedWhilePasswordChange(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/", true},
		{http.MethodGet, "/assets/index.js", true},
		{http.MethodGet, "/api/auth/me", true},
		{http.MethodPost, "/api/auth/logout", true},
		{http.MethodGet, "/api/system/version", true},
		{http.MethodPost, "/api/me/change-password", true},
		{http.MethodGet, "/api/me", true},
		{http.MethodPatch, "/api/me", false},
		{http.MethodGet, "/api/me/sessions", false},
		{http.MethodGet, "/api/videos", false},
		{http.MethodPost, "/api/users", false},
		{http.MethodGet, "/api/me/change-password", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := allowedWhilePasswordChange(r); got != c.want {
			t.Errorf("%s %s: got %v want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestPasswordChangeRequiredProblem(t *testing.T) {
	e := passwordChangeRequired()
	if e.Status != http.StatusForbidden || e.Type != TypePasswordChangeRequired {
		t.Fatalf("%+v", e)
	}
}

func TestUserResponseCarriesMustChange(t *testing.T) {
	b, _ := json.Marshal(toUserResponse(&users.User{ID: "1", Username: "a", MustChangePassword: true}))
	if !strings.Contains(string(b), `"must_change_password":true`) {
		t.Fatalf("%s", b)
	}
}

func routeExists(h *Handler, method, path string) bool {
	r := chi.NewRouter()
	h.Mount(r)
	return r.Match(chi.NewRouteContext(), method, path)
}
