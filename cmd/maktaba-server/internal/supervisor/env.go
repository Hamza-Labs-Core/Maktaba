package supervisor

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/dsn"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ffmpeg"
)

// ChildEnv translates a Config into the environment variables a given
// role's process already understands. This is the one place that knows
// the mapping from the human-facing server.toml to the per-service env
// contract — keeping the role binaries themselves unchanged.
//
// Shared wiring (so api/streaming/pipeline agree on where each other
// lives) is applied to every role; role-specific bind addresses are
// only set for the role that owns them.
//
// The node config is read once for it (NodeOf): one that cannot be read is an error, never
// "not joined", which would start a node's role as the home's against this machine's own
// database.
func ChildEnv(role Role, cfg config.Config) ([]string, error) {
	var n config.NodeConfig
	switch role {
	case RoleAPI, RoleStreaming, RolePipeline:
		var err error
		if n, err = NodeOf(cfg); err != nil {
			return nil, err
		}
	}
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

	// One ffmpeg for every role, the one setup's check and the status check
	// ([media] ffmpeg, the operator's MAKTABA_FFMPEG, the desktop app's own on a
	// Mac or Windows, else the PATH's: on Linux the distribution's, Epic 39).
	exe, _ := os.Executable()
	env = append(env, ffmpeg.Find(cfg.Media, config.AppPayload(exe)).Env()...)

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

	// Linked accounts (Epic 42 story 42.7): on the home that runs the relay
	// agent, the api reads the agent's identity file (the cloud's pinned
	// assertion key) and reports its linked cloud accounts through the agent.
	if role == RoleAPI && RelayWanted(cfg) {
		add("MAKTABA_CLOUD_IDENTITY", RelayIdentityPath())
		add("MAKTABA_CLOUDLINK_ADMIN", RelayAdminAddr(cfg))
	}

	// This machine's stream signing key (Story 10.8): the api signs the video
	// sessions' stream URLs with it (creating it on first start) and the
	// streaming role verifies them with it. Per install: a node signs and
	// verifies its own sessions, the home its own (the via-home fallback too).
	if os.Getenv("MAKTABA_STREAM_SIGNING_KEY") == "" && (role == RoleAPI || role == RoleStreaming) {
		add("MAKTABA_STREAM_SIGNING_KEY", StreamKeyPath())
	}

	// The home's JWT keys (Story 10.6): an operator's MAKTABA_JWT_*_PEM, else the pair a
	// switch of the home handed over (Epic 37), in the keys directory.
	if role == RoleAPI && os.Getenv("MAKTABA_JWT_PRIVATE_KEY_PEM") == "" && os.Getenv("MAKTABA_JWT_PUBLIC_KEY_PEM") == "" {
		// a home with none makes its pair (a node's api verifies with its home's keys)
		if !NodeMode(cfg, n) {
			if err := EnsureJWTKeys(); err != nil {
				return nil, err
			}
		}
		privPath, pubPath := JWTKeyPaths()
		priv, err1 := os.ReadFile(privPath)
		pub, err2 := os.ReadFile(pubPath)
		if err1 == nil && err2 == nil && len(priv) > 0 && len(pub) > 0 {
			add("MAKTABA_JWT_PRIVATE_KEY_PEM", string(priv))
			add("MAKTABA_JWT_PUBLIC_KEY_PEM", string(pub))
		}
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
		peers := "streaming=http://" + LocalAddr(streamingAdmin(cfg), "9101") + "/logs/recent," +
			"pipeline=http://" + pipelineLogs(cfg) + "/logs/recent"
		if IndexWorkerOn(cfg, n) {
			// the semantic search is the index worker's (BuildIndexWorker): the
			// main worker opens no Chroma
			add("MAKTABA_PIPELINE_SEARCH_ADDR", pipelineSearch(cfg))
			peers += ",pipeline-index=http://" + pipelineIndexLogs(cfg) + "/logs/recent"
		}
		add("MAKTABA_LOG_PEERS", peers)
		// Process supervision (Story 22.9): the api proxies the
		// supervisor's state to GET /api/system/processes and records
		// crashes into health_samples.
		add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		// the supervisor's switchover endpoints take its per-boot local token (the home's
		// API runs a switch of the home: specs/epics/37-node-replica)
		add("MAKTABA_SUPERVISOR_TOKEN_FILE", LocalTokenPath(cfg))
		// the cloud relay agent's state (a switch checks it moved with the home)
		if relayWanted(cfg, n) {
			add("MAKTABA_CLOUDLINK_STATUS_URL", RelayStatusURL(cfg))
		}
		// the machines reach the bundled Postgres on its own port, verifying its CA
		if cfg.ClusterDB.Port > 0 {
			add("MAKTABA_CLUSTER_PG_PORT", strconv.Itoa(cfg.ClusterDB.Port))
			add("MAKTABA_CLUSTER_PG_CA", cfg.ClusterDB.CA)
		}
		// Maktaba TV across the cluster (Epic 32): the api forwards move/stop/drain to
		// the home streaming service's loopback internal routes.
		add("MAKTABA_STREAMING_HTTP_URL", "http://"+LocalAddr(cfg.Server.StreamAddr, "8081"))
		// Epic 35 story 35.11: this machine's own web address (the Machines page, the
		// cluster sign-in)
		add("MAKTABA_WEB_URL", cfg.PublicWebURL())
		// Epic 35 story 35.8: a full node's api runs in node mode against the home's
		// database with the node's own role; it never migrates.
		if NodeMode(cfg, n) {
			env = setEnv(env, "DATABASE_URL", HomeDSN(n))
			env = setEnv(env, "MAKTABA_DATABASE_URL", HomeDSN(n))
			env = setEnv(env, "MAKTABA_AUTO_MIGRATE", "false")
			add("MAKTABA_NODE_MODE", "1")
			add("MAKTABA_NODE_ID", n.NodeID)
			add("MAKTABA_NODE_NAME", n.Name)
			add("MAKTABA_HOME_API_URL", n.HomeURL())
			add("MAKTABA_HOME_STREAM_URL", HomeStreamURL(n))
			add("MAKTABA_NODE_WEB_URL", cfg.PublicWebURL())
			add("MAKTABA_AGENT_URL", StateURL(cfg))
			add("MAKTABA_AGENT_TOKEN_FILE", LocalTokenPath(cfg))
			// the machine's cluster-link certificate lives beside node.json; the api holds
			// the machine's link (story 35.16)
			add("MAKTABA_NODE_CERT_DIR", nodeConfigDir())
			// its own state (the sign-ins made here, when the home last answered)
			add("MAKTABA_NODE_STATE_DIR", NodeStateDir(cfg))
			// its copy of the home's database (Epic 37)
			if cfg.NodeCopy.DSN != "" {
				add("MAKTABA_COPY_DATABASE_URL", cfg.NodeCopy.DSN)
			}
		}
	case RoleStreaming:
		// The streaming binary binds MAKTABA_HTTP_ADDR; the older
		// MAKTABA_STREAMING_HTTP_ADDR is kept for anything reading it.
		add("MAKTABA_HTTP_ADDR", cfg.Server.StreamAddr)
		addStreamingCache(cfg, add)
		// a joined node's streaming takes the cluster's map for it (Epic 31)
		if !n.Joined() || forcedHome(cfg) {
			addStreamPathMap(cfg, add)
		}
		add("MAKTABA_STREAMING_HTTP_ADDR", cfg.Server.StreamAddr)
		// [media] transcode_min_free_gib: what the video transcodes leave free on
		// that disk (0: the streaming role's automatic floor; an operator's own
		// is kept)
		if cfg.Media.TranscodeMinFreeGiB != 0 && os.Getenv("MAKTABA_TRANSCODE_MIN_FREE_GIB") == "" {
			add("MAKTABA_TRANSCODE_MIN_FREE_GIB", strconv.Itoa(cfg.Media.TranscodeMinFreeGiB))
		}
		add("MAKTABA_ADMIN_ADDR", streamingAdmin(cfg))
		add("MAKTABA_STREAMING_GRPC_ADDR", streamingGRPC(cfg))
		// story 35.16: the home's front door reads a machine's channel over that
		// machine's cluster link, through this machine's api on loopback
		if !n.Joined() || forcedHome(cfg) {
			add("MAKTABA_LINK_PULL_URL", "http://"+LocalAddr(cfg.Server.Listen, "8080"))
			add("MAKTABA_SUPERVISOR_TOKEN_FILE", LocalTokenPath(cfg))
		}
		// Epic 33 story 33.1: the streaming role is the front door of the TV
		// slots (which encode); it asks the supervisor for the standby and
		// staging instances.
		if SlotsEnabled(cfg) {
			add("MAKTABA_TV_SLOTS", SlotsEnv())
			add("MAKTABA_SUPERVISOR_URL", StateURL(cfg))
		}
		// A machine that joined a home (Epic 31) streams as a cluster node (Epic 32.1):
		// the home's database through its own credential, its node id; no extra flag.
		if n.Joined() && !forcedHome(cfg) {
			env = setEnv(env, "DATABASE_URL", HomeDSN(n))
			env = setEnv(env, "MAKTABA_DATABASE_URL", HomeDSN(n))
			add("MAKTABA_TV_HOST_ROLE", "node")
			add("MAKTABA_NODE_ID", n.NodeID)
			// a full node's streaming role also serves its own VOD (Epic 35 story 35.8),
			// from this machine's copy of the home's database while that is down (Epic 37)
			if NodeMode(cfg, n) {
				add("MAKTABA_FULL_NODE", "1")
				add("MAKTABA_NODE_STATE_DIR", NodeStateDir(cfg))
				if cfg.NodeCopy.DSN != "" {
					add("MAKTABA_COPY_DATABASE_URL", cfg.NodeCopy.DSN)
				}
			}
		}
	case RoleTVSlot:
		// a slot's own addresses are set by BuildSlot; it never joins as a node
		// and serves no gRPC. It encodes the channels, so it writes their segments:
		// the same cache as the streaming role (without it every channel failed,
		// "mkdir /var/maktaba: permission denied", on the Mac app)
		addStreamingCache(cfg, add)
		addStreamPathMap(cfg, add) // slots run on a home only (SlotsEnabled)
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
		if NodeMode(cfg, n) {
			env = setEnv(env, "DATABASE_URL", HomeDSN(n))
			env = setEnv(env, "MAKTABA_DATABASE_URL", HomeDSN(n))
			add("MAKTABA_NODE", "1")
			// story 35.16: its channel agent reaches the home on the machine's cluster
			// link, through this machine's api on loopback (straight to the home while
			// the link is not up)
			add("MAKTABA_CLUSTER_AGENT_URL", LocalAgentURL(cfg))
		}
	}

	// a URL never carries the password: the child reads it from a passfile
	return dsn.ProcessEnv(env, config.KeysDir())
}

// LocalAgentURL is a full node's loopback route to the home's channel over its cluster
// link (the node api's /api/cluster/link/local/agent, story 35.16).
func LocalAgentURL(cfg config.Config) string {
	return "ws://" + LocalAddr(cfg.Server.Listen, "8080") + "/api/cluster/link/local/agent"
}

// nodeConfigDir is the folder of this machine's node.json (tests replace it).
var nodeConfigDir = func() string { return filepath.Dir(config.LocalNodeConfigPath()) }

// NodeOf is this machine's node config as a role's environment, and serve's start (its
// Postgres, the machine agent, its copy of the home's database), are made from it: none
// for a forced home ([server] role = "home"), and an error for one that cannot be read
// (a write that does not end, a broken file, a file another program keeps locked), so
// nothing starts rather than start as the home.
func NodeOf(cfg config.Config) (config.NodeConfig, error) {
	if forcedHome(cfg) {
		return config.NodeConfig{}, nil
	}
	n := nodeConfig()
	if n.Err != nil {
		return n, fmt.Errorf("%w: the roles start once it can be read (as not joined they would run as the home)", n.Err)
	}
	return n, nil
}

// NodeStateDir is where a full node's roles keep their own state: the sign-ins made
// on it while the home was away, when the home last answered, its copy's status.
func NodeStateDir(cfg config.Config) string { return filepath.Join(cfg.Server.DataDir, "node") }

// nodeConfig reads this machine's Epic 31 node config (tests replace it).
var nodeConfig = config.LocalNodeConfig

// HomeDSN is a node's connection to its home's database (config.NodeConfig.HomeDSN:
// verifying the home's CA, pinned in the keys directory). A CA that cannot be pinned is
// said: the connection still verifies, so it fails rather than trusts.
func HomeDSN(n config.NodeConfig) string {
	return withClientCert(homeDSN(n), nodeConfigDir())
}

// withClientCert offers the machine's cluster-link certificate (beside node.json, story
// 35.16) to the home's database on a TLS connection: its CN is the machine's role, which
// the home requires once the link is (clientcert=verify-full). A server that does not ask
// for it ignores it.
func withClientCert(dsn, dir string) string {
	cert, key := filepath.Join(dir, "cluster-node.crt"), filepath.Join(dir, "cluster-node.key")
	if dir == "" {
		return dsn
	}
	if _, err := os.Stat(cert); err != nil {
		return dsn
	}
	if _, err := os.Stat(key); err != nil {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	switch q.Get("sslmode") {
	case "verify-ca", "verify-full", "require":
	default:
		return dsn
	}
	q.Set("sslcert", filepath.ToSlash(cert))
	q.Set("sslkey", filepath.ToSlash(key))
	u.RawQuery = q.Encode()
	return u.String()
}

func homeDSN(n config.NodeConfig) string {
	dsn, err := n.HomeDSN(config.KeysDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[supervisor] the home's database: %v\n", err)
	}
	return dsn
}

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
// node config, or one that cannot be read (it may be a node's: nothing of the
// home's is started beside it), unless [server] role = "home" (or
// MAKTABA_ROLE=home) says it is the home whatever the node config holds.
func NodeJoined(cfg config.Config) bool {
	if forcedHome(cfg) {
		return false
	}
	n := nodeConfig()
	return n.Joined() || n.Err != nil
}

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

// JWTKeyPaths are the home's JWT key pair a switch of the home handed over:
// jwt-private.pem and jwt-public.pem in the keys directory.
func JWTKeyPaths() (private, public string) {
	return filepath.Join(config.KeysDir(), "jwt-private.pem"), filepath.Join(config.KeysDir(), "jwt-public.pem")
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

// addStreamingCache gives a streaming process (the front door and the TV slots) its cache
// (channel segments, transcodes) in this install's cache directory (config.CacheDir:
// <state>/cache, ~/.cache/maktaba in the Linux app): the binary's default, /var/maktaba,
// is the containers' path, which a user cannot create on macOS or Windows. An operator's
// own is kept.
func addStreamingCache(cfg config.Config, add func(k, v string)) {
	if os.Getenv("MAKTABA_CACHE_ROOT") == "" {
		add("MAKTABA_CACHE_ROOT", filepath.Join(cfg.CacheDir(), "streaming"))
	}
}

// addStreamPathMap gives a streaming process [media] path_map in the form it reads:
// MAKTABA_STREAM_PATH_MAP, "from=>to;from2=>to2" (streaming/internal/pathmap). Without
// it the files are opened at the paths the database keeps ("/Volumes/Originals/..."),
// which on the Mac app is a share mounted elsewhere (or a stale mount that hangs), and
// every channel aired filler. MAKTABA_PATH_MAP, which every role gets, is the workers'
// "from=to,from2=to2" form, which streaming does not read. An operator's own is kept.
func addStreamPathMap(cfg config.Config, add func(k, v string)) {
	if os.Getenv("MAKTABA_STREAM_PATH_MAP") != "" {
		return
	}
	if m := StreamPathMap(cfg.Media.PathMap); m != "" {
		add("MAKTABA_STREAM_PATH_MAP", m)
	}
}

// StreamPathMap turns the workers' path map ("from=to,from2=to2") into streaming's
// ("from=>to;from2=>to2"); an entry without "=" or without a from is left out.
func StreamPathMap(workers string) string {
	var out []string
	for _, part := range strings.Split(workers, ",") {
		from, to, ok := strings.Cut(strings.TrimSpace(part), "=")
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if !ok || from == "" {
			continue
		}
		out = append(out, from+"=>"+to)
	}
	return strings.Join(out, ";")
}
