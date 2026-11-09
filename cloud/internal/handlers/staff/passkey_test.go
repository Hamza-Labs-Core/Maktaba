package staff

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A passkey keeps a trimmed name of at most MaxPasskeyName characters (counted as
// characters, so an Arabic name is not cut mid-letter), "Passkey" when none is given.
func TestPasskeyName(t *testing.T) {
	long := strings.Repeat("م", MaxPasskeyName+5)
	for in, want := range map[string]string{
		"":            "Passkey",
		"   ":         "Passkey",
		"  Laptop  ":  "Laptop",
		"مفتاح العمل": "مفتاح العمل",
		long:          strings.Repeat("م", MaxPasskeyName),
	} {
		if got := passkeyName(in); got != want {
			t.Errorf("passkeyName(%q) = %q, want %q", in, got, want)
		}
	}
}

// With no relying party (no admin listener) every passkey route is 404 passkeys_off, before
// it reads its body.
func TestPasskeysOff(t *testing.T) {
	d := &Deps{}
	for name, h := range map[string]http.HandlerFunc{
		"mfa begin": d.MFAPasskeyBegin, "mfa finish": d.MFAPasskeyFinish,
		"reauth begin": d.ReauthPasskeyBegin, "reauth finish": d.ReauthPasskeyFinish,
		"enroll begin": d.EnrollPasskeyBegin, "enroll finish": d.EnrollPasskeyFinish,
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "passkeys_off") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
