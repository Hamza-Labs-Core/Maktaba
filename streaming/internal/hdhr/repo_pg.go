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
		return r.repairID(ctx, dev)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Device{}, err
	}
	// Provision a disabled singleton with a stable identity.
	deviceID := newDeviceID()
	_, err = r.pool.Exec(ctx, `
		INSERT INTO hdhr_device (id, device_id, device_uuid, friendly_name, tuner_count, enabled)
		VALUES (1, $1, $2, 'Maktaba', $3, false)
		ON CONFLICT (id) DO NOTHING
	`, deviceID, uuid.NewString(), DefaultTunerCount)
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
		WHERE enabled = true AND lifecycle = 'live' ORDER BY sort_order ASC, number ASC
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

// repairID gives a stored DeviceID without a valid HDHomeRun check digit
// (ids provisioned before the check, e.g. 87C7778E) a valid one, in place:
// only the last hex digit changes. libhdhomerun-based clients reject an id
// that fails hdhomerun_discover_validate_device_id.
func (r *PGRepo) repairID(ctx context.Context, dev Device) (Device, error) {
	fixed, changed := RepairDeviceID(dev.DeviceID)
	if !changed {
		return dev, nil
	}
	if _, err := r.pool.Exec(ctx, `UPDATE hdhr_device SET device_id = $1 WHERE id = 1 AND device_id = $2`,
		fixed, dev.DeviceID); err != nil {
		return dev, nil // serve the old id; the next read retries
	}
	dev.DeviceID = fixed
	return dev, nil
}

// newDeviceID derives an 8-hex-char HDHomeRun device id from a fresh UUID,
// with the check digit libhdhomerun validates (the format Plex expects).
func newDeviceID() string {
	for {
		u := uuid.New()
		id := WithCheckDigit(uint32(u[0])<<24 | uint32(u[1])<<16 | uint32(u[2])<<8 | uint32(u[3]))
		if id != 0 && id != hdhrDeviceIDWildcard {
			return FormatDeviceID(id)
		}
	}
}

// GuideChannels returns the enabled channels for the XMLTV/M3U guide.
func (r *PGRepo) GuideChannels(ctx context.Context) ([]GuideChannel, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, number, name, slug, COALESCE(logo_path, ''), category FROM channels
		WHERE enabled = true AND lifecycle = 'live' ORDER BY sort_order ASC, number ASC
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
		FROM channel_programs_live cp
		JOIN channels c ON c.id = cp.channel_id AND c.enabled = true AND c.lifecycle = 'live'
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
