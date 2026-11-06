// Package playqueue is Epic 56 story 56.01: one play queue on the server.
//
//	POST   /api/me/play-queues                  {source:{kind,id,filters?}, shuffle, start_video_id?, device_id?}
//	GET    /api/me/play-queues/current          the caller's latest queue (any device), or 204
//	GET    /api/me/play-queues/{id}             ?around=&before=&after=: a window
//	PUT    /api/me/play-queues/{id}/current     {pos, version}: move (409 stale_queue on an old version)
//	POST   /api/me/play-queues/{id}/items       {video_ids, where: next|end}: Play next, Add to queue
//	POST   /api/me/play-queues/{id}/reshuffle   a new seed for what comes after the current item
//	DELETE /api/me/play-queues/{id}
//
// Every Play and Shuffle of every app makes a queue from a source (a library in
// the grid's filters and sort, a series, a season, a collection, a playlist or
// one video). The queue writes its items in chunks of 500 as the current item
// nears their end (chunks.go), so a TV can shuffle a library of thousands of
// videos; a queue is its owner's alone (another user's is 404, an admin's too),
// and queues not used for 7 days are deleted (Sweep).
package playqueue

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

const (
	// ChunkSize is how many items a queue writes at once.
	ChunkSize = 500
	// RefillWithin: the next chunk is written when fewer items than this are
	// left after the current one.
	RefillWithin = 50
	// MaxHeld caps the items a queue holds (the oldest played go first).
	MaxHeld = 5000
	// MaxAdd caps the videos one Play next / Add to queue names.
	MaxAdd = 200
	// IdleFor is how long an unused queue is kept.
	IdleFor = 7 * 24 * time.Hour
	// default window around the current item
	defaultBefore = 10
	defaultAfter  = 30
	maxWindow     = 200
)

// Error types of the queue's answers.
const (
	TypeStaleQueue  = "https://maktaba.tv/problems/stale_queue"
	TypeEmptySource = "https://maktaba.tv/problems/empty_source"
)

// Handler serves the play queue routes.
type Handler struct {
	DB      *sql.DB
	Sources Sources
	NowFunc func() time.Time
	// SeedFunc makes a shuffle's seed (nil: crypto random, 63 bits).
	SeedFunc func() int64
}

// Mount attaches the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/api/me/play-queues", h.Create)
	r.Get("/api/me/play-queues/current", h.Current)
	r.Get("/api/me/play-queues/{id}", h.Get)
	r.Put("/api/me/play-queues/{id}/current", h.Move)
	r.Post("/api/me/play-queues/{id}/items", h.AddItems)
	r.Post("/api/me/play-queues/{id}/reshuffle", h.Reshuffle)
	r.Delete("/api/me/play-queues/{id}", h.Delete)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) seed() int64 {
	if h.SeedFunc != nil {
		return h.SeedFunc()
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UnixNano() & (1<<63 - 1)
	}
	return int64(binary.BigEndian.Uint64(b[:]) & (1<<63 - 1))
}

func principalOf(ctx context.Context) *principal.Principal {
	if p := principal.FromContext(ctx); p != nil {
		return p
	}
	return &principal.Principal{}
}

// owner is the signed-in caller with a user row, or an error written: 401 with
// no session, 403 for a principal with no user (the admin token owns no queue).
func owner(w http.ResponseWriter, r *http.Request) (*principal.Principal, string) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized",
			Status: http.StatusUnauthorized, Detail: "authentication required"})
		return nil, ""
	}
	if _, err := uuid.Parse(p.UserID); err != nil {
		httperror.Write(w, r, httperror.Forbidden("", "a play queue belongs to a user account"))
		return nil, ""
	}
	return p, p.UserID
}

func internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	httperror.Write(w, r, httperror.Internal(what).Wrap(err))
}

// CreateRequest is the body of POST /api/me/play-queues.
type CreateRequest struct {
	Source       SourceRef `json:"source"`
	Shuffle      bool      `json:"shuffle"`
	StartVideoID string    `json:"start_video_id,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
}

// Create implements POST /api/me/play-queues: 201 and the queue with its window.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	var req CreateRequest
	if e := common.ReadJSON(r, &req, 16<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if e := validateRef(&req.Source); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.StartVideoID != "" {
		if _, err := uuid.Parse(req.StartVideoID); err != nil {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "start_video_id", Message: "a video id"}}))
			return
		}
	}
	if len(req.DeviceID) > 200 {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "device_id", Message: "at most 200 characters"}}))
		return
	}
	ctx := r.Context()
	var seed int64
	if req.Shuffle {
		seed = h.seed()
	}
	res, e, err := h.resolve(ctx, req.Source, req.Shuffle, seed)
	if err != nil {
		internal(w, r, "read the source", err)
		return
	}
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if res.noShuffle {
		req.Shuffle, seed = false, 0
	}
	first, e, err := res.src.read(ctx, "", ChunkSize)
	if err != nil {
		internal(w, r, "read the source", err)
		return
	}
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	ids := dedupe(first.ids, nil)
	if len(ids) == 0 {
		httperror.Write(w, r, &httperror.Error{Type: TypeEmptySource, Title: "empty_source", Status: http.StatusUnprocessableEntity,
			Detail: "nothing to play in this source"})
		return
	}
	start := res.start
	if req.StartVideoID != "" {
		start = req.StartVideoID
	}
	current := 0
	if start != "" {
		if i := indexOf(ids, start); i >= 0 {
			current = i
		} else if req.StartVideoID != "" {
			// the chosen video is further in the source than one chunk: it
			// opens the queue, the source follows
			if e, err := h.checkVisible(ctx, p, []string{start}); err != nil {
				internal(w, r, "check the start video", err)
				return
			} else if e != nil {
				httperror.Write(w, r, e)
				return
			}
			ids = append([]string{start}, ids...)
		}
	}
	q := Queue{
		ID: uuid.NewString(), UserID: uid, DeviceID: strings.TrimSpace(req.DeviceID), Source: req.Source,
		Shuffle: req.Shuffle, Seed: seed, Cursor: first.cursor, Done: first.done, CurrentPos: current, Version: 1,
	}
	if q.Source.Filters == nil {
		q.Source.Filters = map[string]string{}
	}
	if err := h.insertQueue(ctx, q, ids); err != nil {
		internal(w, r, "create the queue", err)
		return
	}
	h.writeQueue(w, r, http.StatusCreated, p, q.ID, q.CurrentPos, defaultBefore, defaultAfter)
}

// Current implements GET /api/me/play-queues/current: the caller's most
// recently used queue, from any device, or 204.
func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	var id string
	err := h.DB.QueryRowContext(r.Context(), `SELECT CAST(id AS TEXT) FROM play_queues WHERE user_id = $1 ORDER BY used_at DESC, id LIMIT 1`, uid).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		internal(w, r, "latest queue", err)
		return
	}
	q, err := h.loadQueue(r.Context(), h.DB, uid, id, false)
	if err != nil || q == nil {
		internal(w, r, "latest queue", err)
		return
	}
	h.writeQueue(w, r, http.StatusOK, p, id, q.CurrentPos, defaultBefore, defaultAfter)
}

// Get implements GET /api/me/play-queues/{id}?around=&before=&after=.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	q, ok := h.own(w, r, uid)
	if !ok {
		return
	}
	around := q.CurrentPos
	vals := r.URL.Query()
	num := func(name string, def int) (int, bool) {
		raw := vals.Get(name)
		if raw == "" {
			return def, true
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || (name != "around" && n > maxWindow) {
			httperror.Write(w, r, httperror.InvalidQuery(fmt.Sprintf("%s must be a number from 0 (at most %d for a window side)", name, maxWindow)))
			return 0, false
		}
		return n, true
	}
	var ok1, ok2, ok3 bool
	var before, after int
	if around, ok1 = num("around", around); !ok1 {
		return
	}
	if before, ok2 = num("before", defaultBefore); !ok2 {
		return
	}
	if after, ok3 = num("after", defaultAfter); !ok3 {
		return
	}
	h.writeQueue(w, r, http.StatusOK, p, q.ID, around, before, after)
}

// own loads the caller's queue named by {id}, or writes 404.
func (h *Handler) own(w http.ResponseWriter, r *http.Request, uid string) (*Queue, bool) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		httperror.Write(w, r, httperror.NotFound("play queue "+id))
		return nil, false
	}
	q, err := h.loadQueue(r.Context(), h.DB, uid, id, false)
	if err != nil {
		internal(w, r, "load the queue", err)
		return nil, false
	}
	if q == nil {
		httperror.Write(w, r, httperror.NotFound("play queue "+id))
		return nil, false
	}
	return q, true
}

// MoveRequest is the body of PUT …/current.
type MoveRequest struct {
	Pos     *int `json:"pos"`
	Version int  `json:"version"`
}

// Move implements PUT /api/me/play-queues/{id}/current: the new current item
// (by its pos) for the version the caller holds. A stale version is 409
// stale_queue with the queue's current state.
func (h *Handler) Move(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	q, ok := h.own(w, r, uid)
	if !ok {
		return
	}
	var req MoveRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Pos == nil {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "pos", Message: "required"}}))
		return
	}
	stale, e, err := h.move(r.Context(), p, q.ID, uid, *req.Pos, req.Version)
	if err != nil {
		internal(w, r, "move", err)
		return
	}
	if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if stale {
		h.writeStale(w, r, p, q.ID, uid)
		return
	}
	h.writeQueue(w, r, http.StatusOK, p, q.ID, *req.Pos, defaultBefore, defaultAfter)
}

// writeStale answers 409 stale_queue with the queue as it is now.
func (h *Handler) writeStale(w http.ResponseWriter, r *http.Request, p *principal.Principal, id, uid string) {
	q, err := h.loadQueue(r.Context(), h.DB, uid, id, false)
	if err != nil || q == nil {
		internal(w, r, "load the queue", err)
		return
	}
	doc, err := h.queueDoc(r.Context(), p, *q, q.CurrentPos, defaultBefore, defaultAfter)
	if err != nil {
		internal(w, r, "queue window", err)
		return
	}
	httperror.Write(w, r, &httperror.Error{Type: TypeStaleQueue, Title: "stale_queue", Status: http.StatusConflict,
		Detail: "the queue changed on another device", Extras: map[string]any{"queue": doc}})
}

// AddRequest is the body of POST …/items.
type AddRequest struct {
	VideoIDs []string `json:"video_ids"`
	Where    string   `json:"where"`
	Version  *int     `json:"version,omitempty"`
}

// AddItems implements POST /api/me/play-queues/{id}/items: Play next (where
// next: after the current item and the Play next items already after it) or
// Add to queue (where end: after every item held, before the source's next
// chunk). Answers the queue.
func (h *Handler) AddItems(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	q, ok := h.own(w, r, uid)
	if !ok {
		return
	}
	var req AddRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Where != "next" && req.Where != "end" {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "where", Message: "next or end"}}))
		return
	}
	if len(req.VideoIDs) == 0 || len(req.VideoIDs) > MaxAdd {
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "video_ids", Message: "1 to " + strconv.Itoa(MaxAdd) + " videos"}}))
		return
	}
	ids := dedupe(req.VideoIDs, nil)
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			httperror.Write(w, r, httperror.NotFound("video "+id))
			return
		}
	}
	ctx := r.Context()
	if e, err := h.checkVisible(ctx, p, ids); err != nil {
		internal(w, r, "check videos", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	if err := h.add(ctx, q.ID, uid, ids, req.Where); err != nil {
		internal(w, r, "add to the queue", err)
		return
	}
	q2, err := h.loadQueue(ctx, h.DB, uid, q.ID, false)
	if err != nil || q2 == nil {
		internal(w, r, "load the queue", err)
		return
	}
	h.writeQueue(w, r, http.StatusOK, p, q.ID, q2.CurrentPos, defaultBefore, defaultAfter)
}

// Reshuffle implements POST /api/me/play-queues/{id}/reshuffle: a new seed;
// the items after the current one are replaced from the source in the new
// order (without the ones already played), the current one stays.
func (h *Handler) Reshuffle(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	q, ok := h.own(w, r, uid)
	if !ok {
		return
	}
	if !q.Shuffle {
		httperror.Write(w, r, httperror.Conflict("", "the queue is not shuffled"))
		return
	}
	if e, err := h.reshuffle(r.Context(), p, *q); err != nil {
		internal(w, r, "reshuffle", err)
		return
	} else if e != nil {
		httperror.Write(w, r, e)
		return
	}
	h.writeQueue(w, r, http.StatusOK, p, q.ID, q.CurrentPos, defaultBefore, defaultAfter)
}

// Delete implements DELETE /api/me/play-queues/{id}: 204.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	p, uid := owner(w, r)
	if p == nil {
		return
	}
	q, ok := h.own(w, r, uid)
	if !ok {
		return
	}
	for _, s := range []string{`DELETE FROM play_queue_items WHERE queue_id = $1`, `DELETE FROM play_queues WHERE id = $1`} {
		if _, err := h.DB.ExecContext(r.Context(), s, q.ID); err != nil {
			internal(w, r, "delete the queue", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeQueue answers the queue with a window around pos.
func (h *Handler) writeQueue(w http.ResponseWriter, r *http.Request, status int, p *principal.Principal, id string, around, before, after int) {
	q, err := h.loadQueue(r.Context(), h.DB, p.UserID, id, false)
	if err != nil || q == nil {
		internal(w, r, "load the queue", err)
		return
	}
	doc, err := h.queueDoc(r.Context(), p, *q, around, before, after)
	if err != nil {
		internal(w, r, "queue window", err)
		return
	}
	common.WriteJSON(w, r, status, doc)
}

// Sweep deletes the queues not used for IdleFor, every interval, until ctx
// ends (the API's maintenance loop).
func Sweep(ctx context.Context, db *sql.DB, interval time.Duration, logger *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n, err := SweepOnce(ctx, db, time.Now()); err != nil {
			logger.Warn("play queues: the idle sweep failed", "err", err, "event", "play_queue_sweep_failed")
		} else if n > 0 {
			logger.Info("play queues: idle ones deleted", "deleted", n, "event", "play_queue_swept")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce deletes the queues whose last use is older than IdleFor before now.
func SweepOnce(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	cut := tsArg(db, now.Add(-IdleFor))
	if _, err := db.ExecContext(ctx, `DELETE FROM play_queue_items WHERE queue_id IN (SELECT id FROM play_queues WHERE used_at < $1)`, cut); err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM play_queues WHERE used_at < $1`, cut)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func dedupe(ids []string, seen map[string]bool) []string {
	if seen == nil {
		seen = map[string]bool{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func indexOf(ids []string, s string) int {
	for i, x := range ids {
		if x == s {
			return i
		}
	}
	return -1
}
