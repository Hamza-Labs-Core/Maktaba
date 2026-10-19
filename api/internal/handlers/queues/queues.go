// Package queues implements the queue control-centre API of
// plan-06-11 §8 (workstream B):
//
//	GET  /api/queues                      snapshot (gauges, health, flows)
//	GET  /api/queues/graph                stage graph + pools
//	GET  /api/queues/{stage}              one queue's detail page
//	GET  /api/queues/{stage}/rate         10-minute done/failed buckets
//	GET  /api/queues/{stage}/jobs         keyset-paged job list
//	GET  /api/queues/{stage}/failures     open failures grouped by cause
//	POST /api/queues/{stage}/pause|resume pause or resume one queue
//	POST /api/queues/pause-all|resume-all global pause
//	PATCH /api/queues/{stage}             enable/disable, write mode
//	POST /api/queues/{stage}/retry|apply  retry failures / apply a review
//	GET  /api/pools, PUT /api/pools/{pool}
//	POST /api/queue/retrigger             "re-add this to the right queue"
//	GET  /api/issues, /api/activity, /api/jobs/{id}/events
//	GET  /api/items/{kind}/{id}/history
//	GET  /api/queues/libraries, /api/queues/health
//
// All enqueueing goes through the processing_jobs_enqueue SQL function
// (see Enqueue) so Go and the Python engine cannot disagree about
// coalescing. Reads need an authenticated principal; every mutation
// needs IsAdmin and answers 403 problem+json otherwise.
package queues

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// EventPublisher is the slice of *eventbus.Bus the handlers use to
// emit durable, replayable events on the "jobs" channel.
type EventPublisher interface {
	Publish(ctx context.Context, channel, typ string, payload map[string]any) error
}

// Handler bundles the dependencies of the queue routes.
type Handler struct {
	DB *sql.DB
	// Hub receives ephemeral events (queues.snapshot) and is the
	// fallback target for control events when no bus is wired.
	Hub *ws.Hub
	// Events publishes queue.control / pool.updated through the
	// cross-replica bus. nil -> fall back to Hub (single process).
	Events  EventPublisher
	NowFunc func() time.Time
	// StageETA lets a stage supply its own time left for its card, in
	// place of the pending jobs over the finished-jobs rate: transcription's
	// jobs run ~30 at a time per worker and finish long after their work
	// starts, so its rate of finished files says little. A nil result or an
	// error keeps the rate-based estimate.
	StageETA map[string]func(ctx context.Context, now time.Time) (*float64, error)
	// CPUCount feeds the "auto" size of the cpu pool; default
	// runtime.NumCPU.
	CPUCount func() int

	libMu    sync.Mutex
	libCache libraryCache

	ringMu sync.Mutex
	ring   backlogRing

	// PlexTVURL overrides https://plex.tv (tests); PlexHTTP is the
	// client for it (default 15 s timeout).
	PlexTVURL string
	PlexHTTP  *http.Client
	plexPins  plexSignins

	// Secrets is the home's sealed store (slot 0120): every extension
	// secret is kept there, never in extensions.settings. nil refuses to
	// save a secret (503).
	Secrets secrets.Store
	// Testers run an extension's "Test connection" (Cloudflare Stream).
	Testers map[string]ExtensionTester
	// Validators check an extension's settings change before it is saved.
	Validators map[string]ExtensionValidator

	// extMu serialises the read-modify-write of an extension row's
	// settings (PUT, the Plex client id, the Plex sign-in's server pick)
	// so one cannot overwrite the other's keys.
	extMu sync.Mutex
	// plexID caches the stored Plex client identifier (guarded by extMu).
	plexID string
}

// Mount wires the routes. Static segments (graph, libraries, health,
// pause-all, resume-all) are registered next to the {stage} wildcard;
// chi gives static segments priority.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/queues", h.Snapshot)
	r.Get("/api/queues/graph", h.Graph)
	r.Get("/api/queues/libraries", h.Libraries)
	r.Get("/api/queues/health", h.Health)
	r.Get("/api/queues/insights", h.Insights)
	r.Get("/api/queues/pulse", h.Pulse)
	r.Post("/api/queues/pause-all", h.PauseAll)
	r.Post("/api/queues/resume-all", h.ResumeAll)
	r.Get("/api/queues/{stage}", h.Detail)
	r.Patch("/api/queues/{stage}", h.PatchStage)
	r.Get("/api/queues/{stage}/rate", h.Rate)
	r.Get("/api/queues/{stage}/jobs", h.Jobs)
	r.Get("/api/queues/{stage}/failures", h.StageFailures)
	r.Post("/api/queues/{stage}/pause", h.PauseStage)
	r.Post("/api/queues/{stage}/resume", h.ResumeStage)
	r.Post("/api/queues/{stage}/retry", h.Retry)
	r.Post("/api/queues/{stage}/apply", h.Apply)
	r.Get("/api/workers", h.Workers)
	r.Get("/api/extensions", h.Extensions)
	r.Put("/api/extensions/{name}", h.PutExtension)
	r.Post("/api/extensions/{name}/test", h.TestExtension)
	r.Post("/api/extensions/plex/signin", h.PlexSignin)
	r.Get("/api/extensions/plex/signin/{id}", h.PlexSigninStatus)
	r.Post("/api/extensions/plex/signin/{id}/use", h.PlexSigninUse)
	r.Get("/api/pools", h.ListPools)
	r.Put("/api/pools/{pool}", h.PutPool)
	r.Post("/api/queue/retrigger", h.Retrigger)
	r.Get("/api/issues", h.Issues)
	r.Get("/api/activity", h.Activity)
	r.Get("/api/jobs/{id}/events", h.JobEvents)
	r.Get("/api/items/{kind}/{id}/history", h.ItemHistory)
	r.Get("/api/items/library/{id}/collections", h.LibraryCollections)
	// Parity with the stand-alone control centre (see explain.go, style.go, insights.go).
	r.Get("/api/explain", h.Explain)
	r.Get("/api/seasons/styles", h.SeasonStyles)
	r.Put("/api/seasons/{id}/style", h.PutSeasonStyle)
	r.Put("/api/series/{id}/style", h.PutSeriesStyle)
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) cpus() int {
	if h.CPUCount != nil {
		return h.CPUCount()
	}
	return runtime.NumCPU()
}

// requireAuth rejects anonymous callers (403, like the other handlers).
func requireAuth(w http.ResponseWriter, r *http.Request) bool {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	return true
}

// requireAdmin gates every mutation (plan-06-11 §8: same rule as jobs.go).
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// actor is the audit string stored in paused_by / updated_by.
func actor(r *http.Request) string {
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	return "system"
}

// publish emits an event on channel "jobs": durable through the bus
// when wired, otherwise straight to the local hub.
func (h *Handler) publish(ctx context.Context, typ string, payload map[string]any) {
	if h.Events != nil {
		_ = h.Events.Publish(ctx, "jobs", typ, payload)
		return
	}
	if h.Hub != nil {
		h.Hub.Publish("jobs", ws.Event{Type: typ, At: h.now(), Payload: payload})
	}
}

func writeErr(w http.ResponseWriter, r *http.Request, what string, err error) {
	httperror.Write(w, r, httperror.Internal(what+": "+err.Error()))
}

// ---------- stages & pools ----------

const stageSelect = `SELECT stage, label, lane, lane_order, subject_kinds, pool, version,
	legacy, hidden, external_write, write_mode, enabled, disabled_reason, paused,
	paused_at, paused_by, debounce_sec, edges, may_request, entry_for, graph_synced_at, extension
	FROM job_stages`

func scanStage(rs rowScanner) (StageInfo, error) {
	var s StageInfo
	var kinds, may, entry pq.StringArray
	var reason, by, ext sql.NullString
	var pausedAt, synced sql.NullTime
	var edges []byte
	if err := rs.Scan(&s.Stage, &s.Label, &s.Lane, &s.LaneOrder, &kinds, &s.Pool, &s.Version,
		&s.Legacy, &s.Hidden, &s.ExternalWrite, &s.WriteMode, &s.Enabled, &reason, &s.Paused,
		&pausedAt, &by, &s.DebounceSec, &edges, &may, &entry, &synced, &ext); err != nil {
		return StageInfo{}, err
	}
	s.SubjectKinds = nonNilStrings(kinds)
	s.MayRequest = nonNilStrings(may)
	s.EntryFor = nonNilStrings(entry)
	s.DisabledReason = nullStrPtr(reason)
	s.PausedAt = nullTimePtr(pausedAt)
	s.PausedBy = nullStrPtr(by)
	s.Extension = nullStrPtr(ext)
	s.GraphSyncedAt = nullTimePtr(synced)
	s.Edges = parseEdges(edges)
	s.FedBy = []string{}
	return s, nil
}

// loadStages returns every stage in lane/lane_order, with FedBy filled.
func (h *Handler) loadStages(ctx context.Context) ([]StageInfo, error) {
	rows, err := h.DB.QueryContext(ctx, stageSelect+` ORDER BY lane_order, stage`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StageInfo{}
	for rows.Next() {
		s, err := scanStage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortStages(out)
	fillFedBy(out)
	return out, nil
}

// loadStage reads one stage; sql.ErrNoRows when unknown.
func (h *Handler) loadStage(ctx context.Context, stage string) (StageInfo, error) {
	s, err := scanStage(h.DB.QueryRowContext(ctx, stageSelect+` WHERE stage = $1`, stage))
	if err != nil {
		return StageInfo{}, err
	}
	// FedBy needs the whole graph.
	all, err := h.loadStages(ctx)
	if err == nil {
		for _, a := range all {
			if a.Stage == stage {
				s.FedBy = a.FedBy
			}
		}
	}
	return s, nil
}

// stageOr404 writes the 404/500 for loadStage failures.
func (h *Handler) stageOr404(w http.ResponseWriter, r *http.Request, stage string) (StageInfo, bool) {
	s, err := h.loadStage(r.Context(), stage)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httperror.Write(w, r, httperror.NotFound("queue "+stage))
		} else {
			writeErr(w, r, "load stage", err)
		}
		return StageInfo{}, false
	}
	return s, true
}

func (h *Handler) pausedAll(ctx context.Context) (bool, error) {
	var paused bool
	err := h.DB.QueryRowContext(ctx, `SELECT paused FROM queue_control WHERE id = 1`).Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return paused, err
}
