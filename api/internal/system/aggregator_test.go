package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// helper that builds a fake service whose /readyz returns the given
// status code + JSON body.
func fakeReadyz(t *testing.T, code int, body string) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, srv.URL + "/readyz"
}

func TestAggregator_AllOK(t *testing.T) {
	t.Parallel()
	_, urlA := fakeReadyz(t, 200, `{"status":"ok","service":"streaming","checks":{"db":{"status":"ok"}}}`)
	_, urlB := fakeReadyz(t, 200, `{"status":"ok","service":"pipeline","checks":{"chroma":{"status":"ok"}}}`)

	agg := NewAggregator([]Service{
		{Name: "streaming", URL: urlA},
		{Name: "pipeline", URL: urlB},
	})

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, rr.Body.String())
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
	if got.Services["streaming"].Status != "ok" || got.Services["pipeline"].Status != "ok" {
		t.Fatalf("services = %+v", got.Services)
	}
}

// TC2 in the story: with Pipeline down, /api/system/health returns
// degraded with pipeline.reason="grpc_unavailable".
func TestAggregator_OneServiceDegraded(t *testing.T) {
	t.Parallel()
	_, urlPipeline := fakeReadyz(t, 503, `{"status":"degraded","service":"pipeline","checks":{"grpc":{"status":"fail","reason":"grpc_unavailable"}}}`)
	_, urlStreaming := fakeReadyz(t, 200, `{"status":"ok","service":"streaming","checks":{}}`)

	agg := NewAggregator([]Service{
		{Name: "pipeline", URL: urlPipeline},
		{Name: "streaming", URL: urlStreaming},
	})

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "degraded" {
		t.Fatalf("overall = %q, want degraded", got.Status)
	}
	pip := got.Services["pipeline"]
	if pip.Status != "degraded" {
		t.Fatalf("pipeline status = %q, want degraded", pip.Status)
	}
	if got := pip.Checks["grpc"].Reason; !strings.Contains(got, "grpc_unavailable") {
		t.Fatalf("grpc reason = %q, want it to mention grpc_unavailable", got)
	}
}

// EC2 in the story: streaming all-replicas-down → status=degraded with
// services.streaming.status=down. We exercise the network failure path
// by pointing at a closed socket.
func TestAggregator_ServiceUnreachable(t *testing.T) {
	t.Parallel()
	// 127.0.0.1:1 is reserved and not in use; the dial fails fast.
	agg := NewAggregator([]Service{
		{Name: "streaming", URL: "http://127.0.0.1:1/readyz"},
		{Name: "pipeline", URL: "http://127.0.0.1:1/readyz"},
	})

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "down" {
		t.Fatalf("overall = %q, want down (every probed service unreachable)", got.Status)
	}
	if got.Services["streaming"].Status != "down" || got.Services["pipeline"].Status != "down" {
		t.Fatalf("services = %+v", got.Services)
	}
}

func TestAggregator_NoServicesConfigured(t *testing.T) {
	t.Parallel()
	agg := NewAggregator(nil)
	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Fatalf("overall = %q, want ok with no services configured", got.Status)
	}
}

// A /readyz that returns garbage (not JSON) is treated as down with a
// reason — the aggregator does not panic.
func TestAggregator_GarbageBody(t *testing.T) {
	t.Parallel()
	_, url := fakeReadyz(t, 200, `<html>not json</html>`)
	agg := NewAggregator([]Service{{Name: "weird", URL: url}})

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Services["weird"].Status != "down" {
		t.Fatalf("weird = %+v, want status=down", got.Services["weird"])
	}
}

func TestDeriveStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]Snapshot
		want string
	}{
		{"empty", map[string]Snapshot{}, "ok"},
		{"all-ok", map[string]Snapshot{"a": {Status: "ok"}, "b": {Status: "ok"}}, "ok"},
		{"all-down", map[string]Snapshot{"a": {Status: "down"}, "b": {Status: "down"}}, "down"},
		{"mixed", map[string]Snapshot{"a": {Status: "ok"}, "b": {Status: "down"}}, "degraded"},
		{"degraded", map[string]Snapshot{"a": {Status: "ok"}, "b": {Status: "degraded"}}, "degraded"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := deriveStatus(c.in); got != c.want {
				t.Fatalf("deriveStatus(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Sanity: the aggregator timeout protects against a slow upstream — a
// /readyz that hangs longer than the timeout is reported as down
// rather than blocking the response.
func TestAggregator_SlowUpstream(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never write a body
	}))
	t.Cleanup(srv.Close)

	agg := NewAggregator([]Service{{Name: "slow", URL: srv.URL + "/readyz"}})
	// shrink the per-call timeout so the test finishes quickly.
	agg.client.Timeout = 100e6 // 100 ms
	agg.timeout = 100e6

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Services["slow"].Status != "down" {
		t.Fatalf("slow = %+v, want down", got.Services["slow"])
	}
}

// Quick integration-flavored sanity check that exercises the full
// chain using fmt-built JSON to confirm the aggregator's assumed
// shape matches what shared/health/go writes.
func TestAggregator_SnapshotShapeIsStable(t *testing.T) {
	t.Parallel()
	body := fmt.Sprintf(`{"status":%q,"service":%q,"checks":{%q:{"status":%q,"reason":%q}}}`,
		"degraded", "api", "db", "fail", "connection refused")
	_, url := fakeReadyz(t, 503, body)
	agg := NewAggregator([]Service{{Name: "api", URL: url}})

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Services["api"].Checks["db"].Reason != "connection refused" {
		t.Fatalf("did not pass through check reason: %+v", got.Services["api"])
	}
}

// TestAggregator_StatsPopulated confirms the disk-free + queue-depth
// seams flow into the response body. The seams are injected directly
// (this is an internal test) so no real Postgres or syscall is needed.
func TestAggregator_StatsPopulated(t *testing.T) {
	t.Parallel()
	agg := NewAggregator(nil)
	agg.diskFreeFn = func() (uint64, error) { return 4096, nil }
	agg.queueDepthFn = func(context.Context) (int, error) { return 7, nil }

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DiskFreeBytes != 4096 {
		t.Fatalf("disk_free_bytes = %d, want 4096", got.DiskFreeBytes)
	}
	if got.QueueDepth != 7 {
		t.Fatalf("queue_depth = %d, want 7", got.QueueDepth)
	}
	// Self-hosted mode never bills, so the budget stays empty/omitted.
	if got.BudgetUSDLeft != "" {
		t.Fatalf("budget should stay empty in self-hosted mode, got %q", got.BudgetUSDLeft)
	}
}

// TestAggregator_StatProbeFailureIsZero confirms a probe error degrades
// to the zero value rather than corrupting the response.
func TestAggregator_StatProbeFailureIsZero(t *testing.T) {
	t.Parallel()
	agg := NewAggregator(nil)
	agg.diskFreeFn = func() (uint64, error) { return 999, errors.New("statfs failed") }
	agg.queueDepthFn = func(context.Context) (int, error) { return 999, errors.New("db down") }

	rr := httptest.NewRecorder()
	agg.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/system/health", nil))

	var got Health
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DiskFreeBytes != 0 || got.QueueDepth != 0 {
		t.Fatalf("failed probes must yield zero, got disk=%d depth=%d", got.DiskFreeBytes, got.QueueDepth)
	}
	// A failed probe must not break the roll-up status.
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok (no services configured)", got.Status)
	}
}

// TestDiskFreeBytes_RealPath exercises the actual syscall against the
// test's temp dir — it should report a positive figure on any unix CI.
func TestDiskFreeBytes_RealPath(t *testing.T) {
	t.Parallel()
	free, err := diskFreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("diskFreeBytes: %v", err)
	}
	if free == 0 {
		t.Fatalf("expected positive free bytes on the temp volume")
	}
}

// TestNewAggregatorWithStats_WiresSeams confirms the production
// constructor binds both seams when given a data dir + DB, and leaves
// them nil otherwise.
func TestNewAggregatorWithStats_WiresSeams(t *testing.T) {
	t.Parallel()
	none := NewAggregatorWithStats(nil, StatsConfig{})
	if none.diskFreeFn != nil || none.queueDepthFn != nil {
		t.Fatalf("empty config should leave both seams nil")
	}
	withDir := NewAggregatorWithStats(nil, StatsConfig{DataDir: t.TempDir()})
	if withDir.diskFreeFn == nil {
		t.Fatalf("DataDir should wire diskFreeFn")
	}
	if withDir.queueDepthFn != nil {
		t.Fatalf("nil DB should leave queueDepthFn nil")
	}
}
