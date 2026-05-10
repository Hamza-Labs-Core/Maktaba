package httperror

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

func TestWriteNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/videos/abc", nil)
	r = r.WithContext(reqid.WithID(r.Context(), uuid.Must(uuid.NewV7())))

	Write(w, r, NotFound("video not found"))

	if got := w.Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", got)
	}
	if got := w.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("content-type = %q, want application/problem+json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["type"] != TypeNotFound {
		t.Fatalf("type = %v, want %s", body["type"], TypeNotFound)
	}
	if body["status"] != float64(404) {
		t.Fatalf("status field = %v, want 404", body["status"])
	}
	if body["instance"] != "/api/videos/abc" {
		t.Fatalf("instance = %v, want /api/videos/abc", body["instance"])
	}
	if body["requestId"] == "" || body["requestId"] == "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("requestId not set: %v", body["requestId"])
	}
}

func TestWriteValidation(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/videos", nil)

	errs := []FieldError{{Field: "id", Message: "must be a valid UUID"}}
	Write(w, r, Unprocessable(errs))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	var body struct {
		Errors []FieldError `json:"errors"`
		Type   string       `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Type != TypeValidation {
		t.Fatalf("type = %s, want %s", body.Type, TypeValidation)
	}
	if len(body.Errors) != 1 || body.Errors[0].Field != "id" {
		t.Fatalf("errors = %+v, want one with field=id", body.Errors)
	}
}

func TestWriteWrapsUnknown(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	Write(w, r, errors.New("oops"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["type"] != TypeInternal {
		t.Fatalf("type = %v, want %s", body["type"], TypeInternal)
	}
	// Underlying message must NOT be in detail (don't leak).
	if d, ok := body["detail"].(string); ok && d != "" {
		t.Fatalf("detail leaked: %q", d)
	}
}

func TestExtrasFlatMarshalled(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	Write(w, r, Unavailable(30))

	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["retry_after_sec"] != float64(30) {
		t.Fatalf("retry_after_sec = %v, want 30", body["retry_after_sec"])
	}
}
