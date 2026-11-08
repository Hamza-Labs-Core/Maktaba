package staff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
)

// §1: a staff password is 14 to 256 characters and passes the breached-password check.
func TestPasswordProblem(t *testing.T) {
	for pw, want := range map[string]string{
		"short-one":                      "password_too_short",
		strings.Repeat("a", 13):          "password_too_short",
		strings.Repeat("a", 257):         "password_too_long",
		" leading-space-pass":            "password_invalid",
		"correct-horse-battery-staple-9": "",
		strings.Repeat("b", 256):         "",
	} {
		if got := PasswordProblem(pw); got != want {
			t.Errorf("PasswordProblem(%d chars) = %q, want %q", len(pw), got, want)
		}
	}
}

// The staff cookie is host-only, HttpOnly, Secure and Strict, and lives to the session's cap.
func TestStaffCookie(t *testing.T) {
	d := &Deps{Secure: true}
	rec := httptest.NewRecorder()
	at := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	d.setCookie(rec, "raw", at.Add(staffauth.SessionCap))
	c := rec.Result().Cookies()[0]
	if c.Name != Cookie || !strings.HasPrefix(c.Name, "__Host-") || c.Domain != "" || c.Path != "/" ||
		!c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || !c.Expires.Equal(at.Add(8*time.Hour)) {
		t.Fatalf("cookie: %+v", c)
	}
	rec = httptest.NewRecorder()
	d.clearCookie(rec)
	if c := rec.Result().Cookies()[0]; c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("cleared cookie: %+v", c)
	}
}

// A locked answer is 423 account_locked with the seconds left in Retry-After.
func TestLocked(t *testing.T) {
	rec := httptest.NewRecorder()
	now := time.Now()
	locked(rec, now.Add(90*time.Second), now)
	if rec.Code != http.StatusLocked || rec.Header().Get("Retry-After") != "91" || !strings.Contains(rec.Body.String(), "account_locked") {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}
