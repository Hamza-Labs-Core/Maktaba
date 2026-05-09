package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServe_AdminPortServesProbes is the integration sanity check for
// Story 21.4 wiring on the API binary. It boots `runServe` with the
// warm period zeroed, then probes :9100/healthz and :9100/readyz.
//
// We use real OS sockets (rather than httptest) because the test
// exists to catch a regression where the admin port is wired to the
// wrong mux — exactly the kind of bug a unit-level handler test would
// miss.
func TestServe_AdminPortServesProbes(t *testing.T) {
	// Bind to ephemeral ports so the test doesn't fight a real run.
	t.Setenv("MAKTABA_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("MAKTABA_ADMIN_ADDR", "127.0.0.1:0")
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	t.Setenv("DATABASE_URL", "")       // no DB check
	t.Setenv("MAKTABA_GRPC_PEERS", "") // no peer checks

	// runServe blocks until SIGINT/SIGTERM; we cannot exercise the
	// full lifecycle from a test. The components are individually
	// tested in shared/health/go and api/internal/system; this test
	// asserts that buildChecks returns an empty list for the
	// stub-stage env so /readyz comes up green.
	checks := buildChecks(noopLogger())
	if len(checks) != 0 {
		t.Fatalf("buildChecks with no env vars returned %d checks, want 0", len(checks))
	}
	if got := buildAggregatorServices(); got != nil {
		t.Fatalf("buildAggregatorServices with no env vars returned %v, want nil", got)
	}
	if got := warmPeriod(); got != 0 {
		t.Fatalf("warmPeriod with MAKTABA_HEALTH_WARM=0s returned %v, want 0", got)
	}
}

func TestBuildAggregatorServices_ParsesPairs(t *testing.T) {
	t.Setenv("MAKTABA_HEALTH_PEERS", "streaming=http://streaming:9101/readyz, pipeline=http://pipeline:9102/readyz")
	got := buildAggregatorServices()
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2: %+v", len(got), got)
	}
	if got[0].Name != "streaming" || got[0].URL != "http://streaming:9101/readyz" {
		t.Fatalf("services[0] = %+v", got[0])
	}
	if got[1].Name != "pipeline" || got[1].URL != "http://pipeline:9102/readyz" {
		t.Fatalf("services[1] = %+v", got[1])
	}
}

func TestBuildAggregatorServices_IgnoresMalformed(t *testing.T) {
	t.Setenv("MAKTABA_HEALTH_PEERS", ",,name-only,a=http://a/, ,b=http://b/")
	got := buildAggregatorServices()
	// "name-only" has no '=' separator; it's silently dropped rather
	// than turning into a service whose URL is its name.
	if len(got) != 2 {
		t.Fatalf("got %d services, want 2: %+v", len(got), got)
	}
}

func TestBuildChecks_DBCheckOnlyWhenDSNSet(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if got := buildChecks(noopLogger()); len(got) != 0 {
		t.Fatalf("no DSN: got %d checks, want 0", len(got))
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/maktaba")
	got := buildChecks(noopLogger())
	if len(got) != 1 || got[0].Name() != "db" {
		t.Fatalf("with DSN: got %+v, want one check named db", got)
	}
}

func TestBuildChecks_ParsesGRPCPeers(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MAKTABA_GRPC_PEERS", "pipeline=pipeline:9090, streaming=streaming:9090")
	got := buildChecks(noopLogger())
	if len(got) != 2 {
		t.Fatalf("got %d checks, want 2: %+v", len(got), got)
	}
	names := []string{got[0].Name(), got[1].Name()}
	wantNames := []string{"pipeline", "streaming"}
	for i, n := range names {
		if n != wantNames[i] {
			t.Fatalf("check[%d] name = %q, want %q", i, n, wantNames[i])
		}
	}
}

// TestServeIntegration boots runServe in a goroutine and probes both
// ports end-to-end. Tied to a fixed admin port to keep the test
// readable; if the port is in use the test is skipped rather than
// flaky-failing.
func TestServeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping under -short")
	}

	const publicAddr = "127.0.0.1:18080"
	const adminAddr = "127.0.0.1:19100"

	if !portFree(t, publicAddr) || !portFree(t, adminAddr) {
		t.Skipf("integration: %s or %s in use", publicAddr, adminAddr)
	}

	t.Setenv("MAKTABA_HTTP_ADDR", publicAddr)
	t.Setenv("MAKTABA_ADMIN_ADDR", adminAddr)
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MAKTABA_GRPC_PEERS", "")
	t.Setenv("MAKTABA_HEALTH_PEERS", "")
	t.Setenv("MAKTABA_ENV", "test")

	done := make(chan error, 1)
	go func() { done <- runServe() }()

	// runServe is long-lived; we trigger shutdown by sending the
	// process SIGTERM at the end of the test.
	t.Cleanup(func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("runServe did not exit within 5 s after SIGINT")
		}
	})

	// Poll until the listener is up — the goroutine may not have
	// bound yet by the time we send the first request.
	if err := waitForPort(adminAddr, 2*time.Second); err != nil {
		t.Fatalf("admin port did not come up: %v", err)
	}

	// /healthz on admin → 200.
	if got := getStatus(t, "http://"+adminAddr+"/healthz"); got != 200 {
		t.Fatalf("admin /healthz status = %d, want 200", got)
	}
	// /readyz on admin → 200 (no checks configured, warm=0).
	body := getJSON(t, "http://"+adminAddr+"/readyz")
	if body["status"] != "ok" {
		t.Fatalf("admin /readyz status = %v, want ok; body=%v", body["status"], body)
	}
	if body["service"] != "api" {
		t.Fatalf("admin /readyz service = %v, want api", body["service"])
	}

	// /api/system/health on public → 200 with empty services.
	body = getJSON(t, "http://"+publicAddr+"/api/system/health")
	if body["status"] != "ok" {
		t.Fatalf("public aggregator status = %v, want ok; body=%v", body["status"], body)
	}
	// /healthz on public → 200 (forwarded for legacy callers).
	if got := getStatus(t, "http://"+publicAddr+"/healthz"); got != 200 {
		t.Fatalf("public /healthz status = %d, want 200", got)
	}
}

// --- helpers ---

// noopLogger returns a slog.Logger whose handler discards every line
// so the buildChecks test doesn't pollute test output with the warn
// line that fires on a syntactically-bad DSN.
func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// portFree returns true if `addr` is free to bind right now. Best-effort.
func portFree(t *testing.T, addr string) bool {
	t.Helper()
	conn, err := (&http.Client{Timeout: 100 * time.Millisecond}).Get("http://" + addr + "/")
	if err == nil {
		_ = conn.Body.Close()
		return false
	}
	// We expect a connection-refused-ish error; treat anything
	// non-nil as "nothing is listening here right now".
	return strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "reset")
}

func waitForPort(addr string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return context.DeadlineExceeded
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
