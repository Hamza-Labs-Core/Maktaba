package graphql

// Root resolvers: one method per Query / Mutation / Subscription field.
// Each runs the REST route named in its comment through the bridge and
// maps the REST body onto the schema type; no access decision, query or
// write happens here.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/recommendations"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// Resolvers holds what the resolvers need: the REST bridge, the app
// database (for the batch loaders of nested video fields only) and the
// event hub the subscriptions read.
type Resolvers struct {
	Bridge *Bridge
	DB     *sql.DB
	Hub    *ws.Hub

	// batchLoads counts batch-loader queries (tests).
	batchLoads atomic.Int64
}

// NewResolvers builds the resolvers over the API router (REST handlers),
// the app database and the event hub.
func NewResolvers(router http.Handler, db *sql.DB, hub *ws.Hub) *Resolvers {
	return &Resolvers{Bridge: &Bridge{Router: router}, DB: db, Hub: hub}
}

// root is the resolver graph-gophers binds Query, Mutation and
// Subscription to.
type root struct{ rs *Resolvers }

func (q *root) get(ctx context.Context, path string, query url.Values, out any) error {
	_, err := q.rs.Bridge.json(ctx, call{method: http.MethodGet, path: path, query: query}, out)
	return err
}

func (q *root) mutate(ctx context.Context, field, method, path string, query url.Values, body, out any) (*result, error) {
	return q.rs.Bridge.json(ctx, call{method: method, path: path, query: query, body: body, mutation: true, field: field}, out)
}

func seg(id ID) string { return url.PathEscape(string(id)) }

// ---------- Query ----------

// Libraries: GET /api/libraries.
func (q *root) Libraries(ctx context.Context) ([]*library, error) {
	var page struct {
		Items []*library `json:"items"`
	}
	if err := q.get(ctx, "/api/libraries", nil, &page); err != nil {
		return nil, err
	}
	return nonNil(page.Items), nil
}

// Library: GET /api/libraries/{id} (404 → null).
func (q *root) Library(ctx context.Context, args struct{ ID ID }) (*library, error) {
	var lib library
	if err := q.get(ctx, "/api/libraries/"+seg(args.ID), nil, &lib); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &lib, nil
}

// Videos: GET /api/videos (filters, sort and the paginate cursor are the
// REST ones).
func (q *root) Videos(ctx context.Context, args struct {
	Library  *ID
	Language *string
	Q        *string
	Sort     *string
	Limit    *int32
	After    *string
}) (*videoConnection, error) {
	v := url.Values{}
	if args.Library != nil {
		v.Set("library", string(*args.Library))
	}
	setStr(v, "language", args.Language)
	setStr(v, "q", args.Q)
	setStr(v, "sort", args.Sort)
	setStr(v, "cursor", args.After)
	setInt(v, "limit", args.Limit)
	var page struct {
		Items []videos.Video `json:"items"`
		Next  *string        `json:"next"`
	}
	if err := q.get(ctx, "/api/videos", v, &page); err != nil {
		return nil, err
	}
	return &videoConnection{Items: q.rs.newVideoNodes(ctx, page.Items), NextCursor: page.Next}, nil
}

type videoConnection struct {
	Items      []*videoNode
	NextCursor *string
}

// Video: GET /api/videos/{id} (404 → null).
func (q *root) Video(ctx context.Context, args struct{ ID ID }) (*videoNode, error) {
	var v videos.Video
	if err := q.get(ctx, "/api/videos/"+seg(args.ID), nil, &v); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return q.rs.newVideoNodes(ctx, []videos.Video{v})[0], nil
}

type searchInput struct {
	Q       string
	Mode    *string
	Limit   *int32
	Filters *struct {
		Language  *[]string
		LibraryID *[]ID
		Speaker   *[]string
	}
}

// Search: POST /api/search (a read; hits are grouped per video, best
// match first, in the REST ranking order).
func (q *root) Search(ctx context.Context, args struct{ Input searchInput }) (*searchResult, error) {
	body := map[string]any{"q": args.Input.Q}
	if args.Input.Mode != nil {
		body["mode"] = *args.Input.Mode
	}
	if args.Input.Limit != nil {
		body["limit"] = *args.Input.Limit
	}
	if f := args.Input.Filters; f != nil {
		filters := map[string]any{}
		if f.Language != nil {
			filters["language"] = *f.Language
		}
		if f.LibraryID != nil {
			ids := make([]string, 0, len(*f.LibraryID))
			for _, id := range *f.LibraryID {
				ids = append(ids, string(id))
			}
			filters["library_id"] = ids
		}
		if f.Speaker != nil {
			filters["speaker"] = *f.Speaker
		}
		body["filters"] = filters
	}
	var resp struct {
		Hits   []*searchMatch  `json:"hits"`
		Total  int32           `json:"total"`
		TookMs json.RawMessage `json:"took_ms"`
	}
	if _, err := q.rs.Bridge.json(ctx, call{method: http.MethodPost, path: "/api/search", body: body}, &resp); err != nil {
		return nil, err
	}
	out := &searchResult{Hits: groupHits(resp.Hits), Total: resp.Total}
	if len(resp.TookMs) > 0 {
		_ = json.Unmarshal(resp.TookMs, &out.TookMs.V)
	} else {
		out.TookMs.V = map[string]any{}
	}
	return out, nil
}

// groupHits folds segment hits into one SearchHit per video in first-
// seen (rank) order; bestMatch is the highest score.
func groupHits(hits []*searchMatch) []*searchHit {
	out := []*searchHit{}
	byVideo := map[ID]*searchHit{}
	for _, h := range hits {
		g, ok := byVideo[h.VideoID]
		if !ok {
			g = &searchHit{VideoID: h.VideoID, BestMatch: h}
			byVideo[h.VideoID] = g
			out = append(out, g)
		}
		g.Matches = append(g.Matches, h)
		if h.Score > g.BestMatch.Score {
			g.BestMatch = h
		}
	}
	return out
}

// SearchSuggest: GET /api/search/suggest.
func (q *root) SearchSuggest(ctx context.Context, args struct{ Q string }) ([]string, error) {
	var resp struct {
		Suggestions []string `json:"suggestions"`
	}
	if err := q.get(ctx, "/api/search/suggest", url.Values{"q": {args.Q}}, &resp); err != nil {
		return nil, err
	}
	return nonNilStr(resp.Suggestions), nil
}

// Job: GET /api/jobs/{id} (404 → null).
func (q *root) Job(ctx context.Context, args struct{ ID ID }) (*job, error) {
	var j job
	if err := q.get(ctx, "/api/jobs/"+seg(args.ID), nil, &j); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

// Jobs: GET /api/jobs (filters and the paginate cursor are the REST ones).
func (q *root) Jobs(ctx context.Context, args struct {
	Stage *string
	State *string
	Limit *int32
	After *string
}) (*jobConnection, error) {
	v := url.Values{}
	setStr(v, "stage", args.Stage)
	setStr(v, "state", args.State)
	setStr(v, "cursor", args.After)
	setInt(v, "limit", args.Limit)
	var page struct {
		Items []*job `json:"items"`
		Next  *string
	}
	if err := q.get(ctx, "/api/jobs", v, &page); err != nil {
		return nil, err
	}
	return &jobConnection{Items: nonNil(page.Items), NextCursor: page.Next}, nil
}

// QueueStats: GET /api/queue/stats.
func (q *root) QueueStats(ctx context.Context) (JSON, error) {
	var j JSON
	err := q.get(ctx, "/api/queue/stats", nil, &j)
	return j, err
}

// Collections: GET /api/collections.
func (q *root) Collections(ctx context.Context) ([]*collection, error) {
	var page struct {
		Items []*collection `json:"items"`
	}
	if err := q.get(ctx, "/api/collections", nil, &page); err != nil {
		return nil, err
	}
	for _, c := range page.Items {
		c.rs = q.rs
	}
	return nonNil(page.Items), nil
}

// Collection: GET /api/collections/{id} (404 → null).
func (q *root) Collection(ctx context.Context, args struct{ ID ID }) (*collection, error) {
	var c collection
	if err := q.get(ctx, "/api/collections/"+seg(args.ID), nil, &c); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	c.rs = q.rs
	return &c, nil
}

// Tags: GET /api/tags.
func (q *root) Tags(ctx context.Context) ([]*tag, error) {
	var page struct {
		Items []*tag `json:"items"`
	}
	if err := q.get(ctx, "/api/tags", nil, &page); err != nil {
		return nil, err
	}
	return nonNil(page.Items), nil
}

// Speakers: GET /api/speakers.
func (q *root) Speakers(ctx context.Context, args struct{ VideoID *ID }) ([]*speaker, error) {
	v := url.Values{}
	if args.VideoID != nil {
		v.Set("video_id", string(*args.VideoID))
	}
	var page struct {
		Items []*speaker `json:"items"`
	}
	if err := q.get(ctx, "/api/speakers", v, &page); err != nil {
		return nil, err
	}
	return nonNil(page.Items), nil
}

// Settings: GET /api/settings.
func (q *root) Settings(ctx context.Context) (JSON, error) {
	var j JSON
	err := q.get(ctx, "/api/settings", nil, &j)
	return j, err
}

// Recommendations: GET /api/recommendations (the REST surface default,
// cache, dismissals and caps), flattened to cards.
func (q *root) Recommendations(ctx context.Context, args struct {
	Surface *string
	Limit   *int32
}) ([]*railCard, error) {
	surface := "tv-home"
	if args.Surface != nil && *args.Surface != "" {
		surface = *args.Surface
	}
	rails, err := q.rails(ctx, surface, args.Limit)
	if err != nil {
		return nil, err
	}
	return q.cards(ctx, rails)
}

// ContinueWatching: the continue-watching rail of GET
// /api/recommendations?surface=tv-home.
func (q *root) ContinueWatching(ctx context.Context, args struct{ Limit *int32 }) ([]*railCard, error) {
	rails, err := q.rails(ctx, "tv-home", args.Limit)
	if err != nil {
		return nil, err
	}
	var keep []recommendations.Rail
	for _, r := range rails {
		if r.ReasonKind == recommendations.ReasonContinueWatching {
			keep = append(keep, r)
		}
	}
	return q.cards(ctx, keep)
}

func (q *root) rails(ctx context.Context, surface string, limit *int32) ([]recommendations.Rail, error) {
	v := url.Values{"surface": {surface}}
	setInt(v, "limit", limit)
	var resp recommendations.Response
	if err := q.get(ctx, "/api/recommendations", v, &resp); err != nil {
		return nil, err
	}
	return resp.Rails, nil
}

// cards flattens rails into RailCards, titled from the videos (one
// batch read; a video the caller may not read keeps its id as title).
func (q *root) cards(ctx context.Context, rails []recommendations.Rail) ([]*railCard, error) {
	var ids []string
	for _, r := range rails {
		for _, it := range r.Items {
			ids = append(ids, it.VideoID)
		}
	}
	nodes, err := q.rs.videosByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	titles := map[string]string{}
	for _, n := range nodes {
		t := n.v.Filename
		if n.v.Title != nil && *n.v.Title != "" {
			t = *n.v.Title
		}
		titles[n.v.ID] = t
	}
	out := []*railCard{}
	for _, r := range rails {
		reason := r.ReasonKind
		for _, it := range r.Items {
			c := &railCard{ID: ID(it.VideoID), Title: it.VideoID, PositionSec: it.PositionSec,
				RemainingSec: it.RemainingSec, Reason: &reason}
			if t, ok := titles[it.VideoID]; ok {
				c.Title = t
			}
			if it.DurationSec != nil {
				c.DurationSec = *it.DurationSec
			}
			if it.PosterURL != "" {
				p := it.PosterURL
				c.PosterURL = &p
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// StreamCapabilities: GET /api/stream/capabilities.
func (q *root) StreamCapabilities(ctx context.Context) (JSON, error) {
	var j JSON
	err := q.get(ctx, "/api/stream/capabilities", nil, &j)
	return j, err
}

// StreamSession: GET /api/stream/sessions/{id} (404 → null).
func (q *root) StreamSession(ctx context.Context, args struct{ ID ID }) (*streamingSession, error) {
	var s streamingSession
	if err := q.get(ctx, "/api/stream/sessions/"+seg(args.ID), nil, &s); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

// Devices: GET /api/devices.
func (q *root) Devices(ctx context.Context) ([]*device, error) {
	var page struct {
		Items []*device `json:"items"`
	}
	if err := q.get(ctx, "/api/devices", nil, &page); err != nil {
		return nil, err
	}
	return nonNil(page.Items), nil
}

// ---------- Mutation ----------

// CreateLibrary: POST /api/libraries.
func (q *root) CreateLibrary(ctx context.Context, args struct {
	Input struct {
		Name     string
		Roots    []string
		Settings *JSON
	}
}) (*library, error) {
	body := map[string]any{"name": args.Input.Name, "roots": args.Input.Roots}
	if args.Input.Settings != nil {
		body["settings"] = args.Input.Settings.V
	}
	var lib library
	if _, err := q.mutate(ctx, "createLibrary", http.MethodPost, "/api/libraries", nil, body, &lib); err != nil {
		return nil, err
	}
	return &lib, nil
}

// PatchLibrary: PATCH /api/libraries/{id} (only the given fields).
func (q *root) PatchLibrary(ctx context.Context, args struct {
	ID    ID
	Input struct {
		Name     *string
		Roots    *[]string
		Settings *JSON
	}
}) (*library, error) {
	body := map[string]any{}
	if args.Input.Name != nil {
		body["name"] = *args.Input.Name
	}
	if args.Input.Roots != nil {
		body["roots"] = *args.Input.Roots
	}
	if args.Input.Settings != nil {
		body["settings"] = args.Input.Settings.V
	}
	var lib library
	if _, err := q.mutate(ctx, "patchLibrary", http.MethodPatch, "/api/libraries/"+seg(args.ID), nil, body, &lib); err != nil {
		return nil, err
	}
	return &lib, nil
}

type deleteArgs struct {
	ID      ID
	Purge   *bool
	Confirm *string
}

func (a deleteArgs) query() url.Values {
	v := url.Values{}
	if a.Purge != nil {
		v.Set("purge", strconv.FormatBool(*a.Purge))
	}
	setStr(v, "confirm", a.Confirm)
	return v
}

// DeleteLibrary: DELETE /api/libraries/{id} (?purge=&confirm=<name>).
func (q *root) DeleteLibrary(ctx context.Context, args deleteArgs) (*deleteResult, error) {
	var resp struct {
		FailedPaths []string `json:"failed_paths"`
	}
	if _, err := q.mutate(ctx, "deleteLibrary", http.MethodDelete, "/api/libraries/"+seg(args.ID), args.query(), nil, &resp); err != nil {
		return nil, err
	}
	return &deleteResult{Deleted: true, FailedPaths: nonNilStr(resp.FailedPaths)}, nil
}

// ScanLibrary: POST /api/libraries/{id}/scan → the scan job id.
func (q *root) ScanLibrary(ctx context.Context, args struct{ ID ID }) (string, error) {
	var resp struct {
		JobID ID `json:"job_id"`
	}
	_, err := q.mutate(ctx, "scanLibrary", http.MethodPost, "/api/libraries/"+seg(args.ID)+"/scan", nil, nil, &resp)
	return string(resp.JobID), err
}

// PatchVideo: PATCH /api/videos/{id} (tags replace the set).
func (q *root) PatchVideo(ctx context.Context, args struct {
	ID    ID
	Input struct {
		Title       *string
		Description *string
		Tags        *[]string
	}
}) (*videoNode, error) {
	body := map[string]any{}
	if args.Input.Title != nil {
		body["title"] = *args.Input.Title
	}
	if args.Input.Description != nil {
		body["description"] = *args.Input.Description
	}
	if args.Input.Tags != nil {
		body["tags"] = *args.Input.Tags
	}
	var v videos.Video
	if _, err := q.mutate(ctx, "patchVideo", http.MethodPatch, "/api/videos/"+seg(args.ID), nil, body, &v); err != nil {
		return nil, err
	}
	return q.rs.newVideoNodes(ctx, []videos.Video{v})[0], nil
}

// DeleteVideo: DELETE /api/videos/{id} (?purge=&confirm=<video id>).
func (q *root) DeleteVideo(ctx context.Context, args deleteArgs) (*deleteResult, error) {
	res, err := q.mutate(ctx, "deleteVideo", http.MethodDelete, "/api/videos/"+seg(args.ID), args.query(), nil, nil)
	if err != nil {
		return nil, err
	}
	out := &deleteResult{Deleted: true, FailedPaths: []string{}}
	if w := res.header.Get("Maktaba-Warning"); w != "" {
		out.Warning = &w
	}
	return out, nil
}

// ProcessVideo: POST /api/videos/{id}/process → the job id.
func (q *root) ProcessVideo(ctx context.Context, args struct {
	ID       ID
	Stage    *string
	Priority *int32
}) (string, error) {
	body := map[string]any{}
	if args.Stage != nil {
		body["stage"] = *args.Stage
	}
	if args.Priority != nil {
		body["priority"] = *args.Priority
	}
	var resp struct {
		JobID ID `json:"job_id"`
	}
	_, err := q.mutate(ctx, "processVideo", http.MethodPost, "/api/videos/"+seg(args.ID)+"/process", nil, body, &resp)
	return string(resp.JobID), err
}

// ReprocessVideo: POST /api/videos/{id}/reprocess → the job ids.
func (q *root) ReprocessVideo(ctx context.Context, args struct {
	ID        ID
	FromStage string
}) ([]string, error) {
	var resp struct {
		JobIDs []ID `json:"job_ids"`
	}
	if _, err := q.mutate(ctx, "reprocessVideo", http.MethodPost, "/api/videos/"+seg(args.ID)+"/reprocess", nil,
		map[string]any{"from_stage": args.FromStage}, &resp); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.JobIDs))
	for _, id := range resp.JobIDs {
		out = append(out, string(id))
	}
	return out, nil
}

// SaveSearch: POST /api/search/save → the saved search id.
func (q *root) SaveSearch(ctx context.Context, args struct {
	Name  string
	Query JSON
}) (ID, error) {
	var resp struct {
		ID ID `json:"id"`
	}
	_, err := q.mutate(ctx, "saveSearch", http.MethodPost, "/api/search/save", nil,
		map[string]any{"name": args.Name, "query": args.Query.V}, &resp)
	return resp.ID, err
}

// OpenStreamSession: POST /api/stream/sessions, then GET
// /api/stream/sessions/{id} for the stored open time.
func (q *root) OpenStreamSession(ctx context.Context, args struct {
	VideoID ID
	Profile *string
}) (*streamingSession, error) {
	body := map[string]any{"video_id": string(args.VideoID)}
	if args.Profile != nil {
		body["client_profile"] = *args.Profile
	}
	var s streamingSession
	if _, err := q.mutate(ctx, "openStreamSession", http.MethodPost, "/api/stream/sessions", nil, body, &s); err != nil {
		return nil, err
	}
	s.VideoID = args.VideoID
	var stored streamingSession
	if err := q.get(ctx, "/api/stream/sessions/"+seg(s.ID), nil, &stored); err == nil {
		s.OpenedAt = stored.OpenedAt
		s.LastSeenAt = stored.LastSeenAt
	}
	return &s, nil
}

// CloseStreamSession: DELETE /api/stream/sessions/{id}.
func (q *root) CloseStreamSession(ctx context.Context, args struct{ ID ID }) (bool, error) {
	_, err := q.mutate(ctx, "closeStreamSession", http.MethodDelete, "/api/stream/sessions/"+seg(args.ID), nil, nil, nil)
	return err == nil, err
}

// PostProgress: POST /api/stream/sessions/{id}/progress (a debounced
// beat is accepted too, as in REST).
func (q *root) PostProgress(ctx context.Context, args struct {
	SessionID   ID
	PositionSec float64
	Completed   *bool
}) (bool, error) {
	body := map[string]any{"position_sec": args.PositionSec}
	if args.Completed != nil {
		body["completed"] = *args.Completed
	}
	_, err := q.mutate(ctx, "postProgress", http.MethodPost, "/api/stream/sessions/"+seg(args.SessionID)+"/progress", nil, body, nil)
	return err == nil, err
}

func (q *root) jobAction(ctx context.Context, field string, id ID, action string, query url.Values) (*job, error) {
	var j job
	if _, err := q.mutate(ctx, field, http.MethodPost, "/api/jobs/"+seg(id)+"/"+action, query, nil, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// PauseJob: POST /api/jobs/{id}/pause (?force=).
func (q *root) PauseJob(ctx context.Context, args struct {
	ID    ID
	Force *bool
}) (*job, error) {
	v := url.Values{}
	if args.Force != nil {
		v.Set("force", strconv.FormatBool(*args.Force))
	}
	return q.jobAction(ctx, "pauseJob", args.ID, "pause", v)
}

// ResumeJob: POST /api/jobs/{id}/resume.
func (q *root) ResumeJob(ctx context.Context, args struct{ ID ID }) (*job, error) {
	return q.jobAction(ctx, "resumeJob", args.ID, "resume", nil)
}

// CancelJob: POST /api/jobs/{id}/cancel.
func (q *root) CancelJob(ctx context.Context, args struct{ ID ID }) (*job, error) {
	return q.jobAction(ctx, "cancelJob", args.ID, "cancel", nil)
}

// RetryJob: POST /api/jobs/{id}/retry.
func (q *root) RetryJob(ctx context.Context, args struct{ ID ID }) (*job, error) {
	return q.jobAction(ctx, "retryJob", args.ID, "retry", nil)
}

// PatchSettings: PATCH /api/settings → the settings after the patch.
func (q *root) PatchSettings(ctx context.Context, args struct{ Input JSON }) (JSON, error) {
	var j JSON
	_, err := q.mutate(ctx, "patchSettings", http.MethodPatch, "/api/settings", nil, args.Input.V, &j)
	return j, err
}

// RegisterDevice: POST /api/devices/register → the device id.
func (q *root) RegisterDevice(ctx context.Context, args struct {
	Input struct {
		Platform   string
		PushToken  string
		BundleID   string
		AppVersion *string
		Locale     *string
	}
}) (ID, error) {
	in := args.Input
	body := map[string]any{"platform": in.Platform, "push_token": in.PushToken, "bundle_id": in.BundleID}
	if in.AppVersion != nil {
		body["app_version"] = *in.AppVersion
	}
	if in.Locale != nil {
		body["locale"] = *in.Locale
	}
	var resp struct {
		ID ID `json:"id"`
	}
	_, err := q.mutate(ctx, "registerDevice", http.MethodPost, "/api/devices/register", nil, body, &resp)
	return resp.ID, err
}

// UnregisterDevice: DELETE /api/devices/{id}.
func (q *root) UnregisterDevice(ctx context.Context, args struct{ ID ID }) (bool, error) {
	_, err := q.mutate(ctx, "unregisterDevice", http.MethodDelete, "/api/devices/"+seg(args.ID), nil, nil, nil)
	return err == nil, err
}

// MergeSpeakers: POST /api/speakers/merge → the segments reassigned.
func (q *root) MergeSpeakers(ctx context.Context, args struct {
	Keep ID
	Drop ID
}) (int32, error) {
	var resp struct {
		Affected int32 `json:"affected_segments"`
	}
	_, err := q.mutate(ctx, "mergeSpeakers", http.MethodPost, "/api/speakers/merge", nil,
		map[string]any{"keep": string(args.Keep), "drop": string(args.Drop)}, &resp)
	return resp.Affected, err
}

// ---------- helpers ----------

func setStr(v url.Values, k string, s *string) {
	if s != nil && *s != "" {
		v.Set(k, *s)
	}
}

func setInt(v url.Values, k string, n *int32) {
	if n != nil {
		v.Set(k, strconv.Itoa(int(*n)))
	}
}

func nonNil[T any](s []*T) []*T {
	if s == nil {
		return []*T{}
	}
	return s
}

func nonNilStr(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
