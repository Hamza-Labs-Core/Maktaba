package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The anonymous usage-data ingest is never logged with the client
// address; every other path is.
func TestLoggingLeavesOutTheAddressOfTelemetry(t *testing.T) {
	var buf bytes.Buffer
	h := Logging(slog.New(slog.NewTextHandler(&buf, nil)))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, p := range []string{"/v1/telemetry", "/v1/auth/login"} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodPost, p, nil)
		req.RemoteAddr = "203.0.113.5:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
		logged := strings.Contains(buf.String(), "203.0.113.5")
		if want := p != "/v1/telemetry"; logged != want {
			t.Errorf("%s: address logged = %v, want %v (%s)", p, logged, want, buf.String())
		}
	}
}
