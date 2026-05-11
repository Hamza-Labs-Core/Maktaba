// Package relay (handlers) is the HTTP edge of the relay. It takes
// incoming requests at <slug>.relay.maktaba.app and proxies them
// across the live WebSocket tunnel registered by the matching server.
package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

type Deps struct {
	Registry  *relaypkg.Registry
	Servers   *stores.Servers
	Meter     *billing.Meter
	PublicHost string // e.g. "relay.maktaba.app"
}

// Handler returns the http.Handler mounted by the relay role at "/".
// It extracts the slug from the request Host header, looks up the
// tunnel, and proxies through it. Cross-cutting (bandwidth metering,
// plan gating) hooks in around the call.
func (d *Deps) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := slugFromHost(r.Host, d.PublicHost)
		if slug == "" {
			writeErr(w, http.StatusNotFound, "no_slug", "unknown subdomain")
			return
		}
		sv, err := d.Servers.BySlug(r.Context(), slug)
		if err != nil {
			writeErr(w, http.StatusNotFound, "no_server", "")
			return
		}
		t, err := d.Registry.Lookup(slug)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "server_offline", "")
			return
		}
		// Tier gate — Free tier capped at ~50 MB/day inbound at edge.
		if d.Meter != nil {
			if err := d.Meter.Allow(r.Context(), sv); err != nil {
				writeErr(w, http.StatusPaymentRequired, "tier_limit", err.Error())
				return
			}
		}

		ctx, cancel := context.WithTimeout(middleware.WithServerID(r.Context(), sv.ID), 2*time.Minute)
		defer cancel()
		req := r.Clone(ctx)
		// Strip hop-by-hop headers per RFC 7230 §6.1.
		stripHopByHop(req.Header)
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-For", clientIP(r))
		req.Header.Set("X-Relayed-By", "maktaba-cloud")

		resp, err := t.Proxy(ctx, req)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "tunnel_error", err.Error())
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		n, _ := io.Copy(w, resp.Body)

		if d.Meter != nil {
			d.Meter.Record(ctx, sv, int64(reqBytes(r)), n)
		}
	})
}

func slugFromHost(host, publicHost string) string {
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	if !strings.HasSuffix(host, "."+publicHost) {
		return ""
	}
	return strings.TrimSuffix(host, "."+publicHost)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
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
	n += int(r.ContentLength)
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

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind, "message": msg})
}
