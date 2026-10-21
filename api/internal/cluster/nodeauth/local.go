package nodeauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
)

// Local is this machine's own copy of the home's database, for the credentials the
// home cannot be asked about (specs/epics/37-node-replica): the home's own lookup run
// on the copy, exact up to the moment the copy last applied the home's changes (a
// sign-out made on the home after that reaches it when the home is back).
type Local interface {
	// Introspect answers for a session or a token; ErrNotLive when the copy says it is
	// not a live credential.
	Introspect(ctx context.Context, req delegation.IntrospectRequest) (*delegation.IntrospectAnswer, error)
	// Principal is a user's principal (a node-local session's), ErrNotLive for a user
	// the copy does not have.
	Principal(ctx context.Context, userID string) (*delegation.Principal, error)
	// SignIn checks a username and password: the user's principal; ErrNotLive for a
	// wrong pair, ErrLocked for an account the home locked.
	SignIn(ctx context.Context, username, password string) (*delegation.Principal, error)
	// Synced is the home's time of the last change the copy applied (the home's beat,
	// written every few seconds while a machine follows it).
	Synced(ctx context.Context) (time.Time, error)
}

// ErrNotLive: the copy says the credential is not a live one (signed out, expired,
// revoked, wrong password).
var ErrNotLive = errors.New("nodeauth: not a live credential")

// ErrLocked: the account is locked (too many failed sign-ins).
var ErrLocked = errors.New("nodeauth: account locked")

// localTimeout bounds a lookup on the copy (it is on this machine: a slow answer is a
// copy in trouble, and the request is treated as one the copy cannot answer).
const localTimeout = 3 * time.Second

// signInFloor pads a failed offline sign-in as the home pads its own (no timing tells
// a wrong password from an unknown user).
var signInFloor = 500 * time.Millisecond

// The home's lockout (handlers/auth): this many failed sign-ins in the window lock the
// name. The copy is read only, so a node counts its own.
const (
	lockoutThreshold = 5
	lockoutWindow    = 15 * time.Minute
)

// Default lifetimes of a node-local sign-in (rules.cluster).
const (
	DefaultLocalSessionTTL   = 12 * time.Hour
	DefaultLocalSessionGrace = 30 * time.Minute
)

func (r *Resolver) localTTL() time.Duration {
	if r.LocalSessionTTL != nil {
		if d := r.LocalSessionTTL(); d > 0 {
			return d
		}
	}
	return DefaultLocalSessionTTL
}

func (r *Resolver) localGrace() time.Duration {
	if r.LocalSessionGrace != nil {
		if d := r.LocalSessionGrace(); d >= 0 {
			return d
		}
	}
	return DefaultLocalSessionGrace
}

// CopyCurrent reports whether the copy may answer with the home away: it had applied
// the home's changes up to no more than the max lag before the home was last heard (or,
// on a machine that never heard it, before now), on the home's clock, which the copy's
// beat is stamped on (HomeOffset).
func (r *Resolver) CopyCurrent(ctx context.Context) bool {
	if r.Local == nil {
		return false
	}
	lctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	synced, err := r.Local.Synced(lctx)
	if err != nil || synced.IsZero() {
		return false
	}
	r.mu.Lock()
	last := r.lastOK
	r.mu.Unlock()
	if last.IsZero() {
		last = r.now()
	}
	if r.HomeOffset != nil {
		last = last.Add(r.HomeOffset())
	}
	return !synced.Before(last.Add(-r.copyMaxLag()))
}

// copyAnswer is the copy's answer for a credential: said false when the copy cannot
// say (none, not answering, or not current); otherwise the answer for a live one, or
// nil for one that is not.
func (r *Resolver) copyAnswer(ctx context.Context, ireq delegation.IntrospectRequest) (*delegation.IntrospectAnswer, bool) {
	if r.Local == nil || (ireq.Session == "" && ireq.PAT == "") || !r.CopyCurrent(ctx) {
		return nil, false
	}
	lctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	a, err := r.Local.Introspect(lctx, ireq)
	switch {
	case errors.Is(err, ErrNotLive):
		return nil, true
	case err != nil:
		return nil, false
	case !a.ExpiresAt.IsZero() && !r.now().Before(a.ExpiresAt):
		return nil, true
	case ireq.Session != "" && r.revokedLately(SessionKey(ireq.Session), a.Principal.UserID),
		ireq.PAT != "" && r.revokedLately(patKey(ireq.PAT), a.Principal.UserID):
		// the home pushed its revocation; the copy does not have it yet
		return nil, true
	}
	return a, true
}

// jwtFromCopy holds a bearer access token's user to the copy while the home cannot be
// asked: said false when the copy cannot say; nil for a user it does not have (or a
// revocation the home pushed names); otherwise the user as the copy has them.
func (r *Resolver) jwtFromCopy(ctx context.Context, p *principal.Principal) (*principal.Principal, bool) {
	if p == nil || r.Local == nil || !r.CopyCurrent(ctx) {
		return nil, false
	}
	lctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	dp, err := r.Local.Principal(lctx, p.UserID)
	switch {
	case errors.Is(err, ErrNotLive):
		return nil, true
	case err != nil:
		return nil, false
	case r.revokedLately("", dp.UserID):
		return nil, true
	}
	out := toPrincipal(&delegation.IntrospectAnswer{Principal: *dp})
	out.Source, out.MustChangePassword = principal.SourceJWT, false
	return out, true
}

// fromCopy is copyAnswer as the principal to attach and its CSRF token hash.
func (r *Resolver) fromCopy(ctx context.Context, ireq delegation.IntrospectRequest) (*principal.Principal, string, bool) {
	a, said := r.copyAnswer(ctx, ireq)
	if a == nil {
		return nil, "", said
	}
	return toPrincipal(a), a.CSRFSHA256, true
}

// localPrincipal is a node-local session's user, from the copy, and its CSRF token
// hash. The copy need not be current: the session was made against it, and the user's
// rights are the copy's latest. A person the home revoked lately (signed out
// everywhere, reset, locked), or who must change their password now, is not let in.
func (r *Resolver) localPrincipal(ctx context.Context, id string) (*delegation.Principal, string, bool) {
	if r.Sessions == nil || r.Local == nil {
		return nil, "", false
	}
	userID, csrf, ok := r.Sessions.Lookup(id)
	if !ok {
		return nil, "", false
	}
	lctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	dp, err := r.Local.Principal(lctx, userID)
	if err != nil || dp.MustChangePassword || r.revokedLately("", userID) {
		return nil, "", false
	}
	return dp, csrf, true
}

func (r *Resolver) localSession(ctx context.Context, id string) (*principal.Principal, string, bool) {
	dp, csrf, ok := r.localPrincipal(ctx, id)
	if !ok {
		return nil, "", false
	}
	p := toPrincipal(&delegation.IntrospectAnswer{Principal: *dp, Source: string(principal.SourceCookie)})
	p.MustChangePassword = false // never made for such an account (SignIn)
	return p, csrf, true
}

// isLocalSession reports whether the request carries a node-local session cookie.
func isLocalSession(req *http.Request) (string, bool) {
	if bearer(req) != "" {
		return "", false
	}
	c, err := req.Cookie(CookieSession)
	if err != nil || !strings.HasPrefix(c.Value, LocalSessionPrefix) {
		return "", false
	}
	return c.Value, true
}

// LoginPath and LogoutPath are the sign-in routes a node answers itself: the sign-in
// with the home away, the sign-out of a node-local session always.
const (
	LoginPath  = "/api/auth/login"
	LogoutPath = "/api/auth/logout"
)

// locked reports whether a name has failed lockoutThreshold times in the window.
func (r *Resolver) locked(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.recentFails(name)) >= lockoutThreshold
}

func (r *Resolver) recentFails(name string) []time.Time {
	cut := r.now().Add(-lockoutWindow)
	kept := r.fails[name][:0]
	for _, t := range r.fails[name] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(r.fails, name)
		return nil
	}
	r.fails[name] = kept
	return kept
}

// failed counts a failed sign-in for name. With maxEntries names counted, the ones
// whose failures are past the window go first, then those not locked: a locked name is
// never forgotten (spraying names does not unlock one); with every one locked, a new
// name is not counted (the sign-in's rate limit per address still holds).
func (r *Resolver) failed(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fails == nil {
		r.fails = map[string][]time.Time{}
	}
	if _, counted := r.fails[name]; !counted && len(r.fails) >= maxEntries {
		for n := range r.fails {
			r.recentFails(n)
		}
		for n, f := range r.fails {
			if len(r.fails) < maxEntries {
				break
			}
			if len(f) < lockoutThreshold {
				delete(r.fails, n)
			}
		}
		if len(r.fails) >= maxEntries {
			return
		}
	}
	r.fails[name] = append(r.recentFails(name), r.now())
}

func (r *Resolver) signedIn(name string) {
	r.mu.Lock()
	delete(r.fails, name)
	r.mu.Unlock()
}

// SignIn answers POST /api/auth/login with the home not answering (false: the request
// is another one, or this machine cannot sign anyone in: no copy): the password checked
// against the copy, a node-local session on success. A web sign-in only: a native app
// signs in with the home.
func (r *Resolver) SignIn(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path != LoginPath || req.Method != http.MethodPost || r.Local == nil || r.Sessions == nil {
		return false
	}
	w.Header().Set(HomeStateHeader, "unreachable")
	if strings.EqualFold(req.Header.Get("X-Maktaba-Client"), "native") {
		writeUnreachable(w, true)
		return true
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	src := io.Reader(http.MaxBytesReader(w, req.Body, 8<<10))
	if kept, ok := req.Context().Value(signInBodyKey{}).([]byte); ok {
		src = bytes.NewReader(kept) // the proxy read the request's own
	}
	if err := json.NewDecoder(src).Decode(&body); err != nil ||
		strings.TrimSpace(body.Username) == "" || body.Password == "" {
		writeProblem(w, http.StatusBadRequest, "https://maktaba.dev/problems/bad-request",
			"username and password required", "")
		return true
	}
	if !r.CopyCurrent(req.Context()) {
		// a copy that stopped following the home long before it went away might not
		// have a password change or a removal made since
		writeUnreachable(w, false)
		return true
	}
	name := strings.ToLower(strings.TrimSpace(body.Username))
	start := time.Now()
	pad := func() {
		if d := signInFloor - time.Since(start); d > 0 {
			time.Sleep(d)
		}
	}
	if r.locked(name) {
		pad()
		writeLocked(w)
		return true
	}
	lctx, cancel := context.WithTimeout(req.Context(), localTimeout)
	dp, err := r.Local.SignIn(lctx, body.Username, body.Password)
	cancel()
	switch {
	case errors.Is(err, ErrLocked):
		pad()
		writeLocked(w)
		return true
	case errors.Is(err, ErrNotLive):
		r.failed(name)
		pad()
		writeProblem(w, http.StatusUnauthorized, "https://maktaba.dev/problems/invalid-credentials",
			"invalid credentials", "")
		return true
	case err != nil:
		writeUnreachable(w, false)
		return true
	}
	r.signedIn(name)
	if dp.MustChangePassword {
		// the change it asks for is the home's: the sign-in page says so
		w.Header().Set("Content-Type", "application/problem+json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.dev/problems/password-change-waits",
			"title": "the password change waits for the home", "status": http.StatusServiceUnavailable,
			"code":   "password_change_waits",
			"detail": "this account must change its password, which waits for the home"})
		return true
	}
	id, csrf, expires, err := r.Sessions.Create(dp.UserID, dp.Username, r.localTTL())
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "https://maktaba.dev/problems/internal", "sign-in failed", "")
		return true
	}
	if r.Logger != nil {
		r.Logger.Info("node: signed in on this machine, from its copy (the home is away)", "user_id", dp.UserID,
			"until", expires, "event", "node_local_sign_in")
	}
	r.setCookies(w, id, csrf, expires)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"session": "node", "user": map[string]any{
		"id": dp.UserID, "username": dp.Username, "is_admin": dp.IsAdmin, "must_change_password": false}})
	return true
}

// SignOut ends a node-local session (POST /api/auth/logout with one: the home knows
// nothing of it, so this machine answers whether the home is there or not). "Sign out
// everywhere" (/api/auth/logout-all) ends every node-local session of the user and,
// through the home once it answers, their home sessions made before it (signout.go).
// False: the request is another one.
func (r *Resolver) SignOut(w http.ResponseWriter, req *http.Request) bool {
	all := req.URL.Path == LogoutAllPath
	if (req.URL.Path != LogoutPath && !all) || req.Method != http.MethodPost || r.Sessions == nil {
		return false
	}
	id, ok := isLocalSession(req)
	if !ok {
		return false
	}
	user, csrf, live := r.Sessions.Lookup(id)
	if live && !csrfOK(req, csrf) {
		writeProblem(w, http.StatusForbidden, "https://maktaba.dev/problems/csrf-mismatch", "csrf token mismatch", "")
		return true
	}
	var err error
	if all && live {
		err = r.Sessions.EndUser(user)
		if err == nil && r.SignOuts != nil {
			err = r.SignOuts.add("u:"+user, signOut{User: user, All: true, At: r.now()})
			r.Evict(delegation.Revoked{UserID: user})
		}
	} else {
		err = r.Sessions.End(id)
	}
	if err != nil && r.Logger != nil {
		r.Logger.Warn("node: a node-local sign-out was not saved", "err", err, "event", "node_local_sessions_failed")
	}
	r.setCookies(w, "", "", time.Time{})
	w.WriteHeader(http.StatusNoContent)
	return true
}

// setCookies sets (or, with a zero expiry, clears) the session and CSRF cookies as the
// home's sign-in does: host-only, the session cookie HttpOnly, both SameSite=Lax.
func (r *Resolver) setCookies(w http.ResponseWriter, id, csrf string, expires time.Time) {
	maxAge := -1
	if !expires.IsZero() {
		if maxAge = int(expires.Sub(r.now()).Seconds()); maxAge <= 0 {
			maxAge = -1
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieSession, Value: id, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.SecureCookies, MaxAge: maxAge})
	http.SetCookie(w, &http.Cookie{Name: CookieCSRF, Value: csrf, Path: "/",
		SameSite: http.SameSiteLaxMode, Secure: r.SecureCookies, MaxAge: maxAge})
}

func writeLocked(w http.ResponseWriter) {
	writeProblem(w, http.StatusLocked, "https://maktaba.dev/problems/account-locked", "account locked",
		"too many failed sign-ins: try again later")
}

func writeProblem(w http.ResponseWriter, status int, typ, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{"type": typ, "title": title, "status": status}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body)
}
