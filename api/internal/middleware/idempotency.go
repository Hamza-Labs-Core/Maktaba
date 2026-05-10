package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/idempotency"
)

// captureWriter buffers the handler's response so the middleware can
// store it for replay. We don't try to fully spec the http.ResponseWriter
// surface (no Hijack, no Flush) because mutating routes that take an
// idempotency key are JSON CRUD by definition.
type captureWriter struct {
	http.ResponseWriter
	status  int
	body    bytes.Buffer
	headers http.Header
	wrote   bool
}

func (c *captureWriter) Header() http.Header {
	if c.headers == nil {
		c.headers = c.ResponseWriter.Header()
	}
	return c.headers
}

func (c *captureWriter) WriteHeader(code int) {
	if c.wrote {
		return
	}
	c.wrote = true
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

// Idempotency replays a cached response when a state-changing request
// carries an Idempotency-Key header that has been seen before with the
// same body hash. Different body for the same key → 409.
//
// The middleware is a no-op for safe methods (GET/HEAD/OPTIONS) and for
// requests with no Idempotency-Key header.
func Idempotency(store idempotency.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" || !mutatingMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(io.LimitReader(r.Body, DefaultBodyLimit))
			if err != nil {
				httperror.Write(w, r, httperror.BadRequest("read request body: "+err.Error()))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])

			user := UserID(r.Context())
			if cached, ok := store.Lookup(r.Context(), key, user); ok {
				if cached.RequestHash != hash {
					httperror.Write(w, r, httperror.Conflict(httperror.TypeIdempotencyConflict,
						"Idempotency-Key reused with different body"))
					return
				}
				replay(w, cached)
				return
			}

			cap := &captureWriter{ResponseWriter: w}
			next.ServeHTTP(cap, r)

			// Don't cache 5xx — a transient internal error should not be
			// burned into the replay store. 4xx other than 429 IS cached
			// because the response is deterministic for that input.
			if cap.status >= 500 || cap.status == http.StatusTooManyRequests {
				return
			}
			rec := idempotency.Record{
				Key:         key,
				UserID:      user,
				RequestHash: hash,
				Status:      cap.status,
				Body:        cap.body.Bytes(),
				Headers:     headerMap(cap.Header()),
			}
			_ = store.Save(context.Background(), rec)
		})
	}
}

func mutatingMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func replay(w http.ResponseWriter, rec idempotency.Record) {
	for k, v := range rec.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("X-Idempotent-Replay", "1")
	if rec.Status == 0 {
		rec.Status = http.StatusOK
	}
	w.WriteHeader(rec.Status)
	_, _ = w.Write(rec.Body)
}

// headerMap flattens a Header to map[string]string keeping only the
// first value per key. Sufficient for JSON responses; multi-value
// headers (Set-Cookie) are not currently routed through this middleware.
func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
