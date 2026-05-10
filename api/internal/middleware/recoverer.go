package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Recoverer turns a panic into a 500 problem+json. The stack goes to
// the structured log; the body never carries it (preventing accidental
// leak of internal paths).
//
// Story 7.1 AC-1 (the panic-renders-as-500 test) and the recoverer's
// position in the chain (above the handler, below request-id and the
// logger) is part of the canonical order in router.New.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.ErrorContext(r.Context(), "panic",
					"value", v, "stack", string(debug.Stack()))
				httperror.Write(w, r, httperror.Internal(""))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
