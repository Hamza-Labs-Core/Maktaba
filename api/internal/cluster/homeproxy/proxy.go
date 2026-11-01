// Package homeproxy is a full node's reverse proxy to its home (Epic 35 story 35.10,
// design §11.2): every `home` route a node gets goes to the home's API with the user's
// session as X-Maktaba-Delegated-Session, the node's signature (story 35.9) and
// X-Maktaba-Via: node. WebSocket upgrades (the hub socket /api/cluster/ws) and streamed
// answers (SSE) pass through (FlushInterval -1); Set-Cookie loses any Domain so the
// browser keeps the cookie for the node's own host; an incoming X-Maktaba-Via is never
// passed on. The request's Host is kept, so the home sees the address the browser
// used (same-origin checks of sockets hold).
package homeproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
)

// HeaderTimeout bounds the wait for the home's answer headers on JSON calls (EC2);
// sockets and event streams have none. FailingTimeout is the bound while the home is
// not answering its probes (Options.Failing): it is not waited on for long then.
const (
	HeaderTimeout  = 30 * time.Second
	FailingTimeout = 5 * time.Second
)

// Options are a proxy's: Fallback answers what the home did not (true: answered);
// Failing reports that the home is not answering its probes (its calls are bounded by
// FailingTimeout then).
type Options struct {
	Fallback func(http.ResponseWriter, *http.Request) bool
	Failing  func() bool
}

// SessionCookie and CSRFCookie are the session's cookie names; LocalSessionPrefix marks
// a node-local session's value (nodeauth: a sign-in made on the node, which the home
// knows nothing of).
const (
	SessionCookie      = "mkt_sess"
	CSRFCookie         = "mkt_csrf"
	LocalSessionPrefix = "nls_"
)

// withoutSession drops the session's cookies from an outgoing request.
func withoutSession(out *http.Request) {
	cookies := out.Cookies()
	out.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != SessionCookie && c.Name != CSRFCookie {
			out.AddCookie(c)
		}
	}
}

// New builds the proxy over a link.
func New(link homelink.Link, logger *slog.Logger) http.Handler {
	return NewWithFallback(link, logger, nil)
}

// NewWithFallback is New whose requests the home did not answer go to fallback first:
// it answers the ones it can on the node (true) — a node's /api/auth/me from the home's
// last answer — and the rest get the 503 home_unreachable.
func NewWithFallback(link homelink.Link, logger *slog.Logger, fallback func(http.ResponseWriter, *http.Request) bool) http.Handler {
	return NewWith(link, logger, Options{Fallback: fallback})
}

// NewWith is New with Options.
func NewWith(link homelink.Link, logger *slog.Logger, o Options) http.Handler {
	fallback := o.Fallback
	if logger == nil {
		logger = slog.Default()
	}
	target, err := url.Parse(link.HomeURL())
	if err != nil || target.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeUnreachable(w, "no home address") })
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			host := pr.In.Host
			xff := pr.In.Header.Values("X-Forwarded-For")
			pr.SetURL(target)
			pr.Out.Host = host
			// the node is the edge for these headers: only what it sets itself goes on
			for _, h := range []string{delegation.HeaderVia, delegation.HeaderNode, delegation.HeaderKey,
				delegation.HeaderTs, delegation.HeaderNonce, delegation.HeaderSig, delegation.HeaderBody, delegation.HeaderDelegatedSession,
				delegation.HeaderOriginNode} {
				pr.Out.Header.Del(h)
			}
			if len(xff) > 0 {
				pr.Out.Header["X-Forwarded-For"] = xff
			}
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Host", host)
			if c, err := pr.In.Cookie(SessionCookie); err == nil && c.Value != "" {
				if strings.HasPrefix(c.Value, LocalSessionPrefix) {
					// a node-local sign-in never reaches the home (it would clear it)
					withoutSession(pr.Out)
				} else {
					pr.Out.Header.Set(delegation.HeaderDelegatedSession, c.Value)
				}
			}
		},
		Transport:     timeoutTransport{base: link.Transport(), failing: o.Failing},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			StripCookieDomain(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
				return // the browser went away
			}
			logger.Warn("node: the home did not answer a proxied request", "path", r.URL.Path, "err", err,
				"event", "node_home_proxy_failed")
			if fallback != nil && fallback(w, r) {
				return
			}
			if errors.Is(err, errHeaderTimeout) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.tv/problems/home-timeout",
					"title": "the home is slow to answer", "status": http.StatusGatewayTimeout, "code": "home_timeout"})
				return
			}
			writeUnreachable(w, err.Error())
		},
	}
	return rp
}

var errHeaderTimeout = errors.New("homeproxy: the home did not answer in time")

// timeoutTransport cancels a JSON call whose answer headers take longer than
// HeaderTimeout (FailingTimeout while the home is not answering its probes); an
// upgrade or an event stream is never cut.
type timeoutTransport struct {
	base    http.RoundTripper
	failing func() bool
}

func (t timeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Upgrade") != "" || strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return t.base.RoundTrip(r)
	}
	wait := HeaderTimeout
	if t.failing != nil && t.failing() {
		wait = FailingTimeout
	}
	ctx, cancel := context.WithCancelCause(r.Context())
	timer := time.AfterFunc(wait, func() { cancel(errHeaderTimeout) })
	resp, err := t.base.RoundTrip(r.WithContext(ctx))
	timer.Stop()
	if err != nil {
		if context.Cause(ctx) == errHeaderTimeout {
			err = errHeaderTimeout
		}
		cancel(nil)
		return nil, err
	}
	// the body still reads under ctx: release it with the body
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: func() { cancel(nil) }}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel func()
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

var domainAttr = regexp.MustCompile(`(?i);\s*domain=[^;]*`)

// StripCookieDomain removes the Domain attribute from every Set-Cookie (design §10.4:
// the browser keeps the cookie host-only for the node's address).
func StripCookieDomain(h http.Header) {
	vals := h.Values("Set-Cookie")
	if len(vals) == 0 {
		return
	}
	h.Del("Set-Cookie")
	for _, v := range vals {
		h.Add("Set-Cookie", domainAttr.ReplaceAllString(v, ""))
	}
}

func writeUnreachable(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.tv/problems/home-unreachable",
		"title": "the home is not reachable", "status": http.StatusServiceUnavailable, "code": "home_unreachable",
		"detail": detail})
}
