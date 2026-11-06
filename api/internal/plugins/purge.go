package plugins

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lib/pq"

	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Purge deletes what a plugin stored and reverts what it wrote that no one changed since
// (story 54.07 §7): its plugin_data, its artifacts (subtitles and artwork files under
// <data>/plugins/artifacts/<id>), its suggestions, and each attributed write whose target
// still holds the value the plugin left (someone else's later change wins). Its settings
// are the components service's reset (the same route calls both). It answers how many
// writes it reverted.
func (l *Lifecycle) Purge(ctx context.Context, cid string, actor svc.Actor) (int, error) {
	id := strings.TrimPrefix(cid, components.PluginPrefix)
	reverted, err := l.Revert(ctx, id, nil, actor)
	if err != nil {
		return reverted, err
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return reverted, err
	}
	defer func() { _ = tx.Rollback() }()
	var data int64
	res, err := tx.ExecContext(ctx, `DELETE FROM plugin_data WHERE plugin_id = $1`, id)
	if err != nil {
		return reverted, err
	}
	data, _ = res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_suggestions WHERE plugin_id = $1 AND status = 'open'`, id); err != nil {
		return reverted, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_health WHERE plugin_id = $1`, id); err != nil {
		return reverted, err
	}
	if err := audit(ctx, tx, "plugin.purge", cid, actor, nil, nil,
		map[string]any{"data_rows": data, "writes_reverted": reverted}); err != nil {
		return reverted, err
	}
	if err := tx.Commit(); err != nil {
		return reverted, err
	}
	if l.ArtifactsDir != "" && pluginIDOK(id) {
		_ = os.RemoveAll(filepath.Join(l.ArtifactsDir, id))
	}
	return reverted, nil
}

// pluginIDOK: an id that is one path element (a manifest id always is).
func pluginIDOK(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "..") && !strings.HasPrefix(id, ".")
}

// Write is one plugin_writes row as GET /api/plugins/{id}/writes shows it.
type Write struct {
	ID         int64           `json:"id"`
	Version    string          `json:"version"`
	JobID      *int64          `json:"job_id,omitempty"`
	Target     string          `json:"target"`
	SubjectID  string          `json:"subject_id"`
	Field      string          `json:"field"`
	Before     json.RawMessage `json:"before"`
	After      json.RawMessage `json:"after"`
	At         time.Time       `json:"at"`
	RevertedAt *time.Time      `json:"reverted_at,omitempty"`
}

// Writes are a plugin's writes, newest first, below beforeID (0: from the newest).
func (l *Lifecycle) Writes(ctx context.Context, id string, beforeID int64, limit int) ([]Write, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if beforeID <= 0 {
		beforeID = 1<<62 - 1
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT id, version, job_id, target, CAST(subject_id AS text), field,
		COALESCE(before, 'null'::jsonb), COALESCE(after, 'null'::jsonb), at, reverted_at
		FROM plugin_writes WHERE plugin_id = $1 AND id < $2 ORDER BY id DESC LIMIT $3`, id, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Write{}
	for rows.Next() {
		var w Write
		var job sql.NullInt64
		var rev sql.NullTime
		var b, a []byte
		if err := rows.Scan(&w.ID, &w.Version, &job, &w.Target, &w.SubjectID, &w.Field, &b, &a, &w.At, &rev); err != nil {
			return nil, err
		}
		w.Before, w.After = b, a
		if job.Valid {
			w.JobID = &job.Int64
		}
		if rev.Valid {
			w.RevertedAt = &rev.Time
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Revert undoes a plugin's writes (ids nil: all of them), newest first, each only where
// its target still holds the value the plugin left: a later change by anyone else wins.
// It answers how many it reverted; each is marked reverted_at, and one audit row says so.
func (l *Lifecycle) Revert(ctx context.Context, id string, ids []int64, actor svc.Actor) (int, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := `SELECT id, target, CAST(subject_id AS text), field, before, after FROM plugin_writes
		WHERE plugin_id = $1 AND reverted_at IS NULL`
	args := []any{id}
	if ids != nil {
		q += ` AND id = ANY($2)`
		args = append(args, pq.Array(ids))
	}
	rows, err := tx.QueryContext(ctx, q+` ORDER BY id DESC`, args...)
	if err != nil {
		return 0, err
	}
	type row struct {
		id                   int64
		target, subject, fld string
		before, after        []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.target, &r.subject, &r.fld, &r.before, &r.after); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	n := 0
	var reverted []int64
	for _, r := range all {
		did, err := revertOne(ctx, tx, id, r.target, r.subject, r.fld, r.before, r.after)
		if err != nil {
			return 0, err
		}
		if did {
			n++
			reverted = append(reverted, r.id)
		}
	}
	if len(reverted) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_writes SET reverted_at = now() WHERE id = ANY($1)`, pq.Array(reverted)); err != nil {
			return 0, err
		}
		if err := audit(ctx, tx, "plugin.revert", components.PluginPrefix+id, actor, nil, nil,
			map[string]any{"writes": reverted}); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit()
}

func writtenText(raw []byte) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// revertOne undoes one write if its target still holds the plugin's value.
func revertOne(ctx context.Context, tx *sql.Tx, pluginID, target, subject, field string, before, after []byte) (bool, error) {
	afterS, _ := writtenText(after)
	beforeS, hadBefore := writtenText(before)
	var nullable any
	if hadBefore {
		nullable = beforeS
	}
	switch target {
	case "videos":
		switch field {
		case "description":
			res, err := tx.ExecContext(ctx, `UPDATE videos SET description = $3, updated_at = now()
				WHERE id = $1 AND description IS NOT DISTINCT FROM $2`, subject, afterS, nullable)
			return affected(res, err)
		case "poster_path":
			res, err := tx.ExecContext(ctx, `UPDATE videos SET poster_path = $3, updated_at = now()
				WHERE id = $1 AND poster_path IS NOT DISTINCT FROM $2`, subject, afterS, nullable)
			return affected(res, err)
		default:
			path := pq.Array(strings.Split(field, "."))
			var res sql.Result
			var err error
			if hadBefore {
				res, err = tx.ExecContext(ctx, `UPDATE videos SET metadata = jsonb_set(metadata, $2, to_jsonb($4::text)),
					updated_at = now() WHERE id = $1 AND metadata #>> $2 = $3`, subject, path, afterS, beforeS)
			} else {
				res, err = tx.ExecContext(ctx, `UPDATE videos SET metadata = metadata #- $2, updated_at = now()
					WHERE id = $1 AND metadata #>> $2 = $3`, subject, path, afterS)
			}
			return affected(res, err)
		}
	case "video_tags":
		res, err := tx.ExecContext(ctx, `DELETE FROM video_tags WHERE video_id = $1
			AND tag_id = (SELECT id FROM tags WHERE name_norm = $2)`, subject, field)
		return affected(res, err)
	case "chapters":
		// the plugin's chapters, if they are still the ones this write set, give way to
		// the ones before it
		var now json.RawMessage
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(json_agg(json_build_object('start_ms', CAST(round(start_sec * 1000) AS bigint),
			'title', title) ORDER BY seq), '[]') FROM chapters WHERE video_id = $1 AND source = 'plugin'`, subject).Scan(&now); err != nil {
			return false, err
		}
		if !sameChapters(now, after) {
			return false, nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE video_id = $1 AND source = 'plugin'`, subject); err != nil {
			return false, err
		}
		var prev []struct {
			StartSec float64 `json:"start_sec"`
			Title    string  `json:"title"`
		}
		_ = json.Unmarshal(before, &prev)
		for i, ch := range prev {
			if _, err := tx.ExecContext(ctx, `INSERT INTO chapters (video_id, seq, start_sec, title, source, metadata)
				VALUES ($1, $2, $3, $4, 'plugin', jsonb_build_object('plugin', $5::text))`, subject, i, ch.StartSec, ch.Title, pluginID); err != nil {
				return false, err
			}
		}
		return true, nil
	case "subtitle_files":
		var path string
		err := tx.QueryRowContext(ctx, `UPDATE subtitle_files SET deleted_at = now() WHERE video_id = $1 AND path = $2
			AND deleted_at IS NULL RETURNING path`, subject, afterS).Scan(&path)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		_ = os.Remove(path)
		return true, nil
	}
	return false, nil
}

func affected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// sameChapters compares the chapters now ([{start_ms, title}]) with a write's after.
func sameChapters(now, after []byte) bool {
	type ch struct {
		StartMs int64  `json:"start_ms"`
		Title   string `json:"title"`
	}
	var a, b []ch
	if json.Unmarshal(now, &a) != nil || json.Unmarshal(after, &b) != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
