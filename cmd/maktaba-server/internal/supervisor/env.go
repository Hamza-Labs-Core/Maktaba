package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// ChildEnv translates a Config into the environment variables a given
// role's process already understands. This is the one place that knows
// the mapping from the human-facing server.toml to the per-service env
// contract — keeping the role binaries themselves unchanged.
//
// Shared wiring (so api/streaming/pipeline agree on where each other
// lives) is applied to every role; role-specific bind addresses are
// only set for the role that owns them.
func ChildEnv(role Role, cfg config.Config) []string {
	env := []string{}
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}

	// Storage: the api + streaming read DATABASE_URL; the pipeline reads
	// MAKTABA_DATABASE_URL. Set both so a single [database].url drives
	// the whole stack regardless of which service consumes it.
	add("DATABASE_URL", cfg.Database.URL)
	add("MAKTABA_DATABASE_URL", cfg.Database.URL)

	// Auto-run migrations before the api binds (the api honours this),
	// so a fresh single-binary install doesn't need a separate
	// `migrate up` step before the first `serve`.
	add("MAKTABA_AUTO_MIGRATE", "true")

	// Media roots — comma-joined for the scanner.
	add("MAKTABA_MEDIA_ROOTS", strings.Join(cfg.Media.Roots, ","))

	// Transcription backend/model for the pipeline workers.
	add("MAKTABA_TRANSCRIPTION_BACKEND", cfg.Transcription.Backend)
	add("MAKTABA_TRANSCRIPTION_MODEL", cfg.Transcription.Model)
	add("MAKTABA_MODELS_DIR", cfg.Server.DataDir+"/models")

	// Cross-service discovery: the api needs to reach streaming +
	// pipeline over gRPC. These default ports mirror the compose stack.
	// (Streaming's gRPC port, not its HTTP stream_addr.)
	add("MAKTABA_STREAMING_ADDR", StreamingGRPCAddr)
	add("MAKTABA_PIPELINE_ADDR", "127.0.0.1:9090")

	// Where other devices reach the TV: streaming advertises it (SSDP,
	// discover.json, XMLTV/M3U links) and the api reports it to the web
	// app (/api/tv/info).
	add("MAKTABA_PUBLIC_BASE_URL", cfg.PublicStreamURL())

	// The home's secrets key (Epic 32 story 32.6): the api seals
	// home_secrets with it and the pipeline opens the extension secrets
	// (the Plex token) with it, so both get the same file, beside
	// server.toml. An operator's own MAKTABA_SECRETS_KEY is kept.
	if os.Getenv("MAKTABA_SECRETS_KEY") == "" {
		add("MAKTABA_SECRETS_KEY", SecretsKeyPath())
	}

	switch role {
	case RoleAPI:
		add("MAKTABA_HTTP_ADDR", cfg.Server.Listen)
		add("MAKTABA_ADMIN_ADDR", cfg.Server.AdminAddr)
		// Diagnostics export: the api proxies the streaming + pipeline
		// ring buffers into the bundle. Streaming serves /logs/recent on
		// its admin port (9101); the pipeline on its dedicated logs port
		// (9102, set below). Without this the bundle would carry api logs
		// only.
		add("MAKTABA_LOG_PEERS",
			"streaming=http://127.0.0.1:9101/logs/recent,"+
				"pipeline=http://127.0.0.1:9102/logs/recent")
		// Process supervision (Story 22.9): the api proxies the
		// supervisor's state to GET /api/system/processes and records
		// crashes into health_samples.
		add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		// Maktaba TV across the cluster (Epic 32): the api forwards move/stop/drain to
		// the home streaming service's loopback internal routes.
		add("MAKTABA_STREAMING_HTTP_URL", "http://"+LocalAddr(cfg.Server.StreamAddr, "8081"))
	case RoleStreaming:
		// The streaming binary binds MAKTABA_HTTP_ADDR; the older
		// MAKTABA_STREAMING_HTTP_ADDR is kept for anything reading it.
		add("MAKTABA_HTTP_ADDR", cfg.Server.StreamAddr)
		add("MAKTABA_STREAMING_HTTP_ADDR", cfg.Server.StreamAddr)
		add("MAKTABA_ADMIN_ADDR", streamingAdminAddr)
		// Epic 33 story 33.1: the streaming role is the front door of the TV
		// slots (which encode); it asks the supervisor for the standby and
		// staging instances.
		if SlotsEnabled(cfg) {
			add("MAKTABA_TV_SLOTS", SlotsEnv())
			add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		}
		// A machine that joined a home (Epic 31) streams as a cluster node (Epic 32.1):
		// the home's database through its own credential, its node id; no extra flag.
		if n := nodeConfig(); n.Joined() {
			env = setEnv(env, "DATABASE_URL", n.DatabaseURL)
			env = setEnv(env, "MAKTABA_DATABASE_URL", n.DatabaseURL)
			add("MAKTABA_TV_HOST_ROLE", "node")
			add("MAKTABA_NODE_ID", n.NodeID)
		}
	case RoleTVSlot:
		// a slot's own addresses are set by BuildSlot; it never joins as a node
		// and serves no gRPC
	case RolePipeline:
		// The pipeline exposes its gRPC server so the api can call it.
		add("MAKTABA_PIPELINE_GRPC_ADDR", "127.0.0.1:9090")
		// Recent-logs HTTP endpoint drained by the api's diagnostics
		// export proxy (see MAKTABA_LOG_PEERS on the api role).
		// Its /healthz (event-loop heartbeat) is the pipeline's liveness
		// probe.
		add("MAKTABA_PIPELINE_LOGS_ADDR", pipelineLogsAddr)
	}

	return env
}

// nodeConfig reads this machine's Epic 31 node config (tests replace it).
var nodeConfig = config.LocalNodeConfig

// setEnv replaces (or adds) k in env.
func setEnv(env []string, k, v string) []string {
	out := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, k+"=") {
			out = append(out, e)
		}
	}
	return append(out, k+"="+v)
}

// StreamingGRPCAddr is the streaming service's intra-host gRPC address.
const StreamingGRPCAddr = "127.0.0.1:9050"

const (
	// streamingAdminAddr is the streaming admin port (/healthz, /readyz,
	// /logs/recent).
	streamingAdminAddr = ":9101"
	// pipelineLogsAddr is the pipeline's HTTP port (/logs/recent,
	// /healthz).
	pipelineLogsAddr = "127.0.0.1:9102"
)

// StateURL is the base URL of the supervisor's state endpoint, or
// "" when [supervisor] admin_addr is switched off.
func StateURL(cfg config.Config) string {
	p, err := PolicyFromConfig(cfg.Supervisor)
	if err != nil || p.AdminAddr == "" {
		return ""
	}
	return "http://" + LocalAddr(p.AdminAddr, "9103")
}

// LocalAddr turns a bind spec like ":8081" or "0.0.0.0:8081" into a
// dialable 127.0.0.1:<port> address for intra-host gRPC/HTTP. Falls back
// to the supplied default port when the spec has no parseable port.
func LocalAddr(bind, defaultPort string) string {
	port := defaultPort
	if i := strings.LastIndexByte(bind, ':'); i >= 0 && i+1 < len(bind) {
		port = bind[i+1:]
	}
	return fmt.Sprintf("127.0.0.1:%s", port)
}

// SecretsKeyPath is the home's secrets key file: secrets.key in the
// directory of server.toml (config.Path, which honours MAKTABA_CONFIG).
func SecretsKeyPath() string {
	return filepath.Join(filepath.Dir(config.Path()), "secrets.key")
}
