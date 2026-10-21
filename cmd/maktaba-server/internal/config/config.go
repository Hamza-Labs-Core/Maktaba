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
//	          moved once by MigrateWindowsDirs)
//
// $MAKTABA_CONFIG overrides the resolved path everywhere (tests, custom
// installs, container bind-mounts).
package config

import (
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

	// DatabaseURLSet: server.toml sets [database] url (Load). Unset, a Mac with
	// the bundled Postgres uses it (Epic 36 story 36.1).
	DatabaseURLSet bool `toml:"-"`
	// PipelineInstancesSet: server.toml sets [pipeline] instances (0 there means
	// no transcription workers, even on Apple Silicon).
	PipelineInstancesSet bool `toml:"-"`
}

// PostgresSection is the [postgres] table (Epic 36 story 36.1): the Postgres
// bundled with Maktaba.app, run by the supervisor as the postgres role.
type PostgresSection struct {
	// Enabled: "auto" (default: on a Mac whose payload carries Postgres, when
	// [database] url is not set and no SQLite database is in use), "on" or "off".
	Enabled string `toml:"enabled"`
	// Home is the Postgres install (bin/postgres, bin/initdb, …); empty:
	// $MAKTABA_POSTGRES_HOME, else the payload's postgres/ beside bin/.
	Home string `toml:"home"`
	// Port is its loopback port (default 54329).
	Port int `toml:"port"`
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
	// MDNS: "on" (default) advertises this machine on the LAN over multicast DNS
	// (Epic 35 story 35.11); "off" does not, as `serve --no-mdns` (an install moved
	// into the app keeps its flag here, Epic 38 story 38.13).
	MDNS string `toml:"mdns"`
	// The intra-host ports of the roles (defaults 127.0.0.1:9050, :9101,
	// 127.0.0.1:9090, 127.0.0.1:9102); set them to run two machines on one host.
	StreamingGRPCAddr  string `toml:"streaming_grpc_addr"`
	StreamingAdminAddr string `toml:"streaming_admin_addr"`
	PipelineGRPCAddr   string `toml:"pipeline_grpc_addr"`
	PipelineLogsAddr   string `toml:"pipeline_logs_addr"`
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
	// workers' --path-map form ("/Volumes/Originals/=/Users/me/nas/", comma
	// separated): MAKTABA_PATH_MAP for every role, MAKTABA_WORKER_PATH_MAP for
	// the pipeline.
	PathMap string `toml:"path_map"`
}

// TranscriptionSection is the [transcription] table: what the pipeline's
// transcribe stage loads, and (Epic 36 story 36.4) how many transcription
// workers this machine runs and how much memory each may take.
type TranscriptionSection struct {
	// Backend is kept for older files: the pipeline picks MLX (Apple Silicon)
	// or torch by itself.
	Backend string `toml:"backend"`
	// Model is the Whisper model (a Hugging Face repo, MAKTABA_STT_MODEL);
	// empty, or an old short name ("large-v3"): the pipeline's default
	// (mlx-community/whisper-large-v3-turbo).
	Model string `toml:"model"`
	// Refine: "on" (default) lets a second listen correct Whisper's words
	// (MAKTABA_STT_REFINE); "off" stores them as they are.
	Refine string `toml:"refine"`
	// RefineModel is the second listen's model (MAKTABA_STT_REFINE_MODEL);
	// empty: CohereLabs/cohere-transcribe-arabic-07-2026.
	RefineModel string `toml:"refine_model"`
	// Workers is the number of transcription workers beside the main worker:
	// 0 (default) sizes it from the RAM on Apple Silicon and runs none
	// elsewhere (the one worker transcribes); -1 none. [pipeline] instances,
	// when set, wins.
	Workers int `toml:"workers"`
	// Per transcription worker; 0: sized from the RAM (MAKTABA_STT_AUDIO_HOURS,
	// _GPU_GB, _GPU_CACHE_GB, _MIN_FREE_PCT).
	AudioHours float64 `toml:"audio_hours"`
	GPUGB      float64 `toml:"gpu_gb"`
	GPUCacheGB float64 `toml:"gpu_cache_gb"`
	MinFreePct float64 `toml:"min_free_pct"`
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server.toml.*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	enc := toml.NewEncoder(tmp)
	if err := enc.Encode(c); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode toml: %w", err)
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
