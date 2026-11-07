package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
)

// Epic 53 story 53.17 §4: a request is counted by its route pattern, never its path, so a
// home's id never becomes a label.
func TestRequestsAreCountedByRoutePattern(t *testing.T) {
	obs.SetService("cloud.test")
	t.Cleanup(func() { obs.SetService("cloud") })
	r := chi.NewRouter()
	r.Use(Logging(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.Get("/v1/homes/{id}/link", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/homes/0192f0c4-home-id/link", nil))
	var b bytes.Buffer
	_ = obs.WriteMetrics(&b)
	if !strings.Contains(b.String(), `maktaba_http_requests_total{service="cloud.test",route="/v1/homes/{id}/link",status_class="4xx"}`) {
		t.Fatalf("no series by pattern:\n%s", b.String())
	}
	if strings.Contains(b.String(), "0192f0c4-home-id") {
		t.Fatal("a home's id became a label")
	}
}
