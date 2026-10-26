package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

const (
	ctxKeyClientIP ctxKey = iota + 100
	ctxKeySessionID
)

// ClientIP resolves the address a request came from. Without
// trustProxy it is the TCP peer, port stripped — a client cannot pick
// its own rate-limit key by sending a header. With trustProxy (the api
// sits behind Cloudflare or a reverse proxy that overwrites these
// headers) CF-Connecting-IP wins, then the left-most X-Forwarded-For.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); v != "" && net.ParseIP(v) != nil {
			return v
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			first := strings.TrimSpace(strings.Split(v, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}

// WithClientIP resolves the client address once per request and stashes
// it for handlers (sessions, audit rows, abuse signals) and limiters.
func WithClientIP(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxKeyClientIP, ClientIP(r, trustProxy))
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
