// Package account exposes the user-facing account management
// endpoints: profile read/update, password change, email change,
// account deletion (with 30-day reversible hold).
package account

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/password"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps groups collaborators for the handler set.
type Deps struct {
	Users    *stores.Users
	Sessions *sessions.Store
}

// Mount registers the account routes. The caller is expected to wrap
// the underlying router with the bearer-token middleware so each
// handler sees a populated user id on the context.
func Mount(r interface {
	Get(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}, d Deps) {
	r.Get("/v1/account/me", d.Me)
	r.Patch("/v1/account/me", d.UpdateProfile)
	r.Post("/v1/account/password", d.ChangePassword)
	r.Post("/v1/account/email", d.RequestEmailChange)
	r.Delete("/v1/account/me", d.RequestDeletion)
}

type meView struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name,omitempty"`
	Locale        string    `json:"locale"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	Plan          string    `json:"plan"`
	CreatedAt     time.Time `json:"created_at"`
}

func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, "lookup_failed", err.Error())
		return
	}
	v := meView{
		ID:            u.ID,
		Email:         u.Email,
		EmailVerified: u.EmailVerified,
		Locale:        u.Locale,
		Plan:          u.Plan,
		CreatedAt:     u.CreatedAt,
	}
	if u.DisplayName.Valid {
		v.DisplayName = u.DisplayName.String
	}
	if u.AvatarURL.Valid {
		v.AvatarURL = u.AvatarURL.String
	}
	writeJSON(w, 200, v)
}

type updateProfileReq struct {
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
	AvatarURL   string `json:"avatar_url"`
}

func (d *Deps) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req updateProfileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	uid := middleware.GetUserID(r.Context())
	if err := d.Users.UpdateProfile(r.Context(), uid, req.DisplayName, req.Locale, req.AvatarURL); err != nil {
		writeErr(w, 500, "update_failed", err.Error())
		return
	}
	d.Me(w, r)
}

type changePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword requires the current password (defense-in-depth: even
// if a session is hijacked, the attacker can't lock the user out by
// changing the password). On success we revoke all sessions for the
// user — the calling client must re-login.
func (d *Deps) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid_body", err.Error())
		return
	}
	if err := password.Validate(req.NewPassword); err != nil {
		writeErr(w, 422, "weak_password", err.Error())
		return
	}
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		writeErr(w, 500, "lookup_failed", err.Error())
		return
	}
	if !u.PasswordHash.Valid || argon2id.Verify(req.CurrentPassword, u.PasswordHash.String) != nil {
		writeErr(w, 401, "wrong_password", "")
		return
	}
	newHash, err := argon2id.Hash(req.NewPassword, argon2id.DefaultParams())
	if err != nil {
		writeErr(w, 500, "hash_failed", err.Error())
		return
	}
	if err := d.Users.SetPasswordHash(r.Context(), uid, newHash); err != nil {
		writeErr(w, 500, "update_failed", err.Error())
		return
	}
	_ = d.Sessions.RevokeAllForUser(r.Context(), uid)
	w.WriteHeader(http.StatusNoContent)
}

// RequestEmailChange begins a two-step flow: the new address receives
// a token, the caller redeems it from a different endpoint we don't
// land here (verification email worker). We persist a pending row so
// the redeemer can resolve which user is asking.
//
// NOTE: this is currently a stub — it returns 202 without reading the
// request body or persisting anything. The persistence + verification
// worker land with the mail-provider integration (see CODE_QUALITY_REVIEW).
func (d *Deps) RequestEmailChange(w http.ResponseWriter, _ *http.Request) {
	// Implementation note: the actual send is deferred to a worker;
	// here we only persist the request. Verifier endpoint lands when
	// the mail provider integration is wired.
	w.WriteHeader(http.StatusAccepted)
}

// RequestDeletion soft-deletes by enqueueing a 30-day hold. The user
// can cancel within that window by logging back in.
func (d *Deps) RequestDeletion(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	_, _ = d.Users.DB.ExecContext(r.Context(), `
        INSERT INTO account_deletions (user_id, purge_after)
        VALUES ($1, now() + INTERVAL '30 days')
        ON CONFLICT (user_id) DO UPDATE SET requested_at = now(), purge_after = now() + INTERVAL '30 days', cancelled_at = NULL
    `, uid)
	_ = d.Sessions.RevokeAllForUser(r.Context(), uid)
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
