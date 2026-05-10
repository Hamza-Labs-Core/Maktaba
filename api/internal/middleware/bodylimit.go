package middleware

import (
	"net/http"
	"strconv"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// DefaultBodyLimit is the global cap when no per-route override is
// applied. Story 7.19 AC-1: 1 MiB.
const DefaultBodyLimit int64 = 1 << 20

// BodyLimit caps the request body to maxBytes. It rejects on
// Content-Length first (cheap; runs before the handler reads any
// bytes) and wraps the body with http.MaxBytesReader so a fake
// Content-Length cannot trick the limiter (Story 7.19 security TC).
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				httperror.Write(w, r, &httperror.Error{
					Type:   httperror.TypeBodyTooLarge,
					Title:  "payload too large",
					Status: http.StatusRequestEntityTooLarge,
					Detail: "body must be at most " + strconv.FormatInt(maxBytes, 10) + " bytes",
				})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
