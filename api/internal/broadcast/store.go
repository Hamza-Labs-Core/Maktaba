package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// States of a broadcast channel (slot 0226).
const (
	StateDraft   = "draft"
	StateLive    = "live"
	StateOffline = "offline"
	StateOffAir  = "off_air"
	StateRevoked = "revoked"
)

// Channel is one channel as the Broadcast page shows it.
type Channel struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Number      int             `json:"number"`
	Enabled     bool            `json:"enabled"`
	Configured  bool            `json:"configured"`
	Slug        string          `json:"slug"`
	Access      string          `json:"access"`
	Mode        string          `json:"mode"`
	Rungs       []string        `json:"rungs"`
	Countries   []string        `json:"countries"`
	State       string          `json:"state"`
	CloudID     string          `json:"-"`
	EdgeChannel string          `json:"edge_channel,omitempty"`
	Dropped     int             `json:"dropped_rungs"`
	Detail      string          `json:"detail,omitempty"`
	Report      json.RawMessage `json:"report,omitempty"`
	ReportedAt  *time.Time      `json:"reported_at,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	PublishedBy string          `json:"published_by,omitempty"`
}

// Published reports whether the channel is published (live or waiting at its slate).
func (c Channel) Published() bool { return c.State == StateLive || c.State == StateOffline }

// ErrNotFound is a channel that does not exist.
var ErrNotFound = errors.New("broadcast: no such channel")

// Store reads and writes the broadcast tables (Postgres).
type Store struct {
	DB *sql.DB
}

const channelCols = `c.id::text, c.name, c.number, c.enabled, b.channel_id IS NOT NULL,
	COALESCE(b.slug, ''), COALESCE(b.access, 'invite'), COALESCE(b.mode, 'on_demand'),
	COALESCE(b.rungs::text, '[]'), COALESCE(b.countries::text, '[]'), COALESCE(b.state, 'draft'),
	COALESCE(b.cloud_id, ''), COALESCE(b.edge_channel, ''), COALESCE(b.dropped_rungs, 0),
	COALESCE(b.detail, ''), COALESCE(b.report::text, '{}'), b.reported_at, b.published_at,
	COALESCE(b.published_by, '')`

type scanner interface{ Scan(...any) error }

func scanChannel(r scanner) (Channel, error) {
	var c Channel
	var rungs, countries, report string
	if err := r.Scan(&c.ID, &c.Name, &c.Number, &c.Enabled, &c.Configured, &c.Slug, &c.Access, &c.Mode,
		&rungs, &countries, &c.State, &c.CloudID, &c.EdgeChannel, &c.Dropped, &c.Detail, &report,
		&c.ReportedAt, &c.PublishedAt, &c.PublishedBy); err != nil {
		return c, err
	}
	_ = json.Unmarshal([]byte(rungs), &c.Rungs)
	_ = json.Unmarshal([]byte(countries), &c.Countries)
	if len(c.Rungs) == 0 {
		c.Rungs = append([]string(nil), DefaultLadder...)
	}
	if c.Countries == nil {
		c.Countries = []string{}
	}
	if report != "{}" {
		c.Report = json.RawMessage(report)
	}
	return c, nil
}

// List is every channel, with its broadcast setup when it has one.
func (s *Store) List(ctx context.Context) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+channelCols+`
		  FROM channels c LEFT JOIN broadcast_channels b ON b.channel_id = c.id
		 ORDER BY c.sort_order, c.number`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Get is one channel.
func (s *Store) Get(ctx context.Context, id string) (Channel, error) {
	c, err := scanChannel(s.DB.QueryRowContext(ctx, `SELECT `+channelCols+`
		  FROM channels c LEFT JOIN broadcast_channels b ON b.channel_id = c.id
		 WHERE c.id::text = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// SaveSetup writes the channel's setup (a new row is a draft).
func (s *Store) SaveSetup(ctx context.Context, id, slug string, st Setup, countries []string) error {
	rungs, _ := json.Marshal(st.Rungs)
	cs, _ := json.Marshal(countries)
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO broadcast_channels (channel_id, slug, access, mode, rungs, countries)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6::jsonb)
		ON CONFLICT (channel_id) DO UPDATE SET slug = EXCLUDED.slug, access = EXCLUDED.access,
		       mode = EXCLUDED.mode, rungs = EXCLUDED.rungs, countries = EXCLUDED.countries,
		       updated_at = now()`, id, slug, st.Access, st.Mode, string(rungs), string(cs))
	return err
}

// SlugTaken reports whether another channel has slug.
func (s *Store) SlugTaken(ctx context.Context, id, slug string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_channels WHERE slug = $1 AND channel_id::text <> $2`,
		slug, id).Scan(&n)
	return n > 0, err
}

// PublishedChannels are the channels published now, oldest first.
func (s *Store) PublishedChannels(ctx context.Context) ([]Channel, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Channel
	for _, c := range all {
		if c.Published() {
			out = append(out, c)
		}
	}
	return out, nil
}

// MarkPublished records a publish: the cloud's id, the state, who and when.
func (s *Store) MarkPublished(ctx context.Context, id, cloudID, state, detail, by string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE broadcast_channels
		   SET cloud_id = $2, state = $3, detail = $4, published_at = now(), published_by = $5,
		       dropped_rungs = 0, updated_at = now()
		 WHERE channel_id::text = $1`, id, cloudID, state, detail, by)
	return err
}

// SetState changes the state and its detail (a token that expired, a restore).
func (s *Store) SetState(ctx context.Context, id, state, detail string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE broadcast_channels SET state = $2, detail = $3, updated_at = now()
		 WHERE channel_id::text = $1`, id, state, detail)
	return err
}

// MarkUnpublished takes the channel back to a draft; cloudPending keeps the cloud's id so
// the refresher unpublishes it there once the cloud answers.
func (s *Store) MarkUnpublished(ctx context.Context, id string, cloudPending bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM broadcast_ingest_tokens WHERE channel_id::text = $1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE broadcast_channels
		   SET state = 'draft', detail = '', dropped_rungs = 0, report = '{}'::jsonb, reported_at = NULL,
		       cloud_id = CASE WHEN $2 THEN cloud_id ELSE NULL END, updated_at = now()
		 WHERE channel_id::text = $1`, id, cloudPending); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingUnpublish are drafts whose unpublish has not reached the cloud yet.
func (s *Store) PendingUnpublish(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT channel_id::text, cloud_id FROM broadcast_channels
		 WHERE state = 'draft' AND cloud_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, cloud string
		if err := rows.Scan(&id, &cloud); err != nil {
			return nil, err
		}
		out[id] = cloud
	}
	return out, rows.Err()
}

// ClearCloudID forgets the cloud's id once the cloud has the channel unpublished.
func (s *Store) ClearCloudID(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE broadcast_channels SET cloud_id = NULL WHERE channel_id::text = $1 AND state = 'draft'`, id)
	return err
}

// SetDropped records how many top rungs the uplink rule took off.
func (s *Store) SetDropped(ctx context.Context, id string, dropped int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE broadcast_channels SET dropped_rungs = $2, detail = 'rungDropped', updated_at = now()
		 WHERE channel_id::text = $1`, id, dropped)
	return err
}

// AddConfirmation stores the admin's rights confirmation on the home.
func (s *Store) AddConfirmation(ctx context.Context, channelID string, version int, by, userID string) (time.Time, error) {
	var at time.Time
	var uid any
	if userID != "" {
		uid = userID
	}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO broadcast_confirmations (channel_id, agreement_version, confirmed_by, confirmed_by_user)
		VALUES ($1::uuid, $2, $3, (SELECT id FROM users WHERE id::text = $4)) RETURNING confirmed_at`,
		channelID, version, by, uid).Scan(&at)
	return at, err
}

// Uplink is the last measurement.
type Uplink struct {
	Kbps       int        `json:"kbps"`
	MeasuredAt *time.Time `json:"measured_at,omitempty"`
}

// LatestUplink is the newest measurement (zero: none).
func (s *Store) LatestUplink(ctx context.Context) (Uplink, error) {
	var u Uplink
	var at time.Time
	err := s.DB.QueryRowContext(ctx, `SELECT kbps, measured_at FROM broadcast_uplink ORDER BY measured_at DESC LIMIT 1`).
		Scan(&u.Kbps, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Uplink{}, nil
	}
	if err == nil {
		u.MeasuredAt = &at
	}
	return u, err
}

// AddUplink stores a measurement and keeps the last 100.
func (s *Store) AddUplink(ctx context.Context, kbps int, source string) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO broadcast_uplink (kbps, source) VALUES ($1, $2)`, kbps, source); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM broadcast_uplink WHERE id NOT IN
		(SELECT id FROM broadcast_uplink ORDER BY measured_at DESC LIMIT 100)`)
	return err
}

// SaveToken stores the channel's ingest token and its edge id.
func (s *Store) SaveToken(ctx context.Context, id string, t IngestToken) error {
	rungs, _ := json.Marshal(t.Rungs)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO broadcast_ingest_tokens (channel_id, token, ingest_url, edge_channel, rungs, max_top_kbps, expires_at, fetched_at)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6, $7, now())
		ON CONFLICT (channel_id) DO UPDATE SET token = EXCLUDED.token, ingest_url = EXCLUDED.ingest_url,
		       edge_channel = EXCLUDED.edge_channel, rungs = EXCLUDED.rungs, max_top_kbps = EXCLUDED.max_top_kbps,
		       expires_at = EXCLUDED.expires_at, fetched_at = now()`,
		id, t.Token, t.IngestURL, t.Channel, string(rungs), t.MaxTopKbps, t.ExpiresAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE broadcast_channels SET edge_channel = $2 WHERE channel_id::text = $1`, id, t.Channel); err != nil {
		return err
	}
	return tx.Commit()
}

// TokenAge is when the channel's token was fetched and when it expires (zero: none).
func (s *Store) TokenAge(ctx context.Context, id string) (fetched, expires time.Time, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT fetched_at, expires_at FROM broadcast_ingest_tokens WHERE channel_id::text = $1`, id).
		Scan(&fetched, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, nil
	}
	return fetched, expires, err
}

// AnyToken is a live ingest token and its URL (for the speed test).
func (s *Store) AnyToken(ctx context.Context) (token, ingestURL string, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT token, ingest_url FROM broadcast_ingest_tokens
		 WHERE expires_at > now() ORDER BY fetched_at DESC LIMIT 1`).Scan(&token, &ingestURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return token, ingestURL, err
}

// rules reads app_settings' rules.<section> changes (nil: none).
func (s *Store) rules(ctx context.Context, key string) map[string]json.RawMessage {
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	return m
}

// Settings are rules.broadcast with the defaults under them.
func (s *Store) Settings(ctx context.Context) Settings {
	out := DefaultSettings
	m := s.rules(ctx, "rules.broadcast")
	_ = json.Unmarshal(m["uplink_share"], &out.UplinkShare)
	_ = json.Unmarshal(m["rung_slot_weight"], &out.RungSlotWeight)
	_ = json.Unmarshal(m["allow_software_rungs"], &out.AllowSoftwareRungs)
	return out.Clamp()
}

// Output is what the channels' own encode is (rules.tv_output, streaming's defaults under it).
type Output struct {
	Source
	Encoder   string
	MaxActive int
	NVENCMax  int
	VTMax     int
}

// Output reads rules.tv_output.
func (s *Store) Output(ctx context.Context) Output {
	o := Output{Source: Source{VideoKbps: 6000, AudioKbps: 192, Height: 1080}, Encoder: "auto", NVENCMax: 3}
	m := s.rules(ctx, "rules.tv_output")
	_ = json.Unmarshal(m["video_kbps"], &o.VideoKbps)
	_ = json.Unmarshal(m["audio_kbps"], &o.AudioKbps)
	_ = json.Unmarshal(m["encoder"], &o.Encoder)
	_ = json.Unmarshal(m["max_active_channels"], &o.MaxActive)
	_ = json.Unmarshal(m["nvenc_max_sessions"], &o.NVENCMax)
	_ = json.Unmarshal(m["videotoolbox_max_sessions"], &o.VTMax)
	var res string
	if json.Unmarshal(m["resolution"], &res) == nil {
		if _, h, ok := strings.Cut(strings.ToLower(res), "x"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n > 0 {
				o.Height = n
			}
		}
	}
	o.Encoder = strings.ToLower(o.Encoder)
	if o.MaxActive <= 0 { // auto (streaming's EffectiveMaxActive)
		o.MaxActive = 8
		if o.Encoder == "videotoolbox" {
			o.MaxActive = 16
		}
	}
	return o
}

// ReserveSlots is tv_public.reserve_slots (Epic 34; default 2).
func (s *Store) ReserveSlots(ctx context.Context) int {
	n := 2
	var raw string
	if s.DB.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'tv_public'`).Scan(&raw) == nil {
		var v struct {
			ReserveSlots *int `json:"reserve_slots"`
		}
		if json.Unmarshal([]byte(raw), &v) == nil && v.ReserveSlots != nil && *v.ReserveSlots >= 0 {
			n = *v.ReserveSlots
		}
	}
	return n
}

// HostLoad is the slots and sessions the channel's host holds now, the channel left out:
// its placed channels (one slot and one session each) and the published channels' lower
// rungs (weight and a session each), and the host's capacity override.
func (s *Store) HostLoad(ctx context.Context, channelID string, published []Channel, weight float64) (inUse float64, sessions int, capacity *int, err error) {
	var host string
	err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(
		    (SELECT host_id FROM channel_placements WHERE channel_id::text = $1 AND state <> 'released'),
		    (SELECT host_id FROM streaming_hosts WHERE role = 'home' ORDER BY host_id LIMIT 1), '')`, channelID).Scan(&host)
	if err != nil {
		return 0, 0, nil, err
	}
	var placed int
	var override sql.NullInt64
	if host != "" {
		if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM channel_placements
			 WHERE host_id = $1 AND state <> 'released' AND channel_id::text <> $2`, host, channelID).Scan(&placed); err != nil {
			return 0, 0, nil, err
		}
		if err = s.DB.QueryRowContext(ctx, `SELECT capacity_override FROM streaming_hosts WHERE host_id = $1`, host).
			Scan(&override); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, 0, nil, err
		}
	}
	inUse, sessions = float64(placed), placed
	for _, c := range published {
		if c.ID == channelID {
			continue
		}
		enc := EncodedRungs(Effective(c.Rungs, c.Dropped))
		inUse += weight * float64(enc)
		sessions += enc
	}
	if override.Valid {
		v := int(override.Int64)
		capacity = &v
	}
	return inUse, sessions, capacity, nil
}

// Publishable is the seam story 50.23 fills: how much of a time window a channel can fill
// with publishable items. Until 50.23 marks items, the admin's rights confirmation (check
// 6) covers every item the channel airs, so every scheduled programme counts.
type Publishable interface {
	Seconds(ctx context.Context, db *sql.DB, channelID string, from, to time.Time) (int, error)
}

// ScheduledPublishable counts every scheduled programme and filler in the window.
type ScheduledPublishable struct{}

// Seconds is the scheduled time of the channel inside [from, to).
func (ScheduledPublishable) Seconds(ctx context.Context, db *sql.DB, channelID string, from, to time.Time) (int, error) {
	var secs float64
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(EXTRACT(EPOCH FROM (LEAST(end_at, $3) - GREATEST(start_at, $2)))), 0)
		  FROM channel_programs
		 WHERE channel_id::text = $1 AND end_at > $2 AND start_at < $3 AND kind IN ('program', 'filler')`,
		channelID, from, to).Scan(&secs)
	return int(secs), err
}
