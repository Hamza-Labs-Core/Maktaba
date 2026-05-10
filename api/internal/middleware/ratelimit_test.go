package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPerIPLimitBlocks(t *testing.T) {
	// 60/min = 1/s burst 60. Blast 70 immediately; expect at least 5 to
	// 429 (the bucket allows the initial burst then drains).
	h := PerIP(60)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	got429 := 0
	for i := 0; i < 70; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			got429++
		}
	}
	if got429 < 5 {
		t.Fatalf("got %d 429s out of 70, want >= 5", got429)
	}
}

func TestRetryAfterPresent(t *testing.T) {
	h := PerIP(60)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	// Exhaust the bucket then check the next response carries Retry-After.
	for i := 0; i < 80; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = "10.0.0.2:1234"
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			if got := w.Header().Get("Retry-After"); got == "" {
				t.Fatal("429 without Retry-After header")
			}
			return
		}
	}
	t.Fatal("never hit 429 in 80 requests at 60/min")
}

func TestProgressExempt(t *testing.T) {
	calls := 0
	h := PerUser(1)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
	}))

	for i := 0; i < 20; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/stream/sessions/abc/progress", nil)
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("/progress 429'd at request %d; should be exempt", i)
		}
	}
	if calls != 20 {
		t.Fatalf("handler called %d times, want 20", calls)
	}
}
