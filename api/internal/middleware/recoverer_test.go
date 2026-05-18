package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	errrpt "github.com/Hamza-Labs-Core/Maktaba/shared/errrpt/go"
)

// panicHandler always panics so we can exercise the recover path.
func panicHandler() http.Handler {
	return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
}

// fakeSink records the reports errrpt dispatches so the test can assert
// the optional alert path actually fired.
type fakeSink struct {
	mu      sync.Mutex
	reports []errrpt.Report
}

func (f *fakeSink) Send(_ context.Context, r errrpt.Report) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, r)
	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reports)
}

func (f *fakeSink) first() errrpt.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reports[0]
}

// TestRecovererWithReporterCapturesPanic is the HLB-300 live-path
// assertion: a panicking handler wrapped by the reporter-enabled
// Recoverer (a) still recovers and returns 500, and (b) drives
// errrpt.Capture — a structured error_reported / error_id event is
// emitted and the injected sink receives the report.
//
// Fail-without-fix: against the pre-wiring Recoverer (no errrpt) the
// recover happens but Capture never runs, so no error_id is emitted and
// the sink stays empty — these assertions fail, which is the gap this
// change closes.
func TestRecovererWithReporterCapturesPanic(t *testing.T) {
	var buf bytes.Buffer
	lg := slog.New(slog.NewJSONHandler(&buf, nil))
	sink := &fakeSink{}
	rep := errrpt.New(lg, sink)

	h := RecovererWithReporter(rep)(panicHandler())

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(w, r) // must not panic out of ServeHTTP

	// (a) recovered + 500, exactly as before.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("response body leaked panic value: %q", w.Body.String())
	}

	// (b) errrpt.Capture ran: a structured error_reported event with an
	// error_id was emitted on the in-memory handler.
	var sawReported bool
	var errorID string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line not JSON: %v (%q)", err, line)
		}
		if rec["event"] == "error_reported" {
			sawReported = true
			id, _ := rec["error_id"].(string)
			if id == "" {
				t.Fatal("error_reported event missing error_id")
			}
			errorID = id
			if rec["category"] != string(errrpt.CategoryInternal) {
				t.Fatalf("category = %v, want %q",
					rec["category"], errrpt.CategoryInternal)
			}
		}
	}
	if !sawReported {
		t.Fatal("no error_reported event emitted — errrpt.Capture did not run on panic")
	}

	// (b) the injected sink received the report (alert path fired).
	if sink.count() != 1 {
		t.Fatalf("sink got %d reports, want 1", sink.count())
	}
	if got := sink.first().ErrorID; got != errorID {
		t.Fatalf("sink report error_id = %q, want %q (must match logged id)",
			got, errorID)
	}
}

// TestRecovererNilReporterPreservesOldBehaviour pins the default-off
// contract: the bare Recoverer (== RecovererWithReporter(nil)) still
// recovers and returns 500, and does NOT emit any errrpt error_reported
// event — byte-for-byte the historical behaviour, so existing
// callers/tests are unaffected.
func TestRecovererNilReporterPreservesOldBehaviour(t *testing.T) {
	// Capture slog.Default so we can assert no error_reported leaks out
	// even via the package default logger.
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(orig)

	for name, h := range map[string]http.Handler{
		"Recoverer":                Recoverer(panicHandler()),
		"RecovererWithReporterNil": RecovererWithReporter(nil)(panicHandler()),
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			h.ServeHTTP(w, r) // must not panic out

			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", w.Code)
			}
			if strings.Contains(buf.String(), "error_reported") {
				t.Fatalf("nil-Reporter path emitted an errrpt event: %q",
					buf.String())
			}
		})
	}
}
