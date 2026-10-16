package queues

import (
	"encoding/json"
	"time"
)

// This file holds the wire structs of plan-06-11 §8.2. JSON names are
// the contract shared with workstream C (web/src/lib/queueApi.ts); do
// not rename a field without updating the spec.

// CatalogID is the fixed subject id of the single global planning
// subject (plan-06-11 D2).
const CatalogID = "00000000-0000-0000-0000-00000000ca7a"

// Subject kinds (plan-06-11 D1).
const (
	KindVideo   = "video"
	KindLibrary = "library"
	KindSeries  = "series"
	KindSeason  = "season"
	KindCatalog = "catalog"
)

var subjectKinds = map[string]bool{
	KindVideo: true, KindLibrary: true, KindSeries: true, KindSeason: true, KindCatalog: true,
}

// Progress is processing_jobs.progress (JSONB).
type Progress struct {
	Step       string   `json:"step"`
	Steps      []string `json:"steps"`
	DoneUnits  float64  `json:"done_units"`
	TotalUnits float64  `json:"total_units"`
	Message    string   `json:"message,omitempty"`
}

// JobRef is the job summary shared by REST and SSE (plan-06-11 §7.2).
type JobRef struct {
	ID           int64      `json:"id"`
	Stage        string     `json:"stage"`
	State        string     `json:"state"`
	SubjectKind  string     `json:"subject_kind"`
	SubjectID    string     `json:"subject_id"`
	SubjectLabel string     `json:"subject_label"`
	LibraryID    *string    `json:"library_id"`
	Priority     int        `json:"priority"`
	Attempts     int        `json:"attempts"`
	MaxAttempts  int        `json:"max_attempts"`
	Outcome      *string    `json:"outcome"`
	ErrorCause   *string    `json:"error_cause"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	Progress     *Progress  `json:"progress"`
}

// Pool is a worker pool row plus live usage.
type Pool struct {
	Pool          string    `json:"pool"`
	Label         string    `json:"label"`
	Scope         string    `json:"scope"`
	Size          *int      `json:"size"`
	EffectiveSize int       `json:"effective_size"`
	Busy          int       `json:"busy"`
	Stages        []string  `json:"stages"`
	UpdatedAt     time.Time `json:"updated_at"`
	UpdatedBy     *string   `json:"updated_by"`
}

// Edge is one graph edge of a stage.
type Edge struct {
	To     string `json:"to"`
	Map    string `json:"map"`
	When   string `json:"when"`
	Legacy bool   `json:"legacy"`
}

// StageInfo is one job_stages row.
type StageInfo struct {
	Stage          string     `json:"stage"`
	Label          string     `json:"label"`
	Lane           string     `json:"lane"`
	LaneOrder      int        `json:"lane_order"`
	SubjectKinds   []string   `json:"subject_kinds"`
	Pool           string     `json:"pool"`
	Version        int        `json:"version"`
	Legacy         bool       `json:"legacy"`
	Hidden         bool       `json:"hidden"`
	ExternalWrite  bool       `json:"external_write"`
	WriteMode      string     `json:"write_mode"`
	Enabled        bool       `json:"enabled"`
	DisabledReason *string    `json:"disabled_reason"`
	Paused         bool       `json:"paused"`
	PausedAt       *time.Time `json:"paused_at"`
	PausedBy       *string    `json:"paused_by"`
	DebounceSec    int        `json:"debounce_sec"`
	Edges          []Edge     `json:"edges"`
	MayRequest     []string   `json:"may_request"`
	EntryFor       []string   `json:"entry_for"`
	FedBy          []string   `json:"fed_by"`
	GraphSyncedAt  *time.Time `json:"graph_synced_at"`
	// Extension names the optional extension owning this stage (nil = core).
	Extension *string `json:"extension"`
}

// WaitingReason explains why a queue with nothing running is idle.
type WaitingReason string

// Waiting reasons, in the precedence order of plan-06-11 §8.3.
const (
	WaitDisabled       WaitingReason = "disabled"
	WaitNotConfigured  WaitingReason = "not_configured"
	WaitPausedAll      WaitingReason = "paused_all"
	WaitPaused         WaitingReason = "paused"
	WaitAwaitingReview WaitingReason = "awaiting_review"
	WaitNoWorker       WaitingReason = "no_worker"
	WaitPoolFull       WaitingReason = "pool_full"
	WaitDebouncing     WaitingReason = "debouncing"
	WaitBackoff        WaitingReason = "backoff"
	// WaitServiceRetrying: the waiting rows are backing off because an
	// external service (Plex, dizqueTV) did not answer ("Plex not
	// answering, retrying"); QueueSummary.RetryingService names it.
	WaitServiceRetrying WaitingReason = "service_retrying"
	WaitUpstream       WaitingReason = "upstream"
	WaitIdle           WaitingReason = "idle"
)

// Counts are the per-stage job counters.
type Counts struct {
	Pending        int `json:"pending"`
	Ready          int `json:"ready"`
	Running        int `json:"running"`
	Paused         int `json:"paused"`
	FailedOpen     int `json:"failed_open"`
	Done24h        int `json:"done_24h"`
	Skipped24h     int `json:"skipped_24h"`
	AwaitingReview int `json:"awaiting_review"`
}

// Wave drives the ring gauge.
type Wave struct {
	Since *time.Time `json:"since"`
	Done  int        `json:"done"`
	Total int        `json:"total"`
}

// QueueSummary is the per-stage card data.
type QueueSummary struct {
	Stage            string         `json:"stage"`
	Counts           Counts         `json:"counts"`
	Wave             Wave           `json:"wave"`
	RatePerMin       float64        `json:"rate_per_min"`
	EtaSec           *float64       `json:"eta_sec"`
	OldestWaitingSec *float64       `json:"oldest_waiting_sec"`
	WaitingReason    *WaitingReason `json:"waiting_reason"`
	RunningJobs      []JobRef       `json:"running_jobs"`
	// ServedBy lists the online workers that serve this stage; NoWorker
	// is true when the stage is enabled but no online worker serves it
	// ("waiting for a worker that serves transcribe").
	// served_by is the list of hosts (queueApi.ts contract).
	ServedBy      []string `json:"served_by"`
	ServedWorkers int      `json:"served_workers"`
	NoWorker      bool     `json:"no_worker"`
	// Extension mirrors StageInfo.Extension for card rendering.
	Extension *string `json:"extension"`
	// RetryingService names the external service ("plex", "dizquetv")
	// whose failures put this stage's waiting rows into backoff; nil when
	// none did. Set even while other rows run, so the card can say so.
	RetryingService *string `json:"retrying_service"`
}

// Coverage is the live worker coverage of one stage.
type Coverage struct {
	Workers int      `json:"workers"`
	Hosts   []string `json:"hosts"`
}

// Worker is one row of the workers registry plus liveness.
type Worker struct {
	ID          string          `json:"worker_id"`
	Host        string          `json:"host"`
	Online      bool            `json:"online"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	Stages      []string        `json:"serves"`
	ServesLabel string          `json:"serves_label"`
	Pools       json.RawMessage `json:"pools"`
	StartedAt   time.Time       `json:"started_at"`
	Version     *string         `json:"version"`
}

// Flow is trigger traffic between two stages.
type Flow struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	PerMin float64 `json:"per_min"`
}

// HealthNow is the health strip.
type HealthNow struct {
	NAS struct {
		ReadsBusy  int      `json:"reads_busy"`
		ReadsLimit int      `json:"reads_limit"`
		OpenMsP50  *float64 `json:"open_ms_p50"`
		OpenMsP95  *float64 `json:"open_ms_p95"`
		Timeouts   int      `json:"timeouts_10m"`
	} `json:"nas"`
	CPU struct {
		Pct *float64 `json:"pct"`
	} `json:"cpu"`
	Network struct {
		PlexMs    *float64 `json:"plex_ms"`
		TvMs      *float64 `json:"tv_ms"`
		YoutubeMs *float64 `json:"youtube_ms"`
	} `json:"network"`
	Backlog struct {
		Ready            int      `json:"ready"`
		OldestWaitingSec *float64 `json:"oldest_waiting_sec"`
	} `json:"backlog"`
	Spark map[string][]float64 `json:"spark"`
}

// Snapshot is GET /api/queues and the queues.snapshot event.
type Snapshot struct {
	At          time.Time      `json:"at"`
	PausedAll   bool           `json:"paused_all"`
	Stages      []StageInfo    `json:"stages"`
	Queues      []QueueSummary `json:"queues"`
	Pools       []Pool         `json:"pools"`
	Flows       []Flow         `json:"flows"`
	Health      HealthNow      `json:"health"`
	IssuesCount int            `json:"issues_count"`
}

// RatePoint is one 10-minute bucket.
type RatePoint struct {
	T           time.Time `json:"t"`
	Done        int       `json:"done"`
	Failed      int       `json:"failed"`
	Skipped     int       `json:"skipped"`
	NasTimeouts int       `json:"nas_timeouts"`
}

// KPIs are the 24 h figures on a queue page.
type KPIs struct {
	Done24h          int      `json:"done_24h"`
	Failed24h        int      `json:"failed_24h"`
	MedianDurationMs *float64 `json:"median_duration_ms"`
	P95DurationMs    *float64 `json:"p95_duration_ms"`
	SkippedShare24h  float64  `json:"skipped_share_24h"`
	ChangedShare24h  float64  `json:"changed_share_24h"`
}

// QueueDetail is GET /api/queues/{stage}.
type QueueDetail struct {
	Info     StageInfo      `json:"info"`
	Summary  QueueSummary   `json:"summary"`
	KPIs     KPIs           `json:"kpis"`
	Running  []JobRef       `json:"running"`
	NextUp   []JobRef       `json:"next_up"`
	Failures []FailureGroup `json:"failures"`
	Log      []LogLine      `json:"log"`
}

// SubjectSample is a sample subject of a failure group.
type SubjectSample struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

// FailureGroup is one (stage, cause) issue group.
type FailureGroup struct {
	Stage          string          `json:"stage"`
	Cause          string          `json:"cause"`
	Label          string          `json:"label"`
	Count          int             `json:"count"`
	FirstAt        time.Time       `json:"first_at"`
	LastAt         time.Time       `json:"last_at"`
	SampleMessage  string          `json:"sample_message"`
	SampleSubjects []SubjectSample `json:"sample_subjects"`
	Action         string          `json:"action"`
}

// LogLine is one job_events row.
type LogLine struct {
	ID           int64           `json:"id"`
	At           time.Time       `json:"at"`
	JobID        int64           `json:"job_id"`
	Stage        string          `json:"stage"`
	SubjectKind  string          `json:"subject_kind"`
	SubjectID    string          `json:"subject_id"`
	SubjectLabel string          `json:"subject_label"`
	Level        string          `json:"level"`
	Kind         string          `json:"kind"`
	Message      string          `json:"message"`
	Data         json.RawMessage `json:"data"`
}

// RetriggerItem is one subject of a retrigger request.
type RetriggerItem struct {
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Stage       string `json:"stage,omitempty"`
}

// RetriggerRequest is the POST /api/queue/retrigger body.
type RetriggerRequest struct {
	Items    []RetriggerItem `json:"items"`
	Mode     string          `json:"mode,omitempty"`
	Priority *int            `json:"priority,omitempty"`
}

// RetriggerResult is one outcome row.
type RetriggerResult struct {
	SubjectKind string `json:"subject_kind"`
	SubjectID   string `json:"subject_id"`
	Stage       string `json:"stage"`
	JobID       *int64 `json:"job_id"`
	Outcome     string `json:"outcome"`
}

// RetriggerResponse is the POST /api/queue/retrigger answer.
type RetriggerResponse struct {
	Results []RetriggerResult `json:"results"`
}

// ItemSubject heads an ItemHistory.
type ItemSubject struct {
	Kind         string  `json:"kind"`
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	LibraryID    *string `json:"library_id"`
	LibraryLabel *string `json:"library_label"`
}

// ItemQueue is one "re-queue per queue" row.
type ItemQueue struct {
	Stage      string  `json:"stage"`
	LastJob    *JobRef `json:"last_job"`
	CanRequeue bool    `json:"can_requeue"`
}

// SeasonRef lists a season of a series or library.
type SeasonRef struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Season       int    `json:"season"`
	Kind         string `json:"kind"`
	EpisodeCount int    `json:"episode_count"`
}

// AppearsIn is an external object the subject shows up in.
type AppearsIn struct {
	Target     string `json:"target"`
	Title      string `json:"title"`
	ExternalID string `json:"external_id"`
	Detail     string `json:"detail,omitempty"`
}

// TriggeredRef is what a job re-queued.
type TriggeredRef struct {
	Stage        string `json:"stage"`
	SubjectLabel string `json:"subject_label"`
	Outcome      string `json:"outcome"`
}

// ReasonRef is one entry of processing_jobs.reasons.
type ReasonRef struct {
	At  string `json:"at"`
	Why string `json:"why"`
	By  string `json:"by"`
}

// TimelineEntry is one history row.
type TimelineEntry struct {
	Job        JobRef `json:"job"`
	DurationMs *int64 `json:"duration_ms"`
	// Summary is processing_jobs.output_summary: for external writes it
	// carries the diff the write applied.
	Summary json.RawMessage `json:"output_summary"`
	// Changes is what an external write changed (plex_collection,
	// dizquetv_show, write_channels), from the job_events data / summary.
	Changes   []WriteChange  `json:"changes,omitempty"`
	Triggered []TriggeredRef `json:"triggered"`
	Reasons   []ReasonRef    `json:"reasons"`
}

// ItemHistory is GET /api/items/{kind}/{id}/history.
type ItemHistory struct {
	Subject   ItemSubject     `json:"subject"`
	Kinds     []string        `json:"kinds"`
	Queues    []ItemQueue     `json:"queues"`
	Seasons   []SeasonRef     `json:"seasons,omitempty"`
	AppearsIn []AppearsIn     `json:"appears_in"`
	Timeline  []TimelineEntry `json:"timeline"`
	Next      *string         `json:"next"`
}

// StripCell is one stage segment of a library row.
type StripCell struct {
	Stage   string `json:"stage"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Running int    `json:"running"`
	Pending int    `json:"pending"`
	Failed  int    `json:"failed"`
}

// LibraryQueueRow is one row of the libraries table.
type LibraryQueueRow struct {
	LibraryID      string      `json:"library_id"`
	Name           string      `json:"name"`
	Videos         int         `json:"videos"`
	Seasons        int         `json:"seasons"`
	Strip          []StripCell `json:"strip"`
	LastActivityAt *time.Time  `json:"last_activity_at"`
}

// HealthPoint is one sample of a health series.
type HealthPoint struct {
	T time.Time `json:"t"`
	V float64   `json:"v"`
}

// HealthSeries is one metric over time.
type HealthSeries struct {
	Metric string        `json:"metric"`
	Points []HealthPoint `json:"points"`
}

// WriteChange is one thing an external write changed (queueApi.ts).
type WriteChange struct {
	Action string   `json:"action"` // added|removed|reordered|created|deleted|updated
	What   string   `json:"what"`   // episodes|collection|show|channel|slot
	Target string   `json:"target"`
	Count  *int     `json:"count,omitempty"`
	Items  []string `json:"items,omitempty"`
}
