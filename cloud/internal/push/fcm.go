package push

import (
	"bytes"
	"context"
	"crypto/rsa"
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
)

// FCMDriver speaks Firebase Cloud Messaging's HTTP v1 API. Auth is via
// a Google service-account OAuth2 token; we cache it for ~50 minutes.
type FCMDriver struct {
	ProjectID          string
	ServiceAccountPath string
	HTTP               *http.Client

	mu          sync.Mutex
	token       string
	tokenAt     time.Time
	clientEmail string
	privateKey  *rsa.PrivateKey
}

func NewFCMDriver(projectID, saPath string) *FCMDriver {
	return &FCMDriver{
		ProjectID:          projectID,
		ServiceAccountPath: saPath,
		HTTP:               &http.Client{Timeout: 10 * time.Second},
	}
}

func (f *FCMDriver) Name() string { return "fcm" }

func (f *FCMDriver) Send(ctx context.Context, deviceToken string, n Notification) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return fmt.Errorf("fcm: access token: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{
			"token":        deviceToken,
			"notification": map[string]string{"title": n.Title, "body": n.Body},
			"data":         n.Data,
		},
	})
	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", f.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		return nil
	}
	rb, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("fcm: %d %s", resp.StatusCode, string(rb))
}

// accessToken obtains an OAuth2 access token via the JWT-bearer grant.
// The first call loads + parses the service-account JSON.
func (f *FCMDriver) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Since(f.tokenAt) < 50*time.Minute {
		return f.token, nil
	}
	if f.privateKey == nil {
		if err := f.loadServiceAccount(); err != nil {
			return "", err
		}
	}
	jwt, err := signRS256ForFCM(f.privateKey, f.clientEmail, time.Now())
	if err != nil {
		return "", err
	}
	form := fmt.Sprintf("grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=%s", jwt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", bytes.NewReader([]byte(form)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("fcm: oauth %d %s", resp.StatusCode, string(b))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	f.token = tok.AccessToken
	f.tokenAt = time.Now()
	return f.token, nil
}

type serviceAccountJSON struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

func (f *FCMDriver) loadServiceAccount() error {
	raw, err := os.ReadFile(f.ServiceAccountPath)
	if err != nil {
		return err
	}
	var sa serviceAccountJSON
	if err := json.Unmarshal(raw, &sa); err != nil {
		return err
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return errors.New("fcm: no PEM block in service account key")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	rsaKey, ok := k.(*rsa.PrivateKey)
	if !ok {
		return errors.New("fcm: not an RSA key")
	}
	f.privateKey = rsaKey
	f.clientEmail = sa.ClientEmail
	return nil
}
