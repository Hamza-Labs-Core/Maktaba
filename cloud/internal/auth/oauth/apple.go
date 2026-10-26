package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// AppleConfig is the runtime configuration loaded from cloud.toml.
type AppleConfig struct {
	TeamID      string
	KeyID       string
	ClientID    string // the Services ID for web sign-in
	KeyPath     string
	RedirectURL string

	// Provider endpoints; empty means Apple's.
	AuthEndpoint  string
	TokenEndpoint string
	JWKSURL       string
}

// Apple's published endpoints.
const (
	appleAuth   = "https://appleid.apple.com/auth/authorize"
	appleToken  = "https://appleid.apple.com/auth/token"
	appleJWKS   = "https://appleid.apple.com/auth/keys"
	appleIssuer = "https://appleid.apple.com"
)

// AppleFlow implements Sign in with Apple for the web. The notable
// difference from Google is the `client_secret`, which we generate per
// request: a short-lived ES256 JWT signed with the key Apple issued.
// Apple posts the callback (response_mode=form_post) cross-site, which
// is why the handler's state cookies are SameSite=None.
type AppleFlow struct {
	Config   AppleConfig
	Verifier *IDTokenVerifier
	HTTP     *http.Client
	Now      func() time.Time
}

// NewAppleFlow fills the default endpoints and builds the verifier.
func NewAppleFlow(cfg AppleConfig) *AppleFlow {
	if cfg.AuthEndpoint == "" {
		cfg.AuthEndpoint = appleAuth
	}
	if cfg.TokenEndpoint == "" {
		cfg.TokenEndpoint = appleToken
	}
	if cfg.JWKSURL == "" {
		cfg.JWKSURL = appleJWKS
	}
	hc := &http.Client{Timeout: 10 * time.Second}
	return &AppleFlow{
		Config: cfg,
		HTTP:   hc,
		Now:    func() time.Time { return time.Now().UTC() },
		Verifier: &IDTokenVerifier{
			JWKSURL:  cfg.JWKSURL,
			Issuers:  []string{appleIssuer},
			Audience: cfg.ClientID,
			HTTP:     hc,
		},
	}
}

// AuthURL is where the browser goes to sign in.
func (a *AppleFlow) AuthURL(state, nonce string) string {
	q := url.Values{}
	q.Set("client_id", a.Config.ClientID)
	q.Set("redirect_uri", a.Config.RedirectURL)
	q.Set("response_type", "code")
	q.Set("response_mode", "form_post")
	q.Set("scope", "name email")
	q.Set("state", state)
	q.Set("nonce", nonce)
	return a.Config.AuthEndpoint + "?" + q.Encode()
}

// Exchange swaps the authorization code for an id_token and returns the
// identity it asserts, after verifying it against Apple's keys.
func (a *AppleFlow) Exchange(ctx context.Context, code, nonce string) (Identity, error) {
	clientSecret, err := a.clientSecret()
	if err != nil {
		return Identity{}, fmt.Errorf("apple oauth: client_secret: %w", err)
	}
	form := url.Values{}
	form.Set("client_id", a.Config.ClientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", a.Config.RedirectURL)
	idToken, err := postForIDToken(ctx, a.HTTP, a.Config.TokenEndpoint, form)
	if err != nil {
		return Identity{}, fmt.Errorf("apple oauth: %w", err)
	}
	return a.Verifier.Verify(ctx, idToken, nonce)
}

// AppleUserName reads the display name from the `user` form field Apple
// posts on the first sign-in only (it is not in the id_token).
func AppleUserName(userField string) string {
	var u struct {
		Name struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
		} `json:"name"`
	}
	if json.Unmarshal([]byte(userField), &u) != nil {
		return ""
	}
	return strings.TrimSpace(u.Name.FirstName + " " + u.Name.LastName)
}

// clientSecret returns a short-lived ES256-signed JWT. We do not import
// jwt-go: the payload is tiny and constant-shape, so we sign by hand.
func (a *AppleFlow) clientSecret() (string, error) {
	keyPEM, err := os.ReadFile(a.Config.KeyPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return "", errors.New("apple oauth: invalid PEM key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("apple oauth: key is not ECDSA")
	}
	return signES256(ec, a.Config.TeamID, a.Config.KeyID, a.Config.ClientID, a.Now())
}
