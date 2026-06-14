package channels

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// repo wraps the SQL access to `channels` (+ a cheap now-playing join to
// `channel_programs`). Kept thin: the interesting logic (validation,
// slug, scope) lives in pure helpers so it is unit-tested without a DB.
type repo struct {
	db *sql.DB
}

const channelCols = `id, library_id, number, name, slug, logo_path, category,
	mode, mode_config, source_filter, transition, enabled, sort_order,
	created_at, updated_at`

func (rp *repo) get(ctx context.Context, id string) (Channel, error) {
	row := rp.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id = $1`, id)
	return scanChannel(row)
}

// list returns all channels matching the optional filters, ordered by
// dial position. Library-scoping by the caller's ACL happens in the
// handler so the repo stays a pure data accessor.
func (rp *repo) list(ctx context.Context, f listFilter) ([]Channel, error) {
	q := `SELECT ` + channelCols + ` FROM channels`
	var conds []string
	var args []any
	i := 1
	if f.libraryID != "" {
		conds = append(conds, "library_id = $"+itoa(i))
		args = append(args, f.libraryID)
		i++
	}
	if f.category != "" {
		conds = append(conds, "category = $"+itoa(i))
		args = append(args, f.category)
		i++
	}
	if f.enabled != nil {
		conds = append(conds, "enabled = $"+itoa(i))
		args = append(args, *f.enabled)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY sort_order ASC, number ASC"
	rows, err := rp.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type listFilter struct {
	libraryID string
	category  string
	enabled   *bool
}

// slugTaken reports whether slug is already used within the same scope
// (library_id, or the multi-library bucket when libraryID is nil). Used
// to pick a collision suffix at create time (D4).
func (rp *repo) slugTaken(ctx context.Context, libraryID *string, slug string) (bool, error) {
	var n int
	var err error
	if libraryID == nil {
		err = rp.db.QueryRowContext(ctx,
			`SELECT count(*) FROM channels WHERE library_id IS NULL AND slug = $1`, slug).Scan(&n)
	} else {
		err = rp.db.QueryRowContext(ctx,
			`SELECT count(*) FROM channels WHERE library_id = $1 AND slug = $2`, *libraryID, slug).Scan(&n)
	}
	return n > 0, err
}

func (rp *repo) insert(ctx context.Context, c Channel) error {
	_, err := rp.db.ExecContext(ctx, `
		INSERT INTO channels (`+channelCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`,
		c.ID, nullStr(c.LibraryID), c.Number, c.Name, c.Slug, nullStr(c.LogoPath),
		c.Category, c.Mode, jsonOrEmpty(c.ModeConfig), jsonOrNull(c.SourceFilter),
		c.Transition, c.Enabled, c.SortOrder, c.CreatedAt, c.UpdatedAt,
	)
	return err
}

func (rp *repo) delete(ctx context.Context, id string) error {
	_, err := rp.db.ExecContext(ctx, `DELETE FROM channels WHERE id=$1`, id)
	return err
}

// nowPlaying loads the current schedule block for a channel (best-effort:
// returns nil,nil when there is no current block, and swallows a missing
// channel_programs table so 27.1 works before 27.2 lands).
func (rp *repo) nowPlaying(ctx context.Context, channelID string, now time.Time) (*NowPlaying, error) {
	row := rp.db.QueryRowContext(ctx, `
		SELECT title_snapshot, start_at, end_at
		FROM channel_programs
		WHERE channel_id = $1 AND start_at <= $2 AND end_at > $2
		ORDER BY start_at DESC LIMIT 1
	`, channelID, now)
	var snap []byte
	var startAt, endAt time.Time
	if err := row.Scan(&snap, &startAt, &endAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err // missing table / other → caller treats as best-effort
	}
	np := &NowPlaying{
		Title:   titleFromSnapshot(snap),
		StartAt: startAt,
		EndAt:   endAt,
	}
	if d := endAt.Sub(startAt).Seconds(); d > 0 {
		np.Progress = clamp01(now.Sub(startAt).Seconds() / d)
	}
	return np, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanChannel(rs rowScanner) (Channel, error) {
	var c Channel
	var lib, logo sql.NullString
	var modeCfg, srcFilter []byte
	if err := rs.Scan(
		&c.ID, &lib, &c.Number, &c.Name, &c.Slug, &logo, &c.Category,
		&c.Mode, &modeCfg, &srcFilter, &c.Transition, &c.Enabled, &c.SortOrder,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return Channel{}, err
	}
	if lib.Valid {
		c.LibraryID = &lib.String
	}
	if logo.Valid {
		c.LogoPath = &logo.String
	}
	if len(modeCfg) > 0 {
		c.ModeConfig = modeCfg
	}
	if len(srcFilter) > 0 {
		c.SourceFilter = srcFilter
	}
	return c, nil
}
