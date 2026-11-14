package broadcast

// Publishable sources (Epic 50 story 50.23): what a published channel may air.
//
// An item is publishable when the home's admins say so: it is their own recording, a
// one-off item they cleared with a proof link, a YouTube video under Creative Commons
// Attribution (found at intake), or it comes from a source they recorded a permission for
// (a YouTube channel or playlist, a speaker, a label). The rule itself is slot 0227's SQL
// functions, so the scheduler, the channel's player and this page all read the same one.
// Nothing here is sent to Maktaba Cloud.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Reasons an item is publishable (video_publishable_reason).
const (
	ReasonOwn        = "own"
	ReasonCleared    = "cleared"
	ReasonCCBy       = "cc_by"
	ReasonPermission = "source_permission"
)

// Permission kinds a permission names.
const (
	KindChannel  = "channel"
	KindPlaylist = "playlist"
	KindSpeaker  = "speaker"
	KindLabel    = "label"
)

// ShareDays is the window the Broadcast page's publishable share covers.
const ShareDays = 7

// maxSuggestions is how many sources a channel's share suggests.
const maxSuggestions = 5

// PublishableOnlyKey is the app_settings key of broadcast.publishable_only (absent: on).
const PublishableOnlyKey = "broadcast.publishable_only"

// confirmOff are the phrases an admin types to turn publishable_only off (either language).
var confirmOff = map[string]bool{"turn off": true, "إيقاف": true}

// Permission is one recorded permission for a source.
type Permission struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Ref           string     `json:"ref"`
	Label         string     `json:"label"`
	ProofURL      string     `json:"proof_url"`
	ProofFile     string     `json:"proof_file,omitempty"`
	Note          string     `json:"note"`
	Credit        string     `json:"credit"`
	NonCommercial bool       `json:"non_commercial"`
	RecordedBy    string     `json:"recorded_by"`
	RecordedAt    time.Time  `json:"recorded_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
	RevokedReason string     `json:"revoked_reason,omitempty"`
}

// PermissionRequest is POST /api/tv/broadcast/publishable/sources.
type PermissionRequest struct {
	Kind          string `json:"kind"`
	Ref           string `json:"ref"`
	Label         string `json:"label"`
	ProofURL      string `json:"proof_url"`
	ProofFile     string `json:"proof_file"`
	Note          string `json:"note"`
	Credit        string `json:"credit"`
	NonCommercial bool   `json:"non_commercial"`
}

// MarkRequest is PUT /api/tv/broadcast/publishable/items: the items (by id, or every item
// of a folder, series or collection) and the mark (own, cleared, or none to clear it).
type MarkRequest struct {
	Reason       string   `json:"reason"`
	VideoIDs     []string `json:"video_ids"`
	LibraryID    string   `json:"library_id"`
	Folder       string   `json:"folder"`
	SeriesID     string   `json:"series_id"`
	CollectionID string   `json:"collection_id"`
	ProofURL     string   `json:"proof_url"`
	Note         string   `json:"note"`
}

// Suggestion is a source that would add the most publishable hours if it were marked.
type Suggestion struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Seconds int    `json:"seconds"`
	// URL is where its permission is usually written (a YouTube channel's About page).
	URL string `json:"url,omitempty"`
}

// Share is a channel's publishable share of the next ShareDays days.
type Share struct {
	ChannelID          string       `json:"channel_id"`
	ScheduledSeconds   int          `json:"scheduled_seconds"`
	PublishableSeconds int          `json:"publishable_seconds"`
	Percent            int          `json:"percent"`
	Suggestions        []Suggestion `json:"suggestions"`
}

// PublishableView is GET /api/tv/broadcast/publishable.
type PublishableView struct {
	PublishableOnly bool         `json:"publishable_only"`
	Sources         []Permission `json:"sources"`
	Shares          []Share      `json:"shares"`
}

// MarkedPublishable counts only what slot 0227's rule says may air: check 3 of a publish.
type MarkedPublishable struct{}

// Seconds is the channel's scheduled time inside [from, to) whose item is publishable.
func (MarkedPublishable) Seconds(ctx context.Context, db *sql.DB, channelID string, from, to time.Time) (int, error) {
	var secs float64
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(EXTRACT(EPOCH FROM (LEAST(end_at, $3) - GREATEST(start_at, $2)))), 0)
		  FROM channel_programs_live
		 WHERE channel_id::text = $1 AND end_at > $2 AND start_at < $3 AND kind IN ('program', 'filler')
		   AND video_id IS NOT NULL AND video_publishable_reason(video_id) IS NOT NULL`,
		channelID, from, to).Scan(&secs)
	return int(secs), err
}

// PublishableOnly is broadcast.publishable_only (absent: on).
func (s *Store) PublishableOnly(ctx context.Context) bool {
	var raw string
	if err := s.DB.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = $1`, PublishableOnlyKey).Scan(&raw); err != nil {
		return true
	}
	return strings.TrimSpace(raw) != "false"
}

// SetPublishableOnly writes broadcast.publishable_only. Turning it off takes a typed
// confirmation ("turn off", or its Arabic); turning it on takes none.
func (s *Service) SetPublishableOnly(ctx context.Context, on bool, confirm string) (bool, error) {
	if !on && !confirmOff[strings.ToLower(strings.TrimSpace(confirm))] {
		return true, problem(http.StatusBadRequest, "", "confirmOff", nil)
	}
	v := "true"
	if !on {
		v = "false"
	}
	if _, err := s.Store.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, $2::jsonb)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, PublishableOnlyKey, v); err != nil {
		return !on, err
	}
	s.log().Info("broadcast: publishable_only changed", "on", on, "event", "broadcast_publishable_only")
	return on, nil
}

const sourceCols = `id::text, kind, ref, label, proof_url, COALESCE(proof_file, ''), note, credit, non_commercial,
	recorded_by, recorded_at, revoked_at, COALESCE(revoked_reason, '')`

func scanSource(r scanner) (Permission, error) {
	var src Permission
	err := r.Scan(&src.ID, &src.Kind, &src.Ref, &src.Label, &src.ProofURL, &src.ProofFile, &src.Note, &src.Credit,
		&src.NonCommercial, &src.RecordedBy, &src.RecordedAt, &src.RevokedAt, &src.RevokedReason)
	return src, err
}

// Sources are the recorded permissions, standing ones first, newest first.
func (s *Store) Sources(ctx context.Context) ([]Permission, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sourceCols+` FROM publishable_sources
		 ORDER BY revoked_at IS NOT NULL, recorded_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Permission{}
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func validProof(u string) bool {
	p, err := url.Parse(strings.TrimSpace(u))
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

// RecordSource stores a permission for a source. A source already standing answers
// 409 sourceRecorded (withdraw it first to record it again).
func (s *Service) RecordSource(ctx context.Context, req PermissionRequest, by Admin) (Permission, error) {
	req.Ref = strings.TrimSpace(req.Ref)
	switch {
	case req.Kind != KindChannel && req.Kind != KindPlaylist && req.Kind != KindSpeaker && req.Kind != KindLabel:
		return Permission{}, problem(http.StatusBadRequest, "", "badSourceKind", nil)
	case req.Ref == "" || len(req.Ref) > 200:
		return Permission{}, problem(http.StatusBadRequest, "", "badSourceRef", nil)
	case !validProof(req.ProofURL):
		return Permission{}, problem(http.StatusBadRequest, "", "badProof", nil)
	case len(req.Note) > 2000 || len(req.Credit) > 300 || len(req.Label) > 200 || len(req.ProofFile) > 500:
		return Permission{}, problem(http.StatusBadRequest, "", "tooLong", nil)
	}
	var uid any
	if by.UserID != "" {
		uid = by.UserID
	}
	src, err := scanSource(s.Store.DB.QueryRowContext(ctx, `
		INSERT INTO publishable_sources (kind, ref, label, proof_url, proof_file, note, credit, non_commercial,
		                                 recorded_by, recorded_by_user)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, (SELECT id FROM users WHERE id::text = $10))
		RETURNING `+sourceCols,
		req.Kind, req.Ref, strings.TrimSpace(req.Label), strings.TrimSpace(req.ProofURL), req.ProofFile,
		strings.TrimSpace(req.Note), strings.TrimSpace(req.Credit), req.NonCommercial, by.Name, uid))
	if err != nil {
		if strings.Contains(err.Error(), "publishable_sources_standing_idx") {
			return Permission{}, problem(http.StatusConflict, "", "sourceRecorded", nil)
		}
		return Permission{}, err
	}
	s.log().Info("broadcast: source permission recorded", "kind", src.Kind, "source_id", src.ID,
		"event", "broadcast_source_recorded")
	return src, nil
}

// ErrNoSource is a permission that does not exist or is already withdrawn.
var ErrNoSource = errors.New("broadcast: no such standing permission")

// WithdrawSource withdraws a permission at once: its items stop being publishable, the
// channel's player slates any already scheduled, and the next top-up replaces them.
func (s *Service) WithdrawSource(ctx context.Context, id, reason string) (Permission, error) {
	if len(reason) > 500 {
		return Permission{}, problem(http.StatusBadRequest, "", "tooLong", nil)
	}
	src, err := scanSource(s.Store.DB.QueryRowContext(ctx, `UPDATE publishable_sources
		   SET revoked_at = now(), revoked_reason = $2
		 WHERE id::text = $1 AND revoked_at IS NULL
		 RETURNING `+sourceCols, id, strings.TrimSpace(reason)))
	if errors.Is(err, sql.ErrNoRows) {
		return Permission{}, ErrNoSource
	}
	if err != nil {
		return Permission{}, err
	}
	s.log().Info("broadcast: source permission withdrawn", "source_id", id, "event", "broadcast_source_withdrawn")
	return src, nil
}

// MarkItems sets (or, with reason "none", clears) the admin's mark on items: by id, or
// every item of a folder (library and path prefix), a series or a collection. It answers
// how many items changed.
func (s *Service) MarkItems(ctx context.Context, req MarkRequest, by Admin) (int, error) {
	var reason any
	switch req.Reason {
	case ReasonOwn:
		reason = ReasonOwn
	case ReasonCleared:
		// a one-off item: its proof is the point (§1)
		if len(req.VideoIDs) != 1 || !validProof(req.ProofURL) {
			return 0, problem(http.StatusBadRequest, "", "badProof", nil)
		}
		reason = ReasonCleared
	case "none":
	default:
		return 0, problem(http.StatusBadRequest, "", "badReason", nil)
	}
	var proof any
	if reason == ReasonCleared {
		b, _ := json.Marshal(map[string]string{"proof_url": strings.TrimSpace(req.ProofURL), "note": strings.TrimSpace(req.Note)})
		proof = string(b)
	}
	var who any
	if reason != nil {
		who = by.Name
	}
	set := `UPDATE videos v SET publishable_reason = $1, publishable_proof = $2::jsonb, publishable_by = $3,
		       publishable_at = CASE WHEN $1::text IS NULL THEN NULL ELSE now() END`
	var res sql.Result
	var err error
	switch {
	case len(req.VideoIDs) > 0:
		if len(req.VideoIDs) > 5000 {
			return 0, problem(http.StatusBadRequest, "", "tooMany", nil)
		}
		ids, _ := json.Marshal(req.VideoIDs)
		res, err = s.Store.DB.ExecContext(ctx, set+` WHERE v.id::text IN (SELECT jsonb_array_elements_text($4::jsonb))
			   AND v.deleted_at IS NULL`, reason, proof, who, string(ids))
	case req.LibraryID != "" && req.Reason != ReasonCleared:
		prefix := strings.TrimRight(req.Folder, "/")
		res, err = s.Store.DB.ExecContext(ctx, set+` WHERE v.library_id::text = $4 AND v.deleted_at IS NULL
			   AND ($5 = '' OR v.path = $5 OR left(v.path, length($5) + 1) = $5 || '/')`, reason, proof, who, req.LibraryID, prefix)
	case req.SeriesID != "" && req.Reason != ReasonCleared:
		res, err = s.Store.DB.ExecContext(ctx, set+` FROM series_episodes se
			 WHERE se.video_id = v.id AND se.series_id::text = $4 AND v.deleted_at IS NULL`, reason, proof, who, req.SeriesID)
	case req.CollectionID != "" && req.Reason != ReasonCleared:
		res, err = s.Store.DB.ExecContext(ctx, set+` FROM collection_items ci
			 WHERE ci.video_id = v.id AND ci.collection_id::text = $4 AND v.deleted_at IS NULL`, reason, proof, who, req.CollectionID)
	default:
		return 0, problem(http.StatusBadRequest, "", "noItems", nil)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	s.log().Info("broadcast: items marked", "reason", req.Reason, "count", n, "event", "broadcast_items_marked")
	return int(n), nil
}

// ItemReason is one item's publishable reason ("" when it is not publishable).
func (s *Store) ItemReason(ctx context.Context, videoID string) (string, error) {
	var r sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT video_publishable_reason(id) FROM videos WHERE id::text = $1`, videoID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return r.String, err
}

// ChannelShare is a channel's publishable share of the next ShareDays days, and the
// sources that would add the most hours if marked: YouTube channels (with their About
// page) and, for items with no YouTube source, their library (a reciter's or a speaker's).
func (s *Store) ChannelShare(ctx context.Context, channelID string, now time.Time) (Share, error) {
	to := now.Add(ShareDays * 24 * time.Hour)
	sh := Share{ChannelID: channelID, Suggestions: []Suggestion{}}
	var total, ok float64
	if err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(sum(secs), 0), COALESCE(sum(secs) FILTER (WHERE video_publishable_reason(video_id) IS NOT NULL), 0)
		  FROM (SELECT video_id, EXTRACT(EPOCH FROM (LEAST(end_at, $3) - GREATEST(start_at, $2))) AS secs
		          FROM channel_programs_live
		         WHERE channel_id::text = $1 AND end_at > $2 AND start_at < $3
		           AND kind IN ('program', 'filler') AND video_id IS NOT NULL) p`,
		channelID, now, to).Scan(&total, &ok); err != nil {
		return sh, err
	}
	sh.ScheduledSeconds, sh.PublishableSeconds = int(total), int(ok)
	if total > 0 {
		sh.Percent = int(ok * 100 / total)
	}
	rows, err := s.DB.QueryContext(ctx, `
		WITH p AS (
		    SELECT cp.video_id, video_youtube_id(cp.video_id) AS yid,
		           EXTRACT(EPOCH FROM (LEAST(cp.end_at, $3) - GREATEST(cp.start_at, $2))) AS secs
		      FROM channel_programs_live cp
		     WHERE cp.channel_id::text = $1 AND cp.end_at > $2 AND cp.start_at < $3
		       AND cp.kind IN ('program', 'filler') AND cp.video_id IS NOT NULL
		       AND video_publishable_reason(cp.video_id) IS NULL
		)
		SELECT 'channel', y.channel_id, COALESCE(c.title, y.channel_id), sum(p.secs)::int
		  FROM p JOIN youtube_videos y ON y.youtube_id = p.yid
		  LEFT JOIN youtube_channels c ON c.channel_id = y.channel_id
		 WHERE y.channel_id IS NOT NULL AND y.channel_id <> ''
		 GROUP BY y.channel_id, c.title
		UNION ALL
		SELECT 'speaker', l.name, l.name, sum(p.secs)::int
		  FROM p JOIN videos v ON v.id = p.video_id JOIN libraries l ON l.id = v.library_id
		 WHERE p.yid IS NULL
		 GROUP BY l.name
		 ORDER BY 4 DESC, 2
		 LIMIT $4`, channelID, now, to, maxSuggestions)
	if err != nil {
		return sh, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sg Suggestion
		if err := rows.Scan(&sg.Kind, &sg.Ref, &sg.Label, &sg.Seconds); err != nil {
			return sh, err
		}
		if sg.Kind == KindChannel {
			sg.URL = "https://www.youtube.com/channel/" + url.PathEscape(sg.Ref) + "/about"
		}
		sh.Suggestions = append(sh.Suggestions, sg)
	}
	return sh, rows.Err()
}

// PublishableView gathers the page's publishable section: the switch, the permissions
// and each channel's share.
func (s *Service) PublishableView(ctx context.Context) (PublishableView, error) {
	v := PublishableView{PublishableOnly: s.Store.PublishableOnly(ctx), Shares: []Share{}}
	srcs, err := s.Store.Sources(ctx)
	if err != nil {
		return v, err
	}
	v.Sources = srcs
	chs, err := s.Store.List(ctx)
	if err != nil {
		return v, err
	}
	now := s.now()
	for _, c := range chs {
		if !c.Enabled {
			continue
		}
		sh, err := s.Store.ChannelShare(ctx, c.ID, now)
		if err != nil {
			return v, err
		}
		v.Shares = append(v.Shares, sh)
	}
	return v, nil
}
