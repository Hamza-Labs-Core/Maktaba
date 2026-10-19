package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

// The remote controller's tables (slot 0125). The SQL here is portable
// between Postgres and the SQLite twin: every time is passed from Go (no
// now() in a query), ids are made in Go, and the few Postgres-only calls
// (advisory locks, NOTIFY) are skipped on SQLite.

// Input states.
const (
	InputIdle         = "idle"
	InputAssigned     = "assigned"
	InputBroadcasting = "broadcasting"
	InputCleanup      = "cleanup"
	InputErrored      = "errored"
)

// Push states (design §7.3).
const (
	PushRequested  = "requested"
	PushConnecting = "connecting"
	PushLive       = "live"
	PushStopping   = "stopping"
	PushCleanup    = "cleanup"
	PushDone       = "done"
	PushFailed     = "failed"
)

// Running reports whether a push holds its channel (the unique index's states).
func Running(state string) bool {
	switch state {
	case PushRequested, PushConnecting, PushLive, PushStopping:
		return true
	}
	return false
}

// Input is a row of tv_remote_inputs.
type Input struct {
	ID           string     `json:"id"`
	CFUID        string     `json:"cf_uid"`
	Name         string     `json:"name"`
	State        string     `json:"state"`
	ChannelID    string     `json:"channel_id,omitempty"`
	Ingest       IngestURLs `json:"ingest"`
	LastVideoUID string     `json:"last_video_uid,omitempty"`
	AssignedAt   *time.Time `json:"assigned_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	ErrorAt      *time.Time `json:"error_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// IngestURLs are an input's ingest endpoints (never its key).
type IngestURLs struct {
	RTMPS string `json:"rtmps_url,omitempty"`
	SRT   string `json:"srt_url,omitempty"`
}

// Push is a row of tv_remote_pushes.
type Push struct {
	ID                 string     `json:"id"`
	ChannelID          string     `json:"channel_id"`
	InputID            string     `json:"input_id"`
	HostID             string     `json:"host_id,omitempty"`
	Generation         int64      `json:"generation,omitempty"`
	State              string     `json:"state"`
	Attempt            int        `json:"attempt"`
	Ingest             string     `json:"ingest"`
	VideoUID           string     `json:"video_uid,omitempty"`
	StartedAt          time.Time  `json:"started_at"`
	StateAt            time.Time  `json:"state_at"`
	LiveAt             *time.Time `json:"live_at,omitempty"`
	IdleSince          *time.Time `json:"idle_since,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
	EndReason          string     `json:"end_reason,omitempty"`
	Detail             string     `json:"detail,omitempty"`
	RelayState         string     `json:"relay_state,omitempty"`
	RelayDetail        string     `json:"relay_detail,omitempty"`
	RelayAt            *time.Time `json:"relay_at,omitempty"`
	MinutesDelivered   float64    `json:"minutes_delivered"`
	RecordingDeletedAt *time.Time `json:"recording_deleted_at,omitempty"`
}

// Session is a row of tv_remote_sessions.
type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	ChannelID  string     `json:"channel_id"`
	PushID     string     `json:"push_id,omitempty"`
	Device     string     `json:"device"`
	StartedAt  time.Time  `json:"started_at"`
	LastBeatAt time.Time  `json:"last_beat_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	EndReason  string     `json:"end_reason,omitempty"`
	TokenExp   *time.Time `json:"token_exp,omitempty"`
	TokenKID   string     `json:"token_kid,omitempty"`
	VideoUID   string     `json:"video_uid,omitempty"`
}

// store is the SQL of the controller.
type store struct {
	db       *sql.DB
	postgres bool
}

func newStore(db *sql.DB) *store {
	s := &store{db: db}
	if db != nil {
		name := strings.ToLower(fmt.Sprintf("%T", db.Driver()))
		s.postgres = strings.Contains(name, "pq") || strings.Contains(name, "pgx") || strings.Contains(name, "postgres")
	}
	return s
}

// t is a time parameter: a time for Postgres, RFC 3339 text for SQLite.
func (s *store) t(v time.Time) any {
	if s.postgres {
		return v.UTC()
	}
	return v.UTC().Format(time.RFC3339Nano)
}

// tp is an optional time parameter.
func (s *store) tp(v *time.Time) any {
	if v == nil {
		return nil
	}
	return s.t(*v)
}

// nt scans a nullable time stored as timestamptz (Postgres) or text (SQLite).
type nt struct {
	T     time.Time
	Valid bool
}

func (n *nt) Scan(v any) error {
	n.Valid = false
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.T, n.Valid = x.UTC(), true
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("tvremote: cannot scan %T as a time", v)
}

func (n *nt) parse(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05.999Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			n.T, n.Valid = t.UTC(), true
			return nil
		}
	}
	return fmt.Errorf("tvremote: unreadable time %q", s)
}

func (n nt) ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.T
	return &t
}

// ns scans a nullable text (uuid columns come back as text or bytes).
type ns struct{ S string }

func (n *ns) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		n.S = ""
	case string:
		n.S = x
	case []byte:
		n.S = string(x)
	default:
		n.S = fmt.Sprint(x)
	}
	return nil
}

// nullable turns "" into NULL.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const inputCols = `id, cf_uid, name, state, channel_id, ingest, last_video_uid, assigned_at, error, error_at, created_at`

func scanInput(sc interface{ Scan(...any) error }) (Input, error) {
	var in Input
	var id, ch, last, errS ns
	var ingest []byte
	var assigned, errAt, created nt
	if err := sc.Scan(&id, &in.CFUID, &in.Name, &in.State, &ch, &ingest, &last, &assigned, &errS, &errAt, &created); err != nil {
		return in, err
	}
	in.ID, in.ChannelID, in.LastVideoUID, in.Error = id.S, ch.S, last.S, errS.S
	_ = json.Unmarshal(ingest, &in.Ingest)
	in.AssignedAt, in.ErrorAt, in.CreatedAt = assigned.ptr(), errAt.ptr(), created.T
	return in, nil
}

func (s *store) inputs(ctx context.Context) ([]Input, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+inputCols+` FROM tv_remote_inputs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Input
	for rows.Next() {
		in, err := scanInput(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, rows.Err()
}

func (s *store) input(ctx context.Context, id string) (Input, error) {
	return scanInput(s.db.QueryRowContext(ctx, `SELECT `+inputCols+` FROM tv_remote_inputs WHERE id = $1`, id))
}

func (s *store) insertInput(ctx context.Context, in Input) error {
	blob, _ := json.Marshal(in.Ingest)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_remote_inputs (id, cf_uid, name, state, ingest, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, in.ID, in.CFUID, in.Name, in.State, string(blob), s.t(in.CreatedAt))
	return err
}

// setInput updates columns of one input (values: time.Time, *time.Time, string, nil).
func (s *store) setInput(ctx context.Context, id string, cols map[string]any) error {
	return s.set(ctx, "tv_remote_inputs", id, cols)
}

func (s *store) deleteInput(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tv_remote_inputs WHERE id = $1`, id)
	return err
}

// set is UPDATE table SET cols WHERE id.
func (s *store) set(ctx context.Context, table, id string, cols map[string]any) error {
	if len(cols) == 0 {
		return nil
	}
	keys := make([]string, 0, len(cols))
	for k := range cols {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	args := make([]any, 0, len(keys)+1)
	for i, k := range keys {
		parts = append(parts, fmt.Sprintf("%s = $%d", k, i+1))
		v := cols[k]
		switch x := v.(type) {
		case time.Time:
			v = s.t(x)
		case *time.Time:
			v = s.tp(x)
		}
		args = append(args, v)
	}
	args = append(args, id)
	_, err := s.db.ExecContext(ctx, "UPDATE "+table+" SET "+strings.Join(parts, ", ")+
		fmt.Sprintf(" WHERE id = $%d", len(args)), args...)
	return err
}

const pushCols = `id, channel_id, input_id, host_id, generation, state, attempt, ingest, video_uid, started_at,
	state_at, live_at, idle_since, ended_at, end_reason, detail, relay_state, relay_detail, relay_at,
	minutes_delivered, recording_deleted_at`

func scanPush(sc interface{ Scan(...any) error }) (Push, error) {
	var p Push
	var id, ch, in, host, video, reason, detail, rstate, rdetail ns
	var gen sql.NullInt64
	var started, stateAt, live, idle, ended, relayAt, deleted nt
	var minutes sql.NullFloat64
	if err := sc.Scan(&id, &ch, &in, &host, &gen, &p.State, &p.Attempt, &p.Ingest, &video, &started,
		&stateAt, &live, &idle, &ended, &reason, &detail, &rstate, &rdetail, &relayAt, &minutes, &deleted); err != nil {
		return p, err
	}
	p.ID, p.ChannelID, p.InputID, p.HostID, p.VideoUID = id.S, ch.S, in.S, host.S, video.S
	p.EndReason, p.Detail, p.RelayState, p.RelayDetail = reason.S, detail.S, rstate.S, rdetail.S
	p.Generation, p.MinutesDelivered = gen.Int64, minutes.Float64
	p.StartedAt, p.StateAt = started.T, stateAt.T
	p.LiveAt, p.IdleSince, p.EndedAt, p.RelayAt, p.RecordingDeletedAt = live.ptr(), idle.ptr(), ended.ptr(), relayAt.ptr(), deleted.ptr()
	return p, nil
}

func (s *store) queryPushes(ctx context.Context, where string, args ...any) ([]Push, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pushCols+` FROM tv_remote_pushes `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Push
	for rows.Next() {
		p, err := scanPush(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, rows.Err()
}

// activePushes are the pushes not yet done or failed.
func (s *store) activePushes(ctx context.Context) ([]Push, error) {
	return s.queryPushes(ctx, `WHERE state NOT IN ('done', 'failed')`)
}

func (s *store) push(ctx context.Context, id string) (Push, error) {
	return scanPush(s.db.QueryRowContext(ctx, `SELECT `+pushCols+` FROM tv_remote_pushes WHERE id = $1`, id))
}

// runningPushFor is the channel's push holding it (requested..stopping).
func (s *store) runningPushFor(ctx context.Context, channel string) (Push, bool, error) {
	ps, err := s.queryPushes(ctx, `WHERE channel_id = $1 AND state IN ('requested', 'connecting', 'live', 'stopping')`, channel)
	if err != nil || len(ps) == 0 {
		return Push{}, false, err
	}
	return ps[0], true, nil
}

// undeletedRecordings are ended pushes whose recording is still at Cloudflare.
func (s *store) undeletedRecordings(ctx context.Context) ([]Push, error) {
	return s.queryPushes(ctx, `WHERE state IN ('cleanup', 'done', 'failed') AND video_uid IS NOT NULL AND recording_deleted_at IS NULL`)
}

// pushesSince are the pushes started at or after t (the month's broadcasts).
func (s *store) pushesSince(ctx context.Context, t time.Time) ([]Push, error) {
	ps, err := s.queryPushes(ctx, ``)
	if err != nil {
		return nil, err
	}
	out := ps[:0]
	for _, p := range ps {
		if !p.StartedAt.Before(t) || p.EndedAt == nil || !p.EndedAt.Before(t) {
			out = append(out, p)
		}
	}
	return out, nil
}

// recentFailures are pushes of a channel that failed at or after t.
func (s *store) recentFailures(ctx context.Context, channel string, t time.Time) (int, error) {
	ps, err := s.queryPushes(ctx, `WHERE channel_id = $1 AND state = 'failed'`, channel)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range ps {
		if p.EndedAt != nil && !p.EndedAt.Before(t) && p.EndReason != "retried" {
			n++
		}
	}
	return n, nil
}

// errDuplicate: the unique index refused a second push for the channel.
var errDuplicate = errors.New("tvremote: the channel already has a running push")

func (s *store) insertPush(ctx context.Context, p Push) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_remote_pushes
		(id, channel_id, input_id, state, attempt, ingest, started_at, state_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.ChannelID, nullable(p.InputID), p.State, p.Attempt, p.Ingest, s.t(p.StartedAt), s.t(p.StateAt))
	if err != nil && isUnique(err) {
		return errDuplicate
	}
	return err
}

func isUnique(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate key")
}

func (s *store) setPush(ctx context.Context, id string, cols map[string]any) error {
	return s.set(ctx, "tv_remote_pushes", id, cols)
}

func (s *store) putPushSecret(ctx context.Context, push, url, key, streamID string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_push_secrets (push_id, url, key, stream_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (push_id) DO UPDATE SET url = excluded.url, key = excluded.key,
			stream_id = excluded.stream_id, expires_at = excluded.expires_at`,
		push, url, key, nullable(streamID), s.t(expires))
	return err
}

func (s *store) deletePushSecret(ctx context.Context, push string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tv_push_secrets WHERE push_id = $1`, push)
	return err
}

const sessionCols = `id, user_id, channel_id, push_id, device, started_at, last_beat_at, ended_at, end_reason,
	token_exp, token_kid, video_uid`

func scanSession(sc interface{ Scan(...any) error }) (Session, error) {
	var x Session
	var id, ch, push, reason, kid, video ns
	var started, beat, ended, exp nt
	if err := sc.Scan(&id, &x.UserID, &ch, &push, &x.Device, &started, &beat, &ended, &reason, &exp, &kid, &video); err != nil {
		return x, err
	}
	x.ID, x.ChannelID, x.PushID, x.EndReason, x.TokenKID, x.VideoUID = id.S, ch.S, push.S, reason.S, kid.S, video.S
	x.StartedAt, x.LastBeatAt, x.EndedAt, x.TokenExp = started.T, beat.T, ended.ptr(), exp.ptr()
	return x, nil
}

func (s *store) querySessions(ctx context.Context, where string, args ...any) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionCols+` FROM tv_remote_sessions `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, rows.Err()
}

func (s *store) openSessions(ctx context.Context) ([]Session, error) {
	return s.querySessions(ctx, `WHERE ended_at IS NULL`)
}

func (s *store) session(ctx context.Context, id string) (Session, error) {
	return scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM tv_remote_sessions WHERE id = $1`, id))
}

// sessionsSince are the sessions still open or ended at or after t.
func (s *store) sessionsSince(ctx context.Context, t time.Time) ([]Session, error) {
	all, err := s.querySessions(ctx, ``)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, x := range all {
		if x.EndedAt == nil || !x.EndedAt.Before(t) {
			out = append(out, x)
		}
	}
	return out, nil
}

func (s *store) insertSession(ctx context.Context, x Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_remote_sessions
		(id, user_id, channel_id, push_id, device, started_at, last_beat_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		x.ID, x.UserID, x.ChannelID, nullable(x.PushID), x.Device, s.t(x.StartedAt), s.t(x.LastBeatAt))
	return err
}

func (s *store) setSession(ctx context.Context, id string, cols map[string]any) error {
	return s.set(ctx, "tv_remote_sessions", id, cols)
}

// UsageDay is a row of tv_remote_usage_daily.
type UsageDay struct {
	Day                  string  `json:"day"`
	MinutesDelivered     float64 `json:"minutes_delivered"`
	PeakRecordingMinutes float64 `json:"peak_recording_minutes"`
	CostUSD              float64 `json:"cost_usd"`
	Source               string  `json:"source"`
}

// putUsage writes one day; minutes and the peak never decrease within a day
// (analytics arrive late, story 32.10 EC2).
func (s *store) putUsage(ctx context.Context, u UsageDay, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_remote_usage_daily
		(day, minutes_delivered, peak_recording_minutes, cost_usd, source, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (day) DO UPDATE SET
			minutes_delivered = CASE WHEN excluded.minutes_delivered > tv_remote_usage_daily.minutes_delivered
				THEN excluded.minutes_delivered ELSE tv_remote_usage_daily.minutes_delivered END,
			peak_recording_minutes = CASE WHEN excluded.peak_recording_minutes > tv_remote_usage_daily.peak_recording_minutes
				THEN excluded.peak_recording_minutes ELSE tv_remote_usage_daily.peak_recording_minutes END,
			cost_usd = CASE WHEN excluded.cost_usd > tv_remote_usage_daily.cost_usd
				THEN excluded.cost_usd ELSE tv_remote_usage_daily.cost_usd END,
			source = CASE WHEN excluded.minutes_delivered > 0 THEN excluded.source ELSE tv_remote_usage_daily.source END,
			updated_at = excluded.updated_at`,
		u.Day, u.MinutesDelivered, u.PeakRecordingMinutes, u.CostUSD, u.Source, s.t(at))
	return err
}

// usageSince returns the days on or after day (YYYY-MM-DD).
func (s *store) usageSince(ctx context.Context, day string) ([]UsageDay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT day, minutes_delivered, peak_recording_minutes, cost_usd, source
		FROM tv_remote_usage_daily`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageDay
	for rows.Next() {
		var u UsageDay
		var d nt
		if err := rows.Scan(&d, &u.MinutesDelivered, &u.PeakRecordingMinutes, &u.CostUSD, &u.Source); err != nil {
			return nil, err
		}
		u.Day = d.T.Format("2006-01-02")
		if u.Day >= day {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, rows.Err()
}

// lockKey is a stable advisory lock id for a name.
func lockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("maktaba:" + name))
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// tryLock takes a Postgres session advisory lock on a dedicated connection
// (true and a release func), so only one API process acts. SQLite: always.
func (s *store) tryLock(ctx context.Context, name string) (bool, func()) {
	if !s.postgres {
		return true, func() {}
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, func() {}
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, lockKey(name)).Scan(&ok); err != nil || !ok {
		_ = conn.Close()
		return false, func() {}
	}
	return true, func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey(name))
		_ = conn.Close()
	}
}

// lock waits for a Postgres session advisory lock (tune serialisation).
func (s *store) lock(ctx context.Context, name string) (func(), error) {
	if !s.postgres {
		return func() {}, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey(name)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey(name))
		_ = conn.Close()
	}, nil
}

// notify wakes the push relay (Postgres only).
func (s *store) notify(ctx context.Context, channel, payload string) {
	if !s.postgres {
		return
	}
	_, _ = s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
}

// channelInfo is what the tune API answers about a channel.
type channelInfo struct {
	ID        string  `json:"id"`
	Number    int     `json:"number"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	LibraryID *string `json:"library_id,omitempty"`
	enabled   bool
}

func (s *store) channel(ctx context.Context, id string) (channelInfo, error) {
	var c channelInfo
	var lib ns
	var enabled any
	err := s.db.QueryRowContext(ctx, `SELECT id, number, name, slug, library_id, enabled FROM channels WHERE id = $1`, id).
		Scan(new(ns), &c.Number, &c.Name, &c.Slug, &lib, &enabled)
	if err != nil {
		return c, err
	}
	c.ID = id
	if lib.S != "" {
		l := lib.S
		c.LibraryID = &l
	}
	switch v := enabled.(type) {
	case bool:
		c.enabled = v
	case int64:
		c.enabled = v != 0
	default:
		c.enabled = true
	}
	return c, nil
}

// placement is the channel's channel_placements row (host and generation).
func (s *store) placement(ctx context.Context, channel string) (string, int64, bool) {
	var host ns
	var gen sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT host_id, generation FROM channel_placements
		WHERE channel_id = $1 AND state <> 'released'`, channel).Scan(&host, &gen)
	if err != nil {
		return "", 0, false
	}
	return host.S, gen.Int64, true
}

// instanceTag is the first 8 characters of the home's instance id (input names).
func (s *store) instanceTag(ctx context.Context) string {
	var id ns
	if err := s.db.QueryRowContext(ctx, `SELECT instance_id FROM instance_identity WHERE id = 1`).Scan(&id); err != nil || id.S == "" {
		return "home"
	}
	x := strings.ReplaceAll(id.S, "-", "")
	if len(x) > 8 {
		x = x[:8]
	}
	return x
}
