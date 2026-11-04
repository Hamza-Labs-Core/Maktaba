package queues

// The channel a library with no YouTube source looks like (slot 0175), for the libraries
// Tartube's records do not cover (yttartube.go is the first way):
//
//	POST /api/libraries/{id}/youtube/guess   look now; the answer is kept (youtube_source_guesses)
//
// Slot 0182: the library's linked videos come first, so the answer agrees with what the
// pipeline derives (library_mgmt/youtube_derive.py): its decision (youtube_source_decisions:
// the playlist or channel it chose, or the one another library holds), else the channel
// most of its linked videos are from. The evidence is info_json when most links came from
// sidecars, filename when from titles. Only a library with no link reads its files:
//
// The YouTube ids are read from the library's file names as yt-dlp writes them
// (`… #<id>.mp4`, `… [<id>].mp4`) and from its `.info.json` sidecars (channel_id,
// playlist_id), and resolved through what Maktaba already knows of YouTube
// (youtube_videos, youtube_channels): no network. An admin's click, never a page
// load; nothing is added or turned on from it (the card offers "Add as source", off).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// SourceGuess is what POST …/youtube/guess found (Kind, ExternalID and URL nil: nothing).
type SourceGuess struct {
	LibraryID       string        `json:"library_id"`
	Kind            *string       `json:"kind"`
	ExternalID      *string       `json:"external_id"`
	URL             *string       `json:"url"`
	Title           *string       `json:"title"`
	Evidence        *string       `json:"evidence"`
	SampleYouTubeID *string       `json:"sample_youtube_id"`
	IDsFound        int           `json:"ids_found"`
	IDsMatched      int           `json:"ids_matched"`
	LookedAt        time.Time     `json:"looked_at"`
	FollowedBy      []LibraryName `json:"followed_by"`
}

// LibraryName names a library.
type LibraryName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var (
	// yt-dlp's "%(title)s #%(id)s.%(ext)s" and "%(title)s [%(id)s].%(ext)s"
	fileIDRe    = regexp.MustCompile(`(?:#|\[)([A-Za-z0-9_-]{11})\]?\.[A-Za-z0-9]{1,5}$`)
	ytChannelRe = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)
	ytPlaylist  = regexp.MustCompile(`^(?:PL|OL|UU|FL|LL)[A-Za-z0-9_-]{8,64}$`)
)

// guessSidecars bounds the .info.json files read per look, and the time they may take
// (a NAS mount that stopped answering would hold the request). Variables for the tests.
var (
	guessSidecars       = 40
	guessSidecarTimeout = 5 * time.Second
)

const maxSidecarBytes = 8 << 20

type sidecar struct {
	ChannelID     string `json:"channel_id"`
	Channel       string `json:"channel"`
	Uploader      string `json:"uploader"`
	PlaylistID    string `json:"playlist_id"`
	PlaylistTitle string `json:"playlist_title"`
	ID            string `json:"id"`
}

// readSidecars reads the .info.json beside each of paths (the database's form), at most
// guessSidecars of them, within guessSidecarTimeout; what it read by then.
func (h *Handler) readSidecars(paths []string) []sidecar {
	ch := make(chan []sidecar, 1)
	stop := make(chan struct{})
	go func() {
		defer safego.Recover(nil, "youtube_guess_sidecars")
		var out []sidecar
		for i, p := range paths {
			if i >= guessSidecars {
				break
			}
			select {
			case <-stop:
				ch <- out
				return
			default:
			}
			local := h.local(p)
			base := strings.TrimSuffix(local, filepath.Ext(local))
			f, err := os.Open(base + ".info.json")
			if err != nil {
				continue
			}
			var s sidecar
			err = json.NewDecoder(io.LimitReader(f, maxSidecarBytes)).Decode(&s)
			_ = f.Close()
			if err == nil {
				out = append(out, s)
			}
		}
		ch <- out
	}()
	select {
	case out := <-ch:
		return out
	case <-time.After(guessSidecarTimeout):
		close(stop)
		return nil
	}
}

type vote struct {
	kind, id, title, sample string
	n                       int
}

// linkedGuess is what library lib's linked videos say (slot 0182); ok false when it has
// no link that names a channel.
func (h *Handler) linkedGuess(ctx context.Context, lib string) (g SourceGuess, ok bool, err error) {
	g = SourceGuess{LibraryID: lib, FollowedBy: []LibraryName{}}
	var linked, fromSidecar int
	err = h.DB.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE m.mapped->>'link_method' = 'info_json')
		  FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id
		 WHERE m.provider = 'youtube' AND v.library_id = $1 AND v.deleted_at IS NULL`, lib).Scan(&linked, &fromSidecar)
	if err != nil || linked == 0 {
		return g, false, err
	}
	var kind, ext string
	matched := 0
	var outcome, dKind, clash sql.NullString
	var ids []byte
	var dMatched int
	err = h.DB.QueryRowContext(ctx, `SELECT outcome, kind, external_ids, clash_external_id, matched_videos
		  FROM youtube_source_decisions WHERE library_id = $1`, lib).Scan(&outcome, &dKind, &ids, &clash, &dMatched)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return g, false, err
	}
	if err == nil && dKind.Valid {
		var chosen []string
		_ = json.Unmarshal(ids, &chosen)
		switch {
		case outcome.String == "clash" && clash.Valid:
			kind, ext, matched = dKind.String, clash.String, dMatched
		case (outcome.String == "playlist" || outcome.String == "channel") && len(chosen) > 0:
			kind, ext, matched = dKind.String, chosen[0], dMatched
		}
	}
	if ext == "" {
		err = h.DB.QueryRowContext(ctx, `SELECT y.channel_id, count(*) FROM media_metadata_enrichment m
			  JOIN videos v ON v.id = m.video_id JOIN youtube_videos y ON y.youtube_id = m.external_id
			 WHERE m.provider = 'youtube' AND v.library_id = $1 AND v.deleted_at IS NULL AND y.channel_id IS NOT NULL
			 GROUP BY y.channel_id ORDER BY count(*) DESC, y.channel_id LIMIT 1`, lib).Scan(&ext, &matched)
		if errors.Is(err, sql.ErrNoRows) {
			return g, false, nil
		}
		if err != nil {
			return g, false, err
		}
		kind = "channel"
	}
	var sample, title sql.NullString
	if kind == "playlist" {
		_ = h.DB.QueryRowContext(ctx, `SELECT p.title, (SELECT m.external_id FROM media_metadata_enrichment m
			  JOIN videos v ON v.id = m.video_id JOIN youtube_playlist_items i ON i.youtube_id = m.external_id
			 WHERE m.provider = 'youtube' AND v.library_id = $2 AND i.playlist_id = p.playlist_id
			 ORDER BY m.external_id LIMIT 1)
			  FROM youtube_playlists p WHERE p.playlist_id = $1`, ext, lib).Scan(&title, &sample)
	} else {
		_ = h.DB.QueryRowContext(ctx, `SELECT (SELECT title FROM youtube_channels WHERE channel_id = $1),
			(SELECT m.external_id FROM media_metadata_enrichment m JOIN videos v ON v.id = m.video_id
			   JOIN youtube_videos y ON y.youtube_id = m.external_id
			  WHERE m.provider = 'youtube' AND v.library_id = $2 AND y.channel_id = $1
			  ORDER BY m.external_id LIMIT 1)`, ext, lib).Scan(&title, &sample)
	}
	url := "https://www.youtube.com/channel/" + ext
	if kind == "playlist" {
		url = "https://www.youtube.com/playlist?list=" + ext
	}
	evidence := "filename"
	if 2*fromSidecar > linked {
		evidence = "info_json"
	}
	g.Kind, g.ExternalID, g.URL, g.Evidence = &kind, &ext, &url, &evidence
	g.Title, g.SampleYouTubeID = nonEmpty(title.String), nonEmpty(sample.String)
	g.IDsFound, g.IDsMatched = linked, matched
	return g, true, nil
}

// guessFor looks at library lib's linked videos, else its files.
func (h *Handler) guessFor(ctx context.Context, lib string) (SourceGuess, error) {
	if g, ok, err := h.linkedGuess(ctx, lib); err != nil || ok {
		return g, err
	}
	g := SourceGuess{LibraryID: lib, FollowedBy: []LibraryName{}}
	rows, err := h.DB.QueryContext(ctx, `SELECT path FROM videos WHERE library_id = $1 AND deleted_at IS NULL
		ORDER BY path LIMIT 20000`, lib)
	if err != nil {
		return g, err
	}
	var paths, ids []string
	seen := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return g, err
		}
		paths = append(paths, p)
		if m := fileIDRe.FindStringSubmatch(path.Base(p)); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return g, err
	}

	votes := map[string]*vote{}
	add := func(kind, id, title, sample string) {
		v := votes[kind+"/"+id]
		if v == nil {
			v = &vote{kind: kind, id: id, title: title, sample: sample}
			votes[kind+"/"+id] = v
		}
		v.n++
		if v.title == "" {
			v.title = title
		}
	}
	// a sidecar names its channel itself
	for _, s := range h.readSidecars(paths) {
		if s.ID != "" && !seen[s.ID] {
			seen[s.ID] = true
			ids = append(ids, s.ID)
		}
		switch {
		case ytChannelRe.MatchString(s.ChannelID):
			add("channel", s.ChannelID, firstNonEmpty(s.Channel, s.Uploader), s.ID)
		case ytPlaylist.MatchString(s.PlaylistID):
			add("playlist", s.PlaylistID, s.PlaylistTitle, s.ID)
		}
	}
	g.IDsFound = len(ids)
	evidence := "info_json"
	if len(votes) == 0 && len(ids) > 0 {
		evidence = "filename"
		r, err := h.DB.QueryContext(ctx, `SELECT y.youtube_id, y.channel_id, COALESCE(c.title, '')
			  FROM youtube_videos y LEFT JOIN youtube_channels c ON c.channel_id = y.channel_id
			 WHERE y.youtube_id = ANY($1) AND y.channel_id IS NOT NULL`, pq.Array(ids))
		if err != nil {
			return g, err
		}
		for r.Next() {
			var yid, cid, title string
			if err := r.Scan(&yid, &cid, &title); err != nil {
				r.Close()
				return g, err
			}
			if ytChannelRe.MatchString(cid) {
				add("channel", cid, title, yid)
			}
		}
		r.Close()
		if err := r.Err(); err != nil {
			return g, err
		}
	}
	if len(votes) > 0 {
		all := make([]*vote, 0, len(votes))
		for _, v := range votes {
			all = append(all, v)
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].n != all[j].n {
				return all[i].n > all[j].n
			}
			return all[i].id < all[j].id
		})
		best := all[0]
		url := "https://www.youtube.com/channel/" + best.id
		if best.kind == "playlist" {
			url = "https://www.youtube.com/playlist?list=" + best.id
		}
		if best.title == "" && best.kind == "channel" {
			var t sql.NullString
			_ = h.DB.QueryRowContext(ctx, `SELECT title FROM youtube_channels WHERE channel_id = $1`, best.id).Scan(&t)
			best.title = t.String
		}
		g.Kind, g.ExternalID, g.URL = &best.kind, &best.id, &url
		g.Evidence, g.SampleYouTubeID, g.IDsMatched = &evidence, nonEmpty(best.sample), best.n
		g.Title = nonEmpty(best.title)
	}
	return g, nil
}

// local is p on this machine.
func (h *Handler) local(p string) string {
	if h.LocalPath == nil {
		return p
	}
	return h.LocalPath(p)
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// GuessSource implements POST /api/libraries/{id}/youtube/guess.
func (h *Handler) GuessSource(w http.ResponseWriter, r *http.Request) {
	id, ok := libraryParam(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.libraryExists(ctx, id); errors.Is(err, errLibraryNotFound) {
		httperror.Write(w, r, httperror.NotFound("library not found"))
		return
	} else if err != nil {
		writeErr(w, r, "library", err)
		return
	}
	g, err := h.guessFor(ctx, id)
	if err != nil {
		writeErr(w, r, "guess", err)
		return
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		writeErr(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	err = tx.QueryRowContext(ctx, `INSERT INTO youtube_source_guesses (library_id, kind, external_id, url, title,
		evidence, sample_youtube_id, ids_found, ids_matched, looked_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		ON CONFLICT (library_id) DO UPDATE SET kind = EXCLUDED.kind, external_id = EXCLUDED.external_id,
		       url = EXCLUDED.url, title = EXCLUDED.title, evidence = EXCLUDED.evidence,
		       sample_youtube_id = EXCLUDED.sample_youtube_id, ids_found = EXCLUDED.ids_found,
		       ids_matched = EXCLUDED.ids_matched, looked_at = EXCLUDED.looked_at
		RETURNING looked_at`, id, g.Kind, g.ExternalID, g.URL, g.Title, g.Evidence, g.SampleYouTubeID,
		g.IDsFound, g.IDsMatched).Scan(&g.LookedAt)
	if err != nil {
		writeErr(w, r, "save guess", err)
		return
	}
	if err := auditAdmin(ctx, tx, "queue.youtube_source.guess", "library", id, map[string]any{
		"external_id": g.ExternalID, "ids_found": g.IDsFound, "ids_matched": g.IDsMatched}); err != nil {
		writeErr(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeErr(w, r, "commit", err)
		return
	}
	if g.ExternalID != nil {
		rows, err := h.DB.QueryContext(ctx, `SELECT DISTINCT l.id::text, l.name FROM youtube_sources s
			JOIN libraries l ON l.id = s.library_id WHERE s.external_id = $1 ORDER BY l.name`, *g.ExternalID)
		if err != nil {
			writeErr(w, r, "followers", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var n LibraryName
			if err := rows.Scan(&n.ID, &n.Name); err != nil {
				writeErr(w, r, "followers", err)
				return
			}
			g.FollowedBy = append(g.FollowedBy, n)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, g)
}
