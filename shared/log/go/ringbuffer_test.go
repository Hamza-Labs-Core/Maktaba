package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newRingLogger builds an isolated logger that tees into a fresh ring,
// without touching the process-global Init state.
func newRingLogger(t *testing.T, capacity int) (*slog.Logger, *RingBuffer) {
	t.Helper()
	rb := NewRingBuffer(capacity)
	// Mirror build(): json primary to a discarded sink + ring tee, with
	// the base fields the contract requires.
	lg := build(Options{Service: "api", Env: "prod", Version: "v0", Output: &bytes.Buffer{}}, rb)
	return lg, rb
}

func TestRingBufferCapturesStructuredLines(t *testing.T) {
	lg, rb := newRingLogger(t, 100)
	lg.Info("hello", "k", "v")

	entries := rb.Entries(Filter{})
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	var rec map[string]any
	if err := json.Unmarshal(entries[0].Raw, &rec); err != nil {
		t.Fatalf("entry not valid JSON: %v", err)
	}
	for _, key := range []string{"ts", "level", "service", "msg", "version", "env"} {
		if _, ok := rec[key]; !ok {
			t.Errorf("entry missing base field %q: %s", key, entries[0].Raw)
		}
	}
	if rec["service"] != "api" || rec["msg"] != "hello" || rec["k"] != "v" {
		t.Errorf("unexpected record: %s", entries[0].Raw)
	}
}

func TestRingBufferEvictsOldest(t *testing.T) {
	lg, rb := newRingLogger(t, 3)
	for i := 0; i < 5; i++ {
		lg.Info("m", "i", i)
	}
	entries := rb.Entries(Filter{})
	if len(entries) != 3 {
		t.Fatalf("want 3 retained, got %d", len(entries))
	}
	// Oldest two (i=0,1) evicted; should hold i=2,3,4 in order.
	for idx, wantI := range []float64{2, 3, 4} {
		var rec map[string]any
		_ = json.Unmarshal(entries[idx].Raw, &rec)
		if rec["i"] != wantI {
			t.Errorf("entry %d: want i=%v got %v", idx, wantI, rec["i"])
		}
	}
}

func TestRingBufferRedactsSecrets(t *testing.T) {
	lg, rb := newRingLogger(t, 10)
	lg.Info("login", "password", "hunter2", "user_id", "u1")
	raw := string(rb.Entries(Filter{})[0].Raw)
	if strings.Contains(raw, "hunter2") {
		t.Fatalf("secret leaked into ring buffer: %s", raw)
	}
	if !strings.Contains(raw, redactedValue) {
		t.Errorf("expected redaction marker in %s", raw)
	}
}

func TestFilterByLevel(t *testing.T) {
	lg, rb := newRingLogger(t, 10)
	lg.Debug("d")
	lg.Info("i")
	lg.Warn("w")
	lg.Error("e")
	// Note: the default level floor is Info, so Debug never reaches the
	// handler. Filtering at Warn keeps warn+error.
	got := rb.Entries(Filter{MinLevel: slog.LevelWarn})
	if len(got) != 2 {
		t.Fatalf("want 2 at >=warn, got %d", len(got))
	}
}

func TestFilterBySearchAndService(t *testing.T) {
	lg, rb := newRingLogger(t, 10)
	lg.Info("needle here")
	lg.Info("haystack")

	if got := rb.Entries(Filter{Search: "NEEDLE"}); len(got) != 1 {
		t.Fatalf("case-insensitive search: want 1, got %d", len(got))
	}
	if got := rb.Entries(Filter{Services: map[string]struct{}{"streaming": {}}}); len(got) != 0 {
		t.Fatalf("service filter should exclude api lines, got %d", len(got))
	}
	if got := rb.Entries(Filter{Services: map[string]struct{}{"api": {}}}); len(got) != 2 {
		t.Fatalf("service filter for api: want 2, got %d", len(got))
	}
}

func TestFilterSinceAndLimit(t *testing.T) {
	lg, rb := newRingLogger(t, 10)
	for i := 0; i < 5; i++ {
		lg.Info("m")
	}
	if got := rb.Entries(Filter{Limit: 2}); len(got) != 2 {
		t.Fatalf("limit: want 2, got %d", len(got))
	}
	future := time.Now().Add(time.Hour)
	if got := rb.Entries(Filter{Since: future}); len(got) != 0 {
		t.Fatalf("since-future should drop all, got %d", len(got))
	}
}

func TestRecentHandlerJSONL(t *testing.T) {
	lg, rb := newRingLogger(t, 10)
	lg.Info("one")
	lg.Warn("two")

	req := httptest.NewRequest(http.MethodGet, "/logs/recent?level=warn", nil)
	rec := httptest.NewRecorder()
	RecentHandler(rb).ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 ndjson line at >=warn, got %d: %q", len(lines), rec.Body.String())
	}
	var rj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rj); err != nil {
		t.Fatalf("ndjson line not valid JSON: %v", err)
	}
}

func TestRecentHandlerNilBuffer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/logs/recent", nil)
	rec := httptest.NewRecorder()
	RecentHandler(nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil buffer: want 503, got %d", rec.Code)
	}
}
