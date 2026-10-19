package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Browser topics (design-agent.md §5). logs:<ip|node_id|session>:<level>
// is the per-viewer log tail.
const (
	TopicQueues    = "queues"
	TopicJobs      = "jobs"
	TopicWorkers   = "workers"
	TopicMetrics   = "metrics"
	TopicLibrary   = "library"
	TopicLLM       = "llm"
	TopicProcesses = "processes"
	logsPrefix     = "logs:"

	browserQueueCap = 1000
	browserMaxMsg   = 64 << 10
	browserPing     = 30 * time.Second
	maxSnapshotLogs = 500
)

var topicAdmin = map[string]bool{
	TopicQueues: false, TopicJobs: false, TopicWorkers: false, TopicLibrary: false,
	TopicMetrics: true, TopicLLM: true, TopicProcesses: true,
}

// sendQueue is a browser's bounded queue (design-agent.md §6.4). A
// message of a replacing kind (queues, workers, library) replaces one of
// the same kind still waiting; when full, the oldest other message
// (metrics, log, …) is dropped, and the drops are counted.
type sendQueue struct {
	mu      sync.Mutex
	items   []queued
	limit   int
	dropped int
	notify  chan struct{}
}

type queued struct {
	kind string // "" unless the message replaces its predecessor
	msg  []byte
}

func newSendQueue(capacity int) *sendQueue {
	return &sendQueue{limit: capacity, notify: make(chan struct{}, 1)}
}

func (q *sendQueue) push(b []byte) { q.pushKind("", b) }

func (q *sendQueue) pushKind(kind string, b []byte) {
	q.mu.Lock()
	replaced := false
	if kind != "" {
		for i := range q.items {
			if q.items[i].kind == kind {
				q.items[i].msg = b
				replaced = true
				break
			}
		}
	}
	if !replaced {
		if len(q.items) >= q.limit {
			drop := 0
			for i, it := range q.items {
				if it.kind == "" {
					drop = i
					break
				}
			}
			q.items = append(q.items[:drop], q.items[drop+1:]...)
			q.dropped++
		}
		q.items = append(q.items, queued{kind: kind, msg: b})
	}
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// take returns everything queued and the number dropped since the last
// take.
func (q *sendQueue) take() ([][]byte, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([][]byte, 0, len(q.items))
	for _, it := range q.items {
		out = append(out, it.msg)
	}
	q.items = nil
	d := q.dropped
	q.dropped = 0
	return out, d
}

// replacing are the message types where only the latest matters.
var replacing = map[string]bool{"queues": true, "workers": true, "library": true}

type logFilter struct {
	topic string
	sel   string
	rank  int
}

func (f logFilter) match(s *Session) bool {
	return f.sel == "*" || f.sel == s.ID || f.sel == s.IP || f.sel == IPDir(s.IP) || (s.NodeID != "" && f.sel == s.NodeID)
}

// parseLogsTopic parses logs:<selector>:<level>; the selector may itself
// hold ':' (an IPv6 address), so the level is the last part.
func parseLogsTopic(t string) (logFilter, bool) {
	rest := strings.TrimPrefix(t, logsPrefix)
	sel, level := rest, "info"
	if i := strings.LastIndexByte(rest, ':'); i >= 0 && levelRank(rest[i+1:]) > 0 {
		sel, level = rest[:i], rest[i+1:]
	}
	if sel == "" || len(sel) > 128 {
		return logFilter{}, false
	}
	return logFilter{topic: t, sel: sel, rank: levelRank(level)}, true
}

type browser struct {
	admin  bool
	user   string
	q      *sendQueue
	mu     sync.Mutex
	topics map[string]bool
	logs   []logFilter
}

func (b *browser) subscribed(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.topics[topic]
}

// outMsg is a hub → browser message: {type, topic?, at, data?}.
type outMsg struct {
	Type  string    `json:"type"`
	Topic string    `json:"topic,omitempty"`
	At    time.Time `json:"at"`
	Data  any       `json:"data,omitempty"`
}

func marshalMsg(typ, topic string, data any) []byte {
	b, err := json.Marshal(outMsg{Type: typ, Topic: topic, At: time.Now().UTC(), Data: data})
	if err != nil {
		return nil
	}
	return b
}

func (b *browser) send(typ, topic string, data any) {
	if m := marshalMsg(typ, topic, data); m != nil {
		b.q.push(m)
	}
}

// publish sends a message to every browser subscribed to topic.
func (h *Hub) publish(topic, typ string, data any) {
	h.mu.RLock()
	var targets []*browser
	for b := range h.browsers {
		if b.subscribed(topic) {
			targets = append(targets, b)
		}
	}
	h.mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	m := marshalMsg(typ, "", data)
	if m == nil {
		return
	}
	kind := ""
	if replacing[typ] {
		kind = typ
	}
	for _, b := range targets {
		b.q.pushKind(kind, m)
	}
}

func (h *Hub) publishLogs(s *Session, msgs []logMsg) {
	h.mu.RLock()
	browsers := make([]*browser, 0, len(h.browsers))
	for b := range h.browsers {
		browsers = append(browsers, b)
	}
	h.mu.RUnlock()
	for _, b := range browsers {
		b.mu.Lock()
		lowest := 0
		for _, f := range b.logs {
			if f.match(s) && (lowest == 0 || f.rank < lowest) {
				lowest = f.rank
			}
		}
		b.mu.Unlock()
		if lowest == 0 {
			continue
		}
		for _, m := range msgs {
			if m.rank >= lowest {
				b.send("log", "", m.data)
			}
		}
	}
}

func (h *Hub) subscribers(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for b := range h.browsers {
		if b.subscribed(topic) {
			n++
		}
	}
	return n
}

// resendSnapshot sends a fresh snapshot of topic to its subscribers.
func (h *Hub) resendSnapshot(topic string) {
	h.mu.RLock()
	var targets []*browser
	for b := range h.browsers {
		if b.subscribed(topic) {
			targets = append(targets, b)
		}
	}
	h.mu.RUnlock()
	if len(targets) == 0 {
		return
	}
	data := h.snapshot(topic)
	for _, b := range targets {
		b.send("snapshot", topic, data)
	}
}

// snapshot is the state of a topic for a new subscriber.
func (h *Hub) snapshot(topic string) any {
	if strings.HasPrefix(topic, logsPrefix) {
		f, _ := parseLogsTopic(topic)
		return map[string]any{"records": h.recentLogs(f)}
	}
	switch topic {
	case TopicQueues:
		h.mu.RLock()
		defer h.mu.RUnlock()
		return orNull(h.queues)
	case TopicWorkers:
		h.mu.RLock()
		defer h.mu.RUnlock()
		return orNull(h.workers)
	case TopicLibrary:
		h.mu.RLock()
		defer h.mu.RUnlock()
		return orNull(h.library)
	case TopicJobs:
		running := h.runningJobs()
		h.mu.RLock()
		defer h.mu.RUnlock()
		return map[string]any{"jobs": append([]json.RawMessage{}, h.jobs...), "running": running}
	case TopicMetrics:
		ps := h.Processes()
		out := make([]map[string]any, 0, len(ps))
		for _, p := range ps {
			out = append(out, map[string]any{"session": p.Session, "metrics": p.Metrics})
		}
		return map[string]any{"processes": out}
	case TopicLLM:
		return h.router.Snapshot()
	case TopicProcesses:
		return h.processesSnapshot()
	}
	return nil
}

func orNull(b json.RawMessage) json.RawMessage {
	if b == nil {
		return json.RawMessage("null")
	}
	return b
}

func (h *Hub) recentLogs(f logFilter) []json.RawMessage {
	h.mu.RLock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		if f.match(s) {
			ss = append(ss, s)
		}
	}
	h.mu.RUnlock()
	out := []json.RawMessage{}
	for _, s := range ss {
		s.mu.Lock()
		for _, m := range s.logs {
			if m.rank >= f.rank {
				out = append(out, m.data)
			}
		}
		s.mu.Unlock()
	}
	if len(out) > maxSnapshotLogs {
		out = out[len(out)-maxSnapshotLogs:]
	}
	return out
}

func (h *Hub) llmEvent(ev LLMEvent) { h.publish(TopicLLM, "llm", ev) }

func (h *Hub) llmModelsChanged() { h.resendSnapshot(TopicLLM) }

// ---------- the endpoint ----------

type inMsg struct {
	Op      string          `json:"op"`
	Topics  []string        `json:"topics"`
	Session string          `json:"session"`
	Name    string          `json:"name"`
	Args    json.RawMessage `json:"args"`
	ID      json.RawMessage `json:"id"`
}

// ServeBrowser implements GET /api/cluster/ws (signed-in users; the
// admin-only topics and commands need an admin).
func (h *Hub) ServeBrowser(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, errUnauthorized())
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.cfg.BrowserOrigins})
	if err != nil {
		return
	}
	c.SetReadLimit(browserMaxMsg)
	b := &browser{admin: p.IsAdmin, user: p.UserID, q: newSendQueue(browserQueueCap), topics: map[string]bool{}}
	h.mu.Lock()
	h.browsers[b] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.browsers, b)
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		h.browserRead(ctx, c, b)
	}()
	h.browserWrite(ctx, c, b)
	_ = c.Close(CloseNormal, "")
}

func (h *Hub) browserWrite(ctx context.Context, c *websocket.Conn, b *browser) {
	ping := time.NewTicker(browserPing)
	defer ping.Stop()
	write := func(m []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return c.Write(wctx, websocket.MessageText, m) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		case <-b.q.notify:
			msgs, dropped := b.q.take()
			if dropped > 0 && !write(marshalMsg("dropped", "", map[string]int{"count": dropped})) {
				return
			}
			for _, m := range msgs {
				if !write(m) {
					return
				}
			}
		}
	}
}

func (h *Hub) browserRead(ctx context.Context, c *websocket.Conn, b *browser) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m inMsg
		if json.Unmarshal(data, &m) != nil {
			b.send("error", "", map[string]any{"code": "bad_message"})
			continue
		}
		switch m.Op {
		case "ping":
			b.send("pong", "", nil)
		case "subscribe":
			h.subscribe(b, m.Topics)
		case "unsubscribe":
			h.unsubscribe(b, m.Topics)
		case "cmd":
			if !b.admin {
				b.send("cmd.result", "", map[string]any{"id": m.ID, "ok": false, "detail": "forbidden"})
				continue
			}
			go h.browserCmd(ctx, b, m)
		default:
			b.send("error", "", map[string]any{"code": "unknown_op", "op": m.Op})
		}
	}
}

func (h *Hub) subscribe(b *browser, topics []string) {
	for _, t := range topics {
		admin, known := topicAdmin[t]
		var lf logFilter
		if strings.HasPrefix(t, logsPrefix) {
			var ok bool
			lf, ok = parseLogsTopic(t)
			admin, known = true, ok
		}
		switch {
		case !known:
			b.send("error", "", map[string]any{"code": "unknown_topic", "topic": t})
			continue
		case admin && !b.admin:
			b.send("error", "", map[string]any{"code": "forbidden", "topic": t})
			continue
		}
		b.mu.Lock()
		b.topics[t] = true
		if lf.topic != "" {
			b.logs = append(b.logs, lf)
		}
		b.mu.Unlock()
		b.send("snapshot", t, h.snapshot(t))
		switch t {
		case TopicQueues:
			select {
			case h.queuesDirty <- struct{}{}:
			default:
			}
		case TopicWorkers:
			h.markWorkersDirty()
		}
	}
}

func (h *Hub) unsubscribe(b *browser, topics []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range topics {
		delete(b.topics, t)
		kept := b.logs[:0]
		for _, f := range b.logs {
			if f.topic != t {
				kept = append(kept, f)
			}
		}
		b.logs = kept
	}
}

func (h *Hub) browserCmd(ctx context.Context, b *browser, m inMsg) {
	res, err := h.Command(ctx, m.Session, m.Name, m.Args, "user:"+b.user)
	out := map[string]any{"id": m.ID, "ok": res.OK, "detail": res.Detail}
	if err != nil {
		out["ok"], out["detail"] = false, cmdErrCode(err)
	}
	b.send("cmd.result", "", out)
}

func cmdErrCode(err error) string {
	switch {
	case errors.Is(err, ErrNoSession):
		return "no_session"
	case errors.Is(err, ErrBadCommand):
		return "bad_command"
	case errors.Is(err, ErrCmdTimeout):
		return "timeout"
	}
	return "failed"
}

// processesSnapshot is the processes topic's snapshot and GET
// /api/cluster/processes.
func (h *Hub) processesSnapshot() map[string]any {
	var archive any
	if h.archiver != nil {
		archive = h.archiver.Stats()
	}
	return map[string]any{"processes": h.Processes(), "gone": h.Gone(), "files": h.Location(), "cache": h.cacheStats(),
		"archive": archive}
}
