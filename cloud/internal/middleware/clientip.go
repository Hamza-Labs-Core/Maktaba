package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"
)

const (
	ctxKeyClientIP ctxKey = iota + 100
	ctxKeySessionID
)

// ClientIP resolves the address a request came from (Epic 50 story 50.31). It is the TCP
// peer, port stripped, unless that peer is in trusted (the local cloudflared, or Caddy after
// its origin checks): then CF-Connecting-IP wins when it holds exactly one valid IP. A
// header from any other peer was written by the client and is ignored, so a client can't
// pick its own rate-limit key, blocklist entry or audit address. X-Forwarded-For is never
// read here (the relay's own clientIP reads the one Caddy appends).
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer := peerIP(r)
	v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	if v == "" || !peer.IsValid() {
		return peerString(peer)
	}
	if !inPrefixes(peer, trusted) {
		ignoredHeader(peer)
		return peerString(peer)
	}
	ip, err := netip.ParseAddr(v)
	if err != nil || ip.Zone() != "" {
		return peerString(peer)
	}
	return ip.Unmap().String()
}

func peerIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return ip.WithZone("").Unmap()
}

func peerString(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	return ip.String()
}

func inPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// lastIgnored throttles the debug line for an ignored header to one a minute.
var lastIgnored atomic.Int64

func ignoredHeader(peer netip.Addr) {
	now := time.Now().Unix()
	last := lastIgnored.Load()
	if now-last < 60 || !lastIgnored.CompareAndSwap(last, now) {
		return
	}
	slog.Debug("CF-Connecting-IP from an untrusted peer ignored", "peer", peer.String())
}

// WithClientIP resolves the client address once per request and stashes it for handlers
// (sessions, audit rows, abuse signals, Turnstile) and limiters. trusted is
// server.trusted_proxies; empty means the TCP peer always.
func WithClientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxKeyClientIP, ClientIP(r, trusted))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClientIP returns the address WithClientIP resolved, or "" when the
// middleware did not run.
func GetClientIP(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyClientIP).(string)
	return v
}

// WithSessionID stashes the refresh session an access token came from.
func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeySessionID, id)
}

// GetSessionID returns the session id RequireUser stashed, or "".
func GetSessionID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySessionID).(string)
	return v
}
