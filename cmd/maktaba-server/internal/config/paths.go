package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Dirs is where an install keeps its files when nothing overrides them (Epic 36
// story 36.2, Epic 38 story 38.1):
//
//	macOS   : ~/Library/Application Support/Maktaba (server.toml, keys/, data/,
//	          models/, chroma/, postgres/, cache/, backups/), logs in ~/Library/Logs/Maktaba
//	Linux   : $XDG_CONFIG_HOME/maktaba, $XDG_DATA_HOME/maktaba, $XDG_STATE_HOME/maktaba/logs
//	Windows : %LOCALAPPDATA%\Maktaba (server.toml, keys\, data\, logs\, models\, chroma\,
//	          postgres\, cache\, backups\): nothing in the roaming profile, so the keys and
//	          the node's identity never follow the user to another PC (an install from
//	          before keeps its originals in %APPDATA%\Maktaba, copied by
//	          MigrateWindowsDirs, and its large state in data\, StateDir)
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
		root := WindowsRoot(getenv, userHome)
		return Dirs{
			Config: root,
			Data:   filepath.Join(root, "data"),
			Logs:   filepath.Join(root, "logs"),
			Keys:   filepath.Join(root, "keys"),
		}
	}
	return xdgDirs(getenv, userHome)
}

// WindowsRoot is %LOCALAPPDATA%\Maktaba: the install's home on Windows (the app's
// program and every per-user file, Epic 38).
func WindowsRoot(getenv func(string) string, userHome string) string {
	local := getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(userHome, "AppData", "Local")
	}
	return filepath.Join(local, "Maktaba")
}

// legacyWindowsDirs is the Windows layout before Epic 38: server.toml and the keys
// in the roaming profile (%APPDATA%\Maktaba), the data and logs already local.
func legacyWindowsDirs(getenv func(string) string, userHome string) Dirs {
	roaming := getenv("APPDATA")
	if roaming == "" {
		roaming = filepath.Join(userHome, "AppData", "Roaming")
	}
	cfg := filepath.Join(roaming, "Maktaba")
	root := WindowsRoot(getenv, userHome)
	return Dirs{
		Config: cfg,
		Data:   filepath.Join(root, "data"),
		Logs:   filepath.Join(root, "logs"),
		Keys:   cfg,
	}
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
// postgres/, backups/). On macOS and Windows the default data_dir sits inside that
// root (~/Library/Application Support/Maktaba/data, %LOCALAPPDATA%\Maktaba\data), so
// the root is its parent; on Linux, and for any data_dir the owner chose, it is
// data_dir itself (where these always lived on Linux). A Windows install from
// before Epic 38 keeps them in its default data_dir, where its older versions (and
// a Postgres of its own that may be running) use them: outside the app's payload
// (InAppPayload), a default data_dir that holds one of them is still the state
// root. Nothing moves or copies them (story 38.1).
func (c Config) StateDir() string {
	exe, _ := os.Executable()
	return stateDir(runtime.GOOS, c.Server.DataDir, LocalDirs().Data, InAppPayload(exe))
}

func stateDir(goos, dataDir, defaultData string, inApp bool) string {
	if (goos == "darwin" || goos == "windows") && dataDir != "" && filepath.Clean(dataDir) == filepath.Clean(defaultData) {
		if goos == "windows" && !inApp && holdsState(dataDir) {
			return dataDir
		}
		return filepath.Dir(filepath.Clean(dataDir))
	}
	return dataDir
}

// holdsState: dir holds one of the large state's folders (stateFolders).
func holdsState(dir string) bool {
	for _, d := range stateFolders {
		if exists(filepath.Join(dir, d)) {
			return true
		}
	}
	return false
}
