// Package authz is Maktaba's permission decision point (Story 10.13).
//
// Handlers call `authz.Can(ctx, "video.read", videoID)` rather than
// reaching into the principal directly, so v2 can swap the policy
// engine (RBAC, ABAC) without touching handlers. v1 ships a fixed
// rule set:
//
//   - `*.read`     → caller must have the resource's library_id in
//     their `lib[]` (or be admin / single-user mode).
//   - `*.write`    → admin only, except for user-scoped resources
//     (playback_state, saved_searches) where the owner
//     is allowed.
//   - `library.*`  → admin only.
//
// The interface intentionally takes a string action; permission
// strings are conventional ("video.read", "library.write") and
// readable in audit logs without a translation table.
package authz

import (
	"context"
	"errors"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ErrForbidden is returned by Can when the principal is authenticated
// but lacks the requested permission. Handlers map this to 403
// problem+json `type: forbidden` (Story 10.13 AC-4) with a generic
// message — never leak whether the resource exists.
var ErrForbidden = errors.New("forbidden")

// ErrUnauthenticated is returned when no principal is attached. The
// handler should surface this as 401 (RequireAuth normally catches
// this earlier).
var ErrUnauthenticated = errors.New("unauthenticated")

// LibraryResolver is the read-only adapter Authz uses to map a
// resource id to its owning library_id. Handlers wire concrete
// implementations against the videos / playback_state / saved_searches
// tables.
type LibraryResolver interface {
	// LibraryOf returns the library_id that owns `resourceID` for the
	// given action prefix (e.g. "video", "subtitle"). Returns the
	// empty string when the resource has no library scope (e.g.
	// `library.read` itself, where the resourceID *is* the library).
	LibraryOf(ctx context.Context, action, resourceID string) (string, error)
}

// OwnershipResolver answers "did this user create this resource?" for
// per-user resources like playback_state and saved_searches.
type OwnershipResolver interface {
	OwnerOf(ctx context.Context, action, resourceID string) (userID string, err error)
}

// Authz is the permission-decision interface handlers consume.
type Authz interface {
	// Can reports whether the principal in ctx may perform `action`
	// on `resourceID`. ResourceID may be empty for global actions
	// (e.g. "user.create").
	//
	// Returns nil (allow), ErrForbidden, or ErrUnauthenticated.
	// Concrete implementations may return wrapped infrastructure
	// errors as well.
	Can(ctx context.Context, action, resourceID string) error
}

// V1 is the default policy described above. It looks up library
// membership via Lib (when set) and ownership via Owner (when set);
// both are optional — a nil Lib makes "*.read" admin-only, and a nil
// Owner makes per-user writes admin-only. That degraded mode is the
// safe default for handlers that haven't been wired up to the
// resolvers yet.
type V1 struct {
	Lib   LibraryResolver
	Owner OwnershipResolver

	// SingleUserMode, when true, makes every authenticated request
	// pass *.read on any library (Story 10.13 AC-1). Wired from the
	// admin-token presence check at boot.
	SingleUserMode bool
}

// Can implements Authz.
func (v *V1) Can(ctx context.Context, action, resourceID string) error {
	p := principal.FromContext(ctx)
	if p == nil {
		return ErrUnauthenticated
	}

	// Admins bypass everything. Story 10.13 v1 has no read-only-admin
	// distinction; that's reserved for v2.
	if p.IsAdmin {
		return nil
	}

	res, scope := splitAction(action)

	// `library.*` is admin-only, no exceptions.
	if res == "library" {
		return ErrForbidden
	}

	switch scope {
	case "read":
		if v.SingleUserMode {
			return nil
		}
		if v.Lib == nil {
			return ErrForbidden
		}
		libID, err := v.Lib.LibraryOf(ctx, res, resourceID)
		if err != nil {
			return err
		}
		if libID == "" {
			// No library scope ⇒ default-deny for non-admins.
			return ErrForbidden
		}
		if !p.HasLibrary(libID) {
			return ErrForbidden
		}
		return nil

	case "write":
		// User-scoped resources allow self-write.
		if isUserScoped(res) && v.Owner != nil {
			owner, err := v.Owner.OwnerOf(ctx, res, resourceID)
			if err != nil {
				return err
			}
			if owner == p.UserID {
				return nil
			}
		}
		return ErrForbidden
	}
	return ErrForbidden
}

// Static is a fixed-decision Authz, useful for handler tests where
// the test wants to control allow/deny without wiring resolvers.
type Static struct{ Allow bool }

func (s Static) Can(_ context.Context, _, _ string) error {
	if s.Allow {
		return nil
	}
	return ErrForbidden
}

func splitAction(a string) (resource, scope string) {
	for i := 0; i < len(a); i++ {
		if a[i] == '.' {
			return a[:i], a[i+1:]
		}
	}
	return a, ""
}

func isUserScoped(resource string) bool {
	switch resource {
	case "playback_state", "saved_search":
		return true
	}
	return false
}
