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
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/channels"
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

// isSQLite tells the two databases apart where their SQL differs.
func isSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
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
	Grants         int `json:"grants"`
}

// Impact counts what removing (or merging) the library takes with it: its
// videos, their transcripts, playback positions and watch history, its
// series and collections, and the channels' upcoming blocks that play its
// videos, and its grants (the library_acl rows: the users who lose access,
// a merge included, since a merge never copies grants to the target). A
// table this database does not have counts 0.
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
		{"library_acl", "library_id", &out.Grants, `SELECT COUNT(*) FROM library_acl WHERE library_id = $1`, nil},
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
// scan state, STT usage, purge log, the stats cache and its grants
// (library_acl: a merge never widens access, so a viewer of the merged
// library is not given the target; the impact's grants say who loses it).
//
// Collections, topic collections and channels are named per library; they
// move in mergeNamed, renamed when the target has the same one.
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
}

// mergeTables is what this database has, asked before the transaction.
type mergeTables struct {
	moves                         []string
	roots, statsCache             bool
	collections, topics, channels bool
	pins                          bool
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
	var tables mergeTables
	for _, m := range mergeMoves {
		if h.hasColumn(ctx, m.table, m.column) {
			tables.moves = append(tables.moves, m.query)
		}
	}
	tables.roots = h.hasColumn(ctx, "library_roots", "path_canonical")
	tables.statsCache = h.hasColumn(ctx, "library_stats_cache", "library_id")
	tables.collections = h.hasColumn(ctx, "collections", "name")
	tables.topics = h.hasColumn(ctx, "topic_collections", "playlist_id")
	tables.channels = h.hasColumn(ctx, "channels", "slug")
	tables.pins = h.hasColumn(ctx, "user_library_pins", "position")

	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("tx begin").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()

	// On Postgres both rows are locked until the commit, so a rename or a
	// folder change made meanwhile waits and is not lost under the merge's
	// write. Always in id order: two merges of the same pair never deadlock.
	// SQLite's write lock already serialises the writers.
	lock := !isSQLite(h.DB)
	first, second := id, req.Into
	if second < first {
		first, second = second, first
	}
	var from, into Library
	got := map[string]Library{}
	for _, lid := range []string{first, second} {
		var l Library
		if l, err = loadLibraryTx(ctx, tx, lid, lock); err != nil {
			break
		}
		got[lid] = l
	}
	if err == nil {
		from, into = got[id], got[req.Into]
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library"))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	if !sameName(req.Confirm, from.Name) {
		httperror.Write(w, r, &httperror.Error{
			Type:   httperror.TypeConfirmationReq,
			Title:  "confirmation required",
			Status: http.StatusPreconditionFailed,
			Detail: "send confirm=<the merged library's name> to merge it",
		})
		return
	}
	resp, err := h.merge(ctx, tx, from, into, tables)
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

func (h *Handler) merge(ctx context.Context, tx *sql.Tx, from, into Library, tables mergeTables) (MergeResponse, error) {
	var resp MergeResponse
	res, err := tx.ExecContext(ctx, `
		DELETE FROM videos WHERE library_id = $2
		AND content_hash IN (SELECT content_hash FROM videos WHERE library_id = $1)
	`, into.ID, from.ID)
	if err != nil {
		return resp, err
	}
	resp.DuplicatesRemoved, _ = res.RowsAffected()

	for _, q := range tables.moves {
		if _, err := tx.ExecContext(ctx, q, into.ID, from.ID); err != nil {
			return resp, err
		}
	}
	if err := mergeNamed(ctx, tx, from, into, tables); err != nil {
		return resp, err
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
	if tables.roots {
		if err := SyncLibraryRoots(ctx, tx, into.ID, roots); err != nil {
			return resp, err
		}
	}
	// both libraries' cached stats are wrong now: dropped, the next read recomputes
	if tables.statsCache {
		if _, err := tx.ExecContext(ctx, `DELETE FROM library_stats_cache WHERE library_id IN ($1, $2)`,
			into.ID, from.ID); err != nil {
			return resp, err
		}
	}
	// a user who pinned the merged library keeps the target pinned in its
	// place; one who pinned both keeps the target's pin
	if tables.pins {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_library_pins (user_id, library_id, position, created_at)
			SELECT user_id, $1, position, created_at FROM user_library_pins WHERE library_id = $2
			ON CONFLICT (user_id, library_id) DO NOTHING
		`, into.ID, from.ID); err != nil {
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

// loadLibraryTx reads one library; forUpdate (Postgres only) locks its row
// until the transaction ends.
func loadLibraryTx(ctx context.Context, q queryer, id string, forUpdate bool) (Library, error) {
	var lib Library
	var roots stringArray
	var settings []byte
	query := `SELECT id, name, roots, settings, created_at, updated_at FROM libraries WHERE id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	err := q.QueryRowContext(ctx, query, id).Scan(&lib.ID, &lib.Name, &roots, &settings, &lib.CreatedAt, &lib.UpdatedAt)
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
	lib, err := h.loadLibrary(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("library "+id))
			return
		}
		httperror.Write(w, r, httperror.Internal("load library").Wrap(err))
		return
	}
	// A share that was not mounted during the last scan marks every video
	// under it missing: with a folder out of reach now, "missing" may mean
	// "not mounted", so nothing is deleted.
	if problems := h.checkRoots(lib.Roots); len(problems) > 0 {
		var down []string
		for root, e := range problems {
			down = append(down, root+" ("+e.Error()+")")
		}
		sort.Strings(down)
		httperror.Write(w, r, httperror.Conflict("https://maktaba.dev/problems/library-roots-unreachable",
			"this machine cannot reach "+strings.Join(down, ", ")+"; reconnect it and scan again before emptying the trash"))
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

// mergeNamed moves the merged library's collections, topic collections and
// channels to the target, keeping every one: one that clashes with the
// target's (a collection's name, a topic collection's playlist, a channel's
// slug or number, each unique per library) is renamed "<name> (<merged
// library>)", and given the next free slug ("-2", "-3", as the channels
// handler does), number (after the target's highest) or playlist key.
func mergeNamed(ctx context.Context, tx *sql.Tx, from, into Library, tables mergeTables) error {
	suffix := " (" + from.Name + ")"
	if tables.collections {
		taken, err := takenSet(ctx, tx, `SELECT name FROM collections WHERE library_id = $1`, into.ID)
		if err != nil {
			return err
		}
		rows, err := namedRows(ctx, tx, `SELECT id, name FROM collections WHERE library_id = $1`, from.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			name := row[1]
			if taken[name] {
				name = freeName(taken, name+suffix)
			}
			taken[name] = true
			if _, err := tx.ExecContext(ctx, `UPDATE collections SET library_id = $1, name = $2 WHERE id = $3`,
				into.ID, name, row[0]); err != nil {
				return err
			}
		}
	}
	if tables.topics {
		taken, err := takenSet(ctx, tx, `SELECT playlist_id FROM topic_collections WHERE library_id = $1`, into.ID)
		if err != nil {
			return err
		}
		rows, err := namedRows(ctx, tx, `SELECT id, playlist_id, name FROM topic_collections WHERE library_id = $1`, from.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			playlist, name := row[1], row[2]
			if taken[playlist] {
				playlist = freeKey(taken, playlist)
				name += suffix
			}
			taken[playlist] = true
			if _, err := tx.ExecContext(ctx, `UPDATE topic_collections SET library_id = $1, playlist_id = $2, name = $3 WHERE id = $4`,
				into.ID, playlist, name, row[0]); err != nil {
				return err
			}
		}
	}
	if tables.channels {
		// every channel of the target counts, retired ones too: the index
		// covers the live ones only, so this is the safe side
		slugs, err := takenSet(ctx, tx, `SELECT slug FROM channels WHERE library_id = $1`, into.ID)
		if err != nil {
			return err
		}
		numbers, err := takenSet(ctx, tx, `SELECT number FROM channels WHERE library_id = $1`, into.ID)
		if err != nil {
			return err
		}
		maxNum := 0
		for n := range numbers {
			if v, _ := strconv.Atoi(n); v > maxNum {
				maxNum = v
			}
		}
		rows, err := namedRows(ctx, tx, `SELECT id, slug, number, name FROM channels WHERE library_id = $1 ORDER BY number, id`, from.ID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			slug, number, name := row[1], row[2], row[3]
			clash := false
			if slugs[slug] {
				clash = true
				for n := 2; ; n++ {
					if s := channels.SlugWithSuffix(slug, n); !slugs[s] {
						slug = s
						break
					}
				}
			}
			if numbers[number] {
				clash = true
				maxNum++
				number = strconv.Itoa(maxNum)
			}
			if clash {
				name += suffix
			}
			slugs[slug], numbers[number] = true, true
			if v, _ := strconv.Atoi(number); v > maxNum {
				maxNum = v
			}
			num, _ := strconv.Atoi(number)
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET library_id = $1, slug = $2, number = $3, name = $4 WHERE id = $5`,
				into.ID, slug, num, name, row[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

// takenSet reads one column of the target's rows as text.
func takenSet(ctx context.Context, tx *sql.Tx, q, libraryID string) (map[string]bool, error) {
	rows, err := namedRows(ctx, tx, q, libraryID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r[0]] = true
	}
	return out, nil
}

// namedRows reads every row of q as text columns (the merged library holds
// few of these).
func namedRows(ctx context.Context, tx *sql.Tx, q, libraryID string) ([][]string, error) {
	rows, err := tx.QueryContext(ctx, q, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = v.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// freeName is name, or name with " 2", " 3"... when that is taken too.
func freeName(taken map[string]bool, name string) string {
	if !taken[name] {
		return name
	}
	for n := 2; ; n++ {
		if s := name + " " + strconv.Itoa(n); !taken[s] {
			return s
		}
	}
}

// freeKey is key with "-merged", "-merged-2"... the first one not taken.
func freeKey(taken map[string]bool, key string) string {
	for n := 1; ; n++ {
		s := key + "-merged"
		if n > 1 {
			s += "-" + strconv.Itoa(n)
		}
		if !taken[s] {
			return s
		}
	}
}
