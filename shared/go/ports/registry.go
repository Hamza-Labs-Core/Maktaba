package ports

// registry is every entry, in the order declared below (add appends to it).
var registry []Entry

func add(e Entry) Entry {
	if e.Proto == "" {
		e.Proto = TCP
	}
	registry = append(registry, e)
	return e
}

// The sizes of the ranges, as constants for the code that bounds them.
const (
	// TVSlots is the number of TV slot instances (blue, green, amber).
	TVSlots = 3
	// TranscriptionWorkers bounds [pipeline] instances: the transcription workers
	// stt-1..16 beside the main pipeline worker.
	TranscriptionWorkers = 16
)

// The api (maktaba-api).
var (
	APIPublic = add(Entry{Name: "api", Owner: "api", Host: AllIPv4, Port: 8080,
		Purpose: "the REST API and the cluster link ([server] listen)"})
	APIAdmin = add(Entry{Name: "api admin", Owner: "api", Host: Loopback, Port: 9100,
		Purpose: "/healthz, /readyz, logs and metrics ([server] admin_addr)"})
)

// maktaba-server itself (serve): the web pages, the supervisor's endpoint, mDNS.
var (
	Web = add(Entry{Name: "web", Owner: "serve", Host: AllIPv4, Port: 8088,
		Purpose: "the web pages, /api and /stream/ in front of the roles ([server] web_listen)"})
	WebHTTPS = add(Entry{Name: "web https", Owner: "serve", Host: AllInterfaces, Port: 8443,
		Purpose: "the web pages over HTTPS while the machine keeps a web certificate (tls_web_https)"})
	Supervisor = add(Entry{Name: "supervisor", Owner: "serve", Host: Loopback, Port: 9103,
		Purpose: "the supervisor's /processes and its local endpoints ([supervisor] admin_addr)"})
	// PortFallback is where setup and serve move the web, streaming and API ports
	// when another program holds 8088, 8081 or 8080 (config/ports.go): web tries
	// the first, streaming the second, the API the third, then the rest in order.
	PortFallback = add(Entry{Name: "port fallback", Owner: "serve", Host: AllIPv4, Port: 8780, Count: 11,
		Purpose: "the web, streaming and API ports moved off one another program holds"})
	MDNS = add(Entry{Name: "mdns", Owner: "serve", Host: AllInterfaces, Port: 5353, Proto: UDP,
		Purpose: "the machine's mDNS advertisement (shared with the OS's responder)"})
)

// Streaming (maktaba-streaming serve), the TV front door.
var (
	StreamingPublic = add(Entry{Name: "streaming", Owner: "streaming", Host: AllInterfaces, Port: 8081,
		Purpose: "HLS, the TV front door, Plex's HDHomeRun tuner ([server] stream_addr)"})
	// StreamingAdmin listens on every interface by default (":9101"), as it always has.
	StreamingAdmin = add(Entry{Name: "streaming admin", Owner: "streaming", Host: AllInterfaces, Port: 9101,
		Purpose: "/healthz, /readyz, /logs/recent ([server] streaming_admin_addr)"})
	// StreamingMetrics is read into the streaming config (MAKTABA_METRICS_ADDR) but
	// nothing serves it today; it stays reserved.
	StreamingMetrics = add(Entry{Name: "streaming metrics", Owner: "streaming", Host: AllInterfaces, Port: 9091,
		Purpose: "reserved for streaming's metrics (MAKTABA_METRICS_ADDR)"})
	StreamingGRPC = add(Entry{Name: "streaming grpc", Owner: "streaming", Host: Loopback, Port: 9050,
		Purpose: "the api's calls into streaming ([server] streaming_grpc_addr)"})
	SSDP = add(Entry{Name: "ssdp", Owner: "streaming", Host: AllInterfaces, Port: 1900, Proto: UDP,
		Purpose: "the HDHomeRun tuner's SSDP discovery (multicast, while it is on)"})
	HDHomeRunDiscover = add(Entry{Name: "hdhomerun discover", Owner: "streaming", Host: AllInterfaces, Port: 65001, Proto: UDP,
		Purpose: "the HDHomeRun discover protocol (while the tuner is on)"})
)

// The TV slots (maktaba-streaming slot --slot blue|green|amber), the channel engine.
var (
	TVSlotPublic = add(Entry{Name: "tv-slot", Owner: "tv-slot", Host: Loopback, Port: 8791, Count: TVSlots,
		Purpose: "a slot's channel output the front door reads (blue, green, amber)"})
	TVSlotAdmin = add(Entry{Name: "tv-slot admin", Owner: "tv-slot", Host: Loopback, Port: 9111, Count: TVSlots,
		Purpose: "a slot's /healthz and /readyz (blue, green, amber)"})
)

// The pipeline workers (python -m maktaba_pipeline).
var (
	PipelineGRPC = add(Entry{Name: "pipeline grpc", Owner: "pipeline", Host: Loopback, Port: 9090,
		Purpose: "the api's calls into the main worker ([server] pipeline_grpc_addr)"})
	PipelineLogs = add(Entry{Name: "pipeline logs", Owner: "pipeline", Host: Loopback, Port: 9102,
		Purpose: "the main worker's /healthz and /logs/recent ([server] pipeline_logs_addr)"})
	IndexSearch = add(Entry{Name: "index search", Owner: "pipeline/index", Host: Loopback, Port: 9093,
		Purpose: "the index worker's semantic search ([server] pipeline_search_addr)"})
	IndexLogs = add(Entry{Name: "index logs", Owner: "pipeline/index", Host: Loopback, Port: 9104,
		Purpose: "the index worker's /healthz and /logs/recent ([server] pipeline_index_logs_addr)"})
	// TranscriptionLogs: worker stt-i listens on At(i-1). It was 9110+i until the
	// workers took the TV slots' admin ports (PR #439).
	TranscriptionLogs = add(Entry{Name: "transcription logs", Owner: "pipeline/stt", Host: Loopback,
		Port: 9131, Count: TranscriptionWorkers,
		Purpose: "a transcription worker's /healthz and /logs/recent (stt-1..16)"})
)

// The other roles the supervisor runs.
var (
	RelayAdmin = add(Entry{Name: "relay admin", Owner: "relay", Host: Loopback, Port: 8765,
		Purpose: "the cloud relay agent's status ([cloud] admin_addr)"})
	// Postgres is the bundled Postgres (Maktaba.app, the desktop apps) and a full
	// node's copy of its home's database (never both on one machine). It listens on
	// the network too while a machine may connect (pgbundle.WatchReach).
	Postgres = add(Entry{Name: "postgres", Owner: "postgres", Host: Loopback, Port: 54329,
		Purpose: "the bundled Postgres ([postgres] port; a node's copy: copy_port)"})
	LLM = add(Entry{Name: "llm", Owner: "ollama", Host: Loopback, Port: 11533,
		Purpose: "the bundled language model server ([llm] port; a free one when taken)"})
)

// Other programs' ports Maktaba keeps off.
var (
	PostgresStandard = add(Entry{Name: "postgres (a user's own)", Owner: "another program", Host: AllInterfaces,
		Port: 5432, Foreign: true, Purpose: "a Postgres the user runs; the bundled one stays off it"})
	Ollama = add(Entry{Name: "ollama (a user's own)", Owner: "another program", Host: Loopback,
		Port: 11434, Foreign: true, Purpose: "a user's own Ollama, the default language model server the pipeline asks"})
)
