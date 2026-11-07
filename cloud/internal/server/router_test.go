package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A draining relay (story 50.03) answers /readyz 503 draining before it
// checks anything else, so the edge stops sending it work.
func TestReadyzWhileDraining(t *testing.T) {
	draining := true
	r := NewRouter(slog.New(slog.DiscardHandler), nil, nil, BuildInfo{}, nil, WithDraining(func() bool { return draining }))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"draining"`) {
		t.Errorf("draining: %d %s", rec.Code, rec.Body.String())
	}
	draining = false
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if !strings.Contains(rec.Body.String(), "db_unreachable") {
		t.Errorf("not draining: the database check runs: %d %s", rec.Code, rec.Body.String())
	}
}
