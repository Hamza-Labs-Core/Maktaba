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
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Event is the AC-3 envelope: ``{type, at, ...payload}``.
type Event struct {
	Type    string         `json:"type"`
	At      time.Time      `json:"at"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Hub fan-outs events to subscribers keyed by ``channel``. Each
// subscriber has a bounded channel — once full, the connection is
// closed with 1011 ``slow-consumer`` per AC-4.
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
// drain ``s.C`` quickly; once 1000 events queue up the hub closes the
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

// Handler exposes the SSE endpoints.
type Handler struct {
	Hub *Hub
}

// Mount wires the SSE routes. WebSocket is intentionally absent here;
// the production binary can wrap each route with an upgrader of its
// choice.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/ws/jobs", h.serveSSE("jobs"))
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

	sub := h.Hub.Subscribe(channel)
	defer h.Hub.Unsubscribe(channel, sub)

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	ctx := r.Context()
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
			frame, _ := json.Marshal(e)
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(frame)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

// PublishFromCtx is a tiny helper used by Postgres listener loops to
// route NOTIFY payloads into the right channel. Currently a no-op
// adapter shape; left in place so service bootstrap can wire it.
func PublishFromCtx(_ context.Context, hub *Hub, channel, typ string, payload map[string]any) {
	hub.Publish(channel, Event{Type: typ, At: time.Now().UTC(), Payload: payload})
}
