package guide

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// repo is the time-range read layer over channel_programs + channels.
type repo struct {
	db *sql.DB
}

// ChannelMeta is the channel header used by the grid, XMLTV <channel>,
// and M3U #EXTINF lines.
type ChannelMeta struct {
	ID        string
	LibraryID *string
	Number    int
	Name      string
	Slug      string
	LogoPath  *string
	Category  string
}

// ProgramRow is one channel_programs block as read for the guide.
type ProgramRow struct {
	ChannelID string
	Seq       int64
	Kind      string
	StartAt   time.Time
	EndAt     time.Time
	Snapshot  Snapshot
}

// channelsVisible returns enabled channels, optionally filtered by
// category. ACL scoping is applied by the caller against the principal.
func (rp *repo) channelsVisible(ctx context.Context, category string) ([]ChannelMeta, error) {
	q := `SELECT id, library_id, number, name, slug, logo_path, category
	      FROM channels WHERE enabled = true`
	var args []any
	if category != "" {
		q += " AND category = $1"
		args = append(args, category)
	}
	q += " ORDER BY sort_order ASC, number ASC"
	rows, err := rp.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelMeta{}
	for rows.Next() {
		var c ChannelMeta
		var lib, logo sql.NullString
		if err := rows.Scan(&c.ID, &lib, &c.Number, &c.Name, &c.Slug, &logo, &c.Category); err != nil {
			continue
		}
		if lib.Valid {
			c.LibraryID = &lib.String
		}
		if logo.Valid {
			c.LogoPath = &logo.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (rp *repo) oneChannel(ctx context.Context, id string) (ChannelMeta, error) {
	row := rp.db.QueryRowContext(ctx, `
		SELECT id, library_id, number, name, slug, logo_path, category
		FROM channels WHERE id = $1`, id)
	var c ChannelMeta
	var lib, logo sql.NullString
	if err := row.Scan(&c.ID, &lib, &c.Number, &c.Name, &c.Slug, &logo, &c.Category); err != nil {
		return ChannelMeta{}, err
	}
	if lib.Valid {
		c.LibraryID = &lib.String
	}
	if logo.Valid {
		c.LogoPath = &logo.String
	}
	return c, nil
}

// programsOverlapping returns blocks for the given channels overlapping
// [start, end), ordered by channel then time. A block overlaps when
// start_at < end AND end_at > start.
func (rp *repo) programsOverlapping(ctx context.Context, channelIDs []string, start, end time.Time) ([]ProgramRow, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}
	ph := make([]string, len(channelIDs))
	args := make([]any, 0, len(channelIDs)+2)
	for i, id := range channelIDs {
		ph[i] = "$" + itoa(i+1)
		args = append(args, id)
	}
	args = append(args, start, end)
	startPH := "$" + itoa(len(channelIDs)+1)
	endPH := "$" + itoa(len(channelIDs)+2)
	q := `SELECT channel_id, seq, kind, start_at, end_at, title_snapshot
	      FROM channel_programs
	      WHERE channel_id IN (` + strings.Join(ph, ",") + `)
	        AND start_at < ` + endPH + ` AND end_at > ` + startPH + `
	      ORDER BY channel_id ASC, start_at ASC`
	rows, err := rp.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProgramRow{}
	for rows.Next() {
		var p ProgramRow
		var snap []byte
		if err := rows.Scan(&p.ChannelID, &p.Seq, &p.Kind, &p.StartAt, &p.EndAt, &snap); err != nil {
			continue
		}
		p.Snapshot = parseSnapshot(snap)
		out = append(out, p)
	}
	return out, rows.Err()
}

// currentAndNext returns the current block (start ≤ now < end) and the
// immediately following block for one channel. Either may be nil.
func (rp *repo) currentAndNext(ctx context.Context, channelID string, now time.Time) (cur, next *ProgramRow, err error) {
	curRow := rp.db.QueryRowContext(ctx, `
		SELECT channel_id, seq, kind, start_at, end_at, title_snapshot
		FROM channel_programs
		WHERE channel_id = $1 AND start_at <= $2 AND end_at > $2
		ORDER BY start_at DESC LIMIT 1`, channelID, now)
	cur, err = scanProgram(curRow)
	if err != nil {
		return nil, nil, err
	}
	nextRow := rp.db.QueryRowContext(ctx, `
		SELECT channel_id, seq, kind, start_at, end_at, title_snapshot
		FROM channel_programs
		WHERE channel_id = $1 AND start_at >= $2
		ORDER BY start_at ASC LIMIT 1`, channelID, now)
	next, err = scanProgram(nextRow)
	if err != nil {
		return cur, nil, err
	}
	return cur, next, nil
}

// horizonUntil reads the generated horizon for a channel (AC2/AC10 guide
// marker). Best-effort: a missing channel_schedule_state row (27.2 not
// applied yet) returns zero time, no error.
func (rp *repo) horizonUntil(ctx context.Context, channelID string) (time.Time, bool) {
	var until sql.NullTime
	err := rp.db.QueryRowContext(ctx,
		`SELECT horizon_until FROM channel_schedule_state WHERE channel_id = $1`, channelID).Scan(&until)
	if err != nil || !until.Valid {
		return time.Time{}, false
	}
	return until.Time, true
}

func scanProgram(row *sql.Row) (*ProgramRow, error) {
	var p ProgramRow
	var snap []byte
	if err := row.Scan(&p.ChannelID, &p.Seq, &p.Kind, &p.StartAt, &p.EndAt, &snap); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	p.Snapshot = parseSnapshot(snap)
	return &p, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}
