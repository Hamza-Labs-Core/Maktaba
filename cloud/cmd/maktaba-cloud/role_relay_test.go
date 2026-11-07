package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	relayh "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/handlers/relay"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
)

// idleConn is a tunnel connection that carries nothing until it is closed.
type idleConn struct{ closed chan struct{} }

func (c *idleConn) ReadFrame() (relaypkg.Frame, error) {
	<-c.closed
	return relaypkg.Frame{}, errors.New("closed")
}
func (c *idleConn) WriteFrame(relaypkg.Frame) error { return nil }
func (c *idleConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func oneTunnel() (*relaypkg.Registry, *relaypkg.Tunnel) {
	reg := relaypkg.NewRegistry()
	tn := relaypkg.NewTunnel("srv-1", "acme", &idleConn{closed: make(chan struct{})})
	reg.Register("acme", tn)
	return reg, tn
}

// Story 50.03: the relay waits relay.shutdown_grace for its homes to move,
// never less than the server's own grace.
func TestRelayGrace(t *testing.T) {
	var cfg config.Config
	cfg.Relay.ShutdownGrace, cfg.Server.ShutdownGrace = 150*time.Second, 20*time.Second
	if got := relayGrace(cfg); got != 150*time.Second {
		t.Errorf("grace %s", got)
	}
	cfg.Server.ShutdownGrace = 200 * time.Second
	if got := relayGrace(cfg); got != 200*time.Second {
		t.Errorf("grace %s", got)
	}
}

// A stopping relay drains its tunnels (an idle older home's closes at once,
// and new tunnels are refused); with relay.drain.enabled = false it closes
// them all at once without draining.
func TestRelayDrain(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	var cfg config.Config
	cfg.Relay.DrainEnabled, cfg.Relay.DrainWithin, cfg.Relay.DrainDeadline, cfg.Relay.DrainRate = true, time.Second, 5*time.Second, 100

	reg, tn := oneTunnel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	relayDrain(logger, cfg, reg, relaypkg.NewTickets([]byte("0123456789abcdef0123456789abcdef")))(ctx)
	if !reg.Closing() || !tn.Draining() || reg.Live() != 0 {
		t.Errorf("drained: closing=%v draining=%v live=%d", reg.Closing(), tn.Draining(), reg.Live())
	}

	cfg.Relay.DrainEnabled = false
	reg, tn = oneTunnel()
	relayDrain(logger, cfg, reg, nil)(ctx)
	select {
	case <-tn.Done():
	case <-time.After(time.Second):
		t.Fatal("drain off: the tunnel was not closed")
	}
	if tn.Draining() {
		t.Error("drain off: DRAIN was sent")
	}
}

// Story 50.04: the process's directory name, and relay.hold_max = 0 turning
// holding off.
func TestRelayIDAndHoldMax(t *testing.T) {
	var cfg config.Config
	cfg.Relay.ID = "a-blue"
	if relayIDOf(cfg) != "a-blue" {
		t.Error("relay.id")
	}
	cfg.Relay.ID = ""
	if relayIDOf(cfg) == "" {
		t.Error("no fallback name")
	}
	if holdMax(0) >= 0 || holdMax(15*time.Second) != 15*time.Second {
		t.Error("holdMax")
	}
}

// Story 50.05: the relay's readiness is lenient with its cache, strict
// without it or when asked.
func TestRelayReadiness(t *testing.T) {
	var cfg config.Config
	cache := &relayh.HomeCache{}
	if len(relayReadiness(cfg, cache)) != 1 {
		t.Error("lenient by default")
	}
	if relayReadiness(cfg, nil) != nil {
		t.Error("no cache: strict")
	}
	cfg.Relay.ReadyzStrict = true
	if relayReadiness(cfg, cache) != nil {
		t.Error("relay.readyz_strict")
	}
}

// Story 50.06: a relay whose registry is shutting down answers /readyz 503
// draining, so the edge sends new tunnels and requests to the other process.
func TestRelayReadyzWhileDraining(t *testing.T) {
	reg := relaypkg.NewRegistry()
	r := server.NewRouter(slog.New(slog.DiscardHandler), nil, nil, server.BuildInfo{}, nil, relayRouterOptions(config.Config{}, nil, reg)...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reg.Shutdown(ctx, relaypkg.DrainOptions{}, 1, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "draining") {
		t.Errorf("draining relay: %d %s", rec.Code, rec.Body.String())
	}
}

// Story 50.08: the rehearsal's bad build fails its readiness badAfter seconds
// after it starts; a normal build never does.
func TestBadAfterFailsReadiness(t *testing.T) {
	old := badAfter
	defer func() { badAfter = old }()
	badAfter = 1
	reg := relaypkg.NewRegistry()
	r := server.NewRouter(slog.New(slog.DiscardHandler), nil, nil, server.BuildInfo{}, nil, relayRouterOptions(config.Config{}, nil, reg)...)
	probe := func() string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Body.String()
	}
	if strings.Contains(probe(), "draining") {
		t.Error("draining at once")
	}
	time.Sleep(1100 * time.Millisecond)
	if !strings.Contains(probe(), "draining") {
		t.Error("not failing after badAfter")
	}
}
