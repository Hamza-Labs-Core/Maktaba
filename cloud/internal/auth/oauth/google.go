// Package oauth implements the third-party identity flows: Google
// (OIDC) and Apple (Sign in with Apple).
//
// Both flows reduce to the same shape: send the browser to the
// provider with a state (CSRF), a nonce (id_token replay) and — for
// Google — a PKCE challenge; on the callback swap the code for an
// id_token, verify it against the provider's JWKS (IDTokenVerifier),
// and hand the handler a stable `subject` to federate to a user row.
//
// Endpoints are fields so tests point the flows at a fake provider;
// production uses the defaults.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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

	// Provider endpoints; empty means Google's.
	AuthEndpoint  string
	TokenEndpoint string
	JWKSURL       string
}

// Google's published endpoints.
const (
	googleAuth   = "https://accounts.google.com/o/oauth2/v2/auth"
	googleToken  = "https://oauth2.googleapis.com/token"
	googleJWKS   = "https://www.googleapis.com/oauth2/v3/certs"
	googleIssuer = "https://accounts.google.com"
)

// GoogleFlow is the OIDC authorization-code flow with PKCE.
type GoogleFlow struct {
	Config   GoogleConfig
	Verifier *IDTokenVerifier
	HTTP     *http.Client
}

// NewGoogleFlow fills the default endpoints and builds the verifier.
func NewGoogleFlow(cfg GoogleConfig) *GoogleFlow {
	if cfg.AuthEndpoint == "" {
		cfg.AuthEndpoint = googleAuth
	}
	if cfg.TokenEndpoint == "" {
		cfg.TokenEndpoint = googleToken
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = googleJWKS
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	return &GoogleFlow{
		Config: cfg,
		HTTP:   hc,
		Verifier: &IDTokenVerifier{
			JWKSURL:  cfg.JWKSURL,
			Issuers:  []string{googleIssuer, "accounts.google.com"},
			Audience: cfg.ClientID,
			HTTP:     hc,
		},
	}
}

// AuthURL is where the browser goes to sign in. state and nonce are the
// values the caller stored in short-lived cookies; codeChallenge is
// PKCEChallenge(verifier) of the verifier it stored.
func (g *GoogleFlow) AuthURL(state, nonce, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", g.Config.ClientID)
	q.Set("redirect_uri", g.Config.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("access_type", "online")
	q.Set("prompt", "select_account")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return g.Config.AuthEndpoint + "?" + q.Encode()
}

// Exchange swaps the code (with the PKCE verifier) for an id_token and
// returns the identity it asserts, after checking it was minted for
// this sign-in (nonce).
func (g *GoogleFlow) Exchange(ctx context.Context, code, codeVerifier, nonce string) (Identity, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", g.Config.ClientID)
	form.Set("client_secret", g.Config.ClientSecret)
	form.Set("redirect_uri", g.Config.RedirectURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", codeVerifier)
	idToken, err := postForIDToken(ctx, g.HTTP, g.Config.TokenEndpoint, form)
	if err != nil {
		return Identity{}, fmt.Errorf("google oauth: %w", err)
	}
	return g.Verifier.Verify(ctx, idToken, nonce)
}

// postForIDToken calls a token endpoint and returns its id_token.
func postForIDToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return "", fmt.Errorf("token endpoint %d: %s", resp.StatusCode, e.Error)
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", err
	}
	if tr.IDToken == "" {
		return "", errors.New("token endpoint returned no id_token")
	}
	return tr.IDToken, nil
}

// RandomToken returns n random bytes, base64url — state, nonce and PKCE
// verifier values.
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// PKCEChallenge is the S256 code challenge of a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
