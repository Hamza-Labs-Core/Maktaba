package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/oauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// GoogleStart kicks off the OAuth flow. We mint a `state` value, store
// it in a short-lived cookie, and 302 the browser to Google.
func (d *Deps) GoogleStart(w http.ResponseWriter, r *http.Request) {
	if d.Google == nil {
		writeError(w, http.StatusNotImplemented, "google_disabled", "")
		return
	}
	state, err := randomState()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_failed", err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/v1/auth/oauth",
		HttpOnly: true,
		Secure:   d.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, d.Google.AuthURL(state), http.StatusFound)
}

// GoogleCallback completes the flow. We verify state, exchange the
// code, look up or create the user, then issue cloud tokens.
func (d *Deps) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	if d.Google == nil {
		writeError(w, http.StatusNotImplemented, "google_disabled", "")
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "missing_params", "")
		return
	}
	c, err := r.Cookie("oauth_state")
	if err != nil || c.Value != state {
		writeError(w, http.StatusBadRequest, "bad_state", "")
		return
	}
	id, err := d.Google.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "oauth_exchange_failed", err.Error())
		return
	}
	u, err := d.federate(r, "google", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "federate_failed", err.Error())
		return
	}
	d.issueTokens(w, r, u)
}

// AppleCallback is the POST-style endpoint Apple uses (form_post mode).
func (d *Deps) AppleCallback(w http.ResponseWriter, r *http.Request) {
	if d.Apple == nil {
		writeError(w, http.StatusNotImplemented, "apple_disabled", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	code := r.PostForm.Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing_code", "")
		return
	}
	id, err := d.Apple.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusBadGateway, "oauth_exchange_failed", err.Error())
		return
	}
	u, err := d.federate(r, "apple", id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "federate_failed", err.Error())
		return
	}
	d.issueTokens(w, r, u)
}

// federate resolves an external identity to a local user row,
// creating one if neither (provider, subject) nor email match anything
// we know.
func (d *Deps) federate(r *http.Request, provider string, id oauth.Identity) (stores.User, error) {
	if id.Subject == "" {
		return stores.User{}, errors.New("oauth: empty subject")
	}
	if u, err := d.Users.UserByOAuth(r.Context(), provider, id.Subject); err == nil {
		return u, nil
	}
	if id.Email != "" {
		if u, err := d.Users.ByEmail(r.Context(), id.Email); err == nil {
			_ = d.Users.LinkOAuth(r.Context(), u.ID, provider, id.Subject, id.Email)
			return u, nil
		}
	}
	// Create a passwordless account. We generate a random placeholder
	// hash so the DB column is non-null even though no one can log in
	// with it — preserves the invariant that local-auth requires a
	// password reset to enable.
	placeholder, _ := argon2id.Hash(randomFiller(), argon2id.DefaultParams())
	u, err := d.Users.Create(r.Context(), id.Email, placeholder, id.DisplayName, id.EmailVerified)
	if err != nil {
		return stores.User{}, err
	}
	if err := d.Users.LinkOAuth(r.Context(), u.ID, provider, id.Subject, id.Email); err != nil {
		return stores.User{}, err
	}
	return u, nil
}

func randomFiller() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randomState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
