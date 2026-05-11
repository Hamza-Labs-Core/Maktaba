package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
)

// APNsDriver speaks the HTTP/2 APNs production endpoint.
// We use HTTP/1.1 here for simplicity — go's net/http negotiates
// HTTP/2 automatically with the APNs endpoint when TLS ALPN is
// available, which is the case for every recent Go release.
type APNsDriver struct {
	TeamID   string
	KeyID    string
	KeyPath  string
	BundleID string
	HTTP     *http.Client

	mu        sync.Mutex
	cachedJWT string
	cachedAt  time.Time
}

func NewAPNsDriver(teamID, keyID, keyPath, bundleID string) *APNsDriver {
	return &APNsDriver{
		TeamID:   teamID,
		KeyID:    keyID,
		KeyPath:  keyPath,
		BundleID: bundleID,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (a *APNsDriver) Name() string { return "apns" }

// Send pushes a single notification. APNs returns 200 on success; 400+
// signals a problem with either our credentials (rotate key) or the
// device token (mark for cleanup).
func (a *APNsDriver) Send(ctx context.Context, token string, n Notification) error {
	jwt, err := a.providerToken()
	if err != nil {
		return fmt.Errorf("apns: provider token: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": n.Title, "body": n.Body},
			"sound": "default",
		},
		"data": n.Data,
	})
	url := "https://api.push.apple.com/3/device/" + token
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+jwt)
	req.Header.Set("apns-topic", a.BundleID)
	if n.Topic != "" {
		req.Header.Set("apns-collapse-id", n.Topic)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("apns: %d %s", resp.StatusCode, string(body))
}

// providerToken returns the JWT APNs expects in `authorization`. The
// token is valid up to 60 min; we rotate at 45 to leave margin.
func (a *APNsDriver) providerToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cachedJWT != "" && time.Since(a.cachedAt) < 45*time.Minute {
		return a.cachedJWT, nil
	}
	keyPEM, err := os.ReadFile(a.KeyPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return "", errors.New("apns: invalid PEM key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", err
	}
	ec, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("apns: not an ECDSA key")
	}
	// Reuse the JWT signer shared with the Apple OAuth client_secret
	// flow — the shape is intentionally identical.
	jwt, err := signES256ForAPNs(ec, a.TeamID, a.KeyID)
	if err != nil {
		return "", err
	}
	a.cachedJWT = jwt
	a.cachedAt = time.Now()
	return jwt, nil
}

// signES256ForAPNs delegates to the oauth package's helper to avoid
// duplicating the signing logic.
func signES256ForAPNs(key *ecdsa.PrivateKey, teamID, keyID string) (string, error) {
	return oauth.SignAPNsToken(key, teamID, keyID, time.Now())
}
