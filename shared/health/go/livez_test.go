package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckedLive(t *testing.T) {
	t.Parallel()
	wedged := false
	h := NewCheckedLive("streaming", CheckFunc{N: "watchdog", F: func(context.Context) error {
		if wedged {
			return errors.New("heartbeat stalled for 45s")
		}
		return nil
	}})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthy status = %d", rr.Code)
	}

	wedged = true
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("wedged status = %d, want 503", rr.Code)
	}
	var body readyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "fail" || body.Checks["watchdog"].Reason != "heartbeat stalled for 45s" {
		t.Fatalf("body = %+v", body)
	}
}

func TestAdminMuxWithLiveness(t *testing.T) {
	t.Parallel()
	fail := CheckFunc{N: "db", F: func(context.Context) error { return errors.New("down") }}
	mux := AdminMuxWithLiveness("streaming", nil, []Check{fail}, 0)
	for path, want := range map[string]int{"/healthz": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != want {
			t.Errorf("%s = %d, want %d (a dependency failure must not fail liveness)", path, rr.Code, want)
		}
	}
}
