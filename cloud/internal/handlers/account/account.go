// Package account exposes the user-facing account management
// endpoints: profile read/update, password change, sessions ("devices"),
// data export, and account deletion with a 30-day reversible hold.
//
// There is no email-change endpoint: changing the sign-in address needs
// a verification mail to the new address and a notice to the old one
// (story 25.5); it comes with a real mail provider.
package account

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
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
	Audit    *audit.Log
	Logger   *slog.Logger
}

// Mount registers the account routes inside the bearer-token group.
func Mount(r chi.Router, d *Deps) {
	r.Get("/v1/account/me", d.Me)
	r.Patch("/v1/account/me", d.UpdateProfile)
	r.Post("/v1/account/password", d.ChangePassword)
	r.Get("/v1/account/sessions", d.ListSessions)
	r.Delete("/v1/account/sessions/{id}", d.RevokeSession)
	r.Post("/v1/account/sessions/revoke-all", d.RevokeAllSessions)
	r.Get("/v1/account/export", d.Export)
	r.Delete("/v1/account/me", d.RequestDeletion)
	r.Post("/v1/account/deletion/cancel", d.CancelDeletion)
}

// MaxDisplayName is the display-name cap (story 25.5: 80).
const MaxDisplayName = 80

// Locales the apps ship.
var locales = map[string]bool{"en": true, "ar": true}

type meView struct {
	ID            string            `json:"id"`
	Email         string            `json:"email"`
	EmailVerified bool              `json:"email_verified"`
	DisplayName   string            `json:"display_name,omitempty"`
	Locale        string            `json:"locale"`
	AvatarURL     string            `json:"avatar_url,omitempty"`
	Plan          string            `json:"plan"`
	Role          string            `json:"role"`
	CreatedAt     time.Time         `json:"created_at"`
	Identities    []stores.Identity `json:"identities"`
	HasPassword   bool              `json:"has_password"`
	Deletion      *stores.Deletion  `json:"deletion,omitempty"`
}

func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	v, err := d.me(r.Context(), middleware.GetUserID(r.Context()))
	if errors.Is(err, stores.ErrUserNotFound) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (d *Deps) me(ctx context.Context, uid string) (meView, error) {
	u, err := d.Users.ByID(ctx, uid)
	if err != nil {
		return meView{}, err
	}
	ids, err := d.Users.Identities(ctx, uid)
	if err != nil {
		return meView{}, err
	}
	del, err := d.Users.PendingDeletion(ctx, uid)
	if err != nil {
		return meView{}, err
	}
	return meView{
		ID: u.ID, Email: u.Email, EmailVerified: u.EmailVerified,
		DisplayName: u.DisplayName.String, Locale: u.Locale, AvatarURL: u.AvatarURL.String,
		Plan: u.Plan, Role: u.Role, CreatedAt: u.CreatedAt,
		Identities: ids, HasPassword: u.PasswordHash.Valid, Deletion: del,
	}, nil
}

type updateProfileReq struct {
	DisplayName *string `json:"display_name"`
	Locale      *string `json:"locale"`
	AvatarURL   *string `json:"avatar_url"`
}

func (d *Deps) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	var req updateProfileReq
	if !decode(w, r, &req) {
		return
	}
	var name, locale, avatar string
	if req.DisplayName != nil {
		name = strings.TrimSpace(*req.DisplayName)
		if len([]rune(name)) > MaxDisplayName {
			writeErr(w, http.StatusBadRequest, "display_name_too_long", "display name is limited to 80 characters")
			return
		}
	}
	if req.Locale != nil {
		locale = *req.Locale
		if !locales[locale] {
			writeErr(w, http.StatusBadRequest, "invalid_locale", "supported locales: en, ar")
			return
		}
	}
	if req.AvatarURL != nil && *req.AvatarURL != "" {
		avatar = *req.AvatarURL
		u, err := url.Parse(avatar)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(avatar) > 2048 {
			writeErr(w, http.StatusBadRequest, "invalid_avatar_url", "avatar must be an https URL")
			return
		}
	}
	uid := middleware.GetUserID(r.Context())
	if err := d.Users.UpdateProfile(r.Context(), uid, name, locale, avatar); err != nil {
		d.fail(w, r, "update profile", err)
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
// changing the password). On success every session is revoked — the
// calling client must sign in again. An account without a password
// (OAuth-only) adds one through "forgot password".
func (d *Deps) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordReq
	if !decode(w, r, &req) {
		return
	}
	if err := password.Validate(req.NewPassword); err != nil {
		writeErr(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	if !u.PasswordHash.Valid || argon2id.Verify(req.CurrentPassword, u.PasswordHash.String) != nil {
		writeErr(w, http.StatusUnauthorized, "wrong_password", "")
		return
	}
	newHash, err := argon2id.Hash(req.NewPassword, argon2id.DefaultParams())
	if err != nil {
		d.fail(w, r, "hash", err)
		return
	}
	if err := d.Users.SetPasswordHash(r.Context(), uid, newHash); err != nil {
		d.fail(w, r, "set password", err)
		return
	}
	if err := d.Sessions.RevokeAllForUser(r.Context(), uid); err != nil {
		d.Logger.Error("account: revoke sessions after password change", "err", err)
	}
	d.audit(r, audit.Entry{ActorID: uid, ActorEmail: u.Email, Action: audit.ActionPasswordChange, TargetKind: "user", TargetID: uid})
	w.WriteHeader(http.StatusNoContent)
}

type sessionView struct {
	ID        string     `json:"id"`
	UserAgent string     `json:"user_agent"`
	IP        string     `json:"ip,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	LastUsed  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt time.Time  `json:"expires_at"`
	Current   bool       `json:"current"`
}

// ListSessions lists the account's signed-in devices.
func (d *Deps) ListSessions(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	list, err := d.Sessions.ListActive(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "list sessions", err)
		return
	}
	cur := middleware.GetSessionID(r.Context())
	out := make([]sessionView, 0, len(list))
	for _, s := range list {
		out = append(out, sessionView{ID: s.ID, UserAgent: s.UserAgent, IP: s.IP, CreatedAt: s.CreatedAt,
			LastUsed: s.LastUsed, ExpiresAt: s.ExpiresAt, Current: s.ID == cur})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// RevokeSession signs one device out.
func (d *Deps) RevokeSession(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	ok, err := d.Sessions.RevokeForUser(r.Context(), chi.URLParam(r, "id"), uid)
	if err != nil {
		// A malformed id is a uuid cast error: treat as not found.
		if strings.Contains(err.Error(), "invalid input syntax") {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		d.fail(w, r, "revoke session", err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RevokeAllSessions is "sign out everywhere".
func (d *Deps) RevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if err := d.Sessions.RevokeAllForUser(r.Context(), uid); err != nil {
		d.fail(w, r, "revoke sessions", err)
		return
	}
	d.audit(r, audit.Entry{ActorID: uid, Action: audit.ActionSessionsRevoked, TargetKind: "user", TargetID: uid})
	w.WriteHeader(http.StatusNoContent)
}

// Export returns everything the cloud holds about the caller (GDPR
// Art. 15/20) as a JSON download. It is assembled synchronously: an
// account's cloud footprint is small (no media ever reaches the cloud).
func (d *Deps) Export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := middleware.GetUserID(ctx)
	me, err := d.me(ctx, uid)
	if err != nil {
		d.fail(w, r, "export account", err)
		return
	}
	sess, err := d.Sessions.ListActive(ctx, uid)
	if err != nil {
		d.fail(w, r, "export sessions", err)
		return
	}
	out := map[string]any{
		"exported_at": time.Now().UTC(),
		"account":     me,
		"sessions":    sess,
	}
	queries := map[string]string{
		"servers":      `SELECT id, name, slug, plan, version, created_at, last_seen_at FROM servers WHERE owner_user_id = $1`,
		"subscription": `SELECT plan, interval, status, current_period_end, cancel_at_period_end, updated_at FROM subscriptions WHERE user_id = $1`,
		"invoices":     `SELECT stripe_invoice_id, status, amount_due_cents, currency, updated_at FROM invoices WHERE user_id = $1`,
		"push_devices": `SELECT platform, app_version, created_at, last_seen_at FROM push_devices WHERE user_id = $1`,
		"bandwidth":    `SELECT b.server_id, b.month, b.bytes_in, b.bytes_out FROM bandwidth_monthly b JOIN servers s ON s.id = b.server_id WHERE s.owner_user_id = $1`,
		"audit":        `SELECT action, created_at, detail FROM audit_log WHERE target_kind = 'user' AND target_id = $1::text ORDER BY created_at`,
	}
	for k, q := range queries {
		rows, err := rowsAsMaps(ctx, d.Users, q, uid)
		if err != nil {
			d.fail(w, r, "export "+k, err)
			return
		}
		out[k] = rows
	}
	w.Header().Set("Content-Disposition", `attachment; filename="maktaba-cloud-export.json"`)
	writeJSON(w, http.StatusOK, out)
}

func rowsAsMaps(ctx context.Context, users *stores.Users, q string, arg any) ([]map[string]any, error) {
	rows, err := users.DB.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				var j any
				if json.Unmarshal(b, &j) == nil {
					m[c] = j
				} else {
					m[c] = string(b)
				}
				continue
			}
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type deleteReq struct {
	Password string `json:"password"`
}

// RequestDeletion starts the 30-day deletion hold and signs every
// device out. An account with a password must confirm it, so a
// borrowed unlocked session cannot delete the account.
func (d *Deps) RequestDeletion(w http.ResponseWriter, r *http.Request) {
	var req deleteReq
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	uid := middleware.GetUserID(r.Context())
	u, err := d.Users.ByID(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "load account", err)
		return
	}
	if u.PasswordHash.Valid && argon2id.Verify(req.Password, u.PasswordHash.String) != nil {
		writeErr(w, http.StatusUnauthorized, "wrong_password", "")
		return
	}
	if err := d.Users.RequestDeletion(r.Context(), uid); err != nil {
		d.fail(w, r, "request deletion", err)
		return
	}
	if err := d.Sessions.RevokeAllForUser(r.Context(), uid); err != nil {
		d.Logger.Error("account: revoke sessions on deletion", "err", err)
	}
	d.audit(r, audit.Entry{ActorID: uid, ActorEmail: u.Email, Action: audit.ActionAccountDelete, TargetKind: "user", TargetID: uid})
	w.WriteHeader(http.StatusNoContent)
}

// CancelDeletion lifts the hold.
func (d *Deps) CancelDeletion(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	ok, err := d.Users.CancelDeletion(r.Context(), uid)
	if err != nil {
		d.fail(w, r, "cancel deletion", err)
		return
	}
	if ok {
		d.audit(r, audit.Entry{ActorID: uid, Action: audit.ActionDeletionCancel, TargetKind: "user", TargetID: uid})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Deps) audit(r *http.Request, e audit.Entry) {
	if d.Audit == nil {
		return
	}
	e.IP = middleware.GetClientIP(r.Context())
	if err := d.Audit.Write(r.Context(), e); err != nil {
		d.Logger.Error("account: write audit", "action", e.Action, "err", err)
	}
}

func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	d.Logger.Error("account: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	writeErr(w, http.StatusInternalServerError, "internal_error", "")
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
