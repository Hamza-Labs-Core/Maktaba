package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAuthExcept_ClusterJoinWildcardIsOneSegment(t *testing.T) {
	gate := RequireAuthExcept(DefaultPublicAllowlist())
	reached := 0
	h := gate(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { reached++ }))
	for path, want := range map[string]int{
		"/api/cluster/join":             http.StatusOK,
		"/api/cluster/join/abc":         http.StatusOK,
		"/api/cluster/join/":            http.StatusUnauthorized,
		"/api/cluster/join/abc/x":       http.StatusUnauthorized,
		"/api/cluster":                  http.StatusUnauthorized,
		"/api/cluster/settings":         http.StatusUnauthorized,
		"/api/cluster/nodes/abc/revoke": http.StatusUnauthorized,
		"/api/cluster/agent":            http.StatusOK,
		"/api/cluster/agent/x":          http.StatusUnauthorized,
		"/api/cluster/ws":               http.StatusUnauthorized,
		"/api/cluster/files":            http.StatusUnauthorized,
		"/api/cluster/llm/v1/models":    http.StatusUnauthorized,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Errorf("%s anonymous: status = %d, want %d", path, rec.Code, want)
		}
	}
	if reached != 3 {
		t.Errorf("reached = %d, want 3", reached)
	}
}
