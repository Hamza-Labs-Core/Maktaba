package playqueue

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func TestAnonymousReturnsUnauthorizedProblem(t *testing.T) {
	r := chi.NewRouter()
	(&Handler{}).Mount(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/me/play-queues", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != httperror.TypeUnauthorized {
		t.Fatalf("type = %v, want %s", body["type"], httperror.TypeUnauthorized)
	}
}
