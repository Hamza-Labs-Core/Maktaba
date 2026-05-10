package httpsec

import (
	"log/slog"
	"net/http"
	"os"
)

// SecureCookie returns a *http.Cookie pre-stamped with the attribute
// set required by Story 10.15 AC-3: Secure, HttpOnly, SameSite=Lax,
// Path=/.
//
// `httpOnly` controls the HttpOnly bit: web-session cookies are
// HttpOnly (the SPA never reads them), but a tiny CSRF cookie has to
// be readable by JS so HttpOnly is off there. The caller passes
// the right value for the use-site rather than us trying to guess
// from the cookie name.
//
// `MAKTABA_DEV=1` (story EC-3 / AC-3) drops the Secure flag *and*
// emits a loud warning to slog so the operator notices when production
// boots in dev mode.
func SecureCookie(name, value string, maxAgeSec int, httpOnly bool, logger *slog.Logger) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAgeSec,
		Secure:   true,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	}
	if os.Getenv("MAKTABA_DEV") == "1" {
		c.Secure = false
		if logger != nil {
			logger.Warn("LOUD: cookie issued without Secure flag because MAKTABA_DEV=1",
				"cookie", name,
				"event", "insecure_cookie_dev",
			)
		}
	}
	return c
}

// ClearCookie returns a deletion cookie with the same attributes as
// SecureCookie but a zero value and MaxAge=-1.
func ClearCookie(name string, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   os.Getenv("MAKTABA_DEV") != "1",
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	}
}
