// Package cache is the home database's cache of the cluster channel
// (specs/epics/31-multi-instance/design-agent.md §7.9). The files on the
// share are the record; the hub is the only writer of the cache, which is
// bounded (MAKTABA_CLUSTER_CACHE_HOURS) and disposable: emptying it loses
// nothing, it is refilled from the files.
//
// What it writes:
//
//   - health_samples: each process's health numbers at most once per 10 s
//     slot, under the names the workers wrote before (the Processing page's
//     health strip, the rate charts and the transcription report's live
//     speed read them unchanged), plus nas.reads_busy counted by the hub;
//     and the 48-hour fold of raw samples into 10-minute rollups, moved here
//     from the workers' pruner (pipeline engine/prune.py);
//   - cluster_metrics_cache: one metrics frame per process per 10 s slot;
//   - cluster_log_cache: every log record.
//
// Two ways in. The channel: Metrics and Logs return at once; records wait
// in a bounded queue and are written in batches (logs every 2 s, metrics
// and health every 10 s); a full queue or a failing database drops and
// counts them. The files: a pass over the cluster directory (Backfill, then
// every SweepEvery) reads what each file gained since the last pass, at a
// bounded rate, and cluster_cache_files keeps how far each file was read.
// The files make the cache whole: what the channel missed (a dropped
// batch, a process without a channel, the time the hub was down) is read
// from them by the next pass. Records reaching the cache both ways are kept
// once (natural keys, ON CONFLICT DO NOTHING).
//
// Coverage: the cache covers the window once a pass has read every file in
// it, and keeps covering it while passes succeed; the newest minutes (since
// the last pass) are held as far as the channel delivered them.
package cache

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
)

// Kinds of cached records, as the hub names them (and Stats' sections).
const (
	KindMetrics = "metrics"
	KindLogs    = "logs"
	KindHealth  = "health"
)

// RollupHost is the host of the 10-minute rollup rows of health_samples
// (pipeline engine/prune.py ROLLUP_HOST).
const RollupHost = "*rollup*"

const (
	defaultQueue = 10_000
	defaultSweep = time.Minute
	defaultRate  = 5_000

	// slotWidth: one metrics frame and one health set per process per slot.
	slotWidth    = 10 * time.Second
	logsEvery    = 2 * time.Second
	metricsEvery = slotWidth
	pruneEvery   = 10 * time.Minute

	batchRows    = 500
	writeTimeout = 10 * time.Second
	errLogEvery  = time.Minute
	shutdownTime = 3 * time.Second
)

var _ agent.Cache = (*Cache)(nil)

// Options configures a Cache.
type Options struct {
	// Window is how much history the metrics and log tables keep
	// (MAKTABA_CLUSTER_CACHE_HOURS). 0 keeps none: no rows in them, no
	// passes over the files; health_samples is still written and folded.
	Window time.Duration
	// Driver is "postgres" (default) or "sqlite".
	Driver string
	// QueueSize bounds each kind's queue of records waiting for the
	// database (default 10 000); past it records are dropped and counted.
	QueueSize int
	// ClusterDir, when set, names the cluster directory the periodic
	// passes read ("" while unknown); otherwise they read the directory of
	// the last Backfill.
	ClusterDir func() string
	// SweepEvery is the time between passes over the files (default 1 min).
	SweepEvery time.Duration
	// ReadRate bounds the passes, in lines read per second (default 5 000).
	ReadRate int
	Now      func() time.Time
	Logger   *slog.Logger
}

// Cache is the hub's database cache. Build it with New, start Run, and
// call Backfill with the cluster directory when the hub starts.
type Cache struct {
	db      *sql.DB
	sqlite  bool
	window  time.Duration
	queue   int
	sweep   time.Duration
	dirFn   func() string
	limiter *rate.Limiter
	clock   func() time.Time
	log     *slog.Logger
	timeout time.Duration // one statement's deadline

	mu         sync.Mutex
	logs       []logItem
	logsBusy   int
	frames     []frameItem
	framesBusy int
	health     map[string]healthSet // by process
	slots      map[string]seenSlot  // by file worker: the newest slot queued
	wake       chan struct{}        // a final health set is waiting
	dir        string               // the last Backfill's directory
	passAt     map[string]time.Time // kind → start of the last complete pass
	checked    bool                 // stale read positions cleared (first pass)
	counts     map[string]*counter
	edges      map[string][2]time.Time // kind → oldest, newest row
	fold       FoldStats
	lastErr    string
	lastErrAt  time.Time
	loggedAt   time.Time
	muted      int

	logsMu    sync.Mutex // one logs flush at a time
	metricsMu sync.Mutex // one metrics/health flush at a time; guards usedAt
	usedAt    map[hostAt]usedBy
	passMu    sync.Mutex // one pass over the files at a time
	pruneMu   sync.Mutex
}

type counter struct{ written, dropped, fromFiles int64 }

// logItem is a log record waiting for the database.
type logItem struct {
	ip, worker, node string
	rec              agent.LogRecord
}

// frameItem is a metrics frame waiting for the database.
type frameItem struct {
	ip, worker, node string
	at, slot         time.Time
	frame            json.RawMessage
}

// healthSet is the latest health map of one process.
type healthSet struct {
	key, host string
	at        time.Time
	vals      map[string]float64
	final     bool
}

type seenSlot struct{ slot, seen time.Time }

// New builds a cache on the home's database.
func New(db *sql.DB, opts Options) *Cache {
	c := &Cache{
		db:      db,
		sqlite:  opts.Driver == "sqlite",
		window:  max(opts.Window, 0),
		queue:   opts.QueueSize,
		sweep:   opts.SweepEvery,
		dirFn:   opts.ClusterDir,
		clock:   opts.Now,
		log:     opts.Logger,
		timeout: writeTimeout,
		health:  map[string]healthSet{},
		slots:   map[string]seenSlot{},
		wake:    make(chan struct{}, 1),
		passAt:  map[string]time.Time{},
		counts:  map[string]*counter{KindMetrics: {}, KindLogs: {}, KindHealth: {}},
		edges:   map[string][2]time.Time{},
		usedAt:  map[hostAt]usedBy{},
	}
	if c.queue <= 0 {
		c.queue = defaultQueue
	}
	if c.sweep <= 0 {
		c.sweep = defaultSweep
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	r := opts.ReadRate
	if r <= 0 {
		r = defaultRate
	}
	c.limiter = rate.NewLimiter(rate.Limit(r), max(r, batchRows))
	return c
}

func (c *Cache) now() time.Time { return c.clock().UTC() }

// ---------- the sink: every call returns at once ----------

// Metrics takes a metrics frame (without "t"), its flat health map and
// whether it is the process's last frame. The health map replaces the
// process's waiting one (written at the next 10 s flush; a final one at
// once); the frame is queued when it opens a new 10 s slot for the process.
func (c *Cache) Metrics(p agent.Proc, frame json.RawMessage, health map[string]float64, final bool) {
	now := c.now()
	var item *frameItem
	if c.window > 0 && len(frame) > 0 {
		at, ok := frameTS(frame)
		if !ok {
			at = now
		}
		item = &frameItem{ip: agent.IPDir(p.IP), worker: FileName(p.WorkerID), node: p.NodeID,
			at: at, slot: slotOf(at), frame: frame}
	}
	vals := make(map[string]float64, len(health))
	for k, v := range health {
		if validMetric(k, v) {
			vals[k] = v
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(vals) > 0 {
		key := procKey(p)
		c.health[key] = healthSet{key: key, host: hostOf(p), at: now, vals: vals, final: final}
		if final {
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	}
	if item == nil {
		return
	}
	if last, ok := c.slots[item.worker]; ok && last.slot.Equal(item.slot) {
		return // this slot already has its frame
	}
	c.slots[item.worker] = seenSlot{slot: item.slot, seen: now}
	if len(c.frames)+c.framesBusy >= c.queue {
		c.counts[KindMetrics].dropped++
		return
	}
	c.frames = append(c.frames, *item)
}

// Logs takes a logs frame's records. Records past the queue's room are
// dropped and counted (the next pass over the files reads them).
func (c *Cache) Logs(p agent.Proc, recs []agent.LogRecord) {
	if c.window <= 0 || len(recs) == 0 {
		return
	}
	ip, worker := agent.IPDir(p.IP), FileName(p.WorkerID)
	c.mu.Lock()
	defer c.mu.Unlock()
	room := max(c.queue-len(c.logs)-c.logsBusy, 0)
	n := min(len(recs), room)
	for _, r := range recs[:n] {
		c.logs = append(c.logs, logItem{ip: ip, worker: worker, node: p.NodeID, rec: r})
	}
	c.counts[KindLogs].dropped += int64(len(recs) - n)
}

// procKey names a process: its worker id, else its session.
func procKey(p agent.Proc) string {
	if p.WorkerID != "" {
		return p.WorkerID
	}
	return "session:" + p.SessionID
}

// hostOf is the host health_samples files a process under, as the
// workers did (their stable host name).
func hostOf(p agent.Proc) string {
	switch {
	case p.Host != "":
		return p.Host
	case p.IP != "":
		return agent.IPDir(p.IP)
	}
	return "unknown"
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// FileName is a worker id as the processes name their files
// (<worker_id>.jsonl): the pipeline's cluster files safe_name. The cache
// files every record under it, so a record from the channel and the same
// record read from the file meet on one key.
func FileName(workerID string) string {
	name := strings.Trim(unsafeName.ReplaceAllString(workerID, "_"), "._")
	if len(name) > 120 {
		name = name[:120]
	}
	if name == "" {
		return "worker"
	}
	return name
}

// slotOf is t down to its 10 s slot.
func slotOf(t time.Time) time.Time {
	return t.UTC().Truncate(slotWidth)
}

// frameTS reads a frame's ts.
func frameTS(frame json.RawMessage) (time.Time, bool) {
	var head struct {
		TS json.RawMessage `json:"ts"`
	}
	if json.Unmarshal(frame, &head) != nil {
		return time.Time{}, false
	}
	return parseTS(head.TS)
}

// parseTS reads a record's ts as the hub's file reader does: RFC 3339 (a
// naive time is UTC) or Unix seconds.
func parseTS(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return parseTime(s)
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil && f > 0 {
		sec := int64(f)
		return time.Unix(sec, int64((f-float64(sec))*1e9)).UTC(), true
	}
	return time.Time{}, false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
}

func parseTime(s string) (time.Time, bool) {
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// levelRank orders log levels as the hub does; an unknown level counts
// as info (the files reader's rule).
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
