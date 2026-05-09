package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLive_AlwaysOK(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NewLive("api").ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}

	var body struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, rr.Body.String())
	}
	if body.Status != "ok" || body.Service != "api" {
		t.Fatalf("body = %+v, want {status:ok service:api}", body)
	}
}

func TestReady_AllOK(t *testing.T) {
	t.Parallel()
	checks := []Check{
		CheckFunc{N: "db", F: func(context.Context) error { return nil }},
		CheckFunc{N: "pipeline", F: func(context.Context) error { return nil }},
	}
	r := NewReady("api", checks, 0)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp readyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("overall = %q, want ok", resp.Status)
	}
	for name, sub := range resp.Checks {
		if sub.Status != "ok" {
			t.Fatalf("check %s = %+v, want ok", name, sub)
		}
	}
}

// TC1 in the story: when a dependency fails, /readyz returns 503 and
// names the failing dep in the body. The corresponding /healthz still
// returns 200; that's covered by TestLive_AlwaysOK.
func TestReady_OneCheckFails(t *testing.T) {
	t.Parallel()
	wantErr := "connection refused"
	checks := []Check{
		CheckFunc{N: "db", F: func(context.Context) error { return errors.New(wantErr) }},
		CheckFunc{N: "pipeline", F: func(context.Context) error { return nil }},
	}
	r := NewReady("api", checks, 0)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	var resp readyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "degraded" {
		t.Fatalf("overall = %q, want degraded", resp.Status)
	}
	if got := resp.Checks["db"].Status; got != "fail" {
		t.Fatalf("db status = %q, want fail", got)
	}
	if got := resp.Checks["db"].Reason; !strings.Contains(got, wantErr) {
		t.Fatalf("db reason = %q, want it to mention %q", got, wantErr)
	}
	if got := resp.Checks["pipeline"].Status; got != "ok" {
		t.Fatalf("pipeline status = %q, want ok (a sibling failure must not poison passing checks)", got)
	}
}

// TC3 in the story: during the first 30 s after start the probe returns
// 503 with reason=warming even when the underlying checks are green.
func TestReady_WarmingWindow(t *testing.T) {
	t.Parallel()
	r := NewReady("api", []Check{
		CheckFunc{N: "db", F: func(context.Context) error { return nil }},
	}, 30*time.Second)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 during warmup", rr.Code)
	}
	var resp readyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "warming" {
		t.Fatalf("overall = %q, want warming", resp.Status)
	}
	if resp.Checks["db"].Status != "ok" {
		t.Fatalf("db = %+v during warmup; the underlying check must still run so operators can see what's healthy", resp.Checks["db"])
	}
}

// Once the warm window has elapsed and checks pass, the probe flips to
// 200. We simulate this by injecting `now` rather than sleeping for
// 30 s in tests.
func TestReady_FlipsAfterWarmup(t *testing.T) {
	t.Parallel()
	r := NewReady("api", []Check{
		CheckFunc{N: "db", F: func(context.Context) error { return nil }},
	}, 30*time.Second)
	// Pretend the warm-up has elapsed.
	r.now = func() time.Time { return r.warmingUntil.Add(time.Second) }

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d after warmup, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// A check that hangs longer than PerCheckTimeout must be reported as
// failed, not block the whole probe past the cumulative budget.
// Plan §8: 800 ms cumulative budget, 200 ms per check.
func TestReady_SlowCheckTimesOut(t *testing.T) {
	t.Parallel()
	r := NewReady("api", []Check{
		CheckFunc{N: "slow", F: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		}},
	}, 0)

	start := time.Now()
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	elapsed := time.Since(start)

	if elapsed > r.budget+200*time.Millisecond {
		t.Fatalf("probe took %v, want ≤ %v (budget + slack)", elapsed, r.budget+200*time.Millisecond)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}

func TestAdminMux_RoutesBoth(t *testing.T) {
	t.Parallel()
	mux := AdminMux("streaming", nil, 0)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

// AC1: /healthz must never block on dependencies. We give it a 1 ms
// deadline and confirm it still responds.
func TestLive_NeverBlocks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(ctx)

	rr := httptest.NewRecorder()
	NewLive("api").ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a 1 ms ctx deadline", rr.Code)
	}
}

func TestTCPDial_Reachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c := &TCPDial{N: "self", Addr: addr}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestTCPDial_Unreachable(t *testing.T) {
	t.Parallel()
	// 127.0.0.1:1 is reserved and not in use; dialing it must fail
	// quickly with a non-nil error rather than hanging.
	c := &TCPDial{N: "void", Addr: "127.0.0.1:1"}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err == nil {
		t.Fatalf("Run = nil, want error")
	}
}
