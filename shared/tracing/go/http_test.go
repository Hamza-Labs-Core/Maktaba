package tracing

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseTraceparent(t *testing.T) {
	tID, sID := parseTraceparent("00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if tID != "0af7651916cd43dd8448eb211c80319c" {
		t.Fatalf("traceID = %q", tID)
	}
	if sID != "b7ad6b7169203331" {
		t.Fatalf("spanID = %q", sID)
	}
}

func TestParseTraceparentRejectsBadVersion(t *testing.T) {
	tID, _ := parseTraceparent("ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	if tID != "" {
		t.Fatalf("expected empty trace id for unknown version, got %q", tID)
	}
}

func TestHTTPMintsTraceID(t *testing.T) {
	called := false
	h := HTTP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		tr, ok := FromContext(r.Context())
		if !ok {
			t.Fatal("trace not on context")
		}
		if len(tr.TraceID) != 32 {
			t.Fatalf("traceID = %q (%d chars)", tr.TraceID, len(tr.TraceID))
		}
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(w, r)
	if !called {
		t.Fatal("handler not called")
	}
	if got := w.Header().Get(TraceParentHeader); got == "" {
		t.Fatal("response missing traceparent header")
	}
}

func TestQueryHashHidesContent(t *testing.T) {
	h := QueryHash("q=بسم الله")
	if h == "" || len(h) != 16 {
		t.Fatalf("hash = %q (want 16 hex chars)", h)
	}
	if h == "q=بسم الله" {
		t.Fatal("hash matches input — must be hashed")
	}
}
