// Package auth wires the cloud's HTTP authentication surface:
// /v1/auth/register, /v1/auth/login, /v1/auth/refresh, /v1/auth/logout
// and the OAuth callback endpoints.
//
// Each handler is intentionally small — the heavy lifting lives in
// stores.Users (DB), argon2id (hashing), password (policy), sessions
// (refresh tokens), and token (access tokens). Handlers only orchestrate
// these layers, set HTTP cookies, and shape JSON responses.
package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/password"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/token"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Deps bundles the collaborators each handler needs. We pass it via
// closure rather than embedding into a struct because the handler set
// is small and a struct adds no clarity here.
type Deps struct {
	Users    *stores.Users
	Sessions *sessions.Store
	Signer   *token.Signer
	Logger   *slog.Logger
	Google   *oauth.GoogleFlow
	Apple    *oauth.AppleFlow
	CookieDomain string
	Secure       bool
}

// Mount registers all auth routes on the given router prefix.
func Mount(r interface {
	Post(pattern string, h http.HandlerFunc)
	Get(pattern string, h http.HandlerFunc)
}, d Deps) {
	r.Post("/v1/auth/register", d.Register)
	r.Post("/v1/auth/login", d.Login)
	r.Post("/v1/auth/refresh", d.Refresh)
	r.Post("/v1/auth/logout", d.Logout)
	r.Get("/v1/auth/oauth/google", d.GoogleStart)
	r.Get("/v1/auth/oauth/google/callback", d.GoogleCallback)
	r.Post("/v1/auth/oauth/apple/callback", d.AppleCallback)
}

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type authResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	User        userView `json:"user"`
}

type userView struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	DisplayName   string `json:"display_name,omitempty"`
	Plan          string `json:"plan"`
}

// Register creates a new account, returns an access token, and sets
// the refresh-token cookie. Failure modes: 400 invalid body, 409 email
// in use, 422 weak password.
func (d *Deps) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	if err := password.Validate(req.Password); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	}
	hash, err := argon2id.Hash(req.Password, argon2id.DefaultParams())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_error", err.Error())
		return
	}
	u, err := d.Users.Create(r.Context(), req.Email, hash, req.DisplayName, false)
	if err != nil {
		// The unique-violation manifests as `pq: duplicate key`; we
		// surface 409 rather than echoing the raw error.
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "email_in_use", "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	d.issueTokens(w, r, u)
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login validates credentials and issues tokens. Returns the same
// generic 401 for unknown email and wrong password to avoid leaking
// account existence.
func (d *Deps) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	u, err := d.Users.ByEmail(r.Context(), req.Email)
	if errors.Is(err, stores.ErrUserNotFound) || !u.PasswordHash.Valid {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	if err := argon2id.Verify(req.Password, u.PasswordHash.String); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "")
		return
	}
	d.Users.TouchLogin(r.Context(), u.ID)
	d.issueTokens(w, r, u)
}

// Refresh swaps a refresh-token cookie for a fresh access token.
// Implements rotating refresh tokens: every refresh revokes the old
// session and issues a new one.
func (d *Deps) Refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("maktaba_refresh")
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	sess, err := d.Sessions.Lookup(r.Context(), c.Value)
	if err != nil {
		clearRefreshCookie(w, d.CookieDomain, d.Secure)
		writeError(w, http.StatusUnauthorized, "no_session", "")
		return
	}
	if err := d.Sessions.Revoke(r.Context(), sess.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "rotate_failed", err.Error())
		return
	}
	u, err := d.Users.ByID(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup_failed", err.Error())
		return
	}
	d.issueTokens(w, r, u)
}

// Logout revokes the current refresh session and clears the cookie.
func (d *Deps) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("maktaba_refresh"); err == nil {
		if sess, err := d.Sessions.Lookup(r.Context(), c.Value); err == nil {
			_ = d.Sessions.Revoke(r.Context(), sess.ID)
		}
	}
	clearRefreshCookie(w, d.CookieDomain, d.Secure)
	w.WriteHeader(http.StatusNoContent)
}

// issueTokens is the shared tail of register/login/refresh. Creates a
// fresh session, sets the cookie, mints an access token, and writes
// the JSON envelope.
func (d *Deps) issueTokens(w http.ResponseWriter, r *http.Request, u stores.User) {
	sess, raw, err := d.Sessions.Create(r.Context(), u.ID, r.UserAgent(), middleware.GetRequestID(r.Context()))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session_failed", err.Error())
		return
	}
	access, err := d.Signer.Issue(u.ID, u.Email, u.Plan, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_failed", err.Error())
		return
	}
	setRefreshCookie(w, raw, d.CookieDomain, d.Secure, sess.ExpiresAt)
	resp := authResp{
		AccessToken: access,
		ExpiresIn:   int(token.AccessTTL.Seconds()),
		User:        userViewFrom(u),
	}
	writeJSON(w, http.StatusOK, resp)
}
