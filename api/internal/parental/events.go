package parental

// A restricted account's activity, for its guardians (Epic 57 story 57.08). Events
// are written only for an account that has a policy (the insert reads
// parental_policies, so an unrestricted or signal-only account never gets a row),
// hold ids only, are kept 90 days, and go with the policy when a restriction is
// lifted or the account is deleted.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The event kinds. time_up and time_granted are written by the time rules (story
// 57.14).
const (
	EventWatched     = "watched"
	EventBlocked     = "blocked"
	EventAsked       = "asked"
	EventApproved    = "approved"
	EventDenied      = "denied"
	EventPINLocked   = "pin_locked"
	EventSignal      = "signal"
	EventTimeUp      = "time_up"
	EventTimeGranted = "time_granted"
)

// Subject kinds an event has beside a rule's: the guardian whose PIN locked, the
// child's account (its PIN entry closed), a source address (never stored: the
// subject id is empty) and the device a sign-in's age signal came from.
const (
	EventSubjectGuardian = "guardian"
	EventSubjectAccount  = "account"
	EventSubjectSource   = "source"
	EventSubjectDevice   = "device"
)

const (
	// EventsRetention is how long an event is kept.
	EventsRetention = 90 * 24 * time.Hour
	// EventsPageSize is a page of GET /api/users/{id}/parental/activity.
	EventsPageSize = 50
	// WatchedAfterSec and WatchedAfterPercent are when a play counts as watched.
	WatchedAfterSec     = 60
	WatchedAfterPercent = 20
	// watchedLookback bounds the plays RecordWatched reads: a play older than this
	// has its event already, or is past noting.
	watchedLookback = 48 * time.Hour
)

// ErrBadCursor is a cursor this package did not issue.
var ErrBadCursor = errors.New("parental: bad cursor")

// ValidEventKind reports whether k is a kind an event may have.
func ValidEventKind(k string) bool {
	switch k {
	case EventWatched, EventBlocked, EventAsked, EventApproved, EventDenied, EventPINLocked,
		EventSignal, EventTimeUp, EventTimeGranted:
		return true
	}
	return false
}

// Event is one row of a child's activity.
type Event struct {
	ID          string
	ChildID     string
	Kind        string
	SubjectKind string
	SubjectID   string
	// ActorID is the guardian an approved, denied or pin_locked event names ("" for
	// none).
	ActorID string
	At      time.Time
}

// recordEvent writes one event for childID if that account is restricted. With
// dedupe set, a second event of the same kind and key is not written.
func recordEvent(ctx context.Context, q queryer, ev Event, dedupe string) error {
	if !isUUID(ev.ChildID) || !ValidEventKind(ev.Kind) {
		return nil
	}
	var key sql.NullString
	if dedupe != "" {
		key = sql.NullString{Valid: true, String: dedupe}
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, actor_id, dedupe, at)
		 SELECT pp.user_id, $2, $3, $4, $5, $6, $7 FROM parental_policies pp WHERE pp.user_id = $1
		 ON CONFLICT (child_id, kind, dedupe) WHERE dedupe IS NOT NULL DO NOTHING`,
		ev.ChildID, ev.Kind, ev.SubjectKind, ev.SubjectID, nullUUID(ev.ActorID), key, ev.At.UTC())
	return err
}

// RecordBlocked notes that a restricted account tried to open something its policy
// hides: one event per subject per day (the family's day, which ends at 04:00 home
// time). It does nothing for an account that is not restricted. The content filter
// (story 57.03) calls it where it refuses an item.
func (s *Store) RecordBlocked(ctx context.Context, userID, subjectKind, subjectID string) error {
	if !ValidSubjectKind(subjectKind) || subjectID == "" {
		return nil
	}
	now := s.now()
	day := HomeDay(now, s.HomeLocation(ctx))
	return recordEvent(ctx, s.DB, Event{ChildID: userID, Kind: EventBlocked, SubjectKind: subjectKind,
		SubjectID: subjectID, At: now}, day+"|"+subjectKind+"|"+subjectID)
}

// RecordSignalEvent notes that a restricted account's sign-in declared a looser age
// than before; deviceName is that sign-in's device name. It does nothing for an
// account that is not restricted (a signal-only account has no activity). The
// device signal (story 57.04) calls it.
func (s *Store) RecordSignalEvent(ctx context.Context, userID, deviceName string) error {
	if len(deviceName) > 200 {
		deviceName = deviceName[:200]
	}
	return recordEvent(ctx, s.DB, Event{ChildID: userID, Kind: EventSignal, SubjectKind: EventSubjectDevice,
		SubjectID: deviceName, At: s.now()}, "")
}

// RecordWatched writes a watched event for every play of a restricted account
// that reached 60 seconds or 20 percent, once per play. It reads watch_sessions,
// the one tracker of plays; the activity loop runs it every minute.
func (s *Store) RecordWatched(ctx context.Context) (int64, error) {
	now := s.now().UTC()
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO parental_events (child_id, kind, subject_kind, subject_id, dedupe, at)
		 SELECT ws.user_id, 'watched', 'video', ws.video_id::text, 'play|' || ws.id::text, $1
		   FROM watch_sessions ws JOIN parental_policies pp ON pp.user_id = ws.user_id
		  WHERE ws.started_at > $2 AND (ws.duration_sec >= $3 OR ws.percent_complete >= $4)
		 ON CONFLICT (child_id, kind, dedupe) WHERE dedupe IS NOT NULL DO NOTHING`,
		now, now.Add(-watchedLookback), WatchedAfterSec, WatchedAfterPercent)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SweepEvents deletes the events older than 90 days, and the wrong PIN tries older
// than a day.
func (s *Store) SweepEvents(ctx context.Context) (int64, error) {
	now := s.now().UTC()
	res, err := s.DB.ExecContext(ctx, `DELETE FROM parental_events WHERE at < $1`, now.Add(-EventsRetention))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	_, err = s.DB.ExecContext(ctx, `DELETE FROM parental_pin_failures WHERE at < $1`, now.Add(-24*time.Hour))
	return n, err
}

// ActivityItem is one event as a guardian reads it.
type ActivityItem struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	// Subject is what the event is about; absent for an event with none.
	Subject *SubjectInfo `json:"subject,omitempty"`
	// Guardian is the guardian the event names (approved, denied, pin_locked).
	Guardian *Guardian `json:"guardian,omitempty"`
	// Device is the device name of a signal event.
	Device string `json:"device,omitempty"`
}

// ActivityPage is a page of a child's activity, newest first.
type ActivityPage struct {
	Events []ActivityItem `json:"events"`
	// NextCursor is the cursor of the next page; absent on the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok || !isUUID(id) {
		return time.Time{}, "", ErrBadCursor
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", ErrBadCursor
	}
	return at, id, nil
}

// Activity lists childID's events, newest first, 50 a page, from cursor ("" for
// the first page), of one kind when kind is set. Each carries its subject as it is
// now: a deleted one comes back Removed. ErrNotRestricted when the account has no
// policy; ErrInvalid for an unknown kind; ErrBadCursor for a bad cursor.
func (s *Store) Activity(ctx context.Context, childID, kind, cursor string) (*ActivityPage, error) {
	if kind != "" && !ValidEventKind(kind) {
		return nil, fmt.Errorf("%w: kind", ErrInvalid)
	}
	if _, err := s.Policy(ctx, childID); err != nil {
		return nil, err
	}
	args := []any{childID, s.now().UTC().Add(-EventsRetention)}
	where := `e.child_id = $1 AND e.at >= $2`
	if kind != "" {
		args = append(args, kind)
		where += fmt.Sprintf(` AND e.kind = $%d`, len(args))
	}
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		args = append(args, at, id)
		where += fmt.Sprintf(` AND (e.at, e.id) < ($%d, $%d::uuid)`, len(args)-1, len(args))
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT e.id, e.kind, e.subject_kind, e.subject_id, COALESCE(e.actor_id::text, ''), COALESCE(u.username, ''), e.at
		   FROM parental_events e LEFT JOIN users u ON u.id = e.actor_id
		  WHERE `+where+` ORDER BY e.at DESC, e.id DESC LIMIT `+fmt.Sprint(EventsPageSize+1), args...)
	if err != nil {
		return nil, err
	}
	type row struct {
		item                 ActivityItem
		subjectKind, subject string
	}
	var list []row
	for rows.Next() {
		var (
			r               row
			actor, username string
		)
		if err := rows.Scan(&r.item.ID, &r.item.Kind, &r.subjectKind, &r.subject, &actor, &username, &r.item.At); err != nil {
			rows.Close()
			return nil, err
		}
		if actor != "" {
			r.item.Guardian = &Guardian{ID: actor, Username: username}
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page := &ActivityPage{Events: []ActivityItem{}}
	if len(list) > EventsPageSize {
		list = list[:EventsPageSize]
		last := list[len(list)-1].item
		page.NextCursor = encodeCursor(last.At, last.ID)
	}
	seen := map[Subject]SubjectInfo{}
	for _, r := range list {
		switch {
		case r.subjectKind == EventSubjectDevice:
			r.item.Device = r.subject
		case ValidSubjectKind(r.subjectKind):
			key := Subject{Kind: r.subjectKind, ID: r.subject}
			info, ok := seen[key]
			if !ok {
				if info, err = subjectOf(ctx, s.DB, r.subjectKind, r.subject); err != nil {
					return nil, err
				}
				seen[key] = info
			}
			r.item.Subject = &info
		}
		page.Events = append(page.Events, r.item)
	}
	return page, nil
}
