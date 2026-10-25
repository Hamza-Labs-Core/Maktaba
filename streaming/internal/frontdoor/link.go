package frontdoor

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// The cluster link (Epic 35 story 35.16, design §18.5): the home never connects to a
// machine; the front door's pull of a channel a machine encodes (and the push relay's
// source pull) goes on a data stream the machine opens on the home's ask. The home's API
// holds the machines' links, so the front door asks it on loopback:
// GET <api>/api/cluster/link/pull?node=&purpose=&path=, with this machine's local token.

// Headers a request for a machine's stream carries to LinkPull (never sent on).
const (
	linkNodeHeader    = "X-Maktaba-Link-Node"
	linkPurposeHeader = "X-Maktaba-Link-Purpose"
)

// Purposes of the pulls.
const (
	PurposeTVPull    = "tv_pull"
	PurposeRelayPull = "relay_pull"
)

type purposeKey struct{}

// withPurpose marks a pull's context (the push relay's source: relay_pull).
func withPurpose(ctx context.Context, p string) context.Context {
	return context.WithValue(ctx, purposeKey{}, p)
}

// markLink tags a request for host's stream: LinkPull carries it on that machine's link.
func markLink(req *http.Request, hostID string) {
	req.Header.Set(linkNodeHeader, hostID)
	p, _ := req.Context().Value(purposeKey{}).(string)
	if p == "" {
		p = PurposeTVPull
	}
	req.Header.Set(linkPurposeHeader, p)
}

// LinkPull is the front door's transport: a request tagged with a machine goes through
// the home API's pull route (the machine's link); one the API cannot carry (the machine
// is not on the link) goes straight to the machine's streaming port, unless the link is
// required.
type LinkPull struct {
	// APIURL is the home API on loopback (MAKTABA_LINK_PULL_URL), "" for none.
	APIURL string
	// TokenFile is this machine's local token (MAKTABA_SUPERVISOR_TOKEN_FILE).
	TokenFile string
	Base      http.RoundTripper
	// Required reports whether the home requires the link (no direct fallback).
	Required func() bool
}

// LinkPullFromEnv is the front door's LinkPull from the supervisor's environment.
func LinkPullFromEnv(base http.RoundTripper, required func() bool) *LinkPull {
	return &LinkPull{APIURL: strings.TrimRight(os.Getenv("MAKTABA_LINK_PULL_URL"), "/"),
		TokenFile: os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"), Base: base, Required: required}
}

func (t *LinkPull) required() bool { return t.Required != nil && t.Required() }

// RoundTrip implements http.RoundTripper.
func (t *LinkPull) RoundTrip(req *http.Request) (*http.Response, error) {
	node, purpose := req.Header.Get(linkNodeHeader), req.Header.Get(linkPurposeHeader)
	if node == "" {
		return t.Base.RoundTrip(req)
	}
	direct := req.Clone(req.Context())
	direct.Header.Del(linkNodeHeader)
	direct.Header.Del(linkPurposeHeader)
	tok, _ := os.ReadFile(t.TokenFile)
	if t.APIURL == "" || len(tok) == 0 {
		return t.Base.RoundTrip(direct)
	}
	q := url.Values{"node": {node}, "purpose": {purpose}, "path": {req.URL.RequestURI()}}
	pull, err := http.NewRequestWithContext(req.Context(), http.MethodGet, t.APIURL+"/api/cluster/link/pull?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	pull.Header = direct.Header.Clone()
	pull.Header.Set("X-Maktaba-Local-Token", strings.TrimSpace(string(tok)))
	resp, err := t.Base.RoundTrip(pull)
	if t.required() {
		return resp, err
	}
	if err != nil {
		var ne net.Error
		if ok := asNetErr(err, &ne); ok {
			return t.Base.RoundTrip(direct) // the API is not answering: the older path
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway {
		// the machine is not on the link (or did not open the stream): the older path
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		return t.Base.RoundTrip(direct)
	}
	return resp, nil
}

func asNetErr(err error, target *net.Error) bool {
	ne, ok := err.(net.Error) //nolint:errorlint // a transport's error is a net.Error itself or a *url.Error
	if !ok {
		if ue, isURL := err.(*url.Error); isURL { //nolint:errorlint // see above
			ne, ok = ue.Err.(net.Error) //nolint:errorlint // see above
		}
	}
	if ok {
		*target = ne
	}
	return ok
}

// LoopbackOnlyWhen refuses requests from other machines while required says so (a
// node's streaming port once the home requires the link: the home reads it over the
// link, never over the network).
func LoopbackOnlyWhen(required func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if required != nil && required() && !loopbackPeer(r) {
				http.Error(w, "link_required: this machine's streams go on its cluster link", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func loopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
