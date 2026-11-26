package flags

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

func TestUnauthorizedProblemType(t *testing.T) {
	do := serve(&Handler{})
	rec := do(http.MethodGet, "/api/me/flags", nil, nil)
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
