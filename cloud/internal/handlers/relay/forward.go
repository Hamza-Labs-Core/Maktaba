package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"time"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
)

// Forwarding to the relay process that holds a home's tunnel (story 50.04).

// peerConnectTimeout: a peer that does not take the connection in this long
// is treated as gone (its directory row is stale) and the request is held.
const peerConnectTimeout = time.Second

var defaultPeerTransport = &http.Transport{
	DialContext:           (&net.Dialer{Timeout: peerConnectTimeout}).DialContext,
	MaxIdleConnsPerHost:   64,
	IdleConnTimeout:       90 * time.Second,
	ResponseHeaderTimeout: DefaultHeadTimeout + 5*time.Second,
}

// forwardIfElsewhere forwards r to the peer the directory names for slug,
// when there is one newer than the local tunnel t (nil: none here). It
// reports whether the request was answered.
func (d *Deps) forwardIfElsewhere(w http.ResponseWriter, r *http.Request, slug string, t *relaypkg.Tunnel) bool {
	if d.Directory == nil {
		return false
	}
	p, ok := d.Directory.Lookup(r.Context(), slug)
	if !ok || t != nil && p.Seq <= t.Seq() {
		return false
	}
	if d.forward(w, r, slug, p) {
		return true
	}
	d.Directory.Forget(slug)
	return false
}

// forward proxies r to peer p over its peer listener, signed and marked as
// one hop, streaming both ways. It reports false, having written nothing,
// when the peer could not be reached.
func (d *Deps) forward(w http.ResponseWriter, r *http.Request, slug string, p relaypkg.Peer) bool {
	var unreachable atomic.Bool
	rt := d.PeerTransport
	if rt == nil {
		rt = defaultPeerTransport
	}
	cw := &countingWriter{ResponseWriter: w}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = p.Addr
			pr.Out.Host = pr.In.Host
			// the client's true address, as this relay saw it (the peer
			// trusts a private hop's X-Forwarded-For)
			pr.Out.Header.Set("X-Forwarded-For", clientIP(pr.In))
			pr.Out.Header.Set(relaypkg.HeaderHop, "1")
			pr.Out.Header.Set(relaypkg.HeaderPeerAuth, d.PeerAuth.Sign(d.RelayID, slug))
		},
		Transport:     rt,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if cw.wrote.Load() {
				return // mid-stream: the client sees a broken response
			}
			if isDialError(err) {
				unreachable.Store(true)
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			writeErr(w, r, http.StatusBadGateway, "tunnel_error", err.Error())
		},
	}
	rp.ServeHTTP(cw, r)
	if unreachable.Load() {
		d.logger().Warn("relay: the peer holding a tunnel did not answer", "peer", p.RelayID, "slug", slug, "event", "relay_peer_unreachable")
		return false
	}
	peerStats.forwarded.Add(1)
	peerStats.forwardedBytes.Add(cw.n.Load())
	return true
}

func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

type countingWriter struct {
	http.ResponseWriter
	n     atomic.Int64
	wrote atomic.Bool
}

func (c *countingWriter) WriteHeader(code int) {
	c.wrote.Store(true)
	c.ResponseWriter.WriteHeader(code)
}

func (c *countingWriter) Write(b []byte) (int, error) {
	c.wrote.Store(true)
	n, err := c.ResponseWriter.Write(b)
	c.n.Add(int64(n))
	return n, err
}

func (c *countingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// peerStats are the forwarding and holding counters served on /metrics.
var peerStats = &struct {
	forwarded, forwardedBytes, authFailed atomic.Int64
	mu                                    sync.Mutex
	held                                  map[string]int64
}{held: map[string]int64{}}

func countHeld(result string) {
	peerStats.mu.Lock()
	peerStats.held[result]++
	peerStats.mu.Unlock()
}

// Hold results (relay_held_total{result}).
const (
	HeldFound    = "found"     // the home came back (here or on a peer) in time
	HeldTimeout  = "timeout"   // it did not: 503 home_reconnecting
	HeldRefused  = "full"      // the hold limits were reached: 503 at once
	HeldCanceled = "cancelled" // the client left first
)

// WriteMetrics renders the forwarding and holding counters.
func WriteMetrics(w io.Writer) error {
	peerStats.mu.Lock()
	held := map[string]int64{}
	for k, v := range peerStats.held {
		held[k] = v
	}
	peerStats.mu.Unlock()
	var err error
	p := func(format string, a ...any) {
		if err == nil {
			_, err = fmt.Fprintf(w, format, a...)
		}
	}
	p("# HELP maktaba_relay_forwarded_total Requests forwarded to the relay process holding the home's tunnel.\n")
	p("# TYPE maktaba_relay_forwarded_total counter\n")
	p("maktaba_relay_forwarded_total %d\n", peerStats.forwarded.Load())
	p("# HELP maktaba_relay_forwarded_bytes_total Response bytes of forwarded requests (capacity only; metered by the owner).\n")
	p("# TYPE maktaba_relay_forwarded_bytes_total counter\n")
	p("maktaba_relay_forwarded_bytes_total %d\n", peerStats.forwardedBytes.Load())
	p("# HELP maktaba_relay_held_total Requests held while their home reconnected, by result.\n")
	p("# TYPE maktaba_relay_held_total counter\n")
	for _, r := range []string{HeldFound, HeldTimeout, HeldRefused, HeldCanceled} {
		p("maktaba_relay_held_total{result=%q} %d\n", r, held[r])
	}
	p("# HELP maktaba_relay_peer_auth_failed_total Peer requests refused for a missing, stale or bad signature.\n")
	p("# TYPE maktaba_relay_peer_auth_failed_total counter\n")
	p("maktaba_relay_peer_auth_failed_total %d\n", peerStats.authFailed.Load())
	return err
}
