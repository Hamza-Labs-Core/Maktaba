package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/relay"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/safego"
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
	DefaultAgentRatePerMin = 30
	DefaultCacheHours      = 24
	goneKeep               = 5 * time.Minute

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
	// HomeKeyMode is MAKTABA_HOME_AGENT_KEY_MODE (0600 or 0640), for the
	// caller that loads HomeKey.
	HomeKeyMode os.FileMode
	// ClusterKey is the private half of the home's long-term cluster key
	// (design-agent.md §4.2); nil refuses machines.
	ClusterKey []byte
	// Cache is the database cache of the channel (track C1); nil: none.
	Cache Cache
	// Archive is the home's long-term archive (§7.10); nil: none.
	Archive *ArchiveConfig
	// CacheWindow is MAKTABA_CLUSTER_CACHE_HOURS, for the cache's
	// Options.Window.
	CacheWindow time.Duration
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

	// Relay is the extension service door (svc.request frames, GET
	// /api/cluster/relay); nil: every svc.request is refused (no_door).
	Relay *relay.Door

	LLMQueueTimeout time.Duration
	LLMMaxParallel  int
	LLMIdleTimeout  time.Duration

	// AgentRatePerMin caps handshakes per IP on /api/cluster/agent
	// (default 30; negative: no limit); past it the close is 4013.
	AgentRatePerMin int
	// BrowserOrigins are extra Origin patterns allowed on /api/cluster/ws
	// (the API's own origin is always allowed).
	BrowserOrigins []string

	// Delegation issues the nodes' delegation keys (Epic 35 story 35.9); nil: the
	// machine agents get none (full nodes then cannot act for users).
	Delegation KeyIssuer
	// Certs signs a machine's cluster-link certificate from its CSR (cert.csr, story
	// 35.16: a machine joined before the link gets its certificate on its channel
	// session, no re-join); nil: cert.csr is refused.
	Certs CertIssuer
	// HasCert reports whether a machine holds a cluster-link certificate: only such a
	// machine's plain session is cert-only while the link is required (one that never got
	// one — a worker-only machine, an older release — keeps its channel); nil: none does.
	HasCert func(ctx context.Context, nodeID string) bool
	// DBClientCert keeps a machine's word, from its machine agent's update.state
	// (db_client_cert), on whether every one of its processes presents its cluster
	// certificate to this home's database: once the link is required the home's Postgres
	// asks only such a machine's role for it (pgbundle Reach.CertRoles), so requiring the
	// link never cuts a machine off its database. Called when a session first says it and
	// when it changes; nil: not kept.
	DBClientCert func(ctx context.Context, nodeID string, presents bool) error

	Now    func() time.Time
	Logger *slog.Logger
}

// Hub is the live centre of the cluster.
type Hub struct {
	cfg      Config
	files    *FileWriter
	router   *Router
	sweeper  *Sweeper
	limiter  *ipLimiter
	archiver *Archiver
	homeIP   string

	mu        sync.RWMutex
	sessions  map[string]*Session
	revokedAt map[string]time.Time
	browsers  map[*browser]struct{}
	gone      []Process // left in the last 5 minutes
	loc       Location
	jobs      []json.RawMessage // recent job messages (data of type "job")
	// recentRevoked are the revocations of the last revokedReplay, sent again to a
	// machine agent that connects (it missed them while it was away)
	recentRevoked []revokedFrame
	queues        json.RawMessage
	workers       json.RawMessage
	library       json.RawMessage

	queuesDirty  chan struct{}
	workersDirty chan struct{}

	// acc is the machines' own requests for their database access (access.request)
	acc accessRequests

	// agentJoined is closed (and dropped) when a machine agent connects or leaves: a
	// loop that tells each machine something wakes for it at once (agentJoinedCh)
	agentJoined chan struct{}
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
	h.limiter = newIPLimiter(cfg.AgentRatePerMin)
	if cfg.Relay != nil {
		cfg.Relay.SetMember(h.relayMember)
		cfg.Relay.SetAudit(h.relayAudit)
	}
	if cfg.Archive != nil {
		h.archiver = NewArchiver(*cfg.Archive, func() string { return h.Location().Dir }, cfg.Now, cfg.Logger)
	}
	h.files = NewFileWriter(8192, cfg.FileMaxBytes, cfg.Now, cfg.Logger)
	h.sweeper = &Sweeper{Days: cfg.RetentionDays, Now: cfg.Now}
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
		go func() {
			defer wg.Done()
			defer safego.Recover(h.log(), "cluster_hub_loop")
			f(ctx)
		}()
	}
	loc := h.Locate(ctx)
	start(h.files.Run)
	if h.archiver != nil {
		start(h.archiver.Run)
	}
	if h.cfg.Cache != nil {
		start(h.cfg.Cache.Run)
		start(func(ctx context.Context) {
			if loc.Dir == "" {
				return
			}
			if err := h.cfg.Cache.Backfill(ctx, loc.Dir); err != nil && ctx.Err() == nil {
				h.log().Warn("cluster: cache backfill failed", "err", err, "event", "cluster_cache_backfill_failed")
			}
		})
	}
	start(h.locateLoop)
	start(h.queuesLoop)
	start(h.workersLoop)
	start(h.libraryLoop)
	start(h.sweepLoop)
	start(h.keysLoop)
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
		s.closeWith(CloseGoingAway, "")
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
		if n, err := h.sweeper.Sweep(loc.Dir); err != nil {
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

// event appends one line to the hub's events file.
func (h *Hub) event(fields map[string]any) {
	fields["ts"] = h.now().Format(time.RFC3339Nano)
	h.files.Write(KindEvents, fields)
}

// Record appends an event of the home's own to the hub's events file (its `event` field
// names it: the update flow's, story 35.15 AC2).
func (h *Hub) Record(fields map[string]any) {
	if h == nil || len(fields) == 0 {
		return
	}
	rec := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		rec[k] = v
	}
	h.event(rec)
}

// ---------- registry ----------

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// register adds an authenticated session unless its node was revoked
// after authAt (the handshake's registry read). An older session with the
// same worker id is replaced (4009).
func (h *Hub) register(s *Session, authAt time.Time) bool {
	h.mu.Lock()
	if s.NodeID != "" {
		if at, ok := h.revokedAt[s.NodeID]; ok && !at.Before(authAt) {
			h.mu.Unlock()
			return false
		}
	}
	var replaced []*Session
	for _, o := range h.sessions {
		if o.WorkerID == s.WorkerID && o.Kind == s.Kind && o.NodeID == s.NodeID {
			replaced = append(replaced, o)
		}
	}
	h.sessions[s.ID] = s
	if s.machineAgent() {
		h.agentsChangedLocked()
	}
	h.mu.Unlock()
	for _, o := range replaced {
		o.closeWith(CloseReplaced, "")
	}
	h.event(map[string]any{"event": "connect", "session": s.ID, "kind": s.Kind, "node_id": nullable(s.NodeID),
		"ip": s.IP, "host": s.Host, "worker_id": s.WorkerID, "pid": s.PID})
	h.publish(TopicProcesses, "process", processEvent{Event: "connected", Session: &s.ID, Process: ptr(s.view())})
	h.markWorkersDirty()
	return true
}

// agentsChangedLocked wakes the loops waiting on agentJoinedCh (h.mu held).
func (h *Hub) agentsChangedLocked() {
	if h.agentJoined != nil {
		close(h.agentJoined)
		h.agentJoined = nil
	}
}

// agentJoinedCh is closed when the next machine agent connects or leaves. Take it before
// reading the machine agents, so a change after that read is never missed. A leave
// counts: a machine's agent that reconnects is registered while the session it
// replaces is still there (closed after, removed when its pump ends), so a read in
// between may name the old session for the machine; its removal names the new one.
func (h *Hub) agentJoinedCh() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agentJoined == nil {
		h.agentJoined = make(chan struct{})
	}
	return h.agentJoined
}

func (h *Hub) unregister(s *Session, reason string) {
	now := h.now()
	h.mu.Lock()
	if h.sessions[s.ID] != s {
		h.mu.Unlock()
		return
	}
	delete(h.sessions, s.ID)
	if s.machineAgent() {
		h.agentsChangedLocked()
	}
	p := s.view()
	p.GoneAt = &now
	kept := h.gone[:0]
	for _, g := range h.gone {
		if g.GoneAt != nil && now.Sub(*g.GoneAt) < goneKeep {
			kept = append(kept, g)
		}
	}
	h.gone = append(kept, p)
	h.mu.Unlock()
	h.router.HostGone(s.ID)
	h.event(map[string]any{"event": "disconnect", "session": s.ID, "kind": s.Kind, "node_id": nullable(s.NodeID),
		"ip": s.IP, "worker_id": s.WorkerID, "detail": reason})
	h.publish(TopicProcesses, "process", processEvent{Event: "disconnected", Session: &s.ID, Process: &p, Reason: &reason})
	h.markWorkersDirty()
}

// Gone lists the processes that left in the last 5 minutes.
func (h *Hub) Gone() []Process {
	now := h.now()
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := []Process{}
	for _, g := range h.gone {
		if g.GoneAt != nil && now.Sub(*g.GoneAt) < goneKeep {
			out = append(out, g)
		}
	}
	return out
}

func (h *Hub) refused(p Process, reason string) {
	h.event(map[string]any{"event": "auth_refused", "kind": p.Kind, "node_id": p.NodeID, "ip": p.IP, "host": p.Host,
		"worker_id": p.WorkerID, "pid": p.PID, "detail": reason})
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
	if h.cfg.Delegation != nil {
		// its delegation key stops working at once (story 35.9 EC4)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		h.cfg.Delegation.Revoke(ctx, nodeID)
		cancel()
	}
	sessions := h.nodeSessions(nodeID)
	for _, s := range sessions {
		frame := map[string]any{"t": "cmd", "id": newID(), "name": "disconnect", "args": map[string]any{"reason": "revoked"}}
		s.sendThenClose(frame, CloseRevoked, "")
	}
	h.event(map[string]any{"event": "revoked", "node_id": nodeID, "sessions": len(sessions)})
	for _, s := range sessions {
		id := s.ID
		h.publish(TopicProcesses, "process", processEvent{Event: "revoked", Session: &id, Process: ptr(s.view())})
	}
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
	data, err := json.Marshal(map[string]any{"session": s.ID, "ip": s.IP, "node_name": s.NodeName,
		"worker_id": s.WorkerID, "host": s.Host, "job": job})
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

// runningJob is a metrics frame's pipeline.jobs entry.
type runningJob struct {
	JobID    json.RawMessage `json:"job_id"`
	Stage    string          `json:"stage"`
	Subject  string          `json:"subject"` // "<kind>:<id>"
	Title    *string         `json:"title"`
	Since    *string         `json:"since"`
	Progress json.RawMessage `json:"progress"`
}

// runningJobs is the jobs snapshot's `running`: every live session's
// running jobs from its latest metrics frame, each as a `job` message
// carries it (event "progress" when the job reported progress, else
// "claimed"), so a page that loads mid-job shows it even when its
// claimed event is no longer among the recent ones.
func (h *Hub) runningJobs() []json.RawMessage {
	out := []json.RawMessage{}
	for _, p := range h.Processes() {
		if p.Metrics == nil {
			continue
		}
		var m struct {
			TS       *string `json:"ts"`
			Pipeline struct {
				Jobs []runningJob `json:"jobs"`
			} `json:"pipeline"`
		}
		if json.Unmarshal(p.Metrics, &m) != nil {
			continue
		}
		for _, j := range m.Pipeline.Jobs {
			if len(j.JobID) == 0 || j.Stage == "" {
				continue
			}
			kind, id, _ := strings.Cut(j.Subject, ":")
			var subjectKind, subjectID *string
			if kind != "" {
				subjectKind = &kind
			}
			if id != "" && id != "None" && id != "null" {
				subjectID = &id
			}
			event := "claimed"
			progress := json.RawMessage("null")
			if len(j.Progress) > 0 && string(j.Progress) != "null" {
				event, progress = "progress", j.Progress
			}
			job := map[string]any{"event": event, "job_id": j.JobID, "stage": j.Stage, "subject_kind": subjectKind,
				"subject_id": subjectID, "title": j.Title, "attempt": nil, "started_at": j.Since, "ts": m.TS,
				"duration_ms": nil, "progress": progress, "outcome": nil, "output": nil}
			data, err := json.Marshal(map[string]any{"session": p.Session, "ip": p.IP, "node_name": p.NodeName,
				"worker_id": p.WorkerID, "host": p.Host, "job": job})
			if err == nil {
				out = append(out, data)
			}
		}
	}
	return out
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

// QueueLine is one line of queues/<date>/hub.jsonl: the queue snapshot in
// the Processing page's own shape (trimmed to its numbers) under
// `snapshot`, which the web app's charts read, and the flat per-stage rows
// of design-agent.md §7.6, which the CLI reads.
type QueueLine struct {
	TS       time.Time         `json:"ts"`
	Snapshot QueueSnapshotLite `json:"snapshot"`
	Stages   []QueueStageLine  `json:"stages"`
	Pools    []QueuePoolLine   `json:"pools"`
	Flows    []queues.Flow     `json:"flows"`
}

// QueueSnapshotLite is a queues.Snapshot without the stage registry, the
// running jobs or the health strip.
type QueueSnapshotLite struct {
	At          time.Time        `json:"at"`
	PausedAll   bool             `json:"paused_all"`
	Queues      []QueueLiteStage `json:"queues"`
	Pools       []QueuePoolLite  `json:"pools"`
	Flows       []queues.Flow    `json:"flows"`
	IssuesCount int              `json:"issues_count"`
}

// QueueLiteStage is one queue in QueueSnapshotLite (the QueueSummary
// field names).
type QueueLiteStage struct {
	Stage            string                `json:"stage"`
	Counts           queues.Counts         `json:"counts"`
	RatePerMin       float64               `json:"rate_per_min"`
	EtaSec           *float64              `json:"eta_sec"`
	OldestWaitingSec *float64              `json:"oldest_waiting_sec"`
	WaitingReason    *queues.WaitingReason `json:"waiting_reason"`
}

// QueuePoolLite is one pool in QueueSnapshotLite.
type QueuePoolLite struct {
	Pool          string `json:"pool"`
	Busy          int    `json:"busy"`
	EffectiveSize int    `json:"effective_size"`
}

// QueueStageLine is one stage row of design-agent.md §7.6.
type QueueStageLine struct {
	Stage       string   `json:"stage"`
	Waiting     int      `json:"waiting"`
	Ready       int      `json:"ready"`
	Running     int      `json:"running"`
	Paused      int      `json:"paused"`
	Failed      int      `json:"failed"`
	DonePerMin  float64  `json:"done_per_min"`
	OldestWaitS *float64 `json:"oldest_wait_s"`
}

// QueuePoolLine is one pool row of design-agent.md §7.6.
type QueuePoolLine struct {
	Pool string `json:"pool"`
	Busy int    `json:"busy"`
	Size int    `json:"size"`
}

func compactQueues(s queues.Snapshot) QueueLine {
	flows := s.Flows
	if flows == nil {
		flows = []queues.Flow{}
	}
	lite := QueueSnapshotLite{At: s.At, PausedAll: s.PausedAll, Queues: []QueueLiteStage{}, Pools: []QueuePoolLite{},
		Flows: flows, IssuesCount: s.IssuesCount}
	out := QueueLine{TS: s.At, Stages: []QueueStageLine{}, Pools: []QueuePoolLine{}, Flows: flows}
	for _, q := range s.Queues {
		lite.Queues = append(lite.Queues, QueueLiteStage{Stage: q.Stage, Counts: q.Counts, RatePerMin: q.RatePerMin,
			EtaSec: q.EtaSec, OldestWaitingSec: q.OldestWaitingSec, WaitingReason: q.WaitingReason})
		out.Stages = append(out.Stages, QueueStageLine{Stage: q.Stage, Waiting: q.Counts.Pending, Ready: q.Counts.Ready,
			Running: q.Counts.Running, Paused: q.Counts.Paused, Failed: q.Counts.FailedOpen, DonePerMin: q.RatePerMin,
			OldestWaitS: q.OldestWaitingSec})
	}
	for _, p := range s.Pools {
		lite.Pools = append(lite.Pools, QueuePoolLite{Pool: p.Pool, Busy: p.Busy, EffectiveSize: p.EffectiveSize})
		out.Pools = append(out.Pools, QueuePoolLine{Pool: p.Pool, Busy: p.Busy, Size: p.EffectiveSize})
	}
	out.Snapshot = lite
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
		data, err := json.Marshal(map[string]any{"workers": h.withSessions(ws)})
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

// withSessions adds each worker's live session (null when it has none)
// to the GET /api/workers shape.
func (h *Hub) withSessions(ws []queues.Worker) []map[string]any {
	h.mu.RLock()
	byWorker := map[string]string{}
	for _, s := range h.sessions {
		byWorker[s.WorkerID] = s.ID
	}
	h.mu.RUnlock()
	out := make([]map[string]any, 0, len(ws))
	for _, w := range ws {
		b, err := json.Marshal(w)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		m["session"] = nil
		if id, ok := byWorker[w.ID]; ok {
			m["session"] = id
		}
		out = append(out, m)
	}
	return out
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
