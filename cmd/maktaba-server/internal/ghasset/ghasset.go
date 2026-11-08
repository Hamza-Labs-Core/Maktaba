// Package ghasset reaches a GitHub release's file without github.com's download door
// (https://github.com/<repo>/releases/download/<tag>/<name>), which can fail on its own
// while GitHub's API and its asset storage answer: the API's asset route,
// <api>/repos/<repo>/releases/assets/<id> with Accept: application/octet-stream, which
// redirects to the file on release-assets.githubusercontent.com. It sends no credentials:
// the file is public, and whatever it delivers is checked by the caller against a pin or a
// signed index, never trusted for where it came from.
//
// It also says which failures are worth trying again (Transient): a 5xx, a 429 or 408, a
// timeout, a connection refused or cut; not a 4xx, a host that does not exist, or the
// caller's own cancellation.
package ghasset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAPI is GitHub's API.
const DefaultAPI = "https://api.github.com"

// StatusError is an answer that was not 200 (or 206 where a range was asked for).
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string { return "HTTP " + strconv.Itoa(e.Code) }

// ErrNoAsset: the release has no file of that name.
var ErrNoAsset = errors.New("no such asset in the release")

// releaseLimit caps the release's JSON (its asset list).
const releaseLimit = 4 << 20

// AssetURL is the API's asset route for the file name of repo's release tag:
// <api>/repos/<repo>/releases/assets/<id>, the id looked up by name in
// <api>/repos/<repo>/releases/tags/<tag>. The route is built from api and the id, never
// taken from the answer.
func AssetURL(ctx context.Context, c *http.Client, api, repo, tag, name string) (string, error) {
	api = strings.TrimRight(api, "/")
	u := api + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release %s: %w", tag, &StatusError{Code: resp.StatusCode})
	}
	var rel struct {
		Assets []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, releaseLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > releaseLimit {
		return "", fmt.Errorf("release %s: larger than %d bytes", tag, releaseLimit)
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return "", fmt.Errorf("release %s: %w", tag, err)
	}
	for _, a := range rel.Assets {
		if a.Name == name && a.ID > 0 {
			return api + "/repos/" + repo + "/releases/assets/" + strconv.FormatInt(a.ID, 10), nil
		}
	}
	return "", fmt.Errorf("release %s: %s: %w", tag, name, ErrNoAsset)
}

// AssetRequest is a GET of an asset route: Accept: application/octet-stream (the file,
// not its JSON), and no credentials.
func AssetRequest(ctx context.Context, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	return req, nil
}

// Transient reports whether err is worth another try at the same place: a 5xx, 429 or
// 408, a timeout, or a connection refused, reset or cut short. A 4xx, a host name that
// does not resolve, and the caller's own cancellation are not.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code >= 500 || se.Code == http.StatusTooManyRequests || se.Code == http.StatusRequestTimeout
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	if errors.Is(err, ErrNoAsset) {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return true // the request did not complete: refused, reset, a timeout, a TLS failure
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "connection reset")
}

// Backoff is the wait before try n (1-based: the wait after the first failure is first),
// doubled each time.
func Backoff(first time.Duration, n int) time.Duration {
	d := first
	for i := 1; i < n; i++ {
		d *= 2
	}
	return d
}

// Sleep waits d, or until ctx is done.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
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

// Client is an HTTP client for large downloads: no overall timeout (the caller's context
// bounds the whole), but a connection, TLS handshake or first answer that takes too long
// fails (a front door that hangs before a 504 is a timeout, tried again), with the
// environment's proxy honoured (HTTPS_PROXY), as http.DefaultTransport's.
func Client() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = 90 * time.Second
	t.TLSHandshakeTimeout = 20 * time.Second
	return &http.Client{Transport: t}
}
