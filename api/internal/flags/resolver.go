package flags

import (
	"context"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/subscriptions"
)

// Entitlements is the license view the resolver reads: main.go's one
// *subscriptions.Store.
type Entitlements interface {
	Current() *subscriptions.Entitlements
}

// Subject is who a bundle is for.
type Subject struct {
	// UserID is "" for a principal that is no user (the admin token).
	UserID  string
	IsAdmin bool
}

// Resolver merges defaults, the license and the overrides into a signed
// bundle, and keeps each one for TTL.
//
// The cache key carries the license snapshot itself: applying or
// revoking a license replaces the store's *Entitlements, so the next
// call resolves again without waiting out the TTL. Every write through
// the admin API calls Invalidate. A home runs one API process, so the
// cache needs no cross-process invalidation; a write from elsewhere (SQL
// by hand) shows within TTL.
type Resolver struct {
	Store        *Store
	Entitlements Entitlements
	Keys         *serverkeys.Store
	// Now is the clock (tests); nil is time.Now.
	Now func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cached
}

type cacheKey struct {
	subject Subject
	license *subscriptions.Entitlements
}

type cached struct {
	bundle Bundle
	until  time.Time
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Invalidate drops every cached bundle.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.cache = nil
	r.mu.Unlock()
}

// Bundle returns the signed bundle for s, from the cache while it is
// fresh.
func (r *Resolver) Bundle(ctx context.Context, s Subject) (Bundle, error) {
	lic := r.Entitlements.Current()
	key := cacheKey{subject: s, license: lic}
	now := r.now()
	r.mu.Lock()
	if c, ok := r.cache[key]; ok && now.Before(c.until) {
		r.mu.Unlock()
		return c.bundle, nil
	}
	r.mu.Unlock()

	values, err := r.Resolve(ctx, s, lic)
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{Flags: values, Tier: string(tierOf(lic))}
	if err := b.sign(r.Keys, now); err != nil {
		return Bundle{}, err
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[cacheKey]cached{}
	}
	for k, c := range r.cache { // keep the map to the live entries
		if !now.Before(c.until) {
			delete(r.cache, k)
		}
	}
	r.cache[key] = cached{bundle: b, until: now.Add(TTL)}
	r.mu.Unlock()
	return b, nil
}

func tierOf(lic *subscriptions.Entitlements) subscriptions.Tier {
	if lic == nil || lic.Tier == "" {
		return subscriptions.TierFree
	}
	return lic.Tier
}

// Resolve computes s's flag values under the license lic (nil: free),
// uncached and unsigned (the admin view of any user uses it).
func (r *Resolver) Resolve(ctx context.Context, s Subject, lic *subscriptions.Entitlements) (map[string]any, error) {
	cohorts, err := r.Store.CohortsOf(ctx, s.UserID)
	if err != nil {
		return nil, err
	}
	userID := ""
	if nullUUID(s.UserID) != nil {
		userID = s.UserID
	}
	rows, err := r.Store.Applicable(ctx, string(tierOf(lic)), userID, cohorts)
	if err != nil {
		return nil, err
	}
	return merge(s, lic, cohorts, rows), nil
}

// merge is the resolution itself: defaults, then the overrides scope by
// scope (rows are oldest first, so a later row of a scope wins), then
// the two rules no override beats.
func merge(s Subject, lic *subscriptions.Entitlements, cohorts []string, rows []Override) map[string]any {
	member := map[string]bool{}
	for _, c := range cohorts {
		member[c] = true
	}
	out := map[string]any{}
	for _, d := range Declarations {
		v := d.Default
		if d.Feature != "" {
			v = lic.Allows(d.Feature)
		}
		for _, c := range d.Cohorts {
			if member[c] {
				v = true
			}
		}
		out[d.Key] = v
	}
	for _, scope := range []string{ScopeGlobal, ScopeTier, ScopeCohort, ScopeUser} {
		for _, o := range rows {
			if o.Scope != scope {
				continue
			}
			d, ok := Lookup(o.FlagKey)
			if !ok {
				continue // undeclared: a newer release's flag, not this one's
			}
			if b, isBool := o.Value.(bool); isBool {
				out[d.Key] = b
			}
		}
	}
	for _, d := range Declarations {
		switch {
		case d.AdminOnly && !s.IsAdmin:
			delete(out, d.Key)
		case d.Feature != "" && !lic.Allows(d.Feature):
			out[d.Key] = false // only the license grants a paid feature
		}
	}
	return out
}
