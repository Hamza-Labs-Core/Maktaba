package middleware

import (
	"context"
	"net/http"
	"net/netip"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/clientip"
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
// read here (the relay's own clientIP reads the one Caddy appends). The resolver is
// shared/go/clientip's (Cloudflare), which the home's forwarded-header resolver sits beside.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	return clientip.Cloudflare(r, trusted)
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
