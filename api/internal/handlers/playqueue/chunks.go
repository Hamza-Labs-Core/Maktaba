package playqueue

// The queue's rows: writing its chunks, moving, Play next and Add to queue,
// reshuffling, and the window the apps read.
//
// A move reads the source's next chunk (when fewer than RefillWithin items are
// left after the new current one) before its transaction, then writes it in
// the transaction only if nobody moved the queue meanwhile: the source's reads
// run through other routes on other connections, never inside the write.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Queue is one play_queues row.
type Queue struct {
	ID         string
	UserID     string
	DeviceID   string
	Source     SourceRef
	Shuffle    bool
	Seed       int64
	Cursor     string
	Done       bool
	CurrentPos int
	Version    int
	CreatedAt  time.Time
	UsedAt     time.Time
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}

// tsArg is a timestamp parameter: native on Postgres, the columns' text on SQLite.
func tsArg(db *sql.DB, t time.Time) any {
	if isSQLite(db) {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return t.UTC()
}

// nullTime scans a timestamp stored natively (Postgres) or as text (SQLite).
type nullTime struct{ T time.Time }

func (n *nullTime) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		n.T = x.UTC()
		return nil
	case string:
		return n.parse(x)
	case []byte:
		return n.parse(string(x))
	}
	return fmt.Errorf("nullTime: cannot scan %T", v)
}

func (n *nullTime) parse(s string) error {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			n.T = t.UTC()
			return nil
		}
	}
	return fmt.Errorf("nullTime: cannot parse %q", s)
}

// loadQueue reads the queue id of uid (nil when there is none); lock takes the
// row FOR UPDATE on Postgres inside a transaction.
func (h *Handler) loadQueue(ctx context.Context, q querier, uid, id string, lock bool) (*Queue, error) {
	suffix := ""
	if lock && !isSQLite(h.DB) {
		suffix = " FOR UPDATE"
	}
	var out Queue
	var device, cursor sql.NullString
	var seed sql.NullInt64
	var filters string
	var created, used nullTime
	err := q.QueryRowContext(ctx, `SELECT CAST(id AS TEXT), CAST(user_id AS TEXT), device_id, source_kind, source_id,
			CAST(source_filters AS TEXT), shuffle, seed, source_cursor, source_done, current_pos, version, created_at, used_at
		FROM play_queues WHERE id = $1 AND user_id = $2`+suffix, id, uid).Scan(
		&out.ID, &out.UserID, &device, &out.Source.Kind, &out.Source.ID, &filters, &out.Shuffle, &seed, &cursor,
		&out.Done, &out.CurrentPos, &out.Version, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.DeviceID, out.Cursor, out.Seed = device.String, cursor.String, seed.Int64
	out.CreatedAt, out.UsedAt = created.T, used.T
	out.Source.Filters = map[string]string{}
	if filters != "" {
		if err := json.Unmarshal([]byte(filters), &out.Source.Filters); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// insertQueue writes a new queue and its first chunk.
func (h *Handler) insertQueue(ctx context.Context, q Queue, ids []string) error {
	filters, err := json.Marshal(q.Source.Filters)
	if err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := tsArg(h.DB, h.now())
	var seed, device, cursor any
	if q.Shuffle {
		seed = q.Seed
	}
	if q.DeviceID != "" {
		device = q.DeviceID
	}
	if q.Cursor != "" {
		cursor = q.Cursor
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO play_queues (id, user_id, device_id, source_kind, source_id, source_filters,
			shuffle, seed, source_cursor, source_done, current_pos, version, created_at, used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 1, $12, $12)`,
		q.ID, q.UserID, device, q.Source.Kind, q.Source.ID, string(filters), q.Shuffle, seed, cursor, q.Done, q.CurrentPos, now); err != nil {
		return err
	}
	if err := insertItems(ctx, tx, q.ID, 0, ids, "source"); err != nil {
		return err
	}
	return tx.Commit()
}

// insertItems writes ids at from, from+1, … with origin.
func insertItems(ctx context.Context, tx querier, queueID string, from int, ids []string, origin string) error {
	const batch = 200
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		var sb strings.Builder
		args := []any{queueID, origin}
		sb.WriteString(`INSERT INTO play_queue_items (queue_id, pos, video_id, origin) VALUES `)
		for i, id := range ids[start:end] {
			if i > 0 {
				sb.WriteString(", ")
			}
			args = append(args, from+start+i, id)
			fmt.Fprintf(&sb, "($1, $%d, $%d, $2)", len(args)-1, len(args))
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

// stats is how a queue's held items stand around pos: how many come after it,
// the last pos, how many are held.
func stats(ctx context.Context, q querier, queueID string, pos int) (after, last, held int, err error) {
	var lastN sql.NullInt64
	err = q.QueryRowContext(ctx, `SELECT COUNT(*), MAX(pos), COALESCE(SUM(CASE WHEN pos > $2 THEN 1 ELSE 0 END), 0)
		FROM play_queue_items WHERE queue_id = $1`, queueID, pos).Scan(&held, &lastN, &after)
	if lastN.Valid {
		last = int(lastN.Int64)
	} else {
		last = -1
	}
	return after, last, held, err
}

// heldSource is the ids the queue holds from its source (a new chunk skips them).
func heldSource(ctx context.Context, q querier, queueID string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT CAST(video_id AS TEXT) FROM play_queue_items WHERE queue_id = $1 AND origin = 'source'`, queueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// move sets the current item to pos for a caller holding version: stale when
// the version is old. It writes the source's next chunk when the new current
// item is near the end, and drops the oldest played items past MaxHeld.
func (h *Handler) move(ctx context.Context, _ *principal.Principal, id, uid string, pos, version int) (bool, *httperror.Error, error) {
	q, err := h.loadQueue(ctx, h.DB, uid, id, false)
	if err != nil || q == nil {
		return false, nil, err
	}
	if version != q.Version {
		return true, nil, nil
	}
	var one int
	err = h.DB.QueryRowContext(ctx, `SELECT 1 FROM play_queue_items WHERE queue_id = $1 AND pos = $2`, id, pos).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, httperror.Unprocessable([]httperror.FieldError{{Field: "pos", Message: "not an item of the queue"}}), nil
	}
	if err != nil {
		return false, nil, err
	}
	after, _, _, err := stats(ctx, h.DB, id, pos)
	if err != nil {
		return false, nil, err
	}
	var next *chunk
	if after < RefillWithin && !q.Done {
		res, e, err := h.resolve(ctx, q.Source, q.Shuffle, q.Seed)
		if e != nil {
			// the source went away (deleted, or the caller lost it): the
			// queue keeps what it holds and reads no more
			next = &chunk{cursor: q.Cursor, done: true}
		} else if err != nil {
			return false, nil, err
		} else {
			c, e, err := res.src.read(ctx, q.Cursor, ChunkSize)
			if err != nil {
				return false, nil, err
			}
			if e != nil {
				c = chunk{cursor: q.Cursor, done: true}
			}
			next = &c
		}
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := h.loadQueue(ctx, tx, uid, id, true)
	if err != nil {
		return false, nil, err
	}
	if locked == nil {
		return false, httperror.NotFound("play queue " + id), nil
	}
	if locked.Version != version {
		return true, nil, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE play_queues SET current_pos = $1, version = version + 1, used_at = $2 WHERE id = $3`,
		pos, tsArg(h.DB, h.now()), id); err != nil {
		return false, nil, err
	}
	if next != nil && locked.Cursor == q.Cursor && !locked.Done {
		if err := h.appendChunk(ctx, tx, id, *next); err != nil {
			return false, nil, err
		}
	}
	if err := trim(ctx, tx, id, pos); err != nil {
		return false, nil, err
	}
	return false, nil, tx.Commit()
}

// appendChunk writes a chunk after every item held (skipping videos the queue
// already holds from its source) and moves the source's cursor.
func (h *Handler) appendChunk(ctx context.Context, tx querier, id string, c chunk) error {
	held, err := heldSource(ctx, tx, id)
	if err != nil {
		return err
	}
	ids := dedupe(c.ids, held)
	_, last, _, err := stats(ctx, tx, id, 0)
	if err != nil {
		return err
	}
	if err := insertItems(ctx, tx, id, last+1, ids, "source"); err != nil {
		return err
	}
	var cursor any
	if c.cursor != "" {
		cursor = c.cursor
	}
	_, err = tx.ExecContext(ctx, `UPDATE play_queues SET source_cursor = $1, source_done = $2 WHERE id = $3`, cursor, c.done, id)
	return err
}

// trim drops the oldest played items while more than MaxHeld are held, keeping
// the window before the current one.
func trim(ctx context.Context, tx querier, id string, current int) error {
	_, _, held, err := stats(ctx, tx, id, current)
	if err != nil || held <= MaxHeld {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM play_queue_items WHERE queue_id = $1 AND pos IN (
			SELECT pos FROM play_queue_items WHERE queue_id = $1 AND pos < $2 ORDER BY pos LIMIT $3)`,
		id, current-defaultBefore, held-MaxHeld)
	return err
}

// add puts ids after the current item (and the Play next items after it), or
// after every item held, and bumps the version.
func (h *Handler) add(ctx context.Context, id, uid string, ids []string, where string) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q, err := h.loadQueue(ctx, tx, uid, id, true)
	if err != nil || q == nil {
		return err
	}
	origin := "added"
	var at int
	if where == "end" {
		_, last, _, err := stats(ctx, tx, id, q.CurrentPos)
		if err != nil {
			return err
		}
		at = last + 1
	} else {
		origin = "next"
		// after the current item and the run of Play next items behind it
		after := q.CurrentPos
		rows, err := tx.QueryContext(ctx, `SELECT pos, origin FROM play_queue_items WHERE queue_id = $1 AND pos > $2 ORDER BY pos LIMIT 500`, id, after)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pos int
			var o string
			if err := rows.Scan(&pos, &o); err != nil {
				rows.Close()
				return err
			}
			if o != "next" {
				break
			}
			after = pos
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// make room: shift everything after it by len(ids), through the
		// negatives so the primary key never sees two rows at one pos
		k := len(ids)
		if _, err := tx.ExecContext(ctx, `UPDATE play_queue_items SET pos = -(pos + $3) - 1 WHERE queue_id = $1 AND pos > $2`, id, after, k); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE play_queue_items SET pos = -pos - 1 WHERE queue_id = $1 AND pos < 0`, id); err != nil {
			return err
		}
		at = after + 1
	}
	if err := insertItems(ctx, tx, id, at, ids, origin); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE play_queues SET version = version + 1, used_at = $1 WHERE id = $2`, tsArg(h.DB, h.now()), id); err != nil {
		return err
	}
	if err := trim(ctx, tx, id, q.CurrentPos); err != nil {
		return err
	}
	return tx.Commit()
}

// reshuffle gives the queue a new seed and replaces what follows the current
// item with the source in the new order, without the videos played so far.
func (h *Handler) reshuffle(ctx context.Context, _ *principal.Principal, q Queue) (*httperror.Error, error) {
	seed := h.seed()
	res, e, err := h.resolve(ctx, q.Source, true, seed)
	if e != nil || err != nil {
		return e, err
	}
	// the videos up to the current one are not played again
	played := map[string]bool{}
	rows, err := h.DB.QueryContext(ctx, `SELECT CAST(video_id AS TEXT) FROM play_queue_items WHERE queue_id = $1 AND pos <= $2`, q.ID, q.CurrentPos)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		played[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	c, e, err := res.src.read(ctx, "", ChunkSize)
	if e != nil || err != nil {
		return e, err
	}
	ids := dedupe(c.ids, played)
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := h.loadQueue(ctx, tx, q.UserID, q.ID, true)
	if err != nil {
		return nil, err
	}
	if locked == nil {
		return httperror.NotFound("play queue " + q.ID), nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM play_queue_items WHERE queue_id = $1 AND pos > $2`, q.ID, locked.CurrentPos); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, q.ID, locked.CurrentPos+1, ids, "source"); err != nil {
		return nil, err
	}
	var cursor any
	if c.cursor != "" {
		cursor = c.cursor
	}
	if _, err := tx.ExecContext(ctx, `UPDATE play_queues SET seed = $1, source_cursor = $2, source_done = $3,
			version = version + 1, used_at = $4 WHERE id = $5`, seed, cursor, c.done, tsArg(h.DB, h.now()), q.ID); err != nil {
		return nil, err
	}
	return nil, tx.Commit()
}

// checkVisible is 404 unless every id is a video (not deleted) of a library the
// caller may read.
func (h *Handler) checkVisible(ctx context.Context, p *principal.Principal, ids []string) (*httperror.Error, error) {
	args := []any{}
	ph := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		ph[i] = fmt.Sprintf("$%d", len(args))
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT CAST(id AS TEXT), CAST(library_id AS TEXT) FROM videos
		WHERE id IN (`+strings.Join(ph, ",")+`) AND deleted_at IS NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ok := map[string]bool{}
	for rows.Next() {
		var id, lib string
		if err := rows.Scan(&id, &lib); err != nil {
			return nil, err
		}
		if p.AccessAllLibraries || p.HasLibrary(lib) {
			ok[id] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !ok[id] {
			return httperror.NotFound("video " + id), nil
		}
	}
	return nil, nil
}

// Item is one queue item in a window: its pos, how it got there, and the
// video's card fields with the caller's playback.
type Item struct {
	Pos         int      `json:"pos"`
	Origin      string   `json:"origin"`
	ID          string   `json:"id"`
	Title       *string  `json:"title"`
	Filename    string   `json:"filename"`
	LibraryID   string   `json:"library_id"`
	DurationSec *float64 `json:"duration_sec"`
	Poster      string   `json:"poster"`
	PositionSec *float64 `json:"position_sec,omitempty"`
	Completed   *bool    `json:"completed,omitempty"`
	SeriesID    *string  `json:"series_id,omitempty"`
	SeriesName  *string  `json:"series_name,omitempty"`
}

// Doc is a queue as the API answers it.
type Doc struct {
	ID            string    `json:"id"`
	Source        SourceRef `json:"source"`
	Shuffle       bool      `json:"shuffle"`
	CurrentPos    int       `json:"current_pos"`
	Version       int       `json:"version"`
	DeviceID      string    `json:"device_id,omitempty"`
	SourceDone    bool      `json:"source_done"`
	TotalEstimate int       `json:"total_estimate"`
	CreatedAt     time.Time `json:"created_at"`
	UsedAt        time.Time `json:"used_at"`
	Items         []Item    `json:"items"`
}

const windowColumns = `i.pos, i.origin, CAST(v.id AS TEXT), v.title, v.filename, CAST(v.library_id AS TEXT), v.duration_sec,
	ps.position_sec, ps.completed,
	(SELECT CAST(se.series_id AS TEXT) FROM series_episodes se WHERE se.video_id = v.id ORDER BY se.series_id LIMIT 1),
	(SELECT s.name FROM series_episodes se JOIN series s ON s.id = se.series_id WHERE se.video_id = v.id ORDER BY se.series_id LIMIT 1)`

// queueDoc is q with the visible items around pos (before of them before it,
// after after it, pos's own when visible).
func (h *Handler) queueDoc(ctx context.Context, p *principal.Principal, q Queue, around, before, after int) (Doc, error) {
	doc := Doc{ID: q.ID, Source: q.Source, Shuffle: q.Shuffle, CurrentPos: q.CurrentPos, Version: q.Version,
		DeviceID: q.DeviceID, SourceDone: q.Done, CreatedAt: q.CreatedAt, UsedAt: q.UsedAt, Items: []Item{}}
	scope, sargs := "", []any{}
	if !p.AccessAllLibraries {
		if len(p.Libraries) == 0 {
			return doc, nil
		}
		ph := make([]string, len(p.Libraries))
		for i, l := range p.Libraries {
			sargs = append(sargs, l)
			ph[i] = fmt.Sprintf("$%d", 4+i)
		}
		scope = " AND v.library_id IN (" + strings.Join(ph, ",") + ")"
	}
	from := ` FROM play_queue_items i JOIN videos v ON v.id = i.video_id AND v.deleted_at IS NULL
		LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $2
		WHERE i.queue_id = $1` + scope
	read := func(cond, order string, n int) ([]Item, error) {
		args := append([]any{q.ID, q.UserID, around}, sargs...)
		args = append(args, n)
		rows, err := h.DB.QueryContext(ctx, `SELECT `+windowColumns+from+` AND `+cond+` ORDER BY `+order+
			fmt.Sprintf(" LIMIT $%d", len(args)), args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Item
		for rows.Next() {
			var it Item
			var pos sql.NullFloat64
			var done sql.NullBool
			var sid, sname sql.NullString
			if err := rows.Scan(&it.Pos, &it.Origin, &it.ID, &it.Title, &it.Filename, &it.LibraryID, &it.DurationSec,
				&pos, &done, &sid, &sname); err != nil {
				return nil, err
			}
			it.Poster = "/api/videos/" + url.PathEscape(it.ID) + "/poster"
			if pos.Valid {
				it.PositionSec = &pos.Float64
			}
			if done.Valid {
				it.Completed = &done.Bool
			}
			if sid.Valid {
				it.SeriesID = &sid.String
			}
			if sname.Valid {
				it.SeriesName = &sname.String
			}
			out = append(out, it)
		}
		return out, rows.Err()
	}
	var head []Item
	var err error
	if before > 0 {
		if head, err = read("i.pos < $3", "i.pos DESC", before); err != nil {
			return doc, err
		}
	}
	tail, err := read("i.pos >= $3", "i.pos", after+1)
	if err != nil {
		return doc, err
	}
	for i := len(head) - 1; i >= 0; i-- {
		doc.Items = append(doc.Items, head[i])
	}
	doc.Items = append(doc.Items, tail...)
	if err := h.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM play_queue_items WHERE queue_id = $1`, q.ID).Scan(&doc.TotalEstimate); err != nil {
		return doc, err
	}
	return doc, nil
}
