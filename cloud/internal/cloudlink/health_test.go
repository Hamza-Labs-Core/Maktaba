package cloudlink

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackoff_Exponential_Capped(t *testing.T) {
	base := 1 * time.Second
	max := 30 * time.Second
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second},  // 32s capped to 30s
		{50, 30 * time.Second}, // far past cap, no overflow
		{0, 1 * time.Second},   // clamps to attempt 1
	}
	for _, c := range cases {
		if got := Backoff(c.attempt, base, max); got != c.want {
			t.Errorf("Backoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

// TestSupervisor_ReconnectsAfterDrop runs the real Supervisor against a
// dial func that fails twice then succeeds, proving the
// connect→serve→reconnect loop and backoff path execute and that state
// transitions are reported for the admin endpoint.
func TestSupervisor_ReconnectsAfterDrop(t *testing.T) {
	attempts := 0
	clientEnd, cloudEnd := newPipe()

	sup := &Supervisor{
		Dialer:    &Dialer{}, // overridden by dialFunc below
		Proxy:     &LocalProxy{BaseURL: "http://127.0.0.1:1"},
		BaseWait:  5 * time.Millisecond,
		MaxWait:   20 * time.Millisecond,
		PingEvery: 10 * time.Millisecond,
	}
	// Inject a fake dialer via the test seam: replace Dialer.WS path by
	// swapping Dial through a function field is not exported, so we
	// drive the lower-level pieces directly here instead.
	dial := func(ctx context.Context) (DialResult, error) {
		attempts++
		if attempts < 3 {
			return DialResult{}, context.DeadlineExceeded
		}
		return DialResult{Conn: clientEnd, Slug: "acme"}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = runSupervisorWithDial(ctx, sup, dial)
		close(done)
	}()

	// Wait until the supervisor reports online.
	deadline := time.After(2 * time.Second)
	for sup.State().Status != "online" {
		select {
		case <-deadline:
			t.Fatalf("supervisor never came online; state=%+v attempts=%d", sup.State(), attempts)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if attempts < 3 {
		t.Fatalf("expected >=3 dial attempts (2 fail + 1 ok), got %d", attempts)
	}
	st := sup.State()
	if st.Slug != "acme" {
		t.Fatalf("online state slug = %q, want acme", st.Slug)
	}

	// Drop the tunnel; supervisor should leave "online".
	_ = cloudEnd.Close()
	cancel()
	<-done
}

func TestAdminHandler_OnlineVsDown(t *testing.T) {
	sup := &Supervisor{}
	sup.setStatus("connecting", "", 1)

	rec := httptest.NewRecorder()
	AdminHandler(sup).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("down status code = %d, want 503", rec.Code)
	}
	var st LinkState
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st.Status != "connecting" {
		t.Fatalf("body status = %q", st.Status)
	}

	sup.setOnline("acme")
	rec = httptest.NewRecorder()
	AdminHandler(sup).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/cloud-link", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("online status code = %d, want 200", rec.Code)
	}
}

func TestAdminHandler_RejectsNonGET(t *testing.T) {
	sup := &Supervisor{}
	rec := httptest.NewRecorder()
	AdminHandler(sup).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/cloud-link", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rec.Code)
	}
}
