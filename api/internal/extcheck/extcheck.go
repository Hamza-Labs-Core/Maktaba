// Package extcheck is "Test connection" for the Plex and dizqueTV
// extensions (the Cloudflare Stream one lives in tvremote). Each test is a
// list of checks in the shape tvremote.TestResult uses, so the extensions
// page lists them the same way: key, ok, optional (a warning when not ok),
// skipped, detail, plus an error code and the round trip in milliseconds.
//
// Rules every check keeps:
//   - one request per check, each bounded by CheckTimeout;
//   - a redirect is followed only to the same scheme, host and port: the
//     Plex token (a header) is never sent to another host;
//   - the token is never put in a URL, a detail, a log line or the answer
//     (Redact scrubs any error text that could carry it).
package extcheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// CheckTimeout bounds each check's request.
const CheckTimeout = 5 * time.Second

// maxBody caps what a check reads of an answer.
const maxBody = 4 << 20

// Error codes of a failed check (Line.Code).
const (
	CodeNotConfigured = "not_configured"
	CodeBadURL        = "bad_url"
	CodeTimeout       = "timeout"
	CodeUnreachable   = "unreachable"
	CodeDNS           = "dns"
	CodeTLSHostname   = "tls_hostname"
	CodeTLSUntrusted  = "tls_untrusted"
	CodeTLSExpired    = "tls_expired"
	CodeTLS           = "tls"
	CodeRedirect      = "redirect_refused"
	CodeHTTP          = "http_error"
	CodeBadAnswer     = "bad_answer"
	CodeNoToken       = "no_token"
	CodeTokenRejected = "token_rejected"
	CodeSecrets       = "secrets_unavailable"
	CodeLiveTVMissing = "livetv_missing"
	CodeNotFound      = "not_found"
	CodePathMap       = "path_map_mismatch"
)

// Line is one check (same JSON as tvremote.Line, plus code and latency_ms).
type Line struct {
	Key       string `json:"key"`
	OK        bool   `json:"ok"`
	Optional  bool   `json:"optional,omitempty"`
	Skipped   bool   `json:"skipped,omitempty"`
	Detail    string `json:"detail"`
	Code      string `json:"code,omitempty"`
	LatencyMS *int64 `json:"latency_ms,omitempty"`
}

// Result is the answer of POST /api/extensions/{name}/test.
type Result struct {
	OK        bool      `json:"ok"`
	Message   string    `json:"message"`
	CheckedAt time.Time `json:"checked_at"`
	Lines     []Line    `json:"lines"`
}

// Extensions reads an extension's stored (non-secret) settings (the
// queues handler implements it).
type Extensions interface {
	ExtensionSettings(ctx context.Context, name string) (map[string]any, bool, error)
}

// Deps is what the testers share.
type Deps struct {
	Ext     Extensions
	Secrets secrets.Store
	// DB is read for Maktaba's tuner id and a sample library root; nil
	// skips those parts.
	DB *sql.DB
	// HTTP is the client (tests: a fake's); nil builds one. Its redirect
	// policy is replaced by the same-host rule either way.
	HTTP *http.Client
	// Timeout bounds each check's request; 0 is CheckTimeout.
	Timeout time.Duration
	Now     func() time.Time
	Logger  *slog.Logger
}

func (d Deps) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return CheckTimeout
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

func (d Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// client is d.HTTP (or a default) that follows redirects on the same
// origin only. No client-wide timeout: each request has CheckTimeout.
func (d Deps) client() *http.Client {
	c := &http.Client{}
	if d.HTTP != nil {
		cp := *d.HTTP
		c = &cp
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errRedirect
		}
		if !sameOrigin(req.URL, via[0].URL) {
			return errRedirect
		}
		return nil
	}
	return c
}

var errRedirect = errors.New("redirect to another host refused")

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && hostPort(a) == hostPort(b)
}

func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

// SameHost: do two addresses name the same host and port (scheme and path aside)?
func SameHost(a, b string) bool {
	ua, ea := parseLoose(a)
	ub, eb := parseLoose(b)
	if ea != nil || eb != nil || ua.Hostname() == "" || ub.Hostname() == "" {
		return false
	}
	return hostPort(ua) == hostPort(ub)
}

func parseLoose(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return url.Parse(s)
}

// baseURL validates the configured address: http(s), a host, no user info.
func baseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("no URL saved")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the saved URL is not an http(s) address")
	}
	if u.User != nil {
		return nil, errors.New("the saved URL must not carry a user name or password")
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

// answer is one request's outcome.
type answer struct {
	status  int
	body    []byte
	latency time.Duration
}

// get sends one GET (bounded by the check timeout) with the given headers.
func (d Deps) get(ctx context.Context, c *http.Client, base *url.URL, path string, headers map[string]string) (answer, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	u := *base
	u.Path = base.Path + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return answer{latency: time.Since(start)}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	a := answer{status: resp.StatusCode, body: body, latency: time.Since(start)}
	if err != nil {
		return a, err
	}
	return a, nil
}

func ms(d time.Duration) *int64 {
	v := d.Milliseconds()
	return &v
}

// Redact removes every non-empty secret from s.
func Redact(s string, secrets ...string) string {
	for _, x := range secrets {
		if x = strings.TrimSpace(x); x != "" {
			s = strings.ReplaceAll(s, x, "[redacted]")
		}
	}
	return s
}

// classify explains a transport error: a code and a sentence for the
// operator (never the raw error, which names the URL).
func classify(err error, base *url.URL, timeout time.Duration) (code, detail string) {
	host := base.Host
	var (
		hostErr  x509.HostnameError
		hostErrP *x509.HostnameError
		unknown  x509.UnknownAuthorityError
		unknownP *x509.UnknownAuthorityError
		invalid  x509.CertificateInvalidError
		invalidP *x509.CertificateInvalidError
		recErr   tls.RecordHeaderError
		dnsErr   *net.DNSError
		netErr   net.Error
	)
	plexDirect := strings.HasSuffix(strings.ToLower(base.Hostname()), ".plex.direct")
	switch {
	case errors.Is(err, errRedirect):
		return CodeRedirect, "the server redirected to another host; Maktaba does not follow it (set the final address instead)"
	case errors.As(err, &hostErr) || errors.As(err, &hostErrP):
		d := "the server's TLS certificate does not cover " + base.Hostname()
		if !plexDirect {
			d += ": Plex's certificate is issued for its https://<ip-with-dashes>.<id>.plex.direct:32400 address only; use that address, or http://<ip>:32400 on the local network"
		}
		return CodeTLSHostname, d
	case errors.As(err, &unknown) || errors.As(err, &unknownP):
		d := "the server's TLS certificate is not trusted (self-signed or an unknown issuer)"
		if !plexDirect {
			d += ": Plex's own certificate is valid only on its plex.direct address; use that address, or http:// on the local network"
		}
		return CodeTLSUntrusted, d
	case errors.As(err, &invalid) || errors.As(err, &invalidP):
		if invalid.Reason == x509.Expired || (invalidP != nil && invalidP.Reason == x509.Expired) {
			return CodeTLSExpired, "the server's TLS certificate has expired (or this machine's clock is wrong)"
		}
		return CodeTLS, "the server's TLS certificate is not valid for this connection"
	case errors.As(err, &recErr):
		return CodeTLS, "the server does not speak TLS on " + host + ": use http:// for this address"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return CodeTimeout, fmt.Sprintf("no answer from %s within %s", host, timeout)
	case errors.As(err, &dnsErr):
		return CodeDNS, "the name " + base.Hostname() + " does not resolve"
	case errors.Is(err, syscall.ECONNREFUSED):
		return CodeUnreachable, "connection refused by " + host + " (is the server running, and is the port right?)"
	default:
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "connection refused") || strings.Contains(lower, "actively refused"):
			return CodeUnreachable, "connection refused by " + host + " (is the server running, and is the port right?)"
		case strings.Contains(lower, "x509") || strings.Contains(lower, "tls"):
			return CodeTLS, "TLS failed with " + host
		}
		return CodeUnreachable, "cannot reach " + host
	}
}

// finish sets ok (no failed non-optional line) and the message.
func finish(res *Result, ready string) {
	res.OK = true
	failed := []string{}
	for _, l := range res.Lines {
		if !l.OK && !l.Optional && !l.Skipped {
			res.OK = false
			failed = append(failed, l.Key)
		}
	}
	if res.OK {
		res.Message = ready
	} else {
		res.Message = "not ready: " + strings.Join(failed, ", ")
	}
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func decode(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return errors.New("the answer is not JSON")
	}
	return nil
}
