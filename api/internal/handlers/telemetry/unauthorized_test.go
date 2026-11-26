package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func TestUnauthorizedProblemType(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry/events", nil)
	if signedIn(rec, req) {
		t.Fatal("expected anonymous request to be rejected")
	}
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
