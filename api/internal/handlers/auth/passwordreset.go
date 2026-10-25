package auth

// Admin password reset. Maktaba has no mailer, so there is no
// self-service "forgot password": an admin resets the password on the
// Users page, in one of two ways:
//
//   - mode "temporary": the server picks a random temporary password,
//     returns it once in the response for the admin to hand over, and sets
//     users.must_change_password (slot 0138). Until the user chooses a new
//     password, a web session reaches only the change-password form, and
//     the apps (JWT clients) cannot sign in with it.
//   - mode "set": the admin types the new password. must_change is
//     optional (default false).
//
// Either way every web session and refresh family of the user is revoked,
// and the reset is written to the security audit log — the password never
// is, nor is it logged anywhere.

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// TypePasswordChangeRequired is the problem type a session gets for any
// request but the change-password form after an admin reset.
const TypePasswordChangeRequired = middleware.TypePasswordChangeRequired

// CredentialRevoker ends every credential a user holds: web sessions and
// refresh-token families. It returns how many were revoked.
type CredentialRevoker interface {
	RevokeAllCredentials(ctx context.Context, userID string) (int64, error)
}

// storeRevoker is the production CredentialRevoker over the session and
// refresh stores.
type storeRevoker struct {
	sessions interface {
		RevokeAllForUser(ctx context.Context, userID string) (int64, error)
	}
	refresh interface {
		RevokeAllForUser(ctx context.Context, userID string) (int64, error)
	}
}

func (s storeRevoker) RevokeAllCredentials(ctx context.Context, userID string) (int64, error) {
	a, err := s.sessions.RevokeAllForUser(ctx, userID)
	if err != nil {
		return a, err
	}
	b, err := s.refresh.RevokeAllForUser(ctx, userID)
	return a + b, err
}

func (h *Handler) credentials() CredentialRevoker {
	if h.Credentials != nil {
		return h.Credentials
	}
	if h.Sessions == nil || h.RefreshTokens == nil {
		return nil
	}
	return storeRevoker{sessions: h.Sessions, refresh: h.RefreshTokens}
}

// resetPasswordRequest is the POST /api/users/{id}/reset-password body.
type resetPasswordRequest struct {
	// Mode is "temporary" (server picks the password) or "set".
	Mode string `json:"mode"`
	// Password is the new password for mode "set".
	Password string `json:"password,omitempty"`
	// MustChange asks the user to replace a "set" password at the next
	// sign-in. Always true for "temporary".
	MustChange bool `json:"must_change,omitempty"`
}

// resetPasswordResponse is the answer. TemporaryPassword is present only
// for mode "temporary", and only in this response.
type resetPasswordResponse struct {
	UserID             string `json:"user_id"`
	Mode               string `json:"mode"`
	MustChangePassword bool   `json:"must_change_password"`
	TemporaryPassword  string `json:"temporary_password,omitempty"`
	SessionsRevoked    int64  `json:"sessions_revoked"`
}

// AdminResetPassword implements POST /api/users/{id}/reset-password.
func (h *Handler) AdminResetPassword(w http.ResponseWriter, r *http.Request) {
	actor := h.requireAdmin(w, r)
	if actor == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	if id == users.SentinelAdminID {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "id", Message: "the built-in admin-token account has no password"},
		}))
		return
	}
	var req resetPasswordRequest
	if err := common.ReadJSON(r, &req, 8*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	var password string
	mustChange := req.MustChange
	switch req.Mode {
	case "temporary":
		if req.Password != "" {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
				{Field: "password", Message: "not used with mode temporary"},
			}))
			return
		}
		tmp, err := temporaryPassword()
		if err != nil {
			httperror.Write(w, r, httperror.Internal("generate password").Wrap(err))
			return
		}
		password, mustChange = tmp, true
	case "set":
		if len(req.Password) < minPasswordLen {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
				{Field: "password", Message: "must be at least 8 characters"},
			}))
			return
		}
		password = req.Password
	default:
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{
			{Field: "mode", Message: "must be temporary or set"},
		}))
		return
	}
	revoker := h.credentials()
	if revoker == nil || h.UserAdmin == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}

	if _, err := h.UserAdmin.Update(r.Context(), id, users.UpdateInput{
		Password:           &password,
		MustChangePassword: &mustChange,
	}); err != nil {
		h.writeUserStoreError(w, r, err)
		return
	}
	// Whoever held the old password (or a session made with it) is out.
	revoked, err := revoker.RevokeAllCredentials(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("revoke sessions").Wrap(err))
		return
	}
	// nodes drop their cached copy of this user's sessions (story 35.9 AC4)
	sessions.NotifyRevoked(sessions.Revocation{UserID: id, Reason: "password_reset"})
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: actor.UserID,
		TargetID:    id,
		Payload: map[string]any{
			"action":           "user.password_reset",
			"mode":             req.Mode,
			"must_change":      mustChange,
			"sessions_revoked": revoked,
			"ip":               remoteIP(r),
		},
	})

	resp := resetPasswordResponse{
		UserID: id, Mode: req.Mode, MustChangePassword: mustChange, SessionsRevoked: revoked,
	}
	if req.Mode == "temporary" {
		resp.TemporaryPassword = password
	}
	// The temporary password must never sit in a cache.
	w.Header().Set("Cache-Control", "no-store")
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// temporaryAlphabet leaves out look-alikes (0/O, 1/l/I) so the password
// can be read out or copied by hand.
const temporaryAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// temporaryPassword returns 16 random characters in groups of four,
// e.g. "k7Qm-xR2p-..." (about 92 bits).
func temporaryPassword() (string, error) {
	var b strings.Builder
	n := big.NewInt(int64(len(temporaryAlphabet)))
	for i := 0; i < 16; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b.WriteByte(temporaryAlphabet[k.Int64()])
	}
	return b.String(), nil
}

// passwordChangeRequired is the 403 a must-change session gets.
func passwordChangeRequired() *httperror.Error {
	return &httperror.Error{
		Type:   TypePasswordChangeRequired,
		Title:  "password change required",
		Status: http.StatusForbidden,
		Detail: "choose a new password in the web app before continuing",
	}
}

// allowedWhilePasswordChange is the shared allowlist the auth gate applies
// (middleware.AllowedWhilePasswordChange).
func allowedWhilePasswordChange(r *http.Request) bool {
	return middleware.AllowedWhilePasswordChange(r)
}
