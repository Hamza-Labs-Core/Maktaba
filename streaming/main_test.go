package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgpool"
)

func TestBuildChecks_NoPeers(t *testing.T) {
	t.Setenv("MAKTABA_GRPC_PEERS", "")
	if got := buildChecks(); len(got) != 0 {
		t.Fatalf("got %d checks, want 0", len(got))
	}
}

func TestBuildChecks_ParsesPeers(t *testing.T) {
	t.Setenv("MAKTABA_GRPC_PEERS", "api=api:9090, pipeline=pipeline:9090")
	got := buildChecks()
	if len(got) != 2 {
		t.Fatalf("got %d checks, want 2: %+v", len(got), got)
	}
	if got[0].Name() != "api" || got[1].Name() != "pipeline" {
		t.Fatalf("check names = %s/%s, want api/pipeline", got[0].Name(), got[1].Name())
	}
}

func TestWarmPeriod(t *testing.T) {
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	if got := warmPeriod(); got != 0 {
		t.Fatalf("warmPeriod = %v, want 0", got)
	}
	t.Setenv("MAKTABA_HEALTH_WARM", "5s")
	if got := warmPeriod(); got != 5*time.Second {
		t.Fatalf("warmPeriod = %v, want 5s", got)
	}
	t.Setenv("MAKTABA_HEALTH_WARM", "garbage")
	if got := warmPeriod(); got != 30*time.Second {
		t.Fatalf("warmPeriod with garbage = %v, want 30s default", got)
	}
}

// TestServeIntegration mirrors the api test: boot runServe in a
// goroutine, hit the admin port, verify /healthz and /readyz, then the
// public port's /healthz.
func TestServeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping under -short")
	}

	// the gRPC default is ":9050" (every interface): loopback here
	t.Setenv("MAKTABA_STREAMING_GRPC_ADDR", "127.0.0.1:0")
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	t.Setenv("MAKTABA_GRPC_PEERS", "")
	t.Setenv("MAKTABA_ENV", "test")

	publicAddr, adminAddr := startServe(t)

	if got := getStatus(t, "http://"+adminAddr+"/healthz"); got != 200 {
		t.Fatalf("admin /healthz status = %d, want 200", got)
	}
	body := getJSON(t, "http://"+adminAddr+"/readyz")
	if body["service"] != "streaming" {
		t.Fatalf("admin /readyz service = %v, want streaming", body["service"])
	}
	if body["status"] != "ok" {
		t.Fatalf("admin /readyz status = %v, want ok; body=%v", body["status"], body)
	}
	if got := getStatus(t, "http://"+publicAddr+"/healthz"); got != 200 {
		t.Fatalf("public /healthz status = %d, want 200", got)
	}
}

// startServe boots runServe in a goroutine with its public and admin servers
// on 127.0.0.1:0 and returns the addresses they bound. It returns once both
// sockets are bound (onListening), so the first request to either connects:
// no port is picked, freed and handed over (another process could take it in
// between), and no port is polled for (the public one could still be unbound
// when the admin one answers). Cleanup stops runServe with SIGINT.
func startServe(t *testing.T) (publicAddr, adminAddr string) {
	t.Helper()
	t.Setenv("MAKTABA_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("MAKTABA_ADMIN_ADDR", "127.0.0.1:0")

	type bound struct{ public, admin string }
	ready := make(chan bound, 1)
	onListening = func(public, admin net.Addr) { ready <- bound{public.String(), admin.String()} }
	t.Cleanup(func() { onListening = nil })

	done := make(chan error, 1)
	go func() { done <- runServe() }()

	select {
	case b := <-ready:
		// runServe installs its signal handler before it binds: this SIGINT
		// stops runServe, never the test binary
		t.Cleanup(func() {
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Signal(os.Interrupt)
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("runServe after SIGINT: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("runServe did not exit within 5 s after SIGINT")
			}
		})
		return b.public, b.admin
	case err := <-done:
		t.Fatalf("runServe returned before listening: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("runServe did not bind its listeners within 30 s")
	}
	return "", ""
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}

// The home's database away at start (lazy): the streaming service's pool still opens,
// with its own cap, not one connection per CPU.
func TestBuildProbeStoresCapsThePool(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://maktaba@127.0.0.1:1/maktaba?sslmode=disable&connect_timeout=2")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, _, pool, up, closeFn := buildProbeStores(context.Background(), logger, true)
	defer closeFn()
	if pool == nil || up {
		t.Fatalf("pool %v up %v: want a pool, home down", pool, up)
	}
	if got := pool.Config().MaxConns; got != pgpool.Serve {
		t.Fatalf("MaxConns = %d, want %d", got, pgpool.Serve)
	}
	if got := pool.Config().MaxConnIdleTime; got != pgpool.IdleTime {
		t.Fatalf("MaxConnIdleTime = %v, want %v", got, pgpool.IdleTime)
	}
}
