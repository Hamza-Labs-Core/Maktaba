package parental

// The gate: one middleware inside the API's router that makes the filter hold on
// every route (Epic 57 story 57.03). REST, the GraphQL bridge's sub-requests and a
// node's forwarded requests all pass it. For each request it finds the route's
// entry in the surface registry and, for a restricted account:
//
//   - Open, Filtered: the handler runs, with the account's limit in the context;
//   - ByID: the object in the path is checked first; a hidden one is served as a
//     fresh random id, so the answer is the handler's own for a missing object;
//   - Refuse: 403 `restricted`;
//   - Closed, or a route no entry names: the answer of a route that does not exist.
//
// It fails closed: a policy that cannot be read refuses the request, and a gate
// with no cache yet refuses every signed-in user.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// TypeRestricted is the problem type of an action a restricted account may not
// take (the same as handlers/parental's).
var TypeRestricted = httperror.Type("restricted")

// RestrictedProblem is the 403 a Refuse route answers a restricted account.
func RestrictedProblem() *httperror.Error {
	return httperror.Forbidden(TypeRestricted, "a restricted account cannot do this").With("code", "restricted")
}

// ErrNoCache is the gate's answer before a cache is set.
var ErrNoCache = errors.New("parental: the gate has no policy cache")

// Gate is the middleware and the one place a request's limit is worked out.
type Gate struct {
	cache atomic.Pointer[Cache]
	// Table is the registry the gate acts on (Surfaces unless a test sets another).
	Table SurfaceTable
}

// NewGate is a gate over the API's registry, with no cache yet: until SetCache it
// refuses every signed-in user (fail closed).
func NewGate() *Gate { return &Gate{Table: Surfaces} }

// SetCache gives the gate the restricted accounts' cache (MountParental's).
func (g *Gate) SetCache(c *Cache) { g.cache.Store(c) }

// Cache is the gate's cache (nil before SetCache).
func (g *Gate) Cache() *Cache { return g.cache.Load() }

// Limit is the request's effective limit: the principal's policy and rules. This
// is the one place a lower-trust input (the sign-in's device signal, story 57.04)
// tightens it. No principal, or one with no user (the admin token, a machine), has
// no limit.
func (g *Gate) Limit(ctx context.Context, p *principal.Principal) (*Effective, error) {
	if p == nil || p.UserID == "" || !isUUID(p.UserID) {
		return unrestricted, nil
	}
	c := g.cache.Load()
	if c == nil {
		return nil, ErrNoCache
	}
	e, err := c.For(ctx, p)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return nil, errors.New("parental: no effective policy")
	}
	return e, nil
}

// noRoute is a path nothing is mounted on: serving it gives the router's own
// answer for a route that does not exist.
const noRoute = "/api/.none"

// Middleware is the gate for the router whose routes are given (the router it is
// installed in).
func (g *Gate) Middleware(routes chi.Routes) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := principal.FromContext(r.Context())
			if p == nil || p.UserID == "" || !isUUID(p.UserID) {
				next.ServeHTTP(w, r)
				return
			}
			method := r.Method
			if method == http.MethodHead {
				method = http.MethodGet
			}
			rctx := chi.NewRouteContext()
			pattern := routes.Find(rctx, method, routePath(r))
			if pattern == "" {
				next.ServeHTTP(w, r) // no such route: the router's own 404 or 405
				return
			}
			s, known := g.Table.Lookup(method, pattern)
			e, err := g.Limit(r.Context(), p)
			if err != nil {
				// the policy could not be read: nothing is shown, and a route that
				// needs none still works (signing in, health)
				if known && s.Class == Open {
					next.ServeHTTP(w, r.WithContext(With(r.Context(), nothing(p.UserID))))
					return
				}
				httperror.Write(w, r, httperror.Unavailable(5))
				return
			}
			ctx := With(r.Context(), e)
			if e.Limits() {
				// a blocked library is one the account has no access to, for
				// every library check of every handler
				np, err := g.narrow(ctx, p, e)
				if err != nil {
					httperror.Write(w, r, httperror.Unavailable(5))
					return
				}
				ctx = principal.WithPrincipal(ctx, np)
			}
			r = r.WithContext(ctx)
			if !e.Limits() {
				next.ServeHTTP(w, r)
				return
			}
			if !known {
				next.ServeHTTP(w, reroute(r, noRoute))
				return
			}
			switch s.Class {
			case Open, Filtered:
				next.ServeHTTP(w, r)
			case ByID:
				id := rctx.URLParam(s.Param)
				ok, err := g.allowed(ctx, e, s.Kind, id)
				if err == nil && ok {
					next.ServeHTTP(w, r)
					return
				}
				Refused(ctx, e, Subject{Kind: s.Kind, ID: id})
				stand := uuid.NewString()
				hidden, swapped := swapParam(routePath(r), pattern, s.Param, stand)
				if !swapped {
					next.ServeHTTP(w, reroute(r, noRoute))
					return
				}
				if r.Header.Get("Upgrade") != "" {
					next.ServeHTTP(w, reroute(r, hidden))
					return
				}
				// the handler answers for the stand-in id; the answer goes out
				// naming the id that was asked, as a missing one's would
				ew := &echoWriter{ResponseWriter: w, from: stand, to: id}
				next.ServeHTTP(ew, reroute(r, hidden))
				ew.flush()
			case Refuse:
				httperror.Write(w, r, RestrictedProblem())
			default: // Closed, and any class this switch does not know
				next.ServeHTTP(w, reroute(r, noRoute))
			}
		})
	}
}

func (g *Gate) allowed(ctx context.Context, e *Effective, kind, id string) (bool, error) {
	c := g.cache.Load()
	if c == nil {
		return false, ErrNoCache
	}
	return Allowed(ctx, c.Store().DB, e, kind, id)
}

// routePath is the path chi routes r by.
func routePath(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePath != "" {
		return rctx.RoutePath
	}
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.Path
}

// reroute is r asking for path instead (the query, headers and body unchanged).
func reroute(r *http.Request, path string) *http.Request {
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	if rctx := chi.RouteContext(r2.Context()); rctx != nil && rctx.RoutePath != "" {
		// a sub-router routes by RoutePath: give it a context of its own
		fresh := chi.NewRouteContext()
		fresh.RoutePath = path
		r2 = r2.WithContext(context.WithValue(r2.Context(), chi.RouteCtxKey, fresh))
	}
	return r2
}

// swapParam is path with the segment that pattern's {param} names replaced by
// value, and whether it found the segment.
func swapParam(path, pattern, param, value string) (string, bool) {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(segs) {
		return "", false
	}
	found := false
	for i, seg := range ps {
		if seg == "{"+param+"}" || strings.HasPrefix(seg, "{"+param+":") {
			segs[i] = value
			found = true
		}
	}
	if !found {
		return "", false
	}
	return "/" + strings.Join(segs, "/"), true
}

// narrow is p without the libraries e's live block rules name: a copy, so every
// handler's own library check hides a blocked library as one the account was never
// given. An account with access to every library gets the list of the others.
func (g *Gate) narrow(ctx context.Context, p *principal.Principal, e *Effective) (*principal.Principal, error) {
	blocked := map[string]bool{}
	for _, id := range e.Subjects(EffectBlock, SubjectLibrary, time.Now()) {
		if c, ok := CanonicalUUID(id); ok {
			blocked[c] = true
		}
	}
	if len(blocked) == 0 {
		return p, nil
	}
	libs := p.Libraries
	if p.AccessAllLibraries {
		c := g.cache.Load()
		if c == nil {
			return nil, ErrNoCache
		}
		rows, err := c.Store().DB.QueryContext(ctx, `SELECT CAST(id AS TEXT) FROM libraries`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		libs = nil
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			libs = append(libs, id)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	np := *p
	np.AccessAllLibraries = false
	np.Libraries = make([]string, 0, len(libs))
	for _, id := range libs {
		if c, ok := CanonicalUUID(id); ok && blocked[c] {
			continue
		}
		np.Libraries = append(np.Libraries, id)
	}
	return &np, nil
}

// echoWriter holds a hidden object's answer and sends it with the stand-in id
// replaced by the id that was asked, in the body and the headers. An event stream
// is not held: it opens at once, on the stand-in's channel, which nothing
// publishes to.
//
// The id asked comes from the request, so it is put back only when it is a UUID
// in one of its spellings, made of nothing but letters, digits, hyphens, colons and
// braces (askedID): the answer is the handler's own for one random UUID with
// another UUID in its place, and no other text of the request can reach a body or
// a header. Nothing else of the answer changes (no header is added, no type
// rewritten, the id keeps the spelling it was asked in): a hidden object's answer
// must stay the answer of a missing one, byte for byte.
type echoWriter struct {
	http.ResponseWriter
	from, to string
	code     int
	live     bool
	// ctype is a live stream's Content-Type, as the handler set it.
	ctype string
	body  bytes.Buffer
}

// nilID stands for an asked id that is not a UUID (no object has such an id).
const nilID = "00000000-0000-0000-0000-000000000000"

// askedID is what replaces the stand-in id: the id as it was asked when it is a
// UUID spelled with safe characters only, or the nil UUID when it is anything else.
func (w *echoWriter) askedID() string {
	if _, err := uuid.Parse(w.to); err != nil {
		return nilID
	}
	for i := 0; i < len(w.to); i++ {
		switch c := w.to[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-', c == ':', c == '{', c == '}':
		default:
			return nilID
		}
	}
	return w.to
}

func (w *echoWriter) WriteHeader(code int) {
	if w.code != 0 {
		return
	}
	w.code = code
	if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		w.live, w.ctype = true, ct
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *echoWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.live {
		return w.body.Write(b)
	}
	// an event stream's frames, under the type the handler gave the stream (it is
	// already sent; it is named beside the write so the two are read together)
	rw := w.ResponseWriter
	rw.Header().Set("Content-Type", w.ctype)
	return rw.Write(b)
}

// Flush sends a live stream's frames on; a held answer goes out whole, at the end.
func (w *echoWriter) Flush() {
	if !w.live {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *echoWriter) flush() {
	if w.live {
		return
	}
	if w.code == 0 {
		w.code = http.StatusOK
	}
	rw := w.ResponseWriter
	id := w.askedID()
	h := rw.Header()
	for k, vs := range h {
		for i, v := range vs {
			vs[i] = strings.ReplaceAll(v, w.from, id)
		}
		h[k] = vs
	}
	body := bytes.ReplaceAll(w.body.Bytes(), []byte(w.from), []byte(id))
	if h.Get("Content-Length") != "" {
		h.Set("Content-Length", strconv.Itoa(len(body)))
	}
	// the body goes out under the type the handler gave it, and no other
	if ct := h.Get("Content-Type"); ct != "" {
		rw.Header().Set("Content-Type", ct)
	}
	rw.WriteHeader(w.code)
	_, _ = rw.Write(body)
}
