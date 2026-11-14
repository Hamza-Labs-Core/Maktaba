package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
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

// Story 50.15: with direct access off nothing is built; on, the relay's handler and the
// worker's jobs are, each CA's account key made on first use.
func TestDirectIsBuiltOnlyWhenOn(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var cfg config.Config
	if directHandler(cfg, nil, logger) != nil || directJobs(cfg, nil, logger) != nil {
		t.Fatal("built while off")
	}
	dir := t.TempDir()
	cfg.Direct = config.DirectConfig{Enabled: true, Domains: []string{"direct-a.example"}, Budget: 45,
		ACME: map[string]config.DirectACME{
			"letsencrypt": {Directory: "https://acme.test/directory", AccountKey: filepath.Join(dir, "le.key")},
			"zerossl":     {Directory: ""}, // not configured: left out
		}}
	pool := &db.Pool{}
	if directHandler(cfg, pool, logger) == nil {
		t.Fatal("no relay handler while on")
	}
	j := directJobs(cfg, pool, logger)
	if j == nil {
		t.Fatal("no worker jobs while on")
	}
	if st, err := os.Stat(filepath.Join(dir, "le.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the account key: %v %v", st, err)
	}
}

// Story 50.19: the relay's plans follow relay.plans.enforce and relay.stream_limit.enabled,
// and count streams under this relay's id.
func TestRelayPlansFollowTheConfig(t *testing.T) {
	cfg := config.Config{}
	cfg.Relay.PlansEnforce, cfg.Relay.StreamLimitEnabled = true, false
	p := relayPlans(cfg, nil, "blue")
	if !p.Enforce || p.StreamLimit || p.Streams == nil {
		t.Fatalf("%+v", p)
	}
}

type countingSweeper struct{ n atomic.Int32 }

func (c *countingSweeper) Sweep(context.Context) (int64, error) { c.n.Add(1); return 1, nil }

// Story 50.19: the worker sweeps silent relayed streams on its interval until it stops.
func TestTheWorkerSweepsStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &countingSweeper{}
	done := make(chan struct{})
	go func() {
		sweepStreams(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), s, 5*time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for s.n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if s.n.Load() < 2 {
		t.Fatalf("swept %d times", s.n.Load())
	}
}

// Story 50.16: with relay.pool.enabled the relay writes its member row before it takes tunnels
// and hands the tunnel server a rebalancer for its member; off, there is none.
func TestRelayPoolWiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := relaypkg.NewMemPoolStore()
	var cfg config.Config
	cfg.Relay.PublicHost = "relay.test"
	if rb, err := relayPool(ctx, slog.New(slog.DiscardHandler), cfg, store, relaypkg.NewRegistry(), nil); rb != nil || err != nil {
		t.Fatalf("pool off: %v %v", rb, err)
	}
	cfg.Relay.PoolEnabled, cfg.Relay.ID, cfg.Relay.PoolMember = true, "r2-blue", "r2"
	cfg.Relay.PoolPublicIP, cfg.Relay.PeerAddr, cfg.Relay.PoolCapacityMbps = "203.0.113.12", "10.0.1.12:8444", 1500
	cfg.Relay.RebalanceEvery, cfg.Relay.RebalanceRate = time.Minute, 20
	rb, err := relayPool(ctx, slog.New(slog.DiscardHandler), cfg, store, relaypkg.NewRegistry(), nil)
	if err != nil || rb == nil {
		t.Fatalf("pool on: %v", err)
	}
	rows, _ := store.Relays(ctx)
	if len(rows) != 1 || rows[0].ID != "r2" || rows[0].PublicIP != "203.0.113.12" || rows[0].PrivateIP != "10.0.1.12" ||
		rows[0].CapacityMbps != 1500 || !rows[0].Healthy() {
		t.Fatalf("member row: %+v", rows)
	}
	if rb.Pool.Self != "r2" || rb.Hosts != "{id}.relay.test" || rb.Payload(relaypkg.Assignment{Primary: "r2"}).Primary.Host != "r2.relay.test" {
		t.Fatalf("rebalancer: %+v", rb)
	}
}
