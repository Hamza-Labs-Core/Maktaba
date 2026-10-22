// Package systemstatus answers "is everything OK, and if not, what is wrong
// and what do I do" for the System health page (/admin/health):
//
//	GET /api/admin/system/status   (admin) every part of Maktaba, each ok /
//	                               degraded / down with a plain reason, when
//	                               it was checked and its help entry, and
//	                               the overall state
//
// The public liveness contract (GET /api/system/health, Story 21.4) is not
// touched: it stays the unauthenticated probe the orchestrator polls. This
// endpoint is the admin's view and reads far more: the database, the
// streaming service and its machines, the pipeline, the worker registry,
// Epic 40's dependency checks and queue health, Plex Live TV, the TV
// changes and slots, and the supervised processes when maktaba-server runs
// them.
//
// Every check runs at the same time under its own deadline (rules
// system_health.check_timeout_ms); a check that does not answer in time is
// reported as not answering, never waited for. The whole answer is reused
// for system_health.cache_seconds, so many open pages cost one round.
package systemstatus

import "time"

// Status values of an Item and of the Report.
const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)

// Groups the page shows the items in, in this order.
const (
	GroupServices     = "services"
	GroupWorkers      = "workers"
	GroupDependencies = "dependencies"
	GroupIntegrations = "integrations"
	GroupQueuesTV     = "queues_tv"
)

// Kinds of item. Each is a help entry: /help/system-health#item-<kind>
// ("_" becomes "-"); help_coverage_test.go keeps them in step.
const (
	KindAPI           = "api"
	KindPostgres      = "postgres"
	KindStreaming     = "streaming"
	KindPipeline      = "pipeline"
	KindProcess       = "process"
	KindWorkers       = "workers"
	KindWorker        = "worker"
	KindStreamingHost = "streaming_host"
	KindDependency    = "dependency"
	KindPlexLiveTV    = "plex_livetv"
	KindQueues        = "queues"
	KindTVChannels    = "tv_channels"
	KindTVChange      = "tv_change"
	KindTVSlot        = "tv_slot"
	KindTVTuner       = "tv_tuner"
)

// Kinds lists every kind (the help coverage test).
var Kinds = []string{
	KindAPI, KindPostgres, KindStreaming, KindPipeline, KindProcess, KindWorkers, KindWorker,
	KindStreamingHost, KindDependency, KindPlexLiveTV, KindQueues, KindTVChannels, KindTVChange,
	KindTVSlot, KindTVTuner,
}

// Item is one part of Maktaba on the page.
type Item struct {
	// ID is stable across answers: "api", "worker:<id>", "dependency:<name>".
	ID    string `json:"id"`
	Group string `json:"group"`
	Kind  string `json:"kind"`
	// Name is what the page shows as the item's title (a machine, a share).
	Name   string `json:"name"`
	Status string `json:"status"`
	// Code names the reason for the web catalog (health.reason.<code>), with
	// Params; Reason is the same in English, for a client without the code.
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
	Reason string         `json:"reason"`
	// Critical: this item down makes the whole page Down.
	Critical bool `json:"critical,omitempty"`
	// History: a stale record kept for reference (a worker replaced by a
	// newer one on the same machine); never a failure.
	History bool `json:"history,omitempty"`
	// Attention: ok, but something waits for the admin (a TV change to
	// approve, items that need you).
	Attention   bool      `json:"attention,omitempty"`
	LastChecked time.Time `json:"last_checked"`
	// Help is the help entry key (the kind); the web app turns it into
	// /help/system-health#item-<kind>.
	Help string `json:"help"`
	// Link is the app page that shows more, when there is one.
	Link    string         `json:"link,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// Report is the GET /api/admin/system/status body.
type Report struct {
	Status string `json:"status"`
	// Code / Params / Reason say why, the same way an item does.
	Code      string         `json:"code"`
	Params    map[string]any `json:"params,omitempty"`
	Reason    string         `json:"reason"`
	CheckedAt time.Time      `json:"checked_at"`
	Items     []Item         `json:"items"`
	Rules     Rules          `json:"rules"`
	// Processes says whether maktaba-server supervises the processes
	// (the page keeps its Processes card either way).
	Processes bool `json:"processes_supervised"`
}
