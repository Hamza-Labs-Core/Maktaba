package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
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
