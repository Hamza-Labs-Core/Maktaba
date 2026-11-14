// Package castgrant is the cast grant (Epic 58 story 58.07): the credential a signed-in
// sender (the Android Player, the Apple Player, the web) mints for Maktaba's Cast receiver,
// so the TV reports progress, renews its stream URLs and walks a play queue by itself,
// without the person's own token.
//
// A grant is an RS256 token of the home's signing keys with the audience "cast", written
// with the prefix "cast_" (Prefix). It names one video or one play queue of its user, lasts
// at most six hours (MaxTTL), and carries the minting sign-in's device age limit (`ac`).
// Its audience keeps it out of every route that takes the home's own tokens (aud "api").
//
//   - Mint writes one; POST /api/cast/grants (handlers/streaming.CastGrants) is the route
//     that does, after the role and parental checks.
//   - Bearer is the credential middleware: a valid grant becomes a principal of
//     principal.SourceCast for its user, read again from the database on every request
//     (a deleted user is no one; a library taken away is gone at once), never an admin.
//   - Gate holds such a principal to the routes a receiver needs, for its items only
//     (gate.go). Epic 57's parental gate holds it to its user's limit, as any sign-in.
package castgrant

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
)

const (
	// Audience is a grant's `aud`: no route that takes the home's tokens (aud "api")
	// accepts it.
	Audience = "cast"
	// Prefix starts every grant, so a log redactor and the credential middlewares know
	// one at a glance.
	Prefix = "cast_"
	// MaxTTL is the longest a grant lives.
	MaxTTL = 6 * time.Hour
	// Scope is the grant's scope as the API names it.
	Scope = "cast"
	// ReceiverOrigin is the origin Maktaba's Cast receiver is served from: the home's
	// API and its streaming service answer it cross-origin (bearer only, no cookies).
	ReceiverOrigin = "https://cast.maktaba.tv"
)

// Errors of Parse.
var (
	ErrNotAGrant = errors.New("castgrant: not a cast grant")
	ErrScope     = errors.New("castgrant: a grant names exactly one video or one play queue")
	ErrTooLong   = errors.New("castgrant: a grant lives at most six hours")
)

// skew is the clock skew jwt.Verify allows; a grant's life is checked with it.
const skew = 30 * time.Second

// Mint writes a grant for userID naming scope, living ttl from now (0 or more than
// MaxTTL: MaxTTL). ageCeiling is the minting sign-in's device age limit (nil: none).
func Mint(set *keys.Set, userID string, ageCeiling *int, scope principal.CastScope, now time.Time, ttl time.Duration) (string, time.Time, error) {
	if (scope.VideoID == "") == (scope.QueueID == "") {
		return "", time.Time{}, ErrScope
	}
	if userID == "" {
		return "", time.Time{}, errors.New("castgrant: no user")
	}
	if ttl <= 0 || ttl > MaxTTL {
		ttl = MaxTTL
	}
	exp := now.Add(ttl).UTC().Truncate(time.Second)
	tok, err := jwt.Sign(set, jwt.Claims{
		Iss: "maktaba",
		Aud: Audience,
		Sub: userID,
		Usr: userID,
		Iat: now.Unix(),
		Exp: exp.Unix(),
		Ac:  ageCeiling,
		Cv:  scope.VideoID,
		Cq:  scope.QueueID,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return Prefix + tok, exp, nil
}

// Parse checks a grant: its prefix, signature, time and audience, that it names exactly
// one video or queue, and that it was not minted to live longer than MaxTTL.
func Parse(set *keys.Set, grant string) (*jwt.Claims, error) {
	tok, ok := strings.CutPrefix(grant, Prefix)
	if !ok || tok == "" {
		return nil, ErrNotAGrant
	}
	c, err := jwt.Verify(set, tok, Audience)
	if err != nil {
		return nil, err
	}
	if c.Usr == "" {
		return nil, ErrNotAGrant
	}
	if (c.Cv == "") == (c.Cq == "") {
		return nil, ErrScope
	}
	if c.Exp == 0 || c.Iat == 0 || time.Duration(c.Exp-c.Iat)*time.Second > MaxTTL+skew {
		return nil, ErrTooLong
	}
	return c, nil
}

// Users reads a grant's user (users.Store).
type Users interface {
	GetByID(ctx context.Context, id string) (*users.User, error)
}

// Libraries reads the libraries a user may open now (authz.ACLStore).
type Libraries interface {
	LibrariesFor(ctx context.Context, userID string) ([]string, error)
}

// Bearer is the grant's credential middleware. For a request with no principal yet whose
// bearer is a valid grant, it attaches the grant's principal: its user as the database has
// it now, never an admin, with the libraries the user may open now (every library for an
// admin's grant, which the gate still holds to its items). A grant whose user is gone, or
// whose libraries cannot be read, is no one: the auth gate answers 401.
func Bearer(set *keys.Set, us Users, acl Libraries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if principal.FromContext(r.Context()) != nil || set == nil || us == nil {
				next.ServeHTTP(w, r)
				return
			}
			bearer := readBearer(r)
			if !strings.HasPrefix(bearer, Prefix) {
				next.ServeHTTP(w, r)
				return
			}
			c, err := Parse(set, bearer)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			u, err := us.GetByID(r.Context(), c.Usr)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			p := &principal.Principal{
				UserID:             u.ID,
				AccessAllLibraries: u.IsAdmin,
				Source:             principal.SourceCast,
				MustChangePassword: u.MustChangePassword,
				AgeCeiling:         c.Ac,
				Cast:               &principal.CastScope{VideoID: c.Cv, QueueID: c.Cq},
			}
			if !u.IsAdmin {
				if acl == nil {
					next.ServeHTTP(w, r)
					return
				}
				libs, err := acl.LibrariesFor(r.Context(), u.ID)
				if err != nil {
					next.ServeHTTP(w, r)
					return
				}
				if libs == nil {
					libs = []string{}
				}
				p.Libraries = libs
			}
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
		})
	}
}

// readBearer is the token of `Authorization: Bearer <tok>` ("" when none).
func readBearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) < len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(v[len(prefix):])
}
