// Package relay (handlers) is the HTTP edge of the relay. It takes
// incoming requests at <slug>.relay.maktaba.tv and proxies them
// across the live WebSocket tunnel registered by the matching server.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	metricspkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/metrics"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/privacy"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// ServerLookup finds the server a relay subdomain names
// (*stores.Servers satisfies it).
type ServerLookup interface {
	BySlug(ctx context.Context, slug string) (stores.Server, error)
}

// Metering is the per-home bandwidth gate and counter (*billing.Meter
// satisfies it).
type Metering interface {
	Allow(ctx context.Context, sv stores.Server) error
	Record(ctx context.Context, sv stores.Server, in, out int64)
}

// DefaultHeadTimeout bounds the wait for the home's response head.
const DefaultHeadTimeout = 60 * time.Second

// meterEvery is how many response bytes are counted at once while a
// response streams, so a long video counts (and meets the plan's gate)
// while it plays, not only when it ends.
const meterEvery = 1 << 20

// clientWriteIdle is how long one write to the client may block (a
// client that stopped reading) before the response is abandoned; each
// chunk renews it, so a stream that flows is never cut.
const clientWriteIdle = 2 * time.Minute

type Deps struct {
	Registry *relaypkg.Registry
	// Servers resolves slugs unless Lookup is set.
	Servers    *stores.Servers
	Lookup     ServerLookup
	Meter      Metering
	PublicHost string // e.g. "relay.maktaba.tv"
	// Editions serves the web app's player and remote editions at /play/ and /manage/ on
	// a home's relay address (Epic 42.9); nil: those paths go to the home like any other.
	Editions *Editions
	// HeadTimeout bounds the wait for the home's response head (0 ⇒
	// DefaultHeadTimeout). The body is bounded by its client alone.
	HeadTimeout time.Duration

	// Collector records aggregate-only traffic metrics (Epic 30). Nil
	// disables relay analytics collection. CountryHeader names the edge
	// header carrying the request's country ("" ⇒ CF-IPCountry).
	Collector     *metricspkg.Collector
	CountryHeader string
}

func (d *Deps) lookup() ServerLookup {
	if d.Lookup != nil {
		return d.Lookup
	}
	return d.Servers
}

// Handler returns the http.Handler mounted by the relay role at "/".
// It extracts the slug from the request Host header, looks up the
// tunnel, and proxies through it. Cross-cutting (bandwidth metering,
// plan gating) hooks in around the call.
func (d *Deps) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := slugFromHost(r.Host, d.PublicHost)
		if slug == "" {
			writeErr(w, r, http.StatusNotFound, "no_slug", "unknown subdomain")
			return
		}
		sv, err := d.lookup().BySlug(r.Context(), slug)
		if err != nil {
			writeErr(w, r, http.StatusNotFound, "no_server", "")
			return
		}
		// the cloud's own files for a claimed home's player and remote (42.9): not the
		// home's bytes, so neither metered nor needing the home online (the app then says
		// it cannot reach the home)
		if d.Editions.Serves(r) {
			d.Editions.ServeHTTP(w, r)
			return
		}
		t, err := d.Registry.Lookup(slug)
		if err != nil {
			writeErr(w, r, http.StatusBadGateway, "server_offline", "")
			return
		}
		// Tier gate: the plan's monthly relay allowance.
		if d.Meter != nil {
			if err := d.Meter.Allow(r.Context(), sv); err != nil {
				writeErr(w, r, http.StatusPaymentRequired, "tier_limit", err.Error())
				return
			}
		}

		// No deadline on the exchange: a video plays as long as its client
		// reads. Only the home's answer (the head) is bounded.
		ctx := middleware.WithServerID(r.Context(), sv.ID)
		req := r.Clone(ctx)
		// Strip hop-by-hop headers per RFC 7230 §6.1.
		stripHopByHop(req.Header)
		setForwarded(req.Header, r)
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Relayed-By", "maktaba-cloud")
		// only the home's own agent marks a request as relayed
		req.Header.Del("X-Maktaba-Via")
		// the cloud's own cookies never reach a home (a parent-domain one
		// would otherwise ride along to every home's subdomain)
		stripCloudCookies(req.Header)

		rc := http.NewResponseController(w)
		// the server's read/write timeouts are for requests, not streams:
		// the body is bounded per chunk below
		_ = rc.SetReadDeadline(time.Time{})
		_ = rc.SetWriteDeadline(time.Time{})

		headTimeout := d.HeadTimeout
		if headTimeout <= 0 {
			headTimeout = DefaultHeadTimeout
		}
		resp, err := t.ProxyWithin(ctx, req, headTimeout)
		if err != nil {
			if errors.Is(err, relaypkg.ErrHeadTimeout) {
				writeErr(w, r, http.StatusGatewayTimeout, "tunnel_timeout", err.Error())
				return
			}
			writeErr(w, r, http.StatusBadGateway, "tunnel_error", err.Error())
			return
		}
		// Closing before the home's EOF (the client left) stops the home.
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				if k == "Set-Cookie" && !hostOnlyCookie(v) {
					continue
				}
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_ = rc.Flush()

		in := int64(reqBytes(r))
		count := func(out int64) {
			if in == 0 && out == 0 {
				return
			}
			if d.Meter != nil {
				d.Meter.Record(ctx, sv, in, out)
			}
			// Aggregate-only relay analytics (Epic 30): no server id, no
			// user id.
			if d.Collector != nil {
				d.Collector.RecordBandwidth(in, out)
			}
			in = 0
		}
		var pending int64
		buf := make([]byte, 32*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				_ = rc.SetWriteDeadline(time.Now().Add(clientWriteIdle))
				if _, werr := w.Write(buf[:n]); werr != nil {
					break
				}
				// each chunk to the client at once: HLS and progressive
				// play start without waiting for a buffer to fill
				_ = rc.Flush()
				pending += int64(n)
				if pending >= meterEvery {
					count(pending)
					pending = 0
				}
			}
			if rerr != nil {
				break
			}
		}
		count(pending)
		if d.Collector != nil {
			// country from the edge header, IP never read or stored
			// (Story 30.2)
			d.Collector.RecordRequest(privacy.CountryFromRequest(r, d.CountryHeader))
		}
	})
}

// SlugFromHost is the exported view of slug extraction. The relay role
// uses it to tell tenant requests (a `<slug>.relay…` host, which must be
// proxied to the home server) from bare-relay-host control requests, so
// the relay's /metrics and /privacy endpoints never shadow a home
// server's own paths.
func SlugFromHost(host, publicHost string) string { return slugFromHost(host, publicHost) }

func slugFromHost(host, publicHost string) string {
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if !strings.HasSuffix(host, "."+publicHost) {
		return ""
	}
	return strings.TrimSuffix(host, "."+publicHost)
}

// cloudCookiePrefixes name the cloud's own cookies (maktaba_refresh, the
// OAuth flow's oauth_state / oauth_nonce / oauth_pkce). The homes' cookies
// are their own names (mkt_sess, mkt_csrf), host-only on their subdomain.
var cloudCookiePrefixes = []string{"maktaba_", "oauth_"}

func cloudCookie(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range cloudCookiePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// stripCloudCookies drops the cloud's cookies from a request bound for a
// home, keeping the home's own (its web sessions through the relay).
func stripCloudCookies(h http.Header) {
	vals := h.Values("Cookie")
	if len(vals) == 0 {
		return
	}
	h.Del("Cookie")
	var keep []string
	for _, v := range vals {
		for _, c := range strings.Split(v, ";") {
			c = strings.TrimSpace(c)
			name, _, _ := strings.Cut(c, "=")
			if c == "" || cloudCookie(name) {
				continue
			}
			keep = append(keep, c)
		}
	}
	if len(keep) > 0 {
		h.Set("Cookie", strings.Join(keep, "; "))
	}
}

// hostOnlyCookie reports whether a home's Set-Cookie stays on its own
// subdomain: one with a Domain attribute could cover the cloud's hosts or
// another home's, and is dropped. A cloud-named cookie is dropped too.
func hostOnlyCookie(setCookie string) bool {
	parts := strings.Split(setCookie, ";")
	name, _, _ := strings.Cut(strings.TrimSpace(parts[0]), "=")
	if cloudCookie(name) {
		return false
	}
	for _, a := range parts[1:] {
		k, _, _ := strings.Cut(strings.TrimSpace(a), "=")
		if strings.EqualFold(strings.TrimSpace(k), "domain") {
			return false
		}
	}
	return true
}

// forwardedHeaders are the client-address headers a client could set;
// the relay replaces them all with the one address it trusts.
var forwardedHeaders = []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded", "True-Client-Ip"}

// setForwarded sets X-Forwarded-For and X-Real-IP to the client's true
// address (clientIP), never a value the client sent: the home's rate
// limits and LAN checks read them.
func setForwarded(h http.Header, r *http.Request) {
	ip := clientIP(r)
	for _, k := range forwardedHeaders {
		h.Del(k)
	}
	h.Set("X-Forwarded-For", ip)
	h.Set("X-Real-IP", ip)
}

// clientIP is the client's address: the immediate peer, unless that peer
// is a reverse proxy on this host or network (loopback or private, e.g.
// Caddy in front of the relay), which appends the address it saw as the
// rightmost X-Forwarded-For entry. Entries further left are the client's
// own claims and are never trusted.
func clientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	ip := net.ParseIP(peer)
	if ip == nil || !ip.IsLoopback() && !ip.IsPrivate() {
		return peer
	}
	vs := r.Header.Values("X-Forwarded-For")
	if len(vs) == 0 {
		return peer
	}
	parts := strings.Split(vs[len(vs)-1], ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	if net.ParseIP(last) == nil {
		return peer
	}
	return last
}

func reqBytes(r *http.Request) int {
	// Approximate request size for metering — request line + headers
	// + content-length. We don't read the body twice; the tunnel does
	// the actual proxying.
	n := len(r.Method) + 1 + len(r.URL.RequestURI()) + len(" HTTP/1.1\r\n")
	for k, vs := range r.Header {
		for _, v := range vs {
			n += len(k) + 2 + len(v) + 2
		}
	}
	n += 2 // CRLF
	if r.ContentLength > 0 {
		n += int(r.ContentLength)
	}
	return n
}

var hopByHop = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func stripHopByHop(h http.Header) {
	for _, k := range hopByHop {
		h.Del(k)
	}
}

func writeErr(w http.ResponseWriter, r *http.Request, code int, kind, msg string) {
	if wantsHTML(r) {
		writePage(w, code, kind, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}
