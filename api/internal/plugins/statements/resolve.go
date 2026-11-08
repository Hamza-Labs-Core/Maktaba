package statements

// Row scopes (Epic 54, README §15.12): a write that addresses a row by an id of its own (a
// season, a transcript, a YouTube channel) names a check of that kind, and the API looks
// the row up here, with SQL of its own, to find whose it is. The kinds are those of
// tools/plugins/scope_tables.toml; the build refuses a write it cannot bind to one.

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/google/uuid"
)

// rowKind is how one kind of id is read and looked up: its key's type and a query that
// answers the row's owners as (library, video) pairs, either NULL where it has none.
type rowKind struct {
	key   string // uuid | int | text
	query string
}

// ownersOfYouTube are the libraries a YouTube row reached through channel y.channel_id is
// in the scope of: the channel's own library, the libraries that follow it as a source, and
// NULL (every library) while no library has claimed the channel.
const ownersOfYouTube = `
	SELECT c.library_id FROM youtube_channels c WHERE c.channel_id = y.channel_id
	UNION ALL SELECT s.library_id FROM youtube_sources s WHERE s.external_id = y.channel_id
	UNION ALL SELECT NULL::uuid WHERE NOT EXISTS (
		SELECT 1 FROM youtube_channels c WHERE c.channel_id = y.channel_id AND c.library_id IS NOT NULL)`

var rowKinds = map[string]rowKind{
	"series":           {"uuid", `SELECT CAST(library_id AS text), NULL FROM series WHERE id = CAST($1 AS uuid)`},
	"season":           {"uuid", `SELECT CAST(library_id AS text), NULL FROM series_seasons WHERE id = CAST($1 AS uuid)`},
	"clip_group":       {"uuid", `SELECT CAST(library_id AS text), NULL FROM clip_groups WHERE id = CAST($1 AS uuid)`},
	"topic_collection": {"uuid", `SELECT CAST(library_id AS text), NULL FROM topic_collections WHERE id = CAST($1 AS uuid)`},
	"dup_group":        {"uuid", `SELECT CAST(library_id AS text), NULL FROM duplicate_groups WHERE id = CAST($1 AS uuid)`},
	"youtube_source":   {"uuid", `SELECT CAST(library_id AS text), NULL FROM youtube_sources WHERE id = CAST($1 AS uuid)`},
	"file_action": {"uuid", `SELECT CAST(library_id AS text), CAST(video_id AS text) FROM file_actions
		WHERE id = CAST($1 AS uuid)`},
	"transcript": {"uuid", `SELECT CAST(v.library_id AS text), CAST(v.id AS text) FROM transcripts t
		JOIN videos v ON v.id = t.video_id WHERE t.id = CAST($1 AS uuid)`},
	"audio_track": {"int", `SELECT CAST(v.library_id AS text), CAST(v.id AS text) FROM audio_tracks a
		JOIN videos v ON v.id = a.video_id WHERE a.id = CAST($1 AS bigint)`},
	"segment": {"int", `SELECT CAST(v.library_id AS text), CAST(v.id AS text) FROM transcript_segments s
		JOIN transcripts t ON t.id = s.transcript_id JOIN videos v ON v.id = t.video_id WHERE s.id = CAST($1 AS bigint)`},
	"audio_cache": {"text", `SELECT CAST(v.library_id AS text), CAST(v.id AS text) FROM audio_cache a
		JOIN videos v ON v.id = a.video_id WHERE a.content_hash = $1`},
	"youtube_channel": {"text", `SELECT CAST(c.library_id AS text), NULL FROM youtube_channels c WHERE c.channel_id = $1
		UNION ALL SELECT CAST(s.library_id AS text), NULL FROM youtube_sources s
		WHERE s.external_id = $1 AND EXISTS (SELECT 1 FROM youtube_channels c WHERE c.channel_id = $1)`},
	"youtube_playlist": {"text", `SELECT CAST(o.library_id AS text), NULL FROM youtube_playlists y
		CROSS JOIN LATERAL (` + ownersOfYouTube + `
		UNION ALL SELECT s.library_id FROM youtube_sources s WHERE s.external_id = y.playlist_id) AS o(library_id)
		WHERE y.playlist_id = $1`},
	"youtube_video": {"text", `SELECT CAST(o.library_id AS text), NULL FROM youtube_videos y
		CROSS JOIN LATERAL (` + ownersOfYouTube + `
		UNION ALL SELECT d.library_id FROM youtube_downloads d WHERE d.youtube_id = y.youtube_id) AS o(library_id)
		WHERE y.youtube_id = $1`},
}

// KnownScope reports whether the API checks a scope of this kind.
func KnownScope(kind string) bool {
	switch kind {
	case "subject", "library", "settings":
		return true
	}
	_, ok := rowKinds[kind]
	return ok
}

// RowScopes are the kinds resolved by a lookup (tests: one for each in scope_tables.toml).
func RowScopes() []string {
	out := make([]string, 0, len(rowKinds))
	for k := range rowKinds {
		out = append(out, k)
	}
	return out
}

// checkRow checks an id of a row kind: allowed when no row has it (an insert's new id, or a
// write that matches nothing), when the row is no library's, or when one of its owners is
// the job's library (and, for a video job, the job's video or no video). A job with no
// library (the catalog's) spans every library.
func (e *Executor) checkRow(ctx context.Context, sc Scope, st *Statement, kind, v string) *Error {
	rk := rowKinds[kind]
	key := v
	switch rk.key {
	case "uuid":
		id, err := uuid.Parse(v)
		if err != nil {
			return refuse(403, CodeScope, st.Name+": the scope argument is not a "+kind+" id")
		}
		key = id.String()
	case "int":
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return refuse(403, CodeScope, st.Name+": the scope argument is not a "+kind+" id")
		}
		key = strconv.FormatInt(n, 10)
	}
	if sc.LibraryID == "" && sc.SubjectKind != "video" {
		return nil
	}
	rows, err := e.DB.QueryContext(ctx, rk.query, key)
	if err != nil {
		return refuse(503, CodeDatabase, err.Error())
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var lib, video sql.NullString
		if err := rows.Scan(&lib, &video); err != nil {
			return refuse(503, CodeDatabase, err.Error())
		}
		found = true
		if !lib.Valid {
			return nil // a row no library owns
		}
		if sc.LibraryID != "" && !sameID(lib.String, sc.LibraryID) {
			continue
		}
		if sc.SubjectKind == "video" && video.Valid && !sameID(video.String, sc.SubjectID) {
			continue
		}
		return nil
	}
	if err := rows.Err(); err != nil {
		return refuse(503, CodeDatabase, err.Error())
	}
	if !found {
		return nil
	}
	return refuse(403, CodeScope, st.Name+": "+kind+" "+key+" is not of this job's scope")
}
