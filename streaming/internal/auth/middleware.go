package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/httpx"
)

type ctxKey int

const (
	ctxKeyClaims ctxKey = iota
	ctxKeySubject
)

// LibraryResolver maps a URL path or claim subject to the library id of
// the resource being served. Story 8.15 implements this with the probe
// cache; for static-asset handlers a fake resolver returns the known
// library at handler-construction time.
type LibraryResolver interface {
	// Resolve looks up the resource's library. The Streaming
	// service rejects when the JWT's lib[] doesn't include the
	// returned id (Story 8.1 AC-1 — wrong-lib).
	Resolve(ctx context.Context, r *http.Request, claims *Claims) (uuid.UUID, error)
}

// LibraryResolverFunc adapts an inline function.
type LibraryResolverFunc func(ctx context.Context, r *http.Request, claims *Claims) (uuid.UUID, error)

func (f LibraryResolverFunc) Resolve(ctx context.Context, r *http.Request, claims *Claims) (uuid.UUID, error) {
	return f(ctx, r, claims)
}

// Verifier is the runtime dependency the middleware needs. Production
// wires JWKS + leeway from config; tests build a hand-rolled instance.
type Verifier struct {
	JWKS   *JWKSCache
	Leeway time.Duration
	Now    func() time.Time
}

// SignedURL returns chi-compatible middleware for a route family.
//
//   - aud must equal `policy`
//   - sub must equal the URL parameter named `subParam` (e.g. session_id, video_id)
//   - exp must be in the future, with leeway for clock skew
//   - signature must verify against the JWKS cache
//   - lib[] must be non-empty and well-formed
//
// Library *coverage* (does lib[] include the resource's library?) is
// enforced by LibraryGuard, which can run after this middleware once
// the resource→library lookup has been done.
func SignedURL(v *Verifier, policy AudPolicy, subParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.URL.Query().Get("sig")
			if raw == "" {
				if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
					raw = strings.TrimPrefix(h, "Bearer ")
				}
			}
			if raw == "" {
				httpx.WriteSignedURLError(w, httpx.SignedURLMissing)
				return
			}

			cfg := VerifyConfig{Leeway: v.Leeway, Now: v.Now}
			claims, err := Verify(raw, v.JWKS, cfg)
			if err != nil {
				httpx.WriteSignedURLError(w, mapVerifyError(err))
				return
			}

			if claims.Aud != string(policy) {
				httpx.WriteSignedURLError(w, httpx.SignedURLWrongAud)
				return
			}

			wanted := chi.URLParam(r, subParam)
			if wanted == "" || claims.Sub != wanted {
				httpx.WriteSignedURLError(w, httpx.SignedURLWrongSub)
				return
			}

			// lib[] presence and well-formedness — coverage is the
			// LibraryGuard's job because it needs the resource lookup.
			if _, err := claims.LibIDs(); err != nil {
				httpx.WriteSignedURLError(w, httpx.SignedURLWrongLib)
				return
			}

			ctx := context.WithValue(r.Context(), ctxKeyClaims, claims)
			ctx = context.WithValue(ctx, ctxKeySubject, claims.Sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// LibraryGuard runs after SignedURL. It calls the resolver to learn
// the resource's library id and rejects if the JWT's lib[] doesn't
// include it.
func LibraryGuard(resolver LibraryResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cl, ok := ClaimsFromContext(r.Context())
			if !ok {
				httpx.WriteSignedURLError(w, httpx.SignedURLMissing)
				return
			}
			lib, err := resolver.Resolve(r.Context(), r, cl)
			if err != nil {
				// Resource lookup failed — surface as 404, the JWT
				// itself is well-formed.
				httpx.Write(w, http.StatusNotFound, "resource-not-found",
					"resource not found", err.Error())
				return
			}
			if !cl.CoversLibrary(lib) {
				httpx.WriteSignedURLError(w, httpx.SignedURLWrongLib)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClaimsFromContext returns the verified claims attached by SignedURL.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	cl, ok := ctx.Value(ctxKeyClaims).(*Claims)
	return cl, ok
}

// ContextWithClaims attaches claims to a context. Useful for tests
// that bypass the SignedURL middleware but still want the handlers
// to find their subject in context.
func ContextWithClaims(ctx context.Context, c *Claims) context.Context {
	ctx = context.WithValue(ctx, ctxKeyClaims, c)
	ctx = context.WithValue(ctx, ctxKeySubject, c.Sub)
	return ctx
}

// SubjectFromContext returns the JWT's sub (also in the URL) for use
// in handler logic.
func SubjectFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxKeySubject).(string)
	return s
}

func mapVerifyError(err error) string {
	switch err {
	case ErrExpired:
		return httpx.SignedURLExpired
	case ErrSignature, ErrUnknownKID, ErrUnsupportedAlg:
		return httpx.SignedURLBadSignature
	case ErrMalformed, ErrNotYetValid:
		return httpx.SignedURLBadSignature
	default:
		return httpx.SignedURLBadSignature
	}
}
