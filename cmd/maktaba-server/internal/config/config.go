// Package config is the single human-facing configuration surface for
// maktaba-server. It loads/saves server.toml and translates it into the
// environment each underlying service already consumes, so the role
// binaries need zero changes to be driven from one config file.
//
// Path resolution follows the platform conventions the spec calls out:
//
//	Linux/macOS : $XDG_CONFIG_HOME/maktaba/server.toml
//	              (fallback ~/.config/maktaba/server.toml)
//	Windows     : %APPDATA%\Maktaba\server.toml
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
}

// ServerSection controls listen addresses and on-disk state.
type ServerSection struct {
	Listen     string `toml:"listen"`      // public API bind, e.g. "0.0.0.0:8080"
	DataDir    string `toml:"data_dir"`    // writable state root
	WebListen  string `toml:"web_listen"`  // embedded SPA bind, e.g. "0.0.0.0:8088"
	AdminAddr  string `toml:"admin_addr"`  // api admin/metrics bind, e.g. ":9100"
	StreamAddr string `toml:"stream_addr"` // streaming public bind, e.g. ":8081"
}

// DatabaseSection selects the storage backend via a single DSN. The
// scheme (postgres:// vs sqlite://) picks the driver — see internal/dsn.
type DatabaseSection struct {
	URL string `toml:"url"`
}

// MediaSection lists the library roots scanned for media.
type MediaSection struct {
	Roots []string `toml:"roots"`
}

// TranscriptionSection selects the Whisper backend + model.
type TranscriptionSection struct {
	Backend string `toml:"backend"` // "mlx-whisper" | "faster-whisper" | "openai"
	Model   string `toml:"model"`   // e.g. "large-v3"
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
			Model:   "large-v3",
		},
		Cloud: CloudSection{Enabled: false},
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
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return cfg, true, fmt.Errorf("parse %s: %w", path, err)
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
// server.toml).
func configDir() string {
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Maktaba")
		}
		return filepath.Join(home(), "AppData", "Roaming", "Maktaba")
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "maktaba")
		}
		return filepath.Join(home(), ".config", "maktaba")
	}
}

// defaultDataDir resolves a writable state root for the SQLite DB,
// downloaded models, and caches.
func defaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			return filepath.Join(appData, "Maktaba", "data")
		}
		return filepath.Join(home(), "AppData", "Local", "Maktaba", "data")
	default:
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "maktaba")
		}
		return filepath.Join(home(), ".local", "share", "maktaba")
	}
}

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
