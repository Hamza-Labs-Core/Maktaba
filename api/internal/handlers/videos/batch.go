package videos

// Batch readers for a video's joined data: tags, active transcript,
// per-user playback state, subtitles, chapters, probe output (media_info)
// and audio tracks. The REST detail / subtitles / chapters handlers call
// them with one id; the GraphQL layer calls them once per list of videos
// (its DataLoader), so the SQL lives here exactly once. Callers check
// access to the parent video before asking for its rows.

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// videoColumns is the column list scanVideoRow reads.
const videoColumns = `id, library_id, path, filename, title, description, state,
       detected_language, duration_sec, size_bytes, content_hash,
       metadata, created_at, updated_at`

// batchSize caps the ids bound into one IN (...) list.
const batchSize = 500

// forChunks runs fn over ids in IN-list sized chunks, with the
// placeholders ($start..) already rendered.
func forChunks(ids []string, start int, fn func(placeholders string, args []any) error) error {
	for lo := 0; lo < len(ids); lo += batchSize {
		hi := lo + batchSize
		if hi > len(ids) {
			hi = len(ids)
		}
		ph := make([]string, 0, hi-lo)
		args := make([]any, 0, hi-lo)
		for i, id := range ids[lo:hi] {
			ph = append(ph, pidx(start+i))
			args = append(args, id)
		}
		if err := fn(strings.Join(ph, ","), args); err != nil {
			return err
		}
	}
	return nil
}

// uniq drops duplicate and empty ids, keeping the first order.
func uniq(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// LoadVideos reads the video rows for ids (missing ids are absent from
// the map). Access is the caller's to check (CanRead).
func LoadVideos(ctx context.Context, db *sql.DB, ids []string) (map[string]Video, error) {
	out := map[string]Video{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `SELECT `+videoColumns+` FROM videos WHERE id IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := scanVideoRow(rows)
			if err != nil {
				return err
			}
			out[v.ID] = v
		}
		return rows.Err()
	})
	return out, err
}

// TagRef is one tag on a video.
type TagRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TagsByVideo returns each video's tags ordered by name.
func TagsByVideo(ctx context.Context, db *sql.DB, ids []string) (map[string][]TagRef, error) {
	out := map[string][]TagRef{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT vt.video_id, t.id, t.name FROM tags t
			JOIN video_tags vt ON vt.tag_id = t.id
			WHERE vt.video_id IN (`+ph+`) ORDER BY t.name`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var t TagRef
			if err := rows.Scan(&vid, &t.ID, &t.Name); err != nil {
				return err
			}
			out[vid] = append(out[vid], t)
		}
		return rows.Err()
	})
	return out, err
}

// TranscriptRef is a video's latest active transcript.
type TranscriptRef struct {
	ID        string
	Language  *string
	CreatedAt time.Time
}

// ActiveTranscripts returns each video's newest active (non-superseded)
// transcript.
func ActiveTranscripts(ctx context.Context, db *sql.DB, ids []string) (map[string]TranscriptRef, error) {
	out := map[string]TranscriptRef{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, id, language, created_at FROM transcripts
			WHERE video_id IN (`+ph+`) AND is_active = TRUE
			ORDER BY video_id, created_at DESC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var t TranscriptRef
			var lang sql.NullString
			if err := rows.Scan(&vid, &t.ID, &lang, &t.CreatedAt); err != nil {
				return err
			}
			if _, seen := out[vid]; seen {
				continue // rows are newest first per video
			}
			if lang.Valid {
				l := lang.String
				t.Language = &l
			}
			out[vid] = t
		}
		return rows.Err()
	})
	return out, err
}

// PlaybackStates returns userID's playback state for each video watched.
func PlaybackStates(ctx context.Context, db *sql.DB, userID string, ids []string) (map[string]PlaybackState, error) {
	out := map[string]PlaybackState{}
	if userID == "" {
		return out, nil
	}
	err := forChunks(uniq(ids), 2, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, position_sec, completed, updated_at FROM playback_state
			WHERE user_id = $1 AND video_id IN (`+ph+`)`, append([]any{userID}, args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var ps PlaybackState
			if err := rows.Scan(&vid, &ps.PositionSec, &ps.Completed, &ps.UpdatedAt); err != nil {
				return err
			}
			out[vid] = ps
		}
		return rows.Err()
	})
	return out, err
}

// SubtitlesByVideo lists each video's live subtitle files, ordered by
// language then format, with their stream URLs.
func SubtitlesByVideo(ctx context.Context, db *sql.DB, ids []string) (map[string][]Subtitle, error) {
	out := map[string][]Subtitle{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, id, language, format, source FROM subtitle_files
			WHERE video_id IN (`+ph+`) AND deleted_at IS NULL
			ORDER BY video_id, language ASC, format ASC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var s Subtitle
			if err := rows.Scan(&vid, &s.ID, &s.Language, &s.Format, &s.Source); err != nil {
				return err
			}
			s.URL = "/stream/subtitles/" + vid + "/" + s.Language + "." + s.Format
			out[vid] = append(out[vid], s)
		}
		return rows.Err()
	})
	return out, err
}

// ChaptersByVideo lists each video's chapters in order.
func ChaptersByVideo(ctx context.Context, db *sql.DB, ids []string) (map[string][]Chapter, error) {
	out := map[string][]Chapter{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, id, seq, start_sec, end_sec, title, source FROM chapters
			WHERE video_id IN (`+ph+`) ORDER BY video_id, seq ASC`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var c Chapter
			if err := rows.Scan(&vid, &c.ID, &c.Seq, &c.StartSec, &c.EndSec, &c.Title, &c.Source); err != nil {
				return err
			}
			out[vid] = append(out[vid], c)
		}
		return rows.Err()
	})
	return out, err
}

// MediaInfo is a video's probe summary (slot 0009 media_info).
type MediaInfo struct {
	Container   *string  `json:"container,omitempty"`
	VideoCodec  *string  `json:"video_codec,omitempty"`
	Width       *int     `json:"width,omitempty"`
	Height      *int     `json:"height,omitempty"`
	FPS         *float64 `json:"fps,omitempty"`
	BitrateKbps *int     `json:"bitrate_kbps,omitempty"`
}

// MediaInfoByVideo returns the probe summary of each probed video.
func MediaInfoByVideo(ctx context.Context, db *sql.DB, ids []string) (map[string]MediaInfo, error) {
	out := map[string]MediaInfo{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, container, video_codec, width, height, fps, bitrate_kbps
			FROM media_info WHERE video_id IN (`+ph+`)`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var m MediaInfo
			var container, codec sql.NullString
			var width, height, bitrate sql.NullInt64
			var fps sql.NullFloat64
			if err := rows.Scan(&vid, &container, &codec, &width, &height, &fps, &bitrate); err != nil {
				return err
			}
			m.Container = nullStr(container)
			m.VideoCodec = nullStr(codec)
			m.Width = nullInt(width)
			m.Height = nullInt(height)
			m.BitrateKbps = nullInt(bitrate)
			if fps.Valid {
				f := fps.Float64
				m.FPS = &f
			}
			out[vid] = m
		}
		return rows.Err()
	})
	return out, err
}

// AudioTrack is one probed audio stream (slot 0009 audio_tracks).
type AudioTrack struct {
	ID       int64   `json:"id"`
	Index    int     `json:"track_index"`
	Language *string `json:"language,omitempty"`
	Codec    string  `json:"codec"`
	Channels *int    `json:"channels,omitempty"`
	Default  bool    `json:"is_default"`
}

// AudioTracksByVideo lists each video's audio tracks in stream order.
// The language is the detected one when the probe tag was "und".
func AudioTracksByVideo(ctx context.Context, db *sql.DB, ids []string) (map[string][]AudioTrack, error) {
	out := map[string][]AudioTrack{}
	err := forChunks(uniq(ids), 1, func(ph string, args []any) error {
		rows, err := db.QueryContext(ctx, `
			SELECT video_id, id, track_index, language, detected_language, codec, channels, is_default
			FROM audio_tracks WHERE video_id IN (`+ph+`) ORDER BY video_id, track_index`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var vid string
			var a AudioTrack
			var lang, detected, codec sql.NullString
			var channels sql.NullInt64
			if err := rows.Scan(&vid, &a.ID, &a.Index, &lang, &detected, &codec, &channels, &a.Default); err != nil {
				return err
			}
			switch {
			case lang.Valid && lang.String != "" && lang.String != "und":
				a.Language = nullStr(lang)
			case detected.Valid && detected.String != "":
				a.Language = nullStr(detected)
			}
			a.Codec = codec.String
			a.Channels = nullInt(channels)
			out[vid] = append(out[vid], a)
		}
		return rows.Err()
	})
	return out, err
}

func nullStr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}
