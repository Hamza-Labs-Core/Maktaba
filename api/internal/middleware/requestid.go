// Package middleware bundles the canonical chi.Mux middleware stack
// for the Maktaba API. Order matters; see api/internal/router for the
// load-bearing assembly.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/reqid"
)

// RequestID is the first middleware after RealIP. It either accepts a
// client-supplied X-Request-Id (when it parses as a valid UUID v7) or
// mints a fresh v7. The id is stored on the request context and echoed
// in the response header so a client retrying a request can pin the
// log line.
//
// Story 7.1 AC-2:
//   - missing header → mint
//   - syntactically valid v7 → reuse verbatim
//   - anything else → ignored, fresh v7 minted
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id uuid.UUID
		if got := r.Header.Get(reqid.Header); got != "" {
			if parsed, err := uuid.Parse(got); err == nil && parsed.Version() == 7 {
				id = parsed
			}
		}
		if id == uuid.Nil {
			id = uuid.Must(uuid.NewV7())
		}
		ctx := reqid.WithID(r.Context(), id)
		w.Header().Set(reqid.Header, id.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
