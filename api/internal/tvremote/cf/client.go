// Package cf is Maktaba's client for the Cloudflare API v4, the Stream
// calls of Epic 32 (design §7.1): live inputs, videos and lifecycle,
// signing keys and token minting, storage use and the GraphQL analytics
// probe. Only the home's API uses it; the token comes from home_secrets
// and is never logged (Client.String and errors leave it out).
package cf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Cloudflare API v4 root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Client calls the Cloudflare API with one account's API token.
type Client struct {
	// BaseURL is the API root (DefaultBaseURL; a fake server in tests).
	BaseURL string
	// AccountID is the Cloudflare account the Stream calls address.
	AccountID string
	token     string
	// HTTP is the client (default: 30 s timeout).
	HTTP *http.Client
	// Retries on 429 and 5xx (default 3), with Backoff between tries
	// (default 500 ms, doubled; a Retry-After header wins).
	Retries int
	Backoff time.Duration
	// Sleep waits between retries (tests make it instant).
	Sleep func(ctx context.Context, d time.Duration) error
	// CustomerBase overrides https://customer-<code>.cloudflarestream.com
	// (tests): the lifecycle and playback host.
	CustomerBase string
}

// New returns a client for account with token.
func New(accountID, token string) *Client {
	return &Client{BaseURL: DefaultBaseURL, AccountID: strings.TrimSpace(accountID), token: strings.TrimSpace(token)}
}

// String never prints the token.
func (c *Client) String() string { return "cf.Client(account " + c.AccountID + ")" }

// Error is one entry of a Cloudflare response's errors[].
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// APIError is a failed call: the HTTP status and Cloudflare's errors.
type APIError struct {
	Status int
	Method string
	Path   string
	Errors []Error
}

func (e *APIError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, x := range e.Errors {
		parts = append(parts, strconv.Itoa(x.Code)+" "+x.Message)
	}
	msg := strings.Join(parts, "; ")
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("cloudflare %d: %s", e.Status, msg)
}

// HasCode reports whether Cloudflare answered with this error code.
func (e *APIError) HasCode(code int) bool {
	for _, x := range e.Errors {
		if x.Code == code {
			return true
		}
	}
	return false
}

// AsAPIError unwraps err to an *APIError.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	ok := errors.As(err, &ae)
	return ae, ok
}

type envelope struct {
	Success  bool            `json:"success"`
	Errors   []Error         `json:"errors"`
	Messages json.RawMessage `json:"messages"`
	Result   json.RawMessage `json:"result"`
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// account is the path prefix of the account's resources.
func (c *Client) account() string { return "/accounts/" + c.AccountID }

// do calls method path (relative to BaseURL) with an optional JSON body
// and decodes result into out (nil: ignore). It retries 429 and 5xx.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	raw, err := c.doRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("cloudflare %s %s: undecodable answer: %w", method, path, err)
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("cloudflare %s %s: unexpected result: %w", method, path, err)
	}
	return nil
}

// doRaw returns the whole body of a successful call.
func (c *Client) doRaw(ctx context.Context, method, path string, body any) ([]byte, error) {
	if c.token == "" {
		return nil, errors.New("cloudflare: no API token")
	}
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = b
	}
	retries := c.Retries
	if retries <= 0 {
		retries = 3
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, backoff); err != nil {
				return nil, err
			}
			backoff *= 2
		}
		var rd io.Reader
		if payload != nil {
			rd = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			// the URL holds no secret; the token is only in the header
			last = fmt.Errorf("cloudflare %s %s: %w", method, path, err)
			continue
		}
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if rerr != nil {
			last = fmt.Errorf("cloudflare %s %s: read: %w", method, path, rerr)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			last = decodeError(resp.StatusCode, method, path, b)
			if ra, perr := strconv.Atoi(resp.Header.Get("Retry-After")); perr == nil && ra > 0 && ra <= 60 {
				backoff = time.Duration(ra) * time.Second
			}
			continue
		}
		if resp.StatusCode >= 300 {
			return nil, decodeError(resp.StatusCode, method, path, b)
		}
		var env envelope
		if json.Unmarshal(b, &env) == nil && !env.Success && len(env.Errors) > 0 {
			return nil, &APIError{Status: resp.StatusCode, Method: method, Path: path, Errors: env.Errors}
		}
		return b, nil
	}
	return nil, last
}

func decodeError(status int, method, path string, b []byte) error {
	var env envelope
	_ = json.Unmarshal(b, &env)
	return &APIError{Status: status, Method: method, Path: path, Errors: env.Errors}
}

// TokenStatus is GET /user/tokens/verify's result.
type TokenStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"` // active | disabled | expired
	ExpiresOn string `json:"expires_on,omitempty"`
	// AccountOwned: verified through /accounts/{id}/tokens/verify.
	AccountOwned bool `json:"-"`
}

// VerifyToken checks the token: the user-token endpoint first, then the
// account-token one (an account-owned token answers 401/403 on the first).
func (c *Client) VerifyToken(ctx context.Context) (TokenStatus, error) {
	var st TokenStatus
	err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &st)
	if err == nil {
		return st, nil
	}
	ae, ok := AsAPIError(err)
	if !ok || (ae.Status != http.StatusUnauthorized && ae.Status != http.StatusForbidden && ae.Status != http.StatusBadRequest) || c.AccountID == "" {
		return st, err
	}
	var st2 TokenStatus
	if err2 := c.do(ctx, http.MethodGet, c.account()+"/tokens/verify", nil, &st2); err2 != nil {
		return st, err
	}
	st2.AccountOwned = true
	return st2, nil
}
