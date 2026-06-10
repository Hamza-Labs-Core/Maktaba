package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRequestID_MintsWhenAbsent(t *testing.T) {
	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetRequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	u, err := uuid.Parse(got)
	if err != nil {
		t.Fatalf("not a uuid: %q (%v)", got, err)
	}
	if u.Version() != 7 {
		t.Errorf("want v7, got v%d", u.Version())
	}
	if w.Header().Get("X-Request-Id") != got {
		t.Errorf("response header missing/mismatched: %q vs %q", w.Header().Get("X-Request-Id"), got)
	}
}

func TestRequestID_ReusesValidInput(t *testing.T) {
	u, _ := uuid.NewV7()
	want := u.String()
	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetRequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", want)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRequestID_RejectsNonV7(t *testing.T) {
	u := uuid.Must(uuid.NewRandom()) // v4
	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = GetRequestID(r.Context())
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", u.String())
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got == u.String() {
		t.Errorf("v4 input should have been replaced")
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Errorf("replacement not a uuid: %q", got)
	}
}
