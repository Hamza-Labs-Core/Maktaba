package placement

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// HeaderPlacement tells a client (and the tests) where a node served a request.
const HeaderPlacement = "X-Maktaba-Placement"

// Dispatcher is a full node's front: it finds the route a request names and serves it
// by its class — local and any here (any only while the schema guard says the node's
// code fits), home through the proxy. A path no route matches goes to the home.
type Dispatcher struct {
	Routes chi.Routes
	Table  Table
	// Fixed classes paths outside the router (the public mux's /healthz, the JWKS).
	Fixed map[string]Class
	Local http.Handler
	Proxy http.Handler
	// Paused is the schema guard's verdict ("" = the node's code fits).
	Paused func() string
	// Served counts each request by its class and status (story 35.15: the node's
	// metrics; nil: not counted).
	Served func(class Class, status int)
}

// ClassOf is the class a request is served by.
func (d *Dispatcher) ClassOf(r *http.Request) Class {
	if c, ok := d.Fixed[r.URL.Path]; ok {
		return c
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	pattern := d.Routes.Find(chi.NewRouteContext(), method, r.URL.Path)
	if pattern == "" {
		return Home
	}
	c, _ := d.Table.Classify(method, pattern)
	return c
}

type classKey struct{}

// WithClass is ctx served by class c (the dispatcher's mark; tests set it too).
func WithClass(ctx context.Context, c Class) context.Context {
	return context.WithValue(ctx, classKey{}, c)
}

// ClassFrom is the class the dispatcher served a request by ("" outside a node).
func ClassFrom(ctx context.Context) Class {
	c, _ := ctx.Value(classKey{}).(Class)
	return c
}

func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := d.ClassOf(r)
	w.Header().Set(HeaderPlacement, string(c))
	r = r.WithContext(WithClass(r.Context(), c))
	if d.Served != nil {
		sc := &statusCapture{ResponseWriter: w}
		w = sc
		defer func() {
			status := sc.status
			if status == 0 {
				status = http.StatusOK
			}
			d.Served(c, status)
		}()
	}
	switch c {
	case Local:
		d.Local.ServeHTTP(w, r)
	case Any:
		if d.Paused != nil {
			if reason := d.Paused(); reason != "" {
				WritePaused(w, reason)
				return
			}
		}
		d.Local.ServeHTTP(w, r)
	default:
		d.Proxy.ServeHTTP(w, r)
	}
}

// statusCapture keeps the status a handler wrote. No Write override (the bytes pass
// straight through); Flush and Unwrap keep streams and sockets working (the proxy's
// flushes and hijack go through http.ResponseController).
type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// WritePaused answers 503 {code: "paused", reason} (story 35.8 AC4).
func WritePaused(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "30")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.tv/problems/node-paused",
		"title": "this machine is waiting for its update", "status": http.StatusServiceUnavailable,
		"code": "paused", "reason": reason})
}
