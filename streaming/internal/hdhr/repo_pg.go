package hdhr

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGRepo is the production Repo over the streaming pgx pool (slot 0084
// hdhr_device + the slot-0081 channels lineup).
type PGRepo struct {
	pool *pgxpool.Pool
}

// NewPGRepo builds a PGRepo.
func NewPGRepo(pool *pgxpool.Pool) *PGRepo { return &PGRepo{pool: pool} }

// Device loads the singleton, lazily provisioning a disabled row (stable
// DeviceID/UUID generated once, D3/D7) if none exists yet.
func (r *PGRepo) Device(ctx context.Context) (Device, error) {
	dev, err := r.load(ctx)
	if err == nil {
		return dev, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Device{}, err
	}
	// Provision a disabled singleton with a stable identity.
	deviceID := newDeviceID()
	_, err = r.pool.Exec(ctx, `
		INSERT INTO hdhr_device (id, device_id, device_uuid, friendly_name, tuner_count, enabled)
		VALUES (1, $1, $2, 'Maktaba', 4, false)
		ON CONFLICT (id) DO NOTHING
	`, deviceID, uuid.NewString())
	if err != nil {
		return Device{}, err
	}
	return r.load(ctx)
}

func (r *PGRepo) load(ctx context.Context) (Device, error) {
	var d Device
	err := r.pool.QueryRow(ctx, `
		SELECT device_id, device_uuid, friendly_name, tuner_count, enabled
		FROM hdhr_device WHERE id = 1
	`).Scan(&d.DeviceID, &d.UUID, &d.FriendlyName, &d.TunerCount, &d.Enabled)
	return d, err
}

// Lineup returns the enabled channels in dial order.
func (r *PGRepo) Lineup(ctx context.Context) ([]LineupChannel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, number, name, slug FROM channels
		WHERE enabled = true ORDER BY sort_order ASC, number ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineupChannel
	for rows.Next() {
		var c LineupChannel
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.Slug); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// newDeviceID derives an 8-hex-char HDHomeRun-style device id from a
// fresh UUID (the format Plex expects).
func newDeviceID() string {
	u := uuid.New()
	return strUpperHex(u[:4])
}

func strUpperHex(b []byte) string {
	const hex = "0123456789ABCDEF"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = hex[c>>4]
		out[i*2+1] = hex[c&0x0f]
	}
	return string(out)
}

// GuideChannels returns the enabled channels for the XMLTV/M3U guide.
func (r *PGRepo) GuideChannels(ctx context.Context) ([]GuideChannel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, number, name, slug, COALESCE(logo_path, ''), category FROM channels
		WHERE enabled = true ORDER BY sort_order ASC, number ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuideChannel
	for rows.Next() {
		var c GuideChannel
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.Slug, &c.LogoPath, &c.Category); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GuidePrograms returns blocks of enabled channels overlapping
// [start, end), ordered by channel then time.
func (r *PGRepo) GuidePrograms(ctx context.Context, start, end time.Time) ([]GuideProgram, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT cp.channel_id, cp.kind, cp.start_at, cp.end_at, cp.title_snapshot
		FROM channel_programs cp
		JOIN channels c ON c.id = cp.channel_id AND c.enabled = true
		WHERE cp.start_at < $2 AND cp.end_at > $1
		ORDER BY cp.channel_id ASC, cp.start_at ASC
	`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuideProgram
	for rows.Next() {
		var p GuideProgram
		if err := rows.Scan(&p.ChannelID, &p.Kind, &p.StartAt, &p.EndAt, &p.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ChannelLogo returns a channel's logo_path ("" when unset).
func (r *PGRepo) ChannelLogo(ctx context.Context, id uuid.UUID) (string, error) {
	var logo *string
	err := r.pool.QueryRow(ctx, `SELECT logo_path FROM channels WHERE id = $1`, id).Scan(&logo)
	if err != nil {
		return "", err
	}
	if logo == nil {
		return "", nil
	}
	return *logo, nil
}
