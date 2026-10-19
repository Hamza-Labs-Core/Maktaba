package agent

import (
	"context"
	"encoding/json"
	"time"
)

// The database cache of the channel (track C1, package
// api/internal/cluster/cache): the files on the share stay the record; the
// hub is the only writer of the cache, and the cache never blocks it. The
// hub sees the cache through this interface (nil: no cache).

// Proc identifies the process a frame came from.
type Proc struct {
	SessionID string
	WorkerID  string
	NodeID    string
	NodeName  string
	Host      string
	IP        string
}

// LogRecord is one record of a logs frame.
type LogRecord struct {
	TS     string          `json:"ts"`
	Level  string          `json:"level"`
	Event  string          `json:"event"`
	Logger string          `json:"logger"`
	Fields json.RawMessage `json:"fields"`
}

// CacheQuery is a history read (the REST filters).
type CacheQuery struct {
	IP       string
	WorkerID string // "" for every process
	Level    string // logs: records at or above it ("" for all)
	Q        string // logs: case-insensitive substring
	From, To time.Time
	Limit    int
}

// CacheRecord is one cached line: the process it came from, its time and
// the record as the files hold it (a log record; a metrics frame without
// "t").
type CacheRecord struct {
	WorkerID string
	TS       time.Time
	Line     json.RawMessage
}

// Cache is what the hub needs from the cache.
type Cache interface {
	// Metrics stores a metrics frame (without "t"), its flat health map
	// (health_samples names) and whether it is the process's final frame.
	Metrics(p Proc, frame json.RawMessage, health map[string]float64, final bool)
	// Logs stores a logs frame's records.
	Logs(p Proc, recs []LogRecord)
	// Covers reports whether the cache holds kind ("metrics" | "logs")
	// for the whole of [from, to).
	Covers(kind string, from, to time.Time) bool
	ReadMetrics(ctx context.Context, q CacheQuery) ([]CacheRecord, error)
	ReadLogs(ctx context.Context, q CacheQuery) ([]CacheRecord, error)
	// Run writes until ctx ends; Backfill loads the cluster files of the
	// cache window from dir.
	Run(ctx context.Context)
	Backfill(ctx context.Context, dir string) error
	// Stats is shown in the processes snapshot as `cache`.
	Stats() any
}

func (s *Session) proc() Proc {
	p := Proc{SessionID: s.ID, WorkerID: s.WorkerID, NodeID: s.NodeID, Host: s.Host, IP: s.IP}
	if s.NodeName != nil {
		p.NodeName = *s.NodeName
	}
	return p
}

// toCache hands a metrics frame to the cache: the frame without "t", its
// flat `health` map and its `final` flag.
func (h *Hub) metricsToCache(s *Session, frame json.RawMessage) {
	if h.cfg.Cache == nil {
		return
	}
	var f struct {
		Health map[string]float64 `json:"health"`
		Final  bool               `json:"final"`
	}
	_ = json.Unmarshal(frame, &f)
	h.cfg.Cache.Metrics(s.proc(), frame, f.Health, f.Final)
}

func (h *Hub) logsToCache(s *Session, records []json.RawMessage) {
	if h.cfg.Cache == nil || len(records) == 0 {
		return
	}
	recs := make([]LogRecord, 0, len(records))
	for _, raw := range records {
		var r LogRecord
		if json.Unmarshal(raw, &r) == nil {
			recs = append(recs, r)
		}
	}
	h.cfg.Cache.Logs(s.proc(), recs)
}

func (h *Hub) cacheStats() any {
	if h.cfg.Cache == nil {
		return nil
	}
	return h.cfg.Cache.Stats()
}
