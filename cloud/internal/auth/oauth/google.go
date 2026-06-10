// Package oauth implements the third-party identity flows: Google
// (OIDC) and Apple (Sign in with Apple).
//
// Both flows reduce to the same shape: get an `id_token`, verify its
// signature against the provider's JWKS, extract a stable `subject`,
// and federate that to a user row via stores.Users.LinkOAuth.
package oauth

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

// GoogleVerifier fetches Google's JWKS and verifies an id_token.
// In tests we replace it with a stub that returns a fixed identity.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (Identity, error)
}

// Identity is the trimmed-down claim set we care about.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	AvatarURL     string
}

// GoogleConfig is the runtime configuration loaded from cloud.toml.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// GoogleFlow encapsulates the OAuth-code/PKCE flow. The flow operates
// in two stages:
//  1. AuthURL — returned to the browser to begin the dance.
//  2. Exchange — server-side, swaps the code for tokens, then verifies
//     the id_token via GoogleVerifier and returns Identity.
type GoogleFlow struct {
	Config   GoogleConfig
	Verifier GoogleVerifier
	HTTP     *http.Client
}

func NewGoogleFlow(cfg GoogleConfig, v GoogleVerifier) *GoogleFlow {
	return &GoogleFlow{Config: cfg, Verifier: v, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

// AuthURL builds the URL the SPA redirects to. `state` is the CSRF
// nonce the caller stores in a short-lived cookie; we echo it back
// on the redirect so a replay against another browser fails.
func (g *GoogleFlow) AuthURL(state string) string {
	q := url.Values{}
	q.Set("client_id", g.Config.ClientID)
	q.Set("redirect_uri", g.Config.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("access_type", "online")
	q.Set("state", state)
	return "https://accounts.google.com/o/oauth2/v2/auth?" + q.Encode()
}

// Exchange swaps the code for a token bundle.
func (g *GoogleFlow) Exchange(ctx context.Context, code string) (Identity, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", g.Config.ClientID)
	form.Set("client_secret", g.Config.ClientSecret)
	form.Set("redirect_uri", g.Config.RedirectURL)
	form.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("google oauth: token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return Identity{}, fmt.Errorf("google oauth: token endpoint %d: %s", resp.StatusCode, string(body))
	}
	var tr struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return Identity{}, err
	}
	if tr.IDToken == "" {
		return Identity{}, errors.New("google oauth: empty id_token")
	}
	// Guard against a missing verifier: production wiring must supply a
	// JWKS-backed GoogleVerifier. Without it we cannot trust the id_token,
	// so fail closed with a clear error instead of panicking on a nil
	// interface call.
	if g.Verifier == nil {
		return Identity{}, errors.New("google oauth: id_token verifier not configured")
	}
	return g.Verifier.Verify(ctx, tr.IDToken)
}
