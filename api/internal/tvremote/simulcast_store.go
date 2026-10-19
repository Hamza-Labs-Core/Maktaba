package tvremote

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"
)

// The simulcast tables (slot 0126), portable between Postgres and SQLite like
// the rest of the store.

// Destination is a row of tv_simulcast_destinations (its key is in
// home_secrets; the answer carries only a masked form).
type Destination struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	IngestURL string    `json:"ingest_url"`
	KeySecret string    `json:"-"`
	KeySet    bool      `json:"key_set"`
	KeyMasked string    `json:"stream_key_masked,omitempty"`
	InUse     bool      `json:"in_use"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Attachment is a row of tv_simulcast_attachments.
type Attachment struct {
	ID                string     `json:"id"`
	ChannelID         string     `json:"channel_id"`
	DestinationID     string     `json:"destination_id"`
	Mode              string     `json:"mode"`
	Rules             []Rule     `json:"rules"`
	Enabled           bool       `json:"enabled"`
	Manual            string     `json:"manual,omitempty"`
	ManualUntil       *time.Time `json:"manual_until,omitempty"`
	RightsConfirmedBy string     `json:"rights_confirmed_by,omitempty"`
	RightsConfirmedAt *time.Time `json:"rights_confirmed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// SimOutput is a row of tv_simulcast_outputs: one output's life.
type SimOutput struct {
	ID            string     `json:"id"`
	AttachmentID  string     `json:"attachment_id,omitempty"`
	ChannelID     string     `json:"channel_id,omitempty"`
	DestinationID string     `json:"destination_id,omitempty"`
	PushID        string     `json:"push_id,omitempty"`
	InputID       string     `json:"input_id,omitempty"`
	InputUID      string     `json:"-"`
	CFOutputUID   string     `json:"cf_output_uid,omitempty"`
	State         string     `json:"state"`
	WindowStart   *time.Time `json:"window_start,omitempty"`
	WindowEnd     *time.Time `json:"window_end,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Minutes       float64    `json:"minutes"`
	Detail        string     `json:"detail,omitempty"`
	Error         string     `json:"error,omitempty"`
	CFStatus      string     `json:"cf_status,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// liveMinutes are the output's minutes in [from, now): from its start to its
// end (or now).
func (o SimOutput) liveMinutes(from, now time.Time) float64 {
	if o.StartedAt == nil {
		return 0
	}
	a, b := *o.StartedAt, now
	if o.EndedAt != nil {
		b = *o.EndedAt
	}
	if a.Before(from) {
		a = from
	}
	if !b.After(a) {
		return 0
	}
	return b.Sub(a).Minutes()
}

func boolOf(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case []byte:
		return string(x) == "1" || string(x) == "t" || string(x) == "true"
	case string:
		return x == "1" || x == "t" || x == "true"
	}
	return false
}

const destCols = `id, name, kind, ingest_url, key_secret, created_at, updated_at`

func scanDest(sc interface{ Scan(...any) error }) (Destination, error) {
	var d Destination
	var id ns
	var created, updated nt
	if err := sc.Scan(&id, &d.Name, &d.Kind, &d.IngestURL, &d.KeySecret, &created, &updated); err != nil {
		return d, err
	}
	d.ID, d.CreatedAt, d.UpdatedAt = id.S, created.T, updated.T
	return d, nil
}

func (s *store) destinations(ctx context.Context) ([]Destination, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+destCols+` FROM tv_simulcast_destinations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Destination
	for rows.Next() {
		d, err := scanDest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, rows.Err()
}

func (s *store) destination(ctx context.Context, id string) (Destination, error) {
	return scanDest(s.db.QueryRowContext(ctx, `SELECT `+destCols+` FROM tv_simulcast_destinations WHERE id = $1`, id))
}

func (s *store) insertDestination(ctx context.Context, d Destination) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_simulcast_destinations (id, name, kind, ingest_url, key_secret, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, d.ID, d.Name, d.Kind, d.IngestURL, d.KeySecret, s.t(d.CreatedAt), s.t(d.UpdatedAt))
	return err
}

func (s *store) deleteDestination(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tv_simulcast_attachments WHERE destination_id = $1`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM tv_simulcast_destinations WHERE id = $1`, id)
	return err
}

const attCols = `id, channel_id, destination_id, mode, rules, enabled, manual, manual_until, rights_confirmed_by,
	rights_confirmed_at, created_at, updated_at`

func scanAtt(sc interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	var id, ch, dest, manual, by ns
	var rules ns
	var enabled any
	var until, rat, created, updated nt
	if err := sc.Scan(&id, &ch, &dest, &a.Mode, &rules, &enabled, &manual, &until, &by, &rat, &created, &updated); err != nil {
		return a, err
	}
	a.ID, a.ChannelID, a.DestinationID, a.Manual, a.RightsConfirmedBy = id.S, ch.S, dest.S, manual.S, by.S
	a.Enabled = boolOf(enabled)
	a.ManualUntil, a.RightsConfirmedAt, a.CreatedAt, a.UpdatedAt = until.ptr(), rat.ptr(), created.T, updated.T
	_ = json.Unmarshal([]byte(rules.S), &a.Rules)
	if a.Rules == nil {
		a.Rules = []Rule{}
	}
	return a, nil
}

// attachments lists attachments (one channel's when channel is set).
func (s *store) attachments(ctx context.Context, channel string) ([]Attachment, error) {
	q, args := `SELECT `+attCols+` FROM tv_simulcast_attachments`, []any{}
	if channel != "" {
		q += ` WHERE channel_id = $1`
		args = append(args, channel)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAtt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, rows.Err()
}

func (s *store) attachment(ctx context.Context, id string) (Attachment, error) {
	return scanAtt(s.db.QueryRowContext(ctx, `SELECT `+attCols+` FROM tv_simulcast_attachments WHERE id = $1`, id))
}

func (s *store) upsertAttachment(ctx context.Context, a Attachment) error {
	rules, _ := json.Marshal(a.Rules)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_simulcast_attachments
		(id, channel_id, destination_id, mode, rules, enabled, manual, manual_until, rights_confirmed_by,
		 rights_confirmed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (channel_id, destination_id) DO UPDATE SET mode = excluded.mode, rules = excluded.rules,
			enabled = excluded.enabled, manual = excluded.manual, manual_until = excluded.manual_until,
			rights_confirmed_by = excluded.rights_confirmed_by, rights_confirmed_at = excluded.rights_confirmed_at,
			updated_at = excluded.updated_at`,
		a.ID, a.ChannelID, a.DestinationID, a.Mode, string(rules), a.Enabled, nullable(a.Manual), s.tp(a.ManualUntil),
		nullable(a.RightsConfirmedBy), s.tp(a.RightsConfirmedAt), s.t(a.CreatedAt), s.t(a.UpdatedAt))
	return err
}

func (s *store) deleteAttachment(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tv_simulcast_attachments WHERE id = $1`, id)
	return err
}

func (s *store) setAttachment(ctx context.Context, id string, cols map[string]any) error {
	return s.set(ctx, "tv_simulcast_attachments", id, cols)
}

const simOutCols = `id, attachment_id, channel_id, destination_id, push_id, input_id, input_uid, cf_output_uid, state,
	window_start, window_end, started_at, ended_at, minutes, detail, error, cf_status, created_at`

func scanSimOut(sc interface{ Scan(...any) error }) (SimOutput, error) {
	var o SimOutput
	var id, att, ch, dest, push, in, inUID, uid, detail, errS, cfst ns
	var ws, we, st, en, created nt
	var minutes sql.NullFloat64
	if err := sc.Scan(&id, &att, &ch, &dest, &push, &in, &inUID, &uid, &o.State, &ws, &we, &st, &en, &minutes,
		&detail, &errS, &cfst, &created); err != nil {
		return o, err
	}
	o.ID, o.AttachmentID, o.ChannelID, o.DestinationID, o.PushID, o.InputID = id.S, att.S, ch.S, dest.S, push.S, in.S
	o.InputUID, o.CFOutputUID, o.Detail, o.Error, o.CFStatus = inUID.S, uid.S, detail.S, errS.S, cfst.S
	o.WindowStart, o.WindowEnd, o.StartedAt, o.EndedAt, o.CreatedAt = ws.ptr(), we.ptr(), st.ptr(), en.ptr(), created.T
	o.Minutes = minutes.Float64
	return o, nil
}

func (s *store) querySimOutputs(ctx context.Context, where string, args ...any) ([]SimOutput, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+simOutCols+` FROM tv_simulcast_outputs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SimOutput
	for rows.Next() {
		o, err := scanSimOut(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, rows.Err()
}

// openOutputs are the outputs not ended.
func (s *store) openOutputs(ctx context.Context) ([]SimOutput, error) {
	return s.querySimOutputs(ctx, `WHERE ended_at IS NULL`)
}

// outputsSince are the outputs open or ended at or after t.
func (s *store) outputsSince(ctx context.Context, t time.Time) ([]SimOutput, error) {
	all, err := s.querySimOutputs(ctx, ``)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, o := range all {
		if o.EndedAt == nil || !o.EndedAt.Before(t) {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *store) insertSimOutput(ctx context.Context, o SimOutput) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_simulcast_outputs
		(id, attachment_id, channel_id, destination_id, state, window_start, window_end, detail, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		o.ID, nullable(o.AttachmentID), nullable(o.ChannelID), nullable(o.DestinationID), o.State,
		s.tp(o.WindowStart), s.tp(o.WindowEnd), nullable(o.Detail), s.t(o.CreatedAt))
	return err
}

func (s *store) setSimOutput(ctx context.Context, id string, cols map[string]any) error {
	return s.set(ctx, "tv_simulcast_outputs", id, cols)
}

// putSimulcastUsage writes a day's output minutes (never decreasing).
func (s *store) putSimulcastUsage(ctx context.Context, day string, minutes float64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tv_remote_usage_daily (day, simulcast_minutes, updated_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (day) DO UPDATE SET
			simulcast_minutes = CASE WHEN excluded.simulcast_minutes > tv_remote_usage_daily.simulcast_minutes
				THEN excluded.simulcast_minutes ELSE tv_remote_usage_daily.simulcast_minutes END,
			updated_at = excluded.updated_at`, day, minutes, s.t(at))
	return err
}

// setHolds writes a push's holds.
func (s *store) setHolds(ctx context.Context, push string, holds []string) error {
	if holds == nil {
		holds = []string{}
	}
	b, _ := json.Marshal(holds)
	_, err := s.db.ExecContext(ctx, `UPDATE tv_remote_pushes SET holds = $1 WHERE id = $2`, string(b), push)
	return err
}
