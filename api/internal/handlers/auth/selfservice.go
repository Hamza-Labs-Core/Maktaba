// Self-service auth surface (web-pages-batch2): registration, the
// forgot/reset-password flow, the signed-in user's own session list,
// and account profile / password changes.
//
// Routes (wired in auth.go Mount):
//
//	POST  /api/auth/register         (public)  — open-registration or first-user bootstrap
//	POST  /api/auth/forgot-password  (public)  — always 200 (no email enumeration)
//	POST  /api/auth/reset-password   (public)  — exchange a reset token for a new password
//	GET   /api/me/sessions           (gated)   — the caller's active web sessions
//	DELETE /api/me/sessions/{id}     (gated)   — revoke one of the caller's sessions
//	PATCH /api/me                    (gated)   — update display name / email
//	POST  /api/me/change-password    (gated)   — change password (verifies current)
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/passwordreset"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// minPasswordLen is the floor for self-service passwords. Argon2id
// already caps the upper bound; this guards the trivially-weak low end.
const minPasswordLen = 8

// openRegistrationKey is the app_settings key gating self-service signup.
const openRegistrationKey = "auth.open_registration"

// ---------- Register ----------

type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register implements POST /api/auth/register. A new account is created
// when self-service registration is open OR the users table is empty
// (first-user bootstrap — that account is made admin). On success it
// issues credentials exactly like Login (cookie for web, JWT for native).
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if req.Username == "" || req.Email == "" || req.Password == "" {
		httperror.Write(w, r, httperror.BadRequest("username, email and password are required"))
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		httperror.Write(w, r, httperror.BadRequest("email is not valid"))
		return
	}
	if len(req.Password) < minPasswordLen {
		httperror.Write(w, r, httperror.BadRequest("password must be at least 8 characters"))
		return
	}

	// Gate: first-user bootstrap always allowed; otherwise the
	// open-registration setting must be on.
	hasUser, err := h.Users.HasAnyUser(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("registration check"))
		return
	}
	bootstrap := !hasUser
	if !bootstrap && !h.openRegistration(r) {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/registration-closed",
			Title:  "registration closed",
			Status: http.StatusForbidden,
			Detail: "self-service registration is disabled",
		})
		return
	}

	// Pre-check email so a collision returns a precise 409 rather than
	// the ambiguous username/email unique violation from Create.
	if _, err := h.Users.IDByEmail(r.Context(), req.Email); err == nil {
		httperror.Write(w, r, httperror.Conflict(
			"https://maktaba.dev/problems/email-exists", "email already registered"))
		return
	} else if !errors.Is(err, users.ErrNotFound) {
		httperror.Write(w, r, httperror.Internal("registration check"))
		return
	}

	u, err := h.Users.Create(r.Context(), users.CreateInput{
		Username: req.Username,
		Password: req.Password,
		Email:    req.Email,
		IsAdmin:  bootstrap, // first user is the admin
	})
	if err != nil {
		switch {
		case errors.Is(err, users.ErrUsernameExists):
			httperror.Write(w, r, httperror.Conflict(
				"https://maktaba.dev/problems/username-exists", "username already taken"))
		case errors.Is(err, users.ErrEmailExists):
			httperror.Write(w, r, httperror.Conflict(
				"https://maktaba.dev/problems/email-exists", "email already registered"))
		default:
			httperror.Write(w, r, httperror.Internal("create user"))
		}
		return
	}

	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.Event("user.registered"),
		ActorUserID: u.ID,
		TargetID:    u.ID,
		Payload:     map[string]any{"ip": remoteIP(r), "bootstrap": bootstrap},
	})

	// Issue credentials just like Login — the new user is signed in.
	if isNativeClient(r) {
		h.respondNative(w, r, u)
		return
	}
	h.respondWeb(w, r, u)
}

// openRegistration reads the auth.open_registration setting from
// app_settings. Any read failure / missing row ⇒ false (closed), the
// safe default for an exposed server.
func (h *Handler) openRegistration(r *http.Request) bool {
	if h.DB == nil {
		return false
	}
	var raw []byte
	err := h.DB.QueryRowContext(r.Context(),
		`SELECT value FROM app_settings WHERE key = $1`, openRegistrationKey).Scan(&raw)
	if err != nil {
		return false
	}
	// value is a JSON scalar ("true"/"false") on Postgres or the literal
	// text on SQLite; trim quotes/space and compare.
	v := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return strings.EqualFold(v, "true")
}

// ---------- Forgot / Reset password ----------

type forgotRequest struct {
	Email string `json:"email"`
}

// ForgotPassword implements POST /api/auth/forgot-password. It ALWAYS
// responds 200 regardless of whether the email exists, so an attacker
// can't enumerate accounts. When the email maps to a user a single-use
// reset token is minted; delivery (email) is an operator concern, so we
// log the token server-side for now.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	// Only act when we have a deliverable address AND a backing store.
	if req.Email != "" && h.PasswordReset != nil {
		if userID, err := h.Users.IDByEmail(r.Context(), req.Email); err == nil {
			if token, terr := h.PasswordReset.Create(r.Context(), userID); terr == nil {
				// Delivery is out of scope; surface the token to the
				// operator log so a self-hoster can complete the flow.
				slog.Default().Info("password reset requested",
					"event", "password_reset_requested",
					"user_id", userID,
					"reset_token", token)
			}
		}
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ResetPassword implements POST /api/auth/reset-password. It consumes a
// reset token, sets the new password, and revokes the user's existing
// sessions + refresh families so a compromised credential can't ride
// the reset.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.PasswordReset == nil {
		httperror.Write(w, r, httperror.Unavailable(0))
		return
	}
	var req resetRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Token == "" {
		httperror.Write(w, r, httperror.BadRequest("token is required"))
		return
	}
	if len(req.Password) < minPasswordLen {
		httperror.Write(w, r, httperror.BadRequest("password must be at least 8 characters"))
		return
	}
	userID, err := h.PasswordReset.Consume(r.Context(), req.Token)
	if err != nil {
		if errors.Is(err, passwordreset.ErrInvalid) {
			httperror.Write(w, r, httperror.BadRequest("reset token is invalid or expired"))
			return
		}
		httperror.Write(w, r, httperror.Internal("reset password"))
		return
	}
	if _, err := h.Users.Update(r.Context(), userID, users.UpdateInput{Password: &req.Password}); err != nil {
		httperror.Write(w, r, httperror.Internal("update password"))
		return
	}
	// Invalidate everything issued before the reset.
	_, _ = h.Sessions.RevokeAllForUser(r.Context(), userID)
	_, _ = h.RefreshTokens.RevokeAllForUser(r.Context(), userID)
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventPasswordChanged,
		ActorUserID: userID,
		TargetID:    userID,
		Payload:     map[string]any{"ip": remoteIP(r), "via": "reset"},
	})
	common.WriteNoContent(w)
}

// ---------- Self-service sessions ----------

type sessionView struct {
	ID         string  `json:"id"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt string  `json:"last_seen_at"`
	ExpiresAt  string  `json:"expires_at"`
	IP         *string `json:"ip"`
	UserAgent  *string `json:"user_agent"`
	Current    bool    `json:"current"`
}

// MeSessions implements GET /api/me/sessions — the caller's own active
// web sessions, with the request's session flagged `current`.
func (h *Handler) MeSessions(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	rows, err := h.Sessions.ListActive(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("list sessions"))
		return
	}
	currentID := ""
	if c, cerr := r.Cookie(CookieSession); cerr == nil {
		currentID = c.Value
	}
	items := make([]sessionView, 0, len(rows))
	for _, s := range rows {
		items = append(items, sessionView{
			ID:         s.ID,
			CreatedAt:  s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			LastSeenAt: s.LastSeenAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			ExpiresAt:  s.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
			IP:         s.IP,
			UserAgent:  s.UserAgent,
			Current:    s.ID == currentID,
		})
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// RevokeMeSession implements DELETE /api/me/sessions/{id}. Owner-scoped:
// the session must belong to the caller or it's a 404.
func (h *Handler) RevokeMeSession(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	sess, err := h.Sessions.Lookup(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("session"))
		return
	}
	if sess.UserID != p.UserID {
		httperror.Write(w, r, httperror.NotFound("session"))
		return
	}
	if err := h.Sessions.Revoke(r.Context(), id); err != nil {
		httperror.Write(w, r, httperror.Internal("revoke session"))
		return
	}
	common.WriteNoContent(w)
}

// ---------- Account profile / password ----------

type updateMeRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
}

type profileResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
}

// GetMe implements GET /api/me — the caller's editable profile
// (username, email, display name). Distinct from /api/auth/me, which
// returns the authorization principal (libraries, is_admin).
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	h.writeProfile(w, r, p.UserID)
}

// UpdateMe implements PATCH /api/me — edit display name and/or email.
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	var req updateMeRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Email != nil {
		e := strings.TrimSpace(*req.Email)
		if e != "" {
			if _, err := mail.ParseAddress(e); err != nil {
				httperror.Write(w, r, httperror.BadRequest("email is not valid"))
				return
			}
		}
		req.Email = &e
	}
	if req.DisplayName != nil {
		dn := strings.TrimSpace(*req.DisplayName)
		req.DisplayName = &dn
	}
	err := h.Users.UpdateProfile(r.Context(), p.UserID, users.ProfileUpdate{
		Email:       req.Email,
		DisplayName: req.DisplayName,
	})
	if err != nil {
		switch {
		case errors.Is(err, users.ErrEmailExists):
			httperror.Write(w, r, httperror.Conflict(
				"https://maktaba.dev/problems/email-exists", "email already registered"))
		case errors.Is(err, users.ErrNotFound):
			httperror.Write(w, r, httperror.NotFound("user"))
		default:
			httperror.Write(w, r, httperror.Internal("update profile"))
		}
		return
	}
	h.writeProfile(w, r, p.UserID)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword implements POST /api/me/change-password. Verifies the
// current password before applying the new one.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		writeUnauthorized(w, r)
		return
	}
	var req changePasswordRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		httperror.Write(w, r, httperror.BadRequest("current_password and new_password are required"))
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		httperror.Write(w, r, httperror.BadRequest("new password must be at least 8 characters"))
		return
	}
	u, err := h.Users.GetByID(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load user"))
		return
	}
	if verr := u.VerifyPassword(req.CurrentPassword); verr != nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/invalid-credentials",
			Title:  "invalid credentials",
			Status: http.StatusUnauthorized,
			Detail: "current password is incorrect",
		})
		return
	}
	if _, err := h.Users.Update(r.Context(), p.UserID, users.UpdateInput{Password: &req.NewPassword}); err != nil {
		httperror.Write(w, r, httperror.Internal("update password"))
		return
	}
	common.WriteNoContent(w)
}

// writeProfile fetches and writes the caller's profile projection.
func (h *Handler) writeProfile(w http.ResponseWriter, r *http.Request, userID string) {
	pr, err := h.Users.GetProfile(r.Context(), userID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load profile"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, profileResponse{
		ID:          pr.ID,
		Username:    pr.Username,
		Email:       pr.Email,
		DisplayName: pr.DisplayName,
		IsAdmin:     pr.IsAdmin,
	})
}

func writeUnauthorized(w http.ResponseWriter, r *http.Request) {
	httperror.Write(w, r, &httperror.Error{
		Type:   "https://maktaba.dev/problems/unauthorized",
		Title:  "unauthorized",
		Status: http.StatusUnauthorized,
		Detail: "authentication required",
	})
}
