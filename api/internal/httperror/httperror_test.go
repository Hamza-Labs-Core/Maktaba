package httperror

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
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

func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := mlog.Default
	mlog.Default = slog.New(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { mlog.Default = prev })
	return &buf
}

func TestWrite_LogsServerErrorsWithTheirCause(t *testing.T) {
	buf := captureDefaultLog(t)
	r := httptest.NewRequest(http.MethodPost, "/api/libraries/7/scan", nil)
	r = r.WithContext(mlog.WithRequestID(r.Context(), "req-1"))
	Write(httptest.NewRecorder(), r, Internal("load library").Wrap(errors.New("pq: connection refused")))
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("%v: %q", err, buf.String())
	}
	for k, v := range map[string]any{
		"event": "http_error", "level": "ERROR", "method": "POST", "path": "/api/libraries/7/scan",
		"status": float64(500), "detail": "load library", "err": "pq: connection refused", "request_id": "req-1",
	} {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}
}

func TestWrite_DoesNotSendTheCause(t *testing.T) {
	captureDefaultLog(t)
	w := httptest.NewRecorder()
	Write(w, httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("secret internals"))
	if strings.Contains(w.Body.String(), "secret internals") {
		t.Fatalf("cause leaked to the client: %s", w.Body.String())
	}
}

func TestWrite_ClientErrorsAreNotLoggedHere(t *testing.T) {
	buf := captureDefaultLog(t)
	Write(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), NotFound("video"))
	if buf.Len() != 0 {
		t.Fatalf("a 404 is the request log's: %s", buf.String())
	}
}
