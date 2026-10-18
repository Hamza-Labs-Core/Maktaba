package queues

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Explain answers "why is this video where it is": for every video whose
// title or file name matches the text it returns the air date and how sure
// it is, the length and shape, the playlists and positions, how it was
// linked to YouTube, its Plex item and the detector's verdict (episode N of
// a season, extra, second upload, short, in a clip group, a promo, or
// unassigned). Ported from the stand-alone control centre's /api/explain
// (scripts/mediaintel/explain.py) onto Maktaba's tables.

const (
	explainDefaultLimit = 30
	explainMaxLimit     = 100
	explainMinChars     = 2
)

// Verdict kinds.
const (
	VerdictEpisode    = "episode"
	VerdictExtra      = "extra"
	VerdictDuplicate  = "duplicate"
	VerdictShort      = "short"
	VerdictClipGroup  = "clip_group"
	VerdictPromo      = "promo"
	VerdictUnassigned = "unassigned"
)

// ExplainVerdict is where the detector put a video.
type ExplainVerdict struct {
	Kind     string  `json:"kind"`
	Label    string  `json:"label"`
	SeasonID *string `json:"season_id"`
	SeriesID *string `json:"series_id"`
	Season   *int    `json:"season"`
	Episode  *int    `json:"episode"`
	Attached bool    `json:"attached"`
}

// ExplainPlaylist is one real YouTube playlist a video is in.
type ExplainPlaylist struct {
	Title    string `json:"title"`
	Position *int   `json:"position"`
}

// ExplainVideo is one downloaded video.
type ExplainVideo struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	File        string            `json:"file"`
	LibraryID   string            `json:"library_id"`
	Library     string            `json:"library"`
	Aired       *string           `json:"aired"`
	DateQuality *string           `json:"date_quality"`
	DurationSec *float64          `json:"duration_sec"`
	Width       *int              `json:"width"`
	Height      *int              `json:"height"`
	YouTubeID   *string           `json:"youtube_id"`
	LinkMethod  *string           `json:"link_method"`
	Tab         *string           `json:"tab"`
	Playlists   []ExplainPlaylist `json:"playlists"`
	PlexKey     *string           `json:"plex_rating_key"`
	Verdict     ExplainVerdict    `json:"verdict"`
}

// ExplainRemote is a YouTube video of the channel that is not downloaded.
type ExplainRemote struct {
	YouTubeID   string            `json:"youtube_id"`
	Title       string            `json:"title"`
	Aired       *string           `json:"aired"`
	DateQuality *string           `json:"date_quality"`
	DurationSec *float64          `json:"duration_sec"`
	Tab         *string           `json:"tab"`
	Playlists   []ExplainPlaylist `json:"playlists"`
}

// ExplainResponse is the GET /api/explain body.
type ExplainResponse struct {
	Query  string          `json:"q"`
	Videos []ExplainVideo  `json:"videos"`
	Remote []ExplainRemote `json:"remote"`
	Limit  int             `json:"limit"`
}

const explainVideosSQL = `
SELECT v.id::text, COALESCE(yv.title, v.title, v.filename), v.filename, v.library_id::text, l.name,
       yv.upload_date::text, yv.date_quality, COALESCE(v.duration_sec, yv.duration_s),
       v.metadata, m.external_id, m.mapped->>'link_method', yv.tab, el.external_id
  FROM videos v
  JOIN libraries l ON l.id = v.library_id
  LEFT JOIN LATERAL (
        SELECT external_id, mapped FROM media_metadata_enrichment
         WHERE video_id = v.id AND provider = 'youtube'
         ORDER BY confidence DESC LIMIT 1) m ON true
  LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id
  LEFT JOIN external_links el ON el.target = 'plex_item' AND el.subject_kind = 'video'
                              AND el.subject_id = v.id::text
 WHERE v.deleted_at IS NULL
   AND (v.filename ILIKE $1 ESCAPE '\' OR v.title ILIKE $1 ESCAPE '\' OR yv.title ILIKE $1 ESCAPE '\')
   AND ($2::uuid IS NULL OR v.library_id = $2::uuid)
 ORDER BY yv.upload_date NULLS LAST, v.filename
 LIMIT $3`

const explainRemoteSQL = `
SELECT y.youtube_id, y.title, y.upload_date::text, y.date_quality, y.duration_s, y.tab
  FROM youtube_videos y
 WHERE y.title ILIKE $1 ESCAPE '\'
   AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment m
                    WHERE m.provider = 'youtube' AND m.external_id = y.youtube_id)
   AND ($2::uuid IS NULL OR y.channel_id IN
        (SELECT channel_id FROM youtube_channels WHERE library_id = $2::uuid))
 ORDER BY y.upload_date NULLS LAST
 LIMIT $3`

const explainPlaylistsSQL = `
SELECT p.title, i.position
  FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id
 WHERE i.youtube_id = $1 AND p.is_real
 ORDER BY p.title`

const explainEpisodeSQL = `
SELECT ss.id::text, ss.series_id::text, ss.name, ss.season, COALESCE(e.episode_override, e.episode),
       COALESCE(ss.metadata->'attached' @> to_jsonb($1::text), false)
  FROM series_episodes e
  JOIN series_seasons ss ON ss.series_id = e.series_id
   AND ss.season = COALESCE(e.season_override, e.season) AND ss.retired_at IS NULL
 WHERE e.video_id = $1::uuid
 LIMIT 1`

const explainHiddenSQL = `
SELECT ss.id::text, ss.series_id::text, ss.name, ss.season,
       CASE WHEN ss.metadata->'extras' @> to_jsonb($1::text) THEN 'extra' ELSE 'duplicate' END
  FROM series_seasons ss
 WHERE ss.retired_at IS NULL
   AND (ss.metadata->'extras' @> to_jsonb($1::text) OR ss.metadata->'duplicates' @> to_jsonb($1::text))
 LIMIT 1`

const explainClipGroupSQL = `
SELECT g.title FROM clip_group_items i JOIN clip_groups g ON g.id = i.group_id
 WHERE i.video_id = $1::uuid LIMIT 1`

const explainPromoSQL = `
SELECT COALESCE(s.name_override, s.name) FROM promo_links p JOIN series s ON s.id = p.series_id
 WHERE p.video_id = $1::uuid LIMIT 1`

// likePattern wraps text for ILIKE ... ESCAPE '\' so %, _ and \ in the
// search text match themselves.
func likePattern(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func nullFloat(f sql.NullFloat64) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// Explain implements GET /api/explain?q=&library_id=&limit=.
func (h *Handler) Explain(w http.ResponseWriter, r *http.Request) {
	if !requireAuth(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < explainMinChars {
		httperror.Write(w, r, httperror.BadRequest("q must be at least 2 characters"))
		return
	}
	var lib any
	if v := r.URL.Query().Get("library_id"); v != "" {
		if !isUUID(v) {
			httperror.Write(w, r, httperror.InvalidQuery("library_id must be a uuid"))
			return
		}
		lib = v
	}
	limit, e := common.QueryInt(r, "limit", explainDefaultLimit)
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > explainMaxLimit {
		limit = explainMaxLimit
	}
	out, err := h.explain(r.Context(), q, lib, limit)
	if err != nil {
		writeErr(w, r, "explain", err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) explain(ctx context.Context, q string, lib any, limit int) (ExplainResponse, error) {
	like := likePattern(q)
	out := ExplainResponse{Query: q, Videos: []ExplainVideo{}, Remote: []ExplainRemote{}, Limit: limit}

	rrows, err := h.DB.QueryContext(ctx, explainRemoteSQL, like, lib, limit)
	if err != nil {
		return out, err
	}
	for rrows.Next() {
		var x ExplainRemote
		var aired, dq, tab sql.NullString
		var dur sql.NullFloat64
		if err := rrows.Scan(&x.YouTubeID, &x.Title, &aired, &dq, &dur, &tab); err != nil {
			_ = rrows.Close()
			return out, err
		}
		x.Aired, x.DateQuality, x.Tab, x.DurationSec = nullStrPtr(aired), nullStrPtr(dq), nullStrPtr(tab), nullFloat(dur)
		out.Remote = append(out.Remote, x)
	}
	if err := rrows.Err(); err != nil {
		_ = rrows.Close()
		return out, err
	}
	_ = rrows.Close()
	for i := range out.Remote {
		if out.Remote[i].Playlists, err = h.explainPlaylists(ctx, out.Remote[i].YouTubeID); err != nil {
			return out, err
		}
	}

	rows, err := h.DB.QueryContext(ctx, explainVideosSQL, like, lib, limit)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v ExplainVideo
		var aired, dq, yt, method, tab, plex sql.NullString
		var dur sql.NullFloat64
		var meta []byte
		if err := rows.Scan(&v.ID, &v.Title, &v.File, &v.LibraryID, &v.Library, &aired, &dq, &dur,
			&meta, &yt, &method, &tab, &plex); err != nil {
			_ = rows.Close()
			return out, err
		}
		v.Aired, v.DateQuality, v.DurationSec = nullStrPtr(aired), nullStrPtr(dq), nullFloat(dur)
		v.YouTubeID, v.LinkMethod, v.Tab, v.PlexKey = nullStrPtr(yt), nullStrPtr(method), nullStrPtr(tab), nullStrPtr(plex)
		v.Width, v.Height = dimensions(meta)
		out.Videos = append(out.Videos, v)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	_ = rows.Close()
	for i := range out.Videos {
		v := &out.Videos[i]
		if v.YouTubeID != nil {
			if v.Playlists, err = h.explainPlaylists(ctx, *v.YouTubeID); err != nil {
				return out, err
			}
		} else {
			v.Playlists = []ExplainPlaylist{}
		}
		if v.Verdict, err = h.verdict(ctx, v); err != nil {
			return out, err
		}
	}
	return out, nil
}

// dimensions reads width and height from a video's metadata JSON.
func dimensions(meta []byte) (w, h *int) {
	var m struct {
		Width  *float64 `json:"width"`
		Height *float64 `json:"height"`
	}
	if len(meta) == 0 || json.Unmarshal(meta, &m) != nil {
		return nil, nil
	}
	toInt := func(f *float64) *int {
		if f == nil || *f <= 0 {
			return nil
		}
		v := int(*f)
		return &v
	}
	return toInt(m.Width), toInt(m.Height)
}

func (h *Handler) explainPlaylists(ctx context.Context, youtubeID string) ([]ExplainPlaylist, error) {
	rows, err := h.DB.QueryContext(ctx, explainPlaylistsSQL, youtubeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExplainPlaylist{}
	for rows.Next() {
		var p ExplainPlaylist
		var pos sql.NullInt64
		if err := rows.Scan(&p.Title, &pos); err != nil {
			return nil, err
		}
		p.Position = nullInt(pos)
		out = append(out, p)
	}
	return out, rows.Err()
}

// verdict decides where the detector put a video, in the order the
// stand-alone tool used: a season's episode, an extra or second upload of
// one, a short, then (Maktaba) a clip group or a promo link; else
// unassigned (a stand-alone programme).
func (h *Handler) verdict(ctx context.Context, v *ExplainVideo) (ExplainVerdict, error) {
	var seasonID, seriesID, name string
	var season int
	var ep sql.NullInt64
	var attached bool
	err := h.DB.QueryRowContext(ctx, explainEpisodeSQL, v.ID).Scan(&seasonID, &seriesID, &name, &season, &ep, &attached)
	if err == nil {
		out := ExplainVerdict{Kind: VerdictEpisode, SeasonID: &seasonID, SeriesID: &seriesID, Season: &season, Episode: nullInt(ep), Attached: attached}
		out.Label = seasonLabel(name, season) + episodeSuffix(out.Episode)
		if attached {
			out.Label += " (attached)"
		}
		return out, nil
	}
	if err != sql.ErrNoRows {
		return ExplainVerdict{}, err
	}

	var kind string
	err = h.DB.QueryRowContext(ctx, explainHiddenSQL, v.ID).Scan(&seasonID, &seriesID, &name, &season, &kind)
	if err == nil {
		out := ExplainVerdict{Kind: VerdictExtra, SeasonID: &seasonID, SeriesID: &seriesID, Season: &season}
		out.Label = "extra (promo/recap) of " + seasonLabel(name, season)
		if kind == "duplicate" {
			out.Kind = VerdictDuplicate
			out.Label = "second upload of an episode in " + seasonLabel(name, season)
		}
		return out, nil
	}
	if err != sql.ErrNoRows {
		return ExplainVerdict{}, err
	}

	if (v.Tab != nil && *v.Tab == "shorts") || (v.Width != nil && v.Height != nil && *v.Height > *v.Width) {
		return ExplainVerdict{Kind: VerdictShort, Label: "short (never an episode)"}, nil
	}
	var title string
	err = h.DB.QueryRowContext(ctx, explainClipGroupSQL, v.ID).Scan(&title)
	if err == nil {
		return ExplainVerdict{Kind: VerdictClipGroup, Label: "clip in the group " + title}, nil
	}
	if err != sql.ErrNoRows {
		return ExplainVerdict{}, err
	}
	err = h.DB.QueryRowContext(ctx, explainPromoSQL, v.ID).Scan(&title)
	if err == nil {
		return ExplainVerdict{Kind: VerdictPromo, Label: "promo of " + title}, nil
	}
	if err != sql.ErrNoRows {
		return ExplainVerdict{}, err
	}
	return ExplainVerdict{Kind: VerdictUnassigned, Label: "unassigned (stand-alone)"}, nil
}

func seasonLabel(name string, season int) string {
	return name + " S" + itoa(season)
}

func episodeSuffix(ep *int) string {
	if ep == nil {
		return ""
	}
	return " E" + itoa(*ep)
}
