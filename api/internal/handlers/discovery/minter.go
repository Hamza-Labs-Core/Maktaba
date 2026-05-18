// Real TokenMinter for the pairing Exchange path (Epic 15 Story 15.5).
//
// A paired device must end the flow holding a usable session. This
// mints the same credential pair the native-login path issues
// (handlers/auth.respondNative): an RS256 access JWT carrying the
// user's library ACL snapshot + an opaque, device-bound refresh token.
// The refresh token's client_meta records the pairing origin and the
// device kind/label so the device list and audit trail can attribute
// it.
//
// Default refresh TTL is 30 days (Story 15.5 AC: "device-bound refresh
// token, valid 30 d") — shorter than the 60-day interactive-login
// default because a paired device proves possession of a one-time code
// rather than a password.
package discovery

import (
	"context"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
)

// PairRefreshTTL is the lifetime of a pairing-issued refresh token.
const PairRefreshTTL = 30 * 24 * time.Hour

// pairAccessTTL matches the interactive access-token lifetime (15 min,
// Story 10.3) — the access token is cheap to rotate via the refresh
// token, so a short window limits the blast radius of a leak.
const pairAccessTTL = 15 * time.Minute

// userResolver is the slice of users.Store the minter needs: look up a
// user by id so the access token's is_admin / library scope is
// accurate. Interface-seam so the minter is unit-testable without a DB.
type userResolver interface {
	IsAdmin(ctx context.Context, userID string) (bool, error)
}

// libraryResolver is the read-side seam the minter uses to snapshot the
// user's readable libraries into the access token's `lib[]` claim — the
// exact source of truth native login uses (handlers/auth.librariesFor →
// authz.ACLStore.LibrariesFor). Without this claim a paired non-admin
// is default-denied on every library read/search (authz.go HasLibrary →
// ErrForbidden; search.go empty-libs → empty results), so the pairing
// flow dead-ends for non-admins. Interface-seam so the mint path is
// unit-testable without a database.
type libraryResolver interface {
	LibrariesFor(ctx context.Context, userID string) ([]string, error)
}

// refreshIssuer is the slice of refresh.Store the minter needs.
type refreshIssuer interface {
	Issue(ctx context.Context, in refresh.IssueInput) (*refresh.Token, error)
}

// jwtSigner signs access claims. Production uses a *keys.Set; the seam
// keeps the minter testable without standing up an RSA key set.
type jwtSigner interface {
	Sign(c jwt.Claims) (string, error)
}

// keySetSigner adapts a *keys.Set to jwtSigner.
type keySetSigner struct{ Set *keys.Set }

func (k keySetSigner) Sign(c jwt.Claims) (string, error) { return jwt.Sign(k.Set, c) }

// tokenMinter is the production TokenMinter.
type tokenMinter struct {
	users   userResolver
	libs    libraryResolver
	refresh refreshIssuer
	signer  jwtSigner
	now     func() time.Time
}

// NewTokenMinter builds the production minter. keySet may be nil only
// in tests; production callers must pass a live set or pairing Exchange
// is left disabled (the handler returns 503 when Minter is nil). acl is
// the library ACL source; a nil acl yields an empty `lib[]` snapshot
// (the same fallback handlers/auth.librariesFor uses when its ACL is
// unset) — correct for admins, default-deny for non-admins.
func NewTokenMinter(u userResolver, acl libraryResolver, rt refreshIssuer, keySet *keys.Set) TokenMinter {
	return &tokenMinter{
		users:   u,
		libs:    acl,
		refresh: rt,
		signer:  keySetSigner{Set: keySet},
	}
}

func (m *tokenMinter) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now().UTC()
}

// Mint issues the access JWT + refresh token for a paired device.
func (m *tokenMinter) Mint(ctx context.Context, userID, deviceKind, deviceLabel string) (MintedTokens, error) {
	now := m.clock()

	isAdmin := false
	if m.users != nil {
		a, err := m.users.IsAdmin(ctx, userID)
		if err != nil {
			return MintedTokens{}, err
		}
		isAdmin = a
	}

	// Mirror handlers/auth.librariesFor exactly: admins read everything,
	// so skip the ACL lookup and emit an empty slice (the verifier sets
	// AccessAllLibraries from is_admin). A non-admin gets the ACL
	// snapshot; a nil resolver or nil result yields an empty slice
	// rather than a panic. Without this claim a paired non-admin is
	// default-denied on every library read/search (Defect 1 / AC 15.5).
	libs := []string{}
	if !isAdmin && m.libs != nil {
		l, err := m.libs.LibrariesFor(ctx, userID)
		if err != nil {
			return MintedTokens{}, err
		}
		if l != nil {
			libs = l
		}
	}

	access, err := m.signer.Sign(jwt.Claims{
		Iss:     "maktaba",
		Aud:     "api",
		Sub:     userID,
		Iat:     now.Unix(),
		Exp:     now.Add(pairAccessTTL).Unix(),
		Usr:     userID,
		Lib:     libs,
		IsAdmin: isAdmin,
	})
	if err != nil {
		return MintedTokens{}, err
	}

	meta := map[string]any{"origin": "pairing"}
	if deviceKind != "" {
		meta["device_kind"] = deviceKind
	}
	if deviceLabel != "" {
		meta["device_label"] = deviceLabel
	}
	rt, err := m.refresh.Issue(ctx, refresh.IssueInput{
		UserID:     userID,
		ClientMeta: meta,
		TTL:        PairRefreshTTL,
	})
	if err != nil {
		return MintedTokens{}, err
	}

	return MintedTokens{
		AccessToken:      access,
		AccessExpiresIn:  int(pairAccessTTL.Seconds()),
		RefreshToken:     rt.Plaintext,
		RefreshExpiresIn: int(time.Until(rt.ExpiresAt).Seconds()),
		UserID:           userID,
	}, nil
}

// usersAdminResolver adapts *users.Store to userResolver. Defined here
// (not in the users package) so the minter owns its own seam.
type usersAdminResolver struct {
	get func(ctx context.Context, id string) (bool, error)
}

func (u usersAdminResolver) IsAdmin(ctx context.Context, id string) (bool, error) {
	return u.get(ctx, id)
}

// NewUsersAdminResolver wraps a GetByID-style lookup into the seam the
// minter expects. The caller passes a closure over users.Store.GetByID
// so this package does not import users (avoids an import cycle and
// keeps the seam narrow).
func NewUsersAdminResolver(get func(ctx context.Context, id string) (bool, error)) userResolver {
	return usersAdminResolver{get: get}
}
