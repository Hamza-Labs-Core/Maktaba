package errrpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewErrorIDIsUUIDv7AndOrdered(t *testing.T) {
	a := NewErrorID()
	time.Sleep(2 * time.Millisecond)
	b := NewErrorID()
	if len(a) != 36 || len(b) != 36 {
		t.Fatalf("not uuid-shaped: %q %q", a, b)
	}
	// UUIDv7 is time-ordered: a minted earlier sorts <= one minted
	// later (string compare works because the timestamp is the prefix).
	if a >= b {
		t.Fatalf("v7 ids not time-ordered: %q !< %q", a, b)
	}
	// Version nibble (14th hex char) must be '7'.
	if a[14] != '7' {
		t.Fatalf("not a v7 uuid: %q", a)
	}
}

func TestCaptureMintsIDAndLogsStructured(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, nil))
	r := New(lg, nil)

	id := r.Capture(context.Background(), CategoryDependency,
		errors.New("db down"))
	if id == "" {
		t.Fatal("empty error_id")
	}

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("log not JSON: %v", err)
	}
	if rec["error_id"] != id {
		t.Fatalf("error_id field = %v, want %s", rec["error_id"], id)
	}
	if rec["category"] != "dependency" {
		t.Fatalf("category = %v", rec["category"])
	}
	if rec["error"] != "db down" {
		t.Fatalf("error = %v", rec["error"])
	}
	if s, _ := rec["stack"].(string); !strings.Contains(s, "errrpt") {
		t.Fatalf("stack missing/empty: %q", s)
	}
}

func TestCaptureReusesPropagatedID(t *testing.T) {
	r := New(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil)
	ctx := WithErrorID(context.Background(), "inbound-id-123")
	if got := r.Capture(ctx, CategoryInternal, errors.New("x")); got != "inbound-id-123" {
		t.Fatalf("propagated id not reused: %q", got)
	}
}

func TestWebhookSinkPostsAndRateLimits(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&hits, 1)
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["text"] == nil || body["error_id"] == nil {
			t.Errorf("missing fields in webhook body: %v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	frozen := time.Now()
	s := NewWebhookSink(srv.URL).WithMaxPerMin(3)
	s.now = func() time.Time { return frozen } // freeze the window

	for i := 0; i < 6; i++ {
		_ = s.Send(context.Background(), Report{
			ErrorID: "e", Category: CategoryInternal, Message: "boom",
			OccurredAt: frozen,
		})
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("rate limit not enforced: %d hits, want 3", got)
	}

	// Advance past the window: budget refills, suppressed-count is
	// annotated on the next send.
	frozen = frozen.Add(61 * time.Second)
	_ = s.Send(context.Background(), Report{ErrorID: "e2", Message: "again"})
	if got := atomic.LoadInt32(&hits); got != 4 {
		t.Fatalf("window did not refill: %d hits, want 4", got)
	}
}

func TestNilWebhookSinkIsDefaultOff(t *testing.T) {
	if NewWebhookSink("") != nil {
		t.Fatal("empty URL must yield nil sink (default-off)")
	}
	// Capture with nil sink must still mint+log and never panic.
	r := New(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil)
	if r.Capture(context.Background(), CategoryInternal, errors.New("x")) == "" {
		t.Fatal("nil-sink capture returned empty id")
	}
}

func TestSinkFailureDoesNotMaskOriginal(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, nil))
	bad := NewWebhookSink("http://127.0.0.1:0/nope")
	bad.Client = &http.Client{Timeout: 100 * time.Millisecond}
	r := New(lg, bad)
	id := r.Capture(context.Background(), CategoryInternal, errors.New("real error"))
	if id == "" {
		t.Fatal("capture returned empty id despite sink failure")
	}
	if !strings.Contains(buf.String(), "error_sink_failed") {
		t.Fatal("sink failure not logged as warn")
	}
	if !strings.Contains(buf.String(), "real error") {
		t.Fatal("original error not logged")
	}
}
