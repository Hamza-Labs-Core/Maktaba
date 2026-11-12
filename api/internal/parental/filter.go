package parental

// The one content filter (Epic 57 story 57.03). Every surface that lists, searches,
// describes, plays, downloads or streams content asks it, in one of two ways:
//
//   - a query over videos adds SQL(From(ctx), alias) next to its library scope;
//   - a single object is checked with Allowed (or AllowedVideo, AllowedVideos).
//
// Both read the request's effective limit, which the gate (gate.go) loads once from
// Cache.For and puts in the context. SQL and Effective.Permits are the same rule;
// TestSQLMatchesPermits holds them together. An account with no limit gets "" from
// SQL and true from Allowed without a query.

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type effectiveKey struct{}

// unrestricted is what From answers where no limit was loaded.
var unrestricted = &Effective{}

// nothing is the limit of a request whose policy could not be read: it shows
// nothing (fail closed).
func nothing(userID string) *Effective {
	return &Effective{UserID: userID, Restricted: true, MaxTier: NothingAllowed, Unrated: UnratedHide, AllowOnly: true}
}

// With is ctx carrying the request's effective limit.
func With(ctx context.Context, e *Effective) context.Context {
	return context.WithValue(ctx, effectiveKey{}, e)
}

// From is the request's effective limit: what the gate loaded, or the unrestricted
// value where no gate ran (a handler mounted alone in a test). Never nil; shared,
// so callers must not change it.
func From(ctx context.Context) *Effective {
	if e, ok := ctx.Value(effectiveKey{}).(*Effective); ok && e != nil {
		return e
	}
	return unrestricted
}

// Limits reports whether e hides anything at all.
func (e *Effective) Limits() bool { return e != nil && e.Restricted }

// CanonicalUUID is s as a uuid column prints it, and whether s is a uuid. Only
// such ids are written into a clause. Postgres reads more spellings of a uuid than
// the canonical one (no hyphens, braces), so an id is always compared in this form.
func CanonicalUUID(s string) (string, bool) {
	u, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// inList is "'a','b'" of the ids that are uuids ("" when none is: a rule on an id
// that cannot be a row's names nothing).
func inList(ids []string) string {
	var b strings.Builder
	for _, id := range ids {
		c, ok := CanonicalUUID(id)
		if !ok {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('\'')
		b.WriteString(c)
		b.WriteByte('\'')
	}
	return b.String()
}

// names reports whether a live rule of effect names s, whatever spelling of the
// uuid the rule was stored with (s.ID is canonical).
func (e *Effective) names(effect string, s Subject, now time.Time) bool {
	for _, id := range e.Subjects(effect, s.Kind, now) {
		if c, ok := CanonicalUUID(id); ok && c == s.ID {
			return true
		}
	}
	return false
}

// subjectTerms is one OR-able term per kind of subject a live rule of effect names,
// each true for a videos row (alias) that is, or sits in, one of them.
func (e *Effective) subjectTerms(effect, alias string, now time.Time) []string {
	var out []string
	if l := inList(e.Subjects(effect, SubjectVideo, now)); l != "" {
		out = append(out, alias+".id IN ("+l+")")
	}
	if l := inList(e.Subjects(effect, SubjectLibrary, now)); l != "" {
		out = append(out, "("+alias+".library_id IS NOT NULL AND "+alias+".library_id IN ("+l+"))")
	}
	if l := inList(e.Subjects(effect, SubjectSeries, now)); l != "" {
		out = append(out, "EXISTS (SELECT 1 FROM series_episodes pf_se WHERE pf_se.video_id = "+alias+".id AND pf_se.series_id IN ("+l+"))")
	}
	if l := inList(e.Subjects(effect, SubjectCollection, now)); l != "" {
		out = append(out, "EXISTS (SELECT 1 FROM collection_items pf_ci WHERE pf_ci.video_id = "+alias+".id AND pf_ci.collection_id IN ("+l+"))")
	}
	if l := inList(e.Subjects(effect, SubjectYouTubeSource, now)); l != "" {
		out = append(out, "EXISTS (SELECT 1 FROM youtube_downloads pf_yd WHERE pf_yd.video_id = "+alias+".id AND pf_yd.source_id IN ("+l+"))")
	}
	return out
}

// tierTerm is the limit and the unrated rule for a videos row.
func (e *Effective) tierTerm(alias string) string {
	if e.AllowOnly || e.MaxTier < TierAll {
		return "FALSE"
	}
	limit := strconv.Itoa(e.MaxTier)
	col := alias + ".age_tier"
	if as, ok := unratedTiers[e.Unrated]; ok && as <= e.MaxTier {
		return "(" + col + " IS NULL OR " + col + " <= " + limit + ")"
	}
	return "(" + col + " IS NOT NULL AND " + col + " <= " + limit + ")"
}

// PredicateAt is the filter as a boolean expression over a videos row named alias,
// as of now: "TRUE" for an account with no limit. It holds no placeholders (tiers
// are numbers, ids are checked uuids), so it drops into any query, on Postgres and
// SQLite alike, without renumbering arguments.
func PredicateAt(e *Effective, alias string, now time.Time) string {
	if !e.Limits() {
		return "TRUE"
	}
	pass := e.tierTerm(alias)
	if allowed := e.subjectTerms(EffectAllow, alias, now); len(allowed) > 0 {
		pass = "(" + strings.Join(allowed, " OR ") + " OR " + pass + ")"
	}
	if blocked := e.subjectTerms(EffectBlock, alias, now); len(blocked) > 0 {
		return "(NOT (" + strings.Join(blocked, " OR ") + ") AND " + pass + ")"
	}
	return pass
}

// Predicate is PredicateAt now.
func Predicate(e *Effective, alias string) string { return PredicateAt(e, alias, time.Now()) }

// SQL is the clause a query over videos (alias) adds beside its library scope:
// " AND <predicate>", or "" for an account with no limit (the query is unchanged).
func SQL(e *Effective, alias string) string {
	if !e.Limits() {
		return ""
	}
	return " AND " + Predicate(e, alias)
}

// Where is SQL for the request's limit.
func Where(ctx context.Context, alias string) string { return SQL(From(ctx), alias) }

// RowQueryer is what Allowed reads through (*sql.DB, *sql.Tx, *sql.Conn).
type RowQueryer interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// AllowedVideo reports whether e may see the video id. A missing video is allowed
// (there is nothing to hide; the caller's own lookup answers it). An error means
// refuse.
func AllowedVideo(ctx context.Context, q RowQueryer, e *Effective, id string) (bool, error) {
	return Allowed(ctx, q, e, SubjectVideo, id)
}

// Allowed reports whether e may see one object: a video, a series (not blocked,
// and at least one episode passes), a collection (not blocked; a hand-made one
// needs an item that passes), a library (not blocked), a channel (its own tier
// and the rules naming it). An id that names no row is allowed: there is nothing
// to hide and the caller's own lookup answers it. An id that is not a uuid is
// refused (Postgres reads spellings of a uuid this check would not recognise), and
// so is every id on an error.
func Allowed(ctx context.Context, q RowQueryer, e *Effective, kind, id string) (bool, error) {
	if !e.Limits() {
		return true, nil
	}
	cid, ok := CanonicalUUID(id)
	if !ok {
		return false, nil
	}
	now := time.Now()
	self := Subject{Kind: kind, ID: cid}
	var query string
	switch kind {
	case SubjectVideo:
		query = `SELECT NOT EXISTS (SELECT 1 FROM videos v WHERE v.id = $1)
		             OR EXISTS (SELECT 1 FROM videos v WHERE v.id = $1 AND ` + PredicateAt(e, "v", now) + `)`
	case SubjectSeries:
		if e.names(EffectBlock, self, now) {
			return false, nil
		}
		query = `SELECT NOT EXISTS (SELECT 1 FROM series s WHERE s.id = $1)
		             OR EXISTS (SELECT 1 FROM series_episodes se JOIN videos v ON v.id = se.video_id
		                         WHERE se.series_id = $1 AND ` + PredicateAt(e, "v", now) + `)`
	case SubjectCollection:
		if e.names(EffectBlock, self, now) {
			return false, nil
		}
		// a smart collection keeps no items: its list is the videos query, filtered
		query = `SELECT NOT EXISTS (SELECT 1 FROM collections c WHERE c.id = $1)
		             OR EXISTS (SELECT 1 FROM collections c WHERE c.id = $1 AND c.is_smart)
		             OR EXISTS (SELECT 1 FROM collection_items ci JOIN videos v ON v.id = ci.video_id
		                         WHERE ci.collection_id = $1 AND ` + PredicateAt(e, "v", now) + `)`
	case SubjectLibrary:
		return !e.names(EffectBlock, self, now), nil
	case SubjectChannel:
		if e.names(EffectBlock, self, now) {
			return false, nil
		}
		if e.names(EffectAllow, self, now) {
			return true, nil
		}
		if e.AllowOnly {
			return false, nil
		}
		var tier sql.NullInt64
		err := q.QueryRowContext(ctx, `SELECT age_tier FROM channels WHERE id = $1`, cid).Scan(&tier)
		if err == sql.ErrNoRows {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return e.TierPermits(intPtr(tier)), nil
	default:
		return false, nil
	}
	var ok2 bool
	if err := q.QueryRowContext(ctx, query, cid).Scan(&ok2); err != nil {
		return false, err
	}
	return ok2, nil
}

// AllowedVideos is the subset of ids e may see, as a set. With no limit every id
// is in it and no query runs. On an error the set is empty.
func AllowedVideos(ctx context.Context, q RowQueryer, e *Effective, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if !e.Limits() {
		for _, id := range ids {
			out[id] = true
		}
		return out, nil
	}
	l := inList(ids)
	if l == "" {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT v.id::text FROM videos v WHERE v.id IN (`+l+`) AND `+Predicate(e, "v"))
	if err != nil {
		return map[string]bool{}, err
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return map[string]bool{}, err
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return map[string]bool{}, err
	}
	for _, id := range ids {
		if c, ok := CanonicalUUID(id); ok && seen[c] {
			out[id] = true
		}
	}
	return out, nil
}

// Check is Allowed for the request's limit, for a handler's single-object path: it
// answers false on an error too (the caller then writes its own missing-id answer)
// and tells the blocked hook.
func Check(ctx context.Context, q RowQueryer, kind, id string) bool {
	e := From(ctx)
	if !e.Limits() {
		return true
	}
	ok, err := Allowed(ctx, q, e, kind, id)
	if err != nil || !ok {
		Refused(ctx, e, Subject{Kind: kind, ID: id})
		return false
	}
	return true
}

// BlockedHook is told of each single item the filter refused a restricted account
// (a by-id read, a stream, a download; never a list that merely left it out).
type BlockedHook func(ctx context.Context, e *Effective, s Subject)

var blockedHook atomic.Pointer[BlockedHook]

// SetBlockedHook installs the hook Refused calls (nil: none). Story 57.08 records
// its `blocked` activity through it; until then nothing is installed.
func SetBlockedHook(h BlockedHook) {
	if h == nil {
		blockedHook.Store(nil)
		return
	}
	blockedHook.Store(&h)
}

// Refused reports that the filter refused s to e's account. Every refusal site
// calls it: the gate's by-id check, Check, stream creation and URL renewal.
func Refused(ctx context.Context, e *Effective, s Subject) {
	if h := blockedHook.Load(); h != nil && e.Limits() {
		(*h)(ctx, e, s)
	}
}
