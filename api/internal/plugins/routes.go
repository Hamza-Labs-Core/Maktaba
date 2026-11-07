package plugins

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/tags"
	"github.com/Hamza-Labs-Core/Maktaba/shared/plugins/pluginpanel"
)

// Size limits (README §4.7).
const (
	MaxValue    = 64 << 10
	MaxArtifact = 4 << 20
	MaxRequests = 1000
	MaxNotices  = 10
	pageSize    = 100
)

type subjectRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// videoLibrary answers a video's library, or not_found.
func (c *call) videoLibrary(videoID string) (string, *Refusal) {
	if !uuidRe.MatchString(videoID) {
		return "", refuse(400, CodeInvalid, "a video id is a UUID")
	}
	var lib string
	err := c.s.DB.QueryRowContext(c.ctx, `SELECT CAST(library_id AS text) FROM videos WHERE id = $1 AND deleted_at IS NULL`, videoID).Scan(&lib)
	if errors.Is(err, sql.ErrNoRows) {
		return "", refuse(404, CodeNotFound, "no such video")
	}
	if err != nil {
		return "", refuse(503, CodeMediaGone, err.Error())
	}
	return lib, nil
}

// readScope: a read of a video in a library the admin granted (54.04 §3).
func (c *call) readScope(videoID string) *Refusal {
	lib, ref := c.videoLibrary(videoID)
	if ref != nil {
		return ref
	}
	if !c.inv.LibraryAllowed(lib) {
		return refuse(403, CodeScope, "the video's library is not granted")
	}
	return nil
}

// writeScope: a write (or a media read) acts on the job's subject only; a library,
// season, series or catalog job on items inside the granted libraries (54.04 §3).
func (c *call) writeScope(videoID string) *Refusal {
	if ref := c.readScope(videoID); ref != nil {
		return ref
	}
	if c.inv.SubjectKind == "video" && videoID != c.inv.SubjectID {
		return refuse(403, CodeScope, "a video job acts on its own video only")
	}
	return nil
}

func (c *call) attribute(ctx context.Context, tx *sql.Tx, target, subject, field string, before, after any) error {
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	_, err := tx.ExecContext(ctx, `INSERT INTO plugin_writes (plugin_id, version, job_id, target, subject_id, field, before, after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, c.inv.PluginID, c.inv.Version, c.inv.JobID, target, subject, field, b, a)
	return err
}

func dbErr(err error) *Refusal { return refuse(503, CodeMediaGone, err.Error()) }

// ---- data: the plugin's own namespace (54.04 §4) ----------------------------------

func (s *Server) dataRoutes() {
	key := func(c *call) (subjectRef, string, *Refusal) {
		var in struct {
			Subject subjectRef `json:"subject"`
			Key     string     `json:"key"`
		}
		if ref := c.decode(&in); ref != nil {
			return in.Subject, "", ref
		}
		if in.Key == "" || len(in.Key) > 256 || in.Subject.Kind == "" {
			return in.Subject, "", refuse(400, CodeInvalid, "a subject and a key (<= 256 bytes)")
		}
		return in.Subject, in.Key, nil
	}
	s.routes["data/get"] = func(c *call) (any, *Refusal) {
		sub, k, ref := key(c)
		if ref != nil {
			return nil, ref
		}
		var v []byte
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT value FROM plugin_data WHERE plugin_id=$1 AND subject_kind=$2 AND subject_id=$3 AND key=$4`,
			c.inv.PluginID, sub.Kind, sub.ID, k).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{"value": nil}, nil
		}
		if err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{"value": string(v)}, nil
	}
	s.routes["data/put"] = func(c *call) (any, *Refusal) {
		var in struct {
			Subject  subjectRef `json:"subject"`
			Key      string     `json:"key"`
			Value    string     `json:"value"`
			Exported bool       `json:"exported"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if in.Key == "" || len(in.Key) > 256 {
			return nil, refuse(400, CodeInvalid, "a key of <= 256 bytes")
		}
		if len(in.Value) > MaxValue {
			return nil, refuse(413, CodeTooLarge, "a value is at most 64 KB")
		}
		if !json.Valid([]byte(in.Value)) {
			return nil, refuse(400, CodeInvalid, "a value is JSON text")
		}
		// a key a panel shows holds plain text of its block's shape (54.20 §2)
		if ref := c.checkPanelValue(in.Subject.Kind, in.Key, in.Value); ref != nil {
			return nil, ref
		}
		var used int64
		if err := c.s.DB.QueryRowContext(c.ctx, `SELECT COALESCE(SUM(bytes),0) FROM plugin_data WHERE plugin_id=$1
			AND NOT (subject_kind=$2 AND subject_id=$3 AND key=$4)`, c.inv.PluginID, in.Subject.Kind, in.Subject.ID, in.Key).Scan(&used); err != nil {
			return nil, dbErr(err)
		}
		if c.inv.DataBytes >= 0 && used+int64(len(in.Value)) > c.inv.DataBytes {
			return nil, refuse(403, CodeQuota, "the plugin's data quota is used up")
		}
		if _, err := c.s.DB.ExecContext(c.ctx, `INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, exported, bytes, updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,now()) ON CONFLICT (plugin_id, subject_kind, subject_id, key)
			DO UPDATE SET value = EXCLUDED.value, exported = EXCLUDED.exported, bytes = EXCLUDED.bytes, updated_at = now()`,
			c.inv.PluginID, in.Subject.Kind, in.Subject.ID, in.Key, in.Value, in.Exported, len(in.Value)); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["data/delete"] = func(c *call) (any, *Refusal) {
		sub, k, ref := key(c)
		if ref != nil {
			return nil, ref
		}
		if _, err := c.s.DB.ExecContext(c.ctx, `DELETE FROM plugin_data WHERE plugin_id=$1 AND subject_kind=$2 AND subject_id=$3 AND key=$4`,
			c.inv.PluginID, sub.Kind, sub.ID, k); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["data/list"] = func(c *call) (any, *Refusal) {
		var in struct {
			Subject subjectRef `json:"subject"`
			Prefix  string     `json:"prefix"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		rows, err := c.s.DB.QueryContext(c.ctx, `SELECT key FROM plugin_data WHERE plugin_id=$1 AND subject_kind=$2 AND subject_id=$3
			AND starts_with(key, $4) ORDER BY key LIMIT 1000`, c.inv.PluginID, in.Subject.Kind, in.Subject.ID, in.Prefix)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = rows.Close() }()
		keys := []string{}
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				return nil, dbErr(err)
			}
			keys = append(keys, k)
		}
		return map[string]any{"keys": keys}, nil
	}
}

// ---- library and transcript reads ---------------------------------------------------

func (s *Server) libraryRoutes() {
	s.routes["library/video"] = func(c *call) (any, *Refusal) {
		var in struct {
			ID string `json:"id"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if ref := c.readScope(in.ID); ref != nil {
			return nil, ref
		}
		var v struct {
			ID          string   `json:"id"`
			LibraryID   string   `json:"library_id"`
			Title       *string  `json:"title"`
			Display     *string  `json:"display_title"`
			Description *string  `json:"description"`
			Duration    *float64 `json:"duration_sec"`
			Language    *string  `json:"detected_language"`
			Published   *string  `json:"published_at"`
			Tags        []string `json:"tags"`
		}
		var tagNames pq.StringArray
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT CAST(v.id AS text), CAST(v.library_id AS text), v.title, v.display_title,
			v.description, v.duration_sec, v.detected_language, to_char(v.published_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
			COALESCE((SELECT array_agg(t.name ORDER BY t.name) FROM video_tags vt JOIN tags t ON t.id = vt.tag_id WHERE vt.video_id = v.id), '{}')
			FROM videos v WHERE v.id = $1`, in.ID).Scan(&v.ID, &v.LibraryID, &v.Title, &v.Display, &v.Description, &v.Duration,
			&v.Language, &v.Published, &tagNames)
		if err != nil {
			return nil, dbErr(err)
		}
		v.Tags = tagNames
		return v, nil
	}
	s.routes["library/series"] = func(c *call) (any, *Refusal) {
		var in struct {
			ID string `json:"id"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if !uuidRe.MatchString(in.ID) {
			return nil, refuse(400, CodeInvalid, "a series id is a UUID")
		}
		var out struct {
			ID        string  `json:"id"`
			LibraryID *string `json:"library_id"`
			Name      string  `json:"name"`
			Year      *int    `json:"year"`
		}
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT CAST(id AS text), CAST(library_id AS text), COALESCE(name_override, name), year
			FROM series WHERE id = $1`, in.ID).Scan(&out.ID, &out.LibraryID, &out.Name, &out.Year)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, refuse(404, CodeNotFound, "no such series")
		}
		if err != nil {
			return nil, dbErr(err)
		}
		if out.LibraryID == nil || !c.inv.LibraryAllowed(*out.LibraryID) {
			return nil, refuse(403, CodeScope, "the series' library is not granted")
		}
		return out, nil
	}
	s.routes["library/list"] = func(c *call) (any, *Refusal) {
		var in struct {
			Library string `json:"library"`
			Page    int    `json:"page"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if !c.inv.LibraryAllowed(in.Library) {
			return nil, refuse(403, CodeScope, "the library is not granted")
		}
		if in.Page < 0 {
			in.Page = 0
		}
		rows, err := c.s.DB.QueryContext(c.ctx, `SELECT CAST(id AS text), COALESCE(display_title, title, '') FROM videos
			WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id LIMIT $2 OFFSET $3`, in.Library, pageSize+1, in.Page*pageSize)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = rows.Close() }()
		type item struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		items := []item{}
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.ID, &it.Title); err != nil {
				return nil, dbErr(err)
			}
			items = append(items, it)
		}
		more := len(items) > pageSize
		if more {
			items = items[:pageSize]
		}
		return map[string]any{"items": items, "more": more}, nil
	}
	transcript := func(words bool) route {
		return func(c *call) (any, *Refusal) {
			var in struct {
				Video string `json:"video"`
			}
			if ref := c.decode(&in); ref != nil {
				return nil, ref
			}
			if ref := c.readScope(in.Video); ref != nil {
				return nil, ref
			}
			q := `SELECT s.seq, s.start_sec, s.end_sec, s.text, COALESCE(s.speaker, '') FROM transcript_segments s
				JOIN transcripts t ON t.id = s.transcript_id WHERE t.video_id = $1 AND t.is_active ORDER BY s.seq LIMIT 20000`
			if words {
				q = `SELECT w.seq, w.start_sec, w.end_sec, w.text, CAST(s.seq AS text) FROM transcript_words w
					JOIN transcript_segments s ON s.id = w.segment_id JOIN transcripts t ON t.id = s.transcript_id
					WHERE t.video_id = $1 AND t.is_active ORDER BY s.seq, w.seq LIMIT 200000`
			}
			rows, err := c.s.DB.QueryContext(c.ctx, q, in.Video)
			if err != nil {
				return nil, dbErr(err)
			}
			defer func() { _ = rows.Close() }()
			out := []map[string]any{}
			for rows.Next() {
				var seq int
				var start, end float64
				var text, extra string
				if err := rows.Scan(&seq, &start, &end, &text, &extra); err != nil {
					return nil, dbErr(err)
				}
				m := map[string]any{"seq": seq, "start": start, "end": end, "text": text}
				if words {
					m["segment"] = extra
				} else if extra != "" {
					m["speaker"] = extra
				}
				out = append(out, m)
			}
			key := "segments"
			if words {
				key = "words"
			}
			return map[string]any{key: out}, nil
		}
	}
	s.routes["transcript/segments"] = transcript(false)
	s.routes["transcript/words"] = transcript(true)
}

// ---- writes (54.04 §4) --------------------------------------------------------------

// FieldAllowed answers whether set-field may write a field (54.04 §4): description,
// sort_title, year, content_rating, original_language, and local_ids.<this plugin's
// short id>. Never title (a plugin suggests a title instead), state, paths, libraries,
// users or ACLs.
func FieldAllowed(pluginID, field string) bool {
	switch field {
	case "description", "sort_title", "year", "content_rating", "original_language":
		return true
	}
	return field == "local_ids."+ShortID(pluginID)
}

// ShortID is a plugin's 8 hex short id (the hash in its stage names).
func ShortID(pluginID string) string { return QueueName(pluginID, "")[2:10] }

func (s *Server) writeRoutes() {
	s.routes["write/set-field"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string `json:"video"`
			Field string `json:"field"`
			Value string `json:"value"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if !FieldAllowed(c.inv.PluginID, in.Field) {
			return nil, refuse(403, CodeField, in.Field)
		}
		if len(in.Value) > MaxValue || !utf8.ValidString(in.Value) {
			return nil, refuse(413, CodeTooLarge, "a value is at most 64 KB of UTF-8")
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		tx, err := c.s.DB.BeginTx(c.ctx, nil)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = tx.Rollback() }()
		var before *string
		if in.Field == "description" {
			if err := tx.QueryRowContext(c.ctx, `SELECT description FROM videos WHERE id = $1 FOR UPDATE`, in.Video).Scan(&before); err != nil {
				return nil, dbErr(err)
			}
			_, err = tx.ExecContext(c.ctx, `UPDATE videos SET description = $2, updated_at = now() WHERE id = $1`, in.Video, in.Value)
		} else {
			path := pq.Array(strings.Split(in.Field, "."))
			if err := tx.QueryRowContext(c.ctx, `SELECT metadata #>> $2 FROM videos WHERE id = $1 FOR UPDATE`, in.Video, path).Scan(&before); err != nil {
				return nil, dbErr(err)
			}
			if strings.HasPrefix(in.Field, "local_ids.") {
				_, err = tx.ExecContext(c.ctx, `UPDATE videos SET metadata = jsonb_set(jsonb_set(metadata, '{local_ids}',
					COALESCE(metadata->'local_ids', '{}'::jsonb)), $2, to_jsonb($3::text)), updated_at = now() WHERE id = $1`, in.Video, path, in.Value)
			} else {
				_, err = tx.ExecContext(c.ctx, `UPDATE videos SET metadata = jsonb_set(metadata, $2, to_jsonb($3::text)), updated_at = now()
					WHERE id = $1`, in.Video, path, in.Value)
			}
		}
		if err != nil {
			return nil, dbErr(err)
		}
		if err := c.attribute(c.ctx, tx, "videos", in.Video, in.Field, before, in.Value); err != nil {
			return nil, dbErr(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["write/add-tags"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string   `json:"video"`
			Tags  []string `json:"tags"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Tags) > 100 {
			return nil, refuse(413, CodeTooLarge, "at most 100 tags a call")
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		tx, err := c.s.DB.BeginTx(c.ctx, nil)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = tx.Rollback() }()
		for _, name := range in.Tags {
			name = strings.TrimSpace(name)
			norm := tags.NormaliseTagName(name)
			if norm == "" || utf8.RuneCountInString(name) > 100 {
				continue
			}
			if _, err := tx.ExecContext(c.ctx, `INSERT INTO tags (name, name_norm) VALUES ($1, $2) ON CONFLICT (name_norm) DO NOTHING`, name, norm); err != nil {
				return nil, dbErr(err)
			}
			res, err := tx.ExecContext(c.ctx, `INSERT INTO video_tags (video_id, tag_id) SELECT $1, id FROM tags WHERE name_norm = $2
				ON CONFLICT DO NOTHING`, in.Video, norm)
			if err != nil {
				return nil, dbErr(err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				if err := c.attribute(c.ctx, tx, "video_tags", in.Video, norm, nil, name); err != nil {
					return nil, dbErr(err)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["write/set-chapters"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video    string `json:"video"`
			Chapters []struct {
				StartMs int64  `json:"start_ms"`
				Title   string `json:"title"`
			} `json:"chapters"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Chapters) > 500 {
			return nil, refuse(413, CodeTooLarge, "at most 500 chapters")
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		tx, err := c.s.DB.BeginTx(c.ctx, nil)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = tx.Rollback() }()
		var before json.RawMessage
		if err := tx.QueryRowContext(c.ctx, `SELECT COALESCE(json_agg(json_build_object('start_sec', start_sec, 'title', title) ORDER BY seq), '[]')
			FROM chapters WHERE video_id = $1 AND source = 'plugin'`, in.Video).Scan(&before); err != nil {
			return nil, dbErr(err)
		}
		var others int
		if err := tx.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM chapters WHERE video_id = $1 AND source <> 'plugin'`, in.Video).Scan(&others); err != nil {
			return nil, dbErr(err)
		}
		if others > 0 {
			// chapters from the file or an admin win: a plugin's only fill an empty list
			return nil, refuse(403, CodeField, "the video has chapters already")
		}
		if _, err := tx.ExecContext(c.ctx, `DELETE FROM chapters WHERE video_id = $1 AND source = 'plugin'`, in.Video); err != nil {
			return nil, dbErr(err)
		}
		for i, ch := range in.Chapters {
			title := strings.TrimSpace(ch.Title)
			if ch.StartMs < 0 || title == "" || utf8.RuneCountInString(title) > 200 {
				return nil, refuse(400, CodeInvalid, fmt.Sprintf("chapter %d", i))
			}
			if _, err := tx.ExecContext(c.ctx, `INSERT INTO chapters (video_id, seq, start_sec, title, source, metadata)
				VALUES ($1,$2,$3,$4,'plugin', jsonb_build_object('plugin', $5::text))`, in.Video, i, float64(ch.StartMs)/1000, title, c.inv.PluginID); err != nil {
				return nil, dbErr(err)
			}
		}
		if err := c.attribute(c.ctx, tx, "chapters", in.Video, "", before, in.Chapters); err != nil {
			return nil, dbErr(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["write/add-subtitle"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string `json:"video"`
			Lang  string `json:"lang"`
			Text  string `json:"text"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Text) > MaxArtifact {
			return nil, refuse(413, CodeTooLarge, "a subtitle is at most 4 MB")
		}
		if !langRe.MatchString(in.Lang) {
			return nil, refuse(400, CodeInvalid, "lang is a BCP 47 code")
		}
		vtt, err := Subtitle(in.Text)
		if err != nil {
			return nil, refuse(400, CodeInvalid, err.Error())
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		path, ref := c.s.writeArtifact(c.inv, in.Video, "sub-"+in.Lang+".vtt", []byte(vtt))
		if ref != nil {
			return nil, ref
		}
		tx, err := c.s.DB.BeginTx(c.ctx, nil)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(c.ctx, `UPDATE subtitle_files SET deleted_at = now() WHERE video_id = $1 AND language = $2
			AND metadata->>'plugin' = $3 AND deleted_at IS NULL`, in.Video, in.Lang, c.inv.PluginID); err != nil {
			return nil, dbErr(err)
		}
		if _, err := tx.ExecContext(c.ctx, `INSERT INTO subtitle_files (video_id, language, format, source, path, byte_size, metadata)
			VALUES ($1,$2,'vtt','generated',$3,$4, jsonb_build_object('plugin', $5::text, 'origin', 'plugin:' || $5::text || '@' || $6::text))`,
			in.Video, in.Lang, path, len(vtt), c.inv.PluginID, c.inv.Version); err != nil {
			return nil, dbErr(err)
		}
		if err := c.attribute(c.ctx, tx, "subtitle_files", in.Video, in.Lang, nil, path); err != nil {
			return nil, dbErr(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["write/set-artwork"] = func(c *call) (any, *Refusal) {
		var in struct {
			Subject subjectRef `json:"subject"`
			Kind    string     `json:"kind"`
			Image   string     `json:"image"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		img, err := base64.StdEncoding.DecodeString(in.Image)
		if err != nil {
			return nil, refuse(400, CodeInvalid, "image is base64")
		}
		if len(img) > MaxArtifact {
			return nil, refuse(413, CodeTooLarge, "an image is at most 4 MB")
		}
		ext := imageExt(img)
		if ext == "" {
			return nil, refuse(400, CodeInvalid, "a PNG or JPEG")
		}
		if in.Subject.Kind != "video" || (in.Kind != "poster" && in.Kind != "panel") {
			return nil, refuse(400, CodeInvalid, "a video's poster, or its panel image")
		}
		if ref := c.writeScope(in.Subject.ID); ref != nil {
			return nil, ref
		}
		// a panel image (54.20 §2): at most 1 MB, a file of the plugin's own, no core field
		if in.Kind == "panel" {
			if len(img) > pluginpanel.MaxImageBytes {
				return nil, refuse(413, CodeTooLarge, "a panel image is at most 1 MB")
			}
			other := map[string]string{".png": "panel.jpg", ".jpg": "panel.png"}[ext]
			if c.s.ArtifactsDir != "" {
				_ = os.Remove(filepath.Join(c.s.ArtifactsDir, c.inv.PluginID, in.Subject.ID, other))
			}
			if _, ref := c.s.writeArtifact(c.inv, in.Subject.ID, "panel"+ext, img); ref != nil {
				return nil, ref
			}
			return map[string]any{}, nil
		}
		path, ref := c.s.writeArtifact(c.inv, in.Subject.ID, "poster"+ext, img)
		if ref != nil {
			return nil, ref
		}
		tx, err := c.s.DB.BeginTx(c.ctx, nil)
		if err != nil {
			return nil, dbErr(err)
		}
		defer func() { _ = tx.Rollback() }()
		var before *string
		if err := tx.QueryRowContext(c.ctx, `SELECT poster_path FROM videos WHERE id = $1 FOR UPDATE`, in.Subject.ID).Scan(&before); err != nil {
			return nil, dbErr(err)
		}
		if _, err := tx.ExecContext(c.ctx, `UPDATE videos SET poster_path = $2, updated_at = now() WHERE id = $1`, in.Subject.ID, path); err != nil {
			return nil, dbErr(err)
		}
		if err := c.attribute(c.ctx, tx, "videos", in.Subject.ID, "poster_path", before, path); err != nil {
			return nil, dbErr(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
}

var langRe = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

func imageExt(b []byte) string {
	switch {
	case len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png"
	case len(b) > 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		return ".jpg"
	}
	return ""
}

// writeArtifact stores a plugin's file under <artifacts>/<plugin>/<video>/ with a name
// Maktaba chooses (README §9 T3: nothing a plugin names reaches the disk).
func (s *Server) writeArtifact(inv *Invocation, video, name string, b []byte) (string, *Refusal) {
	if s.ArtifactsDir == "" {
		return "", refuse(503, CodeMediaGone, "no artifacts folder")
	}
	dir := filepath.Join(s.ArtifactsDir, inv.PluginID, video)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", dbErr(err)
	}
	p := filepath.Join(dir, name)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return "", dbErr(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", dbErr(err)
	}
	return p, nil
}

// ---- suggest, queue, notify, exports, job --------------------------------------------

func (s *Server) otherRoutes() {
	s.routes["suggest/add"] = func(c *call) (any, *Refusal) {
		var in struct {
			Subject subjectRef `json:"subject"`
			Field   string     `json:"field"`
			Value   string     `json:"value"`
			Reason  string     `json:"reason"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if len(in.Value)+len(in.Reason) > MaxValue || in.Field == "" || len(in.Field) > 64 {
			return nil, refuse(413, CodeTooLarge, "a field and at most 64 KB")
		}
		if in.Subject.Kind == "video" {
			if ref := c.writeScope(in.Subject.ID); ref != nil {
				return nil, ref
			}
		}
		if _, err := c.s.DB.ExecContext(c.ctx, `INSERT INTO plugin_suggestions (plugin_id, version, job_id, subject_kind, subject_id, field, value, reason)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, c.inv.PluginID, c.inv.Version, c.inv.JobID, in.Subject.Kind, in.Subject.ID, in.Field, in.Value, in.Reason); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["queue/request"] = func(c *call) (any, *Refusal) {
		var in struct {
			Stage   string     `json:"stage"`
			Subject subjectRef `json:"subject"`
			Payload string     `json:"payload"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		target := QueueName(c.inv.PluginID, in.Stage)
		allowed := false
		for _, m := range c.inv.MayRequest[c.inv.Stage] {
			if m == target {
				allowed = true
			}
		}
		if !allowed {
			return nil, refuse(403, CodeScope, "only this stage's may_request")
		}
		if in.Subject.Kind == "video" {
			if ref := c.readScope(in.Subject.ID); ref != nil {
				return nil, ref
			}
		}
		c.inv.mu.Lock()
		c.inv.requests++
		over := c.inv.requests > MaxRequests
		c.inv.mu.Unlock()
		if over {
			return nil, refuse(403, CodeQuota, "at most 1,000 requests a job")
		}
		return map[string]any{"stage": target}, nil
	}
	s.routes["notify/admin"] = func(c *call) (any, *Refusal) {
		var in struct {
			Code   string `json:"code"`
			Params string `json:"params"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if !codeRe.MatchString(in.Code) {
			return nil, refuse(400, CodeInvalid, "a code is a word")
		}
		if in.Params == "" {
			in.Params = "{}"
		}
		var params map[string]any
		if json.Unmarshal([]byte(in.Params), &params) != nil {
			return nil, refuse(400, CodeInvalid, "params is a JSON object")
		}
		if !c.s.pluginHasString(c.ctx, c.inv.PluginID, "notice."+in.Code) {
			return nil, refuse(400, CodeInvalid, "the code is not in the plugin's strings")
		}
		var today int
		if err := c.s.DB.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM notifications WHERE kind = 'plugin' AND subject = $1
			AND created_at > now() - interval '1 day'`, c.inv.PluginID).Scan(&today); err != nil {
			return nil, dbErr(err)
		}
		if today >= MaxNotices {
			return nil, refuse(403, CodeQuota, "at most 10 notices a day")
		}
		params["plugin"] = c.inv.PluginID
		pb, _ := json.Marshal(params)
		if _, err := c.s.DB.ExecContext(c.ctx, `INSERT INTO notifications (kind, severity, title_key, params, audience, subject)
			VALUES ('plugin', 'info', $1, $2, 'admins', $3)`, "plugin."+c.inv.PluginID+".notice."+in.Code, pb, c.inv.PluginID); err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{}, nil
	}
	s.routes["exports/read"] = func(c *call) (any, *Refusal) {
		var in struct {
			Plugin  string     `json:"plugin"`
			Subject subjectRef `json:"subject"`
			Key     string     `json:"key"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		ok := false
		for _, g := range c.inv.Grants {
			if g == "plugin-data.read:"+in.Plugin {
				ok = true
			}
		}
		if !ok {
			return nil, refuse(403, CodeCapability, "plugin-data.read:"+in.Plugin)
		}
		var v []byte
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT value FROM plugin_data WHERE plugin_id=$1 AND subject_kind=$2 AND subject_id=$3
			AND key=$4 AND exported`, in.Plugin, in.Subject.Kind, in.Subject.ID, in.Key).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]any{"value": nil}, nil
		}
		if err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{"value": string(v)}, nil
	}
	s.routes["job/grants"] = func(c *call) (any, *Refusal) {
		return map[string]any{"grants": nonNil(c.inv.Grants)}, nil
	}
	s.routes["job/settings"] = func(c *call) (any, *Refusal) {
		var v []byte
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT value FROM app_settings WHERE key = $1`, "plugins."+c.inv.PluginID).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) || len(v) == 0 {
			return map[string]any{"settings": "{}"}, nil
		}
		if err != nil {
			return nil, dbErr(err)
		}
		return map[string]any{"settings": string(v)}, nil
	}
	s.routes["log/write"] = func(_ *call) (any, *Refusal) {
		// the host writes plugin lines itself (54.09 §3); this route only accepts them
		return map[string]any{}, nil
	}
}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// pluginHasString reports whether the installed plugin's English strings hold key.
func (s *Server) pluginHasString(ctx context.Context, pluginID, key string) bool {
	var path string
	if err := s.DB.QueryRowContext(ctx, `SELECT path FROM plugin_installs WHERE plugin_id = $1`, pluginID).Scan(&path); err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(path, "i18n", "en.json"))
	if err != nil {
		return false
	}
	var words map[string]any
	if json.Unmarshal(b, &words) != nil {
		return false
	}
	_, ok := words[key].(string)
	return ok
}
