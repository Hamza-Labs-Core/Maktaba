package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Problem-type URIs for the admin user-management surface (Story 10.1
// AC-Edge). The store enforces the invariants; these map its sentinel
// errors onto the wire.
const (
	TypeUsernameExists = "https://maktaba.dev/problems/username-exists"
	TypeLastAdmin      = "https://maktaba.dev/problems/last-admin"
	// TypeSeatLimit is returned when provisioning another user would
	// exceed the licensed seat cap (Epic 16 Story 16.2). It is a 403:
	// the request is well-formed and the caller is authorised, but the
	// instance's entitlement forbids the action.
	TypeSeatLimit = "https://maktaba.dev/problems/seat-limit"
)

// SeatLimiter reports the maximum number of provisionable user seats
// for the running instance's entitlement (Epic 16 Story 16.2).
// subscriptions.NewSeatLimiter wraps the live entitlement Store;
// subscriptions.SeatsUnlimited means no cap.
type SeatLimiter interface {
	SeatLimit() int
}

// SeatCounter reports how many real user accounts already exist (seats
// consumed). *users.Store satisfies it via CountUsers.
type SeatCounter interface {
	CountUsers(ctx context.Context) (int, error)
}

// seatsUnlimited is subscriptions.SeatsUnlimited.
const seatsUnlimited = subscriptions.SeatsUnlimited

// enforceSeatLimit is the Epic 16 Story 16.2 gate at the user-create
// boundary — the first real premium-gate call site. It returns a
// non-nil *httperror.Error to abort the create when the licensed seat
// cap would be exceeded, or when the current seat count cannot be
// determined (fail-closed: never silently over-provision a paid tier).
// With no SeatLimiter wired the gate is inert (returns nil).
func (h *Handler) enforceSeatLimit(ctx context.Context) error {
	if h.Seats == nil {
		return nil // gate not wired (free build / no entitlement source)
	}
	limit := h.Seats.SeatLimit()
	if limit == seatsUnlimited {
		return nil // pro tier — no cap
	}
	counter := h.SeatCount
	if counter == nil {
		// Fall back to the limiter if it also counts (the production
		// wiring keeps these as separate seams, but a combined test
		// double is convenient).
		if c, ok := h.Seats.(SeatCounter); ok {
			counter = c
		}
	}
	if counter == nil {
		// We have a cap but no way to count — cannot prove we're under
		// it. Fail closed.
		return httperror.Unavailable(0)
	}
	n, err := counter.CountUsers(ctx)
	if err != nil {
		return httperror.Unavailable(0)
	}
	if n+1 > limit {
		tier := "free"
		if tn, ok := h.Seats.(interface{ TierName() string }); ok {
			tier = tn.TierName()
		}
		return seatRefusal(tier, limit, n)
	}
	return nil
}

// seatRefusal says in plain words why another user cannot be added and
// what would allow it; the web shows the detail as is.
func seatRefusal(tier string, limit, used int) *httperror.Error {
	label := subscriptions.TierLabel(tier)
	users := func(n int) string {
		if n == 1 {
			return "1 user"
		}
		return fmt.Sprintf("%d users", n)
	}
	detail := fmt.Sprintf("This home's %s plan allows %s and it has %d. ", label, users(limit), used)
	if tier == string(subscriptions.TierHome) {
		detail += "Pro has no limit: apply a Pro license in Settings → Subscription."
	} else {
		detail += "Adding users needs the Home plan (4 users) or Pro (unlimited): apply a license in Settings → Subscription."
	}
	return (&httperror.Error{
		Type: TypeSeatLimit, Title: "User limit reached", Status: http.StatusForbidden, Detail: detail,
	}).With("feature", string(subscriptions.FeatureMultiUser)).With("tier", tier).
		With("seats_allowed", limit).With("seats_used", used)
}

// UserAdmin is the seam the admin user-management surface drives.
// *users.Store satisfies it; unit tests inject a fake so the HTTP
// behaviour (status mapping, admin gate, secret redaction) is testable
// without a DB. The store layer (Story 10.1) already implements every
// invariant — uniqueness, last-admin protection, lock clearing — and
// was complete but had no HTTP caller (HLB-391). This wires it.
type UserAdmin interface {
	Create(ctx context.Context, in users.CreateInput) (*users.User, error)
	Update(ctx context.Context, id string, in users.UpdateInput) (*users.User, error)
	Delete(ctx context.Context, id string) error
	Unlock(ctx context.Context, id string) error
}

// createUserRequest is the JSON body for POST /api/users.
type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// updateUserRequest is the JSON body for PATCH /api/users/{id}. All
// fields are pointers so "absent" is distinguishable from "set to
// zero value" (e.g. demote to is_admin:false).
type updateUserRequest struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
	IsAdmin  *bool   `json:"is_admin"`
}

// requireAdmin is the shared gate for the user-management surface. The
// global RequireAuthExcept gate already 401s anonymous callers; this is
// defence-in-depth + the non-admin 403 (the handler stays correct even
// if mounted without RequireAdmin in front).
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) *principal.Principal {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		writeAdminGate(w, r, p)
		return nil
	}
	return p
}

// AdminCreateUser implements POST /api/users (Story 10.1 AC-3).
func (h *Handler) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	actor := h.requireAdmin(w, r)
	if actor == nil {
		return
	}
	var req createUserRequest
	if err := common.ReadJSON(r, &req, 8*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if req.Password == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "password", Message: "must not be empty"},
		}))
		return
	}
	// Epic 16 Story 16.2 — seat enforcement. The server enforces the
	// gate (clients only render UI). Checked *before* the store call so
	// an over-cap request never mutates state.
	if e := h.enforceSeatLimit(r.Context()); e != nil {
		httperror.Write(w, r, e)
		return
	}
	u, err := h.UserAdmin.Create(r.Context(), users.CreateInput{
		Username: req.Username,
		Password: req.Password,
		IsAdmin:  req.IsAdmin,
	})
	if err != nil {
		h.writeUserStoreError(w, r, err)
		return
	}
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: actor.UserID,
		TargetID:    u.ID,
		Payload:     map[string]any{"action": "user.created", "username": u.Username, "is_admin": u.IsAdmin},
	})
	common.WriteJSON(w, r, http.StatusCreated, toUserResponse(u))
}

// AdminUpdateUser implements PATCH /api/users/{id} (Story 10.1 AC-3).
func (h *Handler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor := h.requireAdmin(w, r)
	if actor == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	var req updateUserRequest
	if err := common.ReadJSON(r, &req, 8*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if req.Password != nil && *req.Password == "" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "password", Message: "must not be empty"},
		}))
		return
	}
	u, err := h.UserAdmin.Update(r.Context(), id, users.UpdateInput{
		Username: req.Username,
		Password: req.Password,
		IsAdmin:  req.IsAdmin,
	})
	if err != nil {
		h.writeUserStoreError(w, r, err)
		return
	}
	// nodes drop their cached copy of this user's sessions (story 35.9 AC4): a new
	// password, name or admin flag is re-read from the home at once
	sessions.NotifyRevoked(sessions.Revocation{UserID: id, Reason: "user_updated"})
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: actor.UserID,
		TargetID:    id,
		Payload:     map[string]any{"action": "user.updated"},
	})
	common.WriteJSON(w, r, http.StatusOK, toUserResponse(u))
}

// AdminDeleteUser implements DELETE /api/users/{id} (Story 10.1 AC-3).
func (h *Handler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := h.requireAdmin(w, r)
	if actor == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	if err := h.UserAdmin.Delete(r.Context(), id); err != nil {
		h.writeUserStoreError(w, r, err)
		return
	}
	sessions.NotifyRevoked(sessions.Revocation{UserID: id, Reason: "user_deleted"})
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: actor.UserID,
		TargetID:    id,
		Payload:     map[string]any{"action": "user.deleted"},
	})
	common.WriteNoContent(w)
}

// AdminUnlockUser implements POST /api/users/{id}/unlock (Story 10.1
// AC-3). This is the operator escape hatch for an account locked by
// the brute-force counter (HLB-391/398).
func (h *Handler) AdminUnlockUser(w http.ResponseWriter, r *http.Request) {
	actor := h.requireAdmin(w, r)
	if actor == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	if err := h.UserAdmin.Unlock(r.Context(), id); err != nil {
		h.writeUserStoreError(w, r, err)
		return
	}
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: actor.UserID,
		TargetID:    id,
		Payload:     map[string]any{"action": "user.unlocked"},
	})
	common.WriteNoContent(w)
}

// writeUserStoreError maps the store's sentinel errors onto the RFC
// 9457 envelope: username collision and last-admin protection are 409
// with distinct types; an unknown id is 404; anything else is a
// generic 500 (never leak the underlying message).
func (h *Handler) writeUserStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, users.ErrUsernameExists):
		httperror.Write(w, r, httperror.Conflict(TypeUsernameExists, "username already exists"))
	case errors.Is(err, users.ErrLastAdmin):
		httperror.Write(w, r, httperror.Conflict(TypeLastAdmin, "operation would leave the system with no admin"))
	case errors.Is(err, users.ErrNotFound):
		httperror.Write(w, r, httperror.NotFound("user"))
	default:
		httperror.Write(w, r, httperror.Internal("user store"))
	}
}
