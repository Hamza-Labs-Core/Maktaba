// Package auth implements Phase 9 (Epic 10) HTTP handlers for the
// login surface:
//
//	POST  /api/auth/login         — Story 10.2 (cookie) + Story 10.3 (JWT)
//	POST  /api/auth/refresh       — Story 10.4 (rotation + replay detect)
//	POST  /api/auth/logout        — Story 10.5 (web or native)
//	POST  /api/auth/logout-all    — Story 10.5 AC-3
//	GET   /api/security/audit     — Story 10.16 (admin-only)
//
// The handler picks the credential flow from the request:
//   - `X-Maktaba-Client: native` or a JSON body that includes no
//     `accept: cookie` ⇒ JWT + opaque refresh response, no cookies.
//   - Otherwise ⇒ web cookie response (`mkt_sess` + `mkt_csrf`).
//
// Login enforces a minimum 500 ms latency floor for the failure path
// (Story 10.2 AC-3) so an attacker can't time the user-not-found vs
// wrong-password branches.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Cookie names emitted by the web login flow.
const (
	CookieSession = "mkt_sess"
	CookieCSRF    = "mkt_csrf"
)

// Header that flips the login response into native mode.
const HeaderClient = "X-Maktaba-Client"

// FailDelay is the minimum wall-time on a failed login (Story 10.2
// AC-3). Both unknown-user and wrong-password branches sleep up to this
// to even out side-channels.
const FailDelay = 500 * time.Millisecond

// DefaultAccessTTL is the canonical access-token lifetime (15 min).
const DefaultAccessTTL = 15 * time.Minute

// Problem-type URIs surfaced by the auth handler. Aligned with the
// spec's `type:` strings so clients can branch on the URL fragment.
const (
	TypeInvalidCredentials = "https://maktaba.dev/problems/invalid-credentials"
	TypeTokenExpired       = "https://maktaba.dev/problems/token-expired"
	TypeRefreshExpired     = "https://maktaba.dev/problems/refresh-expired"
	TypeRefreshReplayed    = "https://maktaba.dev/problems/refresh-replayed"
	TypeRefreshInvalid     = "https://maktaba.dev/problems/refresh-invalid"
	TypeCSRFMismatch       = "https://maktaba.dev/problems/csrf-mismatch"
)

// LibraryACL is the read-side seam the token minter uses to snapshot
// the user's library grants into the JWT `lib[]` claim at issue time
// (Story 10.13 AC-5). *authz.ACLStore satisfies it; tests inject a
// fake so the mint path can be exercised without a database.
type LibraryACL interface {
	LibrariesFor(ctx context.Context, userID string) ([]string, error)
}

// Handler owns the auth surface. All dependencies are required except
// Audit (a nil writer disables audit emission, used in unit tests).
type Handler struct {
	Users         *users.Store
	Sessions      *sessions.Store
	RefreshTokens *refresh.Store
	Keys          *keys.Set
	Audit         *securityaudit.Writer
	AccessTTL     time.Duration

	// ACL snapshots the user's readable libraries into the access
	// token's `lib[]` claim at mint time. Streaming's claims guard
	// errors on an empty `lib` (streaming/internal/auth/claims.go), so
	// this must be populated for non-admin users to stream.
	ACL LibraryACL

	// FailedLogins drives the per-username brute-force counter
	// (Story 10.11). *users.Store satisfies it; a nil seam disables
	// the increment (unit tests that don't exercise lockout).
	FailedLogins FailedLogins

	// UserAdmin backs the admin user-management surface (Story 10.1
	// AC-3). *users.Store satisfies it.
	UserAdmin UserAdmin

	// SecureCookies controls whether the Set-Cookie response uses the
	// Secure attribute. Production should set this to true; tests and
	// localhost dev leave it false. Defaults to false; we can't infer
	// it from the request because the proxy strips HTTPS.
	SecureCookies bool

	// Now lets tests override wall time. Defaults to time.Now.UTC.
	Now func() time.Time
}

// Mount attaches the auth surface to r. Routes are anonymous (no
// auth middleware in front) so the login handler can issue
// credentials.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/auth/login", h.Login)
	r.Post("/api/auth/refresh", h.Refresh)
	r.Post("/api/auth/logout", h.Logout)
	r.Post("/api/auth/logout-all", h.LogoutAll)
	// /api/auth/me is NOT in the public allowlist, so the global
	// RequireAuthExcept gate already 401s anonymous callers; Me
	// re-checks defensively so it stays correct if mounted standalone.
	r.Get("/api/auth/me", h.Me)
	r.Get("/api/security/audit", h.SecurityAudit)
	r.Delete("/api/users/{id}/sessions/{session_id}", h.AdminRevokeSession)
	r.Delete("/api/users/{id}/refresh-tokens/{family_id}", h.AdminRevokeRefreshFamily)
	// Admin user-management surface (Story 10.1 AC-3). The store layer
	// was complete but had no HTTP caller (HLB-391); these are
	// admin-gated in-handler and behind the global RequireAuthExcept
	// gate (not in the public allowlist).
	r.Post("/api/users", h.AdminCreateUser)
	r.Patch("/api/users/{id}", h.AdminUpdateUser)
	r.Delete("/api/users/{id}", h.AdminDeleteUser)
	r.Post("/api/users/{id}/unlock", h.AdminUnlockUser)
}

// loginRequest is the JSON body shape for POST /api/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// nativeLoginResponse is the JSON shape sent back to native clients.
type nativeLoginResponse struct {
	AccessToken      string       `json:"access_token"`
	AccessExpiresIn  int          `json:"access_expires_in"`
	RefreshToken     string       `json:"refresh_token"`
	RefreshExpiresIn int          `json:"refresh_expires_in"`
	User             userResponse `json:"user"`
}

// webLoginResponse is the JSON shape sent back to cookie clients.
type webLoginResponse struct {
	User userResponse `json:"user"`
}

// userResponse is the public projection of a user row — never exposes
// the password hash or lockout state.
type userResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
}

// Login is the single endpoint shared between web and native flows.
// The `X-Maktaba-Client: native` header (Story 10.3 AC-1) flips the
// response shape; otherwise the response sets cookies.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	started := h.now()

	var req loginRequest
	if err := common.ReadJSON(r, &req, 4*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		httperror.Write(w, r, h.invalidCreds())
		return
	}

	u, err := h.Users.GetByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			h.logFailedLogin(r, req.Username, "")
			h.padFailDelay(started)
			httperror.Write(w, r, h.invalidCreds())
			return
		}
		httperror.Write(w, r, httperror.Internal(""))
		return
	}
	if u.IsLocked(h.now()) {
		h.logFailedLogin(r, req.Username, u.ID)
		h.audit(r.Context(), securityaudit.Entry{
			Event:       securityaudit.EventLockoutUsername,
			ActorUserID: u.ID,
			TargetID:    u.ID,
			Payload:     map[string]any{"username": req.Username, "ip": remoteIP(r)},
		})
		h.padFailDelay(started)
		// 423 (not the generic 401) so the client can surface
		// "locked, try later" — Story 10.11 AC-1. Timing parity is
		// preserved by padFailDelay above.
		h.writeLockedOut(w, r)
		return
	}
	if err := u.VerifyPassword(req.Password); err != nil {
		h.logFailedLogin(r, req.Username, u.ID)
		// Drive the per-username brute-force counter (HLB-398/388:
		// previously dead — the store method existed but nothing
		// called it, so lockout never engaged).
		h.recordFailedLogin(r.Context(), u.ID)
		h.padFailDelay(started)
		httperror.Write(w, r, h.invalidCreds())
		return
	}

	// Past auth — a successful credential check must zero the
	// brute-force counter and drop any (now irrelevant) lockout window.
	// Otherwise a user who once tripped the threshold stays pinned at
	// the cap and the next isolated typo lands at cap+1 >= threshold,
	// re-locking them for the full window with admin Unlock the only
	// recovery (HLB-398 correctness). Best-effort, like recordFailedLogin.
	h.resetFailedLogin(r.Context(), u.ID)

	// Past auth — emit login.success.
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventLoginSuccess,
		ActorUserID: u.ID,
		TargetID:    u.ID,
		Payload:     map[string]any{"ip": remoteIP(r), "ua": r.UserAgent()},
	})

	if isNativeClient(r) {
		h.respondNative(w, r, u)
		return
	}
	h.respondWeb(w, r, u)
}

// respondNative issues an access JWT + opaque refresh and returns the
// JSON shape from Story 10.3.
func (h *Handler) respondNative(w http.ResponseWriter, r *http.Request, u *users.User) {
	accessTTL := h.AccessTTL
	if accessTTL == 0 {
		accessTTL = DefaultAccessTTL
	}
	now := h.now()
	libs, err := h.librariesFor(r.Context(), u)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resolve library acl"))
		return
	}
	access, err := jwt.Sign(h.Keys, accessClaims(u, libs, now, accessTTL))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("sign access token"))
		return
	}
	rt, err := h.RefreshTokens.Issue(r.Context(), refresh.IssueInput{
		UserID:     u.ID,
		ClientMeta: map[string]any{"ip": remoteIP(r), "ua": r.UserAgent()},
	})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("issue refresh token"))
		return
	}
	resp := nativeLoginResponse{
		AccessToken:      access,
		AccessExpiresIn:  int(accessTTL.Seconds()),
		RefreshToken:     rt.Plaintext,
		RefreshExpiresIn: int(time.Until(rt.ExpiresAt).Seconds()),
		User:             toUserResponse(u),
	}
	common.WriteJSON(w, r, http.StatusOK, resp)
}

// respondWeb issues a web_sessions row and sets the SPA cookies.
func (h *Handler) respondWeb(w http.ResponseWriter, r *http.Request, u *users.User) {
	sess, err := h.Sessions.Create(r.Context(), sessions.CreateInput{
		UserID:    u.ID,
		IP:        remoteIP(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("create session"))
		return
	}
	maxAge := int(time.Until(sess.ExpiresAt).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     CookieSession,
		Value:    sess.ID,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CookieCSRF,
		Value:    sess.CSRFToken,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: false, // the SPA reads this
		Secure:   h.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	common.WriteJSON(w, r, http.StatusOK, webLoginResponse{User: toUserResponse(u)})
}

// refreshRequest is the JSON body for POST /api/auth/refresh.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh implements Story 10.4 AC-1 (rotate) and AC-2 (replay detect).
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := common.ReadJSON(r, &req, 4*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	if req.RefreshToken == "" {
		httperror.Write(w, r, h.refreshInvalid("missing refresh_token"))
		return
	}
	next, err := h.RefreshTokens.Rotate(r.Context(), req.RefreshToken,
		map[string]any{"ip": remoteIP(r), "ua": r.UserAgent()})
	if err != nil {
		switch {
		case errors.Is(err, refresh.ErrReplay):
			// The Rotate path already revoked the family; emit audit.
			fam := ""
			if next != nil {
				fam = next.FamilyID
			}
			h.audit(r.Context(), securityaudit.Entry{
				Event:    securityaudit.EventRefreshReplay,
				TargetID: fam,
				Payload:  map[string]any{"ip": remoteIP(r)},
			})
			httperror.Write(w, r, h.refreshReplayed())
			return
		case errors.Is(err, refresh.ErrExpired):
			httperror.Write(w, r, h.refreshExpired())
			return
		case errors.Is(err, refresh.ErrRevoked), errors.Is(err, refresh.ErrNotFound), errors.Is(err, refresh.ErrMalformed):
			httperror.Write(w, r, h.refreshInvalid("invalid refresh token"))
			return
		}
		httperror.Write(w, r, httperror.Internal("refresh"))
		return
	}

	u, err := h.Users.GetByID(r.Context(), next.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load user"))
		return
	}

	accessTTL := h.AccessTTL
	if accessTTL == 0 {
		accessTTL = DefaultAccessTTL
	}
	now := h.now()
	libs, err := h.librariesFor(r.Context(), u)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resolve library acl"))
		return
	}
	access, err := jwt.Sign(h.Keys, accessClaims(u, libs, now, accessTTL))
	if err != nil {
		httperror.Write(w, r, httperror.Internal("sign access token"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, nativeLoginResponse{
		AccessToken:      access,
		AccessExpiresIn:  int(accessTTL.Seconds()),
		RefreshToken:     next.Plaintext,
		RefreshExpiresIn: int(time.Until(next.ExpiresAt).Seconds()),
		User:             toUserResponse(u),
	})
}

// logoutRequest is the optional body for POST /api/auth/logout — native
// clients send `refresh_token`; web clients send nothing (cookie carries
// the session id).
type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Logout supports both surfaces (Story 10.5 AC-1, AC-2).
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	// Body is optional — ignore parse errors when empty.
	if r.ContentLength > 0 {
		_ = common.ReadJSON(r, &req, 4*1024)
	}

	if req.RefreshToken != "" {
		if err := h.RefreshTokens.RevokeByPlaintext(r.Context(), req.RefreshToken); err == nil {
			h.audit(r.Context(), securityaudit.Entry{
				Event:   securityaudit.EventRefreshRevoked,
				Payload: map[string]any{"reason": "logout"},
			})
		}
	}

	if c, err := r.Cookie(CookieSession); err == nil && c.Value != "" {
		if err := h.Sessions.Revoke(r.Context(), c.Value); err == nil {
			h.audit(r.Context(), securityaudit.Entry{
				Event:    securityaudit.EventLogout,
				TargetID: c.Value,
			})
		}
		clearCookie(w, CookieSession, h.SecureCookies)
		clearCookie(w, CookieCSRF, h.SecureCookies)
	}

	common.WriteNoContent(w)
}

// LogoutAll implements Story 10.5 AC-3 — revokes every web session
// AND every refresh family for the authenticated user.
func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	// Also accept a session cookie-only request: look up the session if
	// no principal is on context (the route is not gated by RequireAuth
	// upstream so this handler enforces).
	userID := ""
	if p != nil {
		userID = p.UserID
	} else if c, err := r.Cookie(CookieSession); err == nil && c.Value != "" {
		if sess, err := h.Sessions.Lookup(r.Context(), c.Value); err == nil {
			userID = sess.UserID
		}
	}
	if userID == "" {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unauthorized",
			Title:  "unauthorized",
			Status: http.StatusUnauthorized,
			Detail: "authentication required",
		})
		return
	}
	_, _ = h.Sessions.RevokeAllForUser(r.Context(), userID)
	_, _ = h.RefreshTokens.RevokeAllForUser(r.Context(), userID)

	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventLogoutAll,
		ActorUserID: userID,
		TargetID:    userID,
		Payload:     map[string]any{"ip": remoteIP(r)},
	})
	clearCookie(w, CookieSession, h.SecureCookies)
	clearCookie(w, CookieCSRF, h.SecureCookies)
	common.WriteNoContent(w)
}

// meResponse is the public projection of the request principal.
// libraries is `omitempty`-free so it serializes as [] not null,
// letting clients iterate without a nil guard.
type meResponse struct {
	UserID    string   `json:"user_id"`
	IsAdmin   bool     `json:"is_admin"`
	Libraries []string `json:"libraries"`
}

// Me returns the authenticated principal's projection. The global
// RequireAuthExcept gate already rejects anonymous callers (the route
// is not allowlisted); the nil-principal check here is defence in
// depth so the handler is correct even if mounted without the gate.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unauthorized",
			Title:  "unauthorized",
			Status: http.StatusUnauthorized,
			Detail: "authentication required",
		})
		return
	}
	libs := p.Libraries
	if libs == nil {
		libs = []string{}
	}
	common.WriteJSON(w, r, http.StatusOK, meResponse{
		UserID:    p.UserID,
		IsAdmin:   p.IsAdmin,
		Libraries: libs,
	})
}

// SecurityAudit implements Story 10.16 AC-3. Admin-only; non-admins
// get 403 (the middleware stack normally enforces this; we re-check
// here so the handler can be mounted without depending on chi
// middleware ordering).
func (h *Handler) SecurityAudit(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unauthorized",
			Title:  "unauthorized",
			Status: http.StatusUnauthorized,
		})
		return
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin only"))
		return
	}
	limit, ee := common.QueryInt(r, "limit", 50)
	if ee != nil {
		httperror.Write(w, r, ee)
		return
	}
	var cursor time.Time
	if v := r.URL.Query().Get("cursor"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			cursor = t
		}
	}
	rows, err := h.Audit.ListRecent(r.Context(), cursor, limit)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("audit list"))
		return
	}
	type envelope struct {
		Items []securityaudit.ListEntry `json:"items"`
	}
	common.WriteJSON(w, r, http.StatusOK, envelope{Items: rows})
}

// AdminRevokeSession implements the admin endpoint described in
// Story 10.5 AC-4: DELETE /api/users/{id}/sessions/{session_id}.
func (h *Handler) AdminRevokeSession(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		writeAdminGate(w, r, p)
		return
	}
	userID := chi.URLParam(r, "id")
	sessID := chi.URLParam(r, "session_id")
	if userID == "" || sessID == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	// Cheap sanity: the session must belong to the user. Without this
	// an admin could mistype the user_id and silently revoke a stranger's
	// session.
	sess, err := h.Sessions.Lookup(r.Context(), sessID)
	if err != nil {
		httperror.Write(w, r, httperror.NotFound("session"))
		return
	}
	if sess.UserID != userID {
		httperror.Write(w, r, httperror.NotFound("session for user"))
		return
	}
	if err := h.Sessions.Revoke(r.Context(), sessID); err != nil {
		httperror.Write(w, r, httperror.Internal("revoke"))
		return
	}
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventSessionRevoked,
		ActorUserID: p.UserID,
		TargetID:    userID,
		Payload:     map[string]any{"session_id": sessID},
	})
	common.WriteNoContent(w)
}

// AdminRevokeRefreshFamily implements DELETE
// /api/users/{id}/refresh-tokens/{family_id} (Story 10.5 AC-4).
func (h *Handler) AdminRevokeRefreshFamily(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		writeAdminGate(w, r, p)
		return
	}
	userID := chi.URLParam(r, "id")
	fam := chi.URLParam(r, "family_id")
	if userID == "" || fam == "" {
		httperror.Write(w, r, httperror.BadRequest("missing id"))
		return
	}
	if err := h.RefreshTokens.RevokeFamily(r.Context(), fam); err != nil {
		httperror.Write(w, r, httperror.Internal("revoke"))
		return
	}
	h.audit(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventRefreshRevoked,
		ActorUserID: p.UserID,
		TargetID:    userID,
		Payload:     map[string]any{"family_id": fam, "reason": "admin"},
	})
	common.WriteNoContent(w)
}

// CookieAuth returns a middleware that, for any request carrying a
// valid `mkt_sess` cookie AND no upstream principal, loads the
// session, bumps last_seen_at, and attaches the cookie-source
// principal. CSRF guard is layered separately for mutating verbs
// (Story 10.10).
func (h *Handler) CookieAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal.FromContext(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(CookieSession)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := h.Sessions.Lookup(r.Context(), c.Value)
		if err != nil {
			// Clear an invalid cookie so the browser stops sending it.
			clearCookie(w, CookieSession, h.SecureCookies)
			clearCookie(w, CookieCSRF, h.SecureCookies)
			next.ServeHTTP(w, r)
			return
		}
		u, err := h.Users.GetByID(r.Context(), sess.UserID)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		_ = h.Sessions.TouchLastSeen(r.Context(), sess.ID, h.now())

		p := &principal.Principal{
			UserID:             u.ID,
			IsAdmin:            u.IsAdmin,
			AccessAllLibraries: u.IsAdmin,
			Source:             principal.SourceCookie,
		}
		next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
	})
}

// ---------- internal helpers ----------

// librariesFor snapshots the libraries this user can read for the JWT
// `lib[]` claim. Admins read everything, so we skip the ACL lookup and
// return an empty slice (the verifier sets AccessAllLibraries from
// is_admin). A nil ACL (older callers / unit tests that don't exercise
// the mint path) yields an empty slice rather than a panic.
func (h *Handler) librariesFor(ctx context.Context, u *users.User) ([]string, error) {
	if u.IsAdmin || h.ACL == nil {
		return []string{}, nil
	}
	libs, err := h.ACL.LibrariesFor(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if libs == nil {
		libs = []string{}
	}
	return libs, nil
}

// accessClaims builds the access-token claim set. Kept as a pure
// function so the lib[] snapshot can be unit-tested without standing
// up the DB-backed Login/Refresh path.
func accessClaims(u *users.User, libs []string, now time.Time, ttl time.Duration) jwt.Claims {
	return jwt.Claims{
		Iss:     "maktaba",
		Aud:     "api",
		Sub:     u.ID,
		Iat:     now.Unix(),
		Exp:     now.Add(ttl).Unix(),
		Usr:     u.ID,
		Lib:     libs,
		IsAdmin: u.IsAdmin,
	}
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().UTC()
}

func (h *Handler) padFailDelay(started time.Time) {
	elapsed := h.now().Sub(started)
	if elapsed < FailDelay {
		time.Sleep(FailDelay - elapsed)
	}
}

func (h *Handler) audit(ctx context.Context, e securityaudit.Entry) {
	if h.Audit == nil {
		return
	}
	_ = h.Audit.Write(ctx, e)
}

func (h *Handler) logFailedLogin(r *http.Request, username, userID string) {
	if h.Audit == nil {
		return
	}
	_ = h.Audit.Write(r.Context(), securityaudit.Entry{
		Event:       securityaudit.EventLoginFailed,
		ActorUserID: userID,
		Payload: map[string]any{
			"username": username,
			"ip":       remoteIP(r),
			"ua":       r.UserAgent(),
		},
	})
}

func (h *Handler) invalidCreds() *httperror.Error {
	return &httperror.Error{
		Type:   TypeInvalidCredentials,
		Title:  "invalid credentials",
		Status: http.StatusUnauthorized,
		Detail: "username or password is incorrect",
	}
}

func (h *Handler) refreshInvalid(detail string) *httperror.Error {
	return &httperror.Error{
		Type:   TypeRefreshInvalid,
		Title:  "invalid refresh token",
		Status: http.StatusUnauthorized,
		Detail: detail,
	}
}

func (h *Handler) refreshExpired() *httperror.Error {
	return &httperror.Error{
		Type:   TypeRefreshExpired,
		Title:  "refresh token expired",
		Status: http.StatusUnauthorized,
	}
}

func (h *Handler) refreshReplayed() *httperror.Error {
	return &httperror.Error{
		Type:   TypeRefreshReplayed,
		Title:  "refresh token replayed",
		Status: http.StatusUnauthorized,
		Detail: "the refresh family has been revoked; please log in again",
	}
}

func writeAdminGate(w http.ResponseWriter, r *http.Request, p *principal.Principal) {
	if p == nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unauthorized",
			Title:  "unauthorized",
			Status: http.StatusUnauthorized,
		})
		return
	}
	httperror.Write(w, r, httperror.Forbidden("", "admin only"))
}

func toUserResponse(u *users.User) userResponse {
	return userResponse{
		ID:       u.ID,
		Username: u.Username,
		IsAdmin:  u.IsAdmin,
	}
}

// isNativeClient reports whether the request prefers the JWT response.
func isNativeClient(r *http.Request) bool {
	if v := strings.ToLower(r.Header.Get(HeaderClient)); v == "native" {
		return true
	}
	// Fallback: presence of `Authorization` header is enough to assume
	// the client is doing bearer-flow.
	if r.Header.Get("Authorization") != "" {
		return true
	}
	return false
}

func remoteIP(r *http.Request) string {
	// The router runs chi's RealIP middleware, so RemoteAddr already
	// reflects the client-side address. Strip the port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: name == CookieSession,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Deps bundles the wiring needed by the router so callers don't have
// to construct stores directly.
type Deps struct {
	DB            *sql.DB
	Keys          *keys.Set
	SecureCookies bool
	AccessTTL     time.Duration
}

// NewHandler builds the canonical handler from Deps. Used by the
// router's MountP9 (api/internal/router/p9.go).
func NewHandler(d Deps) *Handler {
	us := users.New(d.DB)
	return &Handler{
		Users:         us,
		Sessions:      sessions.New(d.DB),
		RefreshTokens: refresh.New(d.DB),
		Keys:          d.Keys,
		Audit:         securityaudit.NewWriter(d.DB),
		AccessTTL:     d.AccessTTL,
		SecureCookies: d.SecureCookies,
		ACL:           &authz.ACLStore{DB: d.DB},
		// Same store instance backs the brute-force counter so the
		// (previously dead) IncrementFailedAttempt is driven live.
		FailedLogins: us,
		// ...and the admin user-management surface (Story 10.1 AC-3).
		UserAdmin: us,
	}
}

// envIntSec is a tiny helper to expose `MAKTABA_AUTH_ACCESS_TTL_SEC` as
// a duration. Exported so main.go can call it without re-implementing
// the env-int dance.
func EnvAccessTTL(getenv func(string) string) time.Duration {
	v := getenv("MAKTABA_AUTH_ACCESS_TTL_SEC")
	if v == "" {
		return DefaultAccessTTL
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return DefaultAccessTTL
	}
	return time.Duration(n) * time.Second
}
