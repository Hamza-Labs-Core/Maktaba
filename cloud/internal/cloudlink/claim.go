package cloudlink

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// claimRequest matches cloud/internal/handlers/servers/servers.go
// redeemReq EXACTLY. Field names are load-bearing — they are the JSON
// the cloud's RedeemClaim decodes.
type claimRequest struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Version string `json:"version"`
	PubKey  string `json:"public_key_pem"`
}

// claimResponse matches servers.go redeemResp EXACTLY.
type claimResponse struct {
	ServerID     string `json:"server_id"`
	ServerSecret string `json:"server_secret"`
	Slug         string `json:"slug"`
}

// Credentials is what we persist on the on-prem box after a successful
// claim. The secret is the bearer of all future tunnel auth, so it is
// written encrypted-at-rest (Story 25.6 AC: "persist {server_id,
// server_token} encrypted-at-rest").
type Credentials struct {
	ServerID string `json:"server_id"`
	Secret   string `json:"secret"`
	Slug     string `json:"slug"`
}

var (
	// ErrClaimRejected is returned for any non-2xx redeem reply (the
	// cloud uses 404 claim_invalid for expired/used codes today; the
	// spec's 410/409 are documented divergences, not handled specially
	// because the server never sends them).
	ErrClaimRejected = errors.New("cloudlink: claim redeem rejected")
)

// Claimer redeems a one-time claim code against the cloud and returns
// long-lived server credentials. Endpoint is the cloud API base, e.g.
// https://api.maktaba.app (NOT the relay ws endpoint).
type Claimer struct {
	Endpoint string
	Client   *http.Client
}

// Redeem POSTs the claim code to /v1/servers/claims/redeem. The cloud
// path is taken from servers.go Mount(); the spec's /api/servers/claim
// two-step init+redeem does not exist server-side, so we speak the
// one-step endpoint that is actually mounted.
func (c *Claimer) Redeem(ctx context.Context, code, name, slug, version, pubKeyPEM string) (Credentials, error) {
	body, _ := json.Marshal(claimRequest{
		Code:    code,
		Name:    name,
		Slug:    slug,
		Version: version,
		PubKey:  pubKeyPEM,
	})
	url := c.Endpoint + "/v1/servers/claims/redeem"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudlink: build redeem request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	cl := c.Client
	if cl == nil {
		// A claim is a one-shot call made at most once per box install.
		// Disable keep-alives so we never hold a pooled TCP connection
		// to the cloud after the single request completes — there is no
		// second request to amortize a kept-alive conn against, and a
		// lingering idle conn is pure operational/leak surface.
		cl = &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{DisableKeepAlives: true},
		}
	}
	req.Close = true
	resp, err := cl.Do(req)
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudlink: redeem POST: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode/100 != 2 {
		return Credentials{}, fmt.Errorf("%w: status %d: %s", ErrClaimRejected, resp.StatusCode, string(raw))
	}
	var cr claimResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return Credentials{}, fmt.Errorf("cloudlink: decode redeem reply: %w", err)
	}
	if cr.ServerID == "" || cr.ServerSecret == "" {
		return Credentials{}, fmt.Errorf("%w: empty server_id/secret", ErrClaimRejected)
	}
	return Credentials{ServerID: cr.ServerID, Secret: cr.ServerSecret, Slug: cr.Slug}, nil
}

// --- Encrypted-at-rest credential store (Story 25.6) -------------------

// SaveCredentials writes creds to path, AES-256-GCM sealed under key.
// key must be 32 bytes. The file is created 0600.
func SaveCredentials(path string, key []byte, creds Credentials) error {
	plain, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("cloudlink: nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, sealed, 0o600)
}

// LoadCredentials reads and decrypts a file written by SaveCredentials.
func LoadCredentials(path string, key []byte) (Credentials, error) {
	sealed, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return Credentials{}, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return Credentials{}, errors.New("cloudlink: credential file truncated")
	}
	plain, err := gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
	if err != nil {
		return Credentials{}, fmt.Errorf("cloudlink: decrypt credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("cloudlink: key must be 32 bytes, got %d", len(key))
	}
	blk, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}
