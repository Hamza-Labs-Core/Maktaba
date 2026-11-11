package parental

import (
	"context"
	"errors"
	"time"
)

// Subject names one thing a rule can allow or block.
type Subject struct {
	Kind string
	ID   string
}

// Item is one piece of content as the rules see it: its age tier (nil: unrated)
// and every subject it is or sits in (the video, its library, series, collections,
// channel, YouTube source, plugin).
type Item struct {
	Tier     *int
	Subjects []Subject
}

// Effective is an account's policy as a request applies it: the limit and the
// rules. The zero value (Restricted false) limits nothing.
type Effective struct {
	UserID     string
	Restricted bool
	MaxTier    int
	Unrated    string
	AllowOnly  bool
	Version    int
	// Rules are the account's rules as stored; Permits and the lookups skip the
	// expired ones at the time asked about, so a cached value stays correct as
	// rules run out.
	Rules []Rule
}

func (e *Effective) has(effect string, s Subject, now time.Time) bool {
	for i := range e.Rules {
		r := &e.Rules[i]
		if r.Effect != effect || r.SubjectKind != s.Kind || r.SubjectID != s.ID {
			continue
		}
		if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
			continue
		}
		return true
	}
	return false
}

// Blocked reports whether a live block rule names s.
func (e *Effective) Blocked(s Subject, now time.Time) bool {
	return e != nil && e.Restricted && e.has(EffectBlock, s, now)
}

// AllowedBy reports whether a live allow rule names s.
func (e *Effective) AllowedBy(s Subject, now time.Time) bool {
	return e != nil && e.Restricted && e.has(EffectAllow, s, now)
}

// Subjects lists the ids of kind that a live rule of effect names (for a query's
// IN list).
func (e *Effective) Subjects(effect, kind string, now time.Time) []string {
	if e == nil || !e.Restricted {
		return nil
	}
	var out []string
	for i := range e.Rules {
		r := &e.Rules[i]
		if r.Effect == effect && r.SubjectKind == kind && (r.ExpiresAt == nil || r.ExpiresAt.After(now)) {
			out = append(out, r.SubjectID)
		}
	}
	return out
}

// TierPermits reports whether content of tier (nil: unrated) passes the limit and
// the unrated rule alone, before any rule on a subject.
func (e *Effective) TierPermits(tier *int) bool {
	if e == nil || !e.Restricted {
		return true
	}
	if tier == nil {
		as, ok := unratedTiers[e.Unrated]
		return ok && as <= e.MaxTier
	}
	return *tier <= e.MaxTier
}

// Permits is the whole rule for one item: a block on anything it is or sits in
// hides it; otherwise an allow on any of them shows it, above the limit and
// unrated too; otherwise allow_only hides it; otherwise the limit and the unrated
// rule decide.
func (e *Effective) Permits(item Item, now time.Time) bool {
	if e == nil || !e.Restricted {
		return true
	}
	for _, s := range item.Subjects {
		if e.has(EffectBlock, s, now) {
			return false
		}
	}
	for _, s := range item.Subjects {
		if e.has(EffectAllow, s, now) {
			return true
		}
	}
	if e.AllowOnly {
		return false
	}
	return e.TierPermits(item.Tier)
}

// Effective loads userID's policy and rules. A user with no policy gets the
// unrestricted value, not an error.
func (s *Store) Effective(ctx context.Context, userID string) (*Effective, error) {
	p, err := s.Policy(ctx, userID)
	if errors.Is(err, ErrNotRestricted) {
		return &Effective{UserID: userID}, nil
	}
	if err != nil {
		return nil, err
	}
	rules, err := s.Rules(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Effective{
		UserID: userID, Restricted: true, MaxTier: p.MaxTier, Unrated: p.Unrated,
		AllowOnly: p.AllowOnly, Version: p.Version, Rules: rules,
	}, nil
}
