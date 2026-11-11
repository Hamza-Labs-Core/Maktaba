package parental

// Time limits and time slots (Epic 57 story 57.14, slot 0223). The home counts a
// restricted account's watching time and decides, on its own clock and in its own
// time zone; a device's clock is never read.
//
//	a rule      days (bit 0 Sunday to bit 6 Saturday), an optional slot, an optional
//	            daily limit, an optional library
//	a home day  04:00 home time to 04:00 the next morning, named by the date it starts
//	            on; a rule's days are home days, so a late evening is one day
//	a slot      times of day in home time; an end before the start crosses midnight
//	combining   every rule that covers the moment (and the library) must allow it
//	a grant     minutes for one home day; they are used only while the rules alone
//	            would refuse (the limit is reached or the slot is closed)
//
// EvaluateTime is the whole decision and reads nothing; the store loads what it needs.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	// DayLineHour is the home hour a day ends and the next begins.
	DayLineHour = 4
	// HeartbeatCap is the most one progress post adds to the count.
	HeartbeatCap = 60 * time.Second

	ReasonOutsideSlot  = "outside_slot"
	ReasonLimitReached = "limit_reached"

	// HomeTimezoneKey is the app_settings key of the home's time zone: an IANA name,
	// or the object SetHomeTimezone writes. Unset or unknown: the server's zone.
	HomeTimezoneKey = "home.timezone"

	// NoLibrary is the library id usage is kept under for playback with no library.
	NoLibrary = "00000000-0000-0000-0000-000000000000"

	// MaxTimeRules bounds one account's rules; MaxGrantMinutes one grant.
	MaxTimeRules    = 50
	MaxGrantMinutes = 480

	AuditTimeRulesSet = "parental.time.rules.set"
	AuditTimeGranted  = "parental.time.granted"

	allDays = 127
)

// TimeRule is one time rule of a restricted account.
type TimeRule struct {
	ID string `json:"id,omitempty"`
	// LibraryID: the rule covers only this library ("": the account).
	LibraryID string `json:"library_id,omitempty"`
	// Days is a bitmask of the home days covered: bit 0 Sunday to bit 6 Saturday.
	Days int `json:"days"`
	// SlotStart and SlotEnd ("HH:MM", both or neither): watching is allowed only
	// from the start to the end; an end before the start crosses midnight.
	SlotStart string `json:"slot_start,omitempty"`
	SlotEnd   string `json:"slot_end,omitempty"`
	// DailyMinutes: the most watched on one home day (nil: no limit).
	DailyMinutes *int       `json:"daily_minutes,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

// parseClock reads "HH:MM" (or "HH:MM:00") as minutes after midnight.
func parseClock(s string) (int, bool) {
	if len(s) == 8 && s[5:] == ":00" {
		s = s[:5]
	}
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h, m := int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// slot returns the rule's slot in minutes after midnight; ok false when it has none.
func (r *TimeRule) slot() (start, end int, ok bool) {
	if r.SlotStart == "" && r.SlotEnd == "" {
		return 0, 0, false
	}
	start, ok1 := parseClock(r.SlotStart)
	end, ok2 := parseClock(r.SlotEnd)
	return start, end, ok1 && ok2
}

func (r *TimeRule) validate() error {
	if r.Days < 1 || r.Days > allDays {
		return fmt.Errorf("%w: days", ErrInvalid)
	}
	if (r.SlotStart == "") != (r.SlotEnd == "") {
		return fmt.Errorf("%w: slot_start and slot_end go together", ErrInvalid)
	}
	if r.SlotStart != "" {
		s, e, ok := r.slot()
		if !ok || s == e {
			return fmt.Errorf("%w: slot", ErrInvalid)
		}
	}
	if r.DailyMinutes != nil && (*r.DailyMinutes < 0 || *r.DailyMinutes > 1440) {
		return fmt.Errorf("%w: daily_minutes", ErrInvalid)
	}
	if r.SlotStart == "" && r.DailyMinutes == nil {
		return fmt.Errorf("%w: a rule needs a slot or a daily limit", ErrInvalid)
	}
	if r.LibraryID != "" && !isUUID(r.LibraryID) {
		return fmt.Errorf("%w: library_id", ErrInvalid)
	}
	return nil
}

// TimeState is the home's answer for one account, library and moment.
type TimeState struct {
	// Allowed: playback may start or go on.
	Allowed bool
	// Limited: a rule covers this moment, so Remaining is a real countdown.
	Limited bool
	// Remaining is how long playback may go on from now (0 when not allowed).
	Remaining time.Duration
	// Reason is ReasonOutsideSlot or ReasonLimitReached when not allowed.
	Reason string
	// EndsAt is when a stream running now must end. Nil: the account has no time
	// rule, so nothing ends it.
	EndsAt *time.Time
	// SlotEndsAt is when the open slot closes (nil: no slot rule covers now).
	SlotEndsAt *time.Time
	// NextAllowedAt is when the rules next allow playback (nil when allowed, or
	// when no moment in the next eight days is allowed).
	NextAllowedAt *time.Time
	// Now is the home's clock for this answer, in the home's zone.
	Now time.Time
}

// DayStart returns the start (04:00 home time) of the home day t falls in.
func DayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	d := time.Date(l.Year(), l.Month(), l.Day(), DayLineHour, 0, 0, 0, loc)
	if l.Before(d) {
		d = time.Date(l.Year(), l.Month(), l.Day()-1, DayLineHour, 0, 0, 0, loc)
	}
	return d
}

// nextDayStart is the day line after the home day that starts at start.
func nextDayStart(start time.Time, loc *time.Location) time.Time {
	return time.Date(start.Year(), start.Month(), start.Day()+1, DayLineHour, 0, 0, 0, loc)
}

// DayKey names the home day t falls in: the date it starts on.
func DayKey(t time.Time, loc *time.Location) string {
	return DayStart(t, loc).Format("2006-01-02")
}

func (r *TimeRule) covers(libraryID string) bool {
	return r.LibraryID == "" || (libraryID != "" && r.LibraryID == libraryID)
}

type timeBase struct {
	covered bool          // a rule covers this moment
	remain  time.Duration // until the rules refuse, never past the day line
	reason  string
	slotEnd time.Time // the earliest end of the open slots
}

// baseAt applies the rules alone (no grant) at t. used is the seconds watched on
// t's home day by library id.
func baseAt(rules []TimeRule, loc *time.Location, t time.Time, libraryID string, used map[string]int) timeBase {
	start := DayStart(t, loc)
	b := timeBase{remain: nextDayStart(start, loc).Sub(t)}
	bit := 1 << uint(start.Weekday())
	l := t.In(loc)
	tod := l.Hour()*3600 + l.Minute()*60 + l.Second()
	total := 0
	for _, s := range used {
		total += s
	}
	outside, reached := false, false
	for i := range rules {
		r := &rules[i]
		if r.Days&bit == 0 || !r.covers(libraryID) {
			continue
		}
		b.covered = true
		if s, e, ok := r.slot(); ok {
			s, e = s*60, e*60
			var end time.Time
			switch {
			case s < e && tod >= s && tod < e, s > e && tod < e:
				end = time.Date(l.Year(), l.Month(), l.Day(), 0, 0, e, 0, loc)
			case s > e && tod >= s:
				end = time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, e, 0, loc)
			default:
				outside = true
			}
			if !end.IsZero() {
				if b.slotEnd.IsZero() || end.Before(b.slotEnd) {
					b.slotEnd = end
				}
				if d := end.Sub(t); d < b.remain {
					b.remain = d
				}
			}
		} else if r.SlotStart != "" {
			outside = true // a slot that cannot be read allows nothing
		}
		if r.DailyMinutes != nil {
			spent := total
			if r.LibraryID != "" {
				spent = used[r.LibraryID]
			}
			left := time.Duration(*r.DailyMinutes*60-spent) * time.Second
			if left <= 0 {
				reached = true
			} else if left < b.remain {
				b.remain = left
			}
		}
	}
	switch {
	case outside:
		b.remain, b.reason, b.slotEnd = 0, ReasonOutsideSlot, time.Time{}
	case reached:
		b.remain, b.reason = 0, ReasonLimitReached
	}
	return b
}

// EvaluateTime answers for one account at now. rules are the account's rules, used
// the seconds watched on now's home day by library id, and grantLeft the seconds of
// that day's grants not yet used. libraryID "" asks about the account alone.
func EvaluateTime(rules []TimeRule, loc *time.Location, now time.Time, libraryID string, used map[string]int, grantLeft int) TimeState {
	if loc == nil {
		loc = time.Local
	}
	st := TimeState{Now: now.In(loc)}
	ruled := false
	for i := range rules {
		ruled = ruled || rules[i].covers(libraryID)
	}
	if !ruled {
		st.Allowed = true
		return st
	}
	b := baseAt(rules, loc, now, libraryID, used)
	line := nextDayStart(DayStart(now, loc), loc)
	toLine := line.Sub(now)
	remain := b.remain
	if b.covered && grantLeft > 0 {
		remain += time.Duration(grantLeft) * time.Second
	}
	if remain >= toLine {
		// playing up to the day line: the next day's rules say how far past it
		remain = toLine + baseAt(rules, loc, line, libraryID, nil).remain
	}
	st.Limited = b.covered
	if remain > 0 {
		end := now.Add(remain)
		st.Allowed, st.Remaining, st.EndsAt = true, remain, &end
		if b.remain > 0 && !b.slotEnd.IsZero() {
			e := b.slotEnd
			st.SlotEndsAt = &e
		}
		return st
	}
	st.Reason = b.reason
	st.NextAllowedAt = nextAllowed(rules, loc, now, libraryID, used)
	return st
}

// nextAllowed is the first moment after now the rules alone allow playback. The
// rules only open at a day line or at a slot's start, so those are the moments
// tried, eight days ahead.
func nextAllowed(rules []TimeRule, loc *time.Location, now time.Time, libraryID string, used map[string]int) *time.Time {
	l := now.In(loc)
	today := DayStart(now, loc)
	var at []time.Time
	for d := -1; d <= 8; d++ {
		at = append(at, time.Date(l.Year(), l.Month(), l.Day()+d, DayLineHour, 0, 0, 0, loc))
		for i := range rules {
			if s, _, ok := rules[i].slot(); ok && rules[i].covers(libraryID) {
				at = append(at, time.Date(l.Year(), l.Month(), l.Day()+d, 0, s, 0, 0, loc))
			}
		}
	}
	sort.Slice(at, func(i, j int) bool { return at[i].Before(at[j]) })
	for _, c := range at {
		if !c.After(now) {
			continue
		}
		u := used
		if !DayStart(c, loc).Equal(today) {
			u = nil
		}
		if baseAt(rules, loc, c, libraryID, u).remain > 0 {
			return &c
		}
	}
	return nil
}

// ---- the home's time zone ----

type zoneSetting struct {
	Zone     string     `json:"zone"`
	Previous string     `json:"previous,omitempty"`
	From     *time.Time `json:"from,omitempty"`
}

// zoneAt reads the stored setting at now. A changed zone applies from the day line
// it was changed before (From); until then the previous zone holds.
func zoneAt(raw string, now time.Time) *time.Location {
	var z zoneSetting
	if json.Unmarshal([]byte(raw), &z.Zone) != nil {
		z = zoneSetting{}
		if json.Unmarshal([]byte(raw), &z) != nil {
			return time.Local
		}
	}
	name := z.Zone
	if z.From != nil && now.Before(*z.From) {
		name = z.Previous
	}
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

func homeLocation(ctx context.Context, q queryer, now time.Time) (*time.Location, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, HomeTimezoneKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Local, nil
	}
	if err != nil {
		return nil, err
	}
	return zoneAt(raw, now), nil
}

// HomeLocation is the home's time zone now: app_settings home.timezone, else the
// server's zone.
func (s *Store) HomeLocation(ctx context.Context) (*time.Location, error) {
	return homeLocation(ctx, s.DB, s.now())
}

// SetHomeTimezone stores the home's time zone (an IANA name). The current day's
// count stays where it is: the new zone applies from the next day line.
func (s *Store) SetHomeTimezone(ctx context.Context, actorID, zone string) error {
	if zone == "" || zone == "Local" {
		return fmt.Errorf("%w: time zone", ErrInvalid)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("%w: time zone", ErrInvalid)
	}
	now := s.now()
	return s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := homeLocation(ctx, tx, now)
		if err != nil {
			return err
		}
		from := nextDayStart(DayStart(now, cur), cur).UTC()
		val, err := json.Marshal(zoneSetting{Zone: zone, Previous: cur.String(), From: &from})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at, updated_by)
			VALUES ($1, $2::jsonb, $3, $4)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at,
				updated_by = EXCLUDED.updated_by`, HomeTimezoneKey, string(val), now, nullUUID(actorID))
		return err
	})
}

// ---- rules ----

func timeRulesOf(ctx context.Context, q queryer, userID string) ([]TimeRule, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, COALESCE(library_id::text, ''), days,
			COALESCE(to_char(slot_start, 'HH24:MI'), ''), COALESCE(to_char(slot_end, 'HH24:MI'), ''),
			daily_minutes, updated_at
		FROM parental_time_rules WHERE user_id = $1
		ORDER BY library_id NULLS FIRST, days DESC, slot_start NULLS FIRST, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimeRule{}
	for rows.Next() {
		var r TimeRule
		var mins sql.NullInt64
		var at time.Time
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.Days, &r.SlotStart, &r.SlotEnd, &mins, &at); err != nil {
			return nil, err
		}
		if mins.Valid {
			m := int(mins.Int64)
			r.DailyMinutes = &m
		}
		r.UpdatedAt = &at
		out = append(out, r)
	}
	return out, rows.Err()
}

// TimeRules returns childID's time rules. ErrNotRestricted when the account has no
// policy.
func (s *Store) TimeRules(ctx context.Context, childID string) ([]TimeRule, error) {
	if _, err := policyOf(ctx, s.DB, childID, false); err != nil {
		return nil, err
	}
	return timeRulesOf(ctx, s.DB, childID)
}

func timeRulesAudit(rules []TimeRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, r := range rules {
		m := map[string]any{"days": r.Days}
		if r.LibraryID != "" {
			m["library_id"] = r.LibraryID
		}
		if r.SlotStart != "" {
			m["slot_start"], m["slot_end"] = r.SlotStart, r.SlotEnd
		}
		if r.DailyMinutes != nil {
			m["daily_minutes"] = *r.DailyMinutes
		}
		out = append(out, m)
	}
	return out
}

// SetTimeRules replaces childID's time rules with rules (none: no time limit). The
// caller has checked that actorID may change them. ErrNotRestricted when the
// account has no policy, ErrInvalid for a value a rule may not hold or a library
// that does not exist.
func (s *Store) SetTimeRules(ctx context.Context, actorID, childID string, rules []TimeRule) ([]TimeRule, error) {
	if len(rules) > MaxTimeRules {
		return nil, fmt.Errorf("%w: more than %d rules", ErrInvalid, MaxTimeRules)
	}
	for i := range rules {
		if err := rules[i].validate(); err != nil {
			return nil, err
		}
	}
	var out []TimeRule
	now := s.now()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		before, err := timeRulesOf(ctx, tx, childID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM parental_time_rules WHERE user_id = $1`, childID); err != nil {
			return err
		}
		for _, r := range rules {
			if r.LibraryID != "" {
				var one int
				err := tx.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id = $1`, r.LibraryID).Scan(&one)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: library_id", ErrInvalid)
				}
				if err != nil {
					return err
				}
			}
			var mins sql.NullInt64
			if r.DailyMinutes != nil {
				mins = sql.NullInt64{Int64: int64(*r.DailyMinutes), Valid: true}
			}
			nullStr := func(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }
			if _, err := tx.ExecContext(ctx, `INSERT INTO parental_time_rules
					(user_id, library_id, days, slot_start, slot_end, daily_minutes, created_by, updated_at)
				VALUES ($1, $2::uuid, $3, $4::time, $5::time, $6, $7, $8)`,
				childID, nullUUID(r.LibraryID), r.Days, nullStr(r.SlotStart), nullStr(r.SlotEnd),
				mins, nullUUID(actorID), now); err != nil {
				return err
			}
		}
		if out, err = timeRulesOf(ctx, tx, childID); err != nil {
			return err
		}
		return audit(ctx, tx, AuditTimeRulesSet, actorID, childID, timeRulesAudit(before), timeRulesAudit(out))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---- usage and grants ----

// TimeUsageRow is the seconds an account watched on one home day in one library
// (NoLibrary: playback with no library).
type TimeUsageRow struct {
	Day       string `json:"day"`
	LibraryID string `json:"library_id"`
	Seconds   int    `json:"seconds"`
}

// TimeGrant is more time a guardian gave for one home day.
type TimeGrant struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Day         string    `json:"day"`
	Minutes     int       `json:"minutes"`
	UsedSeconds int       `json:"used_seconds"`
	GrantedBy   string    `json:"granted_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TimeUsage is an account's count over a range of home days.
type TimeUsage struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	TimeZone string         `json:"time_zone"`
	Today    string         `json:"today"`
	Usage    []TimeUsageRow `json:"usage"`
	Grants   []TimeGrant    `json:"grants"`
}

// MaxUsageDays bounds one time-usage read.
const MaxUsageDays = 92

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

// TimeUsage returns childID's usage and grants for the home days from..to
// (YYYY-MM-DD, both included). Empty from and to: the last seven days.
// ErrNotRestricted when the account has no policy, ErrInvalid for a bad range.
func (s *Store) TimeUsage(ctx context.Context, childID, from, to string) (*TimeUsage, error) {
	if _, err := policyOf(ctx, s.DB, childID, false); err != nil {
		return nil, err
	}
	now := s.now()
	loc, err := homeLocation(ctx, s.DB, now)
	if err != nil {
		return nil, err
	}
	today := DayKey(now, loc)
	if to == "" {
		to = today
	}
	t1, ok := parseDay(to)
	if !ok {
		return nil, fmt.Errorf("%w: to", ErrInvalid)
	}
	if from == "" {
		from = t1.AddDate(0, 0, -6).Format("2006-01-02")
	}
	t0, ok := parseDay(from)
	if !ok || t0.After(t1) || t1.Sub(t0) > (MaxUsageDays-1)*24*time.Hour {
		return nil, fmt.Errorf("%w: from", ErrInvalid)
	}
	out := &TimeUsage{From: from, To: to, TimeZone: loc.String(), Today: today, Usage: []TimeUsageRow{}, Grants: []TimeGrant{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT day::text, library_id, seconds FROM parental_time_usage
		WHERE user_id = $1 AND day BETWEEN $2::date AND $3::date ORDER BY day, library_id`, childID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r TimeUsageRow
		if err := rows.Scan(&r.Day, &r.LibraryID, &r.Seconds); err != nil {
			return nil, err
		}
		out.Usage = append(out.Usage, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants, err := s.DB.QueryContext(ctx, `SELECT `+grantCols+` FROM parental_time_grants
		WHERE user_id = $1 AND day BETWEEN $2::date AND $3::date ORDER BY created_at, id`, childID, from, to)
	if err != nil {
		return nil, err
	}
	defer grants.Close()
	for grants.Next() {
		g, err := scanGrant(grants)
		if err != nil {
			return nil, err
		}
		out.Grants = append(out.Grants, *g)
	}
	return out, grants.Err()
}

const grantCols = `id, user_id, day::text, minutes, used_seconds, COALESCE(granted_by::text, ''), created_at`

func scanGrant(row interface{ Scan(...any) error }) (*TimeGrant, error) {
	var g TimeGrant
	if err := row.Scan(&g.ID, &g.UserID, &g.Day, &g.Minutes, &g.UsedSeconds, &g.GrantedBy, &g.CreatedAt); err != nil {
		return nil, err
	}
	return &g, nil
}

// GrantTime gives childID minutes more for the current home day: they add to that
// day only, and open a closed slot until they run out. guardianID is who gave them;
// the caller has checked that they may (an admin, the child's guardian, or an
// approved request). ErrNotRestricted when the account has no policy, ErrInvalid
// for minutes outside 1 to MaxGrantMinutes.
func (s *Store) GrantTime(ctx context.Context, guardianID, childID string, minutes int) (*TimeGrant, error) {
	if minutes < 1 || minutes > MaxGrantMinutes {
		return nil, fmt.Errorf("%w: minutes", ErrInvalid)
	}
	var g *TimeGrant
	now := s.now()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := policyOf(ctx, tx, childID, true); err != nil {
			return err
		}
		loc, err := homeLocation(ctx, tx, now)
		if err != nil {
			return err
		}
		g, err = scanGrant(tx.QueryRowContext(ctx, `INSERT INTO parental_time_grants (user_id, day, minutes, granted_by, created_at)
			VALUES ($1, $2::date, $3, $4, $5) RETURNING `+grantCols,
			childID, DayKey(now, loc), minutes, nullUUID(guardianID), now))
		if err != nil {
			return err
		}
		return audit(ctx, tx, AuditTimeGranted, guardianID, childID, nil,
			map[string]any{"day": g.Day, "minutes": minutes})
	})
	if err != nil {
		return nil, err
	}
	return g, nil
}

// timeFacts is what a decision reads for one account and moment.
type timeFacts struct {
	loc       *time.Location
	day       string
	rules     []TimeRule
	used      map[string]int
	grantLeft int
}

func loadTimeFacts(ctx context.Context, q queryer, userID string, now time.Time) (*timeFacts, error) {
	rules, err := timeRulesOf(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	f := &timeFacts{rules: rules, used: map[string]int{}}
	if len(rules) == 0 {
		return f, nil
	}
	if f.loc, err = homeLocation(ctx, q, now); err != nil {
		return nil, err
	}
	f.day = DayKey(now, f.loc)
	rows, err := q.QueryContext(ctx, `SELECT library_id, seconds FROM parental_time_usage
		WHERE user_id = $1 AND day = $2::date`, userID, f.day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var lib string
		var sec int
		if err := rows.Scan(&lib, &sec); err != nil {
			return nil, err
		}
		f.used[lib] = sec
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	err = q.QueryRowContext(ctx, `SELECT COALESCE(sum(minutes * 60 - used_seconds), 0) FROM parental_time_grants
		WHERE user_id = $1 AND day = $2::date`, userID, f.day).Scan(&f.grantLeft)
	return f, err
}

// TimeOf answers for a restricted account at the home's clock: whether it may play
// in libraryID ("": the account alone) and for how long. An account with no time
// rule is allowed with no end. It does not check that userID is restricted (the
// keeper does, from the cache): an unrestricted account has no rules.
func (s *Store) TimeOf(ctx context.Context, userID, libraryID string) (TimeState, error) {
	now := s.now()
	f, err := loadTimeFacts(ctx, s.DB, userID, now)
	if err != nil {
		return TimeState{}, err
	}
	return EvaluateTime(f.rules, f.loc, now, libraryID, f.used, f.grantLeft), nil
}

// AddUsage adds seconds watched in libraryID ("": no library) to userID's count for
// the current home day. The part watched while the rules alone refuse (past the
// limit or outside the slot) is taken from the day's grants, oldest first. An
// account with no policy, or with no time rule, is not counted.
func (s *Store) AddUsage(ctx context.Context, userID, libraryID string, seconds int) error {
	if seconds <= 0 {
		return nil
	}
	if libraryID == "" {
		libraryID = NoLibrary
	}
	if !isUUID(libraryID) {
		return fmt.Errorf("%w: library_id", ErrInvalid)
	}
	now := s.now()
	return s.inTx(ctx, func(tx *sql.Tx) error {
		// the policy row is the lock: two sessions of one account count in turn
		if _, err := policyOf(ctx, tx, userID, true); err != nil {
			if errors.Is(err, ErrNotRestricted) {
				return nil
			}
			return err
		}
		f, err := loadTimeFacts(ctx, tx, userID, now)
		if err != nil || len(f.rules) == 0 {
			return err
		}
		lib := libraryID
		if lib == NoLibrary {
			lib = ""
		}
		b := baseAt(f.rules, f.loc, now, lib, f.used)
		if _, err := tx.ExecContext(ctx, `INSERT INTO parental_time_usage (user_id, day, library_id, seconds)
			VALUES ($1, $2::date, $3, $4)
			ON CONFLICT (user_id, day, library_id) DO UPDATE SET seconds = parental_time_usage.seconds + EXCLUDED.seconds`,
			userID, f.day, libraryID, seconds); err != nil {
			return err
		}
		over := seconds
		if !b.covered {
			over = 0
		} else if free := int(b.remain / time.Second); free < over {
			over -= free
		} else {
			over = 0
		}
		if over == 0 || f.grantLeft == 0 {
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, minutes * 60 - used_seconds FROM parental_time_grants
			WHERE user_id = $1 AND day = $2::date AND used_seconds < minutes * 60
			ORDER BY created_at, id FOR UPDATE`, userID, f.day)
		if err != nil {
			return err
		}
		type open struct {
			id   string
			left int
		}
		var grants []open
		for rows.Next() {
			var g open
			if err := rows.Scan(&g.id, &g.left); err != nil {
				rows.Close()
				return err
			}
			grants = append(grants, g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, g := range grants {
			if over == 0 {
				break
			}
			take := min(over, g.left)
			if _, err := tx.ExecContext(ctx, `UPDATE parental_time_grants SET used_seconds = used_seconds + $2 WHERE id = $1`,
				g.id, take); err != nil {
				return err
			}
			over -= take
		}
		return nil
	})
}
