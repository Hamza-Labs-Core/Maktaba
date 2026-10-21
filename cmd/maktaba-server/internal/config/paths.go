package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dirs is where an install keeps its files when nothing overrides them (Epic 36
// story 36.2):
//
//	macOS   : ~/Library/Application Support/Maktaba (server.toml, keys/, data/,
//	          models/, chroma/, postgres/, cache/, backups/), logs in ~/Library/Logs/Maktaba
//	Linux   : $XDG_CONFIG_HOME/maktaba, $XDG_DATA_HOME/maktaba, $XDG_STATE_HOME/maktaba/logs
//	Windows : %APPDATA%\Maktaba, %LOCALAPPDATA%\Maktaba\data, %LOCALAPPDATA%\Maktaba\logs
type Dirs struct {
	Config string // holds server.toml
	Data   string // the default [server] data_dir
	Logs   string
	Keys   string // secrets.key, stream-signing.key
}

// DirsFor resolves Dirs for an OS (tests pass their own environment and home).
func DirsFor(getenv func(string) string, goos, userHome string) Dirs {
	switch goos {
	case "darwin":
		root := filepath.Join(userHome, "Library", "Application Support", "Maktaba")
		return Dirs{
			Config: root,
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(userHome, "Library", "Logs", "Maktaba"),
			Keys:   filepath.Join(root, "keys"),
		}
	case "windows":
		roaming := getenv("APPDATA")
		if roaming == "" {
			roaming = filepath.Join(userHome, "AppData", "Roaming")
		}
		local := getenv("LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(userHome, "AppData", "Local")
		}
		cfg := filepath.Join(roaming, "Maktaba")
		return Dirs{
			Config: cfg,
			Data:   filepath.Join(local, "Maktaba", "data"),
			Logs:   filepath.Join(local, "Maktaba", "logs"),
			Keys:   cfg,
		}
	}
	return xdgDirs(getenv, userHome)
}

// xdgDirs is the XDG layout (Linux, and an install a Mac made before Epic 36).
func xdgDirs(getenv func(string) string, userHome string) Dirs {
	base := func(env, def string) string {
		if v := getenv(env); v != "" {
			return v
		}
		return filepath.Join(userHome, def)
	}
	cfg := filepath.Join(base("XDG_CONFIG_HOME", ".config"), "maktaba")
	return Dirs{
		Config: cfg,
		Data:   filepath.Join(base("XDG_DATA_HOME", filepath.Join(".local", "share")), "maktaba"),
		Logs:   filepath.Join(base("XDG_STATE_HOME", filepath.Join(".local", "state")), "maktaba", "logs"),
		Keys:   cfg,
	}
}

// LocalDirs is DirsFor this process.
func LocalDirs() Dirs { return DirsFor(os.Getenv, runtime.GOOS, home()) }

// KeysDir is where the home's key files live: beside server.toml when
// $MAKTABA_CONFIG names it (tests, containers), else the platform's keys dir.
func KeysDir() string {
	if strings.TrimSpace(os.Getenv("MAKTABA_CONFIG")) != "" {
		return filepath.Dir(Path())
	}
	return LocalDirs().Keys
}

// LogsDir is where the server's logs go: $MAKTABA_LOG_DIR, else the platform's.
func LogsDir() string {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_LOG_DIR")); v != "" {
		return v
	}
	return LocalDirs().Logs
}

// StateDir is the root of the install's large state (models/, chroma/, cache/,
// postgres/, backups/). On macOS the default data_dir sits inside that root
// (~/Library/Application Support/Maktaba/data), so the root is its parent;
// everywhere else, and for any data_dir the owner chose, it is data_dir itself
// (where these always lived on Linux and Windows).
func (c Config) StateDir() string { return stateDir(runtime.GOOS, c.Server.DataDir, LocalDirs().Data) }

func stateDir(goos, dataDir, defaultData string) string {
	if goos == "darwin" && dataDir != "" && filepath.Clean(dataDir) == filepath.Clean(defaultData) {
		return filepath.Dir(filepath.Clean(dataDir))
	}
	return dataDir
}
