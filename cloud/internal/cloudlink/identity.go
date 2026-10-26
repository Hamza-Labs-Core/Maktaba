package cloudlink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// IdentityFile is the home's cloud identity, beside the agent's sealed
// credentials (Epic 42 story 42.7): which server this home is in the
// cloud and the cloud's assertion key it pinned at claim. The home's API
// reads it to verify linked-account sign-ins. Nothing in it is secret
// (the secret stays sealed in the credential file); it is written 0600
// all the same, as the keys directory's other files.
const IdentityFile = "cloudlink.identity.json"

// Identity is the identity file's content.
type Identity struct {
	ServerID     string `json:"server_id"`
	Slug         string `json:"slug"`
	AssertionKey string `json:"assertion_key"`
	CloudAPI     string `json:"cloud_api"`
}

// IdentityPath is the identity file beside a credential file.
func IdentityPath(credFile string) string {
	return filepath.Join(filepath.Dir(credFile), IdentityFile)
}

// WriteIdentity writes the identity file atomically.
func WriteIdentity(path string, id Identity) error {
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ValidAssertionKey reports whether k is a base64 Ed25519 public key.
func ValidAssertionKey(k string) bool {
	raw, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(raw) == ed25519.PublicKeySize
}

// FetchAssertionKey asks the cloud for its assertion key: for a home
// claimed before assertions existed (trust on first use, over the cloud
// API's TLS). A key pinned at claim is never replaced by this.
func FetchAssertionKey(ctx context.Context, cloudAPI string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudAPI+"/v1/homes/assertion-key", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cloudlink: assertion key: status %d", resp.StatusCode)
	}
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&body); err != nil {
		return "", err
	}
	if !ValidAssertionKey(body.PublicKey) {
		return "", errors.New("cloudlink: the cloud's assertion key is not an Ed25519 key")
	}
	return body.PublicKey, nil
}

// LinkReporter passes the home's linked cloud accounts to the cloud
// (PUT /v1/servers/{id}/links, authenticated by the server's secret,
// which only this agent holds).
type LinkReporter struct {
	CloudAPI string
	Creds    Credentials
	Client   *http.Client
}

// Report replaces the cloud's copy of the home's links.
func (l *LinkReporter) Report(ctx context.Context, cloudUserIDs []string) error {
	if cloudUserIDs == nil {
		cloudUserIDs = []string{}
	}
	body, _ := json.Marshal(map[string][]string{"cloud_user_ids": cloudUserIDs})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		l.CloudAPI+"/v1/servers/"+l.Creds.ServerID+"/links", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(l.Creds.ServerID, l.Creds.Secret)
	req.Header.Set("Content-Type", "application/json")
	cl := l.Client
	if cl == nil {
		cl = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("cloudlink: report links: status %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// LinksHandler serves POST /admin/cloud-link/links on loopback: the
// home's API reports its links here and the agent passes them on. Any
// local caller could post a list, which only changes which homes the
// cloud lists for which accounts: every sign-in is still the home's to
// refuse (its own home_user_links).
func LinksHandler(rep *LinkReporter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			CloudUserIDs []string `json:"cloud_user_ids"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := rep.Report(ctx, body.CloudUserIDs); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
