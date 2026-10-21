// Package nodeauth resolves who is asking on a full node (Epic 35 story 35.9, design
// §10.3). A node never reads the sessions table: it asks the home (homelink.Link,
// signed), caches a good answer 30 s and a refusal 5 s, keyed by the SHA-256 of the
// credential, and drops cached answers the moment the home pushes a revocation.
//
// A home outage degrades a node to read-only instead of taking it down
// (specs/epics/37-node-replica). The node has its own live copy of the home's database
// (Local): a credential the home cannot be asked about is looked up there, as the home
// would, and so is a sign-in (a node-local session, valid on this machine only:
// localsess.go). The copy answers only while it is current: it had applied the home's
// changes up to no more than rules.cluster.offline_copy_max_lag_minutes before the home
// was last heard (a copy that stopped following long before might not hold a sign-out
// or a password change made since). Then a read (GET, HEAD) and this machine's own
// routes (`local`: its status, playback, TV) are served, marked X-Maktaba-Home:
// unreachable so the app shows it; a change through the home's database gets 503
// home_unreachable. With no usable copy (none yet, being made, or behind) a `local`
// route answers anonymously (its auth gate decides) and anything else is a 503. Once
// the home has not answered for the grace period (30 s,
// rules.cluster.delegate_unreachable_grace_seconds) the node stops asking it per
// request, and `home` routes are refused at once (Home), but for the session probe,
// the sign-in and the sign-out, which this machine answers from its copy. A sign-out
// made here is real: the session is denied here at once and the home is told when it
// answers (signout.go). A bearer access token is held to the same rules (verified
// here, its user checked on the copy). A revocation the home pushes is honoured on the
// copy too until the copy has it (a copy is moments behind the home).
//
// The principal it attaches is the home's answer; the node then runs the same
// middleware and handlers as the home (roles, admin checks, library ACLs, CSRF double
// submit against the session's CSRF token hash).
package nodeauth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
)

// Cache lifetimes of design §10.3.
const (
	PositiveTTL  = 30 * time.Second
	NegativeTTL  = 5 * time.Second
	JWKSTTL      = 5 * time.Minute
	DefaultGrace = 30 * time.Second
	ProbeEvery   = 5 * time.Second
	maxEntries   = 8192
	// DefaultCopyMaxLag: how far behind the home the copy may have been when the home
	// was last heard, for it to answer.
	DefaultCopyMaxLag = 10 * time.Minute
)

// HomeStateHeader marks a response served while the home is unreachable (read-only).
const HomeStateHeader = "X-Maktaba-Home"

// Cookie and header names as the home's auth handlers use them.
const (
	CookieSession = "mkt_sess"
	CookieCSRF    = "mkt_csrf"
	CSRFHeader    = "X-Maktaba-CSRF"
)

// ErrHomeUnreachable: the home has not answered for longer than the grace period (or
// the credential is not cached and the home cannot be asked).
var ErrHomeUnreachable = errors.New("nodeauth: home unreachable")

type entry struct {
	p      *principal.Principal
	csrf   string
	user   string
	until  time.Time
	denied bool
}

// Resolver is the node's session resolver.
type Resolver struct {
	Link homelink.Link
	// Grace returns how long the home may not answer before the node stops asking it
	// per request (nil: DefaultGrace).
	Grace func() time.Duration
	// Local is this machine's copy of the home's database (nil: none).
	Local Local
	// Sessions are the node-local sign-ins (nil: this machine signs no one in).
	Sessions *LocalSessions
	// SignOuts are the sign-outs made here while the home did not answer (nil: none
	// are made here; a sign-out waits for the home).
	SignOuts *SignOuts
	// CopyMaxLag returns how far behind the home the copy may have been when the home
	// was last heard, for it to answer (nil: DefaultCopyMaxLag).
	CopyMaxLag func() time.Duration
	// HomeOffset is the home's clock minus this machine's (the copy's beat is stamped
	// on the home's); nil: 0.
	HomeOffset func() time.Duration
	// LocalSessionTTL and LocalSessionGrace return a node-local sign-in's lifetime and
	// how long it lasts once the home answers again (nil: the defaults).
	LocalSessionTTL   func() time.Duration
	LocalSessionGrace func() time.Duration
	// SecureCookies marks the node-local session's cookies Secure (HTTPS).
	SecureCookies bool
	Now           func() time.Time
	Logger        *slog.Logger

	mu      sync.Mutex
	cache   map[string]*entry
	failing time.Time // first unanswered call of the current outage; zero: the home answers
	lastOK  time.Time // the home's last answer (kept across restarts: KeepHomeSeen)
	fails   map[string][]time.Time
	// gen moves at every revocation: an answer asked for before it is not cached
	gen uint64
	// revoked: what the home's revocations named lately (a session's key, or "u:"+user),
	// when; the copy's answers honour them until it has them too
	revoked map[string]time.Time

	jmu      sync.Mutex
	jwks     map[string]*rsa.PublicKey
	jwksAt   time.Time
	jwksMiss time.Time
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Resolver) grace() time.Duration {
	if r.Grace != nil {
		if g := r.Grace(); g > 0 {
			return g
		}
	}
	return DefaultGrace
}

func (r *Resolver) copyMaxLag() time.Duration {
	if r.CopyMaxLag != nil {
		if d := r.CopyMaxLag(); d > 0 {
			return d
		}
	}
	return DefaultCopyMaxLag
}

func readOnly(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// SessionKey is the cache key of a session id: SHA-256 hex (what auth.revoked names).
func SessionKey(sid string) string {
	s := sha256.Sum256([]byte(sid))
	return hex.EncodeToString(s[:])
}

func patKey(tok string) string {
	s := sha256.Sum256([]byte("pat\x00" + tok))
	return "p:" + hex.EncodeToString(s[:])
}

// CredentialKey is a key for the request's credential ("" for none): the cache key of a
// session or a token, a bearer access token's own hash.
func CredentialKey(req *http.Request) string {
	if b := bearer(req); b != "" {
		if strings.HasPrefix(b, "pat_") {
			return patKey(b)
		}
		s := sha256.Sum256([]byte("jwt\x00" + b))
		return "j:" + hex.EncodeToString(s[:])
	}
	if c, err := req.Cookie(CookieSession); err == nil && c.Value != "" {
		return SessionKey(c.Value)
	}
	return ""
}

// answered records that the home answered (a yes or a no). The node-local sign-ins
// end within the grace from now on (they were for the home's absence).
func (r *Resolver) answered() {
	now := r.now()
	r.mu.Lock()
	r.failing = time.Time{}
	r.lastOK = now
	r.mu.Unlock()
	if r.Sessions != nil {
		if err := r.Sessions.Deadline(now.Add(r.localGrace())); err != nil && r.Logger != nil {
			r.Logger.Warn("node: the node-local sign-ins could not be shortened", "err", err,
				"event", "node_local_sessions_failed")
		}
	}
}

// unanswered records a call the home did not answer.
func (r *Resolver) unanswered() {
	r.mu.Lock()
	if r.failing.IsZero() {
		r.failing = r.now()
		if r.Logger != nil {
			r.Logger.Warn("node: the home does not answer; read-only, on this machine's copy of its database",
				"grace", r.grace().String(), "copy", r.Local != nil, "event", "node_home_unreachable")
		}
	}
	r.mu.Unlock()
}

// Unreachable reports whether the home has failed to answer for longer than the
// grace period (then the node no longer asks it per request: a credential holds only
// under the offline rules, and `home` routes are refused at once).
func (r *Resolver) Unreachable() bool {
	r.mu.Lock()
	failing := r.failing
	r.mu.Unlock()
	// the grace is read outside the lock: it reads the rules, which may wait on a
	// database
	return !failing.IsZero() && r.now().Sub(failing) > r.grace()
}

// away reports whether the home does not answer now (from the first call it did not
// answer): a node-local session is one, made while the home was away (the sign-in
// answered here from DownFor(0)), and stays this machine's until the home answers again.
func (r *Resolver) away() bool { return r.DownFor(0) }

// DownFor reports whether the home has not answered for at least d.
func (r *Resolver) DownFor(d time.Duration) bool {
	r.mu.Lock()
	failing := r.failing
	r.mu.Unlock()
	return !failing.IsZero() && r.now().Sub(failing) >= d
}

// Down reports whether the home has not answered for a probe's interval: what other
// machines and the viewers see (/api/cluster/self, live TV, the copy's reads) does not
// move on one call it did not answer (the next probe tells).
func (r *Resolver) Down() bool { return r.DownFor(ProbeEvery) }

// Reachability is for /api/cluster/self: whether the home answers and since when not
// (Down: one call it did not answer is not reported).
func (r *Resolver) Reachability() (ok bool, failingSince *time.Time) {
	if !r.Down() {
		return true, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failing.IsZero() {
		return true, nil
	}
	t := r.failing
	return false, &t
}

// Watch probes the home every 5 s so an outage is noticed without a cache miss.
func (r *Resolver) Watch(ctx context.Context) {
	t := time.NewTicker(ProbeEvery)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		_, err := r.Link.Self(pctx)
		cancel()
		switch {
		case err == nil || errors.Is(err, homelink.ErrUnauthorized):
			r.answered()
			r.sendSignOuts(ctx)
		case ctx.Err() == nil:
			r.unanswered()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Evict drops what a revocation names (story 35.9 AC4). An answer the home gave before
// it and not cached yet is not cached (gen), and the copy's answers honour it until the
// copy has it too (revoked).
func (r *Resolver) Evict(rv delegation.Revoked) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	now := r.now()
	if r.revoked == nil {
		r.revoked = map[string]time.Time{}
	}
	for k, t := range r.revoked {
		if now.Sub(t) > revokedKeep {
			delete(r.revoked, k)
		}
	}
	for _, h := range rv.SessionSHA256 {
		r.revoked[h] = now
	}
	if rv.UserID != "" {
		r.revoked["u:"+rv.UserID] = now
	}
	n := 0
	for _, h := range rv.SessionSHA256 {
		if _, ok := r.cache[h]; ok {
			delete(r.cache, h)
			n++
		}
	}
	if rv.UserID != "" {
		for k, e := range r.cache {
			if e.user == rv.UserID {
				delete(r.cache, k)
				n++
			}
		}
	}
	return n
}

// OnEvent wires the resolver to the link's pushed events.
func (r *Resolver) OnEvent(e homelink.Event) {
	if e.Kind == "revoked" && e.Revoked != nil {
		n := r.Evict(*e.Revoked)
		// a person signed out everywhere, reset or locked: their sign-ins on this
		// machine end too
		if e.Revoked.UserID != "" && r.Sessions != nil {
			if err := r.Sessions.EndUser(e.Revoked.UserID); err != nil && r.Logger != nil {
				r.Logger.Warn("node: a revoked person's sign-ins on this machine could not be ended", "err", err,
					"user_id", e.Revoked.UserID, "event", "node_local_sessions_failed")
			}
		}
		if r.Logger != nil {
			r.Logger.Info("node: revocation from the home applied", "evicted", n, "user_id", e.Revoked.UserID,
				"sessions", len(e.Revoked.SessionSHA256), "event", "node_auth_revoked")
		}
	}
}

// revokedKeep is how long a pushed revocation is honoured on the copy's answers: the
// copy applies the home's changes within moments.
const revokedKeep = 5 * time.Minute

// revokedLately reports whether a pushed revocation named the session (key) or its user
// lately.
func (r *Resolver) revokedLately(key, user string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if t, ok := r.revoked[key]; ok && now.Sub(t) <= revokedKeep {
		return true
	}
	t, ok := r.revoked["u:"+user]
	return ok && user != "" && now.Sub(t) <= revokedKeep
}

// cached is a credential's cached answer even past its lifetime (whose it was).
func (r *Resolver) cached(key string) (*entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[key]
	return e, ok
}

func (r *Resolver) get(key string) (*entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[key]
	if !ok || !r.now().Before(e.until) {
		return nil, false
	}
	return e, true
}

// put caches an answer asked for at generation gen, unless a revocation came since.
func (r *Resolver) put(key string, e *entry, gen uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gen != r.gen {
		return
	}
	if r.cache == nil {
		r.cache = map[string]*entry{}
	}
	if len(r.cache) >= maxEntries {
		now := r.now()
		for k, v := range r.cache {
			if !now.Before(v.until) {
				delete(r.cache, k)
			}
		}
		if len(r.cache) >= maxEntries {
			r.cache = map[string]*entry{}
		}
	}
	r.cache[key] = e
}

func toPrincipal(a *delegation.IntrospectAnswer) *principal.Principal {
	src := principal.SourceCookie
	if a.Source == string(principal.SourcePAT) {
		src = principal.SourcePAT
	}
	return &principal.Principal{UserID: a.Principal.UserID, IsAdmin: a.Principal.IsAdmin,
		Libraries: a.Principal.Libraries, AccessAllLibraries: a.Principal.AccessAllLibraries || a.Principal.IsAdmin,
		Source: src, MustChangePassword: a.Principal.MustChangePassword && src == principal.SourceCookie}
}

// resolve answers one credential: (principal, csrf hash) or nil for "not signed in".
func (r *Resolver) resolve(ctx context.Context, key string, req delegation.IntrospectRequest) (*principal.Principal, string, error) {
	if r.Unreachable() {
		return nil, "", ErrHomeUnreachable
	}
	if e, ok := r.get(key); ok {
		if e.denied {
			return nil, "", nil
		}
		return e.p, e.csrf, nil
	}
	r.mu.Lock()
	gen := r.gen
	r.mu.Unlock()
	a, err := r.Link.Introspect(ctx, req)
	switch {
	case errors.Is(err, homelink.ErrUnauthorized):
		r.answered()
		r.put(key, &entry{denied: true, until: r.now().Add(NegativeTTL)}, gen)
		return nil, "", nil
	case err != nil:
		// only the home not answering counts: not the caller going away, nor an answer
		// the call did not expect
		if ctx.Err() == nil && errors.Is(err, homelink.ErrUnreachable) {
			r.unanswered()
		}
		return nil, "", ErrHomeUnreachable
	}
	r.answered()
	now := r.now()
	until := now.Add(PositiveTTL)
	if !a.ExpiresAt.IsZero() && a.ExpiresAt.Before(until) {
		until = a.ExpiresAt
	}
	p := toPrincipal(a)
	if p.MustChangePassword {
		// a must-change answer is never reused: the next request asks the home
		// again, so it is let through the moment the password is changed even if
		// the home's revocation push is lost
		return p, a.CSRFSHA256, nil
	}
	r.put(key, &entry{p: p, csrf: a.CSRFSHA256, user: a.Principal.UserID, until: until}, gen)
	return p, a.CSRFSHA256, nil
}

// ---------- bearer access tokens: the home's JWKS ----------

func (r *Resolver) jwksKey(ctx context.Context, kid string) *rsa.PublicKey {
	r.jmu.Lock()
	defer r.jmu.Unlock()
	now := r.now()
	if k, ok := r.jwks[kid]; ok && now.Sub(r.jwksAt) < JWKSTTL {
		return k
	}
	// refetch when stale, or on an unknown kid (at most every 10 s)
	if now.Sub(r.jwksAt) < JWKSTTL && now.Sub(r.jwksMiss) < 10*time.Second {
		return r.jwks[kid]
	}
	r.jwksMiss = now
	raw, err := r.Link.JWKS(ctx)
	if err != nil {
		return r.jwks[kid]
	}
	var doc struct {
		Keys []struct {
			Kty, Kid, N, E string
		} `json:"keys"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return r.jwks[kid]
	}
	m := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		m[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	r.jwks, r.jwksAt = m, now
	return m[kid]
}

func jwtKID(tok string) string {
	h, _, ok := strings.Cut(tok, ".")
	if !ok {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(h)
	if err != nil {
		return ""
	}
	var hdr struct {
		Kid string `json:"kid"`
	}
	_ = json.Unmarshal(b, &hdr)
	return hdr.Kid
}

func (r *Resolver) verifyJWT(ctx context.Context, tok string) *principal.Principal {
	kid := jwtKID(tok)
	if kid == "" {
		return nil
	}
	pub := r.jwksKey(ctx, kid)
	if pub == nil {
		return nil
	}
	set := keys.NewSet(0)
	set.Replace(&keys.Key{KID: kid, Public: pub})
	c, err := jwt.Verify(set, tok, "api")
	if err != nil {
		return nil
	}
	return &principal.Principal{UserID: c.Usr, IsAdmin: c.IsAdmin, Libraries: c.Lib,
		AccessAllLibraries: c.IsAdmin, Source: principal.SourceJWT}
}

// ---------- middleware ----------

type csrfKey struct{}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) > 7 && strings.EqualFold(v[:7], "Bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return ""
}

// Middleware attaches the principal to a request the node serves itself (`local` and
// `any` routes): the home's answer, or with the home away this machine's copy's
// (see the package doc). Anonymous requests pass through (the auth gate behind it
// decides).
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if principal.FromContext(req.Context()) != nil {
			next.ServeHTTP(w, req)
			return
		}
		local := placement.ClassFrom(req.Context()) == placement.Local
		if id, ok := isLocalSession(req); ok {
			p, csrf, ok := r.localSession(req.Context(), id)
			switch {
			case !ok:
				// ended, or of another machine: anonymous, and the cookie cleared
				r.setCookies(w, "", "", time.Time{})
				next.ServeHTTP(w, req)
				return
			case local:
			case !r.away() && !readOnly(req.Method):
				// a change through the home's database, the home back: it knows nothing of
				// this sign-in, so it asks for one there
				r.setCookies(w, "", "", time.Time{})
				writeSignInAgain(w)
				return
			case !readOnly(req.Method):
				writeUnreachable(w, true)
				return
			}
			if r.away() {
				w.Header().Set(HomeStateHeader, "unreachable")
			}
			serveAs(next, w, req, p, csrf)
			return
		}
		var (
			p    *principal.Principal
			csrf string
			ireq delegation.IntrospectRequest
			err  error
			jwt  bool
		)
		if b := bearer(req); b != "" {
			if strings.HasPrefix(b, "pat_") {
				ireq.PAT = b
				p, _, err = r.resolve(req.Context(), patKey(b), ireq)
			} else {
				p, jwt = r.verifyJWT(req.Context(), b), true
				if p != nil && r.Unreachable() {
					// the home cannot be asked whether the user is still allowed in:
					// the copy is, under the same rules as any other credential
					err = ErrHomeUnreachable
				}
			}
		} else if c, cerr := req.Cookie(CookieSession); cerr == nil && c.Value != "" {
			ireq.Session = c.Value
			p, csrf, err = r.resolve(req.Context(), SessionKey(c.Value), ireq)
		}
		if err != nil {
			var said bool
			if jwt {
				p, said = r.jwtFromCopy(req.Context(), p)
			} else {
				p, csrf, said = r.fromCopy(req.Context(), ireq)
			}
			switch {
			case said && p != nil && !local && !readOnly(req.Method):
				// a change through the home's database waits for the home
				writeUnreachable(w, true)
				return
			case said || local:
				// the copy's answer (a credential it says is not live is anonymous, as
				// one the home refused), or this machine's own routes, which keep
				// answering with the home gone (design §10.3), anonymously
				w.Header().Set(HomeStateHeader, "unreachable")
			default:
				// no copy to ask: this machine cannot serve this person until the home
				// answers
				writeUnreachable(w, !readOnly(req.Method))
				return
			}
		}
		if p != nil && r.deniedHere(req, p.UserID) {
			// signed out on this machine; the home is told (or was, a moment ago)
			p = nil
		}
		if p == nil {
			next.ServeHTTP(w, req)
			return
		}
		serveAs(next, w, req, p, csrf)
	})
}

func serveAs(next http.Handler, w http.ResponseWriter, req *http.Request, p *principal.Principal, csrf string) {
	ctx := principal.WithPrincipal(req.Context(), p)
	if csrf != "" {
		ctx = context.WithValue(ctx, csrfKey{}, csrf)
	}
	next.ServeHTTP(w, req.WithContext(ctx))
}

// MePath is the session probe the web app makes on every load.
const MePath = "/api/auth/me"

// Me answers GET /api/auth/me on the node (false: the request is another one) for a
// node-local session always, and for any other credential while the home does not
// answer it: from this machine's copy, shaped as the home's own handler shapes it
// (session "node" for a node-local one), so a reload keeps a signed-in person in the
// app. Without a credential, or with one the copy says is not live, it is a 401 (the
// app shows its sign-in, which this machine answers); with no copy to ask, a 503
// home_unreachable with readable false (this machine cannot serve that person).
func (r *Resolver) Me(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path != MePath || !readOnly(req.Method) {
		return false
	}
	if r.Unreachable() {
		w.Header().Set(HomeStateHeader, "unreachable")
	}
	var (
		dp    *delegation.Principal
		scope = "home"
	)
	if id, ok := isLocalSession(req); ok {
		scope = "node"
		if d, _, ok := r.localPrincipal(req.Context(), id); ok {
			dp = d
		}
	} else {
		w.Header().Set(HomeStateHeader, "unreachable")
		ireq := delegation.IntrospectRequest{}
		if b := bearer(req); strings.HasPrefix(b, "pat_") {
			ireq.PAT = b
		} else if c, err := req.Cookie(CookieSession); err == nil && c.Value != "" && b == "" {
			ireq.Session = c.Value
		}
		if ireq.Session != "" || ireq.PAT != "" {
			a, said := r.copyAnswer(req.Context(), ireq)
			if !said {
				writeUnreachable(w, false)
				return true
			}
			if a != nil && !r.deniedHere(req, a.Principal.UserID) {
				d := a.Principal
				d.MustChangePassword = d.MustChangePassword && ireq.Session != ""
				dp = &d
			}
		}
	}
	if dp == nil {
		writeProblem(w, http.StatusUnauthorized, "https://maktaba.dev/problems/unauthorized", "unauthorized",
			"authentication required")
		return true
	}
	libs := dp.Libraries
	if libs == nil || dp.IsAdmin {
		libs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if req.Method == http.MethodHead {
		return true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": dp.UserID, "is_admin": dp.IsAdmin,
		"libraries": libs, "id": dp.UserID, "username": dp.Username, "session": scope,
		"user": map[string]any{"id": dp.UserID, "username": dp.Username, "is_admin": dp.IsAdmin,
			"must_change_password": dp.MustChangePassword}})
	return true
}

// Home fronts the proxy to the home for a node's `home` routes. A node-local session
// never goes to the home (it knows nothing of it): this machine answers its session
// probe and sign-out; a sign-in with the home, once it answers, ends the node-local one
// and goes to the home without it; any other `home` route asks for a sign-in with the
// home and clears the node-local cookie (or, with the home away, gets 503
// home_unreachable). Once the home has not answered
// for the grace period it is not tried per request: the session probe and the sign-in
// are answered from this machine's copy (Me, SignIn), and every other `home` route
// gets 503 home_unreachable at once rather than waiting on a dead address.
func (r *Resolver) Home(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if id, ok := isLocalSession(req); ok {
			switch {
			case r.SignOut(w, req), r.Me(w, req):
			case r.away():
				writeUnreachable(w, true)
			case req.Method == http.MethodPost && req.URL.Path == LoginPath:
				// a sign-in with the home, now that it answers: this machine's own ends
				// and the home never sees it (it sets its own cookies)
				if r.Sessions != nil {
					_ = r.Sessions.End(id)
				}
				proxy.ServeHTTP(w, keepSignIn(withoutLocalSession(req)))
			default:
				r.setCookies(w, "", "", time.Time{})
				writeSignInAgain(w)
			}
			return
		}
		if r.signedOutHere(req) {
			// signed out on this machine and the home may not know yet: never sent there
			r.setCookies(w, "", "", time.Time{})
			if req.Method == http.MethodPost && (req.URL.Path == LogoutPath || req.URL.Path == LogoutAllPath) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeProblem(w, http.StatusUnauthorized, "https://maktaba.dev/problems/unauthorized", "unauthorized",
				"authentication required")
			return
		}
		if !r.Unreachable() {
			// the home not answering its probes (inside the grace): what this machine
			// can answer itself is answered here at once, rather than waiting on it
			// (from the first call it did not answer: a home switched off is not waited
			// on; the caller going away does not count, unanswered)
			if r.DownFor(0) && (r.Me(w, req) || r.SignIn(w, req) || r.SignOutHere(w, req)) {
				return
			}
			// a sign-in or a sign-out the home turns out not to answer is answered here
			// from the proxy's fallback (Fallback): a sign-in's body is kept for that
			proxy.ServeHTTP(w, keepSignIn(req))
			return
		}
		if !r.Me(w, req) && !r.SignIn(w, req) && !r.SignOutHere(w, req) {
			// a read: the full page when this machine cannot serve the person's reads
			// either (no copy, or one that was behind), else the notice
			writeUnreachable(w, !readOnly(req.Method) || r.CopyCurrent(req.Context()))
		}
	})
}

// signedOutHere reports whether the request's home session was signed out on this
// machine: by its key, or (while a "sign out everywhere" waits for the home) its user,
// as far as this machine knows it.
func (r *Resolver) signedOutHere(req *http.Request) bool {
	if r.SignOuts == nil || bearer(req) != "" {
		return false
	}
	c, err := req.Cookie(CookieSession)
	if err != nil || c.Value == "" {
		return false
	}
	user := ""
	if r.SignOuts.anyAll() {
		if e, ok := r.cached(SessionKey(c.Value)); ok && !e.denied {
			user = e.user
		} else if a, said := r.copyAnswer(req.Context(), delegation.IntrospectRequest{Session: c.Value}); said && a != nil {
			user = a.Principal.UserID
		}
	}
	return r.deniedHere(req, user)
}

// Fallback answers what the home proxy could not reach the home for (it did not
// answer, or not in time) and this machine can answer itself
// (homeproxy.NewWithFallback): the session probe and the sign-in, from its copy, and the
// sign-out.
func (r *Resolver) Fallback(w http.ResponseWriter, req *http.Request) bool {
	return r.Me(w, req) || r.SignIn(w, req) || r.SignOutHere(w, req)
}

// withoutLocalSession is req without this machine's session cookies: a sign-in with the
// home starts afresh (the home would clear a cookie it does not know, and refuse its CSRF
// token).
func withoutLocalSession(req *http.Request) *http.Request {
	out := req.Clone(req.Context())
	out.Header.Del("Cookie")
	for _, c := range req.Cookies() {
		if c.Name != CookieSession && c.Name != CookieCSRF {
			out.AddCookie(c)
		}
	}
	return out
}

type signInBodyKey struct{}

// keepSignIn keeps a sign-in's body (it is small) for SignIn, since the proxy may have
// read the request's own before the home failed to answer.
func keepSignIn(req *http.Request) *http.Request {
	if req.URL.Path != LoginPath || req.Method != http.MethodPost || req.Body == nil {
		return req
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 8<<10+1))
	_ = req.Body.Close()
	if err != nil {
		body = nil
	}
	out := req.WithContext(context.WithValue(req.Context(), signInBodyKey{}, body))
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return out
}

// writeSignInAgain answers 401 {code: signin_required}: a node-local sign-in does not
// reach the home; the app asks for a sign-in (with the home, now that it answers).
func writeSignInAgain(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.dev/problems/signin-required",
		"title": "sign in again", "status": http.StatusUnauthorized, "code": "signin_required",
		"detail": "you signed in on this machine while the home was away; sign in again to make changes"})
}

// CSRF is the node's double-submit guard for cookie sessions (story 35.9 AC5): on a
// mutating request the X-Maktaba-CSRF header must equal the mkt_csrf cookie and hash to
// the session's csrf_sha256 the home answered.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		p := principal.FromContext(r.Context())
		if p == nil || p.Source != principal.SourceCookie || r.Header.Get("Authorization") != "" {
			next.ServeHTTP(w, r)
			return
		}
		want, _ := r.Context().Value(csrfKey{}).(string)
		header := r.Header.Get(CSRFHeader)
		c, err := r.Cookie(CookieCSRF)
		sum := sha256.Sum256([]byte(header))
		if header == "" || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(header)) != 1 ||
			subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(want)) != 1 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.dev/problems/csrf-mismatch",
				"title": "csrf token mismatch", "status": http.StatusForbidden})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteUnreachable answers 503 {code: home_unreachable}.
func WriteUnreachable(w http.ResponseWriter) { writeUnreachable(w, true) }

// writeUnreachable: readable false adds {readable: false}, which says this machine
// cannot serve the person's reads either, so the app swaps itself for the full "cannot
// reach its home" page (a refused change keeps it under the notice).
func writeUnreachable(w http.ResponseWriter, readable bool) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusServiceUnavailable)
	body := map[string]any{"type": "https://maktaba.dev/problems/home-unreachable",
		"title": "the home is not reachable", "status": http.StatusServiceUnavailable, "code": "home_unreachable",
		"detail": "this machine cannot reach its home to check the sign-in; it tries again"}
	if !readable {
		body["readable"] = false
	}
	_ = json.NewEncoder(w).Encode(body)
}
