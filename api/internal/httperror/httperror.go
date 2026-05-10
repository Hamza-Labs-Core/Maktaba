// Package httperror implements the API's RFC 9457 problem+json error
// envelope (Story 7.1 AC-1).
//
// All handlers MUST surface failure via Write — no http.Error, no
// hand-rolled JSON. A vet-style analyzer (tools/cmd/forbidhttperror)
// fails CI on any handler that bypasses this package.
package httperror

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

// Error is the canonical API error type. It serialises to the RFC 9457
// envelope — `type` is the problem-type URI, `title` is a human-readable
// short summary, `status` is the HTTP status, `detail` carries
// per-instance specifics, and `instance` is filled in by Write to be
// the request path. `errors[]` is populated for 422 validation failures
// (Story 7.19 AC-3); `extras` lets a constructor attach domain-specific
// fields (e.g. `retry_after_sec`) that surface alongside the standard
// keys without polluting Error itself.
type Error struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   []FieldError   `json:"errors,omitempty"`
	Extras   map[string]any `json:"-"`
}

// FieldError is one entry in a 422 envelope.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error satisfies the error interface so *Error can be passed where
// `error` is expected. The returned string is "<title>: <detail>" and
// is not the over-the-wire shape — Write owns serialisation.
func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Title
	}
	return e.Title + ": " + e.Detail
}

// With attaches a key/value to the error's flat-marshalled extras and
// returns the receiver so call sites can chain:
// `httperror.NotFound("video").With("video_id", id)`.
func (e *Error) With(key string, value any) *Error {
	if e.Extras == nil {
		e.Extras = map[string]any{}
	}
	e.Extras[key] = value
	return e
}

// Constructors — the canonical way for handlers to mint an Error. Each
// returns a fresh value so callers can attach additional context with
// .With without affecting other call sites.

func NotFound(detail string) *Error {
	return &Error{Type: TypeNotFound, Title: "not found", Status: http.StatusNotFound, Detail: detail}
}

func BadRequest(detail string) *Error {
	return &Error{Type: TypeBadRequest, Title: "bad request", Status: http.StatusBadRequest, Detail: detail}
}

func InvalidQuery(detail string) *Error {
	return &Error{Type: TypeInvalidQueryParam, Title: "invalid query parameter", Status: http.StatusBadRequest, Detail: detail}
}

func Unprocessable(errs []FieldError) *Error {
	return &Error{Type: TypeValidation, Title: "validation failed", Status: http.StatusUnprocessableEntity, Errors: errs}
}

func Conflict(typ, detail string) *Error {
	if typ == "" {
		typ = TypeConflict
	}
	return &Error{Type: typ, Title: "conflict", Status: http.StatusConflict, Detail: detail}
}

func Forbidden(typ, detail string) *Error {
	if typ == "" {
		typ = TypeForbidden
	}
	return &Error{Type: typ, Title: "forbidden", Status: http.StatusForbidden, Detail: detail}
}

func Internal(detail string) *Error {
	return &Error{Type: TypeInternal, Title: "internal error", Status: http.StatusInternalServerError, Detail: detail}
}

// Unavailable returns a 503 carrying a `retry_after_sec` extra. Callers
// should ALSO set the Retry-After response header before Write fires —
// the extras only surface in the JSON body.
func Unavailable(retryAfterSec int) *Error {
	return (&Error{Type: TypeUnavailable, Title: "service unavailable", Status: http.StatusServiceUnavailable}).
		With("retry_after_sec", retryAfterSec)
}

// Write renders any error to an RFC 9457 problem+json response. It is
// idempotent against double-write attempts when paired with the
// SingleWriteGuard middleware.
//
// If err is not (or does not wrap) an *Error, it's logged at error
// level and rendered as a generic 500 with no detail — never leak the
// underlying message to the client.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		slog.ErrorContext(r.Context(), "unhandled_error", "err", err.Error())
		e = Internal("")
	}
	e.Instance = r.URL.Path

	body := map[string]any{
		"type":      e.Type,
		"title":     e.Title,
		"status":    e.Status,
		"instance":  e.Instance,
		"requestId": reqid.FromContext(r.Context()).String(),
	}
	if e.Detail != "" {
		body["detail"] = e.Detail
	}
	if len(e.Errors) > 0 {
		body["errors"] = e.Errors
	}
	for k, v := range e.Extras {
		body[k] = v
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(e.Status)
	_ = json.NewEncoder(w).Encode(body)
}
