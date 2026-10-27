package graphql

// Subscription fields over the Story 7.16 event hub: jobUpdates reads
// channel "jobs", libraryEvents(libraryId) "library:<id>" and
// playbackUpdates(videoId) "playback:<id>" — the channels /ws/jobs,
// /ws/library/{id} and /ws/playback/{video_id} serve, so a GraphQL
// subscriber sees the same events as the SSE one (the cross-replica bus
// fans every replica's events into this hub). The transport is
// graphql-transport-ws on GET /graphql (serveWS); like the SSE routes it
// needs an authenticated caller and nothing more.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	gql "github.com/graph-gophers/graphql-go"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// eventEnvelope is the JSON a libraryEvents / playbackUpdates
// subscriber receives: the SSE frame's {type, at, payload}.
func eventEnvelope(e ws.Event) JSON {
	m := map[string]any{"type": e.Type, "at": e.At.UTC().Format(time.RFC3339Nano)}
	if e.Payload != nil {
		m["payload"] = e.Payload
	}
	return JSON{V: m}
}

// subscribe streams a hub channel until ctx ends or the hub drops the
// subscriber (slow consumer), mapping each event with fn (nil = skip).
func subscribe[T any](ctx context.Context, hub *ws.Hub, channel string, fn func(ws.Event) (T, bool)) (<-chan T, error) {
	if hub == nil {
		return nil, &restError{Status: http.StatusServiceUnavailable, Title: "subscriptions unavailable", Detail: "no event hub"}
	}
	if principal.FromContext(ctx) == nil {
		return nil, &restError{Status: http.StatusUnauthorized, Title: "unauthorized", Detail: "auth required"}
	}
	sub := hub.Subscribe(channel)
	out := make(chan T, 16)
	go func() {
		defer close(out)
		defer hub.Unsubscribe(channel, sub)
		defer safego.Recover(nil, "graphql_subscribe")
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.Done:
				return
			case e := <-sub.C:
				v, ok := fn(e)
				if !ok {
					continue
				}
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// JobUpdates: every "jobs" event naming a job yields that job as GET
// /api/jobs/{id} returns it now.
func (q *root) JobUpdates(ctx context.Context) (<-chan *job, error) {
	return subscribe(ctx, q.rs.Hub, "jobs", func(e ws.Event) (*job, bool) {
		id := jobIDOf(e.Payload)
		if id == "" {
			return nil, false
		}
		var j job
		if err := q.get(ctx, "/api/jobs/"+seg(ID(id)), nil, &j); err != nil {
			return nil, false
		}
		return &j, true
	})
}

// LibraryEvents: channel "library:<id>", for a library the caller can
// read (as /ws/library/{id}); any other id is not found.
func (q *root) LibraryEvents(ctx context.Context, args struct{ LibraryID ID }) (<-chan JSON, error) {
	p := principal.FromContext(ctx)
	if p == nil || (!p.IsAdmin && !p.AccessAllLibraries && !p.HasLibrary(string(args.LibraryID))) {
		return nil, errors.New("library " + string(args.LibraryID) + " not found")
	}
	return subscribe(ctx, q.rs.Hub, "library:"+string(args.LibraryID), func(e ws.Event) (JSON, bool) {
		return eventEnvelope(e), true
	})
}

// PlaybackUpdates: channel "playback:<video id>", for a video the caller
// can read: GET /api/videos/{id} decides (the REST library rule).
func (q *root) PlaybackUpdates(ctx context.Context, args struct{ VideoID ID }) (<-chan JSON, error) {
	if p := principal.FromContext(ctx); p == nil || (!p.IsAdmin && !p.AccessAllLibraries) {
		var v struct {
			ID string `json:"id"`
		}
		if err := q.get(ctx, "/api/videos/"+seg(args.VideoID), nil, &v); err != nil || v.ID == "" {
			return nil, errors.New("video " + string(args.VideoID) + " not found")
		}
	}
	return subscribe(ctx, q.rs.Hub, "playback:"+string(args.VideoID), func(e ws.Event) (JSON, bool) {
		return eventEnvelope(e), true
	})
}

// jobIDOf finds the job a "jobs" event is about: job_id, a JobRef under
// "job", or the id of a job.* event.
func jobIDOf(p map[string]any) string {
	str := func(v any) string {
		switch x := v.(type) {
		case string:
			return x
		case float64:
			return strconv.FormatInt(int64(x), 10)
		case int:
			return strconv.Itoa(x)
		case int64:
			return strconv.FormatInt(x, 10)
		case json.Number:
			return x.String()
		}
		return ""
	}
	if s := str(p["job_id"]); s != "" {
		return s
	}
	if j, ok := p["job"].(map[string]any); ok {
		if s := str(j["id"]); s != "" {
			return s
		}
	}
	return str(p["id"])
}

// ---------- graphql-transport-ws ----------

const wsProtocol = "graphql-transport-ws"

type wsMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// serveWS runs one graphql-transport-ws connection. Only subscription
// operations are taken here (queries and mutations go over POST, where
// the CSRF guard and the Idempotency-Key replay apply).
func (h *Handler) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{wsProtocol}})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	if conn.Subprotocol() != wsProtocol {
		_ = conn.Close(4406, "subprotocol must be "+wsProtocol)
		return
	}
	ctx, cancel := context.WithCancel(withOuter(r.Context(), r))
	defer cancel()

	var mu sync.Mutex
	send := func(m wsMessage) {
		b, _ := json.Marshal(m)
		mu.Lock()
		defer mu.Unlock()
		_ = conn.Write(ctx, websocket.MessageText, b)
	}
	subs := map[string]context.CancelFunc{}
	var subsMu sync.Mutex
	acked := false

	initTimer := time.AfterFunc(10*time.Second, func() {
		if !acked {
			_ = conn.Close(4408, "connection initialisation timeout")
		}
	})
	defer initTimer.Stop()

	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var m wsMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			_ = conn.Close(4400, "invalid message")
			return
		}
		switch m.Type {
		case "connection_init":
			if acked {
				_ = conn.Close(4429, "too many initialisation requests")
				return
			}
			acked = true
			send(wsMessage{Type: "connection_ack"})
		case "ping":
			send(wsMessage{Type: "pong"})
		case "pong":
		case "subscribe":
			if !acked {
				_ = conn.Close(4401, "unauthorized")
				return
			}
			var p gqlRequest
			if err := json.Unmarshal(m.Payload, &p); err != nil || m.ID == "" {
				_ = conn.Close(4400, "invalid subscribe message")
				return
			}
			subsMu.Lock()
			if _, dup := subs[m.ID]; dup {
				subsMu.Unlock()
				_ = conn.Close(4409, "subscriber for "+m.ID+" already exists")
				return
			}
			subCtx, subCancel := context.WithCancel(ctx)
			subs[m.ID] = subCancel
			subsMu.Unlock()
			go func(id string, p gqlRequest) {
				defer func() {
					subsMu.Lock()
					delete(subs, id)
					subsMu.Unlock()
					subCancel()
				}()
				defer safego.Recover(nil, "graphql_subscription")
				h.runSubscription(subCtx, id, p, send)
			}(m.ID, p)
		case "complete":
			subsMu.Lock()
			if c, ok := subs[m.ID]; ok {
				c()
			}
			subsMu.Unlock()
		default:
			_ = conn.Close(4400, "unknown message type")
			return
		}
	}
}

func (h *Handler) runSubscription(ctx context.Context, id string, p gqlRequest, send func(wsMessage)) {
	sendErrs := func(errs any) {
		b, _ := json.Marshal(errs)
		send(wsMessage{ID: id, Type: "error", Payload: b})
	}
	register, _, perrs := h.resolvePersisted(ctx, &p)
	if perrs != nil {
		sendErrs(perrs)
		return
	}
	plan, errs := h.analyse(p.Query, p.OperationName, p.Variables)
	if errs != nil {
		sendErrs(errs)
		return
	}
	if register {
		h.registerPersisted(ctx, p.Query)
	}
	if plan.operation != "subscription" {
		sendErrs([]map[string]any{{"message": "only subscriptions run over the WebSocket; send queries and mutations as POST /graphql",
			"extensions": map[string]any{"code": "bad-request"}}})
		return
	}
	ch, err := h.schema().Subscribe(ctx, p.Query, p.OperationName, p.Variables)
	if err != nil {
		sendErrs([]map[string]any{{"message": err.Error(), "extensions": map[string]any{"code": "bad-request"}}})
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-ch:
			if !ok {
				send(wsMessage{ID: id, Type: "complete"})
				return
			}
			resp, _ := v.(*gql.Response)
			if resp == nil {
				continue
			}
			b, _ := json.Marshal(resp)
			send(wsMessage{ID: id, Type: "next", Payload: b})
		}
	}
}
