// Package httpsec ships the transport-security middleware for the
// Maktaba API (Story 10.15).
//
// The package is intentionally framework-light: each middleware is an
// `http.Handler -> http.Handler` so they compose with both `http.ServeMux`
// and any router we adopt later. They are also independent — Story
// 10.15 lists HSTS, CORS, security headers, and secure cookies as
// distinct ACs, and we keep them split here so an operator can disable
// one without losing the others.
package httpsec

import (
	"net/http"
	"strings"
)

// HeadersConfig is the static set of response headers added by the
// `Headers` middleware. Defaults match Story 10.15 AC-5; operators
// override CSP via the Settings.CSP field when the SPA evolves.
type HeadersConfig struct {
	// HSTS is the Strict-Transport-Security value. Empty string ⇒ the
	// header is not emitted (e.g. `.local` setups without a trusted
	// cert; AC-2). When non-empty, the value is sent on every
	// response.
	HSTS string

	// CSP is the Content-Security-Policy value applied to the SPA
	// shell. Empty string ⇒ omit. We do not try to compute a per-route
	// CSP — the SPA serves a single `index.html` and a unique CSP per
	// asset request would do nothing useful.
	CSP string

	// Referrer is the Referrer-Policy. Defaults below.
	Referrer string

	// COOP is the Cross-Origin-Opener-Policy value.
	COOP string

	// XContentType is the X-Content-Type-Options value (typically
	// "nosniff").
	XContentType string
}

// DefaultHeaders returns the production-default header set described
// in Story 10.15 AC-5. The HSTS field is left empty by default; the
// caller must set it explicitly so an operator running a `.local`
// install can opt out.
func DefaultHeaders() HeadersConfig {
	return HeadersConfig{
		CSP:          "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'",
		Referrer:     "strict-origin-when-cross-origin",
		COOP:         "same-origin",
		XContentType: "nosniff",
	}
}

// Headers returns a middleware that stamps every response with the
// configured security headers. Existing values from upstream
// middleware are preserved (Set is only called when the slot is empty)
// so a per-route handler can override CSP if it really needs to.
func Headers(cfg HeadersConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if cfg.HSTS != "" && h.Get("Strict-Transport-Security") == "" {
				h.Set("Strict-Transport-Security", cfg.HSTS)
			}
			if cfg.CSP != "" && h.Get("Content-Security-Policy") == "" {
				h.Set("Content-Security-Policy", cfg.CSP)
			}
			if cfg.Referrer != "" && h.Get("Referrer-Policy") == "" {
				h.Set("Referrer-Policy", cfg.Referrer)
			}
			if cfg.COOP != "" && h.Get("Cross-Origin-Opener-Policy") == "" {
				h.Set("Cross-Origin-Opener-Policy", cfg.COOP)
			}
			if cfg.XContentType != "" && h.Get("X-Content-Type-Options") == "" {
				h.Set("X-Content-Type-Options", cfg.XContentType)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HSTSOneYear is the canonical max-age=31536000; includeSubDomains
// value (AC-2). Exposed as a constant so callers don't have to know
// the magic number.
const HSTSOneYear = "max-age=31536000; includeSubDomains"

// ParseAllowedOrigins splits a comma-separated origin list (the format
// `[server].cors_allowed_origins` is loaded as) into the canonical
// trimmed list. Empty origins are dropped.
func ParseAllowedOrigins(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
