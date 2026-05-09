package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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
// goroutine, hit the admin port, verify /healthz and /readyz.
func TestServeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping under -short")
	}

	const publicAddr = "127.0.0.1:18081"
	const adminAddr = "127.0.0.1:19101"

	if !portFree(t, publicAddr) || !portFree(t, adminAddr) {
		t.Skipf("integration: %s or %s in use", publicAddr, adminAddr)
	}

	t.Setenv("MAKTABA_HTTP_ADDR", publicAddr)
	t.Setenv("MAKTABA_ADMIN_ADDR", adminAddr)
	t.Setenv("MAKTABA_HEALTH_WARM", "0s")
	t.Setenv("MAKTABA_GRPC_PEERS", "")
	t.Setenv("MAKTABA_ENV", "test")

	done := make(chan error, 1)
	go func() { done <- runServe() }()

	t.Cleanup(func() {
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("runServe did not exit within 5 s after SIGINT")
		}
	})

	if err := waitForPort(adminAddr, 2*time.Second); err != nil {
		t.Fatalf("admin port did not come up: %v", err)
	}

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

func portFree(t *testing.T, addr string) bool {
	t.Helper()
	conn, err := (&http.Client{Timeout: 100 * time.Millisecond}).Get("http://" + addr + "/")
	if err == nil {
		_ = conn.Body.Close()
		return false
	}
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
