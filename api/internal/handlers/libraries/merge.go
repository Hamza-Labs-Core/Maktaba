// Epic 45 story 45.2: what removing a library takes with it, merging one
// library into another, and emptying a library's trash.
//
//	GET  /api/libraries/{id}/impact
//	POST /api/libraries/{id}/merge        {into, confirm}
//	POST /api/libraries/{id}/empty-trash
//
// All three are admin-only and refuse anyone else before any query.
package libraries

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// queryer is a *sql.DB or a *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// hasColumn reports whether table exists with column. It runs on the pool,
// never inside a transaction: on Postgres a failed statement aborts the
// transaction it ran in. table and column are constants of this file.
func (h *Handler) hasColumn(ctx context.Context, table, column string) bool {
	rows, err := h.DB.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE 1 = 0`)
	if err != nil {
		return false
	}
	_ = rows.Close()
	return true
}

// libraryID reads and checks the {id} path parameter.
func libraryID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.BadRequest("malformed id"))
		return "", false
	}
	return id, true
}

// ImpactResponse counts what goes with a library.
type ImpactResponse struct {
	Videos         int `json:"videos"`
	Transcripts    int `json:"transcripts"`
	PlaybackStates int `json:"playback_states"`
	WatchSessions  int `json:"watch_sessions"`
	Series         int `json:"series"`
	Collections    int `json:"collections"`
	ChannelBlocks  int `json:"channel_blocks"`
}

// Impact counts what removing (or merging) the library takes with it: its
// videos, their transcripts, playback positions and watch history, its
// series and collections, and the channels' upcoming blocks that play its
// videos. A table this database does not have counts 0.
func (h *Handler) Impact(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(w, r); !ok {
		return
	}
	id, ok := libraryID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := h.loadLibrary(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	var out ImpactResponse
	for _, c := range []struct {
		table, column string
		dst           *int
		query         string
		args          []any
	}{
		{"videos", "library_id", &out.Videos, `SELECT COUNT(*) FROM videos WHERE library_id = $1`, nil},
		{"transcripts", "video_id", &out.Transcripts,
			`SELECT COUNT(*) FROM transcripts t JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1`, nil},
		{"playback_state", "video_id", &out.PlaybackStates,
			`SELECT COUNT(*) FROM playback_state p JOIN videos v ON v.id = p.video_id WHERE v.library_id = $1`, nil},
		{"watch_sessions", "video_id", &out.WatchSessions,
			`SELECT COUNT(*) FROM watch_sessions s JOIN videos v ON v.id = s.video_id WHERE v.library_id = $1`, nil},
		{"series", "library_id", &out.Series, `SELECT COUNT(*) FROM series WHERE library_id = $1`, nil},
		{"collections", "library_id", &out.Collections, `SELECT COUNT(*) FROM collections WHERE library_id = $1`, nil},
		// past blocks are history; the upcoming ones lose their video
		{"channel_programs", "video_id", &out.ChannelBlocks,
			`SELECT COUNT(*) FROM channel_programs cp JOIN videos v ON v.id = cp.video_id
			 WHERE v.library_id = $1 AND cp.end_at > $2`, []any{h.now()}},
	} {
		if !h.hasColumn(ctx, c.table, c.column) {
			continue
		}
		if err := h.DB.QueryRowContext(ctx, c.query, append([]any{id}, c.args...)...).Scan(c.dst); err != nil {
			httperror.Write(w, r, httperror.Internal("count "+c.table).Wrap(err))
			return
		}
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// MergeRequest is the POST …/merge body.
type MergeRequest struct {
	Into    string `json:"into"`
	Confirm string `json:"confirm"`
}

// MergeResponse is what a merge did.
type MergeResponse struct {
	Moved             int64   `json:"moved"`
	DuplicatesRemoved int64   `json:"duplicates_removed"`
	RootsAdded        int     `json:"roots_added"`
	Into              Library `json:"into"`
}

// mergeMoves carry the merged library's rows that belong to the videos
// that move, or group them, over to the target ($1 the target, $2 the
// merged library). Each runs only when its table (and column) exists.
//
// Left to go with the merged library (ON DELETE CASCADE): its scan jobs and
// library-wide jobs, topics (library_topics / video_topics: the next topic
// pass groups the target's videos again), collection suggestions, sweeps,
// scan state, STT usage, purge log, the stats cache and its viewers' grants
// (library_acl: a viewer of the merged library is not given the target).
var mergeMoves = []struct{ table, column, query string }{
	// rows of single videos: every video still in the merged library moves
	// (the duplicates are gone by now), so all of these do
	{"processing_jobs", "subject_kind",
		`UPDATE processing_jobs SET library_id = $1 WHERE library_id = $2 AND subject_kind <> 'library'`},
	{"video_embeddings", "library_id", `UPDATE video_embeddings SET library_id = $1 WHERE library_id = $2`},
	{"segment_embeddings", "library_id", `UPDATE segment_embeddings SET library_id = $1 WHERE library_id = $2`},
	{"video_repairs", "library_id", `UPDATE video_repairs SET library_id = $1 WHERE library_id = $2`},
	{"promo_links", "library_id", `UPDATE promo_links SET library_id = $1 WHERE library_id = $2`},
	{"plex_items", "library_id", `UPDATE plex_items SET library_id = $1 WHERE library_id = $2`},
	{"job_events", "library_id", `UPDATE job_events SET library_id = $1 WHERE library_id = $2`},
	// what groups those videos: the series (and their seasons, which would
	// otherwise cascade away), speakers, YouTube channels, clip groups
	{"series", "library_id", `UPDATE series SET library_id = $1 WHERE library_id = $2`},
	{"series_seasons", "library_id", `UPDATE series_seasons SET library_id = $1 WHERE library_id = $2`},
	{"speakers", "library_id", `UPDATE speakers SET library_id = $1 WHERE library_id = $2`},
	{"youtube_channels", "library_id", `UPDATE youtube_channels SET library_id = $1 WHERE library_id = $2`},
	{"clip_groups", "library_id", `UPDATE clip_groups SET library_id = $1 WHERE library_id = $2`},
	// named per library: moved unless the target already has that name;
	// the clashing ones go with the merged library
	{"collections", "library_id", `UPDATE collections SET library_id = $1 WHERE library_id = $2
		AND NOT EXISTS (SELECT 1 FROM collections c WHERE c.library_id = $1 AND c.name = collections.name)`},
	{"topic_collections", "library_id", `UPDATE topic_collections SET library_id = $1 WHERE library_id = $2
		AND NOT EXISTS (SELECT 1 FROM topic_collections c WHERE c.library_id = $1 AND c.playlist_id = topic_collections.playlist_id)`},
	{"channels", "library_id", `UPDATE channels SET library_id = $1 WHERE library_id = $2
		AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.library_id = $1
			AND (c.number = channels.number OR c.slug = channels.slug))`},
}

// Merge merges library {id} into another one, in one transaction: each video
// moves unless the target already holds the same file (content_hash, unique
// per library), in which case it is deleted with its data; the folders the
// target does not cover are added to it; the merged library is deleted.
func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := libraryID(w, r)
	if !ok {
		return
	}
	var req MergeRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if _, err := uuid.Parse(req.Into); err != nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "into", Message: "a library id is required"}}))
		return
	}
	if req.Into == id {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "into", Message: "a library cannot be merged into itself"}}))
		return
	}
	ctx := r.Context()

	// which tables this database has, asked before the transaction
	var moves []string
	for _, m := range mergeMoves {
		if h.hasColumn(ctx, m.table, m.column) {
			moves = append(moves, m.query)
		}
	}
	hasRootsTable := h.hasColumn(ctx, "library_roots", "path_canonical")
	hasStatsCache := h.hasColumn(ctx, "library_stats_cache", "library_id")

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()

	from, err := loadLibraryTx(ctx, tx, id)
	var into Library
	if err == nil {
		into, err = loadLibraryTx(ctx, tx, req.Into)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library"))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	if req.Confirm != from.Name {
		httperror.Write(w, r, &httperror.Error{
			Type:   httperror.TypeConfirmationReq,
			Title:  "confirmation required",
			Status: http.StatusPreconditionFailed,
			Detail: "send confirm=<the merged library's name> to merge it",
		})
		return
	}
	resp, err := h.merge(ctx, tx, from, into, moves, hasRootsTable, hasStatsCache)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("merge").Wrap(err))
		return
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("commit").Wrap(err))
		return
	}
	WriteAudit(ctx, h.DB, EventLibraryMerged, ActorOf(p), into.ID, map[string]any{
		"from_id": from.ID, "from_name": from.Name, "from_roots": from.Roots,
		"into_name": into.Name, "moved": resp.Moved,
		"duplicates_removed": resp.DuplicatesRemoved, "roots_added": resp.RootsAdded,
	})
	common.WriteJSON(w, r, http.StatusOK, resp)
}

func (h *Handler) merge(ctx context.Context, tx *sql.Tx, from, into Library, moves []string, hasRootsTable, hasStatsCache bool) (MergeResponse, error) {
	var resp MergeResponse
	res, err := tx.ExecContext(ctx, `
		DELETE FROM videos WHERE library_id = $2
		AND content_hash IN (SELECT content_hash FROM videos WHERE library_id = $1)
	`, into.ID, from.ID)
	if err != nil {
		return resp, err
	}
	resp.DuplicatesRemoved, _ = res.RowsAffected()

	for _, q := range moves {
		if _, err := tx.ExecContext(ctx, q, into.ID, from.ID); err != nil {
			return resp, err
		}
	}
	res, err = tx.ExecContext(ctx, `UPDATE videos SET library_id = $1 WHERE library_id = $2`, into.ID, from.ID)
	if err != nil {
		return resp, err
	}
	resp.Moved, _ = res.RowsAffected()

	roots, added := mergeRoots(into.Roots, from.Roots)
	resp.RootsAdded = added
	now := h.now()
	if _, err := tx.ExecContext(ctx, `UPDATE libraries SET roots = $1, updated_at = $2 WHERE id = $3`,
		stringArray(roots), now, into.ID); err != nil {
		return resp, err
	}
	if hasRootsTable {
		if err := SyncLibraryRoots(ctx, tx, into.ID, roots); err != nil {
			return resp, err
		}
	}
	// both libraries' cached stats are wrong now: dropped, the next read recomputes
	if hasStatsCache {
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_stats_cache WHERE library_id IN ($1, $2)`,
			into.ID, from.ID); err != nil {
			return resp, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM libraries WHERE id = $1`, from.ID); err != nil {
		return resp, err
	}
	into.Roots, into.UpdatedAt = roots, now
	resp.Into = into
	return resp, nil
}

// mergeRoots adds to into's folders each of from's that they do not already
// cover (the same folder or one inside it). A folder of from that holds some
// of into's replaces them: a library's folders never nest.
func mergeRoots(into, from []string) ([]string, int) {
	out := append([]string(nil), into...)
	added := 0
	for _, f := range from {
		covered := false
		for _, r := range out {
			if k := overlapKind(f, r); k == kindSame || k == kindInside {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept := []string{}
		for _, r := range out {
			if overlapKind(r, f) != kindInside {
				kept = append(kept, r)
			}
		}
		out = append(kept, f)
		added++
	}
	return out, added
}

func loadLibraryTx(ctx context.Context, q queryer, id string) (Library, error) {
	var lib Library
	var roots stringArray
	var settings []byte
	err := q.QueryRowContext(ctx, `
		SELECT id, name, roots, settings, created_at, updated_at FROM libraries WHERE id = $1
	`, id).Scan(&lib.ID, &lib.Name, &roots, &settings, &lib.CreatedAt, &lib.UpdatedAt)
	if err != nil {
		return Library{}, err
	}
	lib.Roots = roots
	if len(settings) == 0 {
		settings = []byte(`{}`)
	}
	lib.Settings = settings
	return lib, nil
}

// EmptyTrash deletes the library's videos whose files are gone (state
// missing), with their data, as Plex's Empty Trash does.
func (h *Handler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	id, ok := libraryID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := h.loadLibrary(ctx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	res, err := h.DB.ExecContext(ctx, `DELETE FROM videos WHERE library_id = $1 AND state = 'missing'`, id)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("empty trash").Wrap(err))
		return
	}
	removed, _ := res.RowsAffected()
	if removed > 0 && h.hasColumn(ctx, "library_stats_cache", "library_id") {
		// best effort: the stats read recomputes a dropped row
		_, _ = h.DB.ExecContext(ctx, `DELETE FROM library_stats_cache WHERE library_id = $1`, id)
	}
	WriteAudit(ctx, h.DB, EventTrashEmptied, ActorOf(p), id, map[string]any{"removed": removed})
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"removed": removed})
}
