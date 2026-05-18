package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	errrpt "github.com/Hamza-Labs-Core/Maktaba/shared/errrpt/go"
)

// Recoverer turns a panic into a 500 problem+json. The stack goes to
// the structured log; the body never carries it (preventing accidental
// leak of internal paths).
//
// Story 7.1 AC-1 (the panic-renders-as-500 test) and the recoverer's
// position in the chain (above the handler, below request-id and the
// logger) is part of the canonical order in router.New.
//
// This is the nil-Reporter form, preserved for existing callers/tests:
// it is exactly RecovererWithReporter(nil) — same recover, same 500,
// same hand-rolled stack log, no errrpt capture (default-off, no
// behaviour change).
func Recoverer(next http.Handler) http.Handler {
	return RecovererWithReporter(nil)(next)
}

// RecovererWithReporter is Recoverer plus a central errrpt capture: on a
// recovered panic it mints (or reuses a propagated) error_id, emits the
// errrpt structured `error_reported` event, and dispatches to the
// optional webhook sink — the live path that actually closes HLB-300
// (error reporting / alerting). It still does everything Recoverer does
// today (recover, 500 problem+json, the existing slog "panic" line);
// the errrpt capture is strictly net-additive.
//
// rep may be nil: with a nil Reporter this is byte-for-byte the old
// Recoverer (errrpt.Reporter.Capture is a no-op on a nil receiver), so
// the no-Reporter wiring and existing tests are unaffected.
func RecovererWithReporter(rep *errrpt.Reporter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					slog.ErrorContext(r.Context(), "panic",
						"value", v, "stack", string(debug.Stack()))
					// Central HLB-300 hook: report the panic through the
					// shared errrpt surface (structured event + optional
					// sink). nil Reporter => no-op, so this never changes
					// the recover/response/log behaviour above.
					rep.Capture(r.Context(), errrpt.CategoryInternal,
						panicError(v))
					httperror.Write(w, r, httperror.Internal(""))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// panicError adapts an arbitrary recover() value to the error errrpt
// expects without losing the panic payload. An error value is passed
// through; anything else is rendered via %v.
func panicError(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return &panicValue{v: v}
}

type panicValue struct{ v any }

func (p *panicValue) Error() string {
	return fmt.Sprintf("panic: %v", p.v)
}
