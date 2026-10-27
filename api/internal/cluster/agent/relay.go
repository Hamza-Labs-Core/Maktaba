package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
)

// The extension service door over the channel (story 31.R):
//
//	C→S svc.request {id, ext, method, path, accept?, content_type?, body? (base64), timeout_ms?}
//	S→C svc.chunk   {id, data (base64)}            the answer's body, in order
//	S→C svc.done    {id, status, content_type, bytes, error? {code, detail}}  always last
//	C→S svc.cancel  {id}                            the caller gave up
//
// The door (api/internal/cluster/relay) checks the call and adds the
// secret; nothing here ever sees it.

const (
	maxRelaysPerSession = 32
	maxRelayID          = 64
	relayDoneWait       = 10 * time.Second
)

// CodeNoDoor: the home runs without the door.
const CodeNoDoor = "no_door"

type relayRequestFrame struct {
	ID          string `json:"id"`
	Ext         string `json:"ext"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Accept      string `json:"accept"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
	TimeoutMS   int64  `json:"timeout_ms"`
}

func (h *Hub) onRelayRequest(s *Session, raw []byte) {
	var f relayRequestFrame
	if json.Unmarshal(raw, &f) != nil || f.ID == "" || len(f.ID) > maxRelayID {
		return
	}
	done := func(res relay.Result) {
		frame := map[string]any{"t": "svc.done", "id": f.ID, "status": res.Status, "content_type": res.ContentType,
			"bytes": res.Bytes, "error": res.Err}
		ctx, cancel := context.WithTimeout(s.ctx, relayDoneWait)
		defer cancel()
		_ = s.sendWait(ctx, frame)
	}
	if h.cfg.Relay == nil {
		done(relay.Result{Err: &relay.Error{Code: CodeNoDoor, Detail: "the home runs without the extension relay"}})
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	if !s.addRelay(f.ID, cancel) {
		cancel()
		done(relay.Result{Err: &relay.Error{Code: relay.CodeBusy, Detail: "too many calls on this channel"}})
		return
	}
	go func() {
		defer cancel()
		defer s.dropRelay(f.ID)
		defer safego.Recover(h.log(), "cluster_relay")
		var body []byte
		if f.Body != "" {
			b, err := base64.StdEncoding.DecodeString(f.Body)
			if err != nil {
				done(relay.Result{Err: &relay.Error{Code: relay.CodeBadRequest, Detail: "body is not base64"}})
				return
			}
			body = b
		}
		caller := relay.Caller{Kind: s.Kind, NodeID: s.NodeID, Session: s.ID, WorkerID: s.WorkerID, IP: s.IP}
		if s.NodeName != nil {
			caller.NodeName = *s.NodeName
		}
		req := relay.Request{Ext: f.Ext, Method: f.Method, Target: f.Path, Accept: f.Accept,
			ContentType: f.ContentType, Body: body, Timeout: time.Duration(f.TimeoutMS) * time.Millisecond}
		res := h.cfg.Relay.Serve(ctx, caller, req, relaySink{s: s, id: f.ID})
		done(res)
	}()
}

// relaySink sends the answer's body as svc.chunk frames.
type relaySink struct {
	s  *Session
	id string
}

func (k relaySink) Chunk(ctx context.Context, data []byte) error {
	return k.s.sendWait(ctx, map[string]any{"t": "svc.chunk", "id": k.id,
		"data": base64.StdEncoding.EncodeToString(data)})
}

// errNotMember refuses a relayed call.
var errNotMember = errors.New("not an approved member")

// relayMember: a home process (it proved the home agent key), or a node
// that is approved now, was not revoked after this session began, and
// whose session is still live.
func (h *Hub) relayMember(ctx context.Context, c relay.Caller) error {
	s := h.Session(c.Session)
	if s == nil {
		return errNotMember
	}
	if s.Kind == "home" && s.NodeID == "" {
		return nil
	}
	if s.Kind != "node" || s.NodeID == "" || s.NodeID != c.NodeID {
		return errNotMember
	}
	h.mu.RLock()
	at, revoked := h.revokedAt[s.NodeID]
	h.mu.RUnlock()
	if revoked && !at.Before(s.ConnectedAt) {
		return errNotMember
	}
	if h.cfg.Store == nil {
		return errNotMember
	}
	n, err := h.cfg.Store.Node(ctx, s.NodeID)
	if err != nil || n.State != cluster.StateApproved {
		return errNotMember
	}
	return nil
}

// relayAudit writes each relayed call to the hub's events file.
func (h *Hub) relayAudit(c relay.Call) {
	fields := map[string]any{"event": "relay", "session": c.Session, "kind": c.Kind, "node_id": nullable(c.NodeID),
		"ip": c.IP, "worker_id": c.WorkerID, "ext": c.Ext, "method": c.Method, "path": c.Path, "status": c.Status,
		"bytes": c.Bytes, "ms": c.MS}
	if c.Error != "" {
		fields["error"] = c.Error
	}
	if c.Scrubbed > 0 {
		fields["scrubbed"] = c.Scrubbed
	}
	h.event(fields)
	if c.Error != "" && c.Error != relay.CodeCancelled {
		h.log().Info("cluster: relayed call refused or failed", "ext", c.Ext, "method", c.Method, "path", c.Path,
			"node_id", c.NodeID, "code", c.Error, "event", "cluster_relay_refused")
	}
}

// RelayHTTP implements GET /api/cluster/relay (admin): the door's status
// and its recent calls.
func (h *Hub) RelayHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if h.cfg.Relay == nil {
		common.WriteJSON(w, r, http.StatusOK, relay.Status{Extensions: []relay.ExtView{}, HomeOnly: []relay.HomeOnlyView{},
			Calls: []relay.Call{}, Totals: []relay.CallerTotals{}})
		return
	}
	common.WriteJSON(w, r, http.StatusOK, h.cfg.Relay.Status(r.Context()))
}

// ---------- session side ----------

func (s *Session) addRelay(id string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.relays == nil {
		s.relays = map[string]context.CancelFunc{}
	}
	if _, dup := s.relays[id]; dup || len(s.relays) >= maxRelaysPerSession {
		return false
	}
	s.relays[id] = cancel
	return true
}

func (s *Session) dropRelay(id string) {
	s.mu.Lock()
	delete(s.relays, id)
	s.mu.Unlock()
}

func (s *Session) cancelRelay(id string) {
	s.mu.Lock()
	cancel := s.relays[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// sendWait queues a frame, waiting while the session's queue is more than
// half full (a long answer must not crowd out pings and fill the queue,
// which would close the session as a slow consumer).
func (s *Session) sendWait(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if !fits(len(b)) {
		return ErrFrameTooLarge
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		if len(s.out) < cap(s.out)/2 {
			select {
			case s.out <- outFrame{payload: b}:
				return nil
			case <-s.ctx.Done():
				return ErrGone
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		select {
		case <-s.ctx.Done():
			return ErrGone
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
