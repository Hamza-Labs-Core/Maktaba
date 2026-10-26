package cloudlink

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// gateTTL is how long the gate trusts a link it read: an unlink or a
// lowered scope reaches access tokens already out within this, while a
// signed-in app does not cost a query on every request.
const gateTTL = 5 * time.Second

// LinkReader is the gate's view of the links (Store satisfies it).
type LinkReader interface {
	LinkFor(ctx context.Context, userID string) (*Link, error)
}

// Gate checks every cloud sign-in's request against the link it came
// from: no link (the user or an admin unlinked it) and the request is
// anonymous (401 on every route that needs a sign-in); a link lowered to
// play caps a manage sign-in to play. Other sign-ins pass untouched.
type Gate struct {
	Links LinkReader
	Now   func() time.Time

	mu    sync.Mutex
	cache map[string]gateEntry
}

type gateEntry struct {
	scope string // "" ⇒ no link
	until time.Time
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Forget drops a user's cached link (the store's change hook calls it,
// so this machine sees an unlink at once).
func (g *Gate) Forget(userID string) {
	g.mu.Lock()
	delete(g.cache, userID)
	g.mu.Unlock()
}

func (g *Gate) linkScope(ctx context.Context, userID string) (string, error) {
	now := g.now()
	g.mu.Lock()
	if e, ok := g.cache[userID]; ok && now.Before(e.until) {
		g.mu.Unlock()
		return e.scope, nil
	}
	g.mu.Unlock()
	scope := ""
	l, err := g.Links.LinkFor(ctx, userID)
	switch {
	case err == nil:
		scope = l.Scope
	case !errors.Is(err, ErrNotLinked):
		return "", err
	}
	g.mu.Lock()
	if g.cache == nil || len(g.cache) > 4096 {
		g.cache = map[string]gateEntry{}
	}
	g.cache[userID] = gateEntry{scope: scope, until: now.Add(gateTTL)}
	g.mu.Unlock()
	return scope, nil
}

// Middleware is the gate. It runs after the credential middlewares and
// before the auth gate.
func (g *Gate) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal.FromContext(r.Context())
		if !p.ViaCloud() {
			next.ServeHTTP(w, r)
			return
		}
		scope, err := g.linkScope(r.Context(), p.UserID)
		if err != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"type":"about:blank","title":"the sign-in check is not answering","status":503}`))
			return
		}
		if scope == "" {
			// the link is gone: this sign-in is no one now
			next.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), nil)))
			return
		}
		if scope == principal.CloudScopePlay && p.CloudScope != principal.CloudScopePlay {
			capped := *p
			capped.ApplyCloudScope(principal.CloudScopePlay)
			r = r.WithContext(principal.WithPrincipal(r.Context(), &capped))
		}
		next.ServeHTTP(w, r)
	})
}
