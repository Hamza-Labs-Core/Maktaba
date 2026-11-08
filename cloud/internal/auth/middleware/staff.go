package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Operator is the staff account a StaffGate admitted (Epic 50 story 50.33): handlers read it
// with ServiceAdmin to name the actor of what they audit.
type Operator struct {
	ID        string
	Email     string
	SessionID string
}

type operatorKey struct{}

// ServiceAdmin returns the staff account StaffGate admitted to an admin route.
func ServiceAdmin(ctx context.Context) (Operator, bool) {
	o, ok := ctx.Value(operatorKey{}).(Operator)
	return o, ok
}

// WithOperator is the context a handler test runs in, as if StaffGate had admitted o.
func WithOperator(ctx context.Context, o Operator) context.Context {
	return context.WithValue(ctx, operatorKey{}, o)
}

// StaffGate runs story 50.33 §3's checks on the admin listener, after the Host (50.32) and
// the IP allow-list (IPAllowList), in this order:
//  3. a staff access token whose session is alive (not revoked, idle or capped) and whose
//     account is active, else 401 no_session (a customer token fails here);
//  4. a second factor in the token's amr, else 403 mfa_required;
//  5. the route's role, from the database on every request, else 403 admin_access_denied;
//  6. for a staffauth.Sensitive route, an MFA check in the last 5 minutes, else 401
//     reauth_required;
//
// (7, CSRF, is the listener's AdminSameOrigin). Each refusal from 4 on is audited, and every
// request it admits is audited as admin.request with its status once it is answered.
type StaffGate struct {
	Staff  *stores.Staff
	Signer *staffauth.Signer
	Audit  *audit.Log
	Logger *slog.Logger
	Now    func() time.Time
}

func (g *StaffGate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func staffErr(w http.ResponseWriter, code int, kind, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if reason != "" {
		_, _ = w.Write([]byte(`{"error":"` + kind + `","reason":"` + reason + `"}`))
		return
	}
	_, _ = w.Write([]byte(`{"error":"` + kind + `"}`))
}

// Session is steps 3 and 4 alone: a signed-in staff account with its second factor, whatever
// its roles (its own /v1/staff/me and sessions). The session's last activity moves.
func (g *StaffGate) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, _, ok := g.admit(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(WithOperator(r.Context(), o)))
	})
}

// Require is every step for an admin route that needs role.
func (g *StaffGate) Require(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o, sess, ok := g.admit(w, r)
			if !ok {
				return
			}
			route := chi.RouteContext(r.Context()).RoutePattern()
			a, err := g.Staff.ByID(r.Context(), o.ID)
			if err != nil {
				g.Logger.Error("staff gate: account", "err", err)
				staffErr(w, http.StatusServiceUnavailable, "status_unavailable", "")
				return
			}
			if !a.HasRole(role) {
				g.audit(r, o, audit.ActionAdminDenied, map[string]any{"method": r.Method, "route": route, "role": role})
				staffErr(w, http.StatusForbidden, "admin_access_denied", "")
				return
			}
			if staffauth.IsSensitive(r.Method, route) && !staffauth.ReauthFresh(sess.AuthTime, g.now()) {
				g.audit(r, o, audit.ActionAdminDenied, map[string]any{"method": r.Method, "route": route, "reason": "reauth_required"})
				staffErr(w, http.StatusUnauthorized, "reauth_required", "")
				return
			}
			sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(sw, r.WithContext(WithOperator(r.Context(), o)))
			g.audit(r, o, audit.ActionAdminRequest, map[string]any{
				"method": r.Method, "route": route, "path": r.URL.Path, "status": sw.code,
			})
		})
	}
}

// admit is steps 3 and 4: the token, the session and its time rules, the account, the MFA.
func (g *StaffGate) admit(w http.ResponseWriter, r *http.Request) (Operator, stores.StaffSession, bool) {
	ctx := r.Context()
	now := g.now()
	raw, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || raw == "" {
		staffErr(w, http.StatusUnauthorized, "no_session", "")
		return Operator{}, stores.StaffSession{}, false
	}
	claims, err := g.Signer.Verify(raw, now)
	if err != nil {
		staffErr(w, http.StatusUnauthorized, "no_session", "")
		return Operator{}, stores.StaffSession{}, false
	}
	sess, err := g.Staff.Session(ctx, claims.SID)
	if errors.Is(err, stores.ErrStaffSessionEnd) || (err == nil && sess.StaffID != claims.Sub) {
		staffErr(w, http.StatusUnauthorized, "no_session", "")
		return Operator{}, stores.StaffSession{}, false
	}
	if err != nil {
		g.Logger.Error("staff gate: session", "err", err)
		staffErr(w, http.StatusServiceUnavailable, "status_unavailable", "")
		return Operator{}, stores.StaffSession{}, false
	}
	a, err := g.Staff.ByID(ctx, claims.Sub)
	if err != nil || !a.Active() {
		staffErr(w, http.StatusUnauthorized, "no_session", "")
		return Operator{}, stores.StaffSession{}, false
	}
	o := Operator{ID: a.ID, Email: a.Email, SessionID: sess.ID}
	if err := staffauth.CheckSession(sess.Times(), now); err != nil {
		reason, action := "idle", audit.ActionStaffIdleEnded
		if errors.Is(err, staffauth.ErrSessionCapped) {
			reason, action = "capped", audit.ActionStaffCapped
		}
		_ = g.Staff.End(ctx, sess.ID, reason, now)
		g.audit(r, o, action, nil)
		staffErr(w, http.StatusUnauthorized, "no_session", reason)
		return Operator{}, stores.StaffSession{}, false
	}
	if !claims.HasMFA() {
		g.audit(r, o, audit.ActionAdminDenied, map[string]any{"method": r.Method, "path": r.URL.Path, "reason": "mfa_required"})
		staffErr(w, http.StatusForbidden, "mfa_required", "")
		return Operator{}, stores.StaffSession{}, false
	}
	if err := g.Staff.Touch(ctx, sess.ID, now); err != nil {
		g.Logger.Error("staff gate: touch", "err", err)
	}
	return o, sess, true
}

func (g *StaffGate) audit(r *http.Request, o Operator, action string, detail map[string]any) {
	if g.Audit == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if ua := r.UserAgent(); ua != "" {
		detail["user_agent"] = ua
	}
	if err := g.Audit.Write(r.Context(), audit.Entry{
		ActorKind: audit.ActorStaff, ActorID: o.ID, ActorEmail: o.Email, Action: action,
		Detail: detail, IP: cmw.GetClientIP(r.Context()),
	}); err != nil {
		g.Logger.Error("staff gate: audit", "action", action, "err", err)
	}
}

type statusWriter struct {
	http.ResponseWriter
	code    int
	written bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.written {
		s.code, s.written = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

// IPAllowList is story 50.33 §7: when allow is set, a request on the admin listener from a
// client IP (50.31's trusted rule) outside every prefix is 403 ip_not_allowed and audited.
// Empty allow is off. The probes answer anyway (the node's own checks call loopback).
func IPAllowList(allow []netip.Prefix, log *audit.Log, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if len(allow) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			ip := cmw.GetClientIP(r.Context())
			if addr, err := netip.ParseAddr(ip); err == nil {
				addr = addr.Unmap()
				for _, p := range allow {
					if p.Contains(addr) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			if log != nil {
				if err := log.Write(r.Context(), audit.Entry{
					ActorKind: audit.ActorStaff, Action: audit.ActionStaffIPDenied,
					Detail: map[string]any{"method": r.Method, "path": r.URL.Path}, IP: ip,
				}); err != nil {
					logger.Error("ip allow-list: audit", "err", err)
				}
			}
			staffErr(w, http.StatusForbidden, "ip_not_allowed", "")
		})
	}
}
