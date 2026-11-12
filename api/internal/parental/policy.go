// Package parental holds restricted accounts (Epic 57): a viewer's policy, the
// guardians an admin named for them, and their allow and block rules (story 57.02,
// slot 0221). Every change raises the policy's version, writes one audit_log row
// (ids and values, never a title) and reaches every API process through
// NOTIFY parental_policy, which the migration's triggers send.
package parental

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// ValidPolicyLimit reports whether t is a limit a policy may hold: a tier of the
// scale (ratings.go), or NothingAllowed, under which only allowed items show. NoLimit
// is not one: an account with no limit has no policy.
func ValidPolicyLimit(t int) bool {
	return t == NothingAllowed || ValidTier(t)
}

// UnratedHide hides content with no tier; the other values treat it as that tier.
const UnratedHide = "hide"

// unratedTiers maps the stored unrated rule to the tier unrated content is treated as.
var unratedTiers = map[string]int{
	"tier_0": TierAll, "tier_9": Tier9, "tier_13": Tier13,
	"tier_16": Tier16, "tier_18": Tier18,
}

// ValidUnrated reports whether s is a stored unrated rule.
func ValidUnrated(s string) bool {
	_, ok := unratedTiers[s]
	return ok || s == UnratedHide
}

// Rule effects and the kinds of thing a rule names.
const (
	EffectAllow = "allow"
	EffectBlock = "block"

	SubjectLibrary       = "library"
	SubjectCollection    = "collection"
	SubjectSeries        = "series"
	SubjectChannel       = "channel"
	SubjectYouTubeSource = "youtube_source"
	SubjectPlugin        = "plugin"
	SubjectVideo         = "video"
)

// ValidSubjectKind reports whether k is a kind a rule may name.
func ValidSubjectKind(k string) bool {
	switch k {
	case SubjectLibrary, SubjectCollection, SubjectSeries, SubjectChannel,
		SubjectYouTubeSource, SubjectPlugin, SubjectVideo:
		return true
	}
	return false
}

// NotifyChannel carries the child's id on every change to their policy, rules or
// guardians.
const NotifyChannel = "parental_policy"

// The constraint names the migration's triggers raise.
const (
	constraintRestrictedAdmin    = "parental_restricted_admin"
	constraintRestrictedGuardian = "parental_restricted_guardian"
)

var (
	// ErrNotRestricted: the user has no policy (or does not exist).
	ErrNotRestricted = errors.New("parental: not a restricted account")
	// ErrUserNotFound: the user to restrict, or to name as guardian, does not exist.
	ErrUserNotFound = errors.New("parental: user not found")
	// ErrStale: the caller's version is not the stored one.
	ErrStale = errors.New("parental: the policy changed since it was read")
	// ErrRestrictedAdmin: a restricted account is never an admin.
	ErrRestrictedAdmin = errors.New("parental: a restricted account cannot be an admin")
	// ErrRestrictedGuardian: a restricted account looks after no one, and a guardian
	// is not restricted.
	ErrRestrictedGuardian = errors.New("parental: a restricted account cannot be a guardian")
	// ErrSelfGuardian: a child is not their own guardian.
	ErrSelfGuardian = errors.New("parental: an account cannot be its own guardian")
	// ErrNoLibraryAccess: an allow on a library the child has no ACL for.
	ErrNoLibraryAccess = errors.New("parental: the account cannot open that library")
	// ErrInvalid: a value outside what a policy or rule may hold.
	ErrInvalid = errors.New("parental: invalid value")
	// ErrRuleNotFound: no such rule on this child.
	ErrRuleNotFound = errors.New("parental: rule not found")
	// ErrGuardianNotFound: that user is not a named guardian of this child.
	ErrGuardianNotFound = errors.New("parental: guardian not found")
)

// IsRestrictedAdmin reports whether err is the database refusing an admin with a
// policy (either a policy on an admin, or is_admin set on a restricted user).
func IsRestrictedAdmin(err error) bool {
	return errors.Is(err, ErrRestrictedAdmin) || constraintOf(err) == constraintRestrictedAdmin
}

func constraintOf(err error) string {
	var pe *pq.Error
	if errors.As(err, &pe) {
		return pe.Constraint
	}
	return ""
}

// translate maps the database's refusals to this package's errors.
func translate(err error) error {
	if err == nil {
		return nil
	}
	var pe *pq.Error
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Constraint {
	case constraintRestrictedAdmin:
		return ErrRestrictedAdmin
	case constraintRestrictedGuardian:
		return ErrRestrictedGuardian
	case "parental_guardians_not_self":
		return ErrSelfGuardian
	}
	switch pe.Code {
	case "23503": // foreign_key_violation: the user (or the policy) is not there
		if strings.Contains(pe.Constraint, "parental_guardians_child_id") ||
			strings.Contains(pe.Constraint, "parental_rules_user_id") {
			return ErrNotRestricted
		}
		return ErrUserNotFound
	case "22P02": // invalid_text_representation: an id that is not a uuid
		return ErrUserNotFound
	}
	return err
}

// Policy is one restricted account's stored limit.
type Policy struct {
	UserID    string    `json:"user_id"`
	MaxTier   int       `json:"max_tier"`
	Unrated   string    `json:"unrated"`
	AllowOnly bool      `json:"allow_only"`
	Version   int       `json:"version"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PolicyInput is what a caller may set. Version is the version the caller read: 0
// makes the account restricted (refused with ErrStale when it already is).
type PolicyInput struct {
	MaxTier   int
	Unrated   string
	AllowOnly bool
	Version   int
}

func (in PolicyInput) validate() error {
	if !ValidPolicyLimit(in.MaxTier) {
		return fmt.Errorf("%w: max_tier", ErrInvalid)
	}
	if !ValidUnrated(in.Unrated) {
		return fmt.Errorf("%w: unrated", ErrInvalid)
	}
	return nil
}

// Guardian is a user an admin named to look after a child.
type Guardian struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// Rule allows or blocks one thing for a restricted account.
type Rule struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Effect      string     `json:"effect"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   string     `json:"subject_id"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// RuleInput is a rule to add.
type RuleInput struct {
	Effect      string
	SubjectKind string
	SubjectID   string
	ExpiresAt   *time.Time
}

func (in RuleInput) validate() error {
	if in.Effect != EffectAllow && in.Effect != EffectBlock {
		return fmt.Errorf("%w: effect", ErrInvalid)
	}
	if !ValidSubjectKind(in.SubjectKind) {
		return fmt.Errorf("%w: subject_kind", ErrInvalid)
	}
	if in.SubjectID == "" || len(in.SubjectID) > 200 || strings.TrimSpace(in.SubjectID) != in.SubjectID {
		return fmt.Errorf("%w: subject_id", ErrInvalid)
	}
	return nil
}

// Child is a restricted account as the people who manage it list it.
type Child struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Policy
}

// Signal is what one sign-in's device said about its user's age (story 57.04 writes
// the rows; this story only lists them).
type Signal struct {
	RefreshFamilyID string          `json:"refresh_family_id"`
	Lower           *int            `json:"lower,omitempty"`
	Upper           *int            `json:"upper,omitempty"`
	Declaration     string          `json:"declaration,omitempty"`
	MovieCeiling    *int            `json:"movie_ceiling,omitempty"`
	TVCeiling       *int            `json:"tv_ceiling,omitempty"`
	Region          string          `json:"region,omitempty"`
	TierLimit       *int            `json:"tier_limit,omitempty"`
	PendingLooser   json.RawMessage `json:"pending_looser,omitempty"`
	ObservedAt      time.Time       `json:"observed_at"`
}

// Store reads and writes policies, guardians and rules.
type Store struct {
	DB *sql.DB
	// Now is the clock (tests set it); nil is time.Now.
	Now func() time.Time
	// OnSignalPending, when set, is told after the commit that userID's sign-in
	// (family) has a looser device signal waiting (story 57.04; the guardian's
	// notice of it is story 57.08's).
	OnSignalPending func(ctx context.Context, userID, family string)
	// OnChildDevice, when set, is told after the commit that userID's sign-in's
	// device said a child may be using it (a limit under 18) while parental controls
	// are not turned on (story 57.12): the admins are told once per sign-in.
	OnChildDevice func(ctx context.Context, userID, family string)
	// OnKeptChange, when set (the time keeper's KeptChanged, story 57.14), is told
	// after an account was made restricted, changed, or had its restriction lifted,
	// so the streaming service learns at once whether its time is kept.
	OnKeptChange func(ctx context.Context, userID string) error
}

// NewStore returns a Store over db.
func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type queryer interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// isUUID keeps a malformed id from reaching a uuid column (which would answer an
// error where a missing row is meant).
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func nullUUID(s string) sql.NullString {
	if !isUUID(s) {
		return sql.NullString{}
	}
	return sql.NullString{Valid: true, String: s}
}

const policyCols = `user_id, max_tier, unrated, allow_only, version, COALESCE(updated_by::text, ''), updated_at`

func scanPolicy(row interface{ Scan(...any) error }) (*Policy, error) {
	var p Policy
	if err := row.Scan(&p.UserID, &p.MaxTier, &p.Unrated, &p.AllowOnly, &p.Version, &p.UpdatedBy, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

func policyOf(ctx context.Context, q queryer, userID string, lock bool) (*Policy, error) {
	if !isUUID(userID) {
		return nil, ErrNotRestricted
	}
	sqlText := `SELECT ` + policyCols + ` FROM parental_policies WHERE user_id = $1`
	if lock {
		sqlText += ` FOR UPDATE`
	}
	p, err := scanPolicy(q.QueryRowContext(ctx, sqlText, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotRestricted
	}
	return p, err
}

// Policy returns userID's policy, or ErrNotRestricted.
func (s *Store) Policy(ctx context.Context, userID string) (*Policy, error) {
	return policyOf(ctx, s.DB, userID, false)
}

// IsRestricted reports whether userID has a policy.
func (s *Store) IsRestricted(ctx context.Context, userID string) (bool, error) {
	_, err := s.Policy(ctx, userID)
	if errors.Is(err, ErrNotRestricted) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return translate(err)
	}
	return translate(tx.Commit())
}

// policyAudit is a policy as the audit log keeps it.
func policyAudit(p *Policy) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{"max_tier": p.MaxTier, "unrated": p.Unrated, "allow_only": p.AllowOnly, "version": p.Version}
}

// audit writes one row for a change: who, whose policy, and the values before and
// after. Ids only: a subject's title is never written.
func audit(ctx context.Context, tx *sql.Tx, action, actorID, childID string, before, after any) error {
	payload, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO audit_log (category, action, actor_user_id, target_id, target_kind, payload)
		 VALUES ('admin', $1, $2, $3, 'user', $4)`,
		action, nullUUID(actorID), childID, string(payload))
	return err
}

// The audit actions.
const (
	AuditPolicySet       = "parental.policy.set"
	AuditPolicyLifted    = "parental.policy.lifted"
	AuditGuardianAdded   = "parental.guardian.added"
	AuditGuardianRemoved = "parental.guardian.removed"
	AuditRuleAdded       = "parental.rule.added"
	AuditRuleRemoved     = "parental.rule.removed"
)

// bump raises the policy's version after a change to its rules or guardians, so a
// client holding the old version knows to read again.
func bump(ctx context.Context, tx *sql.Tx, actorID, childID string, at time.Time) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE parental_policies SET version = version + 1, updated_by = $2, updated_at = $3 WHERE user_id = $1`,
		childID, nullUUID(actorID), at)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotRestricted
	}
	return nil
}

// SetPolicy makes userID restricted (in.Version 0) or changes their limit
// (in.Version the version read). ErrStale when the version is not the stored one,
// ErrRestrictedAdmin for an admin, ErrRestrictedGuardian for a guardian,
// ErrUserNotFound when there is no such user.
func (s *Store) SetPolicy(ctx context.Context, actorID, userID string, in PolicyInput) (*Policy, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if !isUUID(userID) {
		return nil, ErrUserNotFound
	}
	var out *Policy
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		before, err := policyOf(ctx, tx, userID, true)
		if err != nil && !errors.Is(err, ErrNotRestricted) {
			return err
		}
		at := s.now().UTC()
		if before == nil {
			if in.Version != 0 {
				return ErrStale
			}
			out, err = scanPolicy(tx.QueryRowContext(ctx,
				`INSERT INTO parental_policies (user_id, max_tier, unrated, allow_only, version, updated_by, updated_at)
				 VALUES ($1, $2, $3, $4, 1, $5, $6)
				 ON CONFLICT (user_id) DO NOTHING
				 RETURNING `+policyCols,
				userID, in.MaxTier, in.Unrated, in.AllowOnly, nullUUID(actorID), at))
			if errors.Is(err, sql.ErrNoRows) {
				return ErrStale // made restricted by someone else meanwhile
			}
			if err == nil {
				// the first restricted account turns parental controls on (story 57.12)
				err = EnableTx(ctx, tx, actorID)
			}
		} else {
			if in.Version != before.Version {
				return ErrStale
			}
			out, err = scanPolicy(tx.QueryRowContext(ctx,
				`UPDATE parental_policies
				    SET max_tier = $2, unrated = $3, allow_only = $4, version = version + 1,
				        updated_by = $5, updated_at = $6
				  WHERE user_id = $1
				 RETURNING `+policyCols,
				userID, in.MaxTier, in.Unrated, in.AllowOnly, nullUUID(actorID), at))
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, AuditPolicySet, actorID, userID, policyAudit(before), policyAudit(out))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Lift removes userID's restriction with its guardians and rules.
func (s *Store) Lift(ctx context.Context, actorID, userID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		before, err := policyOf(ctx, tx, userID, true)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM parental_policies WHERE user_id = $1`, userID); err != nil {
			return err
		}
		return audit(ctx, tx, AuditPolicyLifted, actorID, userID, policyAudit(before), nil)
	})
}

// Guardians lists the users named to look after childID (admins look after every
// child and are not listed).
func (s *Store) Guardians(ctx context.Context, childID string) ([]Guardian, error) {
	out := []Guardian{}
	if !isUUID(childID) {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT u.id, u.username, g.created_at
		   FROM parental_guardians g JOIN users u ON u.id = g.guardian_id
		  WHERE g.child_id = $1
		  ORDER BY lower(u.username), u.id`, childID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g Guardian
		if err := rows.Scan(&g.ID, &g.Username, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AddGuardian names guardianID a guardian of childID (naming one twice changes
// nothing). ErrNotRestricted when the child has no policy, ErrUserNotFound when the
// guardian does not exist, ErrRestrictedGuardian when the guardian is restricted,
// ErrSelfGuardian for the child themselves.
func (s *Store) AddGuardian(ctx context.Context, actorID, childID, guardianID string) error {
	if !isUUID(guardianID) {
		return ErrUserNotFound
	}
	if childID == guardianID {
		return ErrSelfGuardian
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		at := s.now().UTC()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO parental_guardians (child_id, guardian_id, created_by, created_at)
			 VALUES ($1, $2, $3, $4) ON CONFLICT (child_id, guardian_id) DO NOTHING`,
			childID, guardianID, nullUUID(actorID), at)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		if err := bump(ctx, tx, actorID, childID, at); err != nil {
			return err
		}
		return audit(ctx, tx, AuditGuardianAdded, actorID, childID, nil, map[string]any{"guardian_id": guardianID})
	})
}

// RemoveGuardian takes guardianID off childID. ErrGuardianNotFound when they were
// not named.
func (s *Store) RemoveGuardian(ctx context.Context, actorID, childID, guardianID string) error {
	if !isUUID(guardianID) {
		return ErrGuardianNotFound
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`DELETE FROM parental_guardians WHERE child_id = $1 AND guardian_id = $2`, childID, guardianID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrGuardianNotFound
		}
		if err := bump(ctx, tx, actorID, childID, s.now().UTC()); err != nil {
			return err
		}
		return audit(ctx, tx, AuditGuardianRemoved, actorID, childID, map[string]any{"guardian_id": guardianID}, nil)
	})
}

// IsGuardian reports whether guardianID is a named guardian of childID.
func (s *Store) IsGuardian(ctx context.Context, guardianID, childID string) (bool, error) {
	if !isUUID(guardianID) || !isUUID(childID) {
		return false, nil
	}
	var ok bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM parental_guardians WHERE guardian_id = $1 AND child_id = $2)`,
		guardianID, childID).Scan(&ok)
	return ok, err
}

// HasChildren reports whether guardianID is a named guardian of anyone.
func (s *Store) HasChildren(ctx context.Context, guardianID string) (bool, error) {
	if !isUUID(guardianID) {
		return false, nil
	}
	var ok bool
	err := s.DB.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM parental_guardians WHERE guardian_id = $1)`, guardianID).Scan(&ok)
	return ok, err
}

// Children lists the restricted accounts guardianID was named for, or every one
// when all is set (an admin).
func (s *Store) Children(ctx context.Context, guardianID string, all bool) ([]Child, error) {
	out := []Child{}
	q := `SELECT u.username, p.user_id, p.max_tier, p.unrated, p.allow_only, p.version,
	             COALESCE(p.updated_by::text, ''), p.updated_at
	        FROM parental_policies p JOIN users u ON u.id = p.user_id`
	var args []any
	if !all {
		if !isUUID(guardianID) {
			return out, nil
		}
		q += ` WHERE EXISTS (SELECT 1 FROM parental_guardians g WHERE g.child_id = p.user_id AND g.guardian_id = $1)`
		args = append(args, guardianID)
	}
	q += ` ORDER BY lower(u.username), p.user_id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Child
		if err := rows.Scan(&c.Username, &c.UserID, &c.MaxTier, &c.Unrated, &c.AllowOnly, &c.Version,
			&c.UpdatedBy, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ID = c.UserID
		out = append(out, c)
	}
	return out, rows.Err()
}

// Access is what a caller may do with one child's policy.
type Access int

const (
	// AccessNone: the child is not the caller's to see (answered as missing).
	AccessNone Access = iota
	// AccessGuardian: a named guardian: read, change the limit and the rules.
	AccessGuardian
	// AccessAdmin: everything, including lifting the restriction and naming guardians.
	AccessAdmin
)

// AccessTo says what p may do with childID's policy. An admin manages every
// account (restricted or not yet); a named guardian manages their child; anyone
// else, the child included, has no access.
func (s *Store) AccessTo(ctx context.Context, p *principal.Principal, childID string) (Access, error) {
	if p == nil {
		return AccessNone, nil
	}
	if p.IsAdmin {
		return AccessAdmin, nil
	}
	ok, err := s.IsGuardian(ctx, p.UserID, childID)
	if err != nil || !ok {
		return AccessNone, err
	}
	return AccessGuardian, nil
}

const ruleCols = `id, user_id, effect, subject_kind, subject_id, expires_at, COALESCE(created_by::text, ''), created_at`

func scanRule(row interface{ Scan(...any) error }) (*Rule, error) {
	var (
		r   Rule
		exp sql.NullTime
	)
	if err := row.Scan(&r.ID, &r.UserID, &r.Effect, &r.SubjectKind, &r.SubjectID, &exp, &r.CreatedBy, &r.CreatedAt); err != nil {
		return nil, err
	}
	if exp.Valid {
		t := exp.Time
		r.ExpiresAt = &t
	}
	return &r, nil
}

// Rules lists userID's rules, expired ones included (the caller sees expires_at).
func (s *Store) Rules(ctx context.Context, userID string) ([]Rule, error) {
	out := []Rule{}
	if !isUUID(userID) {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+ruleCols+` FROM parental_rules WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// AddRule adds a rule to userID's policy, or replaces the expiry of the same rule
// (same effect on the same subject). An allow on a library the child has no ACL
// for is ErrNoLibraryAccess: the library ACL applies first, and a rule never opens
// a library.
func (s *Store) AddRule(ctx context.Context, actorID, userID string, in RuleInput) (*Rule, error) {
	var out *Rule
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = s.addRuleTx(ctx, tx, actorID, userID, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// addRuleTx is AddRule inside the caller's transaction (a request's approval and a
// PIN unlock add their rule with their own change, or not at all).
func (s *Store) addRuleTx(ctx context.Context, tx *sql.Tx, actorID, userID string, in RuleInput) (*Rule, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	if _, err := policyOf(ctx, tx, userID, true); err != nil {
		return nil, err
	}
	if in.Effect == EffectAllow && in.SubjectKind == SubjectLibrary {
		var has bool
		if isUUID(in.SubjectID) {
			if err := tx.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM library_acl WHERE user_id = $1 AND library_id = $2)`,
				userID, in.SubjectID).Scan(&has); err != nil {
				return nil, err
			}
		}
		if !has {
			return nil, ErrNoLibraryAccess
		}
	}
	at := s.now().UTC()
	var exp sql.NullTime
	if in.ExpiresAt != nil {
		exp = sql.NullTime{Valid: true, Time: in.ExpiresAt.UTC()}
	}
	out, err := scanRule(tx.QueryRowContext(ctx,
		`INSERT INTO parental_rules (user_id, effect, subject_kind, subject_id, expires_at, created_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (user_id, subject_kind, subject_id, effect)
		 DO UPDATE SET expires_at = EXCLUDED.expires_at, created_by = EXCLUDED.created_by
		 RETURNING `+ruleCols,
		userID, in.Effect, in.SubjectKind, in.SubjectID, exp, nullUUID(actorID), at))
	if err != nil {
		return nil, err
	}
	if err := bump(ctx, tx, actorID, userID, at); err != nil {
		return nil, err
	}
	if err := audit(ctx, tx, AuditRuleAdded, actorID, userID, nil, ruleAudit(out)); err != nil {
		return nil, err
	}
	return out, nil
}

func ruleAudit(r *Rule) map[string]any {
	m := map[string]any{"rule_id": r.ID, "effect": r.Effect, "subject_kind": r.SubjectKind, "subject_id": r.SubjectID}
	if r.ExpiresAt != nil {
		m["expires_at"] = r.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return m
}

// RemoveRule deletes one of userID's rules. ErrRuleNotFound when it is not theirs.
func (s *Store) RemoveRule(ctx context.Context, actorID, userID, ruleID string) error {
	if !isUUID(ruleID) {
		return ErrRuleNotFound
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := policyOf(ctx, tx, userID, true); err != nil {
			return err
		}
		r, err := scanRule(tx.QueryRowContext(ctx,
			`DELETE FROM parental_rules WHERE id = $1 AND user_id = $2 RETURNING `+ruleCols, ruleID, userID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRuleNotFound
		}
		if err != nil {
			return err
		}
		if err := bump(ctx, tx, actorID, userID, s.now().UTC()); err != nil {
			return err
		}
		return audit(ctx, tx, AuditRuleRemoved, actorID, userID, ruleAudit(r), nil)
	})
}

// Signals lists what each of userID's sign-ins said about their age.
func (s *Store) Signals(ctx context.Context, userID string) ([]Signal, error) {
	out := []Signal{}
	if !isUUID(userID) {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT refresh_family_id, lower, upper, COALESCE(declaration, ''), movie_ceiling, tv_ceiling,
		        COALESCE(region, ''), tier_limit, pending_looser, observed_at
		   FROM session_age_signals WHERE user_id = $1 ORDER BY observed_at DESC, refresh_family_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			sg                             Signal
			lower, upper, movie, tv, limit sql.NullInt64
			pending                        sql.NullString
		)
		if err := rows.Scan(&sg.RefreshFamilyID, &lower, &upper, &sg.Declaration, &movie, &tv,
			&sg.Region, &limit, &pending, &sg.ObservedAt); err != nil {
			return nil, err
		}
		sg.Lower, sg.Upper, sg.MovieCeiling, sg.TVCeiling, sg.TierLimit =
			intPtr(lower), intPtr(upper), intPtr(movie), intPtr(tv), intPtr(limit)
		if pending.Valid && pending.String != "" {
			sg.PendingLooser = json.RawMessage(pending.String)
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

func intPtr(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
