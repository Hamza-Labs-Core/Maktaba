package main

// A full node's end of the cluster link (Epic 35 story 35.16, design §18): the node's
// API process holds the machine's control channel (one per machine), carries its home
// calls on it, serves the streams the home asks for from this machine's streaming port,
// and lends the link to the machine's other processes on loopback: the supervisor's
// machine agent and the pipeline's agents reach the home's channel through it (their v2
// sealing kept end to end), and its updater the home's release files.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/link"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// startNodeLink starts the machine's link client (nil without a certificate folder: an
// older supervisor that does not say where node.json lives).
func startNodeLink(ctx context.Context, home, nodeID, certDir, streamURL string, logger *slog.Logger) *link.Client {
	if certDir == "" || home == "" {
		return nil
	}
	c := &link.Client{Home: home, NodeID: nodeID, Files: link.CertFiles{Dir: certDir}, Logger: logger}
	if u, err := url.Parse(streamURL); err == nil && u.Host != "" {
		c.Offer = spliceTo(u.Host)
	}
	go c.Run(ctx)
	return c
}

// spliceTo serves a stream the home asked for (a TV pull, a push relay's source) from
// this machine's streaming port: the home writes an HTTP request on the stream, the
// streaming role answers it, the bytes pass as they are.
func spliceTo(addr string) func(ctx context.Context, purpose, resource string, conn net.Conn) {
	return func(ctx context.Context, _, _ string, conn net.Conn) {
		d := net.Dialer{Timeout: 5 * time.Second}
		up, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			_ = conn.Close()
			return
		}
		splice(conn, up)
	}
}

// splice copies both ways until either side ends, then closes both.
func splice(a, b net.Conn) {
	var once sync.Once
	done := make(chan struct{})
	end := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
			close(done)
		})
	}
	go func() { _, _ = io.Copy(a, b); end() }()
	go func() { _, _ = io.Copy(b, a); end() }()
	<-done
}

// hopHeaders are not passed on to the home.
var hopHeaders = map[string]bool{"Connection": true, "Upgrade": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "X-Maktaba-Local-Token": true, "X-Forwarded-For": true,
	"X-Real-Ip": true, "Forwarded": true}

// serveLocalAgent is GET /api/cluster/link/local/agent from this machine's own processes
// (loopback only): their channel WebSocket goes to the home's /api/cluster/agent on a
// ws_proxy stream of the link. With the link down it answers 503 and the process dials
// the home directly (allowed while the home does not require the link).
func serveLocalAgent(c *link.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !mw.LocalCaller(r) {
			http.Error(w, "this machine's own processes only", http.StatusForbidden)
			return
		}
		if c == nil || !c.Connected() {
			http.Error(w, "the cluster link is not up", http.StatusServiceUnavailable)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			// a probe: the link is up, in the home's mode (a process on a plain session
			// moves to the link only when the home requires it)
			w.Header().Set("X-Maktaba-Link-Mode", c.Mode())
			http.Error(w, "a WebSocket upgrade is expected", http.StatusBadRequest)
			return
		}
		conn, err := c.OpenStream(r.Context(), link.PurposeWSProxy, "/api/cluster/agent")
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		out, _ := http.NewRequest(http.MethodGet, "/api/cluster/agent", nil)
		out.Host = r.Host
		for k, vs := range r.Header {
			if !hopHeaders[http.CanonicalHeaderKey(k)] {
				out.Header[k] = vs
			}
		}
		out.Header.Set("Connection", "Upgrade")
		out.Header.Set("Upgrade", "websocket")
		if err := out.Write(conn); err != nil {
			_ = conn.Close()
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		cconn, brw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			_ = conn.Close()
			return
		}
		if n := brw.Reader.Buffered(); n > 0 {
			b, _ := brw.Peek(n)
			if _, err := conn.Write(b); err != nil {
				_ = conn.Close()
				_ = cconn.Close()
				return
			}
		}
		splice(cconn, conn)
	}
}

// serveLocalFetch is GET /api/cluster/link/local/fetch?purpose=&path= with this
// machine's local token: one of the home's files (a release's, the wheelhouse's, a
// backup) read on a data stream of the link, a Range kept (a download resumes).
func serveLocalFetch(c *link.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !localToken(r) {
			http.Error(w, "the local token is required", http.StatusUnauthorized)
			return
		}
		if c == nil || !c.Connected() {
			w.Header().Set("X-Maktaba-Link", "down")
			http.Error(w, "the cluster link is not up", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		purpose, path := q.Get("purpose"), q.Get("path")
		if purpose == "" {
			purpose = link.PurposeArtifact
		}
		if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}
		out, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://home"+path, nil)
		if err != nil {
			http.Error(w, "not a path of the home's", http.StatusBadRequest) // never the request's words
			return
		}
		for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "Accept"} {
			if v := r.Header.Get(h); v != "" {
				out.Header.Set(h, v)
			}
		}
		resp, err := c.StreamHTTP(r.Context(), purpose, out)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, link.ErrNotConnected) || errors.Is(err, link.ErrLinkLost) {
				code = http.StatusServiceUnavailable
				w.Header().Set("X-Maktaba-Link", "down")
			}
			http.Error(w, err.Error(), code)
			return
		}
		defer resp.Body.Close()
		for k, vs := range resp.Header {
			if !hopHeaders[http.CanonicalHeaderKey(k)] {
				w.Header()[k] = vs
			}
		}
		// served on this machine's origin: never a page the home's stream made (proxiedType)
		w.Header().Set("Content-Type", proxiedType(resp.Header.Get("Content-Type")))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", proxiedCSP)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
