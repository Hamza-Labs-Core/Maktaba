package graphql

// DataLoader-style batching for the nested video fields. Every list of
// videos a resolver returns (a page of videos, a collection's items, a
// single video) shares one videoBatch; the first time any of its videos
// asks for, say, audioTracks, the batch reads the audio tracks of all of
// them in one query (videos.AudioTracksByVideo) and every sibling reads
// from that result. So a page of N videos costs one query per selected
// nested field, never N. The readers are the ones the REST video detail,
// subtitles and chapters routes use; a video only reaches a batch after
// its REST route (or videos.CanRead) let the caller read it.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	gql "github.com/graph-gophers/graphql-go"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
)

// lazy runs its loader once and shares the result.
type lazy[T any] struct {
	once sync.Once
	v    T
	err  error
}

func (l *lazy[T]) get(f func() (T, error)) (T, error) {
	l.once.Do(func() { l.v, l.err = f() })
	return l.v, l.err
}

// videoBatch is one list of videos and its lazily loaded joins.
type videoBatch struct {
	rs     *Resolvers
	db     *sql.DB
	ids    []string
	userID string
	// loads counts the batch queries run (tests pin the N+1 guarantee).
	loads *atomic.Int64

	tags       lazy[map[string][]videos.TagRef]
	transcript lazy[map[string]videos.TranscriptRef]
	playback   lazy[map[string]videos.PlaybackState]
	subtitles  lazy[map[string][]videos.Subtitle]
	chapters   lazy[map[string][]videos.Chapter]
	media      lazy[map[string]videos.MediaInfo]
	audio      lazy[map[string][]videos.AudioTrack]
}

func (b *videoBatch) count() {
	if b.loads != nil {
		b.loads.Add(1)
	}
}

// videoNode is one Video.
type videoNode struct {
	v     videos.Video
	batch *videoBatch
}

// newVideoNodes wraps the REST videos into one batch.
func (rs *Resolvers) newVideoNodes(ctx context.Context, vs []videos.Video) []*videoNode {
	b := &videoBatch{rs: rs, db: rs.DB, loads: &rs.batchLoads}
	if p := principal.FromContext(ctx); p != nil {
		b.userID = p.UserID
	}
	out := make([]*videoNode, 0, len(vs))
	for _, v := range vs {
		b.ids = append(b.ids, v.ID)
		out = append(out, &videoNode{v: v, batch: b})
	}
	return out
}

// videosByID reads videos by id in one query (for resources that list
// video ids: collection items, recommendation cards), keeps the given
// order and drops the ids the caller may not read (videos.CanRead, the
// REST video detail's rule) or that no longer exist.
func (rs *Resolvers) videosByID(ctx context.Context, ids []string) ([]*videoNode, error) {
	if len(ids) == 0 {
		return []*videoNode{}, nil
	}
	rs.batchLoads.Add(1)
	byID, err := videos.LoadVideos(ctx, rs.DB, ids)
	if err != nil {
		return nil, err
	}
	p := principal.FromContext(ctx)
	vs := make([]videos.Video, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		v, ok := byID[id]
		if !ok || seen[id] || !videos.CanRead(p, v.LibraryID) {
			continue
		}
		seen[id] = true
		vs = append(vs, v)
	}
	return rs.newVideoNodes(ctx, vs), nil
}

func (n *videoNode) ID() ID                    { return ID(n.v.ID) }
func (n *videoNode) LibraryID() ID             { return ID(n.v.LibraryID) }
func (n *videoNode) Path() string              { return n.v.Path }
func (n *videoNode) Filename() string          { return n.v.Filename }
func (n *videoNode) Title() *string            { return n.v.Title }
func (n *videoNode) Description() *string      { return n.v.Description }
func (n *videoNode) State() string             { return n.v.State }
func (n *videoNode) DetectedLanguage() *string { return n.v.DetectedLang }
func (n *videoNode) DurationSec() *float64     { return n.v.DurationSec }
func (n *videoNode) SizeBytes() float64        { return float64(n.v.SizeBytes) }
func (n *videoNode) ContentHash() string       { return n.v.ContentHash }
func (n *videoNode) CreatedAt() gql.Time       { return gql.Time{Time: n.v.CreatedAt} }
func (n *videoNode) UpdatedAt() gql.Time       { return gql.Time{Time: n.v.UpdatedAt} }

func (n *videoNode) Metadata() *JSON {
	if len(n.v.Metadata) == 0 {
		return nil
	}
	var j JSON
	if err := json.Unmarshal(n.v.Metadata, &j.V); err != nil {
		return nil
	}
	return &j
}

func (n *videoNode) Tags(ctx context.Context) ([]*tag, error) {
	b := n.batch
	m, err := b.tags.get(func() (map[string][]videos.TagRef, error) {
		b.count()
		return videos.TagsByVideo(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	out := []*tag{}
	for _, t := range m[n.v.ID] {
		out = append(out, &tag{ID: ID(t.ID), Name: t.Name})
	}
	return out, nil
}

func (n *videoNode) PlaybackState(ctx context.Context) (*playbackState, error) {
	b := n.batch
	m, err := b.playback.get(func() (map[string]videos.PlaybackState, error) {
		b.count()
		return videos.PlaybackStates(ctx, b.db, b.userID, b.ids)
	})
	if err != nil {
		return nil, err
	}
	ps, ok := m[n.v.ID]
	if !ok {
		return nil, nil // not watched: null, not an error
	}
	return &playbackState{PositionSec: ps.PositionSec, Completed: ps.Completed, UpdatedAt: gql.Time{Time: ps.UpdatedAt}}, nil
}

func (n *videoNode) Subtitles(ctx context.Context) ([]*subtitle, error) {
	b := n.batch
	m, err := b.subtitles.get(func() (map[string][]videos.Subtitle, error) {
		b.count()
		return videos.SubtitlesByVideo(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	out := []*subtitle{}
	for _, s := range m[n.v.ID] {
		out = append(out, &subtitle{ID: ID(itoa64(s.ID)), Language: s.Language, Format: s.Format,
			Source: s.Source, IsDefault: s.IsDefault, URL: s.URL})
	}
	return out, nil
}

func (n *videoNode) Chapters(ctx context.Context) ([]*chapter, error) {
	b := n.batch
	m, err := b.chapters.get(func() (map[string][]videos.Chapter, error) {
		b.count()
		return videos.ChaptersByVideo(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	out := []*chapter{}
	for _, c := range m[n.v.ID] {
		out = append(out, &chapter{Seq: int32(c.Seq), StartSec: c.StartSec, EndSec: c.EndSec, Title: c.Title, Source: c.Source})
	}
	return out, nil
}

func (n *videoNode) MediaInfo(ctx context.Context) (*mediaInfo, error) {
	b := n.batch
	m, err := b.media.get(func() (map[string]videos.MediaInfo, error) {
		b.count()
		return videos.MediaInfoByVideo(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	mi, ok := m[n.v.ID]
	if !ok {
		return nil, nil // not probed yet
	}
	return &mediaInfo{Container: mi.Container, DurationSec: n.v.DurationSec, Width: i32(mi.Width),
		Height: i32(mi.Height), FPS: mi.FPS, BitrateKbps: i32(mi.BitrateKbps)}, nil
}

func (n *videoNode) AudioTracks(ctx context.Context) ([]*audioTrack, error) {
	b := n.batch
	m, err := b.audio.get(func() (map[string][]videos.AudioTrack, error) {
		b.count()
		return videos.AudioTracksByVideo(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	out := []*audioTrack{}
	for _, a := range m[n.v.ID] {
		out = append(out, &audioTrack{ID: int32(a.ID), Language: a.Language, Codec: a.Codec,
			Channels: i32(a.Channels), Default: a.Default})
	}
	return out, nil
}

func (n *videoNode) LatestTranscript(ctx context.Context) (*transcriptNode, error) {
	b := n.batch
	m, err := b.transcript.get(func() (map[string]videos.TranscriptRef, error) {
		b.count()
		return videos.ActiveTranscripts(ctx, b.db, b.ids)
	})
	if err != nil {
		return nil, err
	}
	t, ok := m[n.v.ID]
	if !ok {
		return nil, nil
	}
	return &transcriptNode{ref: t, videoID: n.v.ID, rs: b.rs}, nil
}

// transcriptNode is a video's latest active transcript.
type transcriptNode struct {
	ref     videos.TranscriptRef
	videoID string
	rs      *Resolvers
}

func (t *transcriptNode) ID() ID              { return ID(t.ref.ID) }
func (t *transcriptNode) Language() *string   { return t.ref.Language }
func (t *transcriptNode) CreatedAt() gql.Time { return gql.Time{Time: t.ref.CreatedAt} }

// Segments resolves GET /api/videos/{id}/segments (the window query, its
// clamps, access check and RTL isolation are the REST route's). Word
// timings are asked for only when the selection reads them.
func (t *transcriptNode) Segments(ctx context.Context, args struct {
	From  *float64
	To    *float64
	First *int32
}) ([]*segment, error) {
	q := url.Values{}
	if args.From != nil {
		q.Set("from", strconv.FormatFloat(*args.From, 'f', -1, 64))
	}
	if args.To != nil {
		q.Set("to", strconv.FormatFloat(*args.To, 'f', -1, 64))
	}
	if args.First != nil {
		q.Set("limit", strconv.Itoa(int(*args.First)))
	}
	if gql.HasSelectedField(ctx, "words") {
		q.Set("words", "true")
	}
	var page struct {
		Items []*segment `json:"items"`
	}
	if _, err := t.rs.Bridge.json(ctx, call{method: http.MethodGet, path: "/api/videos/" + url.PathEscape(t.videoID) + "/segments", query: q}, &page); err != nil {
		return nil, err
	}
	if page.Items == nil {
		page.Items = []*segment{}
	}
	return page.Items, nil
}
