package pat

import (
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

// Middleware returns a credential-attaching middleware for personal
// access tokens. It sits in the same anonymous-attach tier as JWTBearer
// and CookieAuth (auth_bootstrap.applySecurity): on a valid
// `Authorization: Bearer pat_...` it attaches the owner's principal; on
// anything else it is a transparent pass-through and a downstream
// RequireAuthExcept turns "no principal" into 401.
//
// Library access mirrors the cookie path: admins get AccessAllLibraries,
// non-admins get the live ACL snapshot. A PAT therefore never grants
// more than its owner currently has.
func Middleware(store *Store, userStore *users.Store, acl *authz.ACLStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A previous middleware (admin-token / JWT) already decided.
			if principal.FromContext(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}
			bearer := readBearer(r)
			if !IsPAT(bearer) {
				next.ServeHTTP(w, r)
				return
			}
			tok, err := store.Authenticate(r.Context(), bearer)
			if err != nil {
				// Invalid/expired/revoked → continue anonymously.
				next.ServeHTTP(w, r)
				return
			}
			u, err := userStore.GetByID(r.Context(), tok.UserID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p := &principal.Principal{
				UserID:             u.ID,
				IsAdmin:            u.IsAdmin,
				AccessAllLibraries: u.IsAdmin,
				Source:             principal.SourcePAT,
			}
			if !u.IsAdmin && acl != nil {
				if libs, lerr := acl.LibrariesFor(r.Context(), u.ID); lerr == nil {
					p.Libraries = libs
				}
			}
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
		})
	}
}

// readBearer extracts the token from `Authorization: Bearer <tok>`.
// Local copy so the pat package doesn't depend on the middleware
// package (which would create a wiring-order coupling).
func readBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(v, prefix) {
		return ""
	}
	return strings.TrimSpace(v[len(prefix):])
}
