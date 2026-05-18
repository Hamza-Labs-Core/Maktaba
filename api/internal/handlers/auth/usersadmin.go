package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Problem-type URIs for the admin user-management surface (Story 10.1
// AC-Edge). The store enforces the invariants; these map its sentinel
// errors onto the wire.
const (
	TypeUsernameExists = "https://maktaba.dev/problems/username-exists"
	TypeLastAdmin      = "https://maktaba.dev/problems/last-admin"
)

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
