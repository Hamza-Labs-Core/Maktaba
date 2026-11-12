package parental

// The device's signal (Epic 57 story 57.04): what a sign-in's device said about who
// holds it (Apple's Declared Age Range, the Screen Time movie and TV limits), the
// tier limit that maps to, and the ratchet that keeps it.
//
// A signal is lower-trust input, so it only ever tightens. The first one for a
// sign-in (a refresh-token family) is applied; a stricter one replaces it at once; a
// looser one is kept beside it in pending_looser and changes nothing until an admin
// or the account's guardian clears the signal. The limit a request is held to is the
// stricter of the account's policy and its sign-in's signal (Effective.WithSignal,
// Cache.For), and an account with no policy is held to the signal alone.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

var (
	// ErrNoSignIn: the credential names no live sign-in of this user (a web session,
	// an API token, or a refresh-token family that is gone).
	ErrNoSignIn = errors.New("parental: the request names no sign-in")
	// ErrSignalNotFound: this user has no signal on that sign-in.
	ErrSignalNotFound = errors.New("parental: no signal on that sign-in")
)

// SettingUnratedOnSignal is the app_settings key for what a sign-in limited only by
// its device does with unrated content: an unrated rule ("hide" by default). Whoever
// writes it sends NOTIFY parental_policy with an empty payload, so every process
// reads it again.
const SettingUnratedOnSignal = "parental.unrated_on_signal"

// The rating systems a device's ceilings are read with.
const (
	SystemAppleMovie = "apple-movie"
	SystemAppleTV    = "apple-tv"
)

// The audit actions of a signal.
const (
	AuditSignalApplied = "parental.signal.applied"
	AuditSignalPending = "parental.signal.pending"
	AuditSignalCleared = "parental.signal.cleared"
)

// AgeRange is Apple's Declared Age Range as the Player sends it: the bounds of the
// range the person is in (either may be missing) and who declared it.
type AgeRange struct {
	Lower       *int   `json:"lower,omitempty"`
	Upper       *int   `json:"upper,omitempty"`
	Declaration string `json:"declaration,omitempty"`
}

// SignalInput is one statement by a device. A missing part says nothing (no limit
// from it).
type SignalInput struct {
	AgeRange *AgeRange `json:"age_range,omitempty"`
	// MovieCeiling and TVCeiling are Screen Time's limits on Apple's 0 to 1000 scale
	// (0: nothing allowed, 1000: no limit).
	MovieCeiling *int `json:"movie_ceiling,omitempty"`
	TVCeiling    *int `json:"tv_ceiling,omitempty"`
	// Region is the device's region ("US", "SA"). The Apple scale is defined by its
	// United States anchors, which are the only table there is, so the region is kept
	// and does not change the mapping.
	Region string `json:"region,omitempty"`
}

// FieldProblem names one field of a signal that cannot be read.
type FieldProblem struct{ Field, Message string }

var (
	declarationRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	regionRe      = regexp.MustCompile(`^([A-Z]{2}|[0-9]{3})$`)
)

// maxAge bounds an age range's bounds.
const maxAge = 150

// Normalize tidies in (the region in upper case) and lists what is wrong with it;
// an empty list means it can be stored.
func (in *SignalInput) Normalize() []FieldProblem {
	var out []FieldProblem
	if r := in.AgeRange; r != nil {
		for name, v := range map[string]*int{"age_range.lower": r.Lower, "age_range.upper": r.Upper} {
			if v != nil && (*v < 0 || *v > maxAge) {
				out = append(out, FieldProblem{name, "must be between 0 and 150"})
			}
		}
		if r.Lower != nil && r.Upper != nil && *r.Lower > *r.Upper && *r.Lower <= maxAge && *r.Upper >= 0 {
			out = append(out, FieldProblem{"age_range.upper", "must not be below lower"})
		}
		if r.Declaration != "" && !declarationRe.MatchString(r.Declaration) {
			out = append(out, FieldProblem{"age_range.declaration", "must be a lower-case name such as self_declared or guardian_declared"})
		}
	}
	for name, v := range map[string]*int{"movie_ceiling": in.MovieCeiling, "tv_ceiling": in.TVCeiling} {
		if v != nil && (*v < 0 || *v > NoLimit) {
			out = append(out, FieldProblem{name, "must be between 0 and 1000"})
		}
	}
	in.Region = strings.ToUpper(strings.TrimSpace(in.Region))
	if in.Region != "" && !regionRe.MatchString(in.Region) {
		out = append(out, FieldProblem{"region", "must be a two-letter region code such as US, or a three-digit area code"})
	}
	return out
}

// AgeRangeLimit is the limit an age range gives (README §6.3): an upper bound of 9
// or less is All ages, of 13 or less 9+, of 18 or less 13+. A range with no upper
// bound, or one above 18, or no range at all, gives NoLimit. The upper bound decides
// because it is the oldest the person may be and still be in the range Apple named.
func AgeRangeLimit(r *AgeRange) int {
	if r == nil || r.Upper == nil {
		return NoLimit
	}
	switch u := *r.Upper; {
	case u <= 9:
		return TierAll
	case u <= 13:
		return Tier9
	case u <= 18:
		return Tier13
	}
	return NoLimit
}

// ScaleLimit is the limit a value on one of the Apple scales (SystemAppleMovie,
// SystemAppleTV) gives: a tier, NothingAllowed or NoLimit. A value between two of the
// scale's steps counts as the step below it, so an unknown value only errs toward
// hiding. ok is false when system is no scale or value is under its lowest step.
func (t *Table) ScaleLimit(system string, value int) (limit int, ok bool) {
	sys, found := t.systems[system]
	if !found || sys.Scale == nil {
		return 0, false
	}
	step := -1
	for k := range sys.Scale {
		n, err := strconv.Atoi(k)
		if err == nil && n <= value && n > step {
			step = n
		}
	}
	if step < 0 {
		return 0, false
	}
	return t.Limit(system + "/" + strconv.Itoa(step))
}

// SignalLimit is the limit a device's statement gives: the strictest of its age
// range, its movie ceiling and its TV ceiling (a tier, NothingAllowed, or NoLimit when
// none of them limits). A ceiling the table cannot read allows nothing.
func (t *Table) SignalLimit(in SignalInput) int {
	limit := AgeRangeLimit(in.AgeRange)
	for _, c := range []struct {
		system string
		value  *int
	}{{SystemAppleMovie, in.MovieCeiling}, {SystemAppleTV, in.TVCeiling}} {
		if c.value == nil {
			continue
		}
		l, ok := t.ScaleLimit(c.system, *c.value)
		if !ok {
			l = NothingAllowed
		}
		limit = min(limit, l)
	}
	return limit
}

// limitPtr is a limit as the tier_limit column and the `ac` claim hold it: nil for
// NoLimit.
func limitPtr(limit int) *int {
	if limit >= NoLimit {
		return nil
	}
	return &limit
}

// limitOf reads a stored limit back: NoLimit for nil.
func limitOf(p *int) int {
	if p == nil {
		return NoLimit
	}
	return *p
}

// ClaimLimit reads an access token's `ac` claim as a limit. The claim is signed by
// the home, but a value that is not a limit is still read toward hiding: under zero
// allows nothing, anything else is rounded down to a tier.
func ClaimLimit(ac *int) int {
	switch {
	case ac == nil:
		return NoLimit
	case *ac < 0:
		return NothingAllowed
	case *ac >= NoLimit:
		return NoLimit
	}
	return LimitTier(*ac)
}

// SignalState is where a sign-in's signal stands after a change.
type SignalState struct {
	// TierLimit is the limit in force (nil: the device limits nothing).
	TierLimit *int
	// Pending says a looser statement is waiting for an admin or a guardian.
	Pending bool
	// Removed says the signal was cleared away altogether (ClearSignal with nothing
	// pending).
	Removed bool
	// Off says parental controls are not turned on in this home (story 57.12): the
	// statement was recorded and limits nothing, so TierLimit and Pending are empty.
	Off bool
}

// pendingSignal is pending_looser: the looser statement, whole, so that clearing
// applies exactly what the device said.
type pendingSignal struct {
	Lower        *int      `json:"lower,omitempty"`
	Upper        *int      `json:"upper,omitempty"`
	Declaration  string    `json:"declaration,omitempty"`
	MovieCeiling *int      `json:"movie_ceiling,omitempty"`
	TVCeiling    *int      `json:"tv_ceiling,omitempty"`
	Region       string    `json:"region,omitempty"`
	TierLimit    *int      `json:"tier_limit"`
	ObservedAt   time.Time `json:"observed_at"`
}

func nullIntPtr(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Valid: true, Int64: int64(*p)}
}

func nullStr(s string) sql.NullString { return sql.NullString{Valid: s != "", String: s} }

func signalAudit(family string, limit *int, pending bool) map[string]any {
	return map[string]any{"refresh_family_id": family, "tier_limit": limit, "pending_looser": pending}
}

// lockSignal serialises the changes to one sign-in's signal for the transaction.
func lockSignal(ctx context.Context, tx *sql.Tx, family string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('session_age_signal:' || $1::text, 0))`, family)
	return err
}

// notifySignal drops userID's cached limits on every process when the transaction
// commits (the same channel a policy change uses).
func notifySignal(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, userID)
	return err
}

// RecordSignal stores what the device of userID's sign-in (family, a refresh-token
// family) said, by the ratchet: the first statement and a stricter one are applied, an
// equal one refreshes what is stored, a looser one waits in pending_looser. It
// answers the limit now in force. ErrNoSignIn when family is not a live sign-in of
// userID. in must have passed Normalize.
func (s *Store) RecordSignal(ctx context.Context, userID, family string, in SignalInput) (*SignalState, error) {
	if !isUUID(userID) || !isUUID(family) {
		return nil, ErrNoSignIn
	}
	limit := Default().SignalLimit(in)
	rng := in.AgeRange
	if rng == nil {
		rng = &AgeRange{}
	}
	now := s.now().UTC()
	var (
		out      SignalState
		awaiting bool // a looser statement began to wait, or the one waiting changed
		off      bool // parental controls are not on: recorded, limiting nothing
	)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		out, awaiting = SignalState{}, false
		if err := lockSignal(ctx, tx, family); err != nil {
			return err
		}
		on, err := enabledIn(ctx, tx)
		if err != nil {
			return err
		}
		off = !on
		var one int
		err = tx.QueryRowContext(ctx,
			`SELECT 1 FROM refresh_tokens
			  WHERE family_id = $1 AND user_id = $2 AND revoked_at IS NULL AND expires_at > $3 LIMIT 1`,
			family, userID, now).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSignIn
		}
		if err != nil {
			return err
		}
		// signals of this user's sign-ins that have ended (their access tokens still
		// carry the limit until they expire)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM session_age_signals s
			  WHERE s.user_id = $1 AND s.refresh_family_id <> $2
			    AND NOT EXISTS (SELECT 1 FROM refresh_tokens r
			                     WHERE r.family_id = s.refresh_family_id AND r.revoked_at IS NULL AND r.expires_at > $3)`,
			userID, family, now); err != nil {
			return err
		}

		var (
			owner      string
			stored     sql.NullInt64
			hadPending bool
		)
		err = tx.QueryRowContext(ctx,
			`SELECT user_id, tier_limit, pending_looser IS NOT NULL FROM session_age_signals
			  WHERE refresh_family_id = $1 FOR UPDATE`, family).Scan(&owner, &stored, &hadPending)
		first := errors.Is(err, sql.ErrNoRows)
		if err != nil && !first {
			return err
		}
		if !first && owner != userID {
			return ErrNoSignIn
		}
		applied := limitOf(intPtr(stored))

		switch {
		case first:
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO session_age_signals
				   (refresh_family_id, user_id, lower, upper, declaration, movie_ceiling, tv_ceiling, region, tier_limit, observed_at)
				 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				family, userID, nullIntPtr(rng.Lower), nullIntPtr(rng.Upper), nullStr(rng.Declaration),
				nullIntPtr(in.MovieCeiling), nullIntPtr(in.TVCeiling), nullStr(in.Region), nullIntPtr(limitPtr(limit)), now); err != nil {
				return err
			}
			out.TierLimit = limitPtr(limit)
			if limit < NoLimit {
				if err := audit(ctx, tx, AuditSignalApplied, userID, userID, nil, signalAudit(family, out.TierLimit, false)); err != nil {
					return err
				}
			}
		case limit <= applied:
			// as strict or stricter: it is what the device says now, and nothing
			// looser is waiting any more
			if _, err := tx.ExecContext(ctx,
				`UPDATE session_age_signals
				    SET lower = $2, upper = $3, declaration = $4, movie_ceiling = $5, tv_ceiling = $6, region = $7,
				        tier_limit = $8, pending_looser = NULL, observed_at = $9
				  WHERE refresh_family_id = $1`,
				family, nullIntPtr(rng.Lower), nullIntPtr(rng.Upper), nullStr(rng.Declaration),
				nullIntPtr(in.MovieCeiling), nullIntPtr(in.TVCeiling), nullStr(in.Region), nullIntPtr(limitPtr(limit)), now); err != nil {
				return err
			}
			out.TierLimit = limitPtr(limit)
			if limit < applied || hadPending {
				if err := audit(ctx, tx, AuditSignalApplied, userID, userID,
					signalAudit(family, limitPtr(applied), hadPending), signalAudit(family, out.TierLimit, false)); err != nil {
					return err
				}
			}
		default:
			// looser: recorded, not applied
			pending, err := json.Marshal(pendingSignal{
				Lower: rng.Lower, Upper: rng.Upper, Declaration: rng.Declaration,
				MovieCeiling: in.MovieCeiling, TVCeiling: in.TVCeiling, Region: in.Region,
				TierLimit: limitPtr(limit), ObservedAt: now,
			})
			if err != nil {
				return err
			}
			var was sql.NullInt64
			if err := tx.QueryRowContext(ctx,
				`WITH old AS (SELECT (pending_looser->>'tier_limit')::int AS was FROM session_age_signals WHERE refresh_family_id = $1)
				 UPDATE session_age_signals SET pending_looser = $2::jsonb
				  WHERE refresh_family_id = $1 RETURNING (SELECT was FROM old)`,
				family, string(pending)).Scan(&was); err != nil {
				return err
			}
			out.TierLimit, out.Pending = limitPtr(applied), true
			awaiting = !hadPending || limitOf(intPtr(was)) != limit
			if awaiting {
				if err := audit(ctx, tx, AuditSignalPending, userID, userID,
					signalAudit(family, out.TierLimit, hadPending),
					map[string]any{"refresh_family_id": family, "tier_limit": out.TierLimit, "pending_looser": true,
						"pending_tier_limit": limitPtr(limit)}); err != nil {
					return err
				}
			}
		}
		return notifySignal(ctx, tx, userID)
	})
	if err != nil {
		return nil, err
	}
	if off {
		// recorded by the same ratchet, and in force for no one until an admin turns
		// parental controls on; the admins are told a child's device signed in
		out = SignalState{Off: true}
		if limit < Tier18 && s.OnChildDevice != nil {
			s.OnChildDevice(ctx, userID, family)
		}
		return &out, nil
	}
	if awaiting && s.OnSignalPending != nil {
		s.OnSignalPending(ctx, userID, family)
	}
	return &out, nil
}

// ClearSignal is an admin's or a guardian's answer to userID's sign-in's signal: a
// looser statement that was waiting is applied as the device said it; with none
// waiting the signal is removed, and the sign-in is held to the account's policy
// alone until its device speaks again. ErrSignalNotFound when userID has no signal on
// family.
func (s *Store) ClearSignal(ctx context.Context, actorID, userID, family string) (*SignalState, error) {
	if !isUUID(userID) || !isUUID(family) {
		return nil, ErrSignalNotFound
	}
	var out SignalState
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		out = SignalState{}
		if err := lockSignal(ctx, tx, family); err != nil {
			return err
		}
		var (
			stored  sql.NullInt64
			pending sql.NullString
		)
		err := tx.QueryRowContext(ctx,
			`SELECT tier_limit, pending_looser FROM session_age_signals
			  WHERE refresh_family_id = $1 AND user_id = $2 FOR UPDATE`, family, userID).Scan(&stored, &pending)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrSignalNotFound
		}
		if err != nil {
			return err
		}
		before := signalAudit(family, intPtr(stored), pending.Valid)

		var p pendingSignal
		if pending.Valid && json.Unmarshal([]byte(pending.String), &p) == nil &&
			(p.TierLimit == nil || ValidPolicyLimit(*p.TierLimit)) {
			if _, err := tx.ExecContext(ctx,
				`UPDATE session_age_signals
				    SET lower = $2, upper = $3, declaration = $4, movie_ceiling = $5, tv_ceiling = $6, region = $7,
				        tier_limit = $8, pending_looser = NULL, observed_at = $9
				  WHERE refresh_family_id = $1`,
				family, nullIntPtr(p.Lower), nullIntPtr(p.Upper), nullStr(p.Declaration),
				nullIntPtr(p.MovieCeiling), nullIntPtr(p.TVCeiling), nullStr(p.Region), nullIntPtr(p.TierLimit), p.ObservedAt); err != nil {
				return err
			}
			out.TierLimit = p.TierLimit
			if err := audit(ctx, tx, AuditSignalCleared, actorID, userID, before, signalAudit(family, out.TierLimit, false)); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `DELETE FROM session_age_signals WHERE refresh_family_id = $1`, family); err != nil {
				return err
			}
			out.Removed = true
			if err := audit(ctx, tx, AuditSignalCleared, actorID, userID, before, nil); err != nil {
				return err
			}
		}
		return notifySignal(ctx, tx, userID)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SignInSignal is one sign-in's signal as a request applies it.
type SignInSignal struct {
	// Limit is the limit in force (NoLimit: none, or no signal).
	Limit int
	// Pending says a looser statement is waiting.
	Pending bool
	// Unrated is the home's rule for unrated content on a sign-in limited only by
	// its device (SettingUnratedOnSignal).
	Unrated string
	// Off says parental controls are not turned on (story 57.12): no device signal,
	// stored or carried by a token, limits a request.
	Off bool
}

// unratedOnSignal reads the setting's stored JSON; anything that is not an unrated
// rule hides.
func unratedOnSignal(raw sql.NullString) string {
	var v string
	if raw.Valid && json.Unmarshal([]byte(raw.String), &v) == nil && ValidUnrated(v) {
		return v
	}
	return UnratedHide
}

// SignInSignal reads the signal of userID's sign-in (family "": none, the setting
// alone).
func (s *Store) SignInSignal(ctx context.Context, userID, family string) (*SignInSignal, error) {
	out := &SignInSignal{Limit: NoLimit}
	var (
		setting sql.NullString
		enabled sql.NullString
		limit   sql.NullInt64
		found   sql.NullBool
		pending sql.NullBool
	)
	args := []any{SettingUnratedOnSignal, SettingEnabled}
	q := `SELECT (SELECT value::text FROM app_settings WHERE key = $1), (SELECT value::text FROM app_settings WHERE key = $2),
	             NULL::int, NULL::bool, NULL::bool`
	if isUUID(userID) && isUUID(family) {
		q = `SELECT (SELECT value::text FROM app_settings WHERE key = $1), (SELECT value::text FROM app_settings WHERE key = $2),
		            g.tier_limit, g.user_id IS NOT NULL, g.pending_looser IS NOT NULL
		       FROM (SELECT 1) one
		       LEFT JOIN session_age_signals g ON g.refresh_family_id = $3 AND g.user_id = $4`
		args = append(args, family, userID)
	}
	if err := s.DB.QueryRowContext(ctx, q, args...).Scan(&setting, &enabled, &limit, &found, &pending); err != nil {
		return nil, fmt.Errorf("parental: sign-in signal: %w", err)
	}
	out.Unrated = unratedOnSignal(setting)
	if !enabledValue(enabled) {
		out.Off = true
		return out, nil
	}
	if found.Valid && found.Bool {
		out.Limit, out.Pending = limitOf(intPtr(limit)), pending.Bool
	}
	return out, nil
}

// AgeCeiling is the `ac` claim for an access token of userID's sign-in: the stored
// signal's limit, nil when the sign-in has none, and nil while parental controls are
// not turned on in this home (story 57.12: the signal is recorded, not applied).
func (s *Store) AgeCeiling(ctx context.Context, userID, family string) (*int, error) {
	if !isUUID(userID) || !isUUID(family) {
		return nil, nil
	}
	var (
		limit   sql.NullInt64
		enabled sql.NullString
	)
	err := s.DB.QueryRowContext(ctx,
		`SELECT g.tier_limit, (SELECT value::text FROM app_settings WHERE key = $3)
		   FROM session_age_signals g WHERE g.refresh_family_id = $1 AND g.user_id = $2`, family, userID, SettingEnabled).Scan(&limit, &enabled)
	if err == nil && !enabledValue(enabled) {
		return nil, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("parental: age ceiling: %w", err)
	}
	return intPtr(limit), nil
}

// WithSignal is e held to a sign-in's device limit as well: the stricter of the two
// limits. An account with no policy becomes limited by the device alone (Restricted
// and SignalOnly both true, no rules, unrated content by the home's setting), so
// every reader of Restricted, MaxTier and Unrated filters it with no change. A
// restricted account keeps its own unrated rule and its rules: an allow its guardian
// gave still shows the item. e is not changed.
func (e *Effective) WithSignal(sig SignInSignal) *Effective {
	if e == nil {
		e = &Effective{}
	}
	if sig.Limit >= NoLimit && !sig.Pending {
		return e
	}
	out := *e
	out.SignalPending = sig.Pending
	if sig.Limit >= NoLimit {
		return &out
	}
	limit := sig.Limit
	out.SignalLimit = &limit
	if !e.Restricted {
		unrated := sig.Unrated
		if !ValidUnrated(unrated) {
			unrated = UnratedHide
		}
		out.Restricted, out.SignalOnly = true, true
		out.MaxTier, out.Unrated, out.AllowOnly, out.Rules = limit, unrated, false, nil
		return &out
	}
	out.MaxTier = min(e.MaxTier, limit)
	return &out
}

// signalFor is the signal a request's principal is held to: the stricter of its
// sign-in's stored signal and the `ac` claim of its token. The claim alone would do
// on a machine with no copy of the signal; the stored one makes a tightened limit
// hold before the token is renewed. After a clear loosens a limit, the old token's
// claim keeps the stricter one until the Player refreshes (at most the token's life).
func (c *Cache) signalFor(ctx context.Context, p *principal.Principal) (*SignInSignal, error) {
	claim := ClaimLimit(p.AgeCeiling)
	family := p.RefreshFamily
	if !isUUID(p.UserID) || !isUUID(family) {
		if claim >= NoLimit {
			return nil, nil
		}
		family = ""
	}
	sig, err := c.signal(ctx, p.UserID, family)
	if err != nil {
		return nil, err
	}
	if sig.Off {
		// parental controls are off in this home: a claim minted before they were
		// turned off limits nothing either
		return nil, nil
	}
	if claim < sig.Limit {
		out := *sig
		out.Limit = claim
		return &out, nil
	}
	return sig, nil
}

type signalKey struct{ userID, family string }

// signal reads one sign-in's signal through the cache (kept and dropped with the
// account's policy entry: Invalidate(userID) drops both).
func (c *Cache) signal(ctx context.Context, userID, family string) (*SignInSignal, error) {
	key := signalKey{userID, family}
	c.mu.Lock()
	if s, ok := c.signals[key]; ok {
		c.mu.Unlock()
		return s, nil
	}
	gen := c.gen
	c.mu.Unlock()

	s, err := c.store.SignInSignal(ctx, userID, family)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.on && c.gen == gen {
		if c.signals == nil || len(c.signals) >= maxCached {
			c.signals = map[signalKey]*SignInSignal{}
		}
		c.signals[key] = s
	}
	c.mu.Unlock()
	return s, nil
}

// dropSignals forgets userID's sign-ins' signals ("": everyone's). The caller holds
// c.mu.
func (c *Cache) dropSignals(userID string) {
	if userID == "" {
		c.signals = nil
		return
	}
	for k := range c.signals {
		if k.userID == userID || k.userID == "" {
			delete(c.signals, k)
		}
	}
}
