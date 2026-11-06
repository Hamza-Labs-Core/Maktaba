// Package principal carries the authenticated identity through a
// request lifecycle. The HTTP middleware writes one of these into the
// context; handlers and the Authz layer read it back.
//
// Kept tiny on purpose — every field here is something handlers will
// need to discriminate authorization decisions against, and adding
// fields here later costs a search-and-replace across handlers.
package principal

import (
	"context"
	"sync/atomic"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

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

	// MustChangePassword is set for a web session whose password an
	// admin reset to a temporary one (users.must_change_password). The
	// auth gate (middleware.RequireAuthExcept) then lets the request
	// through only to the change-password form and the few endpoints
	// middleware.AllowedWhilePasswordChange names. The home's cookie
	// middleware sets it from the user row; a full node from the home's
	// introspection answer.
	MustChangePassword bool

	// CloudScope is set when the credential came from a Maktaba Cloud
	// sign-in (Epic 42 story 42.7, linked accounts): the link's scope,
	// CloudScopePlay or CloudScopeManage; "" for every other sign-in.
	// Whoever builds a principal from a cloud credential clears IsAdmin
	// for CloudScopePlay (ApplyCloudScope), so every admin check refuses
	// a play sign-in, even an admin's. A cloud sign-in also mints no
	// credential of its own that would outlive the link (ViaCloud).
	CloudScope string
}

// Cloud link scopes (Epic 42 story 42.7).
const (
	// CloudScopePlay: watch only, whatever the home user's role.
	CloudScopePlay = "play"
	// CloudScopeManage: the home user's full role (an admin's own link,
	// set by that admin on the home).
	CloudScopeManage = "manage"
)

// ApplyCloudScope sets the cloud scope and caps the principal by it: a
// play sign-in is never an admin, and an unknown scope is treated as play.
// A play cap also ends all-library access, which several routes read as
// "sees everything" (catalog-wide reports, every job): an admin's play
// sign-in reads the libraries in all (every library, listed) instead, and
// with all nil it reads only the Libraries it already names.
func (p *Principal) ApplyCloudScope(scope string, all []string) {
	if p == nil || scope == "" {
		return
	}
	if scope != CloudScopeManage {
		scope = CloudScopePlay
	}
	p.CloudScope = scope
	if scope == CloudScopePlay {
		p.IsAdmin = false
		if p.AccessAllLibraries {
			p.AccessAllLibraries = false
			p.Libraries = append([]string(nil), all...)
		}
	}
}

// ViaCloud reports whether the principal signed in through Maktaba Cloud.
// Such a sign-in may not mint a credential that outlives its link (an API
// token, a paired device, a new password, another cloud link).
func (p *Principal) ViaCloud() bool { return p != nil && p.CloudScope != "" }

type ctxKey struct{}

type slotKey struct{}

// Slot lets a middleware above the sign-in learn who the request was signed in
// as (the request logger names the user on its line, by user_h, Epic 53 story
// 53.01): WithSlot plants one, WithPrincipal fills it.
type Slot struct{ p atomic.Pointer[Principal] }

// Principal is who the request below was signed in as (nil: no one).
func (s *Slot) Principal() *Principal {
	if s == nil {
		return nil
	}
	return s.p.Load()
}

// WithSlot returns a child context carrying an empty Slot, and the slot.
func WithSlot(ctx context.Context) (context.Context, *Slot) {
	s := &Slot{}
	return context.WithValue(ctx, slotKey{}, s), s
}

// WithPrincipal returns a child context carrying p. nil principals are
// stored as nil (handlers can distinguish "not set" from "empty"). Lines
// logged with the context name the user by their hash (user_h).
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	if s, ok := ctx.Value(slotKey{}).(*Slot); ok && p != nil {
		s.p.Store(p)
	}
	if p != nil && p.UserID != "" {
		ctx = mlog.WithUserID(ctx, p.UserID)
	}
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
