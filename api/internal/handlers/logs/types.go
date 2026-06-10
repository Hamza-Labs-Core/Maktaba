package logs

// SystemInfo is the host/version/resource snapshot written to
// system-info.json in the bundle. Scoped is true for the user-facing
// diagnostics export (lines restricted to the requesting user).
type SystemInfo struct {
	GeneratedAt   string   `json:"generated_at"`
	Service       string   `json:"service"`
	OS            string   `json:"os"`
	Arch          string   `json:"arch"`
	GoVersion     string   `json:"go_version"`
	NumCPU        int      `json:"num_cpu"`
	Goroutines    int      `json:"goroutines"`
	Version       string   `json:"version"`
	BuildSHA      string   `json:"build_sha"`
	BuildTime     string   `json:"build_time"`
	SchemaRev     int      `json:"schema_revision"`
	UptimeSeconds float64  `json:"uptime_seconds"`
	DiskFreeBytes uint64   `json:"disk_free_bytes,omitempty"`
	Memory        MemInfo  `json:"memory"`
	DB            *DBStats `json:"database,omitempty"`
	Scoped        bool     `json:"scoped"`
}

// MemInfo is the process memory snapshot (runtime.MemStats subset).
type MemInfo struct {
	AllocBytes     uint64 `json:"alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	HeapInUseBytes uint64 `json:"heap_inuse_bytes"`
	NumGC          uint32 `json:"num_gc"`
}

// DBStats mirrors sql.DBStats for the connection-pool section.
type DBStats struct {
	MaxOpenConnections int   `json:"max_open_connections"`
	OpenConnections    int   `json:"open_connections"`
	InUse              int   `json:"in_use"`
	Idle               int   `json:"idle"`
	WaitCount          int64 `json:"wait_count"`
	WaitDurationMs     int64 `json:"wait_duration_ms"`
}

// JobStatus is the active-job rollup (admin export only).
type JobStatus struct {
	ByState map[string]int `json:"by_state"`
	Total   int            `json:"total"`
	Note    string         `json:"note,omitempty"`
}

// ErrorSummary is the deduplicated error report written to
// error-summary.json.
type ErrorSummary struct {
	UniqueErrors int           `json:"unique_errors"`
	TotalErrors  int           `json:"total_errors"`
	Errors       []ErrorBucket `json:"errors"`
}

// ErrorBucket is one deduplicated error: its message, occurrence count,
// and the timestamp it was last seen.
type ErrorBucket struct {
	Message  string `json:"message"`
	Service  string `json:"service"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen,omitempty"`
}
