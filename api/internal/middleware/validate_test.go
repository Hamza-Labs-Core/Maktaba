package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

type sampleBody struct {
	ID    string `json:"id" validate:"required,uuid"`
	Limit int    `json:"limit" validate:"min=1,max=200"`
}

func TestValidateRequiredField(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{}`)))

	var dst sampleBody
	err := Bind(r, &dst)
	if err == nil {
		t.Fatal("expected validation error for empty body")
	}
	if err.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", err.Status)
	}
	if err.Type != httperror.TypeValidation {
		t.Fatalf("type = %s, want %s", err.Type, httperror.TypeValidation)
	}
	// Look for the id field error.
	foundID := false
	for _, fe := range err.Errors {
		if fe.Field == "id" && fe.Message == "is required" {
			foundID = true
		}
	}
	if !foundID {
		t.Fatalf("errors didn't surface required id: %+v", err.Errors)
	}
}

func TestValidateUUIDInvalid(t *testing.T) {
	body := []byte(`{"id":"not-a-uuid","limit":50}`)
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))

	var dst sampleBody
	err := Bind(r, &dst)
	if err == nil {
		t.Fatal("expected validation error for non-UUID id")
	}
	for _, fe := range err.Errors {
		if fe.Field == "id" && fe.Message == "must be a valid UUID" {
			return
		}
	}
	t.Fatalf("expected uuid message; got %+v", err.Errors)
}

func TestValidateInvalidJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{not json`)))
	var dst sampleBody
	err := Bind(r, &dst)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if err.Type != httperror.TypeInvalidJSON {
		t.Fatalf("type = %s, want %s", err.Type, httperror.TypeInvalidJSON)
	}
	if err.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", err.Status)
	}
}

func TestValidateAccepts(t *testing.T) {
	body := []byte(`{"id":"01902f00-7c80-77c8-9c00-000000000000","limit":50}`)
	r := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	var dst sampleBody
	if err := Bind(r, &dst); err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if dst.ID == "" || dst.Limit != 50 {
		t.Fatalf("dst not populated: %+v", dst)
	}
}
