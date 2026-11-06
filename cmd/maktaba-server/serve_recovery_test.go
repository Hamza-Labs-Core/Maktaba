package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/recovery"
)

// Epic 53 story 53.04: the web port serves /recovery itself (the supervisor's page, not
// the SPA, not the API); before the supervisor is up it says to wait.
func TestWebPortServesRecovery(t *testing.T) {
	t.Cleanup(func() { serveRecovery.Store(nil) })
	serveRecovery.Store(nil)
	web := deadWeb()
	rec := httptest.NewRecorder()
	web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/recovery", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the supervisor: %d", rec.Code)
	}
	serveRecovery.Store(&recovery.Handler{Verifier: &recovery.Verifier{}, Record: func(recovery.Who, string, string) {}})
	for _, p := range []string{"/recovery", "/recovery/api/state"} {
		rec = httptest.NewRecorder()
		web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a pass: %d", p, rec.Code)
		}
	}
}

// The restarting page offers the recovery page once the API has been down a minute.
func TestRestartingPageOffersRecoveryAfterAMinute(t *testing.T) {
	apiDown = downStreak{}
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	if d := apiDown.since(now); d != 0 {
		t.Fatalf("a first failure: %v", d)
	}
	for i := 1; i <= 13; i++ {
		apiDown.since(now.Add(time.Duration(i) * 5 * time.Second))
	}
	if d := apiDown.since(now.Add(65 * time.Second)); d < time.Minute {
		t.Fatalf("a minute of failures: %v", d)
	}
	if d := apiDown.since(now.Add(5 * time.Minute)); d != 0 {
		t.Fatalf("a failure long after the last starts a new streak: %v", d)
	}
	apiDown = downStreak{first: time.Now().Add(-2 * time.Minute), last: time.Now()}
	t.Cleanup(func() { apiDown = downStreak{} })
	rec := get(deadWeb(), "/api/me", browserNav)
	if !strings.Contains(rec.Body.String(), `href="/recovery"`) {
		t.Fatalf("no recovery link after a minute: %s", rec.Body.String())
	}
}

// The admin port answers the tray's link; everything else goes on to the admin surface.
func TestRecoveryAdminRoutesTheLink(t *testing.T) {
	link := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	base := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h := recoveryAdmin(base, link)
	for p, want := range map[string]int{"/recovery/link": http.StatusTeapot, "/processes": http.StatusAccepted} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", p, rec.Code, want)
		}
	}
}
