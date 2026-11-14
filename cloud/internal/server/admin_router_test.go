package server

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
)

// T04: the console's origin is no CORS origin of the user API, and the admin listener answers
// any preflight 403.
func TestTheConsoleIsSameOriginOnly(t *testing.T) {
	if slices.Contains(DefaultOrigins(config.Config{}), "https://admin.maktaba.tv") {
		t.Fatal("DefaultOrigins lists the console's origin")
	}
	user := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, DefaultOrigins(config.Config{}), WithUserHosts(nil))
	req := httptest.NewRequest(http.MethodOptions, "/v1/account", nil)
	req.Header.Set("Origin", "https://admin.maktaba.tv")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	user.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("the user API allowed the console's origin: %v", rec.Header())
	}
	admin := NewAdminRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, "admin.maktaba.tv", "https://admin.maktaba.tv", nil)
	for _, origin := range []string{"https://admin.maktaba.tv", "https://app.maktaba.tv", "https://evil.test"} {
		req := httptest.NewRequest(http.MethodOptions, "/v1/admin/accounts", nil)
		req.Host = "admin.maktaba.tv"
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		admin.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("preflight from %s: %d %v", origin, rec.Code, rec.Header())
		}
	}
}

// The user listener's JSON carries §5's API headers; the relay's router (no WithUserHosts)
// does not, since it serves homes' own pages.
func TestUserListenerHeadersNotTheRelays(t *testing.T) {
	user := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil, WithUserHosts(nil))
	relay := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil)
	for name, c := range map[string]struct {
		r    http.Handler
		want string
	}{"user": {user, "default-src 'none'; frame-ancestors 'none'"}, "relay": {relay, ""}} {
		rec := httptest.NewRecorder()
		c.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if got := rec.Header().Get("Content-Security-Policy"); got != c.want {
			t.Errorf("%s CSP %q, want %q", name, got, c.want)
		}
	}
}

// story 50.32 §1: the relay's access log prints the client it resolves itself.
func TestWithClientIPFromReachesTheLog(t *testing.T) {
	var logs bytes.Buffer
	r := NewRouter(slog.New(slog.NewTextHandler(&logs, nil)), nil, nil, BuildInfo{}, nil,
		WithClientIPFrom(func(r *http.Request) string { return r.Header.Get("X-Test-Client") }))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("X-Test-Client", "203.0.113.44")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(logs.String(), "remote_ip=203.0.113.44") {
		t.Fatalf("the log: %s", logs.String())
	}
}

func TestIsAdminPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/admin/accounts": true, "/v1/staff/login": true, "/v1/admin": true, "/v1/staff": true,
		"/v1/administrator": false, "/v1/account": false, "/v1/adminx/y": false, "/healthz": false,
	} {
		if IsAdminPath(p) != want {
			t.Errorf("IsAdminPath(%q) = %v", p, !want)
		}
	}
}

func listenerFree(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// RunAll serves both listeners and stops both: when its context ends, and when one of them
// cannot listen (the other is shut down, not left serving).
func TestRunAll(t *testing.T) {
	hello := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	a, b := &http.Server{Addr: listenerFree(t), Handler: hello}, &http.Server{Addr: listenerFree(t), Handler: hello}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunAll(ctx, slog.New(slog.DiscardHandler), 2*time.Second, a, b) }()
	for _, s := range []*http.Server{a, b} {
		deadline := time.Now().Add(3 * time.Second)
		for {
			if res, err := http.Get("http://" + s.Addr + "/"); err == nil {
				_ = res.Body.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never answered", s.Addr)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a clean stop returned %v", err)
	}
	if _, err := http.Get("http://" + a.Addr + "/"); err == nil {
		t.Fatal("still serving after the stop")
	}

	// one listener's address is taken: RunAll returns its error and stops the other
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	good := &http.Server{Addr: listenerFree(t), Handler: hello}
	bad := &http.Server{Addr: taken.Addr().String(), Handler: hello}
	err = RunAll(context.Background(), slog.New(slog.DiscardHandler), 2*time.Second, good, bad)
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("RunAll with a taken address returned %v, want the listen error", err)
	}
	if _, err := http.Get("http://" + good.Addr + "/"); err == nil {
		t.Fatal("the good listener kept serving after the other failed")
	}
}

// RunAllDraining (the api pool, story 50.17): on shutdown the node goes unready at once and
// keeps serving for the delay, so the load balancer takes it out before its port closes.
func TestRunAllDraining(t *testing.T) {
	hello := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	a := &http.Server{Addr: listenerFree(t), Handler: hello}
	ctx, cancel := context.WithCancel(context.Background())
	var unready atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- RunAllDraining(ctx, slog.New(slog.DiscardHandler), 2*time.Second, 600*time.Millisecond,
			func() { unready.Store(true) }, a)
	}()
	get := func() error {
		res, err := http.Get("http://" + a.Addr + "/")
		if err == nil {
			_ = res.Body.Close()
		}
		return err
	}
	deadline := time.Now().Add(3 * time.Second)
	for get() != nil {
		if time.Now().After(deadline) {
			t.Fatal("never answered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopped := time.Now()
	cancel()
	time.Sleep(100 * time.Millisecond)
	if !unready.Load() {
		t.Fatal("not unready after the stop")
	}
	if err := get(); err != nil {
		t.Fatalf("closed during the drain delay: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("a clean stop returned %v", err)
	}
	if took := time.Since(stopped); took < 600*time.Millisecond {
		t.Fatalf("closed %s after the stop, before the delay", took)
	}
	if err := get(); err == nil {
		t.Fatal("still serving after the drain")
	}
}

// The admin listener's /readyz drains too (its own load balancer service, 50.32).
func TestAdminReadyzDrains(t *testing.T) {
	draining := false
	admin := NewAdminRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, "admin.maktaba.tv",
		"https://admin.maktaba.tv", nil, WithDraining(func() bool { return draining }))
	draining = true
	req := httptest.NewRequest("GET", "/readyz", nil)
	req.Host = "admin.maktaba.tv"
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "draining") {
		t.Fatalf("draining admin readyz: %d %s", rec.Code, rec.Body)
	}
}
