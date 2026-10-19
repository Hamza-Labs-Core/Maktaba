package agent

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// LLM error codes (design-agent.md §5.1, §8.2).
const (
	LLMNoModel      = "no_model"
	LLMNoCapacity   = "no_capacity"
	LLMMachineGone  = "machine_gone"
	LLMRuntimeError = "runtime_error"
	LLMCancelled    = "cancelled"
	LLMTooLarge     = "too_large"
	LLMTimeout      = "timeout"
	LLMBadRequest   = "bad_request"
)

// LLMError is llm.done's error: {code, detail}; a zero Code is success.
type LLMError struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

func llmErr(code string) LLMError { return LLMError{Code: code} }

var llmOps = map[string]bool{"chat": true, "generate": true, "embed": true}

// LLMRequestFrame is an llm.request frame.
type LLMRequestFrame struct {
	T      string          `json:"t"`
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Op     string          `json:"op"`
	Body   json.RawMessage `json:"body"`
	Stream bool            `json:"stream"`
}

// Sink receives a request's output. Calls come from the router with no
// lock held, in order; Done is the last call.
type Sink interface {
	Chunk(data json.RawMessage)
	Done(usage json.RawMessage, err LLMError)
}

// LLMRequest is a request to the cluster.
type LLMRequest struct {
	ClientID    string // the requester's own id
	Model       string
	Op          string
	Body        json.RawMessage
	Stream      bool
	FromSession string // "" for the HTTP door
	FromIP      string
	Sink        Sink

	id         string
	enqueuedAt time.Time
	runner     string
	runtime    string
	lastActive time.Time
	timer      *time.Timer
	queued     bool
	cancelled  bool
}

// ID is the router's id of the request (the one the runner sees).
func (r *LLMRequest) ID() string { return r.id }

// LLMEvent is the browsers' `llm` message.
type LLMEvent struct {
	ID          string          `json:"id"`
	Model       string          `json:"model"`
	Op          string          `json:"op"`
	FromSession *string         `json:"from_session"`
	ToSession   *string         `json:"to_session"`
	State       string          `json:"state"` // queued | started | done | failed
	Usage       json.RawMessage `json:"usage"`
	Error       *string         `json:"error"`
}

// RouterConfig wires a Router.
type RouterConfig struct {
	QueueTimeout time.Duration
	MaxParallel  int
	IdleTimeout  time.Duration
	Now          func() time.Time
	// Send delivers a frame to a session: ErrFrameTooLarge when it cannot
	// travel, ErrGone (or another error) when the session is gone.
	Send func(session string, frame any) error
	// Notify receives request state changes; ModelsChanged is called
	// when the cluster's models change.
	Notify         func(LLMEvent)
	ModelsChanged  func()
	DefaultRuntime string
}

type llmModel struct {
	name   string
	loaded bool
}

type llmRuntime struct {
	key         string // provider|base_url
	provider    string
	baseURL     string
	maxParallel int
	inFlight    int
	models      []llmModel
}

type llmHost struct {
	session  string
	ip       string
	since    time.Time
	runtimes map[string]*llmRuntime
	order    []string // runtime keys in the order sent
}

// Router routes LLM requests to the sessions that hold the models: loaded
// first, then the least loaded relative to max_parallel, then the
// requester's own machine; FIFO per model when everyone is busy.
type Router struct {
	cfg RouterConfig

	mu     sync.Mutex
	hosts  map[string]*llmHost
	queues map[string][]*LLMRequest // by requested model
	active map[string]*LLMRequest
}

// NewRouter returns an empty router.
func NewRouter(cfg RouterConfig) *Router {
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = DefaultQueueTimeout
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = DefaultMaxParallel
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = DefaultIdleTimeout
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Router{cfg: cfg, hosts: map[string]*llmHost{}, queues: map[string][]*LLMRequest{}, active: map[string]*LLMRequest{}}
}

type runtimeWire struct {
	Provider    string `json:"provider"`
	BaseURL     string `json:"base_url"`
	MaxParallel int    `json:"max_parallel"`
	Models      []struct {
		Name   string `json:"name"`
		Loaded bool   `json:"loaded"`
	} `json:"models"`
}

// UpdateHost replaces a session's runtimes (its llm.models), keeping the
// in-flight counts of runtimes it still has.
func (rt *Router) UpdateHost(session, ip string, since time.Time, runtimes json.RawMessage) {
	var wire []runtimeWire
	_ = json.Unmarshal(runtimes, &wire)
	rt.mu.Lock()
	old := rt.hosts[session]
	host := &llmHost{session: session, ip: ip, since: since, runtimes: map[string]*llmRuntime{}}
	for _, w := range wire {
		key := w.Provider + "|" + w.BaseURL
		if _, dup := host.runtimes[key]; dup {
			continue
		}
		r := &llmRuntime{key: key, provider: w.Provider, baseURL: w.BaseURL, maxParallel: w.MaxParallel}
		if r.maxParallel <= 0 {
			r.maxParallel = rt.cfg.MaxParallel
		}
		for _, m := range w.Models {
			if m.Name != "" {
				r.models = append(r.models, llmModel{name: m.Name, loaded: m.Loaded})
			}
		}
		if old != nil && old.runtimes[key] != nil {
			r.inFlight = old.runtimes[key].inFlight
		}
		host.runtimes[key] = r
		host.order = append(host.order, key)
	}
	// requests running on a runtime that disappeared keep counting
	// against nothing; they finish or fail on their own
	if len(host.runtimes) == 0 {
		delete(rt.hosts, session)
	} else {
		rt.hosts[session] = host
	}
	start := rt.pumpLocked()
	rt.mu.Unlock()
	rt.dispatch(start)
	if rt.cfg.ModelsChanged != nil {
		rt.cfg.ModelsChanged()
	}
}

func modelMatch(have, want string) bool {
	if strings.EqualFold(have, want) {
		return true
	}
	// Ollama names "llama3" and "llama3:latest" the same model
	h, w := strings.ToLower(have), strings.ToLower(want)
	return h == w+":latest" || w == h+":latest"
}

func (rt *Router) hasModelLocked(model string) bool {
	for _, h := range rt.hosts {
		for _, r := range h.runtimes {
			for _, m := range r.models {
				if modelMatch(m.name, model) {
					return true
				}
			}
		}
	}
	return false
}

type candidate struct {
	host    *llmHost
	rt      *llmRuntime
	loaded  bool
	load    float64
	sameMac bool
}

// pickLocked chooses where req runs, or nil when every holder is busy.
func (rt *Router) pickLocked(req *LLMRequest) *candidate {
	var cs []candidate
	for _, h := range rt.hosts {
		for _, key := range h.order {
			r := h.runtimes[key]
			if r.inFlight >= r.maxParallel {
				continue
			}
			for _, m := range r.models {
				if modelMatch(m.name, req.Model) {
					cs = append(cs, candidate{host: h, rt: r, loaded: m.loaded,
						load: float64(r.inFlight) / float64(r.maxParallel), sameMac: req.FromIP != "" && h.ip == req.FromIP})
					break
				}
			}
		}
	}
	if len(cs) == 0 {
		return nil
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.loaded != b.loaded {
			return a.loaded
		}
		if a.load != b.load {
			return a.load < b.load
		}
		if a.sameMac != b.sameMac {
			return a.sameMac
		}
		if !a.host.since.Equal(b.host.since) {
			return a.host.since.Before(b.host.since)
		}
		if a.host.session != b.host.session {
			return a.host.session < b.host.session
		}
		return a.rt.key < b.rt.key
	})
	return &cs[0]
}

func (rt *Router) assignLocked(req *LLMRequest, c *candidate) {
	c.rt.inFlight++
	req.runner, req.runtime = c.host.session, c.rt.key
	req.lastActive = rt.cfg.Now()
	rt.active[req.id] = req
}

// pumpLocked places queued requests (oldest head first) while there is
// room, and returns the requests to start.
func (rt *Router) pumpLocked() []*LLMRequest {
	var start []*LLMRequest
	for {
		var heads []*LLMRequest
		for _, q := range rt.queues {
			if len(q) > 0 {
				heads = append(heads, q[0])
			}
		}
		sort.Slice(heads, func(i, j int) bool { return heads[i].enqueuedAt.Before(heads[j].enqueuedAt) })
		placed := false
		for _, req := range heads {
			if c := rt.pickLocked(req); c != nil {
				rt.dequeueLocked(req)
				rt.assignLocked(req, c)
				start = append(start, req)
				placed = true
				break
			}
		}
		if !placed {
			return start
		}
	}
}

func (rt *Router) dequeueLocked(req *LLMRequest) {
	key := strings.ToLower(req.Model)
	q := rt.queues[key]
	for i, r := range q {
		if r == req {
			q = append(q[:i:i], q[i+1:]...)
			break
		}
	}
	if len(q) == 0 {
		delete(rt.queues, key)
	} else {
		rt.queues[key] = q
	}
	req.queued = false
	if req.timer != nil {
		req.timer.Stop()
	}
}

// dispatch sends started requests to their runners (no lock held).
func (rt *Router) dispatch(start []*LLMRequest) {
	for _, req := range start {
		rt.notify(req, "started", nil, "")
		if err := rt.cfg.Send(req.runner, requestFrame(req.id, req)); err != nil {
			code := LLMMachineGone
			if errors.Is(err, ErrFrameTooLarge) {
				code = LLMTooLarge
			}
			rt.fail(req.id, code)
		}
	}
}

func (rt *Router) notify(req *LLMRequest, state string, usage json.RawMessage, errCode string) {
	if rt.cfg.Notify == nil {
		return
	}
	ev := LLMEvent{ID: req.id, Model: req.Model, Op: req.Op, FromSession: nullable(req.FromSession),
		ToSession: nullable(req.runner), State: state, Usage: usage}
	if errCode != "" {
		ev.Error = &errCode
	}
	rt.cfg.Notify(ev)
}

// Submit routes a request: at once when a holder has room, queued when
// all are busy, failed with no_model when nobody holds it.
func (rt *Router) Submit(req *LLMRequest) {
	req.id = "llm-" + newID()
	if req.Body == nil {
		req.Body = json.RawMessage("{}")
	}
	if req.Model == "" || !llmOps[req.Op] {
		rt.notify(req, "failed", nil, LLMBadRequest)
		req.Sink.Done(nil, llmErr(LLMBadRequest))
		return
	}
	rt.mu.Lock()
	if !rt.hasModelLocked(req.Model) {
		rt.mu.Unlock()
		rt.notify(req, "failed", nil, LLMNoModel)
		req.Sink.Done(nil, llmErr(LLMNoModel))
		return
	}
	req.enqueuedAt = rt.cfg.Now()
	key := strings.ToLower(req.Model)
	rt.queues[key] = append(rt.queues[key], req)
	req.queued = true
	start := rt.pumpLocked()
	stillQueued := req.queued
	if stillQueued {
		id := req.id
		req.timer = time.AfterFunc(rt.cfg.QueueTimeout, func() { rt.expire(id) })
	}
	rt.mu.Unlock()
	if stillQueued {
		rt.notify(req, "queued", nil, "")
	}
	rt.dispatch(start)
}

// expire fails a request still queued after the queue timeout.
func (rt *Router) expire(id string) {
	rt.mu.Lock()
	var found *LLMRequest
	for _, q := range rt.queues {
		for _, r := range q {
			if r.id == id {
				found = r
			}
		}
	}
	if found == nil {
		rt.mu.Unlock()
		return
	}
	rt.dequeueLocked(found)
	rt.mu.Unlock()
	rt.notify(found, "failed", nil, LLMNoCapacity)
	found.Sink.Done(nil, llmErr(LLMNoCapacity))
}

// Cancel is the requester giving up (router id): a queued request is
// dropped; a running one gets llm.cancel on its runner, which still ends it
// with llm.done (its output is discarded meanwhile).
func (rt *Router) Cancel(id string) {
	rt.mu.Lock()
	for _, q := range rt.queues {
		for _, r := range q {
			if r.id == id {
				rt.dequeueLocked(r)
				rt.mu.Unlock()
				rt.notify(r, "failed", nil, LLMCancelled)
				return
			}
		}
	}
	req := rt.active[id]
	if req == nil || req.cancelled {
		rt.mu.Unlock()
		return
	}
	req.cancelled = true
	runner := req.runner
	rt.mu.Unlock()
	_ = rt.cfg.Send(runner, map[string]any{"t": "llm.cancel", "id": id})
}

// requestFrame is the llm.request a runner receives.
func requestFrame(id string, req *LLMRequest) map[string]any {
	return map[string]any{"t": "llm.request", "id": id, "model": req.Model, "op": req.Op, "body": req.Body,
		"stream": req.Stream}
}

// RequestFits reports whether req's llm.request frame fits in a sealed
// frame (the router's ids are all the same length).
func RequestFits(req *LLMRequest) bool {
	b, err := json.Marshal(requestFrame("llm-"+newID(), req))
	return err == nil && fits(len(b))
}

// CancelFrom is a process's llm.cancel for its own request id.
func (rt *Router) CancelFrom(session, clientID string) {
	rt.mu.Lock()
	var id string
	for _, q := range rt.queues {
		for _, r := range q {
			if r.FromSession == session && r.ClientID == clientID {
				id = r.id
			}
		}
	}
	for _, r := range rt.active {
		if r.FromSession == session && r.ClientID == clientID {
			id = r.id
		}
	}
	rt.mu.Unlock()
	if id != "" {
		rt.Cancel(id)
	}
}

// Chunk forwards a runner's output.
func (rt *Router) Chunk(session, id string, data json.RawMessage) {
	rt.mu.Lock()
	req := rt.active[id]
	if req == nil || req.runner != session {
		rt.mu.Unlock()
		return
	}
	req.lastActive = rt.cfg.Now()
	cancelled := req.cancelled
	rt.mu.Unlock()
	if !cancelled {
		req.Sink.Chunk(data)
	}
}

// Done finishes a request on its runner's word.
func (rt *Router) Done(session, id string, usage json.RawMessage, e LLMError) {
	rt.mu.Lock()
	req := rt.active[id]
	if req == nil || req.runner != session {
		rt.mu.Unlock()
		return
	}
	start := rt.finishLocked(req)
	rt.mu.Unlock()
	state := "done"
	if e.Code != "" {
		state = "failed"
	}
	if len(usage) == 0 || string(usage) == "null" {
		usage = nil
	}
	rt.notify(req, state, usage, e.Code)
	req.Sink.Done(usage, e)
	rt.dispatch(start)
}

func (rt *Router) finishLocked(req *LLMRequest) []*LLMRequest {
	delete(rt.active, req.id)
	if h := rt.hosts[req.runner]; h != nil {
		if r := h.runtimes[req.runtime]; r != nil && r.inFlight > 0 {
			r.inFlight--
		}
	}
	return rt.pumpLocked()
}

// fail ends an active request with errCode.
func (rt *Router) fail(id, errCode string) {
	rt.mu.Lock()
	req := rt.active[id]
	if req == nil {
		rt.mu.Unlock()
		return
	}
	start := rt.finishLocked(req)
	rt.mu.Unlock()
	rt.notify(req, "failed", nil, errCode)
	req.Sink.Done(nil, llmErr(errCode))
	rt.dispatch(start)
}

// HostGone drops a session: requests running there fail with
// machine_gone; its own queued requests are dropped.
func (rt *Router) HostGone(session string) {
	rt.mu.Lock()
	_, had := rt.hosts[session]
	delete(rt.hosts, session)
	var gone []*LLMRequest
	for _, req := range rt.active {
		if req.runner == session {
			gone = append(gone, req)
		}
	}
	for _, req := range gone {
		delete(rt.active, req.id)
	}
	var dropped []*LLMRequest
	for _, q := range rt.queues {
		for _, r := range q {
			if r.FromSession == session {
				dropped = append(dropped, r)
			}
		}
	}
	for _, r := range dropped {
		rt.dequeueLocked(r)
	}
	// running requests whose requester left: their runners stop them
	var orphans []string
	for id, r := range rt.active {
		if r.FromSession == session && !r.cancelled {
			orphans = append(orphans, id)
		}
	}
	rt.mu.Unlock()
	for _, id := range orphans {
		rt.Cancel(id)
	}
	for _, req := range gone {
		rt.notify(req, "failed", nil, LLMMachineGone)
		req.Sink.Done(nil, llmErr(LLMMachineGone))
	}
	for _, req := range dropped {
		rt.notify(req, "failed", nil, "requester_gone")
	}
	if had && rt.cfg.ModelsChanged != nil {
		rt.cfg.ModelsChanged()
	}
}

// Sweep fails running requests silent for longer than the idle timeout.
func (rt *Router) Sweep() {
	now := rt.cfg.Now()
	rt.mu.Lock()
	var stale []string
	for id, req := range rt.active {
		if now.Sub(req.lastActive) > rt.cfg.IdleTimeout {
			stale = append(stale, id)
		}
	}
	rt.mu.Unlock()
	for _, id := range stale {
		rt.fail(id, LLMTimeout)
	}
}

// ModelInfo is one model in the cluster.
type ModelInfo struct {
	Model string        `json:"model"`
	Hosts []ModelHolder `json:"hosts"`
}

// ModelHolder is a runtime that holds a model.
type ModelHolder struct {
	Session     string `json:"session"`
	IP          string `json:"ip"`
	Provider    string `json:"provider"`
	BaseURL     string `json:"base_url"`
	Loaded      bool   `json:"loaded"`
	MaxParallel int    `json:"max_parallel"`
	InFlight    int    `json:"in_flight"`
}

// Models lists every model in the cluster and where it runs.
func (rt *Router) Models() []ModelInfo {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	by := map[string]*ModelInfo{}
	for _, h := range rt.hosts {
		for _, key := range h.order {
			r := h.runtimes[key]
			for _, m := range r.models {
				mi := by[m.name]
				if mi == nil {
					mi = &ModelInfo{Model: m.name}
					by[m.name] = mi
				}
				mi.Hosts = append(mi.Hosts, ModelHolder{Session: h.session, IP: h.ip, Provider: r.provider,
					BaseURL: r.baseURL, Loaded: m.loaded, MaxParallel: r.maxParallel, InFlight: r.inFlight})
			}
		}
	}
	out := make([]ModelInfo, 0, len(by))
	for _, mi := range by {
		sort.Slice(mi.Hosts, func(i, j int) bool {
			if mi.Hosts[i].Session != mi.Hosts[j].Session {
				return mi.Hosts[i].Session < mi.Hosts[j].Session
			}
			return mi.Hosts[i].BaseURL < mi.Hosts[j].BaseURL
		})
		out = append(out, *mi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// Snapshot is the llm topic's state: models, running and queued requests.
func (rt *Router) Snapshot() map[string]any {
	models := rt.Models()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	running := []LLMEvent{}
	for _, req := range rt.active {
		running = append(running, LLMEvent{ID: req.id, Model: req.Model, Op: req.Op,
			FromSession: nullable(req.FromSession), ToSession: nullable(req.runner), State: "started"})
	}
	sort.Slice(running, func(i, j int) bool { return running[i].ID < running[j].ID })
	queued := []LLMEvent{}
	for _, q := range rt.queues {
		for _, req := range q {
			queued = append(queued, LLMEvent{ID: req.id, Model: req.Model, Op: req.Op,
				FromSession: nullable(req.FromSession), State: "queued"})
		}
	}
	sort.Slice(queued, func(i, j int) bool { return queued[i].ID < queued[j].ID })
	return map[string]any{"requests": append(running, queued...), "models": models}
}

// sessionSink returns output to a requesting process. A chunk too large
// for the channel ends the request for the requester with too_large
// (what the runner still sends is dropped).
type sessionSink struct {
	s      *Session
	id     string
	mu     sync.Mutex
	failed bool
}

func (k *sessionSink) Chunk(data json.RawMessage) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failed {
		return
	}
	if errors.Is(k.s.sendErr(map[string]any{"t": "llm.chunk", "id": k.id, "data": data}), ErrFrameTooLarge) {
		k.failed = true
		k.s.send(map[string]any{"t": "llm.done", "id": k.id, "usage": nil,
			"error": LLMError{Code: LLMTooLarge, Detail: "a chunk is over the channel's frame limit"}})
	}
}

func (k *sessionSink) Done(usage json.RawMessage, e LLMError) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failed {
		return
	}
	f := map[string]any{"t": "llm.done", "id": k.id, "usage": usage}
	if usage == nil {
		f["usage"] = nil
	}
	if e.Code != "" {
		f["error"] = e
	}
	k.s.send(f)
}

// sendToSession is the router's way out.
func (h *Hub) sendToSession(session string, frame any) error {
	s := h.Session(session)
	if s == nil {
		return ErrGone
	}
	return s.sendErr(frame)
}
