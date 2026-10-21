// Package nodeauth resolves who is asking on a full node (Epic 35 story 35.9, design
// §10.3). A node never reads the sessions table: it asks the home (homelink.Link,
// signed), caches a good answer 30 s and a refusal 5 s, keyed by the SHA-256 of the
// credential, and drops cached answers the moment the home pushes a revocation.
//
// A home outage degrades a node to read-only instead of taking it down. A credential
// the home cannot be asked about is still honoured on the home's last answer while
// that answer holds offline: the credential's own expiry (as the home named it) has not
// passed, the home gave the answer no earlier than the offline window before the
// outage began, and the outage is younger than the window
// (rules.cluster.offline_read_minutes, 0: never). Then a read (GET, HEAD) and this
// machine's own routes (`local`: its status, playback) are served, marked
// X-Maktaba-Home: unreachable so the app shows it. Anything else gets 503
// home_unreachable — a change, a read the last answer no longer covers — except a
// `local` route, which answers anonymously (its auth gate decides). Nothing signs in
// meanwhile: sign-in is the home's. The answers are kept on the node's disk too (Keep),
// so a node restarted in the outage serves the same people. Once the home has not answered for the grace
// period (30 s, rules.cluster.delegate_unreachable_grace_seconds) the node stops
// asking it per request, and `home` routes are refused at once (Home).
//
// The principal it attaches is the home's answer; the node then runs the same
// middleware and handlers as the home (roles, admin checks, library ACLs, CSRF double
// submit against the session's CSRF token hash).
package nodeauth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	// DefaultOfflineRead: how long reads stay served on the last confirmed answer.
	DefaultOfflineRead = 10 * time.Minute
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
	name   string // the username (an offline /api/auth/me)
	until  time.Time
	denied bool
	// for the offline rules: when the home gave this answer, and the credential's own
	// expiry as the home named it (zero: it named none, and the answer never holds offline)
	confirmed time.Time
	expires   time.Time
}

// Resolver is the node's session resolver.
type Resolver struct {
	Link homelink.Link
	// Grace returns how long the home may not answer before the node stops asking it
	// per request (nil: DefaultGrace).
	Grace func() time.Duration
	// OfflineRead returns the offline window: how long the home's last answers hold
	// once it stops answering (nil: DefaultOfflineRead; 0: not at all).
	OfflineRead func() time.Duration
	Now         func() time.Time
	Logger      *slog.Logger

	mu      sync.Mutex
	cache   map[string]*entry
	failing time.Time // first unanswered call of the current outage; zero: the home answers
	lastOK  time.Time

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

func (r *Resolver) offlineRead() time.Duration {
	if r.OfflineRead != nil {
		return r.OfflineRead()
	}
	return DefaultOfflineRead
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

// answered records that the home answered (a yes or a no).
func (r *Resolver) answered() {
	r.mu.Lock()
	r.failing = time.Time{}
	r.lastOK = r.now()
	r.mu.Unlock()
}

// unanswered records a call the home did not answer.
func (r *Resolver) unanswered() {
	r.mu.Lock()
	if r.failing.IsZero() {
		r.failing = r.now()
		if r.Logger != nil {
			r.Logger.Warn("node: the home does not answer; read-only on its last answers for the offline window",
				"grace", r.grace().String(), "offline_window", r.offlineRead().String(), "event", "node_home_unreachable")
		}
	}
	r.mu.Unlock()
}

// Unreachable reports whether the home has failed to answer for longer than the
// grace period (then the node no longer asks it per request: a credential holds only
// under the offline rules, and `home` routes are refused at once).
func (r *Resolver) Unreachable() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.failing.IsZero() && r.now().Sub(r.failing) > r.grace()
}

// Reachability is for /api/cluster/self: whether the home answers and since when not.
func (r *Resolver) Reachability() (ok bool, failingSince *time.Time) {
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

// Evict drops what a revocation names (story 35.9 AC4).
func (r *Resolver) Evict(rv delegation.Revoked) int {
	r.mu.Lock()
	defer r.mu.Unlock()
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
		if r.Logger != nil {
			r.Logger.Info("node: revocation from the home applied", "evicted", n, "user_id", e.Revoked.UserID,
				"sessions", len(e.Revoked.SessionSHA256), "event", "node_auth_revoked")
		}
	}
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

func (r *Resolver) put(key string, e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	a, err := r.Link.Introspect(ctx, req)
	switch {
	case errors.Is(err, homelink.ErrUnauthorized):
		r.answered()
		r.put(key, &entry{denied: true, until: r.now().Add(NegativeTTL)})
		return nil, "", nil
	case err != nil:
		r.unanswered()
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
		// the home's revocation push is lost (the entry stays for the offline rules)
		until = now
	}
	r.put(key, &entry{p: p, csrf: a.CSRFSHA256, user: a.Principal.UserID, name: a.Principal.Username,
		until: until, confirmed: now, expires: a.ExpiresAt})
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

// Middleware attaches the home-resolved principal to a request the node serves itself
// (`local` and `any` routes). Anonymous requests pass through (the auth gate behind
// it decides); a credential the home cannot be asked about is served on the home's
// last answer while it holds offline, else a 503 home_unreachable (see the package doc).
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if principal.FromContext(req.Context()) != nil {
			next.ServeHTTP(w, req)
			return
		}
		var (
			p    *principal.Principal
			csrf string
			key  string
			err  error
		)
		if b := bearer(req); b != "" {
			if strings.HasPrefix(b, "pat_") {
				key = patKey(b)
				p, _, err = r.resolve(req.Context(), key, delegation.IntrospectRequest{PAT: b})
			} else {
				p = r.verifyJWT(req.Context(), b)
			}
		} else if c, cerr := req.Cookie(CookieSession); cerr == nil && c.Value != "" {
			key = SessionKey(c.Value)
			p, csrf, err = r.resolve(req.Context(), key, delegation.IntrospectRequest{Session: c.Value})
		}
		if err != nil {
			local := placement.ClassFrom(req.Context()) == placement.Local
			e := r.offline(key)
			switch {
			case e != nil && (local || readOnly(req.Method)):
				// the home's last answer still holds: a read, or this machine's own
				// route (playback included)
				p, csrf = e.p, e.csrf
				w.Header().Set(HomeStateHeader, "unreachable")
			case local:
				// this machine's own routes keep answering with the home gone (design
				// §10.3), anonymously
				w.Header().Set(HomeStateHeader, "unreachable")
			default:
				// a change, or a read the last answer no longer covers: this machine
				// cannot serve this person until the home answers
				writeUnreachable(w, !readOnly(req.Method))
				return
			}
		}
		if p == nil {
			next.ServeHTTP(w, req)
			return
		}
		ctx := principal.WithPrincipal(req.Context(), p)
		if csrf != "" {
			ctx = context.WithValue(ctx, csrfKey{}, csrf)
		}
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// offline is the home's last answer for a credential key while it holds with the home
// not answering, else nil: the credential's expiry has not passed, the home gave the
// answer no earlier than the offline window before the outage began (so neither an
// answer from days ago nor one a lost revocation push left behind long ago is used),
// and the outage is younger than the window. A revocation the home pushed has already
// dropped it (Evict); a refusal it gave is never used.
func (r *Resolver) offline(key string) *entry {
	window := r.offlineRead()
	if key == "" || window <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	start := r.failing
	if start.IsZero() || start.After(now) {
		start = now // the home's first unanswered call is this one
	}
	// the outage began after the home's last answer: for a node that restarted in it
	// (and never saw the home fail) that is the one it kept (Keep)
	if !r.lastOK.IsZero() && r.lastOK.Before(start) {
		start = r.lastOK
	}
	e, ok := r.cache[key]
	switch {
	case !ok || e.denied || e.p == nil:
		return nil
	case now.Sub(start) > window:
		return nil
	case !now.Before(e.expires):
		return nil
	case e.confirmed.Before(start.Add(-max(window, PositiveTTL))):
		return nil
	}
	return e
}

// credentialKey is the cache key of the request's credential ("" for none, and for a
// bearer access token, which the node verifies on its own).
func credentialKey(req *http.Request) string {
	if b := bearer(req); b != "" {
		if strings.HasPrefix(b, "pat_") {
			return patKey(b)
		}
		return ""
	}
	if c, err := req.Cookie(CookieSession); err == nil && c.Value != "" {
		return SessionKey(c.Value)
	}
	return ""
}

// MePath is the session probe the web app makes on every load.
const MePath = "/api/auth/me"

// Me answers GET /api/auth/me on the node while the home does not answer it (false: the
// request is another one): the home's last answer for the credential under the offline
// rules, shaped as the home's own handler shapes it, so a reload keeps a signed-in
// person in the app. Without a credential it is a 401 (signing in waits for the home);
// a credential those rules do not cover is a 503 home_unreachable with readable false
// (this machine cannot serve that person). Marked X-Maktaba-Home: unreachable.
func (r *Resolver) Me(w http.ResponseWriter, req *http.Request) bool {
	if req.URL.Path != MePath || !readOnly(req.Method) {
		return false
	}
	w.Header().Set(HomeStateHeader, "unreachable")
	if bearer(req) == "" {
		if c, err := req.Cookie(CookieSession); err != nil || c.Value == "" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "https://maktaba.dev/problems/unauthorized",
				"title": "unauthorized", "status": http.StatusUnauthorized, "detail": "authentication required"})
			return true
		}
	}
	e := r.offline(credentialKey(req))
	if e == nil {
		writeUnreachable(w, false)
		return true
	}
	libs := e.p.Libraries
	if libs == nil {
		libs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if req.Method == http.MethodHead {
		return true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"user_id": e.p.UserID, "is_admin": e.p.IsAdmin,
		"libraries": libs, "id": e.p.UserID, "username": e.name,
		"user": map[string]any{"id": e.p.UserID, "username": e.name, "is_admin": e.p.IsAdmin,
			"must_change_password": e.p.MustChangePassword}})
	return true
}

// Home fronts the proxy to the home for a node's `home` routes. Once the home has not
// answered for the grace period it is not tried per request: GET /api/auth/me answers
// from the home's last answer (Me) and every other `home` route — a sign-in, a sign-out,
// a change — gets 503 home_unreachable at once rather than waiting on a dead address.
func (r *Resolver) Home(proxy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !r.Unreachable() {
			proxy.ServeHTTP(w, req)
			return
		}
		if !r.Me(w, req) {
			writeUnreachable(w, true)
		}
	})
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
