// Package auth wires the cloud's HTTP authentication surface:
// registration with email verification, login, refresh, logout,
// password reset, and the OAuth start/callback endpoints. The contract
// is docs/accounts-billing-admin.md.
//
// Each handler is intentionally small — the heavy lifting lives in
// stores.Users (DB), argon2id (hashing), password (policy), sessions
// (refresh tokens), token (access tokens), stores.MailTokens (mailed
// links) and abuse (lockout, replay). Handlers only orchestrate these
// layers, set HTTP cookies, and shape JSON responses.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/password"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	mailpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps bundles the collaborators each handler needs.
type Deps struct {
	Users     *stores.Users
	Sessions  *sessions.Store
	Signer    *token.Signer
	Tokens    *stores.MailTokens
	Mailer    mailpkg.Mailer
	Templates mailpkg.Templates
	Abuse     *abuse.Detector
	Audit     *audit.Log
	Logger    *slog.Logger
	Google    *oauth.GoogleFlow
	Apple     *oauth.AppleFlow
	// WebURL is the cloud web app's origin; OAuth callbacks land there.
	WebURL       string
	CookieDomain string
	Secure       bool
}

// Router is the part of chi.Router Mount needs.
type Router interface {
	Post(pattern string, h http.HandlerFunc)
	Get(pattern string, h http.HandlerFunc)
}

// Mount registers all auth routes.
func Mount(r Router, d *Deps) {
	r.Post("/v1/auth/register", d.Register)
	r.Post("/v1/auth/verify-email", d.VerifyEmail)
	r.Post("/v1/auth/verify-email/resend", d.ResendVerification)
	r.Post("/v1/auth/login", d.Login)
	r.Post("/v1/auth/refresh", d.Refresh)
	r.Post("/v1/auth/logout", d.Logout)
	r.Post("/v1/auth/password/forgot", d.ForgotPassword)
	r.Post("/v1/auth/password/reset", d.ResetPassword)
	r.Get("/v1/auth/providers", d.Providers)
	r.Get("/v1/auth/oauth/google", d.GoogleStart)
	r.Get("/v1/auth/oauth/google/callback", d.GoogleCallback)
	r.Get("/v1/auth/oauth/apple", d.AppleStart)
	r.Post("/v1/auth/oauth/apple/callback", d.AppleCallback)
}

// RateLimitedPaths are the unauthenticated routes the per-IP limiter
// covers; MailPaths also send mail, so they get the tighter limiter.
var (
	RateLimitedPaths = []string{
		"/v1/auth/register", "/v1/auth/login", "/v1/auth/refresh",
		"/v1/auth/verify-email", "/v1/auth/verify-email/resend",
		"/v1/auth/password/forgot", "/v1/auth/password/reset",
		"/v1/auth/oauth/google", "/v1/auth/oauth/google/callback",
		"/v1/auth/oauth/apple", "/v1/auth/oauth/apple/callback",
	}
	MailPaths = []string{
		"/v1/auth/register", "/v1/auth/verify-email/resend", "/v1/auth/password/forgot",
	}
)

// maxBody bounds every auth request body.
const maxBody = 64 << 10

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return false
	}
	return true
}

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type authResp struct {
	AccessToken string   `json:"access_token"`
	ExpiresIn   int      `json:"expires_in"`
	User        userView `json:"user"`
}

type userView struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	DisplayName   string `json:"display_name,omitempty"`
	Plan          string `json:"plan"`
	Role          string `json:"role"`
}

// normalizeEmail returns the lower-cased address, or "" when s is not a
// bare, plausible email address.
func normalizeEmail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 254 {
		return ""
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return ""
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || !strings.Contains(s[at+1:], ".") || strings.HasSuffix(s, ".") {
		return ""
	}
	return strings.ToLower(s)
}

// Register creates an unverified account and mails a verification link.
// It answers 202 whether or not the address already has an account (the
// owner gets a notice instead), so the endpoint cannot be used to find
// out who has signed up.
func (d *Deps) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decode(w, r, &req) {
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "invalid_email", "enter a valid email address")
		return
	}
	if err := password.Validate(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	ip := middleware.GetClientIP(r.Context())
	if d.signupFlooded(r.Context(), ip) {
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, "too_many_signups", "too many sign-ups from this network; try again later")
		return
	}
	name := strings.TrimSpace(req.DisplayName)
	if len([]rune(name)) > maxDisplayName {
		writeError(w, http.StatusBadRequest, "display_name_too_long", "display name is limited to 80 characters")
		return
	}
	hash, err := argon2id.Hash(req.Password, argon2id.DefaultParams())
	if err != nil {
		d.fail(w, r, "hash", err)
		return
	}
	u, err := d.Users.Create(r.Context(), email, hash, name, false)
	if err != nil {
		if !isUniqueViolation(err) {
			d.fail(w, r, "create user", err)
			return
		}
		d.send(r.Context(), d.Templates.AlreadyRegistered(email))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending_verification"})
		return
	}
	d.sendVerification(r.Context(), u)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending_verification"})
}

// maxDisplayName is the profile name cap (story 25.5: 80).
const maxDisplayName = 80

// signupFlooded records a sign-up from ip and reports whether the ip
// has crossed the abuse thresholds (bot sign-ups, story 25.25).
func (d *Deps) signupFlooded(ctx context.Context, ip string) bool {
	if d.Abuse == nil || ip == "" {
		return false
	}
	subj := abuse.Subject{Kind: "ip", ID: ip}
	if err := d.Abuse.Record(ctx, subj, abuse.KindSignup, 1, nil); err != nil {
		d.Logger.Warn("auth: record signup signal", "err", err)
		return false
	}
	block, _, err := d.Abuse.ShouldBlock(ctx, subj)
	if err != nil {
		d.Logger.Warn("auth: signup abuse check", "err", err)
		return false
	}
	return block
}

func (d *Deps) sendVerification(ctx context.Context, u stores.User) {
	tok, err := d.Tokens.Issue(ctx, u.ID, stores.PurposeVerifyEmail, stores.VerifyEmailTTL)
	if err != nil {
		d.Logger.Error("auth: issue verification token", "user_id", u.ID, "err", err)
		return
	}
	d.send(ctx, d.Templates.VerifyEmail(u.Email, tok))
}

func (d *Deps) send(ctx context.Context, m mailpkg.Message) {
	if err := d.Mailer.Send(ctx, m); err != nil {
		d.Logger.Error("auth: send mail", "kind", m.Kind, "err", err)
	}
}

type tokenReq struct {
	Token string `json:"token"`
}

// VerifyEmail redeems a verification link.
func (d *Deps) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if !decode(w, r, &req) {
		return
	}
	uid, err := d.Tokens.Redeem(r.Context(), req.Token, stores.PurposeVerifyEmail)
	if errors.Is(err, stores.ErrTokenInvalid) {
		writeError(w, http.StatusBadRequest, "invalid_token", "this link is invalid or has expired")
		return
	}
	if err != nil {
		d.fail(w, r, "redeem verification", err)
		return
	}
	if err := d.Users.SetEmailVerified(r.Context(), uid); err != nil {
		d.fail(w, r, "mark verified", err)
		return
	}
	d.audit(r, audit.Entry{ActorID: uid, Action: audit.ActionEmailVerified, TargetKind: "user", TargetID: uid})
	writeJSON(w, http.StatusOK, map[string]string{"status": "verified"})
}

type emailReq struct {
	Email string `json:"email"`
}

// ResendVerification mails a fresh link to an unverified account. It
// answers 202 for any address.
func (d *Deps) ResendVerification(w http.ResponseWriter, r *http.Request) {
	var req emailReq
	if !decode(w, r, &req) {
		return
	}
	if email := normalizeEmail(req.Email); email != "" {
		u, err := d.Users.ByEmail(r.Context(), email)
		if err == nil && !u.EmailVerified && u.Status == stores.StatusActive {
			d.sendVerification(r.Context(), u)
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// dummyHash is verified against when the email is unknown, so a miss
// costs the same argon2id time as a wrong password and response time
// does not reveal whether an account exists.
var dummyHash = func() string {
	h, _ := argon2id.Hash("maktaba-timing-equaliser", argon2id.DefaultParams())
	return h
}()

// Login validates credentials and issues tokens. Unknown email and
// wrong password both answer the same 401.
func (d *Deps) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	u, err := d.Users.ByEmail(r.Context(), req.Email)
	if err != nil && !errors.Is(err, stores.ErrUserNotFound) {
		d.fail(w, r, "lookup user", err)
		return
	}
	if errors.Is(err, stores.ErrUserNotFound) || !u.PasswordHash.Valid {
		_ = argon2id.Verify(req.Password, dummyHash)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	if d.Abuse != nil {
		locked, left, err := d.Abuse.LoginLocked(r.Context(), u.ID)
		if err != nil {
			d.fail(w, r, "lockout check", err)
			return
		}
		if locked {
			w.Header().Set("Retry-After", strconv.Itoa(int(left.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-ins; try again later or reset your password")
			return
		}
	}
	if err := argon2id.Verify(req.Password, u.PasswordHash.String); err != nil {
		if d.Abuse != nil {
			if err := d.Abuse.Record(r.Context(), abuse.Subject{Kind: "user", ID: u.ID}, abuse.KindLoginFailed, 1,
				map[string]any{"ip": middleware.GetClientIP(r.Context())}); err != nil {
				d.Logger.Warn("auth: record failed login", "err", err)
			}
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	if !d.admit(w, r, u) {
		return
	}
	if !u.EmailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":      "email_not_verified",
			"message":    "confirm your email address first",
			"resend_url": "/v1/auth/verify-email/resend",
		})
		return
	}
	if d.Abuse != nil {
		_ = d.Abuse.ClearLoginFailures(r.Context(), u.ID)
	}
	d.Users.TouchLogin(r.Context(), u.ID)
	d.issueTokens(w, r, u)
}

// admit refuses suspended and blocklisted accounts. It writes the
// response and reports false when the account may not sign in.
func (d *Deps) admit(w http.ResponseWriter, r *http.Request, u stores.User) bool {
	if u.Status == stores.StatusSuspended {
		writeError(w, http.StatusForbidden, "account_suspended", "this account is suspended; contact support")
		return false
	}
	if d.Abuse != nil {
		blocked, err := d.Abuse.IsBlocked(r.Context(), abuse.Subject{Kind: "user", ID: u.ID})
		if err != nil {
			d.fail(w, r, "blocklist check", err)
			return false
		}
		if blocked {
			writeError(w, http.StatusForbidden, "blocked", "")
			return false
		}
	}
	return true
}

// Refresh swaps a refresh-token cookie for a fresh access token and a
// rotated refresh token. A token that was already rotated coming back
// means it leaked: every session of the account is revoked.
func (d *Deps) Refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	sess, err := d.Sessions.Lookup(r.Context(), c.Value)
	if errors.Is(err, sessions.ErrReplayed) {
		d.replayed(r, sess.UserID)
		clearRefreshCookie(w, d.CookieDomain, d.Secure)
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	if err != nil {
		if !errors.Is(err, sessions.ErrNotFound) {
			d.Logger.Error("auth: session lookup", "err", err)
		}
		clearRefreshCookie(w, d.CookieDomain, d.Secure)
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	rotated, err := d.Sessions.Rotate(r.Context(), sess.ID)
	if err != nil {
		d.fail(w, r, "rotate session", err)
		return
	}
	if !rotated {
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	u, err := d.Users.ByID(r.Context(), sess.UserID)
	if errors.Is(err, stores.ErrUserNotFound) {
		clearRefreshCookie(w, d.CookieDomain, d.Secure)
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	if err != nil {
		d.fail(w, r, "lookup user", err)
		return
	}
	if !d.admit(w, r, u) {
		clearRefreshCookie(w, d.CookieDomain, d.Secure)
		return
	}
	d.issueTokens(w, r, u)
}

func (d *Deps) replayed(r *http.Request, userID string) {
	ctx := r.Context()
	if err := d.Sessions.RevokeAllForUser(ctx, userID); err != nil {
		d.Logger.Error("auth: revoke after refresh replay", "user_id", userID, "err", err)
	}
	ip := middleware.GetClientIP(ctx)
	if d.Abuse != nil {
		_ = d.Abuse.Record(ctx, abuse.Subject{Kind: "user", ID: userID}, abuse.KindRefreshReplay, 3, map[string]any{"ip": ip})
	}
	d.audit(r, audit.Entry{Action: audit.ActionRefreshReplay, TargetKind: "user", TargetID: userID})
	d.Logger.Warn("auth: rotated refresh token replayed; all sessions revoked", "user_id", userID, "ip", ip)
}

// Logout revokes the current refresh session and clears the cookie.
func (d *Deps) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(refreshCookie); err == nil {
		if sess, err := d.Sessions.Lookup(r.Context(), c.Value); err == nil {
			if err := d.Sessions.Revoke(r.Context(), sess.ID); err != nil {
				d.Logger.Error("auth: revoke on logout", "err", err)
			}
		}
	}
	clearRefreshCookie(w, d.CookieDomain, d.Secure)
	w.WriteHeader(http.StatusNoContent)
}

// ForgotPassword mails a reset link. It answers 202 for any address. An
// OAuth-only account can use it to add a password.
func (d *Deps) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req emailReq
	if !decode(w, r, &req) {
		return
	}
	if email := normalizeEmail(req.Email); email != "" {
		u, err := d.Users.ByEmail(r.Context(), email)
		if err == nil && u.Status == stores.StatusActive {
			tok, err := d.Tokens.Issue(r.Context(), u.ID, stores.PurposePasswordReset, stores.PasswordResetTTL)
			if err != nil {
				d.Logger.Error("auth: issue reset token", "user_id", u.ID, "err", err)
			} else {
				d.send(r.Context(), d.Templates.PasswordReset(u.Email, tok))
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

type resetReq struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// ResetPassword redeems a reset link and sets a new password. The link
// proves control of the address, so it also verifies the email; every
// session is revoked and failed-login history cleared.
func (d *Deps) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetReq
	if !decode(w, r, &req) {
		return
	}
	if err := password.Validate(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	hash, err := argon2id.Hash(req.Password, argon2id.DefaultParams())
	if err != nil {
		d.fail(w, r, "hash", err)
		return
	}
	uid, err := d.Tokens.Redeem(r.Context(), req.Token, stores.PurposePasswordReset)
	if errors.Is(err, stores.ErrTokenInvalid) {
		writeError(w, http.StatusBadRequest, "invalid_token", "this link is invalid or has expired")
		return
	}
	if err != nil {
		d.fail(w, r, "redeem reset", err)
		return
	}
	if err := d.Users.SetPasswordHash(r.Context(), uid, hash); err != nil {
		d.fail(w, r, "set password", err)
		return
	}
	if err := d.Users.SetEmailVerified(r.Context(), uid); err != nil {
		d.Logger.Error("auth: verify email on reset", "user_id", uid, "err", err)
	}
	if err := d.Sessions.RevokeAllForUser(r.Context(), uid); err != nil {
		d.Logger.Error("auth: revoke sessions on reset", "user_id", uid, "err", err)
	}
	if d.Abuse != nil {
		_ = d.Abuse.ClearLoginFailures(r.Context(), uid)
	}
	d.audit(r, audit.Entry{ActorID: uid, Action: audit.ActionPasswordReset, TargetKind: "user", TargetID: uid})
	w.WriteHeader(http.StatusNoContent)
}

// Providers tells the sign-in page which OAuth buttons to show.
func (d *Deps) Providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"google": d.Google != nil, "apple": d.Apple != nil})
}

// issueTokens is the shared tail of login/refresh/OAuth: a fresh
// session, the refresh cookie, and an access token.
func (d *Deps) issueTokens(w http.ResponseWriter, r *http.Request, u stores.User) {
	access, ok := d.startSession(w, r, u)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, authResp{
		AccessToken: access,
		ExpiresIn:   int(token.AccessTTL.Seconds()),
		User:        userViewFrom(u),
	})
}

// startSession creates the session, sets the refresh cookie and returns
// an access token. On failure it has written the error response.
func (d *Deps) startSession(w http.ResponseWriter, r *http.Request, u stores.User) (string, bool) {
	sess, raw, err := d.Sessions.Create(r.Context(), u.ID, truncate(r.UserAgent(), 400), middleware.GetClientIP(r.Context()))
	if err != nil {
		d.fail(w, r, "create session", err)
		return "", false
	}
	access, err := d.Signer.Issue(u.ID, u.Email, u.Plan, sess.ID, time.Now())
	if err != nil {
		d.fail(w, r, "issue token", err)
		return "", false
	}
	setRefreshCookie(w, raw, d.CookieDomain, d.Secure, sess.ExpiresAt)
	return access, true
}

func (d *Deps) audit(r *http.Request, e audit.Entry) {
	if d.Audit == nil {
		return
	}
	if e.IP == "" {
		e.IP = middleware.GetClientIP(r.Context())
	}
	if err := d.Audit.Write(r.Context(), e); err != nil {
		d.Logger.Error("auth: write audit", "action", e.Action, "err", err)
	}
}

// fail logs an internal error with its cause and answers a generic 500;
// the cause stays in the log, not in the response.
func (d *Deps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	d.Logger.Error("auth: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal_error", "")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
