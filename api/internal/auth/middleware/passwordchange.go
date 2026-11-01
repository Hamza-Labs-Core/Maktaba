package middleware

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// After an admin reset a user's password to a temporary one
// (users.must_change_password, slot 0138), the session may only choose a new
// password, see who it is, or sign out. The check lives here, in the auth gate
// (RequireAuthExcept) that the home and every full node run, so the two cannot
// drift: the home's cookie middleware and the node's home-resolved principal both
// carry Principal.MustChangePassword, and the gate refuses the rest.

// TypePasswordChangeRequired is the problem type a must-change session gets.
const TypePasswordChangeRequired = "https://maktaba.tv/problems/password-change-required"

// AllowedWhilePasswordChange lists what a session with a temporary password may
// still reach: anything outside /api (the web app itself), the auth endpoints (who
// am I, sign out), the public system and key endpoints, its own profile read, and
// the change-password form.
func AllowedWhilePasswordChange(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case !strings.HasPrefix(p, "/api/"):
		return true
	case strings.HasPrefix(p, "/api/auth/"),
		strings.HasPrefix(p, "/api/system/"),
		strings.HasPrefix(p, "/api/.well-known/"):
		return true
	case p == "/api/me/change-password" && r.Method == http.MethodPost:
		return true
	case p == "/api/me" && r.Method == http.MethodGet:
		return true
	}
	return false
}

// PasswordChangeBlocked reports whether the request's principal must change its
// password first and the request is not one it may still make.
func PasswordChangeBlocked(r *http.Request) bool {
	p := principal.FromContext(r.Context())
	return p != nil && p.MustChangePassword && !AllowedWhilePasswordChange(r)
}

// WritePasswordChangeRequired writes the 403 a must-change session gets.
func WritePasswordChangeRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   TypePasswordChangeRequired,
		"title":  "password change required",
		"status": http.StatusForbidden,
		"detail": "choose a new password in the web app before continuing",
	})
}
