package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
)

func TestIdempotencyReplay(t *testing.T) {
	store := idempotency.NewMemoryStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = io.NopCloser(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"call":` + boolStr(calls == 1) + `}`))
	}))

	body := []byte(`{"name":"hello"}`)
	headers := http.Header{"Idempotency-Key": []string{"K1"}, "Content-Type": []string{"application/json"}}

	first := newReq(http.MethodPost, body, headers)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, first)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first call status = %d", w1.Code)
	}

	second := newReq(http.MethodPost, body, headers)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, second)
	if calls != 1 {
		t.Fatalf("handler called %d times, want 1 (replay should not invoke)", calls)
	}
	if w2.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", w2.Code)
	}
	if w1.Body.String() != w2.Body.String() {
		t.Fatalf("replay body differs: first=%q second=%q", w1.Body.String(), w2.Body.String())
	}
	if w2.Header().Get("X-Idempotent-Replay") != "1" {
		t.Fatal("replay should set X-Idempotent-Replay")
	}
}

func TestIdempotencyConflict(t *testing.T) {
	store := idempotency.NewMemoryStore()
	h := Idempotency(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	headers := http.Header{"Idempotency-Key": []string{"K2"}, "Content-Type": []string{"application/json"}}

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, newReq(http.MethodPost, []byte(`{"a":1}`), headers))

	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, newReq(http.MethodPost, []byte(`{"a":2}`), headers))
	if w2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w2.Code)
	}
}

func TestIdempotencySkipsGet(t *testing.T) {
	store := idempotency.NewMemoryStore()
	calls := 0
	h := Idempotency(store)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
	}))

	headers := http.Header{"Idempotency-Key": []string{"K3"}}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, newReq(http.MethodGet, nil, headers))
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (GET should not be cached)", calls)
	}
}

func newReq(method string, body []byte, headers http.Header) *http.Request {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, "/x", rdr)
	for k, v := range headers {
		r.Header[k] = v
	}
	return r
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func init() {
	// Pre-import strings so import doesn't get pruned if no other use.
	_ = strings.Contains
}
