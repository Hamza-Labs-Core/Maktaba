// Package middleware (auth) verifies the bearer token on every
// authenticated route and attaches the user id to the request context,
// and gates the service-admin surface on the cloud role.
//
// We keep this separate from the generic HTTP middleware package so
// that the relay router can opt in/out per route — e.g. /v1/auth/login
// must not require auth, but /v1/account/me must.
package middleware

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// StatusFunc reports whether the bearer of an access token may still
// act: the account's status (stores.Status*), or StatusSignedOut when the
// session the token came from was revoked, or StatusBlocked when the
// account is blocklisted; stores.ErrUserNotFound for a deleted account.
// RequireUser consults it on every request so a suspension, block or
// sign-out takes effect at once rather than when the 15-minute token
// runs out.
type StatusFunc func(ctx context.Context, userID, sessionID string) (string, error)

// Extra outcomes of StatusFunc beyond the account statuses.
const (
	StatusSignedOut = "signed_out"
	StatusBlocked   = "blocked"
)

// UserStatus is the production StatusFunc: one query. A rotated session
// is still alive — its access token predates the rotation.
func UserStatus(db *sql.DB) StatusFunc {
	return func(ctx context.Context, id, sid string) (string, error) {
		var status string
		var blocked, live bool
		err := db.QueryRowContext(ctx, `
            SELECT u.status,
                   EXISTS (SELECT 1 FROM blocklist b WHERE b.subject_kind = 'user' AND b.subject = u.id::text
                           AND (b.expires_at IS NULL OR b.expires_at > now())),
                   ($2 = '' OR EXISTS (SELECT 1 FROM sessions s WHERE s.id::text = $2 AND s.user_id = u.id
                           AND (s.revoked_at IS NULL OR s.revoke_reason = 'rotated')))
            FROM users u WHERE u.id = $1`, id, sid).Scan(&status, &blocked, &live)
		if errors.Is(err, sql.ErrNoRows) {
			return "", stores.ErrUserNotFound
		}
		switch {
		case err != nil:
			return "", err
		case status == stores.StatusSuspended:
			// Suspension revokes the sessions too; say why.
			return status, nil
		case !live:
			return StatusSignedOut, nil
		case blocked:
			return StatusBlocked, nil
		}
		return status, nil
	}
}

// RequireUser returns a middleware that 401s when no valid access
// token is present, 403s a suspended account, and stashes the user and
// session ids on the request context otherwise. status may be nil (the
// token alone is then trusted until it expires).
func RequireUser(signer *token.Signer, status StatusFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if !strings.HasPrefix(h, "Bearer ") {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			claims, err := signer.Verify(strings.TrimPrefix(h, "Bearer "), time.Now())
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if status != nil {
				s, err := status(r.Context(), claims.Sub, claims.SID)
				switch {
				case errors.Is(err, stores.ErrUserNotFound), err == nil && s == StatusSignedOut:
					writeErr(w, http.StatusUnauthorized, "unauthorized")
					return
				case err != nil:
					writeErr(w, http.StatusServiceUnavailable, "status_unavailable")
					return
				case s == stores.StatusSuspended:
					writeErr(w, http.StatusForbidden, "account_suspended")
					return
				case s == StatusBlocked:
					writeErr(w, http.StatusForbidden, "blocked")
					return
				}
			}
			ctx := cmw.WithUserID(r.Context(), claims.Sub)
			ctx = cmw.WithSessionID(ctx, claims.SID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type adminKey struct{}

// ServiceAdmin returns the admin account RequireServiceAdmin admitted.
func ServiceAdmin(ctx context.Context) (stores.User, bool) {
	u, ok := ctx.Value(adminKey{}).(stores.User)
	return u, ok
}

// RequireServiceAdmin admits only accounts holding the cloud
// service-admin role. It must run inside RequireUser. The role is read
// from the database on every request — never from the token, an email
// domain, or anything a home server says — so granting and revoking
// take effect at once. A refusal is audited.
func RequireServiceAdmin(users *stores.Users, log *audit.Log, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := cmw.GetUserID(r.Context())
			if uid == "" {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			u, err := users.ByID(r.Context(), uid)
			if errors.Is(err, stores.ErrUserNotFound) {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if err != nil {
				logger.Error("admin gate: lookup", "err", err)
				writeErr(w, http.StatusServiceUnavailable, "status_unavailable")
				return
			}
			if !u.IsServiceAdmin() {
				if log != nil {
					if err := log.Write(r.Context(), audit.Entry{
						ActorID: u.ID, ActorEmail: u.Email, Action: audit.ActionAdminDenied,
						Detail: map[string]any{"method": r.Method, "path": r.URL.Path},
						IP:     cmw.GetClientIP(r.Context()),
					}); err != nil {
						logger.Error("admin gate: audit denied attempt", "err", err)
					}
				}
				writeErr(w, http.StatusForbidden, "admin_access_denied")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminKey{}, u)))
		})
	}
}

func writeErr(w http.ResponseWriter, code int, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + kind + `"}`))
}
