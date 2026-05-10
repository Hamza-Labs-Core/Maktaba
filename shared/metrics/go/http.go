package metrics

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config drives the /metrics listener. Localhost-only by default
// (Story 21.2 AC-4); set Public=true to expose on a non-loopback
// interface, in which case BearerToken is required.
type Config struct {
	Bind        string
	Public      bool
	BearerToken string
}

// NewHandler returns a configured /metrics http.Handler. Returns an
// error when Public=true without a token so a misconfiguration fails
// at startup, never silently exposes /metrics unauthenticated.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Public && cfg.BearerToken == "" {
		return nil, errors.New("public /metrics requires a bearer token")
	}
	h := promhttp.HandlerFor(Reg(), promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		ProcessStartTime:  time.Now(),
	})
	if cfg.Public {
		h = bearerWrap(cfg.BearerToken, h)
	}
	return h, nil
}

// bearerWrap rejects any request whose Authorization header doesn't
// match `Bearer <token>` byte-for-byte. The compare is constant-time
// to deny timing oracles.
func bearerWrap(token string, next http.Handler) http.Handler {
	prefix := []byte("Bearer ")
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		bs := []byte(got)
		if len(bs) <= len(prefix) || subtle.ConstantTimeCompare(bs[:len(prefix)], prefix) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare(bs[len(prefix):], want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
