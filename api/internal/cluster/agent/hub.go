package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
)

// Defaults of design-agent.md §3, §4, §6 and §7.
const (
	DefaultLogLevel        = "info"
	DefaultMetricsEvery    = 2
	DefaultRetentionDays   = 14
	DefaultFileMB          = 256
	DefaultQueueTimeout    = 300 * time.Second
	DefaultMaxParallel     = 1
	DefaultIdleTimeout     = 10 * time.Minute
	DefaultAgentRatePerMin = 60

	recentJobs    = 200
	sessionLogs   = 500
	sessionOutCap = 1024
)

// Timings (variables so tests can shorten them).
var (
	PingEvery     = 15 * time.Second
	SilenceLimit  = 45 * time.Second
	HandshakeTime = 10 * time.Second
	CmdTimeout    = 10 * time.Second

	queuesTick      = 2 * time.Second
	queuesMinGap    = time.Second
	queuesFileEvery = 10 * time.Second
	libraryEvery    = 5 * time.Minute
	locateEvery     = 10 * time.Minute
	sweepEvery      = time.Hour
)

// NodeStore is the slice of the phase-1 machine registry the hub reads:
// a node's state and pairing key (cluster.MemoryStore and
// cluster.PGStore satisfy it).
type NodeStore interface {
	Node(ctx context.Context, id string) (cluster.Node, error)
}

// Config wires a Hub.
type Config struct {
	Store NodeStore
	// HomeKey authenticates the home's own processes; nil refuses them.
	HomeKey []byte
	// LogLevel is MAKTABA_CLUSTER_LOG_LEVEL (sent in welcome).
	LogLevel     string
	MetricsEvery int
	// HomeAddr is the home's folder / address ("" → LANAddress()).
	HomeAddr string
	// Locator finds the cluster directory; nil writes no files.
	Locator       *Locator
	RetentionDays int
	FileMaxBytes  int64

	// Queues builds the queue snapshot (the Processing page's data);
	// Workers the GET /api/workers list; Library the library numbers.
	// nil: that topic stays empty.
	Queues  func(ctx context.Context) (queues.Snapshot, error)
	Workers func(ctx context.Context) ([]queues.Worker, error)
	Library func(ctx context.Context) (*LibrarySnapshot, error)

	LLMQueueTimeout time.Duration
	LLMMaxParallel  int
	LLMIdleTimeout  time.Duration

	// AgentRatePerMin caps connection attempts per IP on
	// /api/cluster/agent (negative: no limit).
	AgentRatePerMin int
	// BrowserOrigins are extra Origin patterns allowed on /api/cluster/ws
	// (the API's own origin is always allowed).
	BrowserOrigins []string

	Now    func() time.Time
	Logger *slog.Logger
}

// Hub is the live centre of the cluster.
type Hub struct {
	cfg     Config
	files   *FileWriter
	router  *Router
	sweeper *Sweeper
	homeIP  string

	mu        sync.RWMutex
	sessions  map[string]*Session
	revokedAt map[string]time.Time
	browsers  map[*browser]struct{}
	loc       Location
	jobs      []json.RawMessage // recent job messages (data of type "job")
	queues    json.RawMessage
	workers   json.RawMessage
	library   json.RawMessage

	queuesDirty  chan struct{}
	workersDirty chan struct{}
}

// New builds a hub; Run starts its loops.
func New(cfg Config) *Hub {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.LogLevel == "" || levelRank(cfg.LogLevel) == 0 {
		cfg.LogLevel = DefaultLogLevel
	}
	if cfg.MetricsEvery <= 0 {
		cfg.MetricsEvery = DefaultMetricsEvery
	}
	if cfg.RetentionDays == 0 {
		cfg.RetentionDays = DefaultRetentionDays
	}
	if cfg.FileMaxBytes == 0 {
		cfg.FileMaxBytes = DefaultFileMB << 20
	}
	if cfg.AgentRatePerMin == 0 {
		cfg.AgentRatePerMin = DefaultAgentRatePerMin
	}
	if cfg.HomeAddr == "" {
		cfg.HomeAddr = LANAddress()
	}
	h := &Hub{
		cfg:          cfg,
		homeIP:       cfg.HomeAddr,
		sessions:     map[string]*Session{},
		revokedAt:    map[string]time.Time{},
		browsers:     map[*browser]struct{}{},
		queuesDirty:  make(chan struct{}, 1),
		workersDirty: make(chan struct{}, 1),
	}
	h.files = NewFileWriter(8192, cfg.FileMaxBytes, cfg.Now, cfg.Logger)
	h.sweeper = &Sweeper{Days: cfg.RetentionDays, Now: cfg.Now, LiveIPs: h.liveIPDirs}
	h.router = NewRouter(RouterConfig{
		QueueTimeout:   cfg.LLMQueueTimeout,
		MaxParallel:    cfg.LLMMaxParallel,
		IdleTimeout:    cfg.LLMIdleTimeout,
		Now:            cfg.Now,
		Send:           h.sendToSession,
		Notify:         h.llmEvent,
		ModelsChanged:  h.llmModelsChanged,
		DefaultRuntime: "",
	})
	return h
}

// Router is the hub's LLM router.
func (h *Hub) Router() *Router { return h.router }

// HomeIP is the home's folder / address.
func (h *Hub) HomeIP() string { return h.homeIP }

func (h *Hub) now() time.Time { return h.cfg.Now().UTC() }

func (h *Hub) log() *slog.Logger { return h.cfg.Logger }

// Location is where the cluster files are now.
func (h *Hub) Location() Location {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.loc
}

// Run starts the loops (file writer, location, queues, workers, library,
// retention, LLM sweeps) and blocks until ctx ends; then it closes every
// session.
func (h *Hub) Run(ctx context.Context) {
	var wg sync.WaitGroup
	start := func(f func(context.Context)) {
		wg.Add(1)
		go func() { defer wg.Done(); f(ctx) }()
	}
	h.Locate(ctx)
	start(h.files.Run)
	start(h.locateLoop)
	start(h.queuesLoop)
	start(h.workersLoop)
	start(h.libraryLoop)
	start(h.sweepLoop)
	start(func(ctx context.Context) {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.router.Sweep()
			}
		}
	})
	<-ctx.Done()
	h.mu.RLock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.RUnlock()
	for _, s := range ss {
		s.closeWith(statusGoingAway, "home shutting down")
	}
	wg.Wait()
}

// Locate resolves the cluster directory now and points the writer at it;
// a change is recorded in the events file and re-sent to process viewers.
func (h *Hub) Locate(ctx context.Context) Location {
	var loc Location
	if h.cfg.Locator == nil {
		loc = Location{Reason: strPtr("no cluster directory configured")}
	} else {
		loc = h.cfg.Locator.Resolve(ctx)
	}
	h.mu.Lock()
	changed := !loc.equal(h.loc)
	h.loc = loc
	h.mu.Unlock()
	h.files.SetTarget(loc.Dir, IPDir(h.homeIP))
	if changed {
		reason := ""
		if loc.Reason != nil {
			reason = *loc.Reason
		}
		if loc.OnShare {
			h.log().Info("cluster: files on the share", "dir", loc.Dir, "event", "cluster_files_dir")
		} else {
			h.log().Warn("cluster: files not on the share", "dir", loc.Dir, "reason", reason, "event", "cluster_files_dir")
		}
		h.event(map[string]any{"event": "files_dir", "dir": loc.Dir, "on_share": loc.OnShare, "reason": loc.Reason})
		h.resendSnapshot(TopicProcesses)
	}
	return loc
}

func (h *Hub) locateLoop(ctx context.Context) {
	t := time.NewTicker(locateEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Locate(ctx)
		}
	}
}

func (h *Hub) sweepLoop(ctx context.Context) {
	sweep := func() {
		loc := h.Location()
		if n, err := h.sweeper.Sweep(loc.Dir, IPDir(h.homeIP)); err != nil {
			h.log().Warn("cluster: retention sweep failed", "err", err, "event", "cluster_sweep_failed")
		} else if n > 0 {
			h.log().Info("cluster: expired cluster files removed", "count", n, "event", "cluster_sweep")
		}
	}
	sweep()
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

func (h *Hub) liveIPDirs() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := map[string]bool{}
	for _, s := range h.sessions {
		out[IPDir(s.IP)] = true
	}
	return out
}

// event appends one line to the hub's events file.
func (h *Hub) event(fields map[string]any) {
	fields["ts"] = h.now().Format(time.RFC3339Nano)
	h.files.Write(KindEvents, fields)
}

// ---------- registry ----------

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// register adds an authenticated session unless its node was revoked
// after authAt (the handshake's registry read).
func (h *Hub) register(s *Session, authAt time.Time) bool {
	h.mu.Lock()
	if s.NodeID != "" {
		if at, ok := h.revokedAt[s.NodeID]; ok && !at.Before(authAt) {
			h.mu.Unlock()
			return false
		}
	}
	h.sessions[s.ID] = s
	h.mu.Unlock()
	h.event(map[string]any{"event": "connected", "session": s.ID, "kind": s.Kind, "node_id": nullable(s.NodeID),
		"ip": s.IP, "host": s.Host, "worker_id": s.WorkerID, "pid": s.PID})
	h.publish(TopicProcesses, "process", processEvent{Event: "connected", Session: &s.ID, Process: ptr(s.view())})
	h.markWorkersDirty()
	return true
}

func (h *Hub) unregister(s *Session, reason string) {
	h.mu.Lock()
	if h.sessions[s.ID] != s {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, s.ID)
	h.mu.Unlock()
	h.router.HostGone(s.ID)
	h.event(map[string]any{"event": "disconnected", "session": s.ID, "kind": s.Kind, "node_id": nullable(s.NodeID),
		"ip": s.IP, "worker_id": s.WorkerID, "reason": reason})
	h.publish(TopicProcesses, "process", processEvent{Event: "disconnected", Session: &s.ID, Process: ptr(s.view()), Reason: &reason})
	h.markWorkersDirty()
}

func (h *Hub) refused(p Process, reason string) {
	h.event(map[string]any{"event": "refused", "kind": p.Kind, "node_id": p.NodeID, "ip": p.IP, "host": p.Host,
		"worker_id": p.WorkerID, "pid": p.PID, "reason": reason})
	h.publish(TopicProcesses, "process", processEvent{Event: "refused", Process: &p, Reason: &reason})
}

// Session returns a live session.
func (h *Hub) Session(id string) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[id]
}

// Processes lists the connected processes, oldest first.
func (h *Hub) Processes() []Process {
	h.mu.RLock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.RUnlock()
	sort.Slice(ss, func(i, j int) bool {
		if !ss[i].ConnectedAt.Equal(ss[j].ConnectedAt) {
			return ss[i].ConnectedAt.Before(ss[j].ConnectedAt)
		}
		return ss[i].ID < ss[j].ID
	})
	out := make([]Process, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.view())
	}
	return out
}

func (h *Hub) nodeSessions(nodeID string) []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var out []*Session
	for _, s := range h.sessions {
		if s.NodeID == nodeID {
			out = append(out, s)
		}
	}
	return out
}

// RevokeNode closes the node's sessions at once and refuses sessions
// whose handshake read the registry before now (phase-1 revoke / remove).
func (h *Hub) RevokeNode(nodeID string) {
	h.mu.Lock()
	h.revokedAt[nodeID] = h.now()
	h.mu.Unlock()
	for _, s := range h.nodeSessions(nodeID) {
		frame := map[string]any{"t": "cmd", "id": newID(), "name": "disconnect", "args": map[string]any{"reason": "revoked"}}
		s.sendThenClose(frame, statusPolicyViolation, "revoked")
	}
	h.event(map[string]any{"event": "revoked", "node_id": nodeID})
}

// NodeChanged tells the node's processes to re-read their stage switches
// and drain at once (phase-1 PUT /api/cluster/nodes/{id}).
func (h *Hub) NodeChanged(nodeID string) {
	for _, s := range h.nodeSessions(nodeID) {
		id := newID()
		if s.send(map[string]any{"t": "cmd", "id": id, "name": "reload_stages", "args": map[string]any{}}) {
			h.event(map[string]any{"event": "cmd", "session": s.ID, "ip": s.IP, "id": id, "name": "reload_stages", "by": "settings"})
		}
	}
}

// ---------- live data from processes ----------

func (h *Hub) onMetrics(s *Session, metrics json.RawMessage) {
	h.publish(TopicMetrics, "metrics", map[string]any{"session": s.ID, "metrics": metrics})
}

func (h *Hub) onJob(s *Session, job json.RawMessage) {
	data, err := json.Marshal(map[string]any{"session": s.ID, "ip": s.IP, "job": job})
	if err != nil {
		return
	}
	h.mu.Lock()
	h.jobs = append(h.jobs, data)
	if len(h.jobs) > recentJobs {
		h.jobs = append([]json.RawMessage(nil), h.jobs[len(h.jobs)-recentJobs:]...)
	}
	h.mu.Unlock()
	h.publish(TopicJobs, "job", json.RawMessage(data))
	select {
	case h.queuesDirty <- struct{}{}:
	default:
	}
}

func (h *Hub) markWorkersDirty() {
	select {
	case h.workersDirty <- struct{}{}:
	default:
	}
}

// ---------- queues, workers, library ----------

func (h *Hub) queuesLoop(ctx context.Context) {
	if h.cfg.Queues == nil {
		return
	}
	t := time.NewTicker(queuesTick)
	defer t.Stop()
	var lastBuild, lastFile time.Time
	var debounce <-chan time.Time
	build := func() {
		now := h.now()
		wantFile := now.Sub(lastFile) >= queuesFileEvery
		if !wantFile && h.subscribers(TopicQueues) == 0 {
			return
		}
		lastBuild = now
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		snap, err := h.cfg.Queues(bctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				h.log().Warn("cluster: queue snapshot failed", "err", err, "event", "cluster_queues_failed")
			}
			return
		}
		data, err := json.Marshal(snap)
		if err != nil {
			return
		}
		h.mu.Lock()
		h.queues = data
		h.mu.Unlock()
		h.publish(TopicQueues, "queues", json.RawMessage(data))
		if wantFile {
			lastFile = now
			h.files.Write(KindQueues, compactQueues(snap))
		}
	}
	build()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			build()
		case <-h.queuesDirty:
			if debounce != nil {
				continue
			}
			wait := queuesMinGap - h.now().Sub(lastBuild)
			if wait < 0 {
				wait = 0
			}
			debounce = time.After(wait)
		case <-debounce:
			debounce = nil
			build()
		}
	}
}

// QueueLine is one line of queues/<date>/hub.jsonl.
type QueueLine struct {
	TS          time.Time                 `json:"ts"`
	PausedAll   bool                      `json:"paused_all"`
	Stages      map[string]QueueStageLine `json:"stages"`
	Pools       map[string]QueuePoolLine  `json:"pools"`
	Flows       []queues.Flow             `json:"flows"`
	IssuesCount int                       `json:"issues_count"`
}

// QueueStageLine is one stage in a QueueLine.
type QueueStageLine struct {
	Waiting          int      `json:"waiting"`
	Ready            int      `json:"ready"`
	Running          int      `json:"running"`
	Paused           int      `json:"paused"`
	Failed           int      `json:"failed"`
	Done24h          int      `json:"done_24h"`
	DonePerMin       float64  `json:"done_per_min"`
	OldestWaitingSec *float64 `json:"oldest_waiting_sec"`
	EtaSec           *float64 `json:"eta_sec"`
	WaitingReason    *string  `json:"waiting_reason"`
}

// QueuePoolLine is one pool in a QueueLine.
type QueuePoolLine struct {
	Busy int `json:"busy"`
	Size int `json:"size"`
}

func compactQueues(s queues.Snapshot) QueueLine {
	out := QueueLine{TS: s.At, PausedAll: s.PausedAll, Stages: map[string]QueueStageLine{},
		Pools: map[string]QueuePoolLine{}, Flows: s.Flows, IssuesCount: s.IssuesCount}
	if out.Flows == nil {
		out.Flows = []queues.Flow{}
	}
	for _, q := range s.Queues {
		var reason *string
		if q.WaitingReason != nil {
			reason = strPtr(string(*q.WaitingReason))
		}
		out.Stages[q.Stage] = QueueStageLine{
			Waiting: q.Counts.Pending, Ready: q.Counts.Ready, Running: q.Counts.Running, Paused: q.Counts.Paused,
			Failed: q.Counts.FailedOpen, Done24h: q.Counts.Done24h, DonePerMin: q.RatePerMin,
			OldestWaitingSec: q.OldestWaitingSec, EtaSec: q.EtaSec, WaitingReason: reason,
		}
	}
	for _, p := range s.Pools {
		out.Pools[p.Pool] = QueuePoolLine{Busy: p.Busy, Size: p.EffectiveSize}
	}
	return out
}

func (h *Hub) workersLoop(ctx context.Context) {
	if h.cfg.Workers == nil {
		return
	}
	t := time.NewTicker(queuesTick)
	defer t.Stop()
	build := func(force bool) {
		if !force && h.subscribers(TopicWorkers) == 0 {
			return
		}
		bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ws, err := h.cfg.Workers(bctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				h.log().Warn("cluster: workers list failed", "err", err, "event", "cluster_workers_failed")
			}
			return
		}
		data, err := json.Marshal(map[string]any{"workers": ws})
		if err != nil {
			return
		}
		h.mu.Lock()
		same := bytes.Equal(data, h.workers)
		h.workers = data
		h.mu.Unlock()
		if !same {
			h.publish(TopicWorkers, "workers", json.RawMessage(data))
		}
	}
	build(true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			build(false)
		case <-h.workersDirty:
			build(false)
		}
	}
}

func (h *Hub) libraryLoop(ctx context.Context) {
	if h.cfg.Library == nil {
		return
	}
	build := func() {
		bctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		snap, err := h.cfg.Library(bctx)
		cancel()
		if err != nil || snap == nil {
			if err != nil && ctx.Err() == nil {
				h.log().Warn("cluster: library snapshot failed", "err", err, "event", "cluster_library_failed")
			}
			return
		}
		data, err := json.Marshal(snap)
		if err != nil {
			return
		}
		h.mu.Lock()
		h.library = data
		h.mu.Unlock()
		h.publish(TopicLibrary, "library", json.RawMessage(data))
		h.files.Write(KindLibrary, snap)
	}
	build()
	t := time.NewTicker(libraryEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			build()
		}
	}
}

// ---------- helpers ----------

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

// levelRank orders log levels; 0 is unknown.
func levelRank(l string) int {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "debug", "trace":
		return 10
	case "info":
		return 20
	case "warning", "warn":
		return 30
	case "error":
		return 40
	case "critical", "fatal":
		return 50
	}
	return 0
}

func normLevel(l string) string {
	switch levelRank(l) {
	case 10:
		return "debug"
	case 20:
		return "info"
	case 30:
		return "warning"
	case 40:
		return "error"
	case 50:
		return "critical"
	}
	return ""
}
