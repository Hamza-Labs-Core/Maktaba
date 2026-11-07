// Package storelink is the home's half of linking it to a plugin store account (Epic 54,
// story 54.15 §5). The home makes an Ed25519 home store key on the first link and keeps
// it sealed in home_secrets (`plugin_store.home_key`); it asks the store for a device code
// (RFC 8628's shape), shows the user code, polls until the admin confirms it on
// plugins.maktaba.tv/link, and afterwards signs each store request with that key
// (`Authorization: MKS-Home id=…, ts=…, nonce=…, sig=…`). There is no bearer secret.
//
// Nothing here talks to Maktaba Cloud: the store is its own service, and the store's home
// id is unrelated to any cloud home id.
package storelink

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// DefaultStore is the store's address (MAKTABA_PLUGIN_STORE_URL overrides it: a local store
// in tests and development).
const DefaultStore = "https://plugins.maktaba.tv"

// KeyName is the home store key's name in home_secrets; StateKey the link's app_settings key.
const (
	KeyName  = "plugin_store.home_key"
	StateKey = "plugins.store_link"
)

// Codes the routes answer (words in web/src/i18n as plugins.store.error.<code>).
const (
	CodeNotLinked   = "store_not_linked"
	CodeNoPending   = "store_link_not_started"
	CodeUnreachable = "store_unreachable"
	CodeExpired     = "code_expired"
	// CodeOffline: MAKTABA_OFFLINE is on (serve --offline): the home calls no store.
	CodeOffline = "store_offline"
)

// Secrets is the home's sealed store (secrets.DBStore; secrets.Memory in tests).
type Secrets interface {
	Get(ctx context.Context, name string) ([]byte, error)
	Put(ctx context.Context, name string, value []byte, by string) error
	Delete(ctx context.Context, name string) error
}

// Error is a refusal with its code and the HTTP status the store or the home gave.
type Error struct {
	Status int
	Code   string
	Msg    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Msg }

// State is what the home keeps about its link (app_settings, not secret).
type State struct {
	HomeID   string    `json:"home_id"`
	LinkedAt time.Time `json:"linked_at"`
}

// Pending is a link the admin started and has not confirmed yet.
type Pending struct {
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
	Interval        int       `json:"interval"`
	deviceCode      string
}

// Link links this home to a store account.
type Link struct {
	DB       *sql.DB
	Secrets  Secrets
	StoreURL string
	HTTP     *http.Client
	Version  string
	Platform string
	// Name is the home's name shown on /link.
	Name string
	Now  func() time.Time
	// Offline reports whether the home makes no outbound call (ports.Offline when nil):
	// then every store request is refused before it is made.
	Offline func() bool

	mu      sync.Mutex
	pending *Pending
}

func (l *Link) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Link) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (l *Link) base() string {
	if l.StoreURL != "" {
		return strings.TrimRight(l.StoreURL, "/")
	}
	return DefaultStore
}

// key loads the home store key, making it on the first link.
func (l *Link) key(ctx context.Context, create bool) (ed25519.PrivateKey, error) {
	seed, err := l.Secrets.Get(ctx, KeyName)
	if err == nil && len(seed) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	if !create {
		return nil, &Error{Status: http.StatusConflict, Code: CodeNotLinked, Msg: "this home has no store key"}
	}
	seed = make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if err := l.Secrets.Put(ctx, KeyName, seed, "plugins.storelink"); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// LoadState reads the link (nil when not linked).
func (l *Link) LoadState(ctx context.Context) (*State, error) {
	var raw []byte
	err := l.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, StateKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil || s.HomeID == "" {
		return nil, nil
	}
	return &s, nil
}

func (l *Link) saveState(ctx context.Context, s *State) error {
	if s == nil {
		_, err := l.DB.ExecContext(ctx, `DELETE FROM app_settings WHERE key = $1`, StateKey)
		return err
	}
	b, _ := json.Marshal(s)
	_, err := l.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, StateKey, string(b))
	return err
}

// Status is the link's state and any pending code.
type Status struct {
	Linked   bool       `json:"linked"`
	HomeID   string     `json:"home_id,omitempty"`
	LinkedAt *time.Time `json:"linked_at,omitempty"`
	Pending  *Pending   `json:"pending,omitempty"`
	Store    string     `json:"store"`
}

func (l *Link) Status(ctx context.Context) (*Status, error) {
	s, err := l.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	out := &Status{Store: l.base()}
	if s != nil {
		at := s.LinkedAt
		out.Linked, out.HomeID, out.LinkedAt = true, s.HomeID, &at
	}
	l.mu.Lock()
	if l.pending != nil && l.pending.ExpiresAt.After(l.now()) {
		p := *l.pending
		out.Pending = &p
	}
	l.mu.Unlock()
	return out, nil
}

func (l *Link) offline() bool {
	if l.Offline != nil {
		return l.Offline()
	}
	return ports.Offline()
}

func (l *Link) do(ctx context.Context, method, path string, body []byte, sign *signer) (*http.Response, []byte, error) {
	if l.offline() {
		return nil, nil, &Error{Status: http.StatusConflict, Code: CodeOffline, Msg: "this home is offline (MAKTABA_OFFLINE): it calls no store"}
	}
	req, err := http.NewRequestWithContext(ctx, method, l.base()+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Maktaba/"+l.Version)
	if sign != nil {
		req.Header.Set("Authorization", sign.header(method, path, body, l.now()))
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return nil, nil, &Error{Status: http.StatusBadGateway, Code: CodeUnreachable, Msg: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp, b, nil
}

func storeError(resp *http.Response, b []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(b, &e)
	if e.Error == "" {
		e.Error = CodeUnreachable
	}
	status := resp.StatusCode
	if status >= 500 {
		status = http.StatusBadGateway
	}
	return &Error{Status: status, Code: e.Error, Msg: fmt.Sprintf("the store answered %d", resp.StatusCode)}
}

// Start asks the store for a code to show (54.15 §5 steps 1-2).
func (l *Link) Start(ctx context.Context) (*Pending, error) {
	k, err := l.key(ctx, true)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{
		"public_key":     base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey)),
		"server_version": l.Version,
		"platform":       l.Platform,
		"name":           l.Name,
	})
	resp, b, err := l.do(ctx, http.MethodPost, "/v1/homes/pair", body, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, storeError(resp, b)
	}
	var a struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := json.Unmarshal(b, &a); err != nil || a.DeviceCode == "" {
		return nil, &Error{Status: http.StatusBadGateway, Code: CodeUnreachable, Msg: "the store's answer is not a code"}
	}
	p := &Pending{UserCode: a.UserCode, VerificationURI: a.VerificationURI, Interval: a.Interval,
		ExpiresAt: l.now().Add(time.Duration(a.ExpiresIn) * time.Second), deviceCode: a.DeviceCode}
	l.mu.Lock()
	l.pending = p
	l.mu.Unlock()
	out := *p
	return &out, nil
}

// Poll asks the store once whether the admin confirmed (step 4): linked, pending or expired.
func (l *Link) Poll(ctx context.Context) (*Status, error) {
	l.mu.Lock()
	p := l.pending
	l.mu.Unlock()
	if p == nil {
		return nil, &Error{Status: http.StatusConflict, Code: CodeNoPending, Msg: "no link was started"}
	}
	body, _ := json.Marshal(map[string]string{"device_code": p.deviceCode})
	resp, b, err := l.do(ctx, http.MethodPost, "/v1/homes/pair/poll", body, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusGone {
		l.mu.Lock()
		l.pending = nil
		l.mu.Unlock()
		return nil, &Error{Status: http.StatusGone, Code: CodeExpired, Msg: "the code expired: start again"}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, storeError(resp, b)
	}
	var a struct {
		Status string `json:"status"`
		HomeID string `json:"home_id"`
	}
	_ = json.Unmarshal(b, &a)
	if a.Status == "linked" && a.HomeID != "" {
		if err := l.saveState(ctx, &State{HomeID: a.HomeID, LinkedAt: l.now().UTC()}); err != nil {
			return nil, err
		}
		l.mu.Lock()
		l.pending = nil
		l.mu.Unlock()
	}
	return l.Status(ctx)
}

// Unlink tells the store (signed) and forgets the link; the key stays for a later link.
func (l *Link) Unlink(ctx context.Context) error {
	s, err := l.LoadState(ctx)
	if err != nil {
		return err
	}
	if s == nil {
		return &Error{Status: http.StatusConflict, Code: CodeNotLinked, Msg: "this home is not linked"}
	}
	resp, b, err := l.Signed(ctx, http.MethodDelete, "/v1/homes/"+s.HomeID, nil)
	if err != nil {
		return err
	}
	// a home the store already forgot (unlinked from the account's page) is unlinked here too
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return storeError(resp, b)
	}
	return l.saveState(ctx, nil)
}

// Signed sends a request signed with the home store key (54.15 §5 step 5).
func (l *Link) Signed(ctx context.Context, method, path string, body []byte) (*http.Response, []byte, error) {
	s, err := l.LoadState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if s == nil {
		return nil, nil, &Error{Status: http.StatusConflict, Code: CodeNotLinked, Msg: "this home is not linked"}
	}
	k, err := l.key(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	return l.do(ctx, method, path, body, &signer{key: k, homeID: s.HomeID})
}

type signer struct {
	key    ed25519.PrivateKey
	homeID string
}

// header is the MKS-Home authorization of one request: Ed25519 over the method, the path,
// the time, a nonce and the body's sha256.
func (s *signer) header(method, path string, body []byte, at time.Time) string {
	n := make([]byte, 16)
	_, _ = rand.Read(n)
	return s.headerWith(method, path, body, at, base64.RawURLEncoding.EncodeToString(n))
}

func (s *signer) headerWith(method, path string, body []byte, at time.Time, nonce string) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	sum := sha256.Sum256(body)
	msg := "MKS-Home\n" + method + "\n" + path + "\n" + ts + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(s.key, []byte(msg)))
	return fmt.Sprintf("MKS-Home id=%s, ts=%s, nonce=%s, sig=%s", s.homeID, ts, nonce, sig)
}
