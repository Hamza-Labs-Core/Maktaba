// Package ws implements Story 7.16 fan-out endpoints:
//
//	/ws/jobs
//	/ws/library/{id}
//	/ws/playback/{video_id}
//
// Plus the SSE fallback for blocked-WebSocket networks (AC-6).
//
// The transport here is built on net/http; we do **not** pull in
// coder/websocket as a hard dependency because the API binary in some
// deployments uses a different upgrader. Instead we expose:
//
//   - SSE for the fallback path (cross-platform, zero deps),
//   - a Hub that other services (Pipeline, the Listener loop) write
//     events to and the SSE handler reads from per connection.
//
// Production wires the Hub to a Postgres LISTEN loop; tests inject
// events directly.
package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// Event is the AC-3 envelope: “{type, at, ...payload}“.
type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Hub fan-outs events to subscribers keyed by “channel“. Each
// subscriber has a bounded channel — once full, the connection is
// closed with 1011 “slow-consumer“ per AC-4.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[string]map[*Subscriber]struct{}
}

// Subscriber is one open connection. Done is closed when the hub
// drops it (e.g. backpressure).
type Subscriber struct {
	C    chan Event
	Done chan struct{}
}

// NewHub constructs an empty Hub.
func NewHub() *Hub {
	return &Hub{subscribers: map[string]map[*Subscriber]struct{}{}}
}

// Subscribe registers a new subscriber on the channel. The caller must
// drain “s.C“ quickly; once 1000 events queue up the hub closes the
// subscription.
func (h *Hub) Subscribe(channel string) *Subscriber {
	s := &Subscriber{
		C:    make(chan Event, 1000),
		Done: make(chan struct{}),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[channel]; !ok {
		h.subscribers[channel] = map[*Subscriber]struct{}{}
	}
	h.subscribers[channel][s] = struct{}{}
	return s
}

// SubscriberCount returns how many subscribers are attached to channel.
func (h *Hub) SubscriberCount(channel string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers[channel])
}

// Unsubscribe drops a subscriber, closes its Done.
func (h *Hub) Unsubscribe(channel string, s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.subscribers[channel]; ok {
		if _, ok := set[s]; ok {
			delete(set, s)
			close(s.Done)
		}
	}
}

// Publish fans out e to every subscriber on the channel. A subscriber
// whose buffer is full is dropped (AC-4 slow-consumer).
func (h *Hub) Publish(channel string, e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	h.mu.RLock()
	subs := make([]*Subscriber, 0, len(h.subscribers[channel]))
	for s := range h.subscribers[channel] {
		subs = append(subs, s)
	}
	h.mu.RUnlock()
	for _, s := range subs {
		select {
		case s.C <- e:
		default:
			h.Unsubscribe(channel, s)
		}
	}
}

// Replayer is the on-connect catch-up seam (Story 19.2 AC3). The
// cross-replica bus (internal/handlers/ws/eventbus) satisfies it: on
// (re)connect the SSE handler asks for every event the client missed
// since the id it last processed, so a client that reconnects to a
// *different* replica loses nothing. nil → no replay (single-process
// dev path; behaviour unchanged). Kept as a local interface so ws has
// no import cycle / hard dependency on eventbus.
type Replayer interface {
	Replay(ctx context.Context, channel string, lastEventID int64) ([]ReplayEvent, error)
}

// ReplayEvent is the minimal shape ws needs from a replayed event. The
// eventbus adapter (busReplayAdapter, wired in p6.go) maps its richer
// Event onto this.
type ReplayEvent struct {
	Type    string
	At      time.Time
	Payload map[string]any
}

// Handler exposes the SSE endpoints.
type Handler struct {
	Hub *Hub
	// Replay, when set, is consulted on connect with the client's
	// Last-Event-ID so missed cross-replica events are re-delivered
	// before the live stream resumes.
	Replay Replayer
	// Initial, when set, may return a first frame for a channel which
	// is written right after connect (the /ws/queues snapshot). It is
	// sent before replay and live events; nil -> nothing extra.
	Initial func(ctx context.Context, channel string) *Event
	// Settings holds the runtime heartbeat interval
	// (ws.heartbeat_interval_sec; nil or unset: 30 s).
	Settings *runtimecfg.Reader
}

// DefaultHeartbeat is the SSE keep-alive comment interval.
const DefaultHeartbeat = 30 * time.Second

// heartbeat is the keep-alive interval a new connection uses.
func (h *Handler) heartbeat(ctx context.Context) time.Duration {
	if d, ok := h.Settings.Seconds(ctx, runtimecfg.WSHeartbeat); ok {
		return d
	}
	return DefaultHeartbeat
}

// Mount wires the SSE routes. WebSocket is intentionally absent here;
// the production binary can wrap each route with an upgrader of its
// choice.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/ws/jobs", h.serveSSE("jobs"))
	r.Get("/ws/queues", h.serveSSE("queues"))
	r.Get("/ws/library/{id}", func(w http.ResponseWriter, r *http.Request) {
		ch := "library:" + chi.URLParam(r, "id")
		h.sseChannel(ch, w, r)
	})
	r.Get("/ws/playback/{video_id}", func(w http.ResponseWriter, r *http.Request) {
		ch := "playback:" + chi.URLParam(r, "video_id")
		h.sseChannel(ch, w, r)
	})
}

// serveSSE is the static-channel variant.
func (h *Handler) serveSSE(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.sseChannel(channel, w, r)
	}
}

// sseChannel runs an SSE response loop for one channel.
func (h *Handler) sseChannel(channel string, w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httperror.Write(w, r, httperror.Internal("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush() // send headers immediately so the client sees the stream open

	// Subscribe BEFORE replay so an event that lands while we are
	// catching up is buffered on the live channel rather than lost in
	// the gap between replay and subscribe.
	sub := h.Hub.Subscribe(channel)
	defer h.Hub.Unsubscribe(channel, sub)

	ctx := r.Context()

	if h.Initial != nil {
		if ev := h.Initial(ctx, channel); ev != nil {
			writeFrame(w, *ev)
			flusher.Flush()
		}
	}

	// Replay-on-connect (Story 19.2 AC3): re-deliver every event the
	// client missed since the id it last processed. Last-Event-ID is
	// the standard SSE reconnect header; ?last_event_id= is the WS
	// fallback. 0/absent → no replay (fresh client). A duplicate at
	// the replay/live boundary is harmless — clients dedupe on the
	// monotonic "_event_id".
	if h.Replay != nil {
		if last := parseLastEventID(r); last > 0 {
			if evs, err := h.Replay.Replay(ctx, channel, last); err == nil {
				for _, e := range evs {
					writeFrame(w, Event(e))
				}
				flusher.Flush()
			}
		}
	}

	heartbeat := time.NewTicker(h.heartbeat(ctx))
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Done:
			return
		case <-heartbeat.C:
			// Comment frame keeps the connection warm.
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case e := <-sub.C:
			writeFrame(w, e)
			flusher.Flush()
		}
	}
}

// writeFrame writes one SSE frame. When the event carries a bus id
// (payload "_event_id") it is emitted as an "id:" line so the browser
// remembers it and sends Last-Event-ID on reconnect, which drives the
// replay path (plan-06-11 F9). Ephemeral events have no id.
func writeFrame(w http.ResponseWriter, e Event) {
	frame, _ := json.Marshal(e)
	if id, ok := eventID(e.Payload); ok {
		_, _ = w.Write([]byte("id: " + strconv.FormatInt(id, 10) + "\n"))
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(frame)
	_, _ = w.Write([]byte("\n\n"))
}

func eventID(p map[string]any) (int64, bool) {
	switch v := p["_event_id"].(type) {
	case int64:
		return v, v > 0
	case int:
		return int64(v), v > 0
	case float64:
		return int64(v), v > 0
	case json.Number:
		n, err := v.Int64()
		return n, err == nil && n > 0
	}
	return 0, false
}

// parseLastEventID extracts the client's reconnect cursor from the
// standard SSE `Last-Event-ID` header, falling back to the
// `?last_event_id=` query param (the WS-fallback transport can't set
// the header). An unparseable/absent value is 0 → no replay.
func parseLastEventID(r *http.Request) int64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("last_event_id")
	}
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// PublishFromCtx routes an event into the local hub. The cross-replica
// publish path goes through eventbus.Bus.Publish (durable insert →
// NOTIFY → every replica's LISTEN loop fans out to its own hub); this
// helper remains the single-process direct-fan-out shim for the
// dev/test path where no bus is wired.
func PublishFromCtx(_ context.Context, hub *Hub, channel, typ string, payload map[string]any) {
	hub.Publish(channel, Event{Type: typ, At: time.Now().UTC(), Payload: payload})
}
