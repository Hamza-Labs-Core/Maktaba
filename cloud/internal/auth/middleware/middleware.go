// Package middleware (auth) verifies the bearer token on every
// authenticated route and attaches the user id to the request context.
//
// We keep this separate from the generic HTTP middleware package so
// that the relay router can opt in/out per route — e.g. /v1/auth/login
// must not require auth, but /v1/account/me must.
package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// RequireUser returns a middleware that 401s when no valid access
// token is present, and stashes the user id on the request context
// when one is.
func RequireUser(signer *token.Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				unauth(w)
				return
			}
			claims, err := signer.Verify(strings.TrimPrefix(h, "Bearer "), time.Now())
			if err != nil {
				unauth(w)
				return
			}
			ctx := cmw.WithUserID(r.Context(), claims.Sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func unauth(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
}
