package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/abuse"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// The OAuth flows keep their per-attempt secrets in short-lived,
// HttpOnly cookies scoped to /v1/auth/oauth: state (CSRF), nonce
// (id_token replay) and, for Google, the PKCE verifier.
const (
	cookieState    = "oauth_state"
	cookieNonce    = "oauth_nonce"
	cookieVerifier = "oauth_pkce"
	oauthCookieTTL = 10 * time.Minute
	oauthPath      = "/v1/auth/oauth"
)

// errOAuth codes end up in the sign-in page's ?error= parameter.
var (
	errEmailInUse      = errors.New("email_in_use")
	errNoEmail         = errors.New("no_email")
	errEmailUnverified = errors.New("email_unverified")
)

func (d *Deps) setOAuthCookie(w http.ResponseWriter, name, value string, crossSite bool) {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     oauthPath,
		HttpOnly: true,
		Secure:   d.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(oauthCookieTTL),
	}
	if crossSite {
		// Apple posts the callback from appleid.apple.com; a Lax cookie
		// is not sent on a cross-site POST. None requires Secure.
		c.SameSite = http.SameSiteNoneMode
		c.Secure = true
	}
	http.SetCookie(w, c)
}

func (d *Deps) clearOAuthCookies(w http.ResponseWriter) {
	for _, n := range []string{cookieState, cookieNonce, cookieVerifier} {
		http.SetCookie(w, &http.Cookie{Name: n, Value: "", Path: oauthPath, MaxAge: -1, HttpOnly: true, Secure: d.Secure})
	}
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// GoogleStart begins the Google flow.
func (d *Deps) GoogleStart(w http.ResponseWriter, r *http.Request) {
	if d.Google == nil {
		writeError(w, http.StatusNotImplemented, "google_disabled", "")
		return
	}
	state, err1 := oauth.RandomToken(24)
	nonce, err2 := oauth.RandomToken(24)
	verifier, err3 := oauth.RandomToken(48)
	if err := errors.Join(err1, err2, err3); err != nil {
		d.fail(w, r, "oauth randomness", err)
		return
	}
	d.setOAuthCookie(w, cookieState, state, false)
	d.setOAuthCookie(w, cookieNonce, nonce, false)
	d.setOAuthCookie(w, cookieVerifier, verifier, false)
	http.Redirect(w, r, d.Google.AuthURL(state, nonce, oauth.PKCEChallenge(verifier)), http.StatusFound)
}

// GoogleCallback completes the Google flow and lands in the web app.
func (d *Deps) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if d.Google == nil {
		writeError(w, http.StatusNotImplemented, "google_disabled", "")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		d.clearOAuthCookies(w)
		d.toSignIn(w, r, "oauth_cancelled")
		return
	}
	if !d.stateOK(r, q.Get("state")) {
		d.clearOAuthCookies(w)
		d.toSignIn(w, r, "bad_state")
		return
	}
	code := q.Get("code")
	nonce, verifier := cookieValue(r, cookieNonce), cookieValue(r, cookieVerifier)
	d.clearOAuthCookies(w)
	if code == "" || nonce == "" || verifier == "" {
		d.toSignIn(w, r, "bad_state")
		return
	}
	id, err := d.Google.Exchange(r.Context(), code, verifier, nonce)
	if err != nil {
		d.Logger.Warn("auth: google exchange", "err", err)
		d.toSignIn(w, r, "oauth_failed")
		return
	}
	d.finishOAuth(w, r, "google", id)
}

// AppleStart begins Sign in with Apple.
func (d *Deps) AppleStart(w http.ResponseWriter, r *http.Request) {
	if d.Apple == nil {
		writeError(w, http.StatusNotImplemented, "apple_disabled", "")
		return
	}
	state, err1 := oauth.RandomToken(24)
	nonce, err2 := oauth.RandomToken(24)
	if err := errors.Join(err1, err2); err != nil {
		d.fail(w, r, "oauth randomness", err)
		return
	}
	d.setOAuthCookie(w, cookieState, state, true)
	d.setOAuthCookie(w, cookieNonce, nonce, true)
	http.Redirect(w, r, d.Apple.AuthURL(state, nonce), http.StatusFound)
}

// AppleCallback is the form_post endpoint Apple redirects to.
func (d *Deps) AppleCallback(w http.ResponseWriter, r *http.Request) {
	if d.Apple == nil {
		writeError(w, http.StatusNotImplemented, "apple_disabled", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		d.toSignIn(w, r, "oauth_failed")
		return
	}
	if r.PostForm.Get("error") != "" {
		d.clearOAuthCookies(w)
		d.toSignIn(w, r, "oauth_cancelled")
		return
	}
	if !d.stateOK(r, r.PostForm.Get("state")) {
		d.clearOAuthCookies(w)
		d.toSignIn(w, r, "bad_state")
		return
	}
	code, nonce := r.PostForm.Get("code"), cookieValue(r, cookieNonce)
	d.clearOAuthCookies(w)
	if code == "" || nonce == "" {
		d.toSignIn(w, r, "bad_state")
		return
	}
	id, err := d.Apple.Exchange(r.Context(), code, nonce)
	if err != nil {
		d.Logger.Warn("auth: apple exchange", "err", err)
		d.toSignIn(w, r, "oauth_failed")
		return
	}
	if id.DisplayName == "" {
		id.DisplayName = oauth.AppleUserName(r.PostForm.Get("user"))
	}
	d.finishOAuth(w, r, "apple", id)
}

// stateOK compares the state echoed by the provider with our cookie. A
// mismatch is a CSRF attempt (or a stale tab) and is recorded.
func (d *Deps) stateOK(r *http.Request, got string) bool {
	want := cookieValue(r, cookieState)
	if want != "" && got == want {
		return true
	}
	if d.Abuse != nil {
		ip := middleware.GetClientIP(r.Context())
		if ip != "" {
			_ = d.Abuse.Record(r.Context(), abuse.Subject{Kind: "ip", ID: ip}, abuse.KindOAuthState, 1, nil)
		}
	}
	return false
}

// finishOAuth federates the identity, starts a session (the refresh
// cookie) and sends the browser to the web app, which exchanges the
// cookie for an access token via /v1/auth/refresh.
func (d *Deps) finishOAuth(w http.ResponseWriter, r *http.Request, provider string, id oauth.Identity) {
	u, err := d.federate(r.Context(), provider, id)
	switch {
	case errors.Is(err, errEmailInUse):
		d.toSignIn(w, r, "email_in_use")
		return
	case errors.Is(err, errNoEmail):
		d.toSignIn(w, r, "no_email")
		return
	case errors.Is(err, errEmailUnverified):
		d.toSignIn(w, r, "email_unverified")
		return
	case err != nil:
		d.Logger.Error("auth: federate", "provider", provider, "err", err)
		d.toSignIn(w, r, "oauth_failed")
		return
	}
	if u.Status == stores.StatusSuspended {
		d.toSignIn(w, r, "account_suspended")
		return
	}
	if d.Abuse != nil {
		if blocked, err := d.Abuse.IsBlocked(r.Context(), abuse.Subject{Kind: "user", ID: u.ID}); err != nil || blocked {
			d.toSignIn(w, r, "blocked")
			return
		}
	}
	if _, ok := d.startSessionNoBody(w, r, u); !ok {
		d.toSignIn(w, r, "oauth_failed")
		return
	}
	d.Users.TouchLogin(r.Context(), u.ID)
	http.Redirect(w, r, d.WebURL+"/signed-in", http.StatusFound)
}

// startSessionNoBody is startSession without writing an error body, for
// the redirecting OAuth callbacks.
func (d *Deps) startSessionNoBody(w http.ResponseWriter, r *http.Request, u stores.User) (string, bool) {
	sess, raw, err := d.Sessions.Create(r.Context(), u.ID, truncate(r.UserAgent(), 400), middleware.GetClientIP(r.Context()))
	if err != nil {
		d.Logger.Error("auth: create session", "err", err)
		return "", false
	}
	setRefreshCookie(w, raw, d.CookieDomain, d.Secure, sess.ExpiresAt)
	return sess.ID, true
}

func (d *Deps) toSignIn(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, d.WebURL+"/sign-in?error="+url.QueryEscape(code), http.StatusFound)
}

// federate resolves an external identity to a local account:
//  1. an existing link for (provider, subject) wins;
//  2. otherwise, when the provider vouches for the email, it links to
//     the account with that email — only if that account's own email is
//     verified too, or an attacker could pre-register a victim's
//     address and wait for them to sign in with Google;
//  3. otherwise a new passwordless account is created.
func (d *Deps) federate(ctx context.Context, provider string, id oauth.Identity) (stores.User, error) {
	if u, err := d.Users.UserByOAuth(ctx, provider, id.Subject); err == nil {
		return u, nil
	} else if !errors.Is(err, stores.ErrUserNotFound) {
		return stores.User{}, err
	}
	email := normalizeEmail(id.Email)
	if email == "" {
		return stores.User{}, errNoEmail
	}
	existing, err := d.Users.ByEmail(ctx, email)
	switch {
	case err == nil:
		if !id.EmailVerified || !existing.EmailVerified {
			return stores.User{}, errEmailInUse
		}
		if err := d.Users.LinkOAuth(ctx, existing.ID, provider, id.Subject, email); err != nil {
			return stores.User{}, err
		}
		return existing, nil
	case !errors.Is(err, stores.ErrUserNotFound):
		return stores.User{}, err
	}
	// A new account needs an address the provider vouches for: an
	// unverified one could be someone else's, and the link would outlive
	// the real owner claiming the account later.
	if !id.EmailVerified {
		return stores.User{}, errEmailUnverified
	}
	name := id.DisplayName
	if len([]rune(name)) > maxDisplayName {
		name = string([]rune(name)[:maxDisplayName])
	}
	u, err := d.Users.Create(ctx, email, "", name, true)
	if err != nil {
		if isUniqueViolation(err) {
			return stores.User{}, errEmailInUse
		}
		return stores.User{}, err
	}
	if err := d.Users.LinkOAuth(ctx, u.ID, provider, id.Subject, email); err != nil {
		return stores.User{}, err
	}
	return u, nil
}
