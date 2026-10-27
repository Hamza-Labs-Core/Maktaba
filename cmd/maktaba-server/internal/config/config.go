// Package config is the single human-facing configuration surface for
// maktaba-server. It loads/saves server.toml and translates it into the
// environment each underlying service already consumes, so the role
// binaries need zero changes to be driven from one config file.
//
// Path resolution follows the platform conventions (paths.go):
//
//	macOS   : ~/Library/Application Support/Maktaba/server.toml (Epic 36)
//	Linux   : $XDG_CONFIG_HOME/maktaba/server.toml
//	          (fallback ~/.config/maktaba/server.toml)
//	Windows : %LOCALAPPDATA%\Maktaba\server.toml (Epic 38; before it %APPDATA%,
//	          copied by MigrateWindowsDirs, the original kept)
//
// $MAKTABA_CONFIG overrides the resolved path everywhere (tests, custom
// installs, container bind-mounts).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// Config mirrors the server.toml schema documented in the task. Every
// section is optional; zero values fall back to Defaults().
type Config struct {
	Server        ServerSection        `toml:"server"`
	Database      DatabaseSection      `toml:"database"`
	Media         MediaSection         `toml:"media"`
	Transcription TranscriptionSection `toml:"transcription"`
	Cloud         CloudSection         `toml:"cloud"`
	Supervisor    SupervisorSection    `toml:"supervisor"`
	TV            TVSection            `toml:"tv"`
	Pipeline      PipelineSection      `toml:"pipeline"`
	Postgres      PostgresSection      `toml:"postgres"`
	LLM           LLMSection           `toml:"llm"`
	Telemetry     TelemetrySection     `toml:"telemetry"`
	Backups       BackupsSection       `toml:"backups"`

	// DatabaseURLSet: server.toml sets [database] url (Load), or a caller chose
	// one to keep (setup); Save writes the url only then. Unset, a Mac with the
	// bundled Postgres uses it (Epic 36 story 36.1).
	DatabaseURLSet bool `toml:"-"`
	// PipelineInstancesSet: server.toml sets [pipeline] instances, or a caller
	// chose a count to keep (setup --node); Save writes a 0 only then (0 there
	// means no transcription workers, even on Apple Silicon).
	PipelineInstancesSet bool `toml:"-"`
	// ClusterDB is how the cluster's machines reach this home's bundled Postgres
	// (serve fills it in; zero: they are not served by it).
	ClusterDB ClusterDB `toml:"-"`
	// NodeCopy is a full node's copy of its home's database (Epic 37, nodecopy), as
	// serve resolved it ("": none).
	NodeCopy NodeCopy `toml:"-"`
}

// NodeCopy: the address this machine's programs reach its copy of the home's database
// at (MAKTABA_COPY_DATABASE_URL).
type NodeCopy struct {
	DSN string
}

// ClusterDB: the bundled Postgres's port on the LAN and the CA certificate its
// clients verify it with ("" with TLS off).
type ClusterDB struct {
	Port int
	CA   string
}

// PostgresSection is the [postgres] table (Epic 36 story 36.1): the Postgres
// bundled with Maktaba.app, run by the supervisor as the postgres role.
type PostgresSection struct {
	// Enabled: "auto" (default: on a Mac whose payload carries Postgres, and on
	// any system when the server runs from a desktop app's payload: the Windows
	// app (Epic 38 story 38.10), the Linux app (Epic 39 story 39.4); when
	// [database] url is not set and no SQLite database is in use), "on" or "off".
	Enabled string `toml:"enabled"`
	// Home is the Postgres install (bin/postgres, bin/initdb, …); empty:
	// $MAKTABA_POSTGRES_HOME, else the payload's postgres/ beside bin/.
	Home string `toml:"home"`
	// Port is its port (default 54329).
	Port int `toml:"port"`
	// Listen: "lan" (default) also listens on this machine's network for the
	// cluster's machines while one may connect (one approved, or joins open:
	// pgbundle.WatchReach), and only their roles get in, over TLS; "loopback": this
	// machine only (no machine can join).
	Listen string `toml:"listen"`
	// TLS: "on" (default) encrypts the machines' connections with the cluster
	// database CA's certificate (made automatically); "off" sends them in the clear,
	// passwords and every replicated row readable on the network.
	TLS string `toml:"tls"`
	// MachinesFrom: where the cluster's machines may connect from: "samenet" (default:
	// the networks this machine is on, and each approved machine's own address, so a
	// machine over a VPN gets in too), "all" (any address), or networks, comma-separated
	// CIDR ("192.168.1.0/24, 10.8.0.0/16": those only).
	MachinesFrom string `toml:"machines_from"`
	// CopyPort is the loopback port of a full node's copy of its home's database
	// (default: Port; a node runs no bundled home, and its copy is the database it
	// would run as the home).
	CopyPort int `toml:"copy_port"`
	// UpdateDumps is how many dumps taken before an update's migrations
	// (backups/pre-<old>-to-<new>.dump, Epic 38 story 38.12) stay in backups/:
	// the newest ones, the last update's always; 0 (default): 3. The nightly
	// dumps are kept apart (the newest 7).
	UpdateDumps int `toml:"update_dumps"`
}

// DefaultUpdateDumps is how many pre-update dumps stay when [postgres]
// update_dumps is not set: the way back from each of the last three updates.
const DefaultUpdateDumps = 3

// KeepUpdateDumps is [postgres] update_dumps, DefaultUpdateDumps when it is not
// set (or not above 0).
func (p PostgresSection) KeepUpdateDumps() int {
	if p.UpdateDumps > 0 {
		return p.UpdateDumps
	}
	return DefaultUpdateDumps
}

// LLMSection is the [llm] table: the local language model server every LLM stage
// of the pipeline uses (check-tags, check-clips, enrich, display titles, the
// cluster's LLM door). Maktaba.app bundles Ollama; the supervisor runs it as the
// ollama role on a loopback port of its own, its models under <state>/models/ollama.
type LLMSection struct {
	// Enabled: "auto" (default: when the app's payload carries Ollama, or [llm]
	// binary / $MAKTABA_OLLAMA_BIN names one), "on" or "off".
	Enabled string `toml:"enabled"`
	// Binary is the ollama executable; empty: $MAKTABA_OLLAMA_BIN, else the
	// payload's ollama/ollama.
	Binary string `toml:"binary"`
	// Port is the bundled server's loopback port; 0 (default): 11533, or a free
	// port when another program holds it (a user's own Ollama keeps 11434).
	Port int `toml:"port"`
	// URL is an Ollama server elsewhere for every LLM stage (the bundled one then
	// does not run), e.g. "http://192.168.1.30:11434". A model server set in the
	// pipeline's rules (Settings → Tag check → Model server) still wins.
	URL string `toml:"url"`
	// Models are the models the server keeps (setup and the supervisor download
	// a missing one); empty: ["qwen2.5:7b"], the tag check's default.
	Models []string `toml:"models"`
	// Pull: "auto" (default) has the supervisor download a missing model in the
	// background; "off" leaves it to setup and `maktaba-server models llm`.
	Pull string `toml:"pull"`

	// RuntimeURL is what serve hands every role as MAKTABA_OLLAMA_URL: the
	// bundled server's address, or URL. Never in the file.
	RuntimeURL string `toml:"-"`
}

// PipelineSection is the [pipeline] table (Epic 35 story 35.8): how many pipeline
// worker processes this machine runs (a converted worker-only node keeps its
// installer's --instances N here).
type PipelineSection struct {
	Instances int `toml:"instances"`
	// Python is the interpreter of the pipeline (a converted worker keeps its own
	// environment, setup --node --python); empty: the release's venv, else python3.
	// MAKTABA_PYTHON still wins.
	Python string `toml:"python"`
	// IndexWorker: "on" (default) runs the search index (the index and chapter_infer
	// stages and the semantic search) in a worker of its own that never reads the
	// media share, so a share that stalls the main worker can no longer get the index
	// killed mid-write; "off" keeps it in the main worker, as before.
	IndexWorker string `toml:"index_worker"`
}

// IndexWorkerOn reports whether the index runs in its own worker (anything but
// off/false/no/0).
func (p PipelineSection) IndexWorkerOn() bool {
	switch strings.ToLower(strings.TrimSpace(p.IndexWorker)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// TelemetrySection is the [telemetry] table (Epic 16 stories 16.5, 16.7): whether
// anonymous usage data may leave this server for Maktaba Cloud at all. On, the
// consents decide (the admin's switch for the server's own report, each app's
// user for that app's batches; both off by default).
type TelemetrySection struct {
	// Enabled: "on" (default) lets the consents decide; "off" sends nothing,
	// the server's data nor any app's (the API's MAKTABA_TELEMETRY_ENABLED).
	Enabled string `toml:"enabled"`
}

// On reports whether usage data may leave the server (anything but off/false/no/0).
func (t TelemetrySection) On() bool {
	switch strings.ToLower(strings.TrimSpace(t.Enabled)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// TVSection is the [tv] table (Epic 33 story 33.1).
type TVSection struct {
	// Slots: "on" (default) runs the channel engine in TV slot processes behind
	// the streaming role's front door (live slot + hot standby); "off" keeps it
	// in the streaming process, as before Epic 33.
	Slots string `toml:"slots"`
}

// SlotsOn reports whether TV slots are on (anything but off/false/no/0).
func (t TVSection) SlotsOn() bool {
	switch strings.ToLower(strings.TrimSpace(t.Slots)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// ServerSection controls listen addresses and on-disk state.
type ServerSection struct {
	Listen     string `toml:"listen"`      // public API bind, e.g. "0.0.0.0:8080"
	DataDir    string `toml:"data_dir"`    // writable state root
	WebListen  string `toml:"web_listen"`  // embedded SPA bind, e.g. "0.0.0.0:8088"
	AdminAddr  string `toml:"admin_addr"`  // api admin/metrics bind, e.g. ":9100"
	StreamAddr string `toml:"stream_addr"` // streaming public bind, e.g. ":8081"
	// PublicURL is how other devices reach the streaming service, e.g.
	// "http://192.168.1.20:8081" (Plex's HDHomeRun address, XMLTV/M3U
	// links). Empty derives it from stream_addr + the LAN IP.
	PublicURL string `toml:"public_url"`
	// WebPublicURL is how other devices reach this machine's web UI (Epic 35 story
	// 35.11), e.g. "http://192.168.1.21:8088". Empty derives it from web_listen and
	// the host of public_url (or the LAN IP).
	WebPublicURL string `toml:"web_public_url"`
	// Role is "auto" (default: a joined node config makes this machine a full node,
	// Epic 35 story 35.8), "home" (never a node, even with a node config) or "node".
	Role string `toml:"role"`
	// MDNS: "on" (default) advertises this machine on the LAN over multicast DNS
	// (Epic 35 story 35.11); "off" does not, as `serve --no-mdns` (an install moved
	// into the app keeps its flag here, Epic 38 story 38.13).
	MDNS string `toml:"mdns"`
	// The intra-host ports of the roles (defaults 127.0.0.1:9050, :9101,
	// 127.0.0.1:9090, 127.0.0.1:9102, and the index worker's 127.0.0.1:9093 and
	// 127.0.0.1:9104); set them to run two machines on one host.
	StreamingGRPCAddr  string `toml:"streaming_grpc_addr"`
	StreamingAdminAddr string `toml:"streaming_admin_addr"`
	PipelineGRPCAddr   string `toml:"pipeline_grpc_addr"`
	PipelineLogsAddr   string `toml:"pipeline_logs_addr"`
	// the index worker's semantic search (gRPC) and its logs and liveness port
	PipelineSearchAddr    string `toml:"pipeline_search_addr"`
	PipelineIndexLogsAddr string `toml:"pipeline_index_logs_addr"`
}

// MDNSOn reports whether the machine advertises itself (anything but
// off/false/no/0).
func (s ServerSection) MDNSOn() bool {
	switch strings.ToLower(strings.TrimSpace(s.MDNS)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// DatabaseSection selects the storage backend via a single DSN. The
// scheme (postgres:// vs sqlite://) picks the driver — see internal/dsn.
type DatabaseSection struct {
	URL string `toml:"url"`
}

// MediaSection lists the library roots scanned for media.
type MediaSection struct {
	Roots []string `toml:"roots"`
	// PathMap maps the paths the database stores to this machine's, in the
	// workers' --path-map form ("/Volumes/Originals/=/Users/me/nas/", pairs
	// separated by ";"): MAKTABA_PATH_MAP for every role, MAKTABA_WORKER_PATH_MAP
	// for the pipeline.
	PathMap string `toml:"path_map"`
	// FFmpeg and FFprobe are the programs every role runs (a path, or a name on
	// the PATH); empty: $MAKTABA_FFMPEG / $MAKTABA_FFPROBE, else the desktop app's
	// own (Mac, Windows), else the PATH's (Linux: the distribution's, Epic 39). An
	// empty FFprobe is the one beside FFmpeg first. Setup's check, `maktaba-server
	// ffmpeg` and the System health page say whether they do what Maktaba needs.
	FFmpeg  string `toml:"ffmpeg"`
	FFprobe string `toml:"ffprobe"`
	// TranscodeMinFreeGiB is the free space on the streaming cache's disk below
	// which no video transcode starts (a transcode keeps its segments until its
	// session closes) and the transcodes kept for closed sessions' viewers go,
	// the longest unwatched first: that many GiB; 0 (default): the smaller of
	// 20 GiB and a tenth of the disk, at least 3 GiB; below 0: none. The
	// streaming role's MAKTABA_TRANSCODE_MIN_FREE_GIB.
	TranscodeMinFreeGiB int `toml:"transcode_min_free_gib"`
}

// TranscriptionSection is the [transcription] table: what the pipeline's
// transcribe stage loads, and (Epic 36 story 36.4, story 38.8) how many
// transcription workers this machine runs and how much each may take.
type TranscriptionSection struct {
	// Backend is kept for older files: the pipeline picks its engine by itself,
	// MLX on Apple Silicon, CTranslate2 (faster-whisper) on Windows and Linux.
	Backend string `toml:"backend"`
	// Model is the Whisper model (a Hugging Face repo or a folder,
	// MAKTABA_STT_MODEL); empty, or an old short name ("large-v3"): the
	// pipeline's default, whisper-large-v3-turbo in the engine's format
	// (mlx-community/whisper-large-v3-turbo on Apple Silicon,
	// dropbox-dash/faster-whisper-large-v3-turbo elsewhere). On Windows and Linux
	// an MLX model is replaced by the CTranslate2 one, with a line in the log.
	Model string `toml:"model"`
	// Refine: "on" (default) lets a second listen correct Whisper's words
	// (MAKTABA_STT_REFINE); "off" stores them as they are. On Windows and Linux it
	// runs on the NVIDIA GPU beside Whisper (story 38.9), when the GPU holds both:
	// 10 GB and up on Windows, 8 GB and up elsewhere, and only while it has the
	// room free; the GPU's tiers then give Whisper a smaller batch to leave it that
	// room. Without such a GPU a transcript keeps Whisper's words and says why.
	Refine string `toml:"refine"`
	// RefineModel is the second listen's model (MAKTABA_STT_REFINE_MODEL);
	// empty: CohereLabs/cohere-transcribe-arabic-07-2026.
	RefineModel string `toml:"refine_model"`
	// Workers is the number of transcription workers beside the main worker:
	// 0 (default) sizes it from the RAM on Apple Silicon and from the GPU's
	// memory on a machine with an NVIDIA GPU of 4 GB or more, and runs none on a
	// machine that transcribes on the CPU (the main worker does); -1 none.
	// [pipeline] instances, when set, wins. While the second listen is on (refine),
	// a machine that cannot run it (a Windows or Linux one without an NVIDIA GPU
	// that does, or without its model) plans none: its main worker transcribes
	// only when the home's rules let it (Settings, Transcription). More
	// workers than planned share the plan's GPU memory, CPU threads and audio
	// hours. None at all when the operator's environment excludes transcribe
	// (MAKTABA_PIPELINE_EXCLUDE_STAGES) or the pipeline has no stt extra.
	Workers int `toml:"workers"`
	// Per transcription worker; 0: sized from the RAM (MAKTABA_STT_AUDIO_HOURS:
	// the hours of audio it holds at once; _MIN_FREE_PCT: below this much free
	// memory a job waits).
	AudioHours float64 `toml:"audio_hours"`
	MinFreePct float64 `toml:"min_free_pct"`
	// Apple Silicon only (MLX), per worker; 0: sized from the RAM. GPUGB caps
	// MLX's GPU memory (MAKTABA_STT_GPU_GB), GPUCacheGB its cache of freed
	// buffers (MAKTABA_STT_GPU_CACHE_GB).
	GPUGB      float64 `toml:"gpu_gb"`
	GPUCacheGB float64 `toml:"gpu_cache_gb"`
	// Batch is how many files a worker decodes together, one 30 s window of each
	// per GPU pass (MAKTABA_STT_BATCH); 0: sized from the NVIDIA GPU's memory (and
	// smaller where the second listen shares the GPU), 4 on the CPU, the
	// pipeline's 32 on Apple Silicon. Larger batches are faster until the GPU's
	// memory runs out.
	Batch int `toml:"batch"`
	// Device is where Windows and Linux transcribe (MAKTABA_STT_DEVICE): "auto"
	// (default: the NVIDIA GPU when CUDA works there, else the CPU), "cuda" (or
	// "cuda:1", the second GPU; a worker that cannot use it says why and
	// transcribes nothing), or "cpu". Apple Silicon always uses its GPU.
	Device string `toml:"device"`
	// ComputeType is the precision CTranslate2 runs Whisper in on Windows and
	// Linux (MAKTABA_STT_COMPUTE_TYPE): "auto" (default: float16 on the GPU,
	// int8_float16 on a GPU under 7 GB, or on an 8 GB one beside the second listen
	// outside Windows, int8 on the CPU), or one of float16,
	// int8_float16, int8, int8_float32, float32. One the device cannot run falls
	// back to its default, with a line in the log.
	ComputeType string `toml:"compute_type"`
	// CPUThreads is how many threads CTranslate2 uses when it transcribes on the
	// CPU (MAKTABA_STT_CPU_THREADS); 0: half the logical CPUs (2 to 16), the
	// rest left to streaming and the other stages.
	CPUThreads int `toml:"cpu_threads"`
}

// CloudSection is the cloud relay (Epic 25): while enabled, the home's supervisor runs
// the relay agent (maktaba-cloudlink), which keeps a tunnel from the cloud to this home's
// API. Its credentials come from a claim once (ClaimCode and Slug, used on the first run
// only) and are kept in the keys directory; a switch of the home moves them, and the
// agent with them (specs/epics/37-node-replica).
type CloudSection struct {
	Enabled   bool   `toml:"enabled" json:"enabled"`
	ClaimCode string `toml:"claim_code" json:"-"`
	// Slug is the subdomain asked for at the claim; Name the server's label there.
	Slug string `toml:"slug" json:"slug,omitempty"`
	Name string `toml:"name" json:"name,omitempty"`
	// API and RelayWS override the cloud's addresses (default: maktaba.app's).
	API     string `toml:"api" json:"api,omitempty"`
	RelayWS string `toml:"relay_ws" json:"relay_ws,omitempty"`
	// AdminAddr is the agent's loopback status address (default 127.0.0.1:8765).
	AdminAddr string `toml:"admin_addr" json:"admin_addr,omitempty"`
}

// Defaults returns a Config populated with the documented defaults for
// a fresh single-host install. The data dir is platform-resolved so the
// SQLite database and downloaded models land somewhere writable.
func Defaults() Config {
	data := defaultDataDir()
	return Config{
		Server: ServerSection{
			Listen:     "0.0.0.0:8080",
			DataDir:    data,
			WebListen:  "0.0.0.0:8088",
			AdminAddr:  "127.0.0.1:9100", // logs and metrics: this machine only
			StreamAddr: ":8081",
		},
		Database: DatabaseSection{
			URL: "sqlite://" + filepath.Join(data, "maktaba.db"),
		},
		Media: MediaSection{Roots: []string{}},
		Transcription: TranscriptionSection{
			Backend: defaultTranscriptionBackend(),
		},
		Cloud:      CloudSection{Enabled: false},
		Supervisor: DefaultSupervisor(),
	}
}

// Path returns the resolved server.toml path, honouring $MAKTABA_CONFIG.
func Path() string {
	if p := strings.TrimSpace(os.Getenv("MAKTABA_CONFIG")); p != "" {
		return p
	}
	// a server its launcher started: the install's own word on where its server.toml is
	// (setup wrote it; a Windows task or service carries no MAKTABA_CONFIG)
	if root := strings.TrimSpace(os.Getenv(layout.EnvInstallRoot)); root != "" {
		if p := layout.ReadConfigPointer(root); p != "" {
			return p
		}
	}
	return filepath.Join(configDir(), "server.toml")
}

// Load reads server.toml from Path(), layering it over Defaults() so a
// partial file still yields a complete Config. A missing file is NOT an
// error — it returns Defaults() with found=false so callers (e.g. the
// setup wizard) can distinguish "first run" from "configured".
func Load() (cfg Config, found bool, err error) {
	return LoadFrom(Path())
}

// LoadFrom is Load against an explicit path (used by tests).
func LoadFrom(path string) (Config, bool, error) {
	cfg := Defaults()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, false, nil
		}
		return cfg, false, fmt.Errorf("read %s: %w", path, err)
	}
	// Decode over the defaults so unset keys keep their default value.
	meta, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return cfg, true, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.DatabaseURLSet = meta.IsDefined("database", "url") && strings.TrimSpace(cfg.Database.URL) != ""
	cfg.PipelineInstancesSet = meta.IsDefined("pipeline", "instances")
	if !cfg.DatabaseURLSet {
		cfg.Database.URL = Defaults().Database.URL
	}
	// the api's admin port (logs, metrics: no sign-in) on every interface was the default
	// setup wrote into server.toml: it is this machine's only now (anything else an
	// operator chose stays)
	if cfg.Server.AdminAddr == legacyAdminAddr {
		cfg.Server.AdminAddr = Defaults().Server.AdminAddr
	}
	return cfg, true, nil
}

// legacyAdminAddr is the api admin port's old default, on every interface.
const legacyAdminAddr = ":9100"

// Save atomically writes cfg to Path(), creating the parent directory.
func (c Config) Save() error {
	return c.SaveTo(Path())
}

// SaveTo writes cfg to an explicit path, atomically (write-temp +
// rename) so a crash mid-write can't truncate a live config. The keys of
// the file there that Config does not know (a newer release's) are kept.
func (c Config) SaveTo(path string) error {
	body, err := c.fileTOML()
	if err != nil {
		return err
	}
	if body, err = keepUnknownKeys(path, body); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server.toml.*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	// 0600: the file may carry DB credentials / cloud claim codes.
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// fileTOML is c as server.toml, without the settings Load tells apart by
// whether the file sets them: [database] url unless DatabaseURLSet, and
// [pipeline] instances while 0 and not PipelineInstancesSet. Written, the
// next Load would take the default as set (a default SQLite url keeps the
// app's bundled Postgres from running; instances = 0 means no transcription
// workers).
func (c Config) fileTOML() ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	var file map[string]any
	if _, err := toml.Decode(buf.String(), &file); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	unset := func(table, key string) {
		if t, ok := file[table].(map[string]any); ok {
			delete(t, key)
			if len(t) == 0 {
				delete(file, table)
			}
		}
	}
	if !c.DatabaseURLSet || strings.TrimSpace(c.Database.URL) == "" {
		unset("database", "url")
	}
	if !c.PipelineInstancesSet && c.Pipeline.Instances == 0 {
		unset("pipeline", "instances")
	}
	// [backups] with every key at its default is no table at all
	if t, ok := file["backups"].(map[string]any); ok && len(t) == 0 {
		delete(file, "backups")
	}
	buf.Reset()
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	return buf.Bytes(), nil
}

// keepUnknownKeys adds to body the keys of the file at path that Config does not
// decode. A missing or unreadable file adds nothing: the save goes ahead.
func keepUnknownKeys(path string, body []byte) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return body, nil
	}
	var probe Config
	meta, err := toml.Decode(string(raw), &probe)
	if err != nil || len(meta.Undecoded()) == 0 {
		return body, nil
	}
	var old, out map[string]any
	if _, err := toml.Decode(string(raw), &old); err != nil {
		return body, nil
	}
	if _, err := toml.Decode(string(body), &out); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	for _, key := range meta.Undecoded() {
		v, ok := lookupKey(old, key)
		if !ok {
			continue
		}
		dst := out
		for _, part := range key[:len(key)-1] {
			next, ok := dst[part].(map[string]any)
			if !ok {
				if _, taken := dst[part]; taken {
					dst = nil
					break
				}
				next = map[string]any{}
				dst[part] = next
			}
			dst = next
		}
		if dst != nil {
			if _, taken := dst[key[len(key)-1]]; !taken {
				dst[key[len(key)-1]] = v
			}
		}
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(out); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	return buf.Bytes(), nil
}

// lookupKey is the value at key in a decoded file (tables only: a key inside an
// array of tables is not looked up).
func lookupKey(m map[string]any, key toml.Key) (any, bool) {
	var cur any = m
	for _, part := range key {
		t, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = t[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// configDir resolves the platform config directory (parent of
// server.toml, paths.go).
func configDir() string { return LocalDirs().Config }

// defaultDataDir resolves a writable state root for the SQLite DB,
// downloaded models, and caches.
func defaultDataDir() string { return LocalDirs().Data }

// defaultTranscriptionBackend picks the sensible default for the host:
// MLX on Apple Silicon, faster-whisper everywhere else.
func defaultTranscriptionBackend() string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return "mlx-whisper"
	}
	return "faster-whisper"
}

func home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}
