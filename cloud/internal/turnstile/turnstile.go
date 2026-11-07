// Package turnstile is the bot check on the cloud's auth pages (Epic 50, story 50.30 §4): the
// web sign-in, register, forgot-password and staff sign-in pages render a Cloudflare Turnstile
// widget, and the API verifies its token server-side with Cloudflare's siteverify before the
// route's normal checks.
//
// Only requests carrying one of the cloud's web origins (Origin header) are asked for a token:
// native apps have no Turnstile SDK and send no Origin; they still meet the edge's rate limit,
// the per-IP buckets and the account lockout. A failed, missing, foreign or reused token is
// 403 turnstile_failed; siteverify unreachable (3 s) is 503 turnstile_unavailable with
// Retry-After, fail closed, never a silent pass.
package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SiteverifyURL is Cloudflare's verification endpoint.
const SiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Timeout is how long the API waits for siteverify.
const Timeout = 3 * time.Second

// Codes the API answers with (web/cloud/src/i18n: auth.turnstile.*).
const (
	CodeFailed      = "turnstile_failed"
	CodeUnavailable = "turnstile_unavailable"
)

// ErrFailed: the token did not verify (missing, wrong, reused, another site's).
var ErrFailed = errors.New(CodeFailed)

// ErrUnavailable: siteverify could not be asked.
var ErrUnavailable = errors.New(CodeUnavailable)

// Verifier checks one token (the fake in tests implements it too).
type Verifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// Client verifies tokens with siteverify.
type Client struct {
	Secret string
	// Hostnames are the hostnames a token may have been issued on (the cloud's web origins).
	Hostnames []string
	// URL is siteverify's address (tests point it at a fake).
	URL  string
	HTTP *http.Client
}

type siteverifyAnswer struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	ErrorCodes []string `json:"error-codes"`
}

// Verify asks siteverify about token. A token is good once: siteverify answers a reused one
// with timeout-or-duplicate.
func (c *Client) Verify(ctx context.Context, token, remoteIP string) error {
	if strings.TrimSpace(token) == "" || len(token) > 2048 {
		return ErrFailed
	}
	u := c.URL
	if u == "" {
		u = SiteverifyURL
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: Timeout}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	form := url.Values{"secret": {c.Secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: siteverify answered %d", ErrUnavailable, resp.StatusCode)
	}
	var a siteverifyAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&a); err != nil {
		return fmt.Errorf("%w: siteverify's answer: %v", ErrUnavailable, err)
	}
	if !a.Success {
		return ErrFailed
	}
	if len(c.Hostnames) > 0 && !contains(c.Hostnames, a.Hostname) {
		return ErrFailed
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// Guard decides, per request, whether a token is needed and whether it verified. A nil Guard
// or one not Enabled lets every request through (dev and CI).
type Guard struct {
	Enabled  bool
	Verifier Verifier
	// WebOrigins are the cloud's web origins ("https://app.maktaba.tv"): only requests whose
	// Origin is one of them are asked for a token.
	WebOrigins []string
	// ClientIP is the request's client address (middleware.GetClientIP), sent as remoteip.
	ClientIP func(*http.Request) string
}

// Needed reports whether the request must carry a token: the guard is on and the request
// comes from one of the cloud's web origins.
func (g *Guard) Needed(r *http.Request) bool {
	if g == nil || !g.Enabled {
		return false
	}
	return contains(g.WebOrigins, strings.TrimSuffix(r.Header.Get("Origin"), "/"))
}

// Check verifies token for r when one is needed. It answers nil when the request may go on,
// ErrFailed or ErrUnavailable otherwise.
func (g *Guard) Check(r *http.Request, token string) error {
	if !g.Needed(r) {
		return nil
	}
	ip := ""
	if g.ClientIP != nil {
		ip = g.ClientIP(r)
	}
	err := g.Verifier.Verify(r.Context(), token, ip)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrUnavailable):
		return ErrUnavailable
	default:
		return ErrFailed
	}
}

// Require is Check for a handler: on a refusal it writes the answer (403 turnstile_failed, or
// 503 turnstile_unavailable with Retry-After: 30) and returns false. The staff sign-in route
// (story 50.33) calls it the same way.
func (g *Guard) Require(w http.ResponseWriter, r *http.Request, token string) bool {
	err := g.Check(r, token)
	if err == nil {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	if errors.Is(err, ErrUnavailable) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"` + CodeUnavailable + `","message":"sign-in is briefly unavailable; try again in a minute"}`))
		return false
	}
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"` + CodeFailed + `","message":"the check that you are a person failed; reload the page and try again"}`))
	return false
}
