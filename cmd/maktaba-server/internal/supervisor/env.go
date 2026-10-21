package supervisor

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

	// The data volume (data_dir): the api keeps the Sigstore root it refreshes
	// (<data_dir>/sigstore) and uploaded channel art there, and the streaming
	// server reads that art; left to themselves they would look in
	// /var/lib/maktaba, which a user's server cannot write. An operator's own
	// MAKTABA_DATA_DIR is kept.
	if os.Getenv("MAKTABA_DATA_DIR") == "" {
		add("MAKTABA_DATA_DIR", cfg.Server.DataDir)
	}

	// Auto-run migrations before the api binds (the api honours this),
	// so a fresh single-binary install doesn't need a separate
	// `migrate up` step before the first `serve`.
	add("MAKTABA_AUTO_MIGRATE", "true")

	// Media roots — comma-joined for the scanner.
	add("MAKTABA_MEDIA_ROOTS", strings.Join(cfg.Media.Roots, ","))

	// The share's paths on this machine ([media] path_map): the api's cluster hub
	// writes <share>/maktaba/cluster through it (the pipeline reads it as Plex's
	// fallback map, another thing: it gets MAKTABA_WORKER_PATH_MAP below).
	if role != RolePipeline && os.Getenv("MAKTABA_PATH_MAP") == "" {
		add("MAKTABA_PATH_MAP", strings.TrimSpace(cfg.Media.PathMap))
	}
	// The install's large state (Epic 36 story 36.2: on a Mac beside data/ in
	// ~/Library/Application Support/Maktaba): models and the semantic-search index.
	// (MAKTABA_MODEL_CACHE_DIR: where this machine keeps the models it downloads;
	// MAKTABA_MODELS_DIR is the share's models folder the home publishes for the
	// nodes, left to the operator.)
	if os.Getenv("MAKTABA_MODEL_CACHE_DIR") == "" {
		add("MAKTABA_MODEL_CACHE_DIR", filepath.Join(cfg.StateDir(), "models"))
	}
	if os.Getenv("MAKTABA_CHROMA_DIR") == "" {
		add("MAKTABA_CHROMA_DIR", filepath.Join(cfg.StateDir(), "chroma"))
	}

	// Maktaba.app (Epic 36): the payload's ffmpeg for every role.
	env = append(env, bundleEnv(config.AppBundle())...)

	// The language model server every LLM stage uses (the bundled Ollama at the
	// port serve claimed, or [llm] url): the pipeline takes it as the model server of its
	// rules unless the rules name one; the api's dependency checks probe it. An
	// operator's own MAKTABA_OLLAMA_URL is kept.
	if os.Getenv("MAKTABA_OLLAMA_URL") == "" {
		add("MAKTABA_OLLAMA_URL", LLMURL(cfg))
	}

	// Cross-service discovery: the api needs to reach streaming +
	// pipeline over gRPC. These default ports mirror the compose stack.
	// (Streaming's gRPC port, not its HTTP stream_addr.)
	add("MAKTABA_STREAMING_ADDR", streamingGRPC(cfg))
	add("MAKTABA_PIPELINE_ADDR", pipelineGRPC(cfg))
	// every role knows it runs under the supervisor (the supervisor is the one mDNS
	// advertiser of the machine, Epic 35 story 35.11)
	add("MAKTABA_SUPERVISED", "1")

	// [server] role = "home" (or MAKTABA_ROLE=home): no role runs as a node. Not
	// adding the node's variables is not enough: streaming (channel.DetectRole,
	// cluster.NodeMode) and the pipeline read the node config themselves, and on
	// Windows that may be a joined %APPDATA%\Maktaba\node.json of another install
	// (the roaming fallback). So every role is told the role, and pointed at a
	// node config that is never written.
	if forcedHome(cfg) {
		env = setEnv(env, "MAKTABA_CLUSTER_ROLE", "home")
		env = setEnv(env, "MAKTABA_TV_HOST_ROLE", "home")
		env = setEnv(env, "MAKTABA_NODE_CONFIG", NoNodeConfigPath(cfg))
	}

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

	// This machine's stream signing key (Story 10.8): the api signs the video
	// sessions' stream URLs with it (creating it on first start) and the
	// streaming role verifies them with it. Per install: a node signs and
	// verifies its own sessions, the home its own (the via-home fallback too).
	if os.Getenv("MAKTABA_STREAM_SIGNING_KEY") == "" && (role == RoleAPI || role == RoleStreaming) {
		add("MAKTABA_STREAM_SIGNING_KEY", StreamKeyPath())
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
			"streaming=http://"+LocalAddr(streamingAdmin(cfg), "9101")+"/logs/recent,"+
				"pipeline=http://"+pipelineLogs(cfg)+"/logs/recent")
		// Process supervision (Story 22.9): the api proxies the
		// supervisor's state to GET /api/system/processes and records
		// crashes into health_samples.
		add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		// Maktaba TV across the cluster (Epic 32): the api forwards move/stop/drain to
		// the home streaming service's loopback internal routes.
		add("MAKTABA_STREAMING_HTTP_URL", "http://"+LocalAddr(cfg.Server.StreamAddr, "8081"))
		// Epic 35 story 35.11: this machine's own web address (the Machines page, the
		// cluster sign-in)
		add("MAKTABA_WEB_URL", cfg.PublicWebURL())
		// Epic 35 story 35.8: a full node's api runs in node mode against the home's
		// database with the node's own role; it never migrates.
		if n := nodeConfig(); NodeMode(cfg, n) {
			env = setEnv(env, "DATABASE_URL", n.DatabaseURL)
			env = setEnv(env, "MAKTABA_DATABASE_URL", n.DatabaseURL)
			env = setEnv(env, "MAKTABA_AUTO_MIGRATE", "false")
			add("MAKTABA_NODE_MODE", "1")
			add("MAKTABA_NODE_ID", n.NodeID)
			add("MAKTABA_NODE_NAME", n.Name)
			add("MAKTABA_HOME_API_URL", n.HomeURL())
			add("MAKTABA_HOME_STREAM_URL", HomeStreamURL(n))
			add("MAKTABA_NODE_WEB_URL", cfg.PublicWebURL())
			add("MAKTABA_AGENT_URL", StateURL(cfg))
			add("MAKTABA_AGENT_TOKEN_FILE", LocalTokenPath(cfg))
		}
	case RoleStreaming:
		// The streaming binary binds MAKTABA_HTTP_ADDR; the older
		// MAKTABA_STREAMING_HTTP_ADDR is kept for anything reading it.
		add("MAKTABA_HTTP_ADDR", cfg.Server.StreamAddr)
		// its cache (channel segments, transcodes) in this install's cache
		// directory (config.CacheDir: <state>/cache, ~/.cache/maktaba in the Linux
		// app): the binary's default, /var/maktaba, is the containers' path, which a
		// user cannot create on macOS or Windows. An operator's own is kept.
		if os.Getenv("MAKTABA_CACHE_ROOT") == "" {
			add("MAKTABA_CACHE_ROOT", filepath.Join(cfg.CacheDir(), "streaming"))
		}
		add("MAKTABA_STREAMING_HTTP_ADDR", cfg.Server.StreamAddr)
		add("MAKTABA_ADMIN_ADDR", streamingAdmin(cfg))
		add("MAKTABA_STREAMING_GRPC_ADDR", streamingGRPC(cfg))
		// Epic 33 story 33.1: the streaming role is the front door of the TV
		// slots (which encode); it asks the supervisor for the standby and
		// staging instances.
		if SlotsEnabled(cfg) {
			add("MAKTABA_TV_SLOTS", SlotsEnv())
			add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		}
		// A machine that joined a home (Epic 31) streams as a cluster node (Epic 32.1):
		// the home's database through its own credential, its node id; no extra flag.
		if n := nodeConfig(); n.Joined() && !forcedHome(cfg) {
			env = setEnv(env, "DATABASE_URL", n.DatabaseURL)
			env = setEnv(env, "MAKTABA_DATABASE_URL", n.DatabaseURL)
			add("MAKTABA_TV_HOST_ROLE", "node")
			add("MAKTABA_NODE_ID", n.NodeID)
			// a full node's streaming role also serves its own VOD (Epic 35 story 35.8)
			if NodeMode(cfg, n) {
				add("MAKTABA_FULL_NODE", "1")
			}
		}
	case RoleTVSlot:
		// a slot's own addresses are set by BuildSlot; it never joins as a node
		// and serves no gRPC
	case RolePipeline:
		// The pipeline exposes its gRPC server so the api can call it.
		add("MAKTABA_PIPELINE_GRPC_ADDR", pipelineGRPC(cfg))
		// Recent-logs HTTP endpoint drained by the api's diagnostics
		// export proxy (see MAKTABA_LOG_PEERS on the api role).
		// Its /healthz (event-loop heartbeat) is the pipeline's liveness
		// probe.
		add("MAKTABA_PIPELINE_LOGS_ADDR", pipelineLogs(cfg))
		// [transcription]: the models the transcribe stage loads (Epic 36 story 36.4)
		for _, kv := range sttEnv(cfg) {
			if k, _, _ := strings.Cut(kv, "="); os.Getenv(k) == "" {
				env = append(env, kv)
			}
		}
		if os.Getenv("MAKTABA_WORKER_PATH_MAP") == "" {
			add("MAKTABA_WORKER_PATH_MAP", strings.TrimSpace(cfg.Media.PathMap))
		}
		// Windows (Epic 38 story 38.1): Python writes its pipes in the ANSI code
		// page unless told otherwise, which cannot hold an Arabic title (a log line
		// with one raised UnicodeEncodeError); unbuffered, so the server's log has
		// each line as it happens.
		if onWindows() {
			for _, kv := range []string{"PYTHONUTF8=1", "PYTHONIOENCODING=utf-8", "PYTHONUNBUFFERED=1"} {
				if k, _, _ := strings.Cut(kv, "="); os.Getenv(k) == "" {
					env = append(env, kv)
				}
			}
		}
		// the main worker's share of the transcription plan is Build's (PlanFor:
		// it asks the pipeline's Python), the workers' BuildPipelineInstances'
		// Epic 35 story 35.8: a full node's pipeline works for the home as this
		// machine's node (node.json: its database role, node id). The pipeline only
		// reads node.json without a database URL, so [database].url above would
		// make it a home worker against this machine's own database.
		if n := nodeConfig(); NodeMode(cfg, n) {
			env = setEnv(env, "DATABASE_URL", n.DatabaseURL)
			env = setEnv(env, "MAKTABA_DATABASE_URL", n.DatabaseURL)
			add("MAKTABA_NODE", "1")
		}
	}

	return env
}

// nodeConfig reads this machine's Epic 31 node config (tests replace it).
var nodeConfig = config.LocalNodeConfig

// onWindows reports a Windows host (tests replace it).
var onWindows = func() bool { return runtime.GOOS == "windows" }

// NodeConfig is this machine's node config as the supervisor reads it.
func NodeConfig() config.NodeConfig { return nodeConfig() }

// NodeMode reports whether this machine runs as a full node (Epic 35 story 35.8): a
// joined node config, unless [server] role = "home" (or MAKTABA_ROLE=home) says
// otherwise.
func NodeMode(cfg config.Config, n config.NodeConfig) bool {
	return !forcedHome(cfg) && n.Joined() && n.HomeURL() != ""
}

// CheckNodeConfig refuses a joined node config the node's roles cannot run with: no
// node_id or private_key (a hand-edited or half-written node.json), on which the api
// refused to start and the pipeline failed with a KeyError, each restarted forever;
// or a node.json that cannot be read at all, which read as not joined and started the
// node as a home (migrating its own database).
func CheckNodeConfig(cfg config.Config) error {
	n := nodeConfig()
	if forcedHome(cfg) {
		return nil
	}
	if n.Err != nil {
		return fmt.Errorf("%w: fix it, or move it aside and re-join this machine (maktaba-pipeline join), or set [server] role = \"home\"", n.Err)
	}
	if !n.Joined() {
		return nil
	}
	if missing := n.Missing(); len(missing) > 0 {
		return fmt.Errorf("node config %s has no %s: move it aside and re-join this machine (maktaba-pipeline join), or set [server] role = \"home\"",
			config.LocalNodeConfigPath(), strings.Join(missing, " or "))
	}
	return nil
}

// NoNodeConfigPath is the node config a forced home's roles are pointed at:
// a path in the install's state that nothing writes, so none reads a node
// config.
func NoNodeConfigPath(cfg config.Config) string {
	return filepath.Join(cfg.StateDir(), "state", "home-has-no-node-config.json")
}

// NodeJoined reports whether this machine counts as a joined node: a joined
// node config, unless [server] role = "home" (or MAKTABA_ROLE=home) says it is
// the home whatever the node config holds.
func NodeJoined(cfg config.Config) bool { return !forcedHome(cfg) && nodeConfig().Joined() }

// forcedHome: [server] role = "home" or MAKTABA_ROLE=home.
func forcedHome(cfg config.Config) bool {
	role := strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_ROLE")))
	if role == "" {
		role = strings.ToLower(strings.TrimSpace(cfg.Server.Role))
	}
	return role == "home"
}

// HomeStreamURL is the home's streaming port, its TV front door (Epic 35 design
// §11.3): MAKTABA_HOME_STREAM_URL, else the host of the home's API URL on 8081.
func HomeStreamURL(n config.NodeConfig) string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("MAKTABA_HOME_STREAM_URL")), "/"); v != "" {
		return v
	}
	u, err := url.Parse(n.HomeURL())
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + net.JoinHostPort(u.Hostname(), "8081")
}

// LocalTokenPath is the per-boot token of the supervisor's loopback endpoints the
// node API uses (<data_dir>/supervisor/local.token, design §9.2).
func LocalTokenPath(cfg config.Config) string {
	return filepath.Join(cfg.Server.DataDir, "supervisor", "local.token")
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func streamingGRPC(cfg config.Config) string {
	return orDefault(cfg.Server.StreamingGRPCAddr, StreamingGRPCAddr)
}
func streamingAdmin(cfg config.Config) string {
	return orDefault(cfg.Server.StreamingAdminAddr, streamingAdminAddr)
}
func pipelineGRPC(cfg config.Config) string {
	return orDefault(cfg.Server.PipelineGRPCAddr, "127.0.0.1:9090")
}
func pipelineLogs(cfg config.Config) string {
	return orDefault(cfg.Server.PipelineLogsAddr, pipelineLogsAddr)
}

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

// SecretsKeyPath is the home's secrets key file: secrets.key in the keys
// directory (config.KeysDir: beside server.toml, or keys/ on a Mac).
func SecretsKeyPath() string {
	return filepath.Join(config.KeysDir(), "secrets.key")
}

// StreamKeyPath is this machine's stream signing key: stream-signing.key in
// the keys directory (next to secrets.key).
func StreamKeyPath() string {
	return filepath.Join(config.KeysDir(), "stream-signing.key")
}

// withStage adds stage to a comma-separated stage list (once).
func withStage(list, stage string) string {
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" && s != stage {
			out = append(out, s)
		}
	}
	return strings.Join(append(out, stage), ",")
}
