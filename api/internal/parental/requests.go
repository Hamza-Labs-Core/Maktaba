package parental

// Ask a parent (Epic 57 story 57.06): a restricted account asks for one thing its
// policy hides, and a guardian approves (an allow rule, added through the same
// code as a rule a guardian adds by hand) or denies. A request holds ids only.
//
// A request of kind `time` (story 57.14) asks for more minutes today instead: it
// has no subject (its subject_id is the minutes asked for), it waits only until
// the home day ends, and approving it is a time grant, not a rule.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// A request's statuses.
const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestDenied   = "denied"
	RequestExpired  = "expired"
)

const (
	// MaxPendingRequests is how many requests one child may have waiting.
	MaxPendingRequests = 20
	// RequestTTL is how long a request waits before it expires.
	RequestTTL = 7 * 24 * time.Hour
)

var (
	// ErrRequestNotFound is a request that is not there (or not the caller's to see).
	ErrRequestNotFound = errors.New("parental: request not found")
	// ErrRequestDecided is a request already approved or denied.
	ErrRequestDecided = errors.New("parental: request already decided")
	// ErrRequestExpired is a request that waited too long, or whose subject is gone.
	ErrRequestExpired = errors.New("parental: request expired")
	// ErrTooManyRequests is a child at MaxPendingRequests.
	ErrTooManyRequests = errors.New("parental: too many pending requests")
	// ErrSubjectNotFound is a subject that is not there, or sits in a library the
	// child cannot open (the two are answered alike).
	ErrSubjectNotFound = errors.New("parental: subject not found")
)

// RequestTime is the kind of a request for more time today (story 57.14). It is
// not a subject kind: no rule can name it.
const RequestTime = "time"

// ValidRequestKind reports whether a child may ask for a subject of kind k: every
// kind a rule can name, and `time`, which has no subject and is approved as a
// time grant, not a rule.
func ValidRequestKind(k string) bool { return k == RequestTime || ValidSubjectKind(k) }

// RequestMinutes reads the subject_id of a stored `time` request: the minutes
// asked for, a whole number from 1 to MaxGrantMinutes written plainly ("30", not
// "+30" or "030"). A new request is held to AskedMinutes.
func RequestMinutes(id string) (int, bool) {
	n, err := strconv.Atoi(id)
	if err != nil || n < 1 || n > MaxGrantMinutes || strconv.Itoa(n) != id {
		return 0, false
	}
	return n, true
}

// ChildMinutes are the minutes a child's own device may ask for, or have unlocked
// with a guardian's PIN typed on it (story 57.06). What the device sends is the
// child's to change, so nothing else is taken from it: a guardian who wants to
// give another number does it from their own sign-in (approve, or a time grant),
// where 1 to MaxGrantMinutes holds.
var ChildMinutes = []int{15, 30, 60}

// AskedMinutes reads the subject_id of a `time` request or PIN unlock coming from
// the child's device: one of ChildMinutes, written plainly.
func AskedMinutes(id string) (int, bool) {
	n, ok := RequestMinutes(id)
	if !ok || !slices.Contains(ChildMinutes, n) {
		return 0, false
	}
	return n, true
}

// ValidRequestStatus reports whether st is a status a request may have.
func ValidRequestStatus(st string) bool {
	switch st {
	case RequestPending, RequestApproved, RequestDenied, RequestExpired:
		return true
	}
	return false
}

// Request is one thing a child asked for.
type Request struct {
	ID          string `json:"id"`
	ChildID     string `json:"child_id"`
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	// Minutes is the minutes a `time` request asks for (its subject_id as a
	// number); absent for every other kind.
	Minutes   int        `json:"minutes,omitempty"`
	Status    string     `json:"status"`
	DecidedBy string     `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// ExpiresAt is when a waiting request expires: seven days after it was asked,
	// or, for a `time` request, the end of the home day it was asked on.
	ExpiresAt time.Time `json:"expires_at"`
}

// fit sets what only a `time` request has: its minutes, and an expiry at the end
// of the home day (04:00 in loc) it was asked on.
func (r *Request) fit(loc *time.Location) {
	if r.SubjectKind != RequestTime {
		return
	}
	r.Minutes, _ = RequestMinutes(r.SubjectID)
	if end := DayEnd(r.CreatedAt, loc); end.Before(r.ExpiresAt) {
		r.ExpiresAt = end.UTC()
	}
}

const requestCols = `id, child_id, subject_kind, subject_id, status, COALESCE(decided_by::text, ''), decided_at, created_at`

func scanRequest(row interface{ Scan(...any) error }) (*Request, error) {
	var (
		r  Request
		at sql.NullTime
	)
	if err := row.Scan(&r.ID, &r.ChildID, &r.SubjectKind, &r.SubjectID, &r.Status, &r.DecidedBy, &at, &r.CreatedAt); err != nil {
		return nil, err
	}
	if at.Valid {
		t := at.Time
		r.DecidedAt = &t
	}
	r.ExpiresAt = r.CreatedAt.Add(RequestTTL)
	return &r, nil
}

// Ask records that childID asks for a subject. all and libraries are the
// libraries the child's sign-in can open: a subject outside them is answered as
// missing. Asking again for the same thing while a request waits returns that
// request (created false). ErrNotRestricted when the account has no policy,
// ErrInvalid for a bad kind or id, ErrSubjectNotFound, ErrTooManyRequests.
//
// A `time` request (id: the minutes, one of ChildMinutes) has no subject to look
// up. A child has one waiting at most: asking again, for any number of minutes,
// returns the waiting one.
func (s *Store) Ask(ctx context.Context, childID string, all bool, libraries []string, kind, id string) (req *Request, created bool, err error) {
	if !ValidRequestKind(kind) {
		return nil, false, fmt.Errorf("%w: subject_kind", ErrInvalid)
	}
	forTime := kind == RequestTime
	if forTime {
		if _, ok := AskedMinutes(id); !ok {
			return nil, false, fmt.Errorf("%w: subject_id", ErrInvalid)
		}
	} else if (RuleInput{Effect: EffectAllow, SubjectKind: kind, SubjectID: id}).validate() != nil {
		return nil, false, fmt.Errorf("%w: subject_id", ErrInvalid)
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		// the policy row is locked, so two asks of one child run one after the other
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		now := s.now().UTC()
		var loc *time.Location
		if forTime {
			if loc, err = homeLocation(ctx, tx, now); err != nil {
				return err
			}
		} else {
			info, err := subjectOf(ctx, tx, kind, id)
			if err != nil {
				return err
			}
			if info.Removed || info.For(all, libraries).Hidden {
				return ErrSubjectNotFound
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE parental_requests SET status = 'expired', decided_at = $2
			  WHERE child_id = $1 AND status = 'pending' AND created_at <= $3`,
			childID, now, now.Add(-RequestTTL)); err != nil {
			return err
		}
		if forTime {
			// yesterday's request for more time is over with its day
			if _, err := tx.ExecContext(ctx,
				`UPDATE parental_requests SET status = 'expired', decided_at = $2
				  WHERE child_id = $1 AND status = 'pending' AND subject_kind = 'time' AND created_at < $3`,
				childID, now, DayStart(now, loc)); err != nil {
				return err
			}
			req, err = scanRequest(tx.QueryRowContext(ctx,
				`SELECT `+requestCols+` FROM parental_requests
				  WHERE child_id = $1 AND subject_kind = 'time' AND status = 'pending'
				  ORDER BY created_at, id LIMIT 1`, childID))
		} else {
			req, err = scanRequest(tx.QueryRowContext(ctx,
				`SELECT `+requestCols+` FROM parental_requests
				  WHERE child_id = $1 AND subject_kind = $2 AND subject_id = $3 AND status = 'pending'`,
				childID, kind, id))
		}
		if err == nil {
			req.fit(loc)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var waiting int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM parental_requests WHERE child_id = $1 AND status = 'pending'`, childID).Scan(&waiting); err != nil {
			return err
		}
		if waiting >= MaxPendingRequests {
			return ErrTooManyRequests
		}
		req, err = scanRequest(tx.QueryRowContext(ctx,
			`INSERT INTO parental_requests (child_id, subject_kind, subject_id, created_at)
			 VALUES ($1, $2, $3, $4) RETURNING `+requestCols, childID, kind, id, now))
		if err != nil {
			return err
		}
		req.fit(loc)
		created = true
		return recordEvent(ctx, tx, Event{ChildID: childID, Kind: EventAsked, SubjectKind: kind, SubjectID: id, At: now}, "")
	})
	if err != nil {
		return nil, false, err
	}
	return req, created, nil
}

// RequestItem is a request as a guardian's list shows it.
type RequestItem struct {
	Request
	Child   ChildRef    `json:"child"`
	Subject SubjectInfo `json:"subject"`
}

// ChildRef names a child.
type ChildRef struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// Requests lists the requests of status (RequestPending when "") of the children
// guardianID looks after, or of every child when all is set (an admin), oldest
// first. A waiting request past its seven days, and a `time` request whose home
// day has ended, is listed as expired, not pending. A `time` request has no
// subject to load: its Subject carries the kind and the minutes only.
func (s *Store) Requests(ctx context.Context, guardianID string, all bool, status string) ([]RequestItem, error) {
	if status == "" {
		status = RequestPending
	}
	if !ValidRequestStatus(status) {
		return nil, fmt.Errorf("%w: status", ErrInvalid)
	}
	out := []RequestItem{}
	now := s.now().UTC()
	cutoff := now.Add(-RequestTTL)
	loc, err := homeLocation(ctx, s.DB, now)
	if err != nil {
		return nil, err
	}
	day := DayStart(now, loc)
	q := `SELECT r.id, r.child_id, r.subject_kind, r.subject_id, r.status, COALESCE(r.decided_by::text, ''),
	             r.decided_at, r.created_at, u.username
	        FROM parental_requests r JOIN users u ON u.id = r.child_id WHERE `
	var args []any
	switch status {
	case RequestPending:
		args = append(args, cutoff, day)
		q += `r.status = 'pending' AND r.created_at > $1 AND (r.subject_kind <> 'time' OR r.created_at >= $2)`
	case RequestExpired:
		args = append(args, cutoff, day)
		q += `(r.status = 'expired' OR (r.status = 'pending'
		         AND (r.created_at <= $1 OR (r.subject_kind = 'time' AND r.created_at < $2))))`
	default:
		args = append(args, status)
		q += `r.status = $1`
	}
	if !all {
		if !isUUID(guardianID) {
			return out, nil
		}
		args = append(args, guardianID)
		q += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM parental_guardians g WHERE g.child_id = r.child_id AND g.guardian_id = $%d)`, len(args))
	}
	q += ` ORDER BY r.created_at, r.id LIMIT 500`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var (
			it RequestItem
			at sql.NullTime
		)
		if err := rows.Scan(&it.ID, &it.ChildID, &it.SubjectKind, &it.SubjectID, &it.Status, &it.DecidedBy,
			&at, &it.CreatedAt, &it.Child.Username); err != nil {
			rows.Close()
			return nil, err
		}
		if at.Valid {
			t := at.Time
			it.DecidedAt = &t
		}
		it.ExpiresAt = it.CreatedAt.Add(RequestTTL)
		it.fit(loc)
		it.Status = status
		it.Child.ID = it.ChildID
		out = append(out, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].SubjectKind == RequestTime {
			out[i].Subject = SubjectInfo{Kind: RequestTime, ID: out[i].SubjectID}
			continue
		}
		if out[i].Subject, err = subjectOf(ctx, s.DB, out[i].SubjectKind, out[i].SubjectID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RequestChild returns the child a request belongs to, or ErrRequestNotFound.
func (s *Store) RequestChild(ctx context.Context, id string) (string, error) {
	if !isUUID(id) {
		return "", ErrRequestNotFound
	}
	var child string
	err := s.DB.QueryRowContext(ctx, `SELECT child_id FROM parental_requests WHERE id = $1`, id).Scan(&child)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRequestNotFound
	}
	return child, err
}

// Decision is what Approve and Deny return.
type Decision struct {
	Request *Request
	// Rule is the allow rule an approval added.
	Rule *Rule
	// Grant is the time an approved `time` request gave.
	Grant *TimeGrant
}

// Approval is how a request is approved.
type Approval struct {
	// ExpiresAt, when set, is when the allow rule ends. Not for a `time` request.
	ExpiresAt *time.Time
	// Minutes, when set, is how many minutes a `time` request is given (1 to
	// MaxGrantMinutes) instead of the minutes asked for. Only for a `time` request.
	Minutes *int
}

// Approve approves a waiting request: the subject is allowed for the child, until
// expiresAt when it is set, and the request is closed. The caller has checked
// that actorID may manage the request's child. ErrRequestNotFound,
// ErrRequestDecided (already approved or denied), ErrRequestExpired (it waited
// too long or its subject is gone: it is closed as expired, and nothing is
// allowed), ErrInvalid (an expiry in the past), ErrNoLibraryAccess.
func (s *Store) Approve(ctx context.Context, actorID, id string, expiresAt *time.Time) (*Decision, error) {
	return s.decide(ctx, actorID, id, true, Approval{ExpiresAt: expiresAt})
}

// ApproveWith is Approve with everything an approval can say. A `time` request
// gives the child a.Minutes (or the minutes asked for) more for the current home
// day, in the transaction that closes the request, and the decision carries the
// grant; the caller then tells the time keeper (TimeKeeper.Refresh). ErrInvalid
// names the field: `expires_at` on a `time` request, `minutes` on any other or out
// of range.
func (s *Store) ApproveWith(ctx context.Context, actorID, id string, a Approval) (*Decision, error) {
	return s.decide(ctx, actorID, id, true, a)
}

// Deny denies a waiting request. Errors as Approve.
func (s *Store) Deny(ctx context.Context, actorID, id string) (*Decision, error) {
	return s.decide(ctx, actorID, id, false, Approval{})
}

func (s *Store) decide(ctx context.Context, actorID, id string, approve bool, a Approval) (*Decision, error) {
	if !isUUID(id) {
		return nil, ErrRequestNotFound
	}
	now := s.now().UTC()
	if approve && a.ExpiresAt != nil && !a.ExpiresAt.After(now) {
		return nil, fmt.Errorf("%w: expires_at", ErrInvalid)
	}
	if approve && a.Minutes != nil && (*a.Minutes < 1 || *a.Minutes > MaxGrantMinutes) {
		return nil, fmt.Errorf("%w: minutes", ErrInvalid)
	}
	out := &Decision{}
	// expired is set when the request is closed as expired: that change is kept
	// (the transaction commits) and the caller is then told.
	expired := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		// The child's policy row is locked before the request's, the order Unlock
		// and Ask take them in (a PIN unlock locks the policy and then closes the
		// waiting requests). Taken the other way round, a guardian approving while
		// another types the PIN for the same item is a deadlock, and the database
		// rolls one of them back. The child id is read without a lock first; a
		// request's child never changes.
		var child string
		err := tx.QueryRowContext(ctx, `SELECT child_id::text FROM parental_requests WHERE id = $1`, id).Scan(&child)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		// a child with no policy left has no row to lock, and no Unlock to meet
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM parental_policies WHERE user_id = $1 FOR UPDATE`, child); err != nil {
			return err
		}
		req, err := scanRequest(tx.QueryRowContext(ctx,
			`SELECT `+requestCols+` FROM parental_requests WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRequestNotFound
		}
		if err != nil {
			return err
		}
		switch req.Status {
		case RequestPending:
		case RequestExpired:
			return ErrRequestExpired
		default:
			return ErrRequestDecided
		}
		forTime := req.SubjectKind == RequestTime
		var loc *time.Location
		if forTime {
			if loc, err = homeLocation(ctx, tx, now); err != nil {
				return err
			}
			req.fit(loc)
		}
		if approve {
			// what the approval says must fit the kind, whatever state the request is in
			if forTime && a.ExpiresAt != nil {
				return fmt.Errorf("%w: expires_at", ErrInvalid)
			}
			if !forTime && a.Minutes != nil {
				return fmt.Errorf("%w: minutes", ErrInvalid)
			}
		}
		status, kind := RequestDenied, EventDenied
		if approve {
			status, kind = RequestApproved, EventApproved
		}
		gone := !req.ExpiresAt.After(now)
		if !gone && approve && !forTime {
			info, err := subjectOf(ctx, tx, req.SubjectKind, req.SubjectID)
			if err != nil {
				return err
			}
			gone = info.Removed
		}
		if gone {
			expired = true
			_, err := tx.ExecContext(ctx,
				`UPDATE parental_requests SET status = 'expired', decided_at = $2 WHERE id = $1`, id, now)
			return err
		}
		if approve {
			// One case per kind of request: more time is a grant, anything else is
			// a subject a rule can name.
			switch req.SubjectKind {
			case RequestTime:
				minutes := req.Minutes
				if a.Minutes != nil {
					minutes = *a.Minutes
				}
				if minutes < 1 || minutes > MaxGrantMinutes {
					return fmt.Errorf("%w: minutes", ErrInvalid)
				}
				if out.Grant, err = grantTimeTx(ctx, tx, now, actorID, req.ChildID, minutes); err != nil {
					return err
				}
			default:
				out.Rule, err = s.addRuleTx(ctx, tx, actorID, req.ChildID, RuleInput{Effect: EffectAllow,
					SubjectKind: req.SubjectKind, SubjectID: req.SubjectID, ExpiresAt: a.ExpiresAt})
				if err != nil {
					return err
				}
			}
		}
		out.Request, err = scanRequest(tx.QueryRowContext(ctx,
			`UPDATE parental_requests SET status = $2, decided_by = $3, decided_at = $4 WHERE id = $1
			 RETURNING `+requestCols, id, status, nullUUID(actorID), now))
		if err != nil {
			return err
		}
		if forTime {
			out.Request.fit(loc)
		}
		return recordEvent(ctx, tx, Event{ChildID: req.ChildID, Kind: kind, SubjectKind: req.SubjectKind,
			SubjectID: req.SubjectID, ActorID: actorID, At: now}, "")
	})
	if err != nil {
		return nil, err
	}
	if expired {
		return nil, ErrRequestExpired
	}
	return out, nil
}

// requestGone are the waiting requests whose subject was deleted, by kind. A
// `time` request has no subject, so it is not here: it ends by age only.
var requestGone = map[string]string{
	SubjectVideo:         `SELECT 1 FROM videos x WHERE x.id::text = r.subject_id`,
	SubjectSeries:        `SELECT 1 FROM series x WHERE x.id::text = r.subject_id`,
	SubjectCollection:    `SELECT 1 FROM collections x WHERE x.id::text = r.subject_id`,
	SubjectLibrary:       `SELECT 1 FROM libraries x WHERE x.id::text = r.subject_id`,
	SubjectChannel:       `SELECT 1 FROM channels x WHERE x.id::text = r.subject_id`,
	SubjectYouTubeSource: `SELECT 1 FROM youtube_sources x WHERE x.id::text = r.subject_id`,
	SubjectPlugin:        `SELECT 1 FROM plugin_installs x WHERE x.plugin_id = r.subject_id`,
}

// CloseRequests closes the waiting requests no one needs to answer: the ones
// older than seven days, the ones whose subject was deleted and the requests for
// more time whose home day has ended become expired;
// a video the child can now see anyway (the limit was raised, or the video was
// rated lower) becomes approved, with no rule and no guardian named. The activity
// loop runs it every minute. It returns how many it closed.
//
// The last case is decided narrowly: only for a video, only by the limit and the
// unrated rule, and only for a child with no live block rule and no allow-only
// policy. Anything else waits for a guardian.
func (s *Store) CloseRequests(ctx context.Context) (int64, error) {
	now := s.now().UTC()
	var closed int64
	res, err := s.DB.ExecContext(ctx,
		`UPDATE parental_requests SET status = 'expired', decided_at = $1
		  WHERE status = 'pending' AND created_at <= $2`, now, now.Add(-RequestTTL))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	closed += n
	for kind, exists := range requestGone {
		res, err := s.DB.ExecContext(ctx,
			`UPDATE parental_requests r SET status = 'expired', decided_at = $1
			  WHERE r.status = 'pending' AND r.subject_kind = $2 AND NOT EXISTS (`+exists+`)`, now, kind)
		if err != nil {
			return closed, err
		}
		n, _ := res.RowsAffected()
		closed += n
	}
	// more time is for the day it was asked on: at the day line the request is over
	loc, err := homeLocation(ctx, s.DB, now)
	if err != nil {
		return closed, err
	}
	res, err = s.DB.ExecContext(ctx,
		`UPDATE parental_requests SET status = 'expired', decided_at = $1
		  WHERE status = 'pending' AND subject_kind = 'time' AND created_at < $2`, now, DayStart(now, loc))
	if err != nil {
		return closed, err
	}
	n, _ = res.RowsAffected()
	closed += n

	rows, err := s.DB.QueryContext(ctx,
		`SELECT r.id, r.child_id, v.age_tier
		   FROM parental_requests r JOIN videos v ON v.id::text = r.subject_id
		  WHERE r.status = 'pending' AND r.subject_kind = 'video' ORDER BY r.child_id LIMIT 1000`)
	if err != nil {
		return closed, err
	}
	type waiting struct {
		id, child string
		tier      *int
	}
	var list []waiting
	for rows.Next() {
		var (
			w    waiting
			tier sql.NullInt64
		)
		if err := rows.Scan(&w.id, &w.child, &tier); err != nil {
			rows.Close()
			return closed, err
		}
		w.tier = intPtr(tier)
		list = append(list, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return closed, err
	}
	policies := map[string]*Effective{}
	for _, w := range list {
		eff, ok := policies[w.child]
		if !ok {
			if eff, err = s.Effective(ctx, w.child); err != nil {
				return closed, err
			}
			policies[w.child] = eff
		}
		if !eff.Restricted || eff.AllowOnly || !eff.TierPermits(w.tier) || hasLiveBlock(eff, now) {
			continue
		}
		res, err := s.DB.ExecContext(ctx,
			`UPDATE parental_requests SET status = 'approved', decided_at = $2 WHERE id = $1 AND status = 'pending'`, w.id, now)
		if err != nil {
			return closed, err
		}
		n, _ := res.RowsAffected()
		closed += n
	}
	return closed, nil
}

func hasLiveBlock(e *Effective, now time.Time) bool {
	for i := range e.Rules {
		r := &e.Rules[i]
		if r.Effect == EffectBlock && (r.ExpiresAt == nil || r.ExpiresAt.After(now)) {
			return true
		}
	}
	return false
}
