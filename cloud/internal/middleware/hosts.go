package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Epic 50 story 50.32: the api role's two listeners, the user one (api, app, maktaba.tv) and
// the operator console's (admin.maktaba.tv only), each answering only its own names, with its
// own headers; the console's also refuses cross-origin requests.

// probePaths answer on any Host: the node's own probes and scrapes (deploy scripts, the
// edge's health checks, Prometheus) call the loopback address, and these carry no user data.
var probePaths = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// requestHost is r's Host, lowercased, without a port.
func requestHost(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}

// OnlyHosts answers 404 (the body of any unknown route, so it says nothing about what is
// served elsewhere) to a request whose Host is not one of hosts; empty hosts allows any.
// A wrong Host is logged at most once a minute: on the admin listener it means a Tunnel
// ingress rule pointed at the wrong port.
func OnlyHosts(logger *slog.Logger, listener string, hosts []string) func(http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, h := range hosts {
		allow[strings.ToLower(h)] = true
	}
	var mu sync.Mutex
	var last time.Time
	return func(next http.Handler) http.Handler {
		if len(allow) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if allow[requestHost(r)] || probePaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			mu.Lock()
			due := time.Since(last) >= time.Minute
			if due {
				last = time.Now()
			}
			mu.Unlock()
			if due {
				logger.Error("request for a name this listener does not serve", "listener", listener,
					"host", requestHost(r), "path", r.URL.Path, "event", "wrong_host")
			}
			http.NotFound(w, r)
		})
	}
}

// StaffHeader is the header a state-changing console request carries besides its Origin.
const StaffHeader = "X-Maktaba-Staff"

// AdminSameOrigin is the console listener's CSRF and CORS rule: no CORS at all (a preflight
// is 403), and a request that changes anything must carry Origin: origin and
// X-Maktaba-Staff: 1, or it is 403 bad_origin before any handler runs.
func AdminSameOrigin(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodOptions:
				w.WriteHeader(http.StatusForbidden)
				return
			case http.MethodGet, http.MethodHead:
				next.ServeHTTP(w, r)
				return
			}
			if r.Header.Get("Origin") != origin || r.Header.Get(StaffHeader) != "1" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"bad_origin","message":"This request didn't come from the console."}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// APIHeaders are the headers of the user listener's JSON (story 50.32 §5, api.maktaba.tv).
func APIHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// AdminHeaders are the console listener's (story 50.32 §5, admin.maktaba.tv): the API's,
// plus nothing cached, nothing indexed, no referrer and no cross-origin use.
func AdminHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// WithResolvedClientIP stores resolve(r) as the request's client IP (GetClientIP), for a
// router whose rule is its own: the relay's rightmost X-Forwarded-For entry from Caddy.
// The access log then prints it instead of the TCP peer.
func WithResolvedClientIP(resolve func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyClientIP, resolve(r))))
		})
	}
}
