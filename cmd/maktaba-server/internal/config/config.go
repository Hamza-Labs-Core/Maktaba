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

	// DatabaseURLSet: server.toml sets [database] url (Load), or a caller chose
	// one to keep (setup); Save writes the url only then. Unset, a Mac with the
	// bundled Postgres uses it (Epic 36 story 36.1).
	DatabaseURLSet bool `toml:"-"`
	// PipelineInstancesSet: server.toml sets [pipeline] instances, or a caller
	// chose a count to keep (setup --node); Save writes a 0 only then (0 there
	// means no transcription workers, even on Apple Silicon).
	PipelineInstancesSet bool `toml:"-"`
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
	// Port is its loopback port (default 54329).
	Port int `toml:"port"`
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
	// The intra-host ports of the roles (defaults 127.0.0.1:9050, :9101,
	// 127.0.0.1:9090, 127.0.0.1:9102); set them to run two machines on one host.
	StreamingGRPCAddr  string `toml:"streaming_grpc_addr"`
	StreamingAdminAddr string `toml:"streaming_admin_addr"`
	PipelineGRPCAddr   string `toml:"pipeline_grpc_addr"`
	PipelineLogsAddr   string `toml:"pipeline_logs_addr"`
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
	// workers' --path-map form ("/Volumes/Originals/=/Users/me/nas/", comma
	// separated): MAKTABA_PATH_MAP for every role, MAKTABA_WORKER_PATH_MAP for
	// the pipeline.
	PathMap string `toml:"path_map"`
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

// CloudSection holds optional cloud-link enrolment.
type CloudSection struct {
	Enabled   bool   `toml:"enabled"`
	ClaimCode string `toml:"claim_code"`
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
			AdminAddr:  ":9100",
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
	return cfg, true, nil
}

// Save atomically writes cfg to Path(), creating the parent directory.
func (c Config) Save() error {
	return c.SaveTo(Path())
}

// SaveTo writes cfg to an explicit path, atomically (write-temp +
// rename) so a crash mid-write can't truncate a live config.
func (c Config) SaveTo(path string) error {
	body, err := c.fileTOML()
	if err != nil {
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
	buf.Reset()
	if err := toml.NewEncoder(&buf).Encode(file); err != nil {
		return nil, fmt.Errorf("encode toml: %w", err)
	}
	return buf.Bytes(), nil
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
