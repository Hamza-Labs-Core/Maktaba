// Package reqid carries the per-request UUID v7 across the middleware
// chain via context.
//
// It lives in its own tiny package so that both api/internal/httperror
// and api/internal/middleware can import it without forming an import
// cycle (the recoverer middleware needs to render an error envelope,
// and the error envelope needs to read the request id). Story 7.1.
package reqid

import (
	"context"

	"github.com/google/uuid"
)

// Header is the canonical HTTP header that carries the request id over
// the wire. Clients may seed a value (idempotent retries); responses
// always echo it.
const Header = "X-Request-Id"

type ctxKey struct{}

// WithID returns a new context carrying id.
func WithID(parent context.Context, id uuid.UUID) context.Context {
	return context.WithValue(parent, ctxKey{}, id)
}

// FromContext returns the request id stored on ctx, or uuid.Nil if no
// id was attached. Callers wanting the string form should call
// .String() on the result; uuid.Nil.String() yields the all-zero UUID
// which is a safe sentinel for "no request id".
func FromContext(ctx context.Context) uuid.UUID {
	if v, ok := ctx.Value(ctxKey{}).(uuid.UUID); ok {
		return v
	}
	return uuid.Nil
}
