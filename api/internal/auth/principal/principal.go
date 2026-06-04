// Package principal carries the authenticated identity through a
// request lifecycle. The HTTP middleware writes one of these into the
// context; handlers and the Authz layer read it back.
//
// Kept tiny on purpose — every field here is something handlers will
// need to discriminate authorization decisions against, and adding
// fields here later costs a search-and-replace across handlers.
package principal

import "context"

// Source distinguishes how the principal was authenticated. Audit
// rows include this so an operator can tell single-user-token
// activity from real JWT-backed sessions (Story 10.9 AC test).
type Source string

const (
	// SourceAdminToken is the single-user `MAKTABA_ADMIN_TOKEN`
	// bypass path (Story 10.9). The principal is always the sentinel
	// admin user.
	SourceAdminToken Source = "admin_token"

	// SourceJWT is a regular bearer-JWT request (Story 10.3).
	SourceJWT Source = "jwt"

	// SourceCookie is a web-session cookie (Story 10.2). Stories 10.2
	// and onward fill this in.
	SourceCookie Source = "cookie"

	// SourcePAT is a personal access token (web-pages-batch2). The
	// principal is the token's owner; library access is resolved from
	// the DB ACL at request time, same as the cookie path.
	SourcePAT Source = "pat"
)

// Principal is the authenticated identity for a single request.
type Principal struct {
	UserID  string
	IsAdmin bool

	// Libraries is the set of library_ids this principal has read
	// access to. Snapshotted from the JWT `lib[]` claim (Story 10.13
	// AC-5) for SourceJWT; populated from the DB ACL for SourceCookie;
	// for SourceAdminToken this is empty and AccessAllLibraries is
	// true (the sentinel admin reads everything).
	Libraries []string

	// AccessAllLibraries is true when the principal should be granted
	// `*.read` on every library regardless of the Libraries slice.
	// Set by the admin-token bypass and by is_admin JWTs.
	AccessAllLibraries bool

	Source Source
}

type ctxKey struct{}

// WithPrincipal returns a child context carrying p. nil principals are
// stored as nil (handlers can distinguish "not set" from "empty").
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext extracts the principal, or nil if no auth middleware
// ran. Anonymous endpoints (e.g. JWKS publication, health) get nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// HasLibrary reports whether this principal can read library_id. Used
// by the Authz layer for `*.read` checks.
func (p *Principal) HasLibrary(libraryID string) bool {
	if p == nil {
		return false
	}
	if p.AccessAllLibraries {
		return true
	}
	for _, l := range p.Libraries {
		if l == libraryID {
			return true
		}
	}
	return false
}
